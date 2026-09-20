package storage

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// The handoff block must survive the Executor's custom UnmarshalYAML (which
// overlays the YAML onto defaultExecutor) without disturbing the defaults.
func TestExecutorUnmarshalHandoff(t *testing.T) {
	var e Executor
	src := `
base_branch: development
handoff:
  playbook: .claude/evidence-playbook.md
  runtime_skill: simulator-verify
  rig_skill: starting-local-rig
`
	if err := yaml.Unmarshal([]byte(src), &e); err != nil {
		t.Fatal(err)
	}
	if e.Handoff.Playbook != ".claude/evidence-playbook.md" {
		t.Errorf("Playbook = %q", e.Handoff.Playbook)
	}
	if e.Handoff.RuntimeSkill != "simulator-verify" {
		t.Errorf("RuntimeSkill = %q", e.Handoff.RuntimeSkill)
	}
	if e.Handoff.RigSkill != "starting-local-rig" {
		t.Errorf("RigSkill = %q", e.Handoff.RigSkill)
	}
	if e.FixRounds != 2 || e.DoneStatus != "merged" {
		t.Errorf("defaults clobbered: fix_rounds=%d done_status=%q", e.FixRounds, e.DoneStatus)
	}
}

// A block with no handoff key must not invent one.
func TestExecutorUnmarshalWithoutHandoff(t *testing.T) {
	var e Executor
	if err := yaml.Unmarshal([]byte("base_branch: main\n"), &e); err != nil {
		t.Fatal(err)
	}
	if !e.Handoff.IsZero() {
		t.Errorf("expected zero handoff, got %+v", e.Handoff)
	}
}

func writeHandoffFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestHandoffIsZero(t *testing.T) {
	tests := []struct {
		name string
		h    Handoff
		want bool
	}{
		{"empty", Handoff{}, true},
		{"blank strings", Handoff{Playbook: "  ", RuntimeSkill: "\t"}, true},
		{"playbook only", Handoff{Playbook: "a.md"}, false},
		{"skill only", Handoff{RuntimeSkill: "sim"}, false},
		{"rig skill only", Handoff{RigSkill: "rig"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.h.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveHandoffUndeclared(t *testing.T) {
	got := Executor{}.ResolveHandoff(t.TempDir(), "")
	if got.Declared {
		t.Error("expected Declared=false for an executor with no handoff block")
	}
	if got.PlaybookPath != "" || got.RuntimeSkill != "" {
		t.Errorf("expected empty resolution, got %+v", got)
	}
}

func TestResolveHandoffPlaybook(t *testing.T) {
	repo := t.TempDir()
	writeHandoffFile(t, filepath.Join(repo, ".claude", "evidence-playbook.md"), "# playbook")

	t.Run("relative path resolves against the repo", func(t *testing.T) {
		e := Executor{Handoff: Handoff{Playbook: ".claude/evidence-playbook.md"}}
		got := e.ResolveHandoff(repo, "")
		if !got.Declared {
			t.Fatal("expected Declared=true")
		}
		want := filepath.Join(repo, ".claude", "evidence-playbook.md")
		if got.PlaybookPath != want {
			t.Errorf("PlaybookPath = %q, want %q", got.PlaybookPath, want)
		}
		if !got.PlaybookExists {
			t.Error("expected PlaybookExists=true")
		}
	})

	t.Run("missing file resolves but is reported absent", func(t *testing.T) {
		e := Executor{Handoff: Handoff{Playbook: ".claude/nope.md"}}
		got := e.ResolveHandoff(repo, "")
		if got.PlaybookPath == "" {
			t.Error("a missing playbook must still resolve to a path so the error can name it")
		}
		if got.PlaybookExists {
			t.Error("expected PlaybookExists=false")
		}
	})

	t.Run("absolute path is used as-is", func(t *testing.T) {
		abs := filepath.Join(repo, ".claude", "evidence-playbook.md")
		e := Executor{Handoff: Handoff{Playbook: abs}}
		if got := e.ResolveHandoff(repo, ""); got.PlaybookPath != abs || !got.PlaybookExists {
			t.Errorf("got %q exists=%v, want %q exists=true", got.PlaybookPath, got.PlaybookExists, abs)
		}
	})

	t.Run("a directory is not a playbook", func(t *testing.T) {
		e := Executor{Handoff: Handoff{Playbook: ".claude"}}
		if got := e.ResolveHandoff(repo, ""); got.PlaybookExists {
			t.Error("a directory must not count as an existing playbook")
		}
	})
}

func TestResolveHandoffRuntimeSkillScripts(t *testing.T) {
	repo := t.TempDir()
	skillDir := filepath.Join(repo, ".claude", "skills", "simulator-verify")
	writeHandoffFile(t, filepath.Join(skillDir, "SKILL.md"), "# simulator-verify")
	writeHandoffFile(t, filepath.Join(skillDir, "scripts", "sim-ui.sh"), "#!/bin/sh")
	writeHandoffFile(t, filepath.Join(skillDir, "scripts", "measure-element.py"), "print()")
	writeHandoffFile(t, filepath.Join(skillDir, "scripts", ".hidden"), "x")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts", "lib"), 0755); err != nil {
		t.Fatal(err)
	}

	t.Run("scripts are derived from disk, sorted, dirs and dotfiles skipped", func(t *testing.T) {
		e := Executor{Handoff: Handoff{RuntimeSkill: "simulator-verify"}}
		got := e.ResolveHandoff(repo, "")
		if got.SkillPath != filepath.Join(skillDir, "SKILL.md") {
			t.Errorf("SkillPath = %q", got.SkillPath)
		}
		want := []string{"measure-element.py", "sim-ui.sh"}
		if len(got.Scripts) != len(want) {
			t.Fatalf("Scripts = %v, want %v", got.Scripts, want)
		}
		for i := range want {
			if got.Scripts[i] != want[i] {
				t.Errorf("Scripts[%d] = %q, want %q", i, got.Scripts[i], want[i])
			}
		}
	})

	t.Run("a leading slash is accepted", func(t *testing.T) {
		e := Executor{Handoff: Handoff{RuntimeSkill: "/simulator-verify"}}
		got := e.ResolveHandoff(repo, "")
		if got.RuntimeSkill != "simulator-verify" || got.SkillPath == "" {
			t.Errorf("got %+v, want the slash stripped and the skill found", got)
		}
	})

	t.Run("unknown skill resolves to no path", func(t *testing.T) {
		e := Executor{Handoff: Handoff{RuntimeSkill: "nope"}}
		got := e.ResolveHandoff(repo, "")
		if !got.Declared || got.RuntimeSkill != "nope" {
			t.Fatalf("got %+v", got)
		}
		if got.SkillPath != "" || len(got.Scripts) != 0 {
			t.Errorf("expected nothing found, got %+v", got)
		}
	})

	t.Run("the runtime skill falls back to the global root only FLAGGED", func(t *testing.T) {
		// 0.49.1 made this repo-only; pm-cli-123 allows the global fallback
		// for machine-knowledge drivers (web-verify) but the flag is what keeps
		// doctor the portability check - see TestResolveHandoffRuntimeSkillGlobalFallback.
		global := t.TempDir()
		md := filepath.Join(global, "skills", "elsewhere", "SKILL.md")
		writeHandoffFile(t, md, "# elsewhere")

		e := Executor{Handoff: Handoff{RuntimeSkill: "elsewhere"}}
		got := e.ResolveHandoff(repo, global)
		if got.SkillPath != md || !got.RuntimeSkillGlobal {
			t.Errorf("a runtime skill living only in the global root resolves there WITH RuntimeSkillGlobal set, got path=%q global=%v", got.SkillPath, got.RuntimeSkillGlobal)
		}
	})
}

func TestResolveHandoffCommandFallback(t *testing.T) {
	repo := t.TempDir()
	cmdPath := filepath.Join(repo, ".claude", "commands", "device-check.md")
	writeHandoffFile(t, cmdPath, "# device-check")

	e := Executor{Handoff: Handoff{RuntimeSkill: "device-check"}}
	got := e.ResolveHandoff(repo, "")
	if got.SkillPath != cmdPath {
		t.Errorf("SkillPath = %q, want the command file %q", got.SkillPath, cmdPath)
	}
	if len(got.Scripts) != 0 {
		t.Errorf("a slash-command carries no scripts dir, got %v", got.Scripts)
	}
}

func TestResolveHandoffRigSkill(t *testing.T) {
	t.Run("resolves from the repo's own skills", func(t *testing.T) {
		repo := t.TempDir()
		md := filepath.Join(repo, ".claude", "skills", "start-rig", "SKILL.md")
		writeHandoffFile(t, md, "# start-rig")

		got := Executor{Handoff: Handoff{RigSkill: "/start-rig"}}.ResolveHandoff(repo, "")
		if got.RigSkill != "start-rig" || got.RigSkillPath != md {
			t.Errorf("got name=%q path=%q, want the slash stripped and %q", got.RigSkill, got.RigSkillPath, md)
		}
	})

	t.Run("falls back to the project's pinned Claude config dir", func(t *testing.T) {
		configDir := t.TempDir() // e.g. ~/.claude-work on a pinned project
		md := filepath.Join(configDir, "skills", "starting-local-rig", "SKILL.md")
		writeHandoffFile(t, md, "# starting-local-rig")

		got := Executor{Handoff: Handoff{RigSkill: "starting-local-rig"}}.ResolveHandoff(t.TempDir(), configDir)
		if got.RigSkillPath != md {
			t.Errorf("RigSkillPath = %q, want the global skill %q", got.RigSkillPath, md)
		}
		if got.GlobalRoot != configDir {
			t.Errorf("GlobalRoot = %q, want the searched dir %q carried for doctor/show", got.GlobalRoot, configDir)
		}
	})

	t.Run("a project-local skill beats the global one", func(t *testing.T) {
		configDir := t.TempDir()
		writeHandoffFile(t, filepath.Join(configDir, "skills", "rig", "SKILL.md"), "# global")
		repo := t.TempDir()
		local := filepath.Join(repo, ".claude", "skills", "rig", "SKILL.md")
		writeHandoffFile(t, local, "# local")

		got := Executor{Handoff: Handoff{RigSkill: "rig"}}.ResolveHandoff(repo, configDir)
		if got.RigSkillPath != local {
			t.Errorf("RigSkillPath = %q, want the project-local %q", got.RigSkillPath, local)
		}
	})

	t.Run("declared but missing resolves to no path", func(t *testing.T) {
		got := Executor{Handoff: Handoff{RigSkill: "ghost"}}.ResolveHandoff(t.TempDir(), t.TempDir())
		if !got.Declared || got.RigSkill != "ghost" || got.RigSkillPath != "" {
			t.Errorf("got %+v, want declared, named, pathless", got)
		}
	})
}

func TestResolveHandoffSkillWithoutScriptsDir(t *testing.T) {
	repo := t.TempDir()
	writeHandoffFile(t, filepath.Join(repo, ".claude", "skills", "verify", "SKILL.md"), "# verify")

	got := Executor{Handoff: Handoff{RuntimeSkill: "verify"}}.ResolveHandoff(repo, "")
	if got.SkillPath == "" {
		t.Fatal("expected the skill to be found")
	}
	if got.ScriptsDir != "" || len(got.Scripts) != 0 {
		t.Errorf("expected no scripts, got dir=%q scripts=%v", got.ScriptsDir, got.Scripts)
	}
}

func TestResolveHandoffRuntimeSkillGlobalFallback(t *testing.T) {
	t.Run("falls back to the pinned Claude config dir and FLAGS it", func(t *testing.T) {
		configDir := t.TempDir() // e.g. ~/.claude-work on a pinned project
		md := filepath.Join(configDir, "skills", "web-verify", "SKILL.md")
		writeHandoffFile(t, md, "# web-verify")
		writeHandoffFile(t, filepath.Join(configDir, "skills", "web-verify", "scripts", "web-ui.mjs"), "")

		got := Executor{Handoff: Handoff{RuntimeSkill: "web-verify"}}.ResolveHandoff(t.TempDir(), configDir)
		if got.SkillPath != md {
			t.Errorf("SkillPath = %q, want the global skill %q", got.SkillPath, md)
		}
		if !got.RuntimeSkillGlobal {
			t.Error("RuntimeSkillGlobal must be true when the repo has no such skill - doctor's WARN hangs on it")
		}
		if len(got.Scripts) != 1 || got.Scripts[0] != "web-ui.mjs" {
			t.Errorf("Scripts = %v, want the global skill's inventory derived like a repo-local one", got.Scripts)
		}
		if got.GlobalRoot != configDir {
			t.Errorf("GlobalRoot = %q, want %q", got.GlobalRoot, configDir)
		}
	})

	t.Run("a repo-local skill wins and is not flagged", func(t *testing.T) {
		configDir := t.TempDir()
		writeHandoffFile(t, filepath.Join(configDir, "skills", "verify", "SKILL.md"), "# global")
		repo := t.TempDir()
		local := filepath.Join(repo, ".claude", "skills", "verify", "SKILL.md")
		writeHandoffFile(t, local, "# local")

		got := Executor{Handoff: Handoff{RuntimeSkill: "verify"}}.ResolveHandoff(repo, configDir)
		if got.SkillPath != local || got.RuntimeSkillGlobal {
			t.Errorf("got path=%q global=%v, want the project-local %q unflagged", got.SkillPath, got.RuntimeSkillGlobal, local)
		}
	})

	t.Run("missing everywhere stays pathless and unflagged", func(t *testing.T) {
		got := Executor{Handoff: Handoff{RuntimeSkill: "ghost"}}.ResolveHandoff(t.TempDir(), t.TempDir())
		if got.SkillPath != "" || got.RuntimeSkillGlobal {
			t.Errorf("got %+v, want no path and no global flag", got)
		}
	})
}
