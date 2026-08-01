package board

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestMenuTruncationHandlesMultiByteStrings covers pm-cli-67-2: the yank menu,
// links menu and session menu truncated with raw byte slices (display[:47],
// s[:42]) instead of truncateWidth - a cut that lands mid-rune produces invalid
// UTF-8 (mojibake) for any multi-byte value (Polish diacritics, emoji), which a
// yanked task's title/brief/AC/body routinely contains. Each value below is
// built so a byte-offset slice at the OLD cutoff falls mid-character,
// guaranteeing this test would have failed against the pre-fix code.
func TestMenuTruncationHandlesMultiByteStrings(t *testing.T) {
	t.Run("yank menu (item.value), old cutoff 47 mid rune", func(t *testing.T) {
		// "ą" is a 2-byte rune -> character boundaries fall on even byte offsets
		// only; byte 47 (odd) always lands mid-character.
		long := strings.Repeat("ą", 30) // 60 bytes, well over the 50-cell cutoff
		m := Model{width: 80, height: 24, yankItems: []yankItem{{label: "Body", value: long}}}

		got := m.viewYankMenu()
		if !utf8.ValidString(got) {
			t.Fatalf("viewYankMenu() produced invalid UTF-8 for a multi-byte value:\n%s", got)
		}
		want := truncateWidth(long, 50)
		if !strings.Contains(stripANSI(got), want) {
			t.Errorf("viewYankMenu() missing expected truncation %q in:\n%s", want, stripANSI(got))
		}
	})

	t.Run("links menu (item.url), old cutoff 47 mid rune", func(t *testing.T) {
		long := strings.Repeat("ą", 30)
		m := Model{width: 80, height: 24, linkItems: []linkItem{{name: "doc", url: long}}}

		got := m.viewLinksMenu()
		if !utf8.ValidString(got) {
			t.Fatalf("viewLinksMenu() produced invalid UTF-8 for a multi-byte url:\n%s", got)
		}
		want := truncateWidth(long, 50)
		if !strings.Contains(stripANSI(got), want) {
			t.Errorf("viewLinksMenu() missing expected truncation %q in:\n%s", want, stripANSI(got))
		}
	})

	t.Run("session menu (item.summary), old cutoff 42 mid rune", func(t *testing.T) {
		// "😀" is a 4-byte rune -> character boundaries fall on multiples of 4;
		// byte 42 (between 40 and 44) always lands mid-character.
		long := strings.Repeat("😀", 20) // 80 bytes, well over the 45-cell cutoff
		m := Model{width: 80, height: 24, sessionMenuItems: []sessionMenuItem{{sessionID: "abc123", summary: long}}}

		got := m.viewSessionMenu()
		if !utf8.ValidString(got) {
			t.Fatalf("viewSessionMenu() produced invalid UTF-8 for a multi-byte summary:\n%s", got)
		}
		want := truncateWidth(long, 45)
		if !strings.Contains(stripANSI(got), want) {
			t.Errorf("viewSessionMenu() missing expected truncation %q in:\n%s", want, stripANSI(got))
		}
	})
}
