package devapi

import "testing"

func TestTierLimits(t *testing.T) {
	cases := []struct {
		tier      string
		rate      int
		quota     int
		unlimited bool
	}{
		{TierFree, 60, 50_000, false},
		{TierTrusted, 600, 1_000_000, false},
		{TierInternal, 0, 0, true},
		{"garbage", 60, 50_000, false}, // unknown → tightest (fail-safe)
	}
	for _, c := range cases {
		r, q, u := TierLimits(c.tier)
		if r != c.rate || q != c.quota || u != c.unlimited {
			t.Errorf("TierLimits(%q) = (%d,%d,%v), want (%d,%d,%v)", c.tier, r, q, u, c.rate, c.quota, c.unlimited)
		}
	}
}

func TestEffectiveRateQuota(t *testing.T) {
	// Tier default when no override.
	free := &Credential{Tier: TierFree}
	if lim, unl := free.EffectiveRate(); lim != 60 || unl {
		t.Errorf("free rate = (%d,%v), want (60,false)", lim, unl)
	}
	if lim, unl := free.EffectiveQuota(); lim != 50_000 || unl {
		t.Errorf("free quota = (%d,%v), want (50000,false)", lim, unl)
	}

	// Positive override wins over the tier default.
	over := &Credential{Tier: TierFree, RateOverride: 5, QuotaOverride: 999}
	if lim, _ := over.EffectiveRate(); lim != 5 {
		t.Errorf("override rate = %d, want 5", lim)
	}
	if lim, _ := over.EffectiveQuota(); lim != 999 {
		t.Errorf("override quota = %d, want 999", lim)
	}

	// Internal is unlimited regardless of override.
	internal := &Credential{Tier: TierInternal, RateOverride: 5}
	if _, unl := internal.EffectiveRate(); !unl {
		t.Errorf("internal rate should be unlimited")
	}
	if _, unl := internal.EffectiveQuota(); !unl {
		t.Errorf("internal quota should be unlimited")
	}
}

func TestHasScope(t *testing.T) {
	c := &Credential{Scopes: []string{ScopeCatalogRead, ScopeGalgameRead}}
	if !c.HasScope(ScopeCatalogRead) {
		t.Errorf("expected catalog:read present")
	}
	if c.HasScope(ScopeGalgameNSFW) {
		t.Errorf("did not expect galgame:nsfw present")
	}
}
