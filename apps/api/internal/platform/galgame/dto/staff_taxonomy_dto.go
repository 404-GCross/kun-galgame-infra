package dto

// Staff taxonomy READ-BACK DTOs (A2-1e area B, refs/proj/135 / doc 106 R5+R11).
//
// These serve the two admin consoles' EDIT forms — moyu's taxonomy page and
// kungal's admin taxonomy tab — on the surviving `/api` staff face. Two design
// rules explain every field below:
//
//  1. **The read-back mirrors the WRITE payload, field for field.** Each shape
//     is exactly the corresponding Update*Request's editable set. That is the
//     whole point: these consoles send a full replacement payload (a nil field
//     means "keep", a present one means "replace"), so any field they cannot
//     read back is a field they silently WIPE on every save. Both consoles do
//     that today with `alias` (and moyu additionally with tag/official
//     `description`), because the list rows they prefill from do not carry it.
//
//  2. **wiki id key space, end to end.** The ids here are galgame_* PKs — the
//     same ones the write ops take and the same ones the revision history is
//     addressed by. The public browse lane migrates to catalog ids (P2/R1),
//     but the staff edit lane stays on wiki ids until the editing engine
//     retires this face, and mixing the two key spaces mid-lane is the exact
//     failure R11 rules against.
//
// Nothing here is a public contract: the `/api` face is staff-only (jwtAuth +
// galgame.taxonomy.edit_any) and carries no spec.

// StaffTagRecord is the tag edit form's read-back — the UpdateTagRequest
// editable set plus the id.
type StaffTagRecord struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Alias       []string `json:"alias"`
}

// StaffOfficialRecord is the official (会社) edit form's read-back. `original`
// and `description` are the two the list rows never carry, which is why the
// forum console already pays for a detail round-trip here.
type StaffOfficialRecord struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Original    string   `json:"original"`
	Link        string   `json:"link"`
	Lang        string   `json:"lang"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Alias       []string `json:"alias"`
}

// StaffEngineRecord is the engine edit form's read-back.
type StaffEngineRecord struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Alias       []string `json:"alias"`
}

// StaffSeriesRecord is the series edit form's read-back. galgame_ids is the
// membership the update op replaces wholesale — a series edit that cannot read
// it back empties the series.
type StaffSeriesRecord struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	GalgameIDs  []int  `json:"galgame_ids"`
}

// StaffTaxonomyListItem is one row of a staff search/browse response. It is the
// IDENTITY subset only — a console picks a row here and then reads the full
// record by id, so the list stays cheap and there is exactly one shape that
// promises "everything the form needs".
type StaffTaxonomyListItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
