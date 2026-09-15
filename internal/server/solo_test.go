package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/solo"
	"github.com/mbalazy/pm/internal/storage"
)

// The cockpit's solo launcher (pm-cli-141): plan -> 202 start -> the launch
// on GET /api/solo beside the shifts -> the shift file joins by session id
// -> stop. A fake claude prints the documented `--bg` block and canned
// agent rows; no real session anywhere.
func TestSolo(t *testing.T) {
	store := newTestStore(t)
	checkout := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", checkout}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if _, err := store.MutateProject("test", func(p *storage.Project) error { p.Path = checkout; return nil }); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	exe := filepath.Join(bin, "claude")
	agents := filepath.Join(bin, "agents.json")
	calls := filepath.Join(bin, "calls.txt")
	script := `#!/bin/sh
case "$1" in
agents) cat "$FAKE_AGENTS" ;;
stop) echo "stop $2" >> "$FAKE_CALLS"; echo "stopped $2" ;;
*) printf '%s\n' "$@" > "$FAKE_OUT"; echo "backgrounded · ab12cd34 · solo-test" ;;
esac
`
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agents, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_AGENTS", agents)
	t.Setenv("FAKE_CALLS", calls)
	t.Setenv("FAKE_OUT", filepath.Join(bin, "argv.txt"))
	srv := newServer(t, store, Options{Solo: &solo.Controller{Store: store, Exe: exe}})

	t.Run("plan: the argv preview, 400 on bad input, 404 on an unknown project", func(t *testing.T) {
		m := getJSON(t, srv.URL+"/api/solo/plan?project=test&queue=t-2&runtime=off&base=main&model=opus&push=1&max_hours=3", 200)
		if m["prompt"] != "/solo t-2 --no-runtime --push --max-hours 3 --base main" || m["cwd"] != checkout || m["name"] != "solo-test" {
			t.Fatalf("plan = %v", m)
		}
		argv := m["argv"].([]any)
		if argv[0] != "--dangerously-skip-permissions" || argv[len(argv)-2] != "--bg" || argv[len(argv)-1] != m["prompt"] {
			t.Fatalf("argv = %v", argv)
		}
		joined := ""
		for _, a := range argv {
			joined += a.(string) + " "
		}
		if !strings.Contains(joined, `--settings {"worktree":{"bgIsolation":"none"}} `) || !strings.Contains(joined, "--model opus ") {
			t.Fatalf("argv = %s", joined)
		}
		if s, _, _ := get(t, srv.URL+"/api/solo/plan?project=test&queue="); s != 400 {
			t.Fatalf("empty queue = %d", s)
		}
		if s, _, _ := get(t, srv.URL+"/api/solo/plan?project=test&queue=x&runtime=phone"); s != 400 {
			t.Fatalf("bad runtime = %d", s)
		}
		if s, _, _ := get(t, srv.URL+"/api/solo/plan?project=nope&queue=x"); s != 404 {
			t.Fatalf("unknown project = %d", s)
		}
	})

	t.Run("start needs the header, answers 202, the launch is on /api/solo before any shift file", func(t *testing.T) {
		req := strings.NewReader(`{"project":"test","queue":"t-2","runtime":"off"}`)
		resp, err := srv.Client().Post(srv.URL+"/api/solo", "application/json", req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatalf("no header = %d", resp.StatusCode)
		}
		postJSON(t, srv.URL+"/api/solo", `{"project":"test","queue":"t-2","runtime":"off","typo":1}`, 400)
		postJSON(t, srv.URL+"/api/solo", `{"project":"test","queue":""}`, 400)
		m := postJSON(t, srv.URL+"/api/solo", `{"project":"test","queue":"t-2","runtime":"off"}`, 202)
		if m["id"] != "ab12cd34" || m["state"] != "starting" || m["attach"] != "claude attach ab12cd34" || m["project"] != "test" {
			t.Fatalf("start = %v", m)
		}
		argv, _ := os.ReadFile(os.Getenv("FAKE_OUT"))
		if !strings.HasSuffix(strings.TrimSpace(string(argv)), "--bg\n/solo t-2 --no-runtime") {
			t.Fatalf("fake claude saw %s", argv)
		}
		all := getJSON(t, srv.URL+"/api/solo", 200)
		launches := all["launches"].([]any)
		if len(launches) != 1 || launches[0].(map[string]any)["id"] != "ab12cd34" || len(all["shifts"].([]any)) != 0 {
			t.Fatalf("solo = %v", all)
		}
	})

	t.Run("the supervisor's row and the shift file join by session id; stop runs claude stop", func(t *testing.T) {
		if err := os.WriteFile(agents, []byte(`[{"id":"ab12cd34","kind":"background","cwd":"`+checkout+`","startedAt":1,"sessionId":"ab12cd34-9999-0000-1111-222222222222","name":"solo-test","pid":77,"status":"busy","state":"working"}]`), 0o644); err != nil {
			t.Fatal(err)
		}
		shiftDir := filepath.Join(store.ProjectDir("test"), ".shift")
		if err := os.MkdirAll(shiftDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(shiftDir, "2026-09-15-ab12cd34-9999-0000-1111-222222222222.md"), []byte("# solo shift\n\n## Status: open\n\n## Queue\n- t-2 · Tracker · todo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// The controller caches agent rows for 3 s; a fresh handler does not share the cache.
		srv2 := newServer(t, store, Options{Solo: &solo.Controller{Store: store, Exe: exe}})
		all := getJSON(t, srv2.URL+"/api/solo", 200)
		l := all["launches"].([]any)[0].(map[string]any)
		sh := all["shifts"].([]any)[0].(map[string]any)
		if l["state"] != "working" || l["status"] != "busy" || l["pid"] != float64(77) || l["session_id"] != sh["id"] {
			t.Fatalf("launch = %v, shift = %v", l, sh)
		}
		one := getJSON(t, srv2.URL+"/api/solo/ab12cd34", 200)
		if one["session_id"] != "ab12cd34-9999-0000-1111-222222222222" {
			t.Fatalf("get = %v", one)
		}
		m := postJSON(t, srv2.URL+"/api/solo/ab12cd34/stop", ``, 200)
		if m["stopped"] == "" || m["stopped"] == nil {
			t.Fatalf("stop = %v", m)
		}
		if b, _ := os.ReadFile(calls); !strings.Contains(string(b), "stop ab12cd34") {
			t.Fatalf("calls = %s", b)
		}
		postJSON(t, srv2.URL+"/api/solo/nope/stop", ``, 404)
		if s, _, _ := get(t, srv2.URL+"/api/solo/nope"); s != 404 {
			t.Fatalf("unknown launch = %d", s)
		}
	})
}
