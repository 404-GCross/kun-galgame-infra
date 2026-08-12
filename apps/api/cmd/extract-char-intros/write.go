package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// seed registry: first-party machine inference over catalog facts
const sourceDerived int16 = 18

const minIntroRunes = 20

type stats struct {
	Extracted          int
	Inserted           int
	Conflict           int
	RefusedNotVerbatim int
	RefusedShort       int
	RefusedNameAbsent  int
	UnmatchedName      int
	CallErrors         int
	Touched            int
}

type writer struct {
	db      *gorm.DB
	apply   bool
	st      *stats
	samples int
	shown   int
	touched []int64
}

// write files one work's extraction results. Every passage passes the verbatim
// gate against the work intro before anything else is considered.
func (w *writer) write(ctx context.Context, cand candidateWork, found map[string]string, mtModel string) {
	byName := make(map[string]rosterChar, len(cand.Roster))
	for _, r := range cand.Roster {
		byName[r.Name] = r
	}
	srcHash := hashText(cand.Intro)
	wrote := false
	for name, passage := range found {
		w.st.Extracted++
		target, ok := byName[strings.TrimSpace(name)]
		if !ok {
			w.st.UnmatchedName++
			continue
		}
		passage = strings.TrimSpace(passage)
		if utf8.RuneCountInString(passage) < minIntroRunes {
			w.st.RefusedShort++
			continue
		}
		if !nameAppears(cand.Intro, target) {
			w.st.RefusedNameAbsent++
			slog.Warn("the work intro never names this character", "work", cand.WorkID,
				"character", target.CharacterID, "name", name)
			continue
		}
		if !verbatim(cand.Intro, passage) {
			w.st.RefusedNotVerbatim++
			slog.Warn("extraction failed the verbatim gate", "work", cand.WorkID,
				"character", target.CharacterID, "name", name)
			continue
		}
		if w.shown < w.samples {
			fmt.Printf("  sample: work=%d %s → %s\n", cand.WorkID, name, truncate(passage, 80))
			w.shown++
		}
		if !w.apply {
			continue
		}
		res := w.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "character_id"}, {Name: "lang"}, {Name: "source_id"}},
			DoNothing: true,
		}).Create(&model.CatalogCharacterIntro{
			CharacterID: target.CharacterID,
			Lang:        "zh-Hans",
			Intro:       passage,
			SourceID:    sourceDerived,
			Provenance:  model.IntroProvenanceMachine,
			SrcHash:     srcHash,
			MTModel:     mtModel,
		})
		if res.Error != nil {
			w.st.CallErrors++
			slog.Warn("insert extracted intro", "work", cand.WorkID, "character", target.CharacterID, "err", res.Error)
			continue
		}
		if res.RowsAffected == 0 {
			w.st.Conflict++
			continue
		}
		w.st.Inserted++
		wrote = true
	}
	if wrote {
		w.touched = append(w.touched, cand.WorkID)
	}
}

func (w *writer) flushTouch(ctx context.Context) error {
	if err := repository.TouchWorks(ctx, w.db, w.touched); err != nil {
		return fmt.Errorf("touch works: %w", err)
	}
	w.st.Touched = len(w.touched)
	return nil
}

// verbatim reports whether the passage exists inside the intro once both are
// stripped of whitespace and common list/heading punctuation. The model may
// merge ADJACENT sentences of one character and drop bullets, so the check
// runs per line of the passage: every line must reappear contiguously.
func verbatim(intro, passage string) bool {
	haystack := normalizeForMatch(intro)
	for line := range strings.SplitSeq(passage, "\n") {
		needle := normalizeForMatch(line)
		if needle == "" {
			continue
		}
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}

// nameAppears requires the WORK INTRO (not the passage — the model is allowed
// to drop a 「名字」 heading, so good extracts often omit the name) to name the
// character: full name, zh alias, or a given-name segment (≥2 runes). The
// family name alone does NOT count — in the 100-work rehearsal the model
// attached a passage about 月森鈴 to her roster-mate 月森玲子, whose own name
// never appears in that intro; a surname match would have let that through.
func nameAppears(intro string, target rosterChar) bool {
	hay := normalizeForMatch(intro)
	for _, cand := range []string{target.Name, target.ZhName} {
		cand = strings.TrimSpace(cand)
		if cand == "" {
			continue
		}
		if full := normalizeForMatch(cand); full != "" && strings.Contains(hay, full) {
			return true
		}
		segs := strings.Fields(cand)
		for i, seg := range segs {
			if i == 0 && len(segs) > 1 {
				continue
			}
			n := normalizeForMatch(seg)
			if utf8.RuneCountInString(n) >= 2 && strings.Contains(hay, n) {
				return true
			}
		}
	}
	return false
}

var matchStripper = strings.NewReplacer(
	" ", "", "\t", "", "\n", "", "\r", "", "　", "",
	"・", "", "、", "", "。", "", ",", "", ".", "",
	"「", "", "」", "", "『", "", "』", "", "【", "", "】", "",
	"(", "", ")", "", "(", "", ")", "", ":", "", ":", "",
	"~", "", "~", "", "-", "", "―", "", "…", "", "!", "", "!", "", "?", "", "?", "",
)

func normalizeForMatch(s string) string {
	return matchStripper.Replace(s)
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
