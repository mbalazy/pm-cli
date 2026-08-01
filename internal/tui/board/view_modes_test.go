package board

import (
	"slices"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestProjectPickerPadsByDisplayWidthNotByteLength covers the picker half of
// pm-cli-67-2: viewProjectPicker sized its two columns with len(plain) (BYTES)
// instead of lipgloss.Width (display cells), so a project whose name or path
// carries diacritics measured wider than it renders.
//
// The observable symptom is not misalignment - lipgloss.JoinHorizontal re-pads
// every line of a block to that block's widest line, which repairs an
// under-padded line on its own (verified by diffing the rendered picker with
// and without the fix: byte-identical while the Ascii color profile is in
// effect). What survives that repair is the right panel's truncation branch:
// a line whose BYTE length passes rightWidth while its display width does not
// is rerendered from its plain text, dropping the label's bold and the value's
// dim colour. So the assertion is on styling, not on geometry, and the test
// forces a real colour profile - under the test default (Ascii) lipgloss emits
// no escapes at all and there is nothing to lose.
//
// Both fixtures below are the same string in display cells and differ only in
// bytes, so an unbugged renderer must style them identically.
func TestProjectPickerPadsByDisplayWidthNotByteLength(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	const (
		asciiName = "Zazolc gesla jazn"
		asciiPath = "/tmp/zazolc-gesla-jazn-projekt"
		plName    = "Zażółć gęślą jaźń"
		plPath    = "/tmp/zażółć-gęślą-jaźń-projekt"
	)
	if lipgloss.Width(plPath) != lipgloss.Width(asciiPath) {
		t.Fatalf("fixture broken: paths render %d vs %d cells, want equal", lipgloss.Width(plPath), lipgloss.Width(asciiPath))
	}
	if len(plPath) <= len(asciiPath) {
		t.Fatalf("fixture broken: %d bytes vs %d, want the diacritic path to be the longer one", len(plPath), len(asciiPath))
	}

	render := func(name, path string) string {
		m := Model{width: 120, height: 40}
		m.projectPicker = true
		m.pickerItems = []pickerItem{{slug: "p", name: name, stack: "Go", taskCount: 3, path: path}}
		return m.viewProjectPicker()
	}

	asciiSeqs := ansiRegexp.FindAllString(render(asciiName, asciiPath), -1)
	plSeqs := ansiRegexp.FindAllString(render(plName, plPath), -1)

	if len(asciiSeqs) == 0 {
		t.Fatal("no ANSI sequences in the rendered picker - the colour profile did not take effect")
	}
	if !slices.Equal(asciiSeqs, plSeqs) {
		t.Errorf("picker styling differs between an ASCII and a diacritic project of the same display width: %d sequences vs %d",
			len(asciiSeqs), len(plSeqs))
	}
}
