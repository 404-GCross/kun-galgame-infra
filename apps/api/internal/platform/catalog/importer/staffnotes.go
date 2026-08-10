package importer

import (
	"maps"
	"regexp"
	"slices"
	"strings"
)

// VNDB's ten-role staff vocabulary is far coarser than its data: the `staff`
// role is a catch-all whose free-text note carries the REAL position
// ("Programming", "OP lyrics", "Special thanks", …), and the wholesale
// staff→其他 mapping parked 64k noted credits in the unmapped bucket — a
// quarter of them beside a classified credit for the same name on the same
// work (wave: refine-staff-notes).
//
// This file is the single source for refining that bucket by note, shared by
// the two writers that must agree:
//
//   - runVNDBCredits refines at plan time, so a re-import PLANS the refined
//     role and can never re-insert the 其他 edge a backfill moved away (the
//     unique index includes role_id, so a moved row would not conflict with a
//     其他 re-insert — the importer-rerun-resurrection trap);
//   - cmd/refine-staff-notes moves the existing edges with the same resolver.
//
// Mapping discipline: EXACT normalized note match (lowercased, trimmed) onto
// an EXISTING vocabulary role — no new roles, no substring guessing. A
// composite note ("Planning, script") splits into components only when EVERY
// component is itself a table key — one unknown word and the whole note stays
// in the bucket rather than half-mapping (RefineVNDBStaffRoles).
// Notably `Script`/`Scripting` map to 程序, not 脚本: VNDB credits writers
// under its dedicated scenario role, so a staff-role Script note is engine
// scripting (スクリプト) — sampled holders overlap same-work scenario credits
// only 20%, and the frequent names are known scripters.
var staffNoteRole = map[string]int64{
	"script":          roleProgram,
	"scripting":       roleProgram,
	"programming":     roleProgram,
	"program":         roleProgram,
	"programmer":      roleProgram,
	"coding":          roleProgram,
	"hacking":         roleProgram,
	"ui programmer":   roleProgram,
	"ui programming":  roleProgram,
	"gui programmer":  roleProgram,
	"gui programming": roleProgram,
	"gui coding":      roleProgram,

	"graphics":                 roleArtWorker,
	"graphic":                  roleArtWorker,
	"2d graphics":              roleArtWorker,
	"cg":                       roleArtWorker,
	"image editing":            roleArtWorker,
	"image editor":             roleArtWorker,
	"gui":                      roleArtWorker,
	"ui":                       roleArtWorker,
	"ui design":                roleArtWorker,
	"gui design":               roleArtWorker,
	"ui designer":              roleArtWorker,
	"gui designer":             roleArtWorker,
	"ui artist":                roleArtWorker,
	"gui artist":               roleArtWorker,
	"ui art":                   roleArtWorker,
	"gui art":                  roleArtWorker,
	"interface design":         roleArtWorker,
	"interface":                roleArtWorker,
	"logo design":              roleTitleDesign,
	"logo designer":            roleTitleDesign,
	"logo artist":              roleTitleDesign,
	"logo":                     roleTitleDesign,
	"title logo design":        roleTitleDesign,
	"backgrounds":              roleBackground,
	"background":               roleBackground,
	"royalty-free backgrounds": roleBackground,
	"coloring":                 roleColoring,
	"cg coloring":              roleColoring,
	"cg supervision":           roleCGSupervision,

	"movie":                    roleAnimationWork,
	"movies":                   roleAnimationWork,
	"op movie":                 roleAnimationWork,
	"ed movie":                 roleAnimationWork,
	"demo movie":               roleAnimationWork,
	"opening movie":            roleAnimationWork,
	"pv movie":                 roleAnimationWork,
	"promotion movie":          roleAnimationWork,
	"op, ed movie":             roleAnimationWork,
	"op & ed movie":            roleAnimationWork,
	"op/ed movie":              roleAnimationWork,
	"movie production":         roleAnimationWork,
	"op movie production":      roleAnimationWork,
	"opening movie production": roleAnimationWork,
	"movie design":             roleAnimationWork,
	"movie designer":           roleAnimationWork,

	"op lyrics":         roleThemeSongLyrics,
	"ed lyrics":         roleThemeSongLyrics,
	"op, ed lyrics":     roleThemeSongLyrics,
	"op/ed lyrics":      roleThemeSongLyrics,
	"theme song lyrics": roleThemeSongLyrics,
	"lyrics":            roleLyric,
	"se":                roleSoundEffects,
	"sound effects":     roleSoundEffects,
	"sfx":               roleSoundEffects,
	"royalty-free se":   roleSoundEffects,
	"recording studio":  roleRecordingStudio,
	"voice recording":   roleRecording,
	"recording":         roleRecording,
	"voice production":  roleSound,
	"sound production":  roleSound,
	"audio production":  roleSound,

	"planning":           rolePlanningJP,
	"producer":           roleProducer,
	"executive producer": roleExecProducer,
	"original work":      roleOriginalWork,
	"game design":        roleGameDesigner,
	"staging":            roleStaging,
	"supervision":        roleSupervision,
	"cooperation":        roleCooperation,
	"pr":                 rolePublicity,
	"debug":              roleQARole,
	"special thanks":     roleSpecialThanks,

	"localization": roleTranslator,
	"localisation": roleTranslator,
	"translation":  roleTranslator,
}

const (
	roleOtherStaffID    int64 = 2
	roleTranslator      int64 = 3
	roleQARole          int64 = 5
	roleAnimationWork   int64 = 114
	roleCGSupervision   int64 = 143
	roleStaging         int64 = 178
	roleExecProducer    int64 = 179
	roleGameDesigner    int64 = 181
	roleLyric           int64 = 199
	roleProducer        int64 = 230
	roleProgram         int64 = 238
	rolePublicity       int64 = 240
	roleRecording       int64 = 244
	roleRecordingStudio int64 = 246
	roleSound           int64 = 259
	roleSoundEffects    int64 = 261
	roleSpecialThanks   int64 = 267
	roleThemeSongLyrics int64 = 279
	roleTitleDesign     int64 = 281
	roleColoring        int64 = 289
	rolePlanningJP      int64 = 291
	roleCooperation     int64 = 305
	roleOriginalWork    int64 = 307
	roleSupervision     int64 = 314
	roleArtWorker       int64 = 316
	roleBackground      int64 = 319
)

var staffNoteSeparators = regexp.MustCompile(`[,&/]`)

func RefineVNDBStaffRoles(roleID int64, note string) []int64 {
	if roleID != roleOtherStaffID {
		return []int64{roleID}
	}
	folded := NormalizeStaffNote(note)
	if refined, ok := staffNoteRole[folded]; ok {
		return []int64{refined}
	}
	parts := staffNoteSeparators.Split(folded, -1)
	if len(parts) < 2 {
		return []int64{roleID}
	}
	var roles []int64
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		refined, ok := staffNoteRole[part]
		if !ok {
			return []int64{roleID}
		}
		if !slices.Contains(roles, refined) {
			roles = append(roles, refined)
		}
	}
	if len(roles) == 0 {
		return []int64{roleID}
	}
	return roles
}

func NormalizeStaffNote(note string) string {
	return strings.ToLower(strings.TrimSpace(note))
}

func StaffNoteRoleTable() map[string]int64 {
	return maps.Clone(staffNoteRole)
}
