package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	mHttpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gitmproxy_http_requests_total",
		Help: "The total number of received requests.",
	}, []string{"method"})

	mCacheRequestsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_cache_requests_total",
		Help: "The total number of received requests.",
	})
	mCacheRequestsHitTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_cache_requests_hits_total",
		Help: "The total number of received requests with cache hits.",
	})
	mCacheRequestsMissTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_cache_requests_miss_total",
		Help: "The total number of received requests with cache miss.",
	})

	mCacheRequestsBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_cache_requests_bytes",
		Help: "Amount of handled data.",
	})
	mCacheRequestsHitBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_cache_requests_hit_bytes",
		Help: "Amount of handled data with cache hit.",
	})
	mCacheRequestsMissBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_cache_requests_miss_bytes",
		Help: "Amount of handled data with cache miss.",
	})
)
