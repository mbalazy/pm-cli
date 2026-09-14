package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mbalazy/pm/internal/storage"
)

// pm serve as an MCP CLIENT (pm-cli-118-19): the slack source runs the
// user's registered Slack MCP server as a stdio subprocess and calls its
// tools - the same server Claude Code talks to, with the same tokens, in
// the one place they already live (~/.claude.json). No LLM in the loop:
// pm asks for messages and reads CSV back. EXCLUDED on purpose: calling
// the Slack Web API with a token of pm's own - a second place with the
// tokens, duplicating what the server already does.

// ServerSpec is a resolved stdio MCP server: what to run and how.
type ServerSpec struct {
	Command string
	Args    []string
	// Env entries (KEY=VALUE) appended to the environment.
	Env []string
}

// ToolCaller is the slice of an MCP session the slack source needs. Tests
// fake it or run the fake stdio server through the real one.
type ToolCaller interface {
	// CallText calls a tool and returns the concatenated text content;
	// a tool-level error (IsError) is returned as an error.
	CallText(ctx context.Context, tool string, args map[string]any) (string, error)
	Close() error
}

// Dialer connects to a server. The default runs it as a subprocess.
type Dialer func(ctx context.Context, spec ServerSpec) (ToolCaller, error)

// stdioSession is a ToolCaller over the go-sdk command transport.
type stdioSession struct {
	session *mcp.ClientSession
}

// DialStdio starts spec as a subprocess (its own process group, killed as a
// group on Close) and completes the MCP handshake.
func DialStdio(ctx context.Context, spec ServerSpec) (ToolCaller, error) {
	if spec.Command == "" {
		return nil, fmt.Errorf("no command to run")
	}
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.Stderr = nil // a server's stderr is noise; the tool errors say what matters
	client := mcp.NewClient(&mcp.Implementation{Name: "pm-serve", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Command, err)
	}
	return &stdioSession{session: session}, nil
}

func (s *stdioSession) CallText(ctx context.Context, tool string, args map[string]any) (string, error) {
	res, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("%s: %w", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	if res.IsError {
		return "", fmt.Errorf("%s: %s", tool, firstLine(b.String()))
	}
	return b.String(), nil
}

func (s *stdioSession) Close() error { return s.session.Close() }

func firstLine(s string) string {
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return "tool error with no text"
}

// ResolveServer turns a config entry into a ServerSpec: the spelled-out
// command, or the named entry of Claude Code's config (top-level
// mcpServers, or under any project - the user's Slack servers are
// registered per project).
func ResolveServer(sv storage.SlackServer, claudeConfig string) (ServerSpec, error) {
	if sv.Command != "" {
		return ServerSpec{Command: sv.Command, Args: sv.Args, Env: envList(sv.Env)}, nil
	}
	doc, path, err := readClaudeConfig(claudeConfig)
	if err != nil {
		return ServerSpec{}, err
	}
	if cs, ok := doc.MCPServers[sv.ClaudeServer]; ok {
		return cs.spec()
	}
	for _, p := range doc.Projects {
		if cs, ok := p.MCPServers[sv.ClaudeServer]; ok {
			return cs.spec()
		}
	}
	return ServerSpec{}, fmt.Errorf("claude config %s has no mcpServers entry %q (top-level or under a project)", path, sv.ClaudeServer)
}

type claudeConfigDoc struct {
	MCPServers map[string]claudeServer `json:"mcpServers"`
	Projects   map[string]struct {
		MCPServers map[string]claudeServer `json:"mcpServers"`
	} `json:"projects"`
}

// readClaudeConfig reads Claude Code's config (~/.claude.json by default).
func readClaudeConfig(claudeConfig string) (*claudeConfigDoc, string, error) {
	path := claudeConfig
	if path == "" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, "", err
		}
		if path == "" {
			path = filepath.Join(home, ".claude.json")
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("claude config %s: %w", path, err)
	}
	var doc claudeConfigDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, path, fmt.Errorf("claude config %s: %w", path, err)
	}
	return &doc, path, nil
}

// ClaudeServerNames lists the distinct mcpServers names of Claude Code's
// config (top-level and under every project), sorted.
func ClaudeServerNames(claudeConfig string) ([]string, error) {
	doc, _, err := readClaudeConfig(claudeConfig)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for n := range doc.MCPServers {
		seen[n] = true
	}
	for _, p := range doc.Projects {
		for n := range p.MCPServers {
			seen[n] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

type claudeServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

func (c claudeServer) spec() (ServerSpec, error) {
	if c.Type != "" && c.Type != "stdio" {
		return ServerSpec{}, fmt.Errorf("mcp server type %q is not stdio", c.Type)
	}
	if c.Command == "" {
		return ServerSpec{}, fmt.Errorf("mcp server entry has no command")
	}
	return ServerSpec{Command: c.Command, Args: c.Args, Env: envList(c.Env)}, nil
}

func envList(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
