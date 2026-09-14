package report

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/feed"
	"github.com/mbalazy/pm/internal/storage"
)

func fakeClaude(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "claude")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

const okEnvelope = `{"type":"result","subtype":"success","is_error":false,"result":"**ACME**: acme-api-1 moved.\n\nDecide on the batch.\n\n- SUGGEST acme-api-1 back_to_todo: the reviewer answered\n- SUGGEST zzz-9 dance: nonsense\n","session_id":"sess-1","num_turns":1,"usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":4000,"output_tokens":90}}`

func input() Input {
	cutoff := time.Date(2026, 9, 4, 18, 0, 0, 0, time.Local)
	return Input{
		Cutoff: cutoff, Now: cutoff.Add(15 * time.Hour), Language: "en",
		Events: []feed.Event{{ID: "e1", TS: cutoff.Add(time.Hour).Format(time.RFC3339), Source: "pm", Project: "acme-api", Group: "acme", TaskID: "acme-api-1", Title: "Acme-api thing", Detail: "moved to waiting", Severity: "warn"}},
		Attention: &storage.Attention{Sections: []storage.AttentionSection{
			{Name: "needs_me", Total: 1, Rows: []storage.AttentionRow{{Project: "atlas", TaskID: "atlas-9", Title: "Epic", Reason: "run failed", Status: "doing"}}},
			{Name: "changes", Total: 5, Rows: []storage.AttentionRow{{Title: "ignored here"}}},
		}},
		Groups: []storage.ProjectGroup{{Slug: "acme", Name: "ACME", Projects: []string{"acme-api", "acme-zap"}}},
	}
}

func TestBuildPromptAndParse(t *testing.T) {
	p := BuildPrompt(input())
	for _, want := range []string{"- ACME (acme): repos acme-api, acme-zap", "pm acme-api acme-api-1: Acme-api thing - moved to waiting [warn]", "### needs_me (1)", "- orbit atlas-9: Epic - run failed (status doing)", `language with code "en"`, "SUGGEST <task_id> <action>"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "ignored here") {
		t.Error("the changes section is the feed itself and must not be repeated")
	}
	if !strings.Contains(p, "(nothing known)") {
		t.Error("an empty current state must say so")
	}
	// The current state rides beside the events, and the instructions put it
	// above them.
	in := input()
	in.PRs = []feed.PRState{{Project: "acme-api", Number: 5, Title: "Fix", State: "merged", ReviewDecision: "APPROVED", TaskID: "acme-api-1"}, {Project: "acme-api", Number: 6, Title: "WIP", State: "open", Draft: true}}
	in.Tasks = []TaskState{{Project: "acme-api", ID: "acme-api-1", Title: "Acme-api thing", Status: "waiting", WaitingFor: "review Anna"}}
	withState := BuildPrompt(in)
	for _, want := range []string{`- PR acme-api #5 "Fix": merged, review approved, task acme-api-1`, `- PR acme-api #6 "WIP": open (draft)`, `- task acme-api acme-api-1 "Acme-api thing": status waiting, waiting for review Anna`, "never write about a merged or closed PR"} {
		if !strings.Contains(withState, want) {
			t.Errorf("prompt lacks %q:\n%s", want, withState)
		}
	}
	prose, sugg := ParseSuggestions("2026-09-04T18", "Text.\n\n- SUGGEST acme-api-1 back_to_todo: reason\n- SUGGEST x-1 dance: no\n", func(id string) string {
		if id == "acme-api-1" {
			return "acme-api"
		}
		return ""
	})
	if prose != "Text." || len(sugg) != 2 {
		t.Fatalf("prose %q sugg %+v", prose, sugg)
	}
	if sugg[0].Project != "acme-api" || sugg[0].Action != "back_to_todo" || sugg[0].Text != "reason" || sugg[0].ID == "" {
		t.Fatalf("sugg[0] = %+v", sugg[0])
	}
	if sugg[1].Action != "" || sugg[1].Project != "" {
		t.Fatalf("an unknown action / unplaced task must be blank: %+v", sugg[1])
	}
	if PeriodKey(time.Date(2026, 9, 4, 18, 0, 0, 0, time.Local)) != "2026-09-04T18" {
		t.Fatal(PeriodKey(time.Now()))
	}
}

func TestWriteSuccessAndFailure(t *testing.T) {
	root := t.TempDir()
	st := Store{Root: root}
	t.Run("success: report + suggestions + tokens stored, both files", func(t *testing.T) {
		exe := fakeClaude(t, "printf '%s' '"+okEnvelope+"'\n")
		w := &Writer{Exe: exe}
		r, err := w.Write(context.Background(), st, input(), "haiku")
		if err != nil {
			t.Fatal(err)
		}
		if r.Period != "2026-09-04T18" || r.Model != "haiku" || r.Tokens == nil || r.Tokens.CacheRead != 4000 || r.Events != 1 || r.Rows != 2 {
			t.Fatalf("report = %+v", r)
		}
		if !strings.HasPrefix(r.Text, "**ACME**") || strings.Contains(r.Text, "SUGGEST") || len(r.Suggestions) != 2 {
			t.Fatalf("text/suggestions = %q %+v", r.Text, r.Suggestions)
		}
		if r.Suggestions[0].Project != "acme-api" || r.Suggestions[0].Action != "back_to_todo" {
			t.Fatalf("sugg = %+v", r.Suggestions[0])
		}
		back, err := st.Read(r.Period)
		if err != nil || back == nil || back.Text != r.Text || len(back.Suggestions) != 2 {
			t.Fatalf("read back = %+v %v", back, err)
		}
		md, _ := os.ReadFile(filepath.Join(root, ".cockpit", "reports", r.Period+".md"))
		if !strings.Contains(string(md), "SUGGEST acme-api-1 back_to_todo") {
			t.Fatalf("md = %s", md)
		}
		// Dismiss: recorded, idempotent, unknown id refused.
		d, err := st.Dismiss(r.Period, r.Suggestions[0].ID)
		if err != nil || len(d.Dismissed) != 1 {
			t.Fatalf("dismiss = %+v %v", d, err)
		}
		if d, _ = st.Dismiss(r.Period, r.Suggestions[0].ID); len(d.Dismissed) != 1 {
			t.Fatal("dismiss must be idempotent")
		}
		if _, err := st.Dismiss(r.Period, "nope"); err == nil {
			t.Fatal("unknown id must be refused")
		}
	})
	t.Run("failure: exit 1 is stored as the period's error, no text", func(t *testing.T) {
		exe := fakeClaude(t, "echo 'not logged in' >&2; exit 1\n")
		w := &Writer{Exe: exe}
		r, err := w.Write(context.Background(), st, input(), "haiku")
		if err == nil || r.Error == "" || !strings.Contains(r.Error, "not logged in") || r.Text != "" {
			t.Fatalf("report = %+v err %v", r, err)
		}
		back, _ := st.Read(r.Period)
		if back == nil || back.Error == "" {
			t.Fatal("the failure must be stored")
		}
	})
	t.Run("the message-array shape and an is_error envelope", func(t *testing.T) {
		out, err := decode([]byte(`[{"type":"system"},{"type":"result","is_error":false,"result":"hi","usage":{"output_tokens":1}}]`))
		if err != nil || out.Text != "hi" || out.Tokens.Output != 1 {
			t.Fatalf("array = %+v %v", out, err)
		}
		if _, err := decode([]byte(`{"type":"result","is_error":true,"subtype":"error_max_turns","result":"boom"}`)); err == nil || !strings.Contains(err.Error(), "error_max_turns") {
			t.Fatalf("is_error = %v", err)
		}
		if _, err := decode([]byte("garbage")); err == nil {
			t.Fatal("garbage must fail")
		}
	})
	t.Run("timeout kills the group", func(t *testing.T) {
		old := Timeout
		Timeout = 300 * time.Millisecond
		t.Cleanup(func() { Timeout = old })
		exe := fakeClaude(t, "sleep 5\n")
		w := &Writer{Exe: exe}
		started := time.Now()
		_, err := w.Run(context.Background(), "p", "haiku")
		if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(started) > 3*time.Second {
			t.Fatalf("err = %v after %s", err, time.Since(started))
		}
	})
	t.Run("the environment drops the API key and marks headless", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-x")
		env := strings.Join(Environ(), "\n")
		if strings.Contains(env, "ANTHROPIC_API_KEY=") || !strings.Contains(env, "PM_HEADLESS=1") {
			t.Fatalf("env = %s", env)
		}
	})
}

// TestConcurrentWritesOfOnePeriod: the background write and a dismiss click
// on the same period must not trip over each other's temp file (one temp
// name per process did) nor interleave a stale read-modify-write.
func TestConcurrentWritesOfOnePeriod(t *testing.T) {
	st := Store{Root: t.TempDir()}
	base := &Report{Period: "2026-09-04T18", Model: "haiku", Text: "base", Suggestions: []Suggestion{{ID: "s1", TaskID: "acme-api-1", Action: "back_to_todo", Text: "x"}}}
	if err := st.Write(base); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := *base
			r.Text = "rewritten"
			if err := st.Write(&r); err != nil {
				errs <- err
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := st.Dismiss(base.Period, "s1"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent write: %v", err)
	}
	back, err := st.Read(base.Period)
	if err != nil || back == nil {
		t.Fatalf("read back = %+v %v", back, err)
	}
	if back.Text != "rewritten" && back.Text != "base" {
		t.Fatalf("torn report: %q", back.Text)
	}
	left, _ := filepath.Glob(filepath.Join(st.dir(), "*.tmp"))
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}
