// Package solo starts a /solo Claude Code session from the cockpit: the user
// fills project + queue + flags, sees the exact command in the confirmation
// dialog (Plan), and pm runs it as a Claude Code BACKGROUND session
// (`claude --bg`, CC 2.1.272) in the project's main checkout. The session
// lives under Claude Code's own supervisor - not under pm serve - so the user
// "jumps in" from any terminal with `claude attach <id>`, reads it with
// `claude logs <id>` and ends it with `claude stop <id>`. pm keeps one launch
// record per session under <pm-root>/.cockpit/solo/ and reads the live state
// from `claude agents --json --all`, the supported interface (the files under
// <config-dir>/jobs/ are not a contract).
//
// The argv restates the author's own launcher once (bypass permissions, a
// 400k autocompact window, `--setting-sources user,local` when the repo's
// shared settings carry `ask` rules) and adds what a background session needs
// to behave like an interactive one in the main checkout:
// `--settings '{"worktree":{"bgIsolation":"none"}}'` - without it a bg
// session blocks Edit/Write until it moves into a .claude/worktrees/ worktree,
// and the solo skill works in the main checkout by design. Warnings are the
// board's kind: shown, never blocking - the skill's own Step 0 is the gate.
//
// Verified 2026-09-15 (task pm-cli-141): `--bg` starts from a process with no
// TTY; `--session-id` is IGNORED under `--bg` (the short id printed is the
// first 8 chars of the session UUID the supervisor mints); `pm session-id`
// inside the session returns that UUID; a process killed by a signal is
// RESTARTED by the supervisor, so a stop is only ever `claude stop`.
package solo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// Runtime values of Input.Runtime.
const (
	RuntimeDefault = ""    // the skill decides (on when the project declares a runtime skill)
	RuntimeSim     = "sim" // --sim
	RuntimeWeb     = "web" // --web
	RuntimeOff     = "off" // --no-runtime
)

// States of a launch, as `claude agents --json` words them, plus pm's own
// three for the moments the supervisor has no row: starting (just launched,
// no row yet), unknown (no row and not fresh), error (the launch itself
// failed).
const (
	StateStarting = "starting"
	StateWorking  = "working"
	StateBlocked  = "blocked"
	StateDone     = "done"
	StateFailed   = "failed"
	StateStopped  = "stopped"
	StateUnknown  = "unknown"
	StateError    = "error"
)

// BgTimeout is how long Start waits for `claude --bg` to print the session
// id - the first launch also starts the supervisor.
const BgTimeout = 45 * time.Second

// AgentsTimeout bounds one `claude agents --json --all` call; AgentsTTL is
// how long its answer is reused (the web polls every 5 s; the call is a
// node start-up).
const (
	AgentsTimeout = 20 * time.Second
	AgentsTTL     = 3 * time.Second
)

// StartingGrace is how long a launch with no supervisor row yet reads as
// "starting" rather than "unknown".
const StartingGrace = 2 * time.Minute

// LaunchSource is what pm stamps into PM_LAUNCH_SOURCE for the session.
const LaunchSource = "cockpit"

// bgIsolationSettings is the inline --settings that keeps a background
// session editing the main checkout (see the package comment).
const bgIsolationSettings = `{"worktree":{"bgIsolation":"none"}}`

// ErrNotFound is an unknown launch id.
var ErrNotFound = errors.New("solo launch not found")

// InputError is a caller-caused failure (400): a bad queue, an unknown
// runtime, a project without a checkout.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// StartError is a launch that did not produce a session: claude exited
// non-zero, printed no id, or could not be found.
type StartError struct {
	Msg string
	Log string
}

func (e *StartError) Error() string { return e.Msg }

// Input is the launch form; Queue is handed to the skill verbatim.
type Input struct {
	Project string `json:"project"`
	// Queue is what /solo takes: a tracker id, task ids, a ticket key, a
	// link, or pasted text.
	Queue string `json:"queue"`
	// Runtime is "", sim, web or off (RuntimeDefault..RuntimeOff).
	Runtime string `json:"runtime,omitempty"`
	// Base is --base <branch>; empty = the skill's default.
	Base string `json:"base,omitempty"`
	// Model is --model <alias>; empty = the account's default.
	Model    string `json:"model,omitempty"`
	Push     bool   `json:"push,omitempty"`
	PR       bool   `json:"pr,omitempty"`
	MaxTasks int    `json:"max_tasks,omitempty"`
	MaxHours int    `json:"max_hours,omitempty"`
}

// Plan is what a launch would run - the dialog's preview IS the argv Start
// runs, byte for byte.
type Plan struct {
	Project string `json:"project"`
	Input   Input  `json:"input"`
	// Exe is the resolved claude binary; Argv its arguments (claude excluded).
	Exe  string   `json:"exe"`
	Argv []string `json:"argv"`
	// Prompt is the "/solo ..." line alone.
	Prompt    string `json:"prompt"`
	Cwd       string `json:"cwd"`
	ConfigDir string `json:"config_dir"`
	// Name is the session's display name in agent view.
	Name     string   `json:"name"`
	Warnings []string `json:"warnings,omitempty"`
	// AskRules > 0 means the repo's shared settings carry `ask` rules and
	// --setting-sources user,local was added (see buildArgv).
	AskRules int `json:"ask_rules"`
}

// Agent is one row of `claude agents --json --all`.
type Agent struct {
	ID         string `json:"id,omitempty"`
	Kind       string `json:"kind"`
	Cwd        string `json:"cwd"`
	StartedAt  int64  `json:"startedAt"`
	SessionID  string `json:"sessionId,omitempty"`
	Name       string `json:"name,omitempty"`
	PID        int    `json:"pid,omitempty"`
	Status     string `json:"status,omitempty"`
	State      string `json:"state,omitempty"`
	WaitingFor string `json:"waitingFor,omitempty"`
}

// Launch is one background solo session as stored plus what is derived on
// read from the supervisor.
type Launch struct {
	// ID is the short id `claude --bg` printed (attach/logs/stop take it).
	ID string `json:"id"`
	// SessionID is the full session UUID (the shift id the skill uses),
	// filled from the supervisor's row on the first read that has it.
	SessionID string   `json:"session_id,omitempty"`
	Project   string   `json:"project"`
	Input     Input    `json:"input"`
	Argv      []string `json:"argv"`
	Cwd       string   `json:"cwd"`
	ConfigDir string   `json:"config_dir"`
	Name      string   `json:"name"`
	Started   string   `json:"started"`
	// Log is the launcher's stdout+stderr file (what `claude --bg` printed).
	Log string `json:"log"`
	// Error is a launch that produced no session.
	Error string `json:"error,omitempty"`
	// Stopped is when the cockpit ran `claude stop` on it.
	Stopped string `json:"stopped,omitempty"`

	// Derived on read, never stored.
	State      string `json:"state"`
	Status     string `json:"status,omitempty"`
	WaitingFor string `json:"waiting_for,omitempty"`
	PID        int    `json:"pid,omitempty"`
	// Attach and Logs are the commands to paste in a terminal.
	Attach string `json:"attach"`
	Logs   string `json:"logs"`
}

// Live is a launch whose session is still doing something (or waiting on
// the user).
func (l *Launch) Live() bool {
	return l.State == StateStarting || l.State == StateWorking || l.State == StateBlocked
}

// Controller plans, starts and reads solo launches.
type Controller struct {
	Store storage.TaskStore
	// Exe is the claude binary; "" = "claude" on PATH, else ~/.local/bin/claude.
	Exe string
	// Now is the clock; nil = time.Now.
	Now func() time.Time
	// BgTimeout / AgentsTimeout override the package constants (tests).
	BgTimeout     time.Duration
	AgentsTimeout time.Duration

	cache map[string]agentsCache
}

type agentsCache struct {
	at   time.Time
	rows []Agent
	err  error
}

// New is a controller over the store with the real binary.
func New(store storage.TaskStore) *Controller { return &Controller{Store: store} }

func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Controller) dir() string { return filepath.Join(c.Store.RootDir(), ".cockpit", "solo") }

func (c *Controller) path(id, ext string) string { return filepath.Join(c.dir(), id+ext) }

// exe resolves the claude binary: the configured one, else PATH, else the
// user-local install pm serve may not have on its PATH.
func (c *Controller) exe() (string, error) {
	if c.Exe != "" {
		return c.Exe, nil
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		local := filepath.Join(home, ".local", "bin", "claude")
		if fi, err := os.Stat(local); err == nil && !fi.IsDir() {
			return local, nil
		}
	}
	return "", fmt.Errorf("claude not found on PATH (nor ~/.local/bin/claude)")
}

var modelOK = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Validate checks the form; a failure is an *InputError.
func (in Input) Validate() error {
	q := strings.TrimSpace(in.Queue)
	switch {
	case in.Project == "":
		return &InputError{Msg: "project is required"}
	case q == "":
		return &InputError{Msg: "queue is required: a tracker id, task ids, a ticket key, a link or a sentence"}
	case strings.ContainsAny(q, "\n\r"):
		return &InputError{Msg: "queue must be one line"}
	}
	switch in.Runtime {
	case RuntimeDefault, RuntimeSim, RuntimeWeb, RuntimeOff:
	default:
		return &InputError{Msg: fmt.Sprintf("runtime must be one of sim, web, off or empty; got %q", in.Runtime)}
	}
	if in.Model != "" && !modelOK.MatchString(in.Model) {
		return &InputError{Msg: fmt.Sprintf("model %q: letters, digits, . _ : - only", in.Model)}
	}
	if strings.ContainsAny(in.Base, " \t\n") {
		return &InputError{Msg: fmt.Sprintf("base %q must be a branch name", in.Base)}
	}
	if in.MaxTasks < 0 || in.MaxHours < 0 {
		return &InputError{Msg: "max_tasks and max_hours must be >= 0"}
	}
	return nil
}

// Prompt is the /solo line the session starts with.
func (in Input) Prompt() string {
	parts := []string{"/solo", strings.TrimSpace(in.Queue)}
	switch in.Runtime {
	case RuntimeSim:
		parts = append(parts, "--sim")
	case RuntimeWeb:
		parts = append(parts, "--web")
	case RuntimeOff:
		parts = append(parts, "--no-runtime")
	}
	if in.Push {
		parts = append(parts, "--push")
	}
	if in.PR {
		parts = append(parts, "--pr")
	}
	if in.MaxTasks > 0 {
		parts = append(parts, "--max-tasks", strconv.Itoa(in.MaxTasks))
	}
	if in.MaxHours > 0 {
		parts = append(parts, "--max-hours", strconv.Itoa(in.MaxHours))
	}
	if in.Base != "" {
		parts = append(parts, "--base", in.Base)
	}
	return strings.Join(parts, " ")
}

// buildArgv is the launcher rule plus the background flags, as one pure function:
//
//	--dangerously-skip-permissions --autocompact 400k --settings <bgIsolation>
//	[--setting-sources user,local]   (askRules > 0)
//	[--model <m>] --name <name> --bg "<prompt>"
func buildArgv(in Input, askRules int, name string) []string {
	args := []string{"--dangerously-skip-permissions", "--autocompact", "400k", "--settings", bgIsolationSettings}
	if askRules > 0 {
		args = append(args, "--setting-sources", "user,local")
	}
	if in.Model != "" {
		args = append(args, "--model", in.Model)
	}
	args = append(args, "--name", name, "--bg", in.Prompt())
	return args
}

// askRules counts `permissions.ask` in the repo's shared settings (they
// prompt in every mode, bypass included).
func askRules(cwd string) int {
	data, err := os.ReadFile(filepath.Join(cwd, ".claude", "settings.json"))
	if err != nil {
		return 0
	}
	var s struct {
		Permissions struct {
			Ask []any `json:"ask"`
		} `json:"permissions"`
	}
	if json.Unmarshal(data, &s) != nil {
		return 0
	}
	return len(s.Permissions.Ask)
}

// Plan resolves the form to the exact launch: argv, cwd, config dir, and the
// warnings a human would want to see first. Reads local files only.
func (c *Controller) Plan(in Input) (*Plan, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	slug, proj, err := c.project(in.Project)
	if err != nil {
		return nil, err
	}
	in.Project = slug
	in.Queue = strings.TrimSpace(in.Queue)
	cwd := proj.Path
	if cwd == "" {
		return nil, &InputError{Msg: fmt.Sprintf("project %s has no path - set it in project.yaml", slug)}
	}
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return nil, &InputError{Msg: fmt.Sprintf("project %s: path %s is not a directory", slug, cwd)}
	}
	p := &Plan{
		Project:   slug,
		Input:     in,
		Prompt:    in.Prompt(),
		Cwd:       cwd,
		ConfigDir: proj.ResolveClaudeConfigDir(),
		Name:      "solo-" + slug,
		AskRules:  askRules(cwd),
	}
	p.Argv = buildArgv(in, p.AskRules, p.Name)
	exe, err := c.exe()
	if err != nil {
		p.Exe = "claude"
		p.Warnings = append(p.Warnings, err.Error()+" - the launch will fail")
	} else {
		p.Exe = exe
	}
	// The prompt types `/solo`; a config dir without that skill starts a
	// session that answers "unknown skill" and sits there.
	if missing := storage.MissingSkills(p.ConfigDir, "solo"); len(missing) > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("no /solo skill under %s/skills - the session would start with nothing to follow; %s", p.ConfigDir, storage.SkillsInstallHint(p.ConfigDir)))
	}
	if p.AskRules > 0 {
		if _, err := os.Stat(filepath.Join(cwd, ".claude", "settings.local.json")); err != nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s/.claude/settings.json has %d ask rule(s) and there is no .claude/settings.local.json: the shared deny list is NOT in effect under --setting-sources user,local - mirror it first, or the skill's launch-check refuses the shift", cwd, p.AskRules))
		}
	}
	p.Warnings = append(p.Warnings, gitWarnings(cwd, in.Base, proj.GetExecutor().BaseBranch)...)
	// An open shift or a live launch of the same project: two shifts in one
	// checkout would fight over the branch.
	for _, sh := range storage.ReadShifts(c.Store.ProjectDir(slug), slug) {
		if sh.Open {
			p.Warnings = append(p.Warnings, fmt.Sprintf("a solo shift of %s is already open (%s, %s) - a second one works in the same checkout", slug, sh.ID, sh.Date))
			break
		}
	}
	if ls, err := c.List(); err == nil {
		for _, l := range ls {
			if l.Project == slug && l.Live() {
				p.Warnings = append(p.Warnings, fmt.Sprintf("solo session %s of %s is still %s - claude attach %s to see it", l.ID, slug, l.State, l.ID))
				break
			}
		}
	}
	return p, nil
}

// project resolves a slug or prefix to the project; unknown = storage.ErrProjectNotFound.
func (c *Controller) project(input string) (string, *storage.Project, error) {
	slug, err := c.Store.ResolveProject(input)
	if err != nil {
		return "", nil, err
	}
	proj, err := c.Store.GetProject(slug)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %s", storage.ErrProjectNotFound, slug)
	}
	return slug, proj, nil
}

// gitWarnings: not a checkout, a dirty tree, a base branch that does not
// exist or has drifted from origin - each one something the skill's Step 0
// would stop on or fork from unexpectedly. Local refs only, no fetch.
func gitWarnings(cwd, base, defaultBase string) []string {
	var out []string
	git := func(args ...string) (string, error) {
		o, err := exec.Command("git", append([]string{"-C", cwd}, args...)...).Output()
		return strings.TrimSpace(string(o)), err
	}
	if _, err := git("rev-parse", "--is-inside-work-tree"); err != nil {
		return []string{cwd + " is not a git checkout - the skill's Step 0 will stop"}
	}
	if st, err := git("status", "--porcelain"); err == nil && st != "" {
		n := len(strings.Split(st, "\n"))
		out = append(out, fmt.Sprintf("%d uncommitted change(s) in %s - the skill's Step 0 stops on a dirty tree", n, cwd))
	}
	branch := base
	if branch == "" {
		branch = defaultBase
	}
	if branch == "" {
		return out
	}
	if _, err := git("rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		out = append(out, fmt.Sprintf("branch %s does not exist locally", branch))
		return out
	}
	if _, err := git("rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch); err != nil {
		return out
	}
	if n, err := git("rev-list", "--count", branch+"..origin/"+branch); err == nil && n != "0" {
		out = append(out, fmt.Sprintf("origin/%s is %s commit(s) ahead of local %s", branch, n, branch))
	}
	if n, err := git("rev-list", "--count", "origin/"+branch+"..."+branch, "--right-only"); err == nil && n != "0" {
		out = append(out, fmt.Sprintf("local %s is %s commit(s) ahead of origin/%s - the skill forks from origin, push first", branch, n, branch))
	}
	return out
}

// PinConfigDir says whether a launch must carry CLAUDE_CONFIG_DIR=<dir>: only
// for a NON-default dir. Setting the variable to the default ~/.claude is not
// a no-op: claude keys its keychain login by the config dir the variable
// names, so an explicit default reads as a separate, never-logged-in
// profile ("Not logged in · Please run /login") - the executor's workerEnv
// rule, learnt again on the first cockpit solo launch (2026-09-15).
func PinConfigDir(configDir string) bool {
	if configDir == "" {
		return false
	}
	home, _ := os.UserHomeDir()
	return filepath.Clean(configDir) != filepath.Join(home, ".claude")
}

// Environ is the session's environment: pm serve's own, minus the API key
// (the subscription pays), minus every Claude Code marker of the session pm
// serve itself may have been started from (CLAUDE_CODE_SESSION_ID is what
// `pm session-id` reads first - the shift id and the guard marker hang on
// it), plus the project's config dir when it is not the default
// (PinConfigDir) and the launch source. Never PM_HEADLESS: a solo session
// drives the runtime, and project hooks read that variable as "no simulator".
func Environ(configDir string) []string {
	src := os.Environ()
	out := make([]string, 0, len(src)+2)
	for _, kv := range src {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "ANTHROPIC_API_KEY", k == "ANTHROPIC_AUTH_TOKEN", k == "PM_HEADLESS",
			k == "CLAUDECODE", k == "CLAUDE_PID", k == "CLAUDE_CONFIG_DIR", k == "CLAUDE_JOB_DIR",
			k == storage.LaunchSourceEnv, strings.HasPrefix(k, "CLAUDE_CODE_"):
			continue
		}
		out = append(out, kv)
	}
	if PinConfigDir(configDir) {
		out = append(out, "CLAUDE_CONFIG_DIR="+configDir)
	}
	return append(out, storage.LaunchSourceEnv+"="+LaunchSource)
}

// backgroundedLine is what `claude --bg` prints: "backgrounded · 7c5dcf5d · name".
var backgroundedLine = regexp.MustCompile(`backgrounded\s+·\s+([A-Za-z0-9._-]+)`)

// Start runs the plan's argv as a background session and records the
// launch. It waits for `claude --bg` to return (BgTimeout) because the id
// is on its stdout; the session itself keeps running under the supervisor.
func (c *Controller) Start(p *Plan) (*Launch, error) {
	if p == nil || len(p.Argv) == 0 || p.Cwd == "" {
		return nil, &InputError{Msg: "empty plan"}
	}
	exe, err := c.exe()
	if err != nil {
		return nil, &StartError{Msg: err.Error()}
	}
	if err := os.MkdirAll(c.dir(), 0o755); err != nil {
		return nil, err
	}
	timeout := c.BgTimeout
	if timeout <= 0 {
		timeout = BgTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, p.Argv...)
	cmd.Dir = p.Cwd
	cmd.Env = Environ(p.ConfigDir)
	cmd.Stdin = nil
	cmd.Stdout = &out
	cmd.Stderr = &out
	// Its own session: no controlling terminal, and the supervisor it may
	// start is not pm serve's child in any way that matters.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	started := c.now()
	runErr := cmd.Run()
	text := out.String()
	m := backgroundedLine.FindStringSubmatch(text)
	l := &Launch{
		Project: p.Project, Input: p.Input, Argv: append([]string{exe}, p.Argv...), Cwd: p.Cwd,
		ConfigDir: p.ConfigDir, Name: p.Name, Started: started.Format(time.RFC3339),
	}
	if m == nil {
		msg := "claude --bg printed no session id"
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			msg = "claude --bg did not return within " + timeout.String()
		case runErr != nil:
			msg = "claude --bg failed: " + runErr.Error()
		}
		if t := strings.TrimSpace(text); t != "" {
			msg += " - " + firstLines(t, 3)
		}
		// Keep the failure on disk too, under a stamp, so the table can show
		// what happened.
		l.ID = "failed-" + started.Format("20060102-150405.000000")
		l.Error = msg
		l.Log = c.path(l.ID, ".log")
		_ = os.WriteFile(l.Log, []byte(text), 0o644)
		_ = c.write(l)
		return nil, &StartError{Msg: msg, Log: l.Log}
	}
	l.ID = m[1]
	if !validID.MatchString(l.ID) {
		return nil, &StartError{Msg: fmt.Sprintf("claude --bg printed an unexpected id %q", l.ID)}
	}
	l.Log = c.path(l.ID, ".log")
	if err := os.WriteFile(l.Log, []byte(text), 0o644); err != nil {
		return nil, err
	}
	if err := c.write(l); err != nil {
		return nil, err
	}
	c.invalidate(p.ConfigDir)
	return c.load(l.ID)
}

func (c *Controller) write(l *Launch) error {
	stored := *l
	stored.State, stored.Status, stored.WaitingFor, stored.PID, stored.Attach, stored.Logs = "", "", "", 0, "", ""
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path(l.ID, ".json"), append(data, '\n'), 0o644)
}

// List returns every launch, newest first, with the live state.
func (c *Controller) List() ([]Launch, error) {
	entries, err := os.ReadDir(c.dir())
	if errors.Is(err, os.ErrNotExist) {
		return []Launch{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Launch{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		l, err := c.load(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		out = append(out, *l)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started > out[j].Started })
	return out, nil
}

// Get returns one launch with the live state.
func (c *Controller) Get(id string) (*Launch, error) {
	if !validID.MatchString(id) {
		return nil, ErrNotFound
	}
	return c.load(id)
}

// Stop ends the session with `claude stop <id>` (the supervisor keeps the
// conversation; `claude attach` reopens it). Never a signal: the
// supervisor restarts a process that dies on it.
func (c *Controller) Stop(id string) (*Launch, error) {
	l, err := c.Get(id)
	if err != nil {
		return nil, err
	}
	if l.Error != "" {
		return nil, &InputError{Msg: "launch " + id + " never started: " + l.Error}
	}
	exe, err := c.exe()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.agentsTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "stop", id)
	cmd.Env = Environ(l.ConfigDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("claude stop %s: %v - %s", id, err, firstLines(string(out), 2))
	}
	l.Stopped = c.now().Format(time.RFC3339)
	if err := c.write(l); err != nil {
		return nil, err
	}
	c.invalidate(l.ConfigDir)
	return c.load(id)
}

func (c *Controller) load(id string) (*Launch, error) {
	data, err := os.ReadFile(c.path(id, ".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var l Launch
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	c.derive(&l)
	return &l, nil
}

// derive fills the live part of a launch from the supervisor's row.
func (c *Controller) derive(l *Launch) {
	prefix := ""
	if PinConfigDir(l.ConfigDir) {
		prefix = "CLAUDE_CONFIG_DIR=" + l.ConfigDir + " "
	}
	l.Attach = prefix + "claude attach " + l.ID
	l.Logs = prefix + "claude logs " + l.ID
	if l.Error != "" {
		l.State, l.Attach, l.Logs = StateError, "", ""
		return
	}
	rows, err := c.Agents(l.ConfigDir)
	if err != nil {
		l.State = StateUnknown
		l.WaitingFor = "claude agents: " + err.Error()
		return
	}
	for _, a := range rows {
		if a.ID != l.ID {
			continue
		}
		l.State, l.Status, l.WaitingFor, l.PID = a.State, a.Status, a.WaitingFor, a.PID
		if l.SessionID == "" && a.SessionID != "" {
			l.SessionID = a.SessionID
			_ = c.write(l) // remember the shift id; best effort
		}
		if l.State == "" {
			l.State = StateUnknown
		}
		return
	}
	if l.Stopped != "" {
		l.State = StateStopped
		return
	}
	if at, ok := storage.ParseStamp(l.Started); ok && c.now().Sub(at) < StartingGrace {
		l.State = StateStarting
		return
	}
	l.State = StateUnknown
}

func (c *Controller) agentsTimeout() time.Duration {
	if c.AgentsTimeout > 0 {
		return c.AgentsTimeout
	}
	return AgentsTimeout
}

func (c *Controller) invalidate(configDir string) {
	if c.cache != nil {
		delete(c.cache, configDir)
	}
}

// Agents runs `claude agents --json --all` under the config dir (each dir
// is its own supervisor) and returns the background rows; answers are
// reused for AgentsTTL.
func (c *Controller) Agents(configDir string) ([]Agent, error) {
	if c.cache == nil {
		c.cache = map[string]agentsCache{}
	}
	if e, ok := c.cache[configDir]; ok && c.now().Sub(e.at) < AgentsTTL {
		return e.rows, e.err
	}
	rows, err := c.agents(configDir)
	c.cache[configDir] = agentsCache{at: c.now(), rows: rows, err: err}
	return rows, err
}

func (c *Controller) agents(configDir string) ([]Agent, error) {
	exe, err := c.exe()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.agentsTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "agents", "--json", "--all")
	cmd.Env = Environ(configDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("claude agents --json: %v - %s", err, firstLines(stderr.String(), 2))
	}
	var rows []Agent
	if err := json.Unmarshal(bytes.TrimSpace(out), &rows); err != nil {
		return nil, fmt.Errorf("claude agents --json: %v", err)
	}
	bg := rows[:0]
	for _, a := range rows {
		if a.Kind == "background" {
			bg = append(bg, a)
		}
	}
	return bg, nil
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " / ")
}
