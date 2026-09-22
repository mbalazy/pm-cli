package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingSkills(t *testing.T) {
	cfg := t.TempDir()
	// A real directory, as a hand-written skill would be.
	if err := os.MkdirAll(filepath.Join(cfg, "skills", "solo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "skills", "solo", "SKILL.md"), []byte("# solo"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink into a checkout, as the skills repo's install.sh leaves it.
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "skills", "batch-finish-auto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "skills", "batch-finish-auto", "SKILL.md"), []byte("# bfa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(checkout, "skills", "batch-finish-auto"), filepath.Join(cfg, "skills", "batch-finish-auto")); err != nil {
		t.Fatal(err)
	}
	// A directory with no SKILL.md is not a skill.
	if err := os.MkdirAll(filepath.Join(cfg, "skills", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := MissingSkills(cfg, AgentSkills...); len(got) != 0 {
		t.Errorf("both agent skills present (one linked) -> nothing missing, got %v", got)
	}
	got := MissingSkills(cfg, "solo", "empty", "web-verify", "batch-finish-auto")
	if strings.Join(got, ",") != "empty,web-verify" {
		t.Errorf("missing = %v, want [empty web-verify] in the order asked", got)
	}
	if got := MissingSkills(filepath.Join(cfg, "nope"), AgentSkills...); len(got) != 2 {
		t.Errorf("a config dir that does not exist misses every skill, got %v", got)
	}
	if hint := SkillsInstallHint(cfg); !strings.Contains(hint, SkillsRepoURL) || !strings.Contains(hint, cfg+"/skills") {
		t.Errorf("hint must name the repo and the target dir: %s", hint)
	}
}
