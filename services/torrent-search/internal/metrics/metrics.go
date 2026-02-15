package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "search",
		Name:      "http_requests_total",
		Help:      "Total HTTP requests by method, path and status code.",
	}, []string{"method", "path", "status"})

	HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "search",
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request duration in seconds.",
		Buckets:   []float64{0.05, 0.1, 0.3, 0.5, 1, 2, 5, 10, 20},
	}, []string{"method", "path"})

	ProviderRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "search",
		Name:      "provider_requests_total",
		Help:      "Total requests to search providers by provider name and result status.",
	}, []string{"provider", "status"})

	ProviderRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "search",
		Name:      "provider_request_duration_seconds",
		Help:      "Search provider request duration in seconds.",
		Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 20, 30},
	}, []string{"provider"})

	ProviderAvailable = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "search",
		Name:      "provider_available",
		Help:      "Whether a provider is available (1) or blocked by circuit breaker (0).",
	}, []string{"provider"})

	CacheHitsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "search",
		Name:      "cache_hits_total",
		Help:      "Total number of search cache hits.",
	})

	CacheMissesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "search",
		Name:      "cache_misses_total",
		Help:      "Total number of search cache misses.",
	})

	FlareSolverrDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "search",
		Name:      "flaresolverr_request_duration_seconds",
		Help:      "FlareSolverr request duration in seconds.",
		Buckets:   []float64{1, 2, 5, 10, 20, 30, 60},
	})
)

func Register(reg prometheus.Registerer) {
	reg.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
		ProviderRequestsTotal,
		ProviderRequestDuration,
		ProviderAvailable,
		CacheHitsTotal,
		CacheMissesTotal,
		FlareSolverrDuration,
	)
}
