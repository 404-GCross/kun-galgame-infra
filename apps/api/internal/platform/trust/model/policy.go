package model

import "time"

// TrustSitePolicy is the per-tenant moderation posture (step 07 M0): the knobs
// that decide how hard the pipeline acts on ONE site's content. It exists
// because everything governing enforcement today is platform-global — a single
// KUN_TRUST_SCAN_MODE env, a single aggregateThreshold constant — so onboarding
// a second site would mean imposing kungal's calibrated posture on a site whose
// content, audience and score distribution are nothing like kungal's. Worse, it
// would deny a new site the shadow period kungal itself got.
//
// Every override is NULLABLE and NULL means "no opinion — use the platform
// default". That is the whole design:
//
//   - A site with no row behaves EXACTLY as it does today. Creating this table
//     changes nothing until someone writes a row, which is what makes it safe to
//     ship ahead of the code that reads it.
//   - NULL is distinguishable from a value equal to the current default, so
//     later changing a platform default moves the sites that never expressed an
//     opinion and leaves the sites that did exactly where they are. A DDL
//     default on these columns would erase that distinction permanently — which
//     is the GORM zero-value trap (章程 ruling 4) in its most damaging form:
//     here the "zero" would silently claim the site had chosen something.
//
// Site is the primary key rather than an identity column: there is at most one
// policy per site, the site string IS the identity, and a surrogate key would
// only invite two competing rows for the same tenant.
type TrustSitePolicy struct {
	Site string `gorm:"primaryKey;column:site" json:"site"`

	// ScanMode overrides the worker's enforcement posture for this site
	// (ScanMode* constants). NULL = the platform default (KUN_TRUST_SCAN_MODE).
	// A newly onboarded site is expected to sit at shadow here for as long as it
	// takes to see its own score distribution.
	ScanMode *int16 `gorm:"column:scan_mode" json:"scan_mode"`

	// SampleRate overrides the clean-band calibration sample for this site. It
	// is per-site for a measurement reason, not a taste one: with one global
	// rate, a high-volume site's samples drown a low-volume site's, and the
	// small site's false-negative rate becomes unmeasurable no matter how long
	// you wait. NULL = the platform default (KUN_TRUST_SCAN_SAMPLE_RATE).
	SampleRate *float64 `gorm:"column:sample_rate" json:"sample_rate"`

	// FlagThreshold is the classifier score at or above which THIS site treats
	// content as flagged. NULL = defer to the AI gateway's own boolean verdict,
	// which is what the pipeline does today.
	//
	// Note what this implies: the gateway decides `flagged` with one general
	// notion of "bad", but a scale is a tenant policy, not a model property. So
	// once trust re-derives the verdict from the stored score, the gateway's
	// boolean becomes a reference signal — preserved as
	// TrustScanResult.GatewayFlagged so calibration samples taken before and
	// after the change stay comparable.
	FlagThreshold *float32 `gorm:"type:real;column:flag_threshold" json:"flag_threshold"`

	// AggregateThreshold overrides the accumulated report weight that opens a
	// review item. NULL = the platform default (aggregateThreshold). A small
	// community and a large forum do not agree on how many complaints mean
	// something.
	AggregateThreshold *float32 `gorm:"type:real;column:aggregate_threshold" json:"aggregate_threshold"`

	// AutoHideEnabled decides whether a live-mode flag may queue a hide
	// disposition, separately from whether it opens a review item. A site can
	// therefore run live for VISIBILITY (everything flagged reaches a human)
	// without granting the classifier the power to take content down on its own
	// — the posture most sites should actually start from once they leave
	// shadow. NULL = the platform default (live implies hide, as today).
	AutoHideEnabled *bool `gorm:"column:auto_hide_enabled" json:"auto_hide_enabled"`

	// Note records WHY this site is set the way it is. A posture without a
	// stated reason is un-reviewable a month later.
	Note *string `gorm:"column:note" json:"note"`

	CreatedAt time.Time `gorm:"not null;default:now();column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"not null;default:now();column:updated_at" json:"updated_at"`
}

func (TrustSitePolicy) TableName() string { return "trust_site_policy" }
