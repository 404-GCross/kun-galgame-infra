package permissions

import (
	"context"
	"log/slog"
	"time"

	"api/internal/platform/authz"

	"gorm.io/gorm"
)

// ChangeChannel is the pub/sub channel a writer announces on so every process
// picks the change up in milliseconds instead of waiting for its next poll.
const ChangeChannel = "authz:overrides:changed"

// PollInterval is the refresh floor. It exists because the announcement is a
// best-effort nudge, not a guarantee: cmd/trust and cmd/ai run with no Redis at
// all (app.Options.NeedCache is false for both), and even where Redis is
// present a subscriber that was restarting when the message went out would
// otherwise stay stale forever. Bounding staleness at one interval everywhere
// is what makes the overlay safe to rely on.
const PollInterval = 30 * time.Second

// Snapshot is the whole overlay in memory: role → the permissions the overlay
// adds to it. It is derived state — role_permission_overrides is the source of
// truth — so it is always rebuilt whole, never patched.
type Snapshot map[string][]authz.Permission

// Merge returns base widened by the snapshot, restricted to `own` (the domain's
// own vocabulary) so a domain's resolver never learns a key from a vocabulary
// it does not enforce. base is not modified.
func Merge(base authz.Bundles, snap Snapshot, own map[authz.Permission]bool) authz.Bundles {
	merged := make(authz.Bundles, len(base))
	for role, perms := range base {
		merged[role] = append([]authz.Permission{}, perms...)
	}
	for role, perms := range snap {
		for _, p := range perms {
			if !own[p] {
				continue
			}
			if !contains(merged[role], p) {
				merged[role] = append(merged[role], p)
			}
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

// Broadcaster is the invalidation nudge. It is an interface, and nil-able, so a
// process without Redis still runs the overlay correctly (just on the poll
// floor) rather than failing to start.
type Broadcaster interface {
	Publish(ctx context.Context, channel, payload string) error
	Subscribe(ctx context.Context, channel string) (<-chan string, error)
}

// Distributor keeps every registered domain's Holder in step with the overlay
// table.
//
// The source of truth is the MAIN database (kun_galgame_infra), read directly
// by each process — every service that has a perm package already holds a
// main-DB connection through app.New, so there is no service that would need a
// cached copy shipped to it. Redis carries only the "something changed" nudge;
// nothing authoritative rides the channel, so a lost, duplicated or malformed
// message can at worst delay a refresh to the next poll.
//
// Fail-safe throughout: a database that cannot be read leaves the previous
// (at startup: the compiled-in) table in force and logs a warning. Enforcement
// never widens because of an infrastructure failure.
type Distributor struct {
	db  *gorm.DB
	reg *Registry
	bus Broadcaster
	own map[string]map[authz.Permission]bool
}

// NewDistributor wires a distributor. bus may be nil (no Redis in this process).
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

// Refresh reloads the overlay and swaps every domain's Resolver. Each Resolver
// is built whole and installed with one atomic store, so no reader ever sees a
// partially applied table.
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

// Announce tells every other process to refresh now. Best-effort by design: a
// failure here only costs latency, since the poll loop catches the change
// within PollInterval anyway.
func (d *Distributor) Announce(ctx context.Context) {
	if d.bus == nil {
		return
	}
	if err := d.bus.Publish(ctx, ChangeChannel, "refresh"); err != nil {
		slog.Warn("permissions: overlay change announcement failed; peers refresh on their next poll",
			"channel", ChangeChannel, "err", err)
	}
}

// Start loads the overlay once and then keeps it current until ctx is done:
// immediately on an announcement (when this process has Redis) and in any case
// every PollInterval.
func (d *Distributor) Start(ctx context.Context) {
	if err := d.Refresh(ctx); err != nil {
		slog.Warn("permissions: initial overlay load failed; enforcing code bundles only", "err", err)
	}

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
					// Subscription dropped — keep polling rather than spinning
					// on a closed channel.
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

// LoadSnapshot reads the whole overlay table. Rows naming a permission that is
// no longer live (a key deleted from the code in a later deploy) are dropped
// with a warning rather than merged: a resolver must not carry a key nothing
// enforces, and silently keeping it would hide the cleanup that is due.
func LoadSnapshot(ctx context.Context, db *gorm.DB, reg *Registry) (Snapshot, error) {
	var rows []RolePermissionOverride
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	snap := make(Snapshot)
	for _, row := range rows {
		p := authz.Permission(row.Permission)
		if _, ok := reg.Lookup(p); !ok {
			slog.Warn("permissions: overlay row names an unknown permission; ignored",
				"role", row.Role, "permission", row.Permission)
			continue
		}
		snap[row.Role] = append(snap[row.Role], p)
	}
	return snap, nil
}
