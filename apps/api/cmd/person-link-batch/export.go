package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

var exportHeader = []string{
	"a_id", "b_id", "a_name", "b_name", "a_source", "b_source",
	"a_credits", "b_credits", "co_credit_works", "a_aliases", "b_aliases",
	"decision", "notes",
}

func runExport(db *gorm.DB, out string) error {
	rows, err := loadCandidates(db, model.CandidateReasonAliasDeclared)
	if err != nil {
		return err
	}
	classify, err := aliasClassifier(db, rows)
	if err != nil {
		return err
	}
	var pending []candidateRow
	var ids []int64
	for _, r := range rows {
		if classify(r) == "" {
			pending = append(pending, r)
			ids = append(ids, r.AID, r.BID)
		}
	}

	sources := loadSources(db, ids)
	credits := loadCreditCounts(db, ids)
	aliases := loadAliasSamples(db, ids)

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	fmt.Fprintln(bw, strings.Join(exportHeader, "\t"))
	for _, r := range pending {
		fmt.Fprintln(bw, strings.Join([]string{
			itoa(r.AID), itoa(r.BID), clean(r.AName), clean(r.BName),
			srcLabel(sources[r.AID]), srcLabel(sources[r.BID]),
			itoa(credits[r.AID]), itoa(credits[r.BID]), "0",
			clean(strings.Join(aliases[r.AID], " | ")), clean(strings.Join(aliases[r.BID], " | ")),
			"", "",
		}, "\t"))
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d T2 rows (of %d alias_declared pending)\n", out, len(pending), len(rows))
	return nil
}

func loadSources(db *gorm.DB, ids []int64) map[int64]int16 {
	m := map[int64]int16{}
	if len(ids) == 0 {
		return m
	}
	var rows []struct {
		ID  int64 `gorm:"column:entity_id"`
		Src int16 `gorm:"column:source_id"`
	}
	_ = db.Raw(`SELECT entity_id, min(source_id) AS source_id FROM catalog_external_ref
		WHERE entity_type = ? AND link_kind = ? AND matched_by LIKE 'rule:%-import' AND entity_id IN ?
		GROUP BY entity_id`, model.EntityTypeCreditName, model.LinkKindExact, ids).Scan(&rows).Error
	for _, r := range rows {
		m[r.ID] = r.Src
	}
	return m
}

func loadCreditCounts(db *gorm.DB, ids []int64) map[int64]int64 {
	m := map[int64]int64{}
	if len(ids) == 0 {
		return m
	}
	var rows []struct {
		ID    int64 `gorm:"column:credit_name_id"`
		Count int64 `gorm:"column:count"`
	}
	_ = db.Raw(`SELECT credit_name_id, count(*) AS count FROM catalog_credit
		WHERE credit_name_id IN ? GROUP BY credit_name_id`, ids).Scan(&rows).Error
	for _, r := range rows {
		m[r.ID] = r.Count
	}
	return m
}

func loadAliasSamples(db *gorm.DB, ids []int64) map[int64][]string {
	m := map[int64][]string{}
	if len(ids) == 0 {
		return m
	}
	var rows []struct {
		Owner int64  `gorm:"column:credit_name_id"`
		Name  string `gorm:"column:name"`
	}
	_ = db.Raw(`SELECT credit_name_id, name FROM catalog_name_alias
		WHERE credit_name_id IN ? ORDER BY credit_name_id, name`, ids).Scan(&rows).Error
	for _, r := range rows {
		if len(m[r.Owner]) < 5 {
			m[r.Owner] = append(m[r.Owner], r.Name)
		}
	}
	return m
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func clean(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}

func srcLabel(id int16) string {
	switch id {
	case 3:
		return "bangumi"
	case 4:
		return "dlsite"
	case 5:
		return "eg"
	case 0:
		return ""
	default:
		return "src" + strconv.Itoa(int(id))
	}
}
