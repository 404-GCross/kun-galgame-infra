package charattrs

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	sourceVNDB    = "vndb"
	sourceBangumi = "bangumi"
)

var pipelineRank = map[string]int{sourceVNDB: 2, sourceBangumi: 1}

const extraNamespaceBGM = "bgm"

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

func canonJSON(raw []byte) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}
