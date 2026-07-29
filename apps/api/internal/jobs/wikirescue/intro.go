package wikirescue

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// parkedTagIntro is a tag description whose tag never converged onto the
// canonical vocabulary. The charter accepts this: the wiki tag layer itself is
// discarded (charter ruling 3), and the doc-70 vocabulary pipeline owns catalog_tag — this
// step is forbidden from minting tag rows to make a description fit.
type parkedTagIntro struct {
	TagID       int64  `json:"galgame_tag_id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
}

// stepTagIntro rescues galgame_tag.description onto catalog_tag_intro for the
// tags whose name matches a canonical catalog_tag exactly (case-insensitively).
func (r *Runner) stepTagIntro(ctx context.Context) (Stats, error) {
	st := Stats{Step: "d"}

	type wikiTag struct {
		ID          int64
		Name        string
		Category    string
		Description string
	}
	var tags []wikiTag
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, name, category, description FROM galgame_tag
		 WHERE coalesce(description, '') <> '' ORDER BY id`).Scan(&tags).Error; err != nil {
		return st, fmt.Errorf("read galgame_tag: %w", err)
	}
	st.Source = len(tags)

	type catTag struct {
		ID   int64
		Name string
	}
	var canon []catTag
	if err := r.catalog.WithContext(ctx).Raw(`SELECT id, name FROM catalog_tag`).Scan(&canon).Error; err != nil {
		return st, fmt.Errorf("read catalog_tag: %w", err)
	}
	// Ambiguous folds would make the match non-deterministic, so a name that
	// folds to the same key from two canonical rows is dropped from the index.
	byName := make(map[string]int64, len(canon))
	dupes := map[string]bool{}
	for _, c := range canon {
		k := strings.ToLower(c.Name)
		if _, seen := byName[k]; seen {
			dupes[k] = true
			continue
		}
		byName[k] = c.ID
	}

	now := time.Now().UTC()
	rows := make([][]any, 0, len(tags))
	parked := make([]parkedTagIntro, 0)
	seen := map[int64]struct{}{}
	for _, t := range tags {
		k := strings.ToLower(t.Name)
		id, ok := byName[k]
		if !ok || dupes[k] {
			reason := "no canonical catalog_tag with this name"
			if dupes[k] {
				reason = "ambiguous: several canonical tags fold to this name"
			}
			parked = append(parked, parkedTagIntro{TagID: t.ID, Name: t.Name, Category: t.Category, Description: t.Description, Reason: reason})
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		rows = append(rows, []any{id, langZhHans, t.Description, r.wikiSrc, now, now})
	}
	st.Anchored = len(tags) - len(parked)
	st.Parked = len(parked)
	st.Planned = len(rows)

	if err := r.park("d-tag-intros", parked); err != nil {
		return st, err
	}
	if !r.opts.Apply {
		return st, nil
	}
	landed, err := insertReturning(r.catalog.WithContext(ctx), "catalog_tag_intro",
		[]string{"tag_id", "lang", "intro", "source_id", "created_at", "updated_at"}, "", rows)
	if err != nil {
		return st, err
	}
	st.Written = len(landed)
	return st, nil
}

// parkedLabelIntro is an official description with no catalog_label bridge.
type parkedLabelIntro struct {
	OfficialID  int64  `json:"galgame_official_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// stepLabelIntro rescues galgame_official.description onto catalog_label_intro.
// Every one of these officials already carries a catalog_label_id bridge, so
// this is a direct insert with no matching to do.
func (r *Runner) stepLabelIntro(ctx context.Context) (Stats, error) {
	st := Stats{Step: "e"}

	type off struct {
		ID             int64
		Name           string
		Description    string
		CatalogLabelID *int64
	}
	var offs []off
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, name, description, catalog_label_id FROM galgame_official
		 WHERE coalesce(description, '') <> '' ORDER BY id`).Scan(&offs).Error; err != nil {
		return st, fmt.Errorf("read galgame_official: %w", err)
	}
	st.Source = len(offs)

	now := time.Now().UTC()
	rows := make([][]any, 0, len(offs))
	parked := make([]parkedLabelIntro, 0)
	seen := map[int64]struct{}{}
	for _, o := range offs {
		if o.CatalogLabelID == nil || *o.CatalogLabelID == 0 {
			parked = append(parked, parkedLabelIntro{OfficialID: o.ID, Name: o.Name, Description: o.Description})
			continue
		}
		if _, dup := seen[*o.CatalogLabelID]; dup {
			continue
		}
		seen[*o.CatalogLabelID] = struct{}{}
		rows = append(rows, []any{*o.CatalogLabelID, langZhHans, o.Description, r.wikiSrc, now, now})
	}
	st.Anchored = len(offs) - len(parked)
	st.Parked = len(parked)
	st.Planned = len(rows)

	if err := r.park("e-label-intros", parked); err != nil {
		return st, err
	}
	if !r.opts.Apply {
		return st, nil
	}
	landed, err := insertReturning(r.catalog.WithContext(ctx), "catalog_label_intro",
		[]string{"label_id", "lang", "intro", "source_id", "created_at", "updated_at"}, "", rows)
	if err != nil {
		return st, err
	}
	st.Written = len(landed)
	return st, nil
}
