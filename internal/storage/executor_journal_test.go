package storage

import (
	"os"
	"testing"
)

func TestJournalAppendRead(t *testing.T) {
	dir := t.TempDir()

	// missing file -> empty, no error
	if got, err := ReadJournal(dir); err != nil || len(got) != 0 {
		t.Fatalf("empty journal: got %v entries, err %v", len(got), err)
	}

	start := &JournalEntry{Event: JournalEventStart, Kind: "run-epic", Project: "proj", TaskID: "proj-1", PID: 42, Model: "opus", Branch: "epic/proj-1"}
	if err := AppendJournal(dir, start); err != nil {
		t.Fatalf("append start: %v", err)
	}
	if start.TS == "" {
		t.Error("AppendJournal should stamp TS when empty")
	}
	end := &JournalEntry{
		Event: JournalEventEnd, Kind: "run-epic", Project: "proj", TaskID: "proj-1",
		Status: "done", DurationS: 123,
		Subs: []JournalSub{
			{ID: "proj-1-1", Result: "merged", DurationS: 100, Session: "abc", Turns: 42, CostUSD: 1.25},
			{ID: "proj-1-2", Result: "manual", Note: "manual sub"},
		},
	}
	if err := AppendJournal(dir, end); err != nil {
		t.Fatalf("append end: %v", err)
	}

	got, err := ReadJournal(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].Event != JournalEventStart || got[1].Event != JournalEventEnd {
		t.Errorf("order/events wrong: %q, %q", got[0].Event, got[1].Event)
	}
	if got[1].Subs[1].Result != "manual" {
		t.Errorf("sub result did not round-trip: %+v", got[1].Subs)
	}
	if got[1].Subs[0].Turns != 42 || got[1].Subs[0].CostUSD != 1.25 {
		t.Errorf("turns/cost did not round-trip: %+v", got[1].Subs[0])
	}
}

func TestJournalSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	if err := AppendJournal(dir, &JournalEntry{Event: JournalEventStart, Kind: "work", Project: "p", TaskID: "p-1"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// simulate a torn write in the middle of the file
	f, err := os.OpenFile(JournalPath(dir), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	f.WriteString("{\"event\":\"end\",\"trunc") // no newline, invalid JSON
	f.WriteString("\n")
	f.Close()
	if err := AppendJournal(dir, &JournalEntry{Event: JournalEventEnd, Kind: "work", Project: "p", TaskID: "p-1", Status: "done"}); err != nil {
		t.Fatalf("append after corrupt: %v", err)
	}

	got, err := ReadJournal(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("corrupt line should be skipped, got %d entries", len(got))
	}
	if got[0].Event != JournalEventStart || got[1].Status != "done" {
		t.Errorf("wrong surviving entries: %+v", got)
	}
}
