package devapi

import (
	"slices"
	"testing"
)

// The news feed republishes two partners' content under our byline, and both
// authorised our downstream specifically. Who holds a key is therefore the gate.
// 2026-08-18 mechanised the paperwork (an application a key owner files in the
// portal) WITHOUT loosening the gate: the scope must never enter
// selfServiceScopes, and an unapproved owner must still be refused.
func TestScopeNewsReadSelfServiceExcluded(t *testing.T) {
	if ScopeNewsRead != "news:read" {
		t.Errorf("ScopeNewsRead = %q, want %q", ScopeNewsRead, "news:read")
	}
	if slices.Contains(selfServiceScopes, ScopeNewsRead) {
		t.Errorf("selfServiceScopes must NOT contain %q — news keys are granted by us, never self-issued", ScopeNewsRead)
	}
	if got := gateForScope(ScopeNewsRead); got != scopeGateGrant {
		t.Errorf("gateForScope(news:read) = %v, want scopeGateGrant", got)
	}
}

// galgame:read outlived its face: /v1/galgame is a 410 tombstone since wave 146,
// so the portal was still offering a permission over nothing.
func TestScopeGalgameReadRetired(t *testing.T) {
	if slices.Contains(selfServiceScopes, ScopeGalgameRead) {
		t.Errorf("selfServiceScopes must NOT contain %q — the galgame face retired at wave 146", ScopeGalgameRead)
	}
	if got := gateForScope(ScopeGalgameRead); got != scopeGateDenied {
		t.Errorf("gateForScope(galgame:read) = %v, want scopeGateDenied", got)
	}
	if want := []string{ScopeCatalogRead}; !slices.Equal(selfServiceScopes, want) {
		t.Errorf("selfServiceScopes = %v, want %v", selfServiceScopes, want)
	}
}

func TestScopeGalgameWriteSelfServiceExcluded(t *testing.T) {
	if ScopeGalgameWrite != "galgame:write" {
		t.Errorf("ScopeGalgameWrite = %q, want %q", ScopeGalgameWrite, "galgame:write")
	}
	if slices.Contains(selfServiceScopes, ScopeGalgameWrite) {
		t.Errorf("selfServiceScopes must NOT contain %q (D3: write is never self-service)", ScopeGalgameWrite)
	}
	if got := gateForScope(ScopeGalgameWrite); got != scopeGateDenied {
		t.Errorf("gateForScope(galgame:write) = %v, want scopeGateDenied", got)
	}
	if got := gateForScope(ScopeCatalogRead); got != scopeGateSelfService {
		t.Errorf("gateForScope(catalog:read) = %v, want scopeGateSelfService", got)
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
