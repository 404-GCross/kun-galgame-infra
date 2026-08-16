// Package provenance is the shared reading of the `field_provenance` jsonb
// column carried by the catalog head tables (work, label, character, person,
// credit_name): a map from column name to the writes recorded against it.
package provenance

import "encoding/json"

// Entry is one recorded write. The array under a column is newest-first — the
// head is the writer that last set the value, and that is what every consumer
// reads: merge survivorship, the importers that skip human-owned fields, and
// the character attribute read face.
type Entry struct {
	Source string `json:"source"`
	At     string `json:"at"`
}

// SourceUser and SourceCurated are the catalog_source keys that mean a person
// decided the value.
const (
	SourceUser    = "user"
	SourceCurated = "curated"
)

// HumanSources is deliberately narrower than imagegradesync's
// humanAuthoredSources, which also counts vndb: vndb is a hand-curated lane for
// image grades but a machine importer for field attribution. Merging the two
// sets would make every vndb-imported name unoverwritable.
func HumanSources() []string { return []string{SourceUser, SourceCurated} }

func IsHuman(source string) bool {
	return source == SourceUser || source == SourceCurated
}

// FirstSource is the source key of the most recent write to field, or "" when
// the document does not record one.
func FirstSource(doc []byte, field string) string {
	if len(doc) == 0 {
		return ""
	}
	var parsed map[string][]Entry
	if err := json.Unmarshal(doc, &parsed); err != nil {
		return ""
	}
	entries := parsed[field]
	if len(entries) == 0 {
		return ""
	}
	return entries[0].Source
}
