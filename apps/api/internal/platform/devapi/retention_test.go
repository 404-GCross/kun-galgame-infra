package devapi

import (
	"context"
	"testing"
	"time"
)

// TestPruneUsageBefore: the retention prune deletes rollup rows strictly older
// than the cutoff day and keeps rows on/after it — the `day < today−400` policy,
// exercised at the exact boundary (cutoff survives, cutoff−1 is pruned) with a
// dry-run count and an idempotent re-run.
func TestPruneUsageBefore(t *testing.T) {
	cleanupSelf(t)
	repo := NewRepository(testDB)
	ctx := context.Background()

	now := time.Now().UTC()
	cutoff := RetentionCutoffDay(now) // oldest day still KEPT = today − 400
	dayStr := func(offset int) string { return now.AddDate(0, 0, offset).Format("2006-01-02") }

	seed := []DeveloperAPIUsage{
		{ClientID: "ret_a", KeyID: 1, Face: "catalog", Day: dayStr(-500), Count: 5, UpdatedAt: now}, // ancient → prune
		{ClientID: "ret_a", KeyID: 1, Face: "catalog", Day: dayStr(-401), Count: 3, UpdatedAt: now}, // one past cutoff → prune
		{ClientID: "ret_a", KeyID: 1, Face: "catalog", Day: cutoff, Count: 7, UpdatedAt: now},       // exactly cutoff → keep
		{ClientID: "ret_a", KeyID: 1, Face: "catalog", Day: dayStr(-10), Count: 9, UpdatedAt: now},  // recent → keep
		{ClientID: "ret_a", KeyID: 1, Face: "catalog", Day: dayStr(0), Count: 1, UpdatedAt: now},    // today → keep
	}
	if err := testDB.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Dry-run: count the prunable rows without deleting.
	would, err := repo.CountUsageBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if would != 2 {
		t.Fatalf("would-delete = %d, want 2 (the two rows older than cutoff)", would)
	}

	deleted, err := repo.PruneUsageBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}

	var remaining []DeveloperAPIUsage
	if err := testDB.Where("client_id = ?", "ret_a").Order("day ASC").Find(&remaining).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(remaining) != 3 {
		t.Fatalf("remaining = %d rows, want 3 (cutoff, −10d, today)", len(remaining))
	}
	for _, r := range remaining {
		if r.Day < cutoff {
			t.Errorf("row day %s survived but is older than cutoff %s", r.Day, cutoff)
		}
	}

	// Re-running the prune is a no-op — nothing remains older than the cutoff.
	again, err := repo.PruneUsageBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("prune again: %v", err)
	}
	if again != 0 {
		t.Errorf("second prune deleted = %d, want 0", again)
	}
}
