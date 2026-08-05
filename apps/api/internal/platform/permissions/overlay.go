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

// Snapshot is the whole overlay in memory, split by effect: role → the
// permissions the overlay adds to it, and role → the ones it takes away. It is
// derived state — role_permission_overrides is the source of truth — so it is
// always rebuilt whole, never patched.
type Snapshot struct {
	Grants map[string][]authz.Permission
	Denies map[string][]authz.Permission
}

// NewSnapshot returns an empty snapshot with both maps ready to append into.
func NewSnapshot() Snapshot {
	return Snapshot{
		Grants: make(map[string][]authz.Permission),
		Denies: make(map[string][]authz.Permission),
	}
}

// Add records one overlay row under the effect it carries. An unknown effect is
// ignored: enforcement must not widen OR narrow because of a value nothing in
// this package writes.
func (s Snapshot) Add(role string, p authz.Permission, effect string) {
	switch effect {
	case EffectGrant:
		s.Grants[role] = append(s.Grants[role], p)
	case EffectDeny:
		s.Denies[role] = append(s.Denies[role], p)
	}
}

// Merge returns base ∪ grants − denies, every side restricted to `own` (the
// domain's own vocabulary) so a domain's resolver never learns — or loses — a
// key from a vocabulary it does not enforce. base is not modified.
//
// Denies NEVER apply to `ren`. That is hardcoded here, at the one place the
// table becomes enforcement, rather than left to the validator: ren is the
// lockout-recovery fuse, and the guarantee has to survive a row nobody in this
// codebase wrote — a hand-run UPDATE, a restored dump, a future endpoint that
// forgets a rule. Whatever the table says, ren keeps its code floor.
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

// without returns perms with every occurrence of p removed, as a new slice: the
// input is a copy of a perm package's package-level bundle and must not be
// aliased into the merged table.
func without(perms []authz.Permission, p authz.Permission) []authz.Permission {
	out := make([]authz.Permission, 0, len(perms))
	for _, existing := range perms {
		if existing != p {
			out = append(out, existing)
		}
	}
	return out
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
// A database that cannot be read leaves the previous (at startup: the
// compiled-in) table in force and logs. Once the overlay can also DENY, that is
// no longer unconditionally fail-safe: a process that never managed its first
// load enforces the code floor, which still has the keys an operator denied. It
// cannot go the other way — an unreadable table never invents a grant — and the
// startup path retries and then says so at slog.Error rather than at the
// warning level a reader might scroll past.
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

// startupBackoff is the wait AFTER a failed initial load attempt. Three
// attempts, so a process boots at most ~3s later than it otherwise would when
// the database is briefly unreachable (the common case: a compose start where
// Postgres is still coming up). The last entry is never slept — it is listed so
// the sequence reads as 1s/2s/4s rather than looking truncated.
var startupBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// Start loads the overlay once — synchronously, with retries — and then keeps it
// current until ctx is done: immediately on an announcement (when this process
// has Redis) and in any case every PollInterval.
//
// The initial load is worth retrying because the window it covers is the one
// window where the process is WRONG rather than stale: with no snapshot loaded,
// every deny an operator wrote is not being enforced. Boot is never blocked
// beyond the backoff — refusing to serve would turn a permission-console outage
// into a site outage — so on total failure the process serves on the code floor
// and says so loudly.
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

// initialLoad tries Refresh up to len(startupBackoff) times before giving up.
// Returns as soon as one attempt succeeds, and always returns.
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

// LoadSnapshot reads the whole overlay table. Rows naming a permission that is
// no longer live (a key deleted from the code in a later deploy) are dropped
// with a warning rather than merged: a resolver must not carry a key nothing
// enforces, and silently keeping it would hide the cleanup that is due.
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
