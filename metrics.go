package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	mRequestsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_requests_total",
		Help: "The total number of received requests.",
	})
	mRequestsHitTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_requests_hits_total",
		Help: "The total number of received requests with cache hits.",
	})
	mRequestsMissTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_requests_miss_total",
		Help: "The total number of received requests with cache miss.",
	})

	mRequestsBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_requests_bytes",
		Help: "Amount of handled data.",
	})
	mRequestsHitBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_requests_hit_bytes",
		Help: "Amount of handled data with cache hit.",
	})
	mRequestsMissBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gitmproxy_requests_miss_bytes",
		Help: "Amount of handled data with cache miss.",
	})
)
