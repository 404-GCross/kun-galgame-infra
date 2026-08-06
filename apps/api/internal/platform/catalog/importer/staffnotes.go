package importer

import (
	"maps"
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
//   - cmd/refine-staff-notes moves the existing edges with the same table.
//
// Mapping discipline: EXACT normalized note match (lowercased, trimmed) onto
// an EXISTING vocabulary role — no new roles, no substring guessing, and
// composite notes ("Planning, script") stay unmapped rather than half-mapped.
// Notably `Script`/`Scripting` map to 程序, not 脚本: VNDB credits writers
// under its dedicated scenario role, so a staff-role Script note is engine
// scripting (スクリプト) — sampled holders overlap same-work scenario credits
// only 20%, and the frequent names are known scripters.
var staffNoteRole = map[string]int64{
	// engine / code
	"script":      roleProgram,
	"scripting":   roleProgram,
	"programming": roleProgram,
	"program":     roleProgram,
	"programmer":  roleProgram,
	"coding":      roleProgram,
	"hacking":     roleProgram, // fan-TL engine work

	// art
	"graphics":                 roleArtWorker,
	"graphic":                  roleArtWorker,
	"2d graphics":              roleArtWorker,
	"cg":                       roleArtWorker,
	"image editing":            roleArtWorker, // 改图 (fan-TL image work)
	"image editor":             roleArtWorker,
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

	// movie (OP/ED/demo video production) — same population as the Bangumi
	// side of 动画制作: role 114's live holders (神月社, プリズムビジョン, …)
	// are exactly the top names in these note buckets, so this mapping is a
	// dedupe, not a guess. Qualified forms ("producer, ed movie",
	// "movie assistance") stay unmapped for the composite wave.
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

	// music / sound
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

	// production
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
}

// Target role ids — all EXISTING catalog_role rows (seed-owned generated
// vocabulary + reserved band), verified in use or faithfully labeled. Keys in
// comments are catalog_role.key.
const (
	roleOtherStaffID    int64 = 2   // other-staff 其他 (the bucket being refined)
	roleQARole          int64 = 5   // qa QA (debug ≈ デバッグ)
	roleAnimationWork   int64 = 114 // animation-work 动画制作 (OP/ED/demo movie)
	roleCGSupervision   int64 = 143 // cg-监修 CG 监修
	roleStaging         int64 = 178 // episode-direction 演出
	roleExecProducer    int64 = 179 // executive-producer 执行制片人
	roleGameDesigner    int64 = 181 // game-designer 游戏设计师
	roleLyric           int64 = 199 // lyric 作词 (bare "Lyrics" — not tied to OP/ED)
	roleProducer        int64 = 230 // producer 制作人
	roleProgram         int64 = 238 // program 程序
	rolePublicity       int64 = 240 // publicity 宣传
	roleRecording       int64 = 244 // recording 录音
	roleRecordingStudio int64 = 246 // recording-studio 录音工作室
	roleSound           int64 = 259 // sound 音响 (audio-production houses)
	roleSoundEffects    int64 = 261 // sound-effects 音效
	roleSpecialThanks   int64 = 267 // special-thanks 特别鸣谢
	roleThemeSongLyrics int64 = 279 // theme-song-lyrics 主题歌作词
	roleTitleDesign     int64 = 281 // title-design 标题设计 (game logo)
	roleColoring        int64 = 289 // 上色
	rolePlanningJP      int64 = 291 // 企画 (the one in live use; generated 224 is empty)
	roleCooperation     int64 = 305 // 协力 (in live use; generated 121 is empty)
	roleOriginalWork    int64 = 307 // 原作 (in live use; 168/221 are empty)
	roleSupervision     int64 = 314 // 监修 (in live use; 274 is empty)
	roleArtWorker       int64 = 316 // 美工 (generic graphics work)
	roleBackground      int64 = 319 // 背景 (in live use; 136 is empty)
)

// RefineVNDBStaffRole answers the role a VNDB staff credit lands under: for
// the 其他 bucket it consults the note table, everything else passes through.
func RefineVNDBStaffRole(roleID int64, note string) int64 {
	if roleID != roleOtherStaffID {
		return roleID
	}
	if refined, ok := staffNoteRole[NormalizeStaffNote(note)]; ok {
		return refined
	}
	return roleID
}

// NormalizeStaffNote is the match key: notes arrive with stray case and
// whitespace, and the table is keyed on the folded form.
func NormalizeStaffNote(note string) string {
	return strings.ToLower(strings.TrimSpace(note))
}

// StaffNoteRoleTable exposes a copy of the note→role table for the backfill
// tool, grouped nowhere — the tool iterates it verbatim so the two writers
// cannot drift.
func StaffNoteRoleTable() map[string]int64 {
	return maps.Clone(staffNoteRole)
}
