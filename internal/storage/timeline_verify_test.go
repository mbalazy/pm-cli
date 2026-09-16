package storage

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseStateLines(t *testing.T) {
	text := "Where we stand:\n" +
		"deploy on dev is green [verified 2026-09-14 by gh api repos/x/deployments]\n" +
		"\n" +
		"SENTRY_DSN is unset on the deploys [assumed]\n" +
		"client wants v2 by October [Assumed - said in the call, no ticket]\n" +
		"PR #340 merged [verified 2026-09-15 by: gh pr view 340]\n" +
		"the [brackets] in the middle stay text [verified 2026-09-16]\n" +
		"an unmarked line with a [tag] elsewhere\n" +
		"typo [verified last week by me]"
	got := ParseStateLines(text)
	want := []StateLine{
		{N: 1, Text: "Where we stand:"},
		{N: 2, Text: "deploy on dev is green", Mark: "verified", Date: "2026-09-14", Source: "gh api repos/x/deployments"},
		{N: 4, Text: "SENTRY_DSN is unset on the deploys", Mark: "assumed"},
		{N: 5, Text: "client wants v2 by October", Mark: "assumed"},
		{N: 6, Text: "PR #340 merged", Mark: "verified", Date: "2026-09-15", Source: "gh pr view 340"},
		{N: 7, Text: "the [brackets] in the middle stay text", Mark: "verified", Date: "2026-09-16"},
		{N: 8, Text: "an unmarked line with a [tag] elsewhere"},
		{N: 9, Text: "typo", Mark: "verified"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestValidateStateMarkers(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.Local)
	tests := []struct {
		name, kind, text string
		wantErr          string
	}{
		{"unmarked state passes", TimelineState, "a\nb", ""},
		{"assumed passes", TimelineState, "a [assumed]", ""},
		{"verified today passes", TimelineState, "a [verified 2026-09-16 by x]", ""},
		{"verified without a day", TimelineState, "ok\nb [verified by me]", "state line 2: a [verified ...] marker needs the day"},
		{"verified with a bad day", TimelineState, "b [verified 2026-13-40]", "not a calendar day"},
		{"verified in the future", TimelineState, "b [verified 2026-09-17 by x]", "in the future"},
		{"an event is never checked", TimelineEvent, "x [verified by nobody]", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStateMarkers(tc.kind, tc.text, now)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("got %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestVerifyState(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.Local)
	t.Run("counts, ages and the recheck list", func(t *testing.T) {
		text := "fresh [verified 2026-09-16 by a]\n" +
			"six days [verified 2026-09-10 by b]\n" +
			"seven days [verified 2026-09-09 by c]\n" +
			"old [verified 2026-08-01 by d]\n" +
			"guess [assumed]\n" +
			"\n" +
			"plain"
		v := VerifyState(text, now)
		if v.Verified != 2 || v.Recheck != 2 || v.Assumed != 1 || v.Unmarked != 1 {
			t.Fatalf("counts = %+v", v)
		}
		if len(v.RecheckLines) != 2 || v.RecheckLines[0].N != 3 || v.RecheckLines[0].Days != 7 || !v.RecheckLines[0].Recheck ||
			v.RecheckLines[1].N != 4 || v.RecheckLines[1].Days != 46 || v.RecheckLines[1].Source != "d" {
			t.Fatalf("recheck lines = %+v", v.RecheckLines)
		}
		if !strings.Contains(v.Note, "2 lines verified 7+ days ago") || !v.Marked() {
			t.Fatalf("note = %q", v.Note)
		}
	})
	t.Run("no markers is not silently verified", func(t *testing.T) {
		v := VerifyState("a\nb\n\nc", now)
		if v.Marked() || v.Unmarked != 3 || !strings.Contains(v.Note, "no verification markers - all 3 lines read as unverified") {
			t.Fatalf("verification = %+v", v)
		}
	})
	t.Run("all fresh has no note", func(t *testing.T) {
		v := VerifyState("a [verified 2026-09-15 by x]\nb [assumed]", now)
		if v.Note != "" || v.Verified != 1 || v.Assumed != 1 || len(v.RecheckLines) != 0 {
			t.Fatalf("verification = %+v", v)
		}
	})
	t.Run("a verified marker without a date reads as unverified, not fresh", func(t *testing.T) {
		// A hand-edited line: the write side would have refused it.
		v := VerifyState("x [verified by me]", now)
		if v.Recheck != 1 || v.Verified != 0 || v.RecheckLines[0].Days != 0 {
			t.Fatalf("verification = %+v", v)
		}
	})
}

func TestTimelineDefaultReadVerification(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.Local)
	entries := []TimelineEntry{
		{TS: "2026-09-15T10:00:00+02:00", Kind: TimelineState, Text: "a [verified 2026-09-01 by x]\nb"},
		{TS: "2026-09-15T11:00:00+02:00", Kind: TimelineEvent, Text: "E"},
	}
	r := TimelineDefaultRead(entries, now)
	if r.Verification == nil || r.Verification.Recheck != 1 || r.Verification.Unmarked != 1 {
		t.Fatalf("verification = %+v", r.Verification)
	}
	if r := TimelineDefaultRead(entries[1:], now); r.Verification != nil {
		t.Fatalf("verification without a state: %+v", r.Verification)
	}
}

func TestAppendTimelineEntryRejectsBadMarker(t *testing.T) {
	dir := t.TempDir()
	err := AppendTimelineEntry(dir, &TimelineEntry{Kind: TimelineState, Text: "x [verified someday by y]"})
	if err == nil || !strings.Contains(err.Error(), "needs the day") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(TimelineDir(dir)); !os.IsNotExist(err) {
		t.Fatalf("a rejected state created the timeline dir: %v", err)
	}
	// The same text as an event is fine: markers are a state's contract.
	if err := AppendTimelineEntry(dir, &TimelineEntry{Kind: TimelineEvent, Text: "x [verified someday by y]"}); err != nil {
		t.Fatalf("event: %v", err)
	}
}
