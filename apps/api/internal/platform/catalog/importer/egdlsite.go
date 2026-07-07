package importer

import (
	"fmt"
	"strings"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// The EG×DLsite rosetta wave (step 28, doc 17 R3/R5 cross-source payoff):
// EG.dlsite_id is EG's community link from an erogame to its DLsite SKU. For
// every such workno that resolves to a real DLsite work, we land the DLsite
// release under the RIGHT catalog work — PARSE FIRST, THEN MINT:
//
//   - already: the workno already carries a release anchor (an ASMR-wave
//     collision, or a step-28 re-run) → skip, count.
//   - attach: the EG game is already reconciled to a claimed wiki work W
//     (rule:eg-vndb-rosetta) → add the DLsite release to W (SKU anchor + maker
//     label + attribution edge + creater credits + a search-hint title). No
//     new work: the three-source identity chain wiki↔EG↔DLsite closes.
//   - mint: nobody claims it → mint an UNCLAIMED galgame work (EG-known erogame)
//     with the full step-14 kit PLUS a PROBABLE EG work-ref (the EG↔DLsite
//     identity is community structural data — R8 probable, promotion deferred).
//
// medium=galgame throughout: EG only catalogues erogames, so this dodges the
// "generic game medium" decision. mediumGalgame is the catalog_medium key id.
const (
	mediumGalgame int16 = 1
	ruleEGDLsite        = "rule:eg-dlsite-rosetta"
)

// EGDLsiteStats is the wave tally.
type EGDLsiteStats struct {
	Attached  int // releases attached to an existing claimed work
	Minted    int // new unclaimed galgame works
	Already   int // workno already has a release anchor
	Ambiguous int // dlsite_id claimed by >1 EG game (skipped)
	Missing   int // EG dlsite_id not found in the local DLsite staging

	ReleasesCreated     int
	TitlesCreated       int
	LabelsCreated       int
	NamesCreated        int
	CreditsWritten      int
	EdgesWritten        int
	EGRefsWritten       int // probable EG work-refs (mint path)
	Stubs               int
	SkippedUnmappedRole int
	Errors              int
}

// egdlItem is one hit workno with its disposition resolved.
type egdlItem struct {
	dw       dlWork
	egGame   int64
	workID   int64 // attach target; 0 = mint
	attach   bool
	nameFold string
}

// dlRow mirrors the DLsite staging columns plus the SQL-computed name fold.
type dlRow struct {
	Workno    string         `gorm:"column:workno"`
	WorkName  string         `gorm:"column:work_name"`
	Kana      string         `gorm:"column:kana"`
	MakerID   string         `gorm:"column:maker_id"`
	MakerName string         `gorm:"column:maker_name"`
	Age       string         `gorm:"column:age_category"`
	Regist    *time.Time     `gorm:"column:regist_date"`
	Creaters  datatypes.JSON `gorm:"column:creaters"`
	NameFold  string         `gorm:"column:name_fold"`
}

// RunEGDLsite executes the wave. dlsiteDB is the DLsite staging connection; the
// EG connection is im.eg.
func (im *Importer) RunEGDLsite(dlsiteDB *gorm.DB) (EGDLsiteStats, error) {
	var st EGDLsiteStats
	if im.eg == nil {
		return st, fmt.Errorf("eg connection required")
	}
	roleMap, err := im.roleMap(dlsiteSource)
	if err != nil {
		return st, err
	}
	relAnchor, err := im.loadAnchors(model.EntityTypeRelease)
	if err != nil {
		return st, err
	}
	labelAnchor, err := im.loadAnchors(model.EntityTypeLabel)
	if err != nil {
		return st, err
	}
	cnAnchor, err := im.loadAnchors(model.EntityTypeCreditName)
	if err != nil {
		return st, err
	}
	egWork, err := im.loadEGRosettaWorkMap()
	if err != nil {
		return st, err
	}

	// EG dlsite_id → the EG games claiming it (multiplicity guard input).
	dlsiteToEG, err := im.loadEGDlsiteClaims()
	if err != nil {
		return st, err
	}
	candidates := make([]string, 0, len(dlsiteToEG))
	for did, games := range dlsiteToEG {
		if len(games) > 1 {
			st.Ambiguous++ // one dlsite_id, several EG games → human review
			continue
		}
		candidates = append(candidates, did)
	}

	// The DLsite hit set among the candidate worknos.
	rows, err := loadDLWorks(dlsiteDB, candidates, im.limit)
	if err != nil {
		return st, err
	}
	found := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		found[r.Workno] = struct{}{}
	}
	for _, did := range candidates {
		if _, ok := found[did]; !ok {
			st.Missing++ // EG knows a dlsite_id the local staging does not carry
		}
	}

	// Partition + collect the shared entities to create.
	makers := map[string]dlNamed{}
	makerKind := map[string]int16{}
	creaters := map[string]dlNamed{}
	var attach, mint []egdlItem
	for _, r := range rows {
		if relAnchor[anchorKey(dlsiteSource, r.Workno)] != 0 {
			st.Already++
			continue
		}
		dw := parseDLRow(r, roleMap, creaters, &st)
		if r.MakerID != "" {
			if _, ok := makers[r.MakerID]; !ok {
				makers[r.MakerID] = dlNamed{ext: r.MakerID, name: firstNonEmptyStr(r.MakerName, r.MakerID)}
				makerKind[r.MakerID] = egdlLabelKind(r.MakerID)
			}
		}
		eg := dlsiteToEG[r.Workno][0]
		item := egdlItem{dw: dw, egGame: eg, nameFold: r.NameFold}
		if wid, ok := egWork[eg]; ok {
			item.attach, item.workID = true, wid
			attach = append(attach, item)
		} else {
			mint = append(mint, item)
		}
	}
	st.Attached = len(attach)
	st.Minted = len(mint)

	newLabels := filterNew(makers, labelAnchor, dlsiteSource)
	newNames := filterNew(creaters, cnAnchor, dlsiteSource)

	if im.dryRun {
		st.LabelsCreated = len(newLabels)
		st.NamesCreated = len(newNames)
		st.ReleasesCreated = len(attach) + len(mint)
		for _, it := range mint {
			if it.dw.stub {
				st.Stubs++
			}
			st.TitlesCreated++ // official
			st.EGRefsWritten++
			st.CreditsWritten += len(it.dw.credits)
		}
		for _, it := range attach {
			st.CreditsWritten += len(it.dw.credits)
			st.TitlesCreated++ // a would-be search-hint title (upper bound; fold check at apply)
		}
		st.EdgesWritten = len(attach) + len(mint) // upper bound (works carrying a maker)
		return st, nil
	}

	// Shared labels + creater credit names (own tx), then update anchors.
	if err := im.createEGDLShared(newLabels, newNames, makerKind, labelAnchor, cnAnchor); err != nil {
		return st, err
	}
	st.LabelsCreated = len(newLabels)
	st.NamesCreated = len(newNames)
	cnResolve := resolver(cnAnchor, dlsiteSource, nil)

	if err := im.mintEGDLWorks(mint, cnResolve, &st); err != nil {
		return st, err
	}
	if err := im.attachEGDLReleases(attach, cnResolve, &st); err != nil {
		return st, err
	}
	if err := im.emitEGDLEdges(append(attach, mint...), labelAnchor, makerKind, &st); err != nil {
		return st, err
	}
	return st, nil
}

// parseDLRow builds a dlWork and folds its per-creater credit plan into the
// shared creaters map (role-mapped; unmapped roles counted).
func parseDLRow(r dlRow, roleMap map[string]int64, creaters map[string]dlNamed, st *EGDLsiteStats) dlWork {
	dw := dlWork{
		workno: r.Workno, name: r.WorkName, kana: r.Kana, makerExt: r.MakerID,
		contentRating: dlContentRating(r.Age), stub: strings.TrimSpace(r.WorkName) == "",
	}
	dw.y, dw.m, dw.d = splitDate(r.Regist)
	for _, c := range parseCreaters(r.Creaters) {
		roleID, ok := roleMap[c.classification]
		if !ok {
			st.SkippedUnmappedRole++
			continue
		}
		if _, ok := creaters[c.id]; !ok {
			creaters[c.id] = dlNamed{ext: c.id, name: c.name}
		}
		dw.credits = append(dw.credits, dlCredit{createrExt: c.id, roleID: roleID})
	}
	return dw
}

// egdlLabelKind maps a DLsite maker id to a label kind. RG = doujin circle
// (RJ/doujin site); VG (DLsite pro/商業) and the rare BG = publisher. This is a
// minimal extension over dlLabelKind for the VJ commercial makers step 14's
// voice subset never carried — dlLabelKind (ASMR path) is left untouched.
func egdlLabelKind(makerID string) int16 {
	if strings.HasPrefix(makerID, "VG") || strings.HasPrefix(makerID, "BG") {
		return model.LabelKindPublisher
	}
	return model.LabelKindDoujinCircle
}
