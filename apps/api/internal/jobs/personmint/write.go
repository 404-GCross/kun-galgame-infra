package personmint

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type provEntry struct {
	Source string `json:"source"`
	At     string `json:"at"`
}

type writer struct {
	db      *gorm.DB
	env     *environment
	stats   *Stats
	apply   bool
	claimed map[anchorKey]string
}

func (w *writer) mint(ctx context.Context, p *mintPlan) error {
	if w.claimed == nil {
		w.claimed = map[anchorKey]string{}
	}
	for _, a := range p.Anchors {
		if other, dup := w.claimed[a]; dup {
			return fmt.Errorf("et=0 anchor (source %d, %s) is claimed by clusters %s and %s: the evidence graph contradicts itself, refusing to write",
				a.SourceID, a.ExternalID, other, p.ClusterID)
		}
		w.claimed[a] = p.ClusterID
	}
	if p.Conflict != nil {
		w.stats.GenderConflicts++
		w.stats.Conflicts = append(w.stats.Conflicts, *p.Conflict)
	}
	if p.BirthConflict {
		w.stats.BirthConflicts++
	}

	fields := w.planFields(p)
	if p.HostID == 0 {
		w.stats.WouldCreatePerson++
	}
	w.stats.WouldLink += len(p.LinkFill)
	w.stats.LinksAlready += p.LinksAlready
	w.stats.WouldAnchor += len(p.AnchorsNew)
	w.stats.AnchorsAlready += p.AnchorsAlready
	w.collect(p)

	if !w.apply {
		return nil
	}
	return w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		personID := p.HostID
		if personID == 0 {
			id, err := w.createPerson(ctx, tx, p, fields)
			if err != nil {
				return err
			}
			personID = id
		} else if len(fields.updates) > 0 {
			if err := tx.WithContext(ctx).Table("catalog_person").Where("id = ?", personID).
				Updates(fields.updateMap()).Error; err != nil {
				return fmt.Errorf("update person %d: %w", personID, err)
			}
			w.stats.PersonsUpdated++
		}
		if err := w.linkMembers(ctx, tx, p, personID); err != nil {
			return err
		}
		return w.writeAnchors(ctx, tx, p, personID)
	})
}

type personFields struct {
	updates map[string]any
	prov    map[string]json.RawMessage
	now     string
}

func (f *personFields) updateMap() map[string]any {
	out := make(map[string]any, len(f.updates)+2)
	for k, v := range f.updates {
		out[k] = v
	}
	if len(f.prov) > 0 {
		merged, _ := json.Marshal(f.prov)
		out["field_provenance"] = datatypes.JSON(merged)
	}
	out["updated_at"] = time.Now()
	return out
}

func (w *writer) planFields(p *mintPlan) *personFields {
	f := &personFields{updates: map[string]any{}, prov: map[string]json.RawMessage{}, now: time.Now().UTC().Format(time.RFC3339)}
	if p.Host != nil && len(p.Host.FieldProvenance) > 0 {
		_ = json.Unmarshal(p.Host.FieldProvenance, &f.prov)
	}
	set := func(col string, cur, val *int16, source string) {
		if val == nil {
			return
		}
		if cur != nil {
			return
		}
		f.updates[col] = *val
		entry, _ := json.Marshal(provEntry{Source: source, At: f.now})
		var arr []json.RawMessage
		if raw, ok := f.prov[col]; ok {
			_ = json.Unmarshal(raw, &arr)
		}
		merged, _ := json.Marshal(append([]json.RawMessage{entry}, arr...))
		f.prov[col] = merged
	}
	var curGender, curY, curM, curD *int16
	if p.Host != nil {
		curGender, curY, curM, curD = p.Host.Gender, p.Host.BirthY, p.Host.BirthM, p.Host.BirthD
	}
	set("gender", curGender, p.Gender, p.GenderFrom)
	set("birth_y", curY, p.BirthY, sourceBangumi)
	set("birth_m", curM, p.BirthM, sourceBangumi)
	set("birth_d", curD, p.BirthD, sourceBangumi)

	if _, wrote := f.updates["gender"]; wrote {
		w.stats.WouldSetGender++
	} else if p.Gender != nil && curGender != nil {
		w.stats.GenderKept++
	}
	_, y := f.updates["birth_y"]
	_, m := f.updates["birth_m"]
	_, d := f.updates["birth_d"]
	switch {
	case y || m || d:
		w.stats.WouldSetBirth++
	case p.BirthY != nil || p.BirthM != nil || p.BirthD != nil:
		w.stats.BirthKept++
	}

	if p.Host != nil {
		if p.Host.DisplayName == "" {
			f.updates["display_name"] = p.DisplayName
		}
		if p.Host.PrimaryCreditNameID == nil {
			f.updates["primary_credit_name_id"] = p.PrimaryID
		}
	}
	return f
}

func (w *writer) createPerson(ctx context.Context, tx *gorm.DB, p *mintPlan, f *personFields) (int64, error) {
	person := &model.CatalogPerson{
		DisplayName:         p.DisplayName,
		PrimaryCreditNameID: &p.PrimaryID,
		Gender:              p.Gender,
		BirthY:              p.BirthY,
		BirthM:              p.BirthM,
		BirthD:              p.BirthD,
		FieldProvenance:     datatypes.JSON("{}"),
	}
	if len(f.prov) > 0 {
		merged, _ := json.Marshal(f.prov)
		person.FieldProvenance = datatypes.JSON(merged)
	}
	if err := tx.WithContext(ctx).Create(person).Error; err != nil {
		return 0, fmt.Errorf("create person for cluster %s: %w", p.ClusterID, err)
	}
	w.stats.PersonsCreated++
	return person.ID, nil
}

func (w *writer) linkMembers(ctx context.Context, tx *gorm.DB, p *mintPlan, personID int64) error {
	if len(p.LinkFill) == 0 {
		return nil
	}
	res := tx.WithContext(ctx).Exec(
		`UPDATE catalog_credit_name SET person_id = ?, updated_at = now()
		 WHERE id IN ? AND person_id IS NULL`, personID, p.LinkFill)
	if res.Error != nil {
		return fmt.Errorf("link credit names of cluster %s: %w", p.ClusterID, res.Error)
	}
	w.stats.LinksWritten += int(res.RowsAffected)
	return nil
}

func (w *writer) writeAnchors(ctx context.Context, tx *gorm.DB, p *mintPlan, personID int64) error {
	for _, a := range p.AnchorsNew {
		res := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model.CatalogExternalRef{
			EntityType: model.EntityTypePerson,
			EntityID:   personID,
			SourceID:   a.SourceID,
			ExternalID: a.ExternalID,
			LinkKind:   model.LinkKindExact,
			MatchedBy:  matchedBy,
		})
		if res.Error != nil {
			return fmt.Errorf("anchor person %d to (%d,%s): %w", personID, a.SourceID, a.ExternalID, res.Error)
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("et=0 anchor (source %d, %s) of cluster %s is already owned by another person",
				a.SourceID, a.ExternalID, p.ClusterID)
		}
		w.stats.AnchorsWritten++
		w.env.et0Owner[a] = personID
	}
	return nil
}

func (w *writer) collect(p *mintPlan) {
	if len(w.stats.Samples) >= maxSamples {
		return
	}
	anchors := make([]string, 0, len(p.Anchors))
	for _, a := range p.Anchors {
		anchors = append(anchors, fmt.Sprintf("%d:%s", a.SourceID, a.ExternalID))
	}
	w.stats.Samples = append(w.stats.Samples, Sample{
		ClusterID: p.ClusterID, PersonID: p.HostID, Reused: p.HostID != 0,
		DisplayName: p.DisplayName, Members: p.Members, Names: p.Names, Anchors: anchors,
	})
}
