// Package metrics defines the Prometheus instrumentation surface for the
// image service. Registered against prometheus.DefaultRegisterer so the
// standard promhttp handler picks them up automatically.
//
// Labels kept deliberately low-cardinality:
//   site    — OAuth client's image_site_key (bounded, <20 values)
//   preset  — preset name (bounded, ~3 values)
//   result  — outcome bucket ("success" / "rejected_quota" / ...)
//   op      — processing sub-stage name (decode / resize / encode / store)
//
// Per docs/image_service/03-api-design.md §6.
package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	// UploadTotal is the total count of upload attempts broken down by
	// site, preset, and outcome. Hot-path counter.
	UploadTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "image_upload_total",
		Help: "Total count of /image/upload requests by outcome",
	}, []string{"site", "preset", "result"})

	// UploadDuration is the end-to-end wall-clock duration of an upload,
	// from request entering the handler to response being written. Useful
	// for P99 latency alerting.
	UploadDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "image_upload_duration_seconds",
		Help:    "End-to-end /image/upload latency in seconds",
		Buckets: prometheus.DefBuckets, // 0.005..10s standard
	}, []string{"site", "preset"})

	// ProcessingDuration tracks individual pipeline sub-stages. Exposed
	// but currently only populated for the full pipeline; instrumenting
	// each stage is a follow-up.
	ProcessingDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "image_processing_duration_seconds",
		Help:    "Per-stage processing duration",
		Buckets: prometheus.DefBuckets,
	}, []string{"op"})

	// DedupHits counts uploads that hit an existing hash. High dedup
	// rate = the content-addressed design is paying off.
	DedupHits = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "image_dedup_hits_total",
		Help: "Uploads where the hash already existed (dedup hit)",
	}, []string{"site"})
)

func init() {
	prometheus.MustRegister(UploadTotal, UploadDuration, ProcessingDuration, DedupHits)
}
