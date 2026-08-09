// user_playtime_test.go — the playtime face's rules that hold without a
// database: what a report must look like to be stored at all.
//
// The folding rules (MAX across clients, finished-outranks-everything) and the
// aggregate's thresholds need rows, so they live with the DB-backed suites;
// what is pinned here is the gate every write passes through first.
package service

import (
	"testing"
	"time"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateReport(t *testing.T) {
	base := PlaytimeReport{ActorUID: 7, WorkID: 1, ClientID: "kurumi", Minutes: 600,
		Status: model.PlaytimeStatusFinished}

	cases := []struct {
		name string
		mut  func(*PlaytimeReport)
		want error
	}{
		{"a well-formed report", func(*PlaytimeReport) {}, nil},
		// Zero is legal: "I own it and have not played it" is a fact a manager
		// legitimately reports, and it simply never clears the aggregate floor.
		{"zero minutes", func(r *PlaytimeReport) { r.Minutes = 0 }, nil},
		{"exactly the ceiling", func(r *PlaytimeReport) { r.Minutes = model.PlaytimeMinutesMax }, nil},
		{"past the ceiling", func(r *PlaytimeReport) { r.Minutes = model.PlaytimeMinutesMax + 1 }, ErrPlaytimeMinutesRange},
		{"negative minutes", func(r *PlaytimeReport) { r.Minutes = -1 }, ErrPlaytimeMinutesRange},
		{"no user", func(r *PlaytimeReport) { r.ActorUID = 0 }, ErrPlaytimeActorRequired},
		// The client is the third key member and the handle a bad reporter is
		// excluded by; a report that cannot name one has nowhere to live.
		{"no client", func(r *PlaytimeReport) { r.ClientID = "" }, ErrPlaytimeClientRequired},
		{"a status outside the vocabulary", func(r *PlaytimeReport) { r.Status = 9 }, ErrPlaytimeBadStatus},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := base
			c.mut(&r)
			err := validateReport(r)
			if c.want == nil {
				require.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, c.want)
		})
	}
}

// Every status the wire can name must survive the gate — otherwise a client
// following the published enum gets a 400 for a legal word.
func TestValidateReportAcceptsEveryStatus(t *testing.T) {
	now := time.Now()
	for _, st := range []int16{
		model.PlaytimeStatusPlaying, model.PlaytimeStatusFinished,
		model.PlaytimeStatusDropped, model.PlaytimeStatusOnHold,
	} {
		require.NoError(t, validateReport(PlaytimeReport{
			ActorUID: 1, WorkID: 1, ClientID: "c", Minutes: 10,
			Status: st, LastPlayedAt: &now,
		}), "status %d", st)
	}
}
