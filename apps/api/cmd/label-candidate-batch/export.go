package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"api/internal/platform/catalog/editspec"

	"gorm.io/gorm"
)

type dossier struct {
	AID      int64     `json:"a_id"`
	BID      int64     `json:"b_id"`
	SourceID int64     `json:"source_id"`
	TargetID int64     `json:"target_id"`
	A        labelSide `json:"a"`
	B        labelSide `json:"b"`
}

type labelSide struct {
	Name      string      `json:"name"`
	Latin     string      `json:"latin,omitempty"`
	Kind      int16       `json:"kind"`
	Import    bool        `json:"is_wiki_import"`
	Aliases   []string    `json:"aliases,omitempty"`
	Refs      []string    `json:"refs,omitempty"`
	WorkCount int         `json:"work_count"`
	Works     []workBrief `json:"works,omitempty"`
}

type workBrief struct {
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
	Src   string `json:"src,omitempty"`
}

func runExport(db *gorm.DB, out string) error {
	rows, err := loadPending(db)
	if err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	defer bw.Flush()

	enc := json.NewEncoder(bw)
	exported := 0
	for _, r := range rows {
		source, target, ok := direction(r)
		if r.SharedWorks > 0 && ok {
			continue
		}
		if !ok {
			source, target = r.BID, r.AID
		}
		a, err := loadSide(db, r.AID)
		if err != nil {
			return fmt.Errorf("label %d: %w", r.AID, err)
		}
		b, err := loadSide(db, r.BID)
		if err != nil {
			return fmt.Errorf("label %d: %w", r.BID, err)
		}
		if err := enc.Encode(dossier{AID: r.AID, BID: r.BID, SourceID: source, TargetID: target, A: a, B: b}); err != nil {
			return err
		}
		exported++
	}
	fmt.Fprintf(os.Stderr, "exported %d dossiers to %s (of %d pending)\n", exported, out, len(rows))
	return nil
}

func loadSide(db *gorm.DB, id int64) (labelSide, error) {
	var s labelSide
	var head struct {
		DisplayName string
		Latin       *string
		Kind        int16
		Note        string
	}
	if err := db.Raw(`SELECT l.display_name, l.latin, l.kind, l.note
		FROM catalog_label l WHERE l.id = ?`, id).Scan(&head).Error; err != nil {
		return s, err
	}
	s.Name = head.DisplayName
	if head.Latin != nil {
		s.Latin = *head.Latin
	}
	s.Kind = head.Kind
	s.Import = containsImportMark(head.Note)
	if err := db.Raw(`SELECT name FROM catalog_label_alias WHERE label_id = ? ORDER BY id LIMIT 20`, id).Scan(&s.Aliases).Error; err != nil {
		return s, err
	}
	if err := db.Raw(`SELECT src.key || ':' || r.external_id
		FROM catalog_external_ref r JOIN catalog_source src ON src.id = r.source_id
		WHERE r.entity_type = 3 AND r.entity_id = ? ORDER BY r.source_id LIMIT 20`, id).Scan(&s.Refs).Error; err != nil {
		return s, err
	}
	if err := db.Raw(`SELECT count(*) FROM catalog_work_label WHERE label_id = ?`, id).Scan(&s.WorkCount).Error; err != nil {
		return s, err
	}
	err := db.Raw(`SELECT
			coalesce((SELECT title FROM catalog_work_title wt WHERE wt.work_id = wl.work_id
				AND `+editspec.NotSuppressedWorkTitleSQL("wt")+` ORDER BY wt.id LIMIT 1), w.display_name) AS title,
			coalesce((SELECT min(released_y) FROM catalog_release rel WHERE rel.work_id = wl.work_id AND rel.released_y IS NOT NULL), 0) AS year,
			coalesce(src.key, '') AS src
		FROM catalog_work_label wl
		JOIN catalog_work w ON w.id = wl.work_id
		LEFT JOIN catalog_source src ON src.id = wl.source_id
		WHERE wl.label_id = ? ORDER BY wl.work_id LIMIT 10`, id).Scan(&s.Works).Error
	return s, err
}

func containsImportMark(note string) bool {
	return strings.Contains(note, importNoteMark)
}
