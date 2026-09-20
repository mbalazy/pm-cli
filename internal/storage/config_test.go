package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig drops a config.yaml into the store's root.
func writeConfig(t *testing.T, s *Store, body string) {
	t.Helper()
	if err := os.WriteFile(s.ConfigPath(), []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

const sampleConfig = `remotes:
  - name: runner
    ssh: runner
    pm: /home/runner/go/bin/pm
    root: /home/runner/.claude/pm
`

func TestLoadConfigMissingFile(t *testing.T) {
	store := &Store{Root: t.TempDir()}

	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatalf("missing config must not be an error, got %v", err)
	}
	if len(cfg.Remotes) != 0 {
		t.Errorf("remotes = %d, want 0", len(cfg.Remotes))
	}
	if cfg.Exists {
		t.Error("Exists = true for a file that is not there")
	}
	if want := filepath.Join(store.Root, "config.yaml"); cfg.Path != want {
		t.Errorf("Path = %q, want %q", cfg.Path, want)
	}
	if _, ok := cfg.Remote("runner"); ok {
		t.Error("Remote found something in an empty config")
	}
}

func TestLoadConfigReadsRemotes(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	writeConfig(t, store, sampleConfig)

	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Exists {
		t.Error("Exists = false for a file that is there")
	}
	if len(cfg.Remotes) != 1 {
		t.Fatalf("remotes = %d, want 1", len(cfg.Remotes))
	}
	r, ok := cfg.Remote("runner")
	if !ok {
		t.Fatal(`Remote("runner") not found`)
	}
	if r.SSH != "runner" || r.PM != "/home/runner/go/bin/pm" || r.Root != "/home/runner/.claude/pm" {
		t.Errorf("remote = %+v, want the fields from the file", *r)
	}
	if _, ok := cfg.Remote("nosuch"); ok {
		t.Error(`Remote("nosuch") reported found`)
	}
}

// The registry is what tells `pm runs` which machines to ask, so an unreadable
// file must NOT read as "you have no remotes" - see LoadConfig's comment.
func TestLoadConfigBadYAMLErrors(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	writeConfig(t, store, "remotes:\n  - name: runner\n   ssh: broken indent\n")

	_, err := store.LoadConfig()
	if err == nil {
		t.Fatal("unparsable config must be an error, got nil")
	}
	if !strings.Contains(err.Error(), store.ConfigPath()) {
		t.Errorf("error %q does not name the file %q", err, store.ConfigPath())
	}
	// yaml.v3 carries the line; without it the user has nothing to look at.
	if !strings.Contains(err.Error(), "line ") {
		t.Errorf("error %q does not carry a line number", err)
	}
}

func TestLoadConfigRejectsBadEntries(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string // substrings the message must carry
	}{
		{
			name: "missing ssh",
			body: "remotes:\n  - name: runner\n    pm: /bin/pm\n    root: /root\n",
			want: []string{"runner", "ssh"},
		},
		{
			name: "missing pm",
			body: "remotes:\n  - name: runner\n    ssh: runner\n    root: /root\n",
			want: []string{"runner", "pm is empty"},
		},
		{
			name: "missing root",
			body: "remotes:\n  - name: runner\n    ssh: runner\n    pm: /bin/pm\n",
			want: []string{"runner", "root is empty"},
		},
		{
			name: "missing name",
			body: "remotes:\n  - ssh: runner\n    pm: /bin/pm\n    root: /root\n",
			want: []string{"remotes[0]", "name is empty"},
		},
		{
			// The name reaches CLI arguments and messages, so it obeys
			// ValidateSlug like a project slug does.
			name: "name breaks ValidateSlug",
			body: "remotes:\n  - name: ../escape\n    ssh: runner\n    pm: /bin/pm\n    root: /root\n",
			want: []string{"remotes[0]", "invalid slug"},
		},
		{
			name: "uppercase name breaks ValidateSlug",
			body: "remotes:\n  - name: Runner\n    ssh: runner\n    pm: /bin/pm\n    root: /root\n",
			want: []string{"invalid slug"},
		},
		{
			name: "duplicate name",
			body: sampleConfig + "  - name: runner\n    ssh: other\n    pm: /bin/pm\n    root: /root\n",
			want: []string{"duplicate name", "runner", "remotes[0]"},
		},
		{
			// The second entry is the broken one: the message must point at it,
			// not at the healthy first.
			name: "second entry named",
			body: sampleConfig + "  - name: other\n    ssh: other\n    root: /root\n",
			want: []string{"remotes[1]", "other", "pm is empty"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &Store{Root: t.TempDir()}
			writeConfig(t, store, tt.body)

			_, err := store.LoadConfig()
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			if !strings.Contains(err.Error(), store.ConfigPath()) {
				t.Errorf("error %q does not name the file", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// An empty file and an explicitly empty list are both "no remotes", not a parse
// failure: a user commenting out their only remote must not break every read.
func TestLoadConfigEmptyBodies(t *testing.T) {
	for _, body := range []string{"", "\n", "# nothing here\n", "remotes:\n", "remotes: []\n"} {
		store := &Store{Root: t.TempDir()}
		writeConfig(t, store, body)

		cfg, err := store.LoadConfig()
		if err != nil {
			t.Fatalf("body %q: %v", body, err)
		}
		if len(cfg.Remotes) != 0 {
			t.Errorf("body %q: remotes = %d, want 0", body, len(cfg.Remotes))
		}
		if !cfg.Exists {
			t.Errorf("body %q: Exists = false for a file that is there", body)
		}
	}
}

// The path MUST come off Store.Root, never off os.UserHomeDir: otherwise a test
// (or any PM_DATA_DIR-scoped run) would read the real user's config.
func TestLoadConfigHonorsPMDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PM_DATA_DIR", dir)

	store := NewStore()
	if store.Root != dir {
		t.Fatalf("store root = %q, want %q", store.Root, dir)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(sampleConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Path != filepath.Join(dir, "config.yaml") {
		t.Errorf("Path = %q, want it inside PM_DATA_DIR", cfg.Path)
	}
	if _, ok := cfg.Remote("runner"); !ok {
		t.Error("remote from the PM_DATA_DIR config not found")
	}
}

// config.yaml lands next to the project DIRECTORIES; nothing may start treating
// it as a project of its own.
func TestConfigFileIsNotAProject(t *testing.T) {
	store, _ := setupTestStore(t)
	writeConfig(t, store, sampleConfig)

	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	for _, p := range projects {
		if p == "config.yaml" || p == "config" {
			t.Fatalf("ListProjects returned the config file as a project: %v", projects)
		}
	}
	if len(projects) != 1 || projects[0] != "alpha" {
		t.Errorf("projects = %v, want just the one project dir", projects)
	}
}

func TestRemoteOnNilConfig(t *testing.T) {
	var cfg *PMConfig
	if _, ok := cfg.Remote("runner"); ok {
		t.Error("Remote on a nil config reported found")
	}
}
