package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "engine",
		Name:      "http_requests_total",
		Help:      "Total HTTP requests by method, path and status code.",
	}, []string{"method", "path", "status"})

	HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "engine",
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request duration in seconds.",
		Buckets:   []float64{0.05, 0.1, 0.3, 0.5, 1, 2, 5, 10, 30},
	}, []string{"method", "path"})

	ActiveSessions = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "engine",
		Name:      "active_sessions",
		Help:      "Number of currently active torrent sessions.",
	})

	DownloadSpeedBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "engine",
		Name:      "download_speed_bytes",
		Help:      "Current aggregate download speed in bytes per second.",
	})

	UploadSpeedBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "engine",
		Name:      "upload_speed_bytes",
		Help:      "Current aggregate upload speed in bytes per second.",
	})

	HLSActiveJobs = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "engine",
		Name:      "hls_active_jobs",
		Help:      "Number of currently active HLS transcode jobs.",
	})

	HLSJobStartsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "engine",
		Name:      "hls_job_starts_total",
		Help:      "Total number of HLS transcode jobs started.",
	})

	HLSJobFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "engine",
		Name:      "hls_job_failures_total",
		Help:      "Total number of HLS transcode job failures.",
	})

	HLSSeekTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "engine",
		Name:      "hls_seek_requests_total",
		Help:      "Total number of HLS seek requests.",
	})

	HLSEncodeDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "engine",
		Name:      "hls_encode_duration_seconds",
		Help:      "Duration of FFmpeg encoding jobs in seconds.",
		Buckets:   []float64{1, 5, 10, 30, 60, 120, 300},
	})

	HLSCacheCleanupErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "engine",
		Name:      "hls_cache_cleanup_errors_total",
		Help:      "Total number of HLS cache cleanup failures.",
	})

	HLSAutoRestartsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "engine",
		Name:      "hls_auto_restarts_total",
		Help:      "Total number of HLS auto-restarts by reason.",
	}, []string{"reason"})

	HLSCacheSizeBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "engine",
		Name:      "hls_cache_size_bytes",
		Help:      "Current total size of the HLS segment cache in bytes.",
	})

	PeersConnected = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "engine",
		Name:      "peers_connected",
		Help:      "Total number of peers connected across all sessions.",
	})
)

func Register(reg prometheus.Registerer) {
	reg.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
		ActiveSessions,
		DownloadSpeedBytes,
		UploadSpeedBytes,
		HLSActiveJobs,
		HLSJobStartsTotal,
		HLSJobFailuresTotal,
		HLSSeekTotal,
		HLSEncodeDuration,
		HLSCacheCleanupErrors,
		HLSAutoRestartsTotal,
		HLSCacheSizeBytes,
		PeersConnected,
	)
}
