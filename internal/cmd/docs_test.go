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

func TestDocsClaudePrintsInstallableGuide(t *testing.T) {
	out := runDocs(t, "claude")
	// The guide must be an appendable CLAUDE.md block: refresh tooling keys on
	// the markers, and the content must carry the core contract.
	for _, want := range []string{
		"<!-- pm:agent-guide:start",
		"<!-- pm:agent-guide:end -->",
		"pm_context",
		"Brief field",
		"Spec / Log body zones",
		"a merged PR closes the task, everything else the user closes",
		"pm docs authoring",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pm docs claude output missing %q", want)
		}
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
