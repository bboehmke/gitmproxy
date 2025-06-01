package main

import (
	"bufio"
	"encoding/hex"
	"hash/fnv"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/AdguardTeam/golibs/log"
)

// DiskCache represents an HTTP response cache that stores entries on the file system, grouped by hostname.
// It can enforce a maximum total disk usage (quota), a max response size for caching, and a cacheEntryTTL for cache entries.
type DiskCache struct {
	config Config

	currSize  atomic.Int64 // tracked current size, updated on set/delete
	sizeOnce  sync.Once
	sizeError error

	// Prevent concurrent downloads of the same cache key
	downloadMu sync.Mutex
	inflight   map[string]*sync.WaitGroup

	transport http.RoundTripper
}

// NewDiskCache creates a new DiskCache storing responses on the file system.
func NewDiskCache(config Config, transport http.RoundTripper) (*DiskCache, error) {
	if err := os.MkdirAll(config.CacheDir, 0755); err != nil {
		return nil, err
	}
	c := &DiskCache{
		config:    config,
		inflight:  make(map[string]*sync.WaitGroup),
		transport: transport,
	}

	// Initialize current size
	_ = filepath.Walk(c.config.CacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			panic(err)
		}
		if err == nil && !info.IsDir() {
			// clean up temporary files
			if strings.HasSuffix(info.Name(), ".tmp") {
				os.Remove(path)
			} else {
				c.currSize.Add(info.Size())
			}

		}
		return nil
	})

	return c, nil
}

// cachePath returns the full filesystem path for a request, grouping by hostname and using the first 4 chars of hash
// as an extra subdirectory, hash as file name.
func (c *DiskCache) cachePath(req *http.Request) (string, string, error) {
	// generate non-cryptographic hash of the request method and URL
	h := fnv.New128a()
	h.Write([]byte(req.Method))
	h.Write([]byte(req.URL.String()))
	key := hex.EncodeToString(h.Sum(nil))

	// build the path: hostname/key[:4]/key
	hostname := req.URL.Hostname()
	subdir := key[:4]
	return filepath.Join(c.config.CacheDir, hostname, subdir, key), key, nil
}

// Get returns a cached http.Response if present, else nil. Honors cacheEntryTTL if set.
func (c *DiskCache) Get(req *http.Request) (*http.Response, error) {
	path, _, err := c.cachePath(req)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, nil // cache miss
	}

	// cacheEntryTTL check: if configured, and file is too old, treat as miss (do not delete file)
	if c.config.EntryTTL > 0 {
		creationTime := info.ModTime()
		if time.Since(creationTime) > c.config.EntryTTL {
			if c.config.EnableLogging {
				log.Info("cache EXPIRED: %s (expired %v ago, cacheEntryTTL %v)", path, time.Since(creationTime), c.config.EntryTTL)
			}
			return nil, nil
		}
	}

	// Touch the file's atime to update LRU (best-effort), but do NOT update mtime!
	now := time.Now()
	_ = os.Chtimes(path, now, info.ModTime())

	f, err := os.Open(path)
	if err != nil {
		return nil, nil // treat as cache miss
	}
	resp, err := http.ReadResponse(bufio.NewReader(f), req)
	if err != nil {
		f.Close()
		return nil, err
	}
	resp.Body = &bodyWithFile{body: resp.Body, file: f}
	return resp, nil
}

// Set stores the HTTP response in the cache. Only stores status, headers, and body.
// No size or cacheEntryTTL check is performed here; size/ttl checks are handled in the transport and Get.
func (c *DiskCache) Set(req *http.Request, resp *http.Response) error {
	path, _, err := c.cachePath(req)
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Write response directly to temp file, count bytes written
	size, err := writeResponseToTmpFile(tmpPath, resp)
	if err != nil {
		return err
	}

	// Ensure quota: evict LRU files until enough space
	if c.config.MaxSize > 0 {
		for c.currSize.Load()+size > int64(c.config.MaxSize) {
			evicted, freed, err := c.evictOne()
			if err != nil {
				// can't evict, either error or nothing to evict
				break
			}
			if evicted {
				c.subSize(freed)
			} else {
				break
			}
		}
	}

	// Rename file to final location
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Update current size
	c.addSize(size)
	return nil
}

// addSize increases the current size of the cache by sz bytes, ensuring it does not exceed cacheMaxSize.
func (c *DiskCache) addSize(sz int64) {
	if c.config.MaxSize > 0 {
		c.currSize.Add(sz)
	}
}

// subSize reduces the current size of the cache by sz bytes, ensuring it does not go below zero.
func (c *DiskCache) subSize(sz int64) {
	if c.config.MaxSize > 0 {
		c.currSize.Add(-sz)
		if c.currSize.Load() < 0 {
			c.currSize.Store(0)
		}
	}
}

// evictOne removes the least-recently-used (oldest atime) cache file.
// Returns true, size of evicted file, and error.
// This implementation uses Linux-specific syscall.Stat_t for robust access time retrieval.
func (c *DiskCache) evictOne() (bool, int64, error) {
	var oldestPath string
	var oldestInfo os.FileInfo
	var oldestAtime time.Time

	err := filepath.Walk(c.config.CacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}

		// Use Atim for access time (Linux-specific)
		atime := time.Unix(stat.Atim.Sec, stat.Atim.Nsec)
		if oldestInfo == nil || atime.Before(oldestAtime) {
			oldestInfo = info
			oldestPath = path
			oldestAtime = atime
		}
		return nil
	})

	if err != nil {
		return false, 0, err
	}

	if oldestInfo == nil {
		return false, 0, nil
	}

	size := oldestInfo.Size()
	if err := os.Remove(oldestPath); err != nil {
		return false, 0, err
	}

	if c.config.EnableLogging {
		log.Printf("cache DELETE: %s", oldestPath)
	}
	return true, size, nil
}

// doSingleflightDownload performs the download, cache, and returns the response for a cache miss.
// It handles inflight map cleanup and wg.Done().
// If the response is too large to cache (by ContentLength), it is returned directly and not stored.
func (c *DiskCache) doSingleflightDownload(req *http.Request, inflightKey string, wg *sync.WaitGroup) (*http.Response, error) {
	defer func() {
		c.downloadMu.Lock()
		delete(c.inflight, inflightKey)
		wg.Done()
		c.downloadMu.Unlock()
	}()

	// Download the response body
	origResp, err := c.transport.RoundTrip(req)
	if err != nil || origResp == nil {
		return origResp, err
	}

	// Only check the limit if ContentLength is given (>= 0).
	if c.config.EntryMaxSize > 0 && origResp.ContentLength > int64(c.config.EntryMaxSize) && origResp.ContentLength >= 0 {
		if c.config.EnableLogging {
			log.Printf("response TOO LARGE to cache: %s %s (Content-Length: %d, Limit: %d)",
				req.Method, req.URL.String(), origResp.ContentLength, c.config.EntryMaxSize)
		}
		return origResp, nil
	}

	if c.config.EnableLogging {
		log.Printf("cache MISS: %s %s", req.Method, req.URL.String())
	}
	_ = c.Set(req, origResp)
	origResp.Body.Close()
	return c.Get(req)
}

// RoundTrip implements http.RoundTripper. Only GET requests are cached.
// If multiple requests for the same URL come in concurrently, only one will download the file.
func (c *DiskCache) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return c.transport.RoundTrip(req)
	}
	inflightKey := req.URL.String()

	for {
		resp, err := c.Get(req)
		if err != nil {
			return nil, err
		}
		if resp != nil {
			if c.config.EnableLogging {
				log.Printf("cache HIT: %s %s", req.Method, req.URL.String())
			}
			mCacheRequestsTotal.Inc()
			mCacheRequestsBytes.Add(float64(resp.ContentLength))
			mCacheRequestsHitTotal.Inc()
			mCacheRequestsHitBytes.Add(float64(resp.ContentLength))
			return resp, nil
		}

		c.downloadMu.Lock()
		if wg, ok := c.inflight[inflightKey]; ok {
			c.downloadMu.Unlock()
			wg.Wait()
			continue
		}
		wg := &sync.WaitGroup{}
		wg.Add(1)
		c.inflight[inflightKey] = wg
		c.downloadMu.Unlock()

		resp, err = c.doSingleflightDownload(req, inflightKey, wg)
		if err == nil {
			mCacheRequestsTotal.Inc()
			mCacheRequestsBytes.Add(float64(resp.ContentLength))
			mCacheRequestsMissTotal.Inc()
			mCacheRequestsMissBytes.Add(float64(resp.ContentLength))
		}
		return resp, err
	}
}
