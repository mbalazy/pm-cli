package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Global pm configuration: <Store.Root>/config.yaml, i.e. ~/.claude/pm/config.yaml
// by default and whatever PM_DATA_DIR points at otherwise.
//
// WHY GLOBAL AND NOT PER PROJECT. Its only content today is the registry of
// remote runners (the VPS a batch runs on). One such machine serves MANY
// projects, so writing it into every project.yaml would be a copy of the same
// three strings in N files - and a copy is the thing that drifts. The knowledge
// lives in one place, next to the projects rather than inside one of them.
//
// The file sits alongside the other non-project files already in the pm root
// (focus.yaml, tui-config.yaml, session-log.txt). ListProjects only considers
// DIRECTORIES holding a project.yaml, so a file here is never mistaken for a
// project (TestConfigFileIsNotAProject pins that).
//
// READING IS THROUGH Store.Root, never through os.UserHomeDir: the root honours
// PM_DATA_DIR, and a hand-joined home path would make every test reach into the
// real user's data.
//
// WRITTEN BY pm ONLY THROUGH SaveConfig (added with the cockpit block, so the
// settings screen can edit it), which pays the same debt writeProject pays:
// the fresh marshal is merged into the existing YAML node tree, so comments
// and unknown keys survive. The remotes block stays hand-authored in
// practice; nothing in pm edits it.

// ConfigFileName is the global config's name inside the pm root.
const ConfigFileName = "config.yaml"

// PMConfig is the resolved global configuration.
type PMConfig struct {
	Remotes []Remote `yaml:"remotes,omitempty"`

	// Cockpit is the web cockpit's settings block (cockpit.go). Always
	// resolved: a missing file or block yields DefaultCockpitConfig, and a
	// file's keys are decoded OVER those defaults, so an absent key keeps its
	// default and an explicit zero wins.
	Cockpit CockpitConfig `yaml:"cockpit"`

	// Path is where this config was read from, and Exists says whether that
	// file was actually there. Both are resolved state, not file content -
	// `pm config show` prints the source, and a caller that finds no remotes
	// needs to distinguish "no file" from "a file listing none".
	Path   string `yaml:"-"`
	Exists bool   `yaml:"-"`
}

// Remote is one machine that runs pm besides this one.
type Remote struct {
	// Name is the short handle used in CLI arguments and messages.
	Name string `yaml:"name"`
	// SSH is the host as ~/.ssh/config knows it.
	SSH string `yaml:"ssh"`
	// PM is the absolute path to the pm binary on that machine.
	PM string `yaml:"pm"`
	// Root is that machine's pm data dir.
	Root string `yaml:"root"`
}

// ConfigPath returns the global config file's path under this store's root.
func (s *Store) ConfigPath() string {
	return filepath.Join(s.Root, ConfigFileName)
}

// LoadConfig reads the global config.
//
// A MISSING FILE IS NOT AN ERROR - the vast majority of pm installs have no
// remote runner at all, so absence is the normal state and yields an empty
// config.
//
// AN UNPARSABLE FILE IS AN ERROR, never a silent degradation to zero remotes:
// a YAML typo would otherwise make `pm runs` quietly stop showing remote runs,
// and the user would read the empty column as "nothing is running" rather than
// as "pm could not read your config". Same rule as ValidateJournalName
// rejecting an undeclared name instead of opening a second file. The error
// carries the file name, and yaml.v3's own message carries the line.
func (s *Store) LoadConfig() (*PMConfig, error) {
	path := s.ConfigPath()
	cfg := &PMConfig{Path: path, Cockpit: DefaultCockpitConfig()}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg.Exists = true

	// Unknown top-level keys are tolerated on purpose, as in project.yaml: a
	// hand-tuned file may carry keys a newer (or older) pm owns, and rejecting
	// them would make the config unreadable to the very versions that must
	// keep working with it.
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.validate(path); err != nil {
		return nil, err
	}
	if err := cfg.Cockpit.validate(path); err != nil {
		return nil, err
	}
	return cfg, nil
}

// knownConfigKeys are the top-level config.yaml keys PMConfig owns; any other
// top-level key in the file is preserved verbatim by SaveConfig.
var knownConfigKeys = map[string]bool{"remotes": true, "cockpit": true}

// SaveConfig writes the config back, keeping what a struct round-trip cannot
// carry: comments and unknown keys in the existing file (mergeYAMLDocuments,
// the writeProject mechanism). The cockpit block is written as RESOLVED -
// every default becomes an explicit key on the first save - which is the
// price of decoding over defaults instead of through pointer fields; the
// file stays readable by an older pm, which tolerates unknown keys. The
// write is atomic (tmp+rename). Path and Exists are not content and are
// never written; the file is created if missing.
//
// Validated before writing, so a bad value from a settings form never
// lands on disk where it would make the NEXT LoadConfig fail.
func (s *Store) SaveConfig(cfg *PMConfig) error {
	if cfg == nil {
		return fmt.Errorf("save config: nil config")
	}
	path := s.ConfigPath()
	if err := cfg.validate(path); err != nil {
		return err
	}
	if err := cfg.Cockpit.validate(path); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if old, rerr := os.ReadFile(path); rerr == nil {
		if merged, merr := mergeYAMLDocuments(old, data, knownConfigKeys); merr == nil {
			data = merged
		}
	}
	return atomicWriteFile(path, data, 0644)
}

// MutateConfig is the read-modify-write for the config: load fresh, apply
// fn, save. No lock: the file is edited by a person or by one settings
// screen, and a flock here would be the first one on the pm root.
func (s *Store) MutateConfig(fn func(*PMConfig) error) (*PMConfig, error) {
	cfg, err := s.LoadConfig()
	if err != nil {
		return nil, err
	}
	if err := fn(cfg); err != nil {
		return nil, err
	}
	if err := s.SaveConfig(cfg); err != nil {
		return nil, err
	}
	cfg.Exists = true
	return cfg, nil
}

// validate rejects a registry that cannot be acted on. Every message names the
// offending entry - by name when it has one, by index when it does not - since
// the whole point of failing loudly is that the user can find the typo.
func (c *PMConfig) validate(path string) error {
	seen := make(map[string]int, len(c.Remotes))
	for i, r := range c.Remotes {
		where := fmt.Sprintf("%s: remotes[%d]", path, i)
		if r.Name != "" {
			where = fmt.Sprintf("%s (%s)", where, r.Name)
		}
		if r.Name == "" {
			return fmt.Errorf("%s: name is empty", where)
		}
		// The name reaches CLI arguments and messages, so it obeys the same
		// rule as a project slug.
		if err := ValidateSlug(r.Name); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if prev, dup := seen[r.Name]; dup {
			return fmt.Errorf("%s: duplicate name %q (already used by remotes[%d])", where, r.Name, prev)
		}
		seen[r.Name] = i
		for _, f := range []struct{ key, val string }{
			{"ssh", r.SSH},
			{"pm", r.PM},
			{"root", r.Root},
		} {
			if f.val == "" {
				return fmt.Errorf("%s: %s is empty", where, f.key)
			}
		}
	}
	return nil
}

// Remote returns the named remote. The bool is the answer to "is there one" -
// callers name a remote on the command line, and an unknown name must be told
// apart from a known one with empty fields (which LoadConfig already refuses).
func (c *PMConfig) Remote(name string) (*Remote, bool) {
	if c == nil {
		return nil, false
	}
	for i := range c.Remotes {
		if c.Remotes[i].Name == name {
			return &c.Remotes[i], true
		}
	}
	return nil, false
}
