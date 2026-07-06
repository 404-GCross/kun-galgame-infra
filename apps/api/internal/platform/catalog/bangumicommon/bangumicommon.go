// Package bangumicommon exposes a pinned, embedded snapshot of the
// bangumi/common vocabulary files (staff positions, subject relations,
// subject platforms). The catalog registry seeds read from here, so seeding
// is self-contained and test-hermetic. Provenance and refresh procedure are
// documented in data/README.md.
package bangumicommon

import (
	"embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed data/*.yml
var dataFS embed.FS

// Bangumi subject type IDs, the outer key of every vocabulary map.
const (
	SubjectTypeBook  = 1
	SubjectTypeAnime = 2
	SubjectTypeMusic = 3
	SubjectTypeGame  = 4
	SubjectTypeReal  = 6
)

// StaffCategory groups staff positions on Bangumi's edit UI, e.g. game
// positions under "发行类" (producer).
type StaffCategory struct {
	Order int    `yaml:"order"`
	EN    string `yaml:"en"`
	CN    string `yaml:"cn"`
}

// StaffPosition is one credit position, e.g. subject type 4 (game) position
// 1001 = Developer/开发.
type StaffPosition struct {
	EN         string          `yaml:"en"`
	CN         string          `yaml:"cn"`
	JP         string          `yaml:"jp"`
	Categories []StaffCategory `yaml:"categories"`
}

// Relation is one subject↔subject relation type, e.g. subject type 4 (game)
// relation 1 = Adaptation/改编.
type Relation struct {
	EN   string `yaml:"en"`
	CN   string `yaml:"cn"`
	JP   string `yaml:"jp"`
	Desc string `yaml:"desc"`
}

// Platform is one release platform of a subject type, e.g. game platform
// 4 = PC.
type Platform struct {
	ID     int    `yaml:"id"`
	Type   string `yaml:"type"`
	TypeCN string `yaml:"type_cn"`
	Alias  string `yaml:"alias"`
}

// LoadStaffPositions returns staff positions keyed by subject type, then
// position ID.
func LoadStaffPositions() (map[int]map[int]StaffPosition, error) {
	var doc struct {
		Staffs map[int]map[int]StaffPosition `yaml:"staffs"`
	}
	if err := unmarshalData("data/subject_staffs.yml", &doc); err != nil {
		return nil, err
	}
	if len(doc.Staffs) == 0 {
		return nil, fmt.Errorf("bangumicommon: subject_staffs.yml: staffs section is empty")
	}
	return doc.Staffs, nil
}

// LoadRelations returns subject relation types keyed by subject type, then
// relation ID.
func LoadRelations() (map[int]map[int]Relation, error) {
	var doc struct {
		Relations map[int]map[int]Relation `yaml:"relations"`
	}
	if err := unmarshalData("data/subject_relations.yml", &doc); err != nil {
		return nil, err
	}
	if len(doc.Relations) == 0 {
		return nil, fmt.Errorf("bangumicommon: subject_relations.yml: relations section is empty")
	}
	return doc.Relations, nil
}

// LoadPlatforms returns platforms keyed by group, then platform ID. Groups
// are strings because upstream mixes numeric subject types ("1", "2", "4",
// "6") with the special groups "book_series" and "game_platforms".
func LoadPlatforms() (map[string]map[int]Platform, error) {
	var doc struct {
		Platforms map[string]map[int]Platform `yaml:"platforms"`
	}
	if err := unmarshalData("data/subject_platforms.yml", &doc); err != nil {
		return nil, err
	}
	if len(doc.Platforms) == 0 {
		return nil, fmt.Errorf("bangumicommon: subject_platforms.yml: platforms section is empty")
	}
	return doc.Platforms, nil
}

func unmarshalData(name string, out any) error {
	raw, err := dataFS.ReadFile(name)
	if err != nil {
		return fmt.Errorf("bangumicommon: read embedded %s: %w", name, err)
	}
	if err := yaml.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("bangumicommon: parse %s: %w", name, err)
	}
	return nil
}
