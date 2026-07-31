package board

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mbalazy/pm/internal/storage"
)

// TestStatusGlyphAligned covers pm-cli-46: the subtask picker formats its rows
// with %-7s after the glyph, which only aligns if every glyph occupies the same
// number of DISPLAY columns - and "○" (todo) plus "🗄" (archived) are 1 where
// the other emoji are 2.
func TestStatusGlyphAligned(t *testing.T) {
	statuses := []storage.TaskStatus{
		storage.StatusTodo, storage.StatusDoing, storage.StatusWaiting,
		storage.StatusDone, storage.StatusArchived, "merged", "some-custom-status",
	}
	for _, s := range statuses {
		if got := lipgloss.Width(statusGlyphAligned(s)); got != 2 {
			t.Errorf("statusGlyphAligned(%q) is %d display columns, want 2 - the picker's columns go ragged", s, got)
		}
	}

	// The unpadded glyph stays unpadded: it goes into running text (the detail
	// view's "Parent:" line), where padding would read as a stray space.
	if got := statusGlyph(storage.StatusTodo); got != "○" {
		t.Errorf("statusGlyph(todo) = %q, want the bare glyph", got)
	}
}

// TestViewArchiveRendersActiveToast covers pm-cli-67-2: viewArchive returned
// sb.String() directly instead of wrapping it in applyToast like every other
// view (viewBoard, viewDetail, viewFocus, viewExecutor, the picker) - so a
// failed restore/undo, or a clipboard confirmation, set toastMsg/toastExpiry
// but the archive view never drew it.
func TestViewArchiveRendersActiveToast(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusDone}})
	m.doArchive(m.taskByID("p-1"))
	m.toastMsg = "restore failed: boom"
	m.toastExpiry = time.Now().Add(4 * time.Second)

	got := stripANSI(m.viewArchive())
	if !strings.Contains(got, m.toastMsg) {
		t.Errorf("viewArchive() output missing active toast %q:\n%s", m.toastMsg, got)
	}
}

func TestTruncLines(t *testing.T) {
	// explicit, readable assertions instead of the table for the tricky wraps:
	t.Run("fits single line untouched", func(t *testing.T) {
		if got := truncLines("Top nav indicator", 40, 2); got != "Top nav indicator" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("wraps to two lines no ellipsis when it fits", func(t *testing.T) {
		// width 12: "Streaks F1" (10) then "Foundation" (10) -> 2 lines, all consumed
		got := truncLines("Streaks F1 Foundation", 12, 2)
		want := "Streaks F1\nFoundation"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("ellipsis when content overflows maxLines", func(t *testing.T) {
		got := truncLines("alpha beta gamma delta epsilon zeta", 11, 2)
		// line1: "alpha beta" (10), line2: "gamma delta" (11) then overflow -> ellipsis
		lines := 0
		for _, r := range got {
			if r == '\n' {
				lines++
			}
		}
		if lines != 1 {
			t.Errorf("expected 2 lines, got %d (%q)", lines+1, got)
		}
		if got[len(got)-len("…"):] != "…" {
			t.Errorf("expected trailing ellipsis, got %q", got)
		}
	})
	t.Run("hard-truncates a single overlong word", func(t *testing.T) {
		got := truncLines("supercalifragilisticexpialidocious", 10, 2)
		if len([]rune(got)) > 10 {
			t.Errorf("overlong word not truncated to width: %q (%d runes)", got, len([]rune(got)))
		}
	})
}
