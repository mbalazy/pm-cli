package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// runDocs executes `pm docs <topic>` against a buffer and returns the output.
func runDocs(t *testing.T, topic string) string {
	t.Helper()
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"docs", topic})
	if err := root.Execute(); err != nil {
		t.Fatalf("pm docs %s: %v", topic, err)
	}
	return buf.String()
}

func TestDocsGuidePrintsInstallableGuide(t *testing.T) {
	out := runDocs(t, "guide")
	// The guide must be an appendable instructions block (CLAUDE.md, AGENTS.md):
	// refresh tooling keys on the markers, and the content must carry the core
	// contract.
	for _, want := range []string{
		"<!-- pm:agent-guide:start",
		"<!-- pm:agent-guide:end -->",
		"pm_context",
		"Brief field",
		"Spec / Log body zones",
		"a merged PR closes the task, everything else the user closes",
		"pm docs authoring",
		"**Timeline** (`pm_timeline_list` / `pm_timeline_add`",
		"never the state alone",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pm docs guide output missing %q", want)
		}
	}
	// The block is one text for every client: nothing in it may name Claude
	// Code as the reader, or a Codex user installs a guide that talks past them.
	if strings.Contains(out, "Claude Code") {
		t.Errorf("pm docs guide names Claude Code; the guide is client-neutral")
	}
}

// `pm docs claude` was the only name until 0.67.0 and is installed in users'
// shell history and CLAUDE.md refresh notes; it stays as an alias that prints
// the same bytes.
func TestDocsClaudeIsAnAliasOfGuide(t *testing.T) {
	if got, want := runDocs(t, "claude"), runDocs(t, "guide"); got != want {
		t.Errorf("pm docs claude and pm docs guide differ")
	}
}

func TestDocsAuthoringPrintsRules(t *testing.T) {
	out := runDocs(t, "authoring")
	for _, want := range []string{"ONLY VERIFIED FACTS", "pm_add_task", "EXCLUDED: X, because Y"} {
		if !strings.Contains(out, want) {
			t.Errorf("pm docs authoring output missing %q", want)
		}
	}
}
