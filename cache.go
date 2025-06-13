package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
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
	"github.com/dustin/go-humanize"
	"github.com/pquerna/cachecontrol/cacheobject"
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
func (c *DiskCache) cachePath(req *http.Request) string {
	// generate non-cryptographic hash of the request method and URL
	h := fnv.New128a()
	h.Write([]byte(req.Method))
	h.Write([]byte(req.URL.String()))
	key := hex.EncodeToString(h.Sum(nil))

	// build the path: hostname/key[:4]/key
	hostname := req.URL.Hostname()
	subdir := key[:4]
	return filepath.Join(c.config.CacheDir, hostname, subdir, key)
}

// Get returns a cached http.Response if present, else nil. Honors cacheEntryTTL if set.
func (c *DiskCache) Get(req *http.Request) (*http.Response, os.FileInfo, error) {
	path := c.cachePath(req)

	info, err := os.Stat(path)
	if err != nil {
		return nil, info, nil // cache miss
	}

	// Touch the file's atime to update LRU (best-effort), but do NOT update mtime!
	now := time.Now()
	_ = os.Chtimes(path, now, info.ModTime())

	f, err := os.Open(path)
	if err != nil {
		return nil, info, nil // treat as cache miss
	}
	resp, err := http.ReadResponse(bufio.NewReader(f), req)
	if err != nil {
		f.Close()
		return nil, info, err
	}

	resp.Body = &bodyWithFile{body: resp.Body, file: f}
	return resp, info, nil
}

// Set stores the HTTP response in the cache. Only stores status, headers, and body.
// No size or cacheEntryTTL check is performed here; size/ttl checks are handled in the transport and Get.
func (c *DiskCache) Set(req *http.Request, resp *http.Response) error {
	path := c.cachePath(req)

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
	// return on error
	if err != nil || origResp == nil {
		return origResp, err
	}

	// only handle StatusOK and StatusNotModified
	if origResp.StatusCode != http.StatusNotModified && origResp.StatusCode != http.StatusOK {
		return origResp, err
	}

	// handle cache control headers (but only if not StatusNotModified)
	if !c.config.IgnoreServerCacheControl && origResp.StatusCode != http.StatusNotModified {
		reasons, _, err := cacheobject.UsingRequestResponse(req, origResp.StatusCode, origResp.Header, false)
		if err != nil {
			if c.config.EnableLogging {
				log.Printf("cache control error: %s %s: %v", req.Method, req.URL.String(), err)
			}
			return origResp, err
		}
		if len(reasons) > 0 {
			if c.config.EnableLogging {
				log.Printf("cache control ignore: %s %s: %v", req.Method, req.URL.String(), reasons)
			}
			return origResp, nil // do not cache this response
		}
	}

	// if response indicates not modified, update modification time
	if origResp.StatusCode == http.StatusNotModified {
		now := time.Now()
		_ = os.Chtimes(c.cachePath(req), now, now)

		response, info, err := c.Get(req)
		if err != nil {
			return nil, fmt.Errorf("failed to get cache entry: %w", err)
		}
		if c.config.EnableLogging {
			log.Printf("cache MISS-UP: %s %s %s", req.Method, req.URL.String(), humanize.Bytes(uint64(info.Size())))
		}
		return response, nil
	} else {
		// Only check the limit if ContentLength is given (>= 0).
		if c.config.EntryMaxSize > 0 && origResp.ContentLength > int64(c.config.EntryMaxSize) && origResp.ContentLength >= 0 {
			if c.config.EnableLogging {
				log.Printf("response TOO LARGE to cache: %s %s (Content-Length: %d, Limit: %d)",
					req.Method, req.URL.String(), origResp.ContentLength, c.config.EntryMaxSize)
			}
			return origResp, nil
		}

		// update cache with the response
		err = c.Set(req, origResp)
		origResp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("cache set error: %w", err)
		}

		response, info, err := c.Get(req)
		if err != nil {
			return nil, fmt.Errorf("failed to get cache entry: %w", err)
		}
		if c.config.EnableLogging {
			log.Printf("cache MISS: %s %s %s", req.Method, req.URL.String(), humanize.Bytes(uint64(info.Size())))
		}
		return response, nil
	}
}

// RoundTrip implements http.RoundTripper. Only GET requests are cached.
// If multiple requests for the same URL come in concurrently, only one will download the file.
func (c *DiskCache) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return c.transport.RoundTrip(req) // bypass cache
	}
	inflightKey := req.URL.String()

	for {
		resp, info, err := c.Get(req)
		if err != nil {
			return nil, err
		}
		if resp != nil {
			// cacheEntryTTL check: if configured and file is too old, treat as miss
			// also set etag of old request to If-None-Match header
			if c.config.EntryTTL > 0 && time.Since(info.ModTime()) > c.config.EntryTTL {
				if c.config.EnableLogging {
					log.Info("cache EXPIRED: %s (expired %v ago, cacheEntryTTL %v)", req.URL.String(), time.Since(info.ModTime()), c.config.EntryTTL)
				}
				// pass etag to request if available
				if resp.Header.Get("ETag") != "" {
					req.Header.Set("If-None-Match", resp.Header.Get("ETag"))
				}

			} else {
				if c.config.EnableLogging {
					log.Printf("cache HIT: %s %s %s", req.Method, req.URL.String(), humanize.Bytes(uint64(info.Size())))
				}
				mCacheRequestsTotal.Inc()
				mCacheRequestsHitTotal.Inc()
				resp.Body = &countingReadCloser{rc: resp.Body, isHit: true}
				return resp, nil
			}
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
		if err == nil && resp != nil {
			mCacheRequestsTotal.Inc()
			mCacheRequestsMissTotal.Inc()
			resp.Body = &countingReadCloser{rc: resp.Body, isHit: false}
		}
		return resp, err
	}
}
