package devapi

import (
	"context"
	"testing"
	"time"
)

func TestPruneUsageBefore(t *testing.T) {
	cleanupSelf(t)
	repo := NewRepository(testDB)
	ctx := context.Background()

	now := time.Now().UTC()
	cutoff := RetentionCutoffDay(now)
	dayStr := func(offset int) string { return now.AddDate(0, 0, offset).Format("2006-01-02") }

	seed := []DeveloperAPIUsage{
		{ClientID: "ret_a", KeyID: 1, Face: "catalog", Day: dayStr(-500), Count: 5, UpdatedAt: now},
		{ClientID: "ret_a", KeyID: 1, Face: "catalog", Day: dayStr(-401), Count: 3, UpdatedAt: now},
		{ClientID: "ret_a", KeyID: 1, Face: "catalog", Day: cutoff, Count: 7, UpdatedAt: now},
		{ClientID: "ret_a", KeyID: 1, Face: "catalog", Day: dayStr(-10), Count: 9, UpdatedAt: now},
		{ClientID: "ret_a", KeyID: 1, Face: "catalog", Day: dayStr(0), Count: 1, UpdatedAt: now},
	}
	if err := testDB.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

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

	again, err := repo.PruneUsageBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("prune again: %v", err)
	}
	if again != 0 {
		t.Errorf("second prune deleted = %d, want 0", again)
	}
}
