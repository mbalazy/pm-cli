package storage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

var DefaultStatuses = []TaskStatus{StatusTodo, StatusDoing, StatusWaiting, StatusDone}

type Project struct {
	Name     string            `yaml:"name"`
	Prefix   string            `yaml:"prefix,omitempty"`
	Path     string            `yaml:"path,omitempty"`
	Repo     string            `yaml:"repo,omitempty"`
	Stack    string            `yaml:"stack,omitempty"`
	Links    map[string]string `yaml:"links,omitempty"`
	Tags     []string          `yaml:"tags,omitempty"`
	Statuses []string          `yaml:"statuses,omitempty"`
	Notes    string            `yaml:"notes,omitempty"`
	// Archived marks a dormant project. It is THE source of truth for "asleep"
	// everywhere pm aggregates across projects (ListActiveProjects, the
	// cockpit's home/sidebar/feed): an archived project is left out of every
	// group and every rollup. The board's own hidden_projects (tui-config.yaml)
	// is UI state of one screen, deliberately never read as a fact about the
	// project.
	Archived bool `yaml:"archived,omitempty"`
	// Group joins several repos into ONE project on the cockpit (ACME = acme-api +
	// acme-zap + acme-crm + acme-best; orbit = orbit +
	// orbit-platform + orbit-web). The value is a group slug,
	// validated like a project slug. Empty = the project is a group of its own
	// whose slug is the project slug (see GroupSlug). A display name for a group
	// lives in the global config's `cockpit.groups` block, never here - the
	// name would otherwise be a copy in every member's project.yaml. The TUI
	// and CLI ignore the field and preserve it on write.
	Group    string    `yaml:"group,omitempty"`
	Executor *Executor `yaml:"executor,omitempty"`
	// Slack maps this project onto Slack for the cockpit's change feed
	// (pm-cli-118-19 reads it, the settings screen of 118-18 edits it): the
	// workspace label (which MCP server instance to ask) and the channels
	// whose messages count as this project's changes. Mentions and DMs are
	// never mapped - the feed carries them always, project-less. Nil = the
	// project has no Slack side. The TUI and CLI ignore it and preserve it.
	Slack *SlackConfig `yaml:"slack,omitempty"`
	// Journals declares which subsystems this project keeps a running record
	// of (see journal.go). This list IS the whole of the journal mechanism's
	// project-awareness: pm never infers a subject, so nothing in the code
	// knows what a simulator rig or a flaky sandbox is.
	Journals []JournalSubject `yaml:"journals,omitempty"`
	// ClaudeConfigDir overrides the Claude Code config dir for this project
	// (the dir CLAUDE_CONFIG_DIR points at - holds projects/, credentials, MCP).
	// Empty = the default ~/.claude. Set it when a project runs claude under a
	// separate account/config (e.g. a company Team account in ~/.claude-alt).
	// "~" is expanded. See ResolveClaudeConfigDir.
	ClaudeConfigDir string `yaml:"claude_config_dir,omitempty"`
}

// GroupSlug is the group this project belongs to on the cockpit: its own
// `group` field, or - for a project that declares none - its own slug, so
// every project is in exactly one group and a consumer never special-cases
// the ungrouped.
func (p *Project) GroupSlug(slug string) string {
	if p != nil && p.Group != "" {
		return p.Group
	}
	return slug
}

// ValidateGroup checks a project's group field. Empty is legal (no group);
// anything else must be a slug, because the value becomes a URL segment
// (`/g/<slug>`), a config key (`cockpit.groups.<slug>`) and a filter value
// (`?g=`), and the same rule keeps two spellings of one group from splitting
// it in half.
func ValidateGroup(group string) error {
	if group == "" {
		return nil
	}
	if err := ValidateSlug(group); err != nil {
		return fmt.Errorf("invalid group %q - a group is named like a project slug: %w", group, err)
	}
	return nil
}

// DefaultClaudeConfigDir returns the default Claude Code config dir (~/.claude),
// honoring the CLAUDE_CONFIG_DIR env var if set.
func DefaultClaudeConfigDir() string {
	if env := os.Getenv("CLAUDE_CONFIG_DIR"); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// ResolveClaudeConfigDir returns the absolute Claude config dir for this project:
// the explicit claude_config_dir (with "~" expanded) if set, else the default.
func (p *Project) ResolveClaudeConfigDir() string {
	if p == nil || p.ClaudeConfigDir == "" {
		return DefaultClaudeConfigDir()
	}
	return expandTilde(p.ClaudeConfigDir)
}

// ResolveWorkerClaudeConfigDir is the config dir a HEADLESS WORKER runs under:
// executor.worker_claude_config_dir when set, else the project's
// claude_config_dir (ResolveClaudeConfigDir). Only pm work / pm run-epic use
// it - pm finish always takes ResolveClaudeConfigDir, because the acceptance
// skills live in the user scope the worker dir exists to leave out.
func (p *Project) ResolveWorkerClaudeConfigDir() string {
	if p == nil {
		return DefaultClaudeConfigDir()
	}
	if d := strings.TrimSpace(p.GetExecutor().WorkerClaudeConfigDir); d != "" {
		return expandTilde(d)
	}
	return p.ResolveClaudeConfigDir()
}

func (p *Project) GetStatuses() []TaskStatus {
	if len(p.Statuses) == 0 {
		out := make([]TaskStatus, len(DefaultStatuses))
		copy(out, DefaultStatuses)
		return out
	}
	out := make([]TaskStatus, len(p.Statuses))
	for i, s := range p.Statuses {
		out[i] = ParseStatus(s)
	}
	return out
}

func ReadProject(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Project
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// WriteProject is the exported package-level writer, used by tests and setupTestStore.
// Production code should use Store.CreateProject() or Store.UpdateProject() instead.
func WriteProject(path string, p *Project) error {
	return writeProject(path, p)
}

// knownProjectKeys are the top-level project.yaml keys the Project struct
// owns. On write, keys OUTSIDE this set found in a hand-edited file are
// preserved verbatim instead of being silently dropped by a struct round-trip.
var knownProjectKeys = map[string]bool{
	"name": true, "prefix": true, "path": true, "repo": true, "stack": true,
	"links": true, "tags": true, "statuses": true, "notes": true,
	"archived": true, "executor": true, "claude_config_dir": true,
	"journals": true, "group": true, "slack": true,
}

// SlackConfig is a project's Slack mapping (Project.Slack).
type SlackConfig struct {
	// Workspace names the Slack workspace (the label of one MCP server
	// instance in the feed's slack source); empty = the source's first.
	Workspace string `yaml:"workspace,omitempty"`
	// Channels are channel names, with or without the leading '#'.
	Channels []string `yaml:"channels,omitempty"`
}

// writeProject persists a project WITHOUT destroying what a plain struct
// round-trip cannot represent: comments and unknown keys in a hand-tuned
// project.yaml (executor blocks are exactly the place people annotate). The
// fresh marshal of p is merged INTO the existing file's YAML node tree -
// unchanged subtrees keep their original nodes (and thus their comments) -
// and the result is written atomically (tmp+rename), so a concurrent reader
// never sees a truncated file.
func writeProject(path string, p *Project) error {
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	if old, rerr := os.ReadFile(path); rerr == nil {
		if merged, merr := mergeProjectYAML(old, data); merr == nil {
			data = merged
		}
		// A merge failure (corrupt existing YAML) falls back to the plain
		// marshal - the write must not be blocked by an unparseable old file.
	}
	return atomicWriteFile(path, data, 0644)
}

// mergeProjectYAML merges freshly marshaled project data into the existing
// file's node tree. Top level: known keys follow the new marshal (present ->
// updated in place, absent -> the field was cleared, so dropped), unknown keys
// are preserved verbatim. Below the top level every key is struct-owned, so
// the new marshal decides the key set - but any subtree whose content is
// unchanged keeps its ORIGINAL node, preserving the comments inside it.
func mergeProjectYAML(oldData, newData []byte) ([]byte, error) {
	return mergeYAMLDocuments(oldData, newData, knownProjectKeys)
}

// mergeYAMLDocuments is the merge behind writeProject and SaveConfig: known
// names the top-level keys the struct owns (absent = cleared = dropped);
// every other top-level key in the old document is user data and survives.
func mergeYAMLDocuments(oldData, newData []byte, known map[string]bool) ([]byte, error) {
	var oldDoc, newDoc yaml.Node
	if err := yaml.Unmarshal(oldData, &oldDoc); err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(newData, &newDoc); err != nil {
		return nil, err
	}
	oldMap, newMap := docMapping(&oldDoc), docMapping(&newDoc)
	if oldMap == nil || newMap == nil {
		return newData, nil
	}

	merged := mergeMappingNodes(oldMap, newMap, known)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	// 4 = yaml.Marshal's default indent, which every existing project.yaml was
	// written with - keeping it makes a no-op write byte-identical.
	enc.SetIndent(4)
	if err := enc.Encode(merged); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// docMapping unwraps a document node to its top-level mapping, or nil.
func docMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
		return doc.Content[0]
	}
	return nil
}

// mergeMappingNodes merges the new mapping into the old one, keeping the old
// file's key order and key nodes (which carry head comments). known limits
// which absent keys may be dropped: at the top level an absent UNKNOWN key is
// user data to preserve; below the top level (known == nil) the new marshal
// owns the key set entirely.
func mergeMappingNodes(old, new *yaml.Node, known map[string]bool) *yaml.Node {
	newPairs := make(map[string]*yaml.Node, len(new.Content)/2)
	for i := 0; i+1 < len(new.Content); i += 2 {
		newPairs[new.Content[i].Value] = new.Content[i+1]
	}

	out := *old
	out.Content = nil
	seen := make(map[string]bool)
	for i := 0; i+1 < len(old.Content); i += 2 {
		key, val := old.Content[i], old.Content[i+1]
		seen[key.Value] = true
		newVal, inNew := newPairs[key.Value]
		if !inNew {
			if known != nil && !known[key.Value] {
				out.Content = append(out.Content, key, val) // unknown key: preserve
			}
			continue // known key cleared: drop
		}
		out.Content = append(out.Content, key, mergeValueNodes(val, newVal))
	}
	// New keys the old file did not have, in the new marshal's order.
	for i := 0; i+1 < len(new.Content); i += 2 {
		if !seen[new.Content[i].Value] {
			out.Content = append(out.Content, new.Content[i], new.Content[i+1])
		}
	}
	return &out
}

// mergeValueNodes picks the node for one key's value: recursive merge for
// mappings; otherwise the ORIGINAL node when the content is unchanged (so its
// comments and style survive), else the new one. Equality is on the DECODED
// value, never the serialized node - marshaling a node renders its comments
// too, which would make every commented-but-unchanged value read as changed.
func mergeValueNodes(old, new *yaml.Node) *yaml.Node {
	if old.Kind == yaml.MappingNode && new.Kind == yaml.MappingNode {
		return mergeMappingNodes(old, new, nil)
	}
	var a, b any
	if old.Decode(&a) == nil && new.Decode(&b) == nil && reflect.DeepEqual(a, b) {
		return old
	}
	return new
}
