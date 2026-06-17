package board

import "testing"

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
