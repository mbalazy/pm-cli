package storage

import "testing"

func TestApplySpec(t *testing.T) {
	t.Run("creates block at top when no markers and body empty", func(t *testing.T) {
		got := ApplySpec("", "We build X.")
		want := SpecStart + "\nWe build X.\n" + SpecEnd
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("prepends block above existing body as the Log", func(t *testing.T) {
		got := ApplySpec("## Log\n\nSession 1 notes.", "Spec v1.")
		want := SpecStart + "\nSpec v1.\n" + SpecEnd + "\n\n## Log\n\nSession 1 notes."
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("replaces existing block, keeps Log untouched", func(t *testing.T) {
		body := SpecStart + "\nold spec\n" + SpecEnd + "\n\n## Log\n\nSession 1."
		got := ApplySpec(body, "new spec")
		want := SpecStart + "\nnew spec\n" + SpecEnd + "\n\n## Log\n\nSession 1."
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("replaces block when content precedes it", func(t *testing.T) {
		body := "intro\n\n" + SpecStart + "\nold\n" + SpecEnd + "\n\ntail"
		got := ApplySpec(body, "new")
		want := "intro\n\n" + SpecStart + "\nnew\n" + SpecEnd + "\n\ntail"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("trims surrounding whitespace in new spec", func(t *testing.T) {
		got := ApplySpec("", "  \n spec body \n  ")
		want := SpecStart + "\nspec body\n" + SpecEnd
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("round-trips: replace twice keeps single block", func(t *testing.T) {
		b := ApplySpec("", "v1")
		b = ApplySpec(b, "v2")
		if got := ExtractSpec(b); got != "v2" {
			t.Errorf("ExtractSpec = %q, want v2", got)
		}
		// exactly one start marker
		if c := countOccurrences(b, SpecStart); c != 1 {
			t.Errorf("expected 1 spec block, got %d", c)
		}
	})
}

func TestExtractSpec(t *testing.T) {
	t.Run("returns content without markers", func(t *testing.T) {
		body := SpecStart + "\nhello world\n" + SpecEnd + "\n\nlog"
		if got := ExtractSpec(body); got != "hello world" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("empty when no markers", func(t *testing.T) {
		if got := ExtractSpec("just a body"); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func countOccurrences(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
