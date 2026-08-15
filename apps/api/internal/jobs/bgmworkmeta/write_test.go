package bgmworkmeta

import (
	"errors"
	"testing"
)

func TestNoteErrorKeepsTheFirstCause(t *testing.T) {
	w := &writer{stats: &Stats{}}
	w.noteError(errors.New(`null value in column "spoiler" violates not-null constraint`))
	w.noteError(errors.New("a later, less interesting failure"))

	if w.stats.Errors != 2 {
		t.Errorf("Errors = %d, want 2", w.stats.Errors)
	}
	if want := `null value in column "spoiler" violates not-null constraint`; w.stats.FirstError != want {
		t.Errorf("FirstError = %q, want %q", w.stats.FirstError, want)
	}
}
