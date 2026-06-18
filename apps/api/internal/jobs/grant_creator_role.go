package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/pkg/config"
)

// GrantCreatorRoleOpts controls auto-promotion to the `creator` role.
type GrantCreatorRoleOpts struct {
	// Threshold is the minimum number of DISTINCT published galgames a user
	// must have contributed to before auto-promotion. Default 3 (matches the
	// legacy "已发布 ≥3" prerequisite).
	Threshold int
	Timeout   time.Duration
	DryRun    bool
}

// DefaultGrantCreatorRoleOpts is what the scheduler uses.
func DefaultGrantCreatorRoleOpts() GrantCreatorRoleOpts {
	return GrantCreatorRoleOpts{Threshold: 3, Timeout: 10 * time.Minute}
}

// RunGrantCreatorRole auto-promotes trusted contributors to the `creator` role
// (design: docs/auth/01-creator-role-design.md §5.1). A user who has
// contributed to >= Threshold PUBLISHED galgames and is in good standing (not
// banned) is granted `creator` if they don't already hold it.
//
// Grant-only: never auto-demotes (decision 4); abuse is handled by an admin
// revoke. Idempotent: re-runs grant nothing new. It reads the wiki DB for the
// contribution count and writes the identity DB for the grant — wiki
// `*.user_id` == identity `users.id` (the galgame service already joins them).
//
// The `creator` role row MUST be seeded first (cmd/migrate); the grant is a
// SELECT id FROM roles WHERE name='creator', which silently no-ops if missing,
// so this job fails loudly instead.
func RunGrantCreatorRole(ctx context.Context, cfg *config.Config, opts GrantCreatorRoleOpts) (Summary, error) {
	if opts.Threshold < 1 {
		opts.Threshold = 3
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// 1. Wiki DB: users who contributed to >= Threshold distinct published games.
	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		return nil, fmt.Errorf("wiki db connect: %w", err)
	}
	defer wikiDB.Close()

	var qualifying []int
	if err := wikiDB.DB().WithContext(ctx).Raw(`
		SELECT c.user_id
		FROM galgame_contributor c
		JOIN galgame g ON g.id = c.galgame_id
		WHERE g.status = 0
		GROUP BY c.user_id
		HAVING count(DISTINCT c.galgame_id) >= ?
	`, opts.Threshold).Scan(&qualifying).Error; err != nil {
		return nil, fmt.Errorf("count contributions: %w", err)
	}
	slog.Info("grant-creator: qualifying contributors", "count", len(qualifying), "threshold", opts.Threshold)
	if len(qualifying) == 0 {
		return Summary{"qualifying": 0, "granted": 0}, nil
	}

	// 2. Identity DB: grant `creator` to good-standing users who lack it.
	idDB, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("identity db connect: %w", err)
	}
	defer idDB.Close()
	db := idDB.DB().WithContext(ctx)

	var creatorRoleID uint
	if err := db.Raw(`SELECT id FROM roles WHERE name = 'creator'`).Scan(&creatorRoleID).Error; err != nil {
		return nil, fmt.Errorf("lookup creator role: %w", err)
	}
	if creatorRoleID == 0 {
		return nil, fmt.Errorf("creator role not seeded — run cmd/migrate against the identity DB first")
	}

	// eligible = qualifying ∩ (good standing) ∩ (not already creator)
	const eligibleWhere = `
		WHERE u.id IN ? AND u.status <> 1
		  AND NOT EXISTS (
		    SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id AND ur.role_id = ?
		  )`

	if opts.DryRun {
		var wouldGrant int64
		if err := db.Raw(`SELECT count(*) FROM users u`+eligibleWhere, qualifying, creatorRoleID).
			Scan(&wouldGrant).Error; err != nil {
			return nil, fmt.Errorf("count eligible: %w", err)
		}
		return Summary{"qualifying": len(qualifying), "would_grant": wouldGrant, "dry_run": true}, nil
	}

	res := db.Exec(`
		INSERT INTO user_roles (user_id, role_id)
		SELECT u.id, ? FROM users u`+eligibleWhere+`
		ON CONFLICT DO NOTHING`, creatorRoleID, qualifying, creatorRoleID)
	if res.Error != nil {
		return nil, fmt.Errorf("grant creator: %w", res.Error)
	}
	slog.Info("grant-creator: granted", "count", res.RowsAffected)
	return Summary{"qualifying": len(qualifying), "granted": res.RowsAffected}, nil
}
