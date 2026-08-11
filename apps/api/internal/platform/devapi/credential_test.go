package devapi

import (
	"slices"
	"testing"
)

// The news feed republishes two partners' content under our byline, and both
// authorised our downstream specifically. Who holds a key is therefore the gate,
// so the scope must never become self-serviceable.
func TestScopeNewsReadSelfServiceExcluded(t *testing.T) {
	if ScopeNewsRead != "news:read" {
		t.Errorf("ScopeNewsRead = %q, want %q", ScopeNewsRead, "news:read")
	}
	if slices.Contains(selfServiceScopes, ScopeNewsRead) {
		t.Errorf("selfServiceScopes must NOT contain %q — news keys are granted by us, never self-issued", ScopeNewsRead)
	}
	if err := checkSelfServiceScopes([]string{ScopeNewsRead}); err != ErrScopeNotAllowed {
		t.Errorf("checkSelfServiceScopes([news:read]) = %v, want ErrScopeNotAllowed", err)
	}
}

func TestScopeGalgameWriteSelfServiceExcluded(t *testing.T) {
	if ScopeGalgameWrite != "galgame:write" {
		t.Errorf("ScopeGalgameWrite = %q, want %q", ScopeGalgameWrite, "galgame:write")
	}
	if slices.Contains(selfServiceScopes, ScopeGalgameWrite) {
		t.Errorf("selfServiceScopes must NOT contain %q (D3: write is never self-service)", ScopeGalgameWrite)
	}
	if err := checkSelfServiceScopes([]string{ScopeGalgameWrite}); err != ErrScopeNotAllowed {
		t.Errorf("checkSelfServiceScopes([write]) = %v, want ErrScopeNotAllowed", err)
	}
	if err := checkSelfServiceScopes([]string{ScopeGalgameRead, ScopeGalgameWrite}); err != ErrScopeNotAllowed {
		t.Errorf("checkSelfServiceScopes([read,write]) = %v, want ErrScopeNotAllowed", err)
	}
	if err := checkSelfServiceScopes([]string{ScopeCatalogRead, ScopeGalgameRead}); err != nil {
		t.Errorf("checkSelfServiceScopes([catalog:read,galgame:read]) = %v, want nil", err)
	}
}

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
		{"garbage", 60, 50_000, false},
	}
	for _, c := range cases {
		r, q, u := TierLimits(c.tier)
		if r != c.rate || q != c.quota || u != c.unlimited {
			t.Errorf("TierLimits(%q) = (%d,%d,%v), want (%d,%d,%v)", c.tier, r, q, u, c.rate, c.quota, c.unlimited)
		}
	}
}

func TestEffectiveRateQuota(t *testing.T) {
	free := &Credential{Tier: TierFree}
	if lim, unl := free.EffectiveRate(); lim != 60 || unl {
		t.Errorf("free rate = (%d,%v), want (60,false)", lim, unl)
	}
	if lim, unl := free.EffectiveQuota(); lim != 50_000 || unl {
		t.Errorf("free quota = (%d,%v), want (50000,false)", lim, unl)
	}

	over := &Credential{Tier: TierFree, RateOverride: 5, QuotaOverride: 999}
	if lim, _ := over.EffectiveRate(); lim != 5 {
		t.Errorf("override rate = %d, want 5", lim)
	}
	if lim, _ := over.EffectiveQuota(); lim != 999 {
		t.Errorf("override quota = %d, want 999", lim)
	}

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
