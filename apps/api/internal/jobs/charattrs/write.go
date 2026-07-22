package charattrs

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Source keys recorded in field_provenance (and the survivorship priority
// ranks). VNDB outranks Bangumi: its attributes come from typed columns, more
// trustworthy than a regex over free-text infobox values (refs/proj/81
// survivorship). A field whose latest writer is NOT one of these (e.g. "user",
// a future human edit) is never overwritten by this pipeline.
const (
	sourceVNDB    = "vndb"
	sourceBangumi = "bangumi"
)

var pipelineRank = map[string]int{sourceVNDB: 2, sourceBangumi: 1}

// extraNamespaces keys the per-source long-tail buckets inside catalog_character
// extra. Only Bangumi contributes a long tail today.
const extraNamespaceBGM = "bgm"

// charState is the current persisted attribute state of a character, loaded
// alongside its source row so the write decision needs no extra query.
type charState struct {
	ID              int64          `gorm:"column:entity_id"`
	Month           *int16         `gorm:"column:birthday_month"`
	Day             *int16         `gorm:"column:birthday_day"`
	Blood           *int16         `gorm:"column:blood_type"`
	Height          *int16         `gorm:"column:height_cm"`
	Weight          *int16         `gorm:"column:weight_kg"`
	Bust            *int16         `gorm:"column:bust_cm"`
	Waist           *int16         `gorm:"column:waist_cm"`
	Hip             *int16         `gorm:"column:hip_cm"`
	Cup             *string        `gorm:"column:cup"`
	Gender          *int16         `gorm:"column:gender"`
	Extra           datatypes.JSON `gorm:"column:extra"`
	FieldProvenance datatypes.JSON `gorm:"column:field_provenance"`
}

type provEntry struct {
	Source string `json:"source"`
	At     string `json:"at"`
}

// charWriter accumulates one character's column + provenance + extra updates,
// then flushes a single UPDATE. It enforces the survivorship + user-protection
// + idempotency rules (see decide).
type charWriter struct {
	source      string
	now         string
	provDoc     map[string]json.RawMessage
	updates     map[string]any
	touchedProv bool
}

func newCharWriter(source, now string, prov datatypes.JSON) *charWriter {
	doc := map[string]json.RawMessage{}
	if len(prov) > 0 {
		_ = json.Unmarshal(prov, &doc)
	}
	return &charWriter{source: source, now: now, provDoc: doc, updates: map[string]any{}}
}

// latestWriter returns the source key of a field's most recent provenance entry
// (R8 array, latest first), or "" when the field has no provenance.
func (w *charWriter) latestWriter(col string) string {
	raw, ok := w.provDoc[col]
	if !ok {
		return ""
	}
	var arr []provEntry
	if json.Unmarshal(raw, &arr) != nil || len(arr) == 0 {
		return ""
	}
	return arr[0].Source
}

// decide implements the write rule: skip an idempotent no-op (same value, same
// writer — the second-pass zero-write); write when the column is empty; on a
// non-empty column write only when the current writer is a pipeline source AND
// this source's priority is >= it (vndb over bangumi; a source re-parsing its
// own field). A non-pipeline writer (user edit) is never overwritten.
func (w *charWriter) decide(col string, curNil, equal bool) bool {
	latest := w.latestWriter(col)
	if equal && latest == w.source {
		return false
	}
	if curNil {
		return true
	}
	lr, ok := pipelineRank[latest]
	if !ok {
		return false
	}
	return pipelineRank[w.source] >= lr
}

// recordProv prepends this run's (source, at) entry to a column's provenance
// array (latest first).
func (w *charWriter) recordProv(col string) {
	entry, _ := json.Marshal(provEntry{Source: w.source, At: w.now})
	var arr []json.RawMessage
	if raw, ok := w.provDoc[col]; ok {
		_ = json.Unmarshal(raw, &arr)
	}
	arr = append([]json.RawMessage{entry}, arr...)
	merged, _ := json.Marshal(arr)
	w.provDoc[col] = merged
	w.touchedProv = true
}

// i16 plans a nullable-int column write; returns whether the write was planned.
func (w *charWriter) i16(col string, cur, proposed *int16) bool {
	if proposed == nil {
		return false
	}
	equal := cur != nil && *cur == *proposed
	if !w.decide(col, cur == nil, equal) {
		return false
	}
	w.updates[col] = *proposed
	w.recordProv(col)
	return true
}

// str plans a nullable-string column write; returns whether it was planned.
func (w *charWriter) str(col string, cur, proposed *string) bool {
	if proposed == nil {
		return false
	}
	equal := cur != nil && *cur == *proposed
	if !w.decide(col, cur == nil, equal) {
		return false
	}
	w.updates[col] = *proposed
	w.recordProv(col)
	return true
}

// setExtraNamespace replaces the source's whole extra namespace with the
// freshly computed long-tail (idempotent: no UPDATE when the canonical JSON is
// unchanged). Other namespaces are preserved. Returns whether extra changed.
func (w *charWriter) setExtraNamespace(cur datatypes.JSON, ns string, val map[string]any) bool {
	doc := map[string]json.RawMessage{}
	if len(cur) > 0 {
		_ = json.Unmarshal(cur, &doc)
	}
	old, had := doc[ns]
	if len(val) == 0 {
		if !had {
			return false
		}
		delete(doc, ns)
	} else {
		newNS, _ := json.Marshal(val)
		if had && canonJSON(old) == canonJSON(newNS) {
			return false
		}
		doc[ns] = newNS
	}
	merged, _ := json.Marshal(doc)
	w.updates["extra"] = datatypes.JSON(merged)
	return true
}

// flush issues the single UPDATE when anything was planned.
func (w *charWriter) flush(ctx context.Context, db *gorm.DB, id int64) error {
	if len(w.updates) == 0 {
		return nil
	}
	if w.touchedProv {
		merged, _ := json.Marshal(w.provDoc)
		w.updates["field_provenance"] = datatypes.JSON(merged)
	}
	w.updates["updated_at"] = time.Now()
	return db.WithContext(ctx).Table("catalog_character").Where("id = ?", id).Updates(w.updates).Error
}

// canonJSON normalizes a JSON blob to Go's key-sorted marshaling so two
// semantically-equal documents compare byte-equal (jsonb key order is not
// stable across a round-trip).
func canonJSON(raw []byte) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}
