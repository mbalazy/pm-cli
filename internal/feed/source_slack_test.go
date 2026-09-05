package feed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// fakeMCP puts the fake stdio MCP server (testdata/fake_mcp.py) on PATH as
// `slack-mcp-fake`, the fakeClaude pattern: the source runs a real
// subprocess over the real go-sdk transport, and the canned CSV comes
// from a script file per workspace (through the server spec's env).
func fakeMCP(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "fake_mcp.py"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "slack-mcp-fake")
	if err := os.WriteFile(exe, src, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return "slack-mcp-fake"
}

func scriptFile(t *testing.T, m map[string]string) string {
	t.Helper()
	data, _ := json.Marshal(m)
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func slackTS(d time.Duration) string { return feedNow.Add(-d).Format("2006-01-02T15:04:05Z07:00") }

func TestSlackSourceOverAFakeMCPServer(t *testing.T) {
	exe := fakeMCP(t)
	store := feedStore(t)
	if _, err := store.MutateProject("alpha", func(p *storage.Project) error {
		p.Slack = &storage.SlackConfig{Workspace: "atlas", Channels: []string{"#dev", "product"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProject("acme-api", &storage.Project{Name: "acme-api", Group: "acme", Slack: &storage.SlackConfig{Workspace: "acme", Channels: []string{"acme-api-dev"}}}); err != nil {
		t.Fatal(err)
	}
	leScript := scriptFile(t, map[string]string{
		"conversations_history|#dev": "UserID,UserName,Channel,ThreadTs,Text,Time\n" +
			"U1,marta,C0DEV,,\"Fixed the header\nsecond line\"," + slackTS(2*time.Hour) + "\n" +
			"U2,me-login,C0DEV,,my own note," + slackTS(time.Hour) + "\n" +
			"U3,old,C0DEV,,before the cutoff," + slackTS(40*time.Hour) + "\n" +
			",,,,,\n",
		"conversations_history|#product": "UserID,UserName,Channel,ThreadTs,Text,Time\n",
		"conversations_search_messages|mention": "UserID,UserName,Channel,ThreadTs,Text,Time,Permalink\n" +
			"U9,antoine,C0FLASH,1.0,\"@me-login B2B admin first\"," + slackTS(3*time.Hour) + ",https://slack.com/archives/C0FLASH/p1\n" +
			"U2,me-login,C0FLASH,,\"@me-login echo of myself\"," + slackTS(time.Hour) + ",\n",
		"conversations_search_messages|dm": "UserID,UserName,Channel,ThreadTs,Text,Time\n" +
			"U4,sarah,@sarah_dm,,\"are we on for Monday?\"," + slackTS(4*time.Hour) + "\n",
	})
	acmeScript := scriptFile(t, map[string]string{
		"conversations_history|#acme-api-dev": "UserID,UserName,Channel,ThreadTs,Text,Time\n" +
			"U7,sarah,C0LDMS,1755000000.000100,branding deadline?,1755001000.000200\n",
	})
	cfg := cfgWith(map[string]bool{"slack": true})
	cfg.Slack.Servers = []storage.SlackServer{
		{Workspace: "atlas", Command: exe, Env: map[string]string{"FAKE_MCP_SCRIPT": leScript}, Me: "@me-login"},
		{Workspace: "acme", Command: exe, Env: map[string]string{"FAKE_MCP_SCRIPT": acmeScript}},
	}
	src := &SlackSource{}
	f := New(store.Root, []Source{src})
	// The acme message's epoch (1755001000) is in Aug 2025 - outside the
	// window - so it proves the epoch parse and the window filter at once.
	res, err := f.Refresh(context.Background(), store, cfg, feedFrom, feedNow)
	if err != nil {
		t.Fatal(err)
	}
	st := res.Sources[0]
	if st.Name != "slack" || !st.Enabled {
		t.Fatalf("status = %+v", st)
	}
	// acme has no `me`: said so, per workspace, without stopping orbit.
	if !strings.Contains(st.Error, "workspace acme") || !strings.Contains(st.Error, "no `me`") {
		t.Fatalf("error = %q", st.Error)
	}
	if strings.Contains(st.Error, "workspace orbit") {
		t.Fatalf("orbit must be clean: %q", st.Error)
	}
	ch, _ := f.Read(feedFrom)
	byTitle := map[string]Event{}
	for _, e := range ch.Events {
		byTitle[e.Title] = e
	}
	if len(ch.Events) != 3 {
		t.Fatalf("events = %d: %+v", len(ch.Events), byTitle)
	}
	// A mapped channel message: the project's, first line only, info.
	dev, ok := byTitle["#dev · marta: Fixed the header"]
	if !ok || dev.Project != "alpha" || dev.Group != "grp" || dev.Severity != SeverityInfo || dev.Detail != "workspace orbit" {
		t.Fatalf("dev = %+v (have %v)", dev, keys(byTitle))
	}
	// A mention: project-less, warn, with the permalink; my own echo skipped.
	mention, ok := byTitle["#c0flash · antoine: @me-login B2B admin first"]
	if !ok || mention.Project != "" || mention.Severity != SeverityWarn || mention.URL != "https://slack.com/archives/C0FLASH/p1" || !strings.HasPrefix(mention.Detail, "mention · ") || !strings.Contains(mention.Detail, "in a thread") {
		t.Fatalf("mention = %+v", mention)
	}
	// A DM: project-less, warn.
	dm, ok := byTitle["@sarah_dm · sarah: are we on for Monday?"]
	if !ok || dm.Project != "" || dm.Severity != SeverityWarn || !strings.HasPrefix(dm.Detail, "dm · ") {
		t.Fatalf("dm = %+v", dm)
	}
	// My own note, the pre-cutoff row and the cursor row produced nothing.
	for title := range byTitle {
		if strings.Contains(title, "my own") || strings.Contains(title, "before the cutoff") {
			t.Fatalf("unexpected event %q", title)
		}
	}
	// Idempotent: a second refresh adds nothing (stable ids).
	res, _ = f.Refresh(context.Background(), store, cfg, feedFrom, feedNow)
	if res.Added != 0 {
		t.Fatalf("second refresh added %d", res.Added)
	}
}

func keys(m map[string]Event) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSlackSourceFailures(t *testing.T) {
	exe := fakeMCP(t)
	store := feedStore(t)
	if _, err := store.MutateProject("alpha", func(p *storage.Project) error {
		p.Slack = &storage.SlackConfig{Workspace: "atlas", Channels: []string{"dev"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Run("no servers configured is a source error, nothing dialled", func(t *testing.T) {
		src := &SlackSource{}
		src.Configure(cfgWith(nil))
		_, err := src.Fetch(context.Background(), feedFrom, feedNow, nil)
		if err == nil || !strings.Contains(err.Error(), "cockpit.slack.servers") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a hung server is a per-workspace timeout; the other workspace still answers; a dying server is reported", func(t *testing.T) {
		old := SlackTimeout
		SlackTimeout = 500 * time.Millisecond
		t.Cleanup(func() { SlackTimeout = old })
		okScript := scriptFile(t, map[string]string{"conversations_history|#dev": "UserName,Text,Time\nmarta,hello," + slackTS(time.Hour) + "\n"})
		cfg := cfgWith(map[string]bool{"slack": true})
		cfg.Slack.Servers = []storage.SlackServer{
			{Workspace: "hung", Command: exe, Env: map[string]string{"FAKE_MCP_MODE": "hang", "FAKE_MCP_SCRIPT": okScript}, Me: "@x"},
			{Workspace: "atlas", Command: exe, Env: map[string]string{"FAKE_MCP_SCRIPT": okScript}, Me: "@me-login"},
			{Workspace: "dead", Command: exe, Env: map[string]string{"FAKE_MCP_MODE": "die"}},
		}
		// The hung workspace has a mapped channel too, so its history call hangs.
		if err := store.CreateProject("beta", &storage.Project{Name: "beta", Slack: &storage.SlackConfig{Workspace: "hung", Channels: []string{"dev"}}}); err != nil {
			t.Fatal(err)
		}
		src := &SlackSource{}
		src.Configure(cfg)
		started := time.Now()
		projects, _ := activeProjects(store)
		events, err := src.Fetch(context.Background(), feedFrom, feedNow, projects)
		if time.Since(started) > 5*time.Second {
			t.Fatalf("fetch took %s - the timeout did not bite", time.Since(started))
		}
		if err == nil || !strings.Contains(err.Error(), "workspace hung") || !strings.Contains(err.Error(), "workspace dead") {
			t.Fatalf("err = %v", err)
		}
		if len(events) != 1 || events[0].Title != "#dev · marta: hello" || events[0].Project != "alpha" {
			t.Fatalf("events = %+v", events)
		}
		var pe *ProjectError
		if !errors.As(err, &pe) {
			t.Fatalf("errors must be ProjectErrors: %v", err)
		}
	})
	t.Run("a project mapping a workspace with no server is reported", func(t *testing.T) {
		cfg := cfgWith(map[string]bool{"slack": true})
		cfg.Slack.Servers = []storage.SlackServer{{Workspace: "other", Command: exe, Env: map[string]string{"FAKE_MCP_SCRIPT": scriptFile(t, map[string]string{})}, Me: "@m"}}
		src := &SlackSource{}
		src.Configure(cfg)
		projects, _ := activeProjects(store)
		_, err := src.Fetch(context.Background(), feedFrom, feedNow, projects)
		if err == nil || !strings.Contains(err.Error(), "workspace orbit") || !strings.Contains(err.Error(), "no such workspace") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestResolveServerFromClaudeConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude.json")
	doc := `{"mcpServers":{"top":{"type":"stdio","command":"npx","args":["-y","x"],"env":{"A":"1"}}},
	"projects":{"/repo":{"mcpServers":{"slack-orbit":{"command":"npx","args":["-y","slack-mcp-server@latest","--transport","stdio"],"env":{"SLACK_MCP_XOXC_TOKEN":"xoxc"}}}},
	"/other":{"mcpServers":{"http-one":{"type":"http","url":"https://x"}}}}}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := ResolveServer(storage.SlackServer{Workspace: "atlas", ClaudeServer: "slack-orbit"}, path)
	if err != nil || spec.Command != "npx" || len(spec.Args) != 4 || len(spec.Env) != 1 || spec.Env[0] != "SLACK_MCP_XOXC_TOKEN=xoxc" {
		t.Fatalf("spec = %+v %v", spec, err)
	}
	if spec, err := ResolveServer(storage.SlackServer{ClaudeServer: "top"}, path); err != nil || spec.Env[0] != "A=1" {
		t.Fatalf("top-level = %+v %v", spec, err)
	}
	if _, err := ResolveServer(storage.SlackServer{ClaudeServer: "http-one"}, path); err == nil || !strings.Contains(err.Error(), "not stdio") {
		t.Fatalf("http = %v", err)
	}
	if _, err := ResolveServer(storage.SlackServer{ClaudeServer: "nope"}, path); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("unknown = %v", err)
	}
	if spec, err := ResolveServer(storage.SlackServer{Command: "./srv", Args: []string{"a"}, Env: map[string]string{"K": "v"}}, path); err != nil || spec.Command != "./srv" || spec.Env[0] != "K=v" {
		t.Fatalf("explicit = %+v %v", spec, err)
	}
	if _, err := ResolveServer(storage.SlackServer{ClaudeServer: "x"}, filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("a missing config must be an error")
	}
}

func TestParseMessagesIsHeaderDriven(t *testing.T) {
	msgs, err := parseMessages("Time,Text,user_name,channel_id,permalink\n2026-09-05T10:00:00Z,hi there,bob,C1,https://x\n,,,,\n")
	if err != nil || len(msgs) != 1 || msgs[0].user != "bob" || msgs[0].channel != "C1" || msgs[0].link != "https://x" || msgs[0].ts != time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC).Format(time.RFC3339) {
		t.Fatalf("msgs = %+v %v", msgs, err)
	}
	if _, err := parseMessages("a,b\n1,2\n"); err == nil || !strings.Contains(err.Error(), "no text/time") {
		t.Fatalf("no columns = %v", err)
	}
	if msgs, err := parseMessages("  "); err != nil || msgs != nil {
		t.Fatalf("empty = %+v %v", msgs, err)
	}
	if normalizeTS("1755001000.000200") == "" || normalizeTS("2026-09-05 10:00:00") == "" || normalizeTS("garbage") != "" {
		t.Fatal("normalizeTS")
	}
	if mentionQuery("U0ABC") != "<@U0ABC>" || mentionQuery("@me-login") != "@me-login" || mentionQuery("me-login") != "@me-login" {
		t.Fatal("mentionQuery")
	}
	if historyLimit(feedFrom, feedNow) != "1d" || historyLimit(feedNow.Add(-50*time.Hour), feedNow) != "3d" {
		t.Fatal("historyLimit")
	}
}
