package main

import (
	"net/http"
	"sync"

	"github.com/AdguardTeam/golibs/log"
)

// CacheTransport is an http.RoundTripper that caches GET responses.
// It ensures that only one download for a given cache key happens concurrently.
type CacheTransport struct {
	Cache     *DiskCache
	transport http.RoundTripper
}

// NewCacheTransport creates new CacheTransport with the given DiskCache and optional RoundTripper.
func NewCacheTransport(cache *DiskCache, roundTripper http.RoundTripper) *CacheTransport {
	if cache == nil {
		panic("cache must not be nil")
	}
	if roundTripper == nil {
		roundTripper = http.DefaultTransport
	}
	return &CacheTransport{
		Cache:     cache,
		transport: roundTripper,
	}
}

// doSingleflightDownload performs the download, cache, and returns the response for a cache miss.
// It handles inflight map cleanup and wg.Done().
// If the response is too large to cache (by ContentLength), it is returned directly and not stored.
func (t *CacheTransport) doSingleflightDownload(req *http.Request, inflightKey string, wg *sync.WaitGroup) (*http.Response, error) {
	defer func() {
		t.Cache.downloadMu.Lock()
		delete(t.Cache.inflight, inflightKey)
		wg.Done()
		t.Cache.downloadMu.Unlock()
	}()

	// Download the response body
	origResp, err := t.transport.RoundTrip(req)
	if err != nil || origResp == nil {
		return origResp, err
	}

	// Only check the limit if ContentLength is given (>= 0).
	if t.Cache.config.EntryMaxSize > 0 && origResp.ContentLength > int64(t.Cache.config.EntryMaxSize) && origResp.ContentLength >= 0 {
		if t.Cache.config.EnableLogging {
			log.Printf("response TOO LARGE to cache: %s %s (Content-Length: %d, Limit: %d)",
				req.Method, req.URL.String(), origResp.ContentLength, t.Cache.config.EntryMaxSize)
		}
		return origResp, nil
	}

	if t.Cache.config.EnableLogging {
		log.Printf("cache MISS: %s %s", req.Method, req.URL.String())
	}
	_ = t.Cache.Set(req, origResp)
	origResp.Body.Close()
	return t.Cache.Get(req)
}

// RoundTrip implements http.RoundTripper. Only GET requests are cached.
// If multiple requests for the same URL come in concurrently, only one will download the file.
func (t *CacheTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return t.transport.RoundTrip(req)
	}
	inflightKey := req.URL.String()

	for {
		resp, err := t.Cache.Get(req)
		if err != nil {
			return nil, err
		}
		if resp != nil {
			if t.Cache.config.EnableLogging {
				log.Printf("cache HIT: %s %s", req.Method, req.URL.String())
			}
			mRequestsTotal.Inc()
			mRequestsBytes.Add(float64(resp.ContentLength))
			mRequestsHitTotal.Inc()
			mRequestsHitBytes.Add(float64(resp.ContentLength))
			return resp, nil
		}

		t.Cache.downloadMu.Lock()
		if wg, ok := t.Cache.inflight[inflightKey]; ok {
			t.Cache.downloadMu.Unlock()
			wg.Wait()
			continue
		}
		wg := &sync.WaitGroup{}
		wg.Add(1)
		t.Cache.inflight[inflightKey] = wg
		t.Cache.downloadMu.Unlock()

		resp, err = t.doSingleflightDownload(req, inflightKey, wg)
		if err == nil {
			mRequestsTotal.Inc()
			mRequestsBytes.Add(float64(resp.ContentLength))
			mRequestsMissTotal.Inc()
			mRequestsMissBytes.Add(float64(resp.ContentLength))
		}
		return resp, err
	}
}
