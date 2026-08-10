package bangumicommon

import (
	"embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed data/*.yml
var dataFS embed.FS

const (
	SubjectTypeBook  = 1
	SubjectTypeAnime = 2
	SubjectTypeMusic = 3
	SubjectTypeGame  = 4
	SubjectTypeReal  = 6
)

type StaffCategory struct {
	Order int    `yaml:"order"`
	EN    string `yaml:"en"`
	CN    string `yaml:"cn"`
}

type StaffPosition struct {
	EN         string          `yaml:"en"`
	CN         string          `yaml:"cn"`
	JP         string          `yaml:"jp"`
	Categories []StaffCategory `yaml:"categories"`
}

type Relation struct {
	EN   string `yaml:"en"`
	CN   string `yaml:"cn"`
	JP   string `yaml:"jp"`
	Desc string `yaml:"desc"`
}

type Platform struct {
	ID     int    `yaml:"id"`
	Type   string `yaml:"type"`
	TypeCN string `yaml:"type_cn"`
	Alias  string `yaml:"alias"`
}

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
