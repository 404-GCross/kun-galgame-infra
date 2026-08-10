package permissions

import (
	"context"
	"log/slog"
	"time"

	"api/internal/platform/authz"

	"gorm.io/gorm"
)

const ChangeChannel = "authz:overrides:changed"

const PollInterval = 30 * time.Second

type Snapshot struct {
	Grants map[string][]authz.Permission
	Denies map[string][]authz.Permission
}

func NewSnapshot() Snapshot {
	return Snapshot{
		Grants: make(map[string][]authz.Permission),
		Denies: make(map[string][]authz.Permission),
	}
}

func (s Snapshot) Add(role string, p authz.Permission, effect string) {
	switch effect {
	case EffectGrant:
		s.Grants[role] = append(s.Grants[role], p)
	case EffectDeny:
		s.Denies[role] = append(s.Denies[role], p)
	}
}

func Merge(base authz.Bundles, snap Snapshot, own map[authz.Permission]bool) authz.Bundles {
	merged := make(authz.Bundles, len(base))
	for role, perms := range base {
		merged[role] = append([]authz.Permission{}, perms...)
	}

	for role, perms := range snap.Grants {
		for _, p := range perms {
			if !own[p] || contains(merged[role], p) {
				continue
			}
			merged[role] = append(merged[role], p)
		}
	}

	for role, perms := range snap.Denies {
		if role == RoleRen {
			continue
		}
		for _, p := range perms {
			if !own[p] {
				continue
			}
			merged[role] = without(merged[role], p)
		}
	}
	return merged
}

func contains(perms []authz.Permission, p authz.Permission) bool {
	for _, existing := range perms {
		if existing == p {
			return true
		}
	}
	return false
}

func without(perms []authz.Permission, p authz.Permission) []authz.Permission {
	out := make([]authz.Permission, 0, len(perms))
	for _, existing := range perms {
		if existing != p {
			out = append(out, existing)
		}
	}
	return out
}

type Broadcaster interface {
	Publish(ctx context.Context, channel, payload string) error
	Subscribe(ctx context.Context, channel string) (<-chan string, error)
}

type Distributor struct {
	db  *gorm.DB
	reg *Registry
	bus Broadcaster
	own map[string]map[authz.Permission]bool
}

func NewDistributor(db *gorm.DB, reg *Registry, bus Broadcaster) *Distributor {
	own := make(map[string]map[authz.Permission]bool, len(reg.Domains()))
	for _, d := range reg.Domains() {
		set := make(map[authz.Permission]bool, len(d.Keys))
		for _, k := range d.Keys {
			set[k.Permission] = true
		}
		own[d.Name] = set
	}
	return &Distributor{db: db, reg: reg, bus: bus, own: own}
}

func (d *Distributor) Refresh(ctx context.Context) error {
	snap, err := LoadSnapshot(ctx, d.db, d.reg)
	if err != nil {
		return err
	}
	for _, dom := range d.reg.Domains() {
		dom.Holder.Swap(authz.NewResolver(Merge(dom.Bundles, snap, d.own[dom.Name])))
	}
	return nil
}

func (d *Distributor) Announce(ctx context.Context) {
	if d.bus == nil {
		return
	}
	if err := d.bus.Publish(ctx, ChangeChannel, "refresh"); err != nil {
		slog.Warn("permissions: overlay change announcement failed; peers refresh on their next poll",
			"channel", ChangeChannel, "err", err)
	}
}

var startupBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

func (d *Distributor) Start(ctx context.Context) {
	d.initialLoad(ctx)

	var nudges <-chan string
	if d.bus != nil {
		ch, err := d.bus.Subscribe(ctx, ChangeChannel)
		if err != nil {
			slog.Warn("permissions: overlay subscribe failed; refreshing on the poll interval only",
				"channel", ChangeChannel, "err", err)
		} else {
			nudges = ch
		}
	}

	go func() {
		ticker := time.NewTicker(PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case _, ok := <-nudges:
				if !ok {
					nudges = nil
					continue
				}
			}
			if err := d.Refresh(ctx); err != nil {
				slog.Warn("permissions: overlay refresh failed; previous table stays in force", "err", err)
			}
		}
	}()
}

func (d *Distributor) initialLoad(ctx context.Context) {
	var lastErr error
	for attempt, wait := range startupBackoff {
		if lastErr = d.Refresh(ctx); lastErr == nil {
			if attempt > 0 {
				slog.Info("permissions: overlay loaded after a retry", "attempts", attempt+1)
			}
			return
		}
		if attempt == len(startupBackoff)-1 {
			break
		}
		slog.Warn("permissions: initial overlay load failed; retrying",
			"attempt", attempt+1, "retry_in", wait, "err", lastErr)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}

	slog.Error("permissions: initial overlay load failed after every retry; "+
		"THIS PROCESS IS ENFORCING THE CODE FLOOR ONLY — no overlay grant and, "+
		"more importantly, NO DENY is in force until a later poll succeeds",
		"attempts", len(startupBackoff), "poll_interval", PollInterval, "err", lastErr)
}

func LoadSnapshot(ctx context.Context, db *gorm.DB, reg *Registry) (Snapshot, error) {
	var rows []RolePermissionOverride
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		return Snapshot{}, err
	}
	snap := NewSnapshot()
	for _, row := range rows {
		p := authz.Permission(row.Permission)
		if _, ok := reg.Lookup(p); !ok {
			slog.Warn("permissions: overlay row names an unknown permission; ignored",
				"role", row.Role, "permission", row.Permission)
			continue
		}
		if row.Effect != EffectGrant && row.Effect != EffectDeny {
			slog.Warn("permissions: overlay row carries an unknown effect; ignored",
				"role", row.Role, "permission", row.Permission, "effect", row.Effect)
			continue
		}
		snap.Add(row.Role, p, row.Effect)
	}
	return snap, nil
}
