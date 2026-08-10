package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	UploadTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "image_upload_total",
		Help: "Total count of /image/upload requests by outcome",
	}, []string{"site", "preset", "result"})

	UploadDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "image_upload_duration_seconds",
		Help:    "End-to-end /image/upload latency in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"site", "preset"})

	ProcessingDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "image_processing_duration_seconds",
		Help:    "Per-stage processing duration",
		Buckets: prometheus.DefBuckets,
	}, []string{"op"})

	DedupHits = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "image_dedup_hits_total",
		Help: "Uploads where the hash already existed (dedup hit)",
	}, []string{"site"})
)

func init() {
	prometheus.MustRegister(UploadTotal, UploadDuration, ProcessingDuration, DedupHits)
}
