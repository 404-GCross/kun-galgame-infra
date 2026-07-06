// Package bangumienrich enriches bid-anchored galgames from the Bangumi
// Silver layer (src_bangumi.subject) — the first Silver→product data flow.
//
// Deliberately conservative (doc 17 §6.1 field-trust table, second-source
// first battle): Chinese name/intro FILL ONLY WHEN EMPTY, aliases APPEND
// ONLY, score/rank land in the galgame_bangumi_meta narrow table. Any
// non-empty current value — user- or VNDB-sourced — is never touched. The
// config-driven survivorship engine is deferred to the Gold enrichment
// steps; this is stricter than it on purpose.
//
// Write discipline is APPROACH B (the cmd/normalize-galgame-intro /
// vndb_sync pattern): bulk writers bypass the service layer, and for every
// changed galgame one transaction UPDATEs the live columns AND jsonb-patches
// the LATEST revision's snapshot so live == latest snapshot stays true —
// WITHOUT minting a new revision (a batch import must not create thousands
// of revisions). The meta narrow table is source-owned and needs no revision
// bookkeeping. Dry-run is the default; --apply opts into writes; a
// reindex-search run afterwards is mandatory (invariant 21).
package bangumienrich

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	srcb "api/internal/platform/catalog/srcbangumi"
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// Stats is the run summary. filled/appended/upserted count actual writes;
// unchanged counts matched rows that needed nothing.
type Stats struct {
	Matched         int `json:"matched"`
	WrongType       int `json:"wrong_type"`
	MissingInDump   int `json:"missing_in_dump"`
	NameFilled      int `json:"name_filled"`
	IntroFilled     int `json:"intro_filled"`
	AliasesAppended int `json:"aliases_appended"`
	MetaUpserted    int `json:"meta_upserted"`
	Unchanged       int `json:"unchanged"`
}

// Options controls a run.
type Options struct {
	DryRun bool
	Limit  int
}

const bangumiGameType = 4

// Run enriches every galgame whose bid resolves to a type-4 (game) subject
// in src_bangumi. wikiDB is the write side (kun_galgame_wiki), catalogDB the
// read side (kun_catalog, Silver schema).
func Run(wikiDB, catalogDB *gorm.DB, opts Options) (*Stats, error) {
	stats := &Stats{}

	// The candidate set: every bid-anchored galgame.
	type anchor struct {
		ID        int
		BID       int    `gorm:"column:bid"`
		NameEnUS  string `gorm:"column:name_en_us"`
		NameJaJP  string `gorm:"column:name_ja_jp"`
		NameZhCN  string `gorm:"column:name_zh_cn"`
		NameZhTW  string `gorm:"column:name_zh_tw"`
		IntroZhCN string `gorm:"column:intro_zh_cn"`
	}
	var anchors []anchor
	q := wikiDB.Table("galgame").
		Select("id, bid, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw, intro_zh_cn").
		Where("bid IS NOT NULL").Order("id")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if err := q.Scan(&anchors).Error; err != nil {
		return nil, fmt.Errorf("load bid anchors: %w", err)
	}

	// Batch-load the referenced subjects from Silver.
	bids := make([]int, 0, len(anchors))
	for _, a := range anchors {
		bids = append(bids, a.BID)
	}
	subjects := map[int64]srcb.Subject{}
	for start := 0; start < len(bids); start += 1000 {
		end := min(start+1000, len(bids))
		var batch []srcb.Subject
		if err := catalogDB.Where("id IN ?", bids[start:end]).Find(&batch).Error; err != nil {
			return nil, fmt.Errorf("load src_bangumi subjects: %w", err)
		}
		for _, s := range batch {
			subjects[s.ID] = s
		}
	}

	// Existing aliases and meta rows, for append-dedup and unchanged
	// detection.
	galgameIDs := make([]int, 0, len(anchors))
	for _, a := range anchors {
		galgameIDs = append(galgameIDs, a.ID)
	}
	aliasSet := map[int]map[string]bool{}
	for start := 0; start < len(galgameIDs); start += 1000 {
		end := min(start+1000, len(galgameIDs))
		var rows []model.GalgameAlias
		if err := wikiDB.Where("galgame_id IN ?", galgameIDs[start:end]).Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("load aliases: %w", err)
		}
		for _, r := range rows {
			if aliasSet[r.GalgameID] == nil {
				aliasSet[r.GalgameID] = map[string]bool{}
			}
			aliasSet[r.GalgameID][r.Name] = true
		}
	}
	existingMeta := map[int]model.GalgameBangumiMeta{}
	{
		var rows []model.GalgameBangumiMeta
		if err := wikiDB.Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("load existing meta: %w", err)
		}
		for _, r := range rows {
			existingMeta[r.GalgameID] = r
		}
	}

	now := time.Now()
	for _, a := range anchors {
		s, ok := subjects[int64(a.BID)]
		if !ok {
			stats.MissingInDump++
			continue
		}
		// Gate: a bid pointing at a non-game subject (anime page, OST,
		// original novel...) must not leak its fields into a galgame — it
		// belongs to the audit's wrong-type bucket.
		if s.Type != bangumiGameType {
			stats.WrongType++
			continue
		}
		stats.Matched++

		// Fill-only-when-empty scalars. This guard is the user-data
		// protection: any non-empty current value (user- OR vndb-sourced)
		// is never touched.
		scalarUpdates := map[string]string{}
		if a.NameZhCN == "" && s.NameCN != "" {
			scalarUpdates["name_zh_cn"] = s.NameCN
			a.NameZhCN = s.NameCN
		}
		if a.IntroZhCN == "" && strings.TrimSpace(s.Summary) != "" {
			scalarUpdates["intro_zh_cn"] = s.Summary
		}

		// Append-only aliases: infobox 别名 array items + name_cn when it
		// is not already one of the galgame's display names. Dedup by the
		// (galgame_id, name) key (matches idx_galgame_alias_unique).
		names := map[string]bool{
			a.NameEnUS: true, a.NameJaJP: true, a.NameZhCN: true, a.NameZhTW: true,
		}
		if aliasSet[a.ID] == nil {
			aliasSet[a.ID] = map[string]bool{}
		}
		var newAliases []string
		for _, candidate := range aliasCandidates(s) {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" || names[candidate] || aliasSet[a.ID][candidate] {
				continue
			}
			aliasSet[a.ID][candidate] = true
			newAliases = append(newAliases, candidate)
		}

		// Score/rank narrow table: whole-row refresh; unchanged rows are
		// skipped so reruns report zero writes. Source-owned data — no
		// revision bookkeeping.
		var metaChange *model.GalgameBangumiMeta
		meta := model.GalgameBangumiMeta{
			GalgameID: a.ID, BID: a.BID,
			Score: s.Score, Rank: s.Rank, Total: ratingTotal(s.ScoreDetails),
			NSFW: s.NSFW, SyncedAt: now,
		}
		if prev, ok := existingMeta[a.ID]; !ok || prev.BID != meta.BID || prev.Score != meta.Score ||
			prev.Rank != meta.Rank || prev.Total != meta.Total || prev.NSFW != meta.NSFW {
			metaChange = &meta
		}

		if len(scalarUpdates) == 0 && len(newAliases) == 0 && metaChange == nil {
			stats.Unchanged++
			continue
		}
		if !opts.DryRun {
			if err := applyOne(wikiDB, a.ID, scalarUpdates, newAliases, metaChange); err != nil {
				return nil, fmt.Errorf("apply galgame %d: %w", a.ID, err)
			}
		}
		if _, ok := scalarUpdates["name_zh_cn"]; ok {
			stats.NameFilled++
		}
		if _, ok := scalarUpdates["intro_zh_cn"]; ok {
			stats.IntroFilled++
		}
		stats.AliasesAppended += len(newAliases)
		if metaChange != nil {
			stats.MetaUpserted++
		}
	}
	return stats, nil
}

// applyOne writes one galgame's enrichment in a single transaction using
// APPROACH B: live columns are updated AND the LATEST revision's snapshot is
// jsonb-patched to match, so live == latest snapshot holds without minting a
// new revision. Galgames with no revisions simply skip the patch.
func applyOne(wikiDB *gorm.DB, galgameID int, scalars map[string]string, newAliases []string, meta *model.GalgameBangumiMeta) error {
	return wikiDB.Transaction(func(tx *gorm.DB) error {
		for column, value := range scalars {
			if err := tx.Exec(`UPDATE galgame SET `+column+` = ? WHERE id = ?`, value, galgameID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`
				UPDATE galgame_revision
				SET snapshot = jsonb_set(snapshot, ?::text[], to_jsonb(?::text), true)
				WHERE galgame_id = ?
				  AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)
			`, "{"+column+"}", value, galgameID, galgameID).Error; err != nil {
				return err
			}
		}
		if len(newAliases) > 0 {
			for _, name := range newAliases {
				if err := tx.Exec(`
					INSERT INTO galgame_alias (galgame_id, name, created, updated)
					VALUES (?, ?, now(), now())
					ON CONFLICT (galgame_id, name) DO NOTHING
				`, galgameID, name).Error; err != nil {
					return err
				}
			}
			appended, err := json.Marshal(newAliases)
			if err != nil {
				return err
			}
			if err := tx.Exec(`
				UPDATE galgame_revision
				SET snapshot = jsonb_set(snapshot, '{aliases}',
				        COALESCE(snapshot->'aliases', '[]'::jsonb) || ?::jsonb, true)
				WHERE galgame_id = ?
				  AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)
			`, string(appended), galgameID, galgameID).Error; err != nil {
				return err
			}
		}
		if meta != nil {
			if err := upsertMeta(tx, *meta); err != nil {
				return err
			}
		}
		return nil
	})
}

// aliasCandidates extracts alias strings from a Silver subject: the 别名
// infobox array items plus name_cn when it differs from the subject's main
// name. The parsed-infobox JSON uses Go-style capitalized keys
// ("Fields"/"Key"/"Items"/"Value") — pinned by the step-09 shape.
func aliasCandidates(s srcb.Subject) []string {
	var out []string
	if len(s.InfoboxParsed) > 0 {
		var box struct {
			Fields []struct {
				Key   string `json:"Key"`
				Items []struct {
					Value string `json:"Value"`
				} `json:"Items"`
			} `json:"Fields"`
		}
		if err := json.Unmarshal(s.InfoboxParsed, &box); err == nil {
			for _, f := range box.Fields {
				if f.Key != "别名" {
					continue
				}
				for _, item := range f.Items {
					out = append(out, item.Value)
				}
			}
		}
	}
	if s.NameCN != "" && s.NameCN != s.Name {
		out = append(out, s.NameCN)
	}
	return out
}

// ratingTotal sums the score_details buckets ({"1": n, ..., "10": n}).
func ratingTotal(details []byte) int {
	if len(details) == 0 {
		return 0
	}
	var buckets map[string]int
	if err := json.Unmarshal(details, &buckets); err != nil {
		return 0
	}
	total := 0
	for _, n := range buckets {
		total += n
	}
	return total
}

func upsertMeta(db *gorm.DB, meta model.GalgameBangumiMeta) error {
	return db.Exec(`
		INSERT INTO galgame_bangumi_meta (galgame_id, bid, score, rank, total, nsfw, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (galgame_id) DO UPDATE SET
		    bid = EXCLUDED.bid, score = EXCLUDED.score, rank = EXCLUDED.rank,
		    total = EXCLUDED.total, nsfw = EXCLUDED.nsfw, synced_at = EXCLUDED.synced_at
	`, meta.GalgameID, meta.BID, meta.Score, meta.Rank, meta.Total, meta.NSFW, meta.SyncedAt).Error
}
