package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestExecutorTimeoutParsesDurationStrings(t *testing.T) {
	const data = `
name: Orbit
executor:
  enabled: true
  timeout: 1h30m
`
	var p Project
	if err := yaml.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got := p.GetExecutor().Timeout.Duration(); got != 90*time.Minute {
		t.Fatalf("Timeout = %s, want 1h30m", got)
	}
}

func TestExecutorTimeoutUnsetIsZero(t *testing.T) {
	const data = `
name: Orbit
executor:
  enabled: true
`
	var p Project
	if err := yaml.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// Zero, not a default: the DEFAULT lives in the command (defaultWorkerTimeout)
	// so "unset" and "set to 120m" stay distinguishable here - which is what lets
	// `pm executor show` say where the number came from.
	if got := p.GetExecutor().Timeout; got != 0 {
		t.Fatalf("Timeout = %s, want 0 for an unset field", got)
	}
}

// TestExecutorTimeoutRejectsBareNumber: a number could be seconds, minutes or
// nanoseconds, and every one of those guesses produces a plausible-looking
// project that silently kills workers. Refusing beats picking.
func TestExecutorTimeoutRejectsBareNumber(t *testing.T) {
	for _, bad := range []string{"90", "90 minutes", "true"} {
		var p Project
		err := yaml.Unmarshal([]byte("name: Orbit\nexecutor:\n  enabled: true\n  timeout: "+bad+"\n"), &p)
		if err == nil {
			t.Fatalf("timeout: %s was accepted, want a parse error", bad)
		}
		if !strings.Contains(err.Error(), "90m") && !strings.Contains(err.Error(), "duration") {
			t.Fatalf("timeout: %s error does not say how to spell it: %v", bad, err)
		}
	}
}

// TestExecutorTimeoutSurvivesProjectRewrite is the regression this field's
// custom type exists for. writeProject re-marshals the WHOLE project on any
// update (a notes-only edit is enough), and yaml.v3 marshals a bare
// time.Duration as a nanosecond integer that its own decoder then refuses - so
// without Duration.MarshalYAML this sequence would leave a project.yaml no
// subsequent pm command could read at all.
func TestExecutorTimeoutSurvivesProjectRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.yaml")
	if err := os.WriteFile(path, []byte("name: Orbit\nexecutor:\n    enabled: true\n    timeout: 90m\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p, err := ReadProject(path)
	if err != nil {
		t.Fatalf("ReadProject: %v", err)
	}
	p.Notes = "unrelated edit"
	if err := WriteProject(path, p); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "5400000000000") {
		t.Fatalf("timeout was rewritten as nanoseconds:\n%s", raw)
	}
	again, err := ReadProject(path)
	if err != nil {
		t.Fatalf("re-read after rewrite: %v\n%s", err, raw)
	}
	if got := again.GetExecutor().Timeout.Duration(); got != 90*time.Minute {
		t.Fatalf("Timeout after rewrite = %s, want 1h30m0s\n%s", got, raw)
	}
}
