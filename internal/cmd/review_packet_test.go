package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// packetRepo builds a real git repo and returns its dir plus the sha the
// "worker" started from. A fixture rather than a canned string on purpose: the
// packet is only worth anything if it can read a diff the way git emits one.
func packetRepo(t *testing.T, files map[string]string) (dir, base string) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "init")
	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	base = strings.TrimSpace(string(out))

	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, base
}

func TestBuildReviewPacketCarriesTheWholeDiff(t *testing.T) {
	dir, base := packetRepo(t, map[string]string{
		"main.go":      "package main\n\nfunc add(a, b int) int { return a + b }\n",
		"main_test.go": "package main\n\nfunc TestAdd(t *testing.T) {}\n",
	})
	packet := buildReviewPacket(dir, base)
	if packet == "" {
		t.Fatal("a real diff must produce a packet")
	}
	// promptCarriesDiff is what the telemetry and the gate both use; the packet
	// must satisfy it, or pm would attach a second copy on the next spawn.
	if !promptCarriesDiff(packet) {
		t.Error("the packet must read as a diff")
	}
	// The test file is IN the packet even though review_cap.go excludes it from
	// the reviewer COUNT - a test encoding the wrong behaviour is exactly what
	// review has to catch.
	for _, want := range []string{"main.go", "main_test.go", "func add", "attached by pm"} {
		if !strings.Contains(packet, want) {
			t.Errorf("packet missing %q", want)
		}
	}
	// Uncommitted work counts: the worker may not have committed yet when it
	// spawns its reviewers.
	if !strings.Contains(packet, "return a + b") {
		t.Error("uncommitted changes must be in the packet")
	}
}

// A documentation-only change gets a different brief, because the generic one
// is a category error on prose: it asks for correctness bugs and unhandled
// cases in text that has neither, and what comes back is wording to argue
// about. What can actually be wrong in a document is what it claims about the
// code, so that is what the single reviewer is asked for.
func TestBuildReviewPacketBriefsADocumentReviewerDifferently(t *testing.T) {
	dir, base := packetRepo(t, map[string]string{
		"docs/plan.md": "# Test plan\n\nRun `scripts/does-not-exist.sh` and read src/gone.ts.\n",
	})
	packet := buildReviewPacket(dir, base)
	if packet == "" {
		t.Fatal("a real diff must produce a packet")
	}
	for _, want := range []string{"Review it as a DOCUMENT", "does not exist or does not say", "Do NOT review wording", "attached by pm"} {
		if !strings.Contains(packet, want) {
			t.Errorf("the document brief must contain %q", want)
		}
	}
	// The generic brief must be gone, not merely added to: two sets of
	// instructions in front of one reviewer is how it ends up doing both jobs.
	if strings.Contains(packet, "judge the change on what is here") {
		t.Error("the generic code brief must not survive on a doc-only change")
	}
	if !strings.Contains(packet, "docs/plan.md") {
		t.Error("the document itself must be in the packet")
	}

	// One code file alongside it and the change is a code change again.
	dir2, base2 := packetRepo(t, map[string]string{
		"docs/plan.md": "# Test plan\n",
		"src/a.ts":     "export const a = 1\n",
	})
	if p := buildReviewPacket(dir2, base2); strings.Contains(p, "Review it as a DOCUMENT") {
		t.Error("a change that touches code is not a documentation change")
	}
}

func TestBuildReviewPacketDegradesOnALargeDiff(t *testing.T) {
	files := map[string]string{}
	// One clearly biggest file, plus enough others to blow the budget.
	files["huge.go"] = "package main\n" + strings.Repeat("// a line of the biggest file\n", 1200)
	for i := 0; i < 12; i++ {
		files[fmt.Sprintf("small%02d.go", i)] = "package main\n" + strings.Repeat("// filler\n", 400)
	}
	dir, base := packetRepo(t, files)

	packet := buildReviewPacket(dir, base)
	if packet == "" {
		t.Fatal("no packet")
	}
	// The budget is a budget; the header, fence and note ride on top of it.
	if len(packet) > reviewPacketBudget*2 {
		t.Errorf("packet is %d bytes, budget is %d - degradation did not happen", len(packet), reviewPacketBudget)
	}
	if !strings.Contains(packet, "exceeded the size pm will put in a prompt") {
		t.Error("a degraded packet must say so - a reviewer that thinks it has everything reports on a change it only half saw")
	}
	// --stat names every file, so nothing changed is invisible even when its
	// diff is not included.
	for i := 0; i < 12; i++ {
		if !strings.Contains(packet, fmt.Sprintf("small%02d.go", i)) {
			t.Fatalf("the summary must name every changed file, missing small%02d.go", i)
		}
	}
	// Largest first: the file most worth reading in full is the one that gets
	// the room.
	if !strings.Contains(packet, "the biggest file") {
		t.Error("the largest changed file must be included in full")
	}
}

func TestBuildReviewPacketDegradesToNothing(t *testing.T) {
	dir, base := packetRepo(t, nil) // no changes at all
	if got := buildReviewPacket(dir, base); got != "" {
		t.Errorf("an empty diff must produce no packet, got %d bytes", len(got))
	}
	if got := buildReviewPacket(dir, ""); got != "" {
		t.Error("no base sha must produce no packet")
	}
	if got := buildReviewPacket(t.TempDir(), "deadbeef"); got != "" {
		t.Error("a directory that is not a git repo must produce no packet")
	}
}

func TestAttachReviewPacket(t *testing.T) {
	dir, base := packetRepo(t, map[string]string{"main.go": "package main\n\nvar x = 1\n"})

	t.Run("a reviewer spawn with no diff gets one", func(t *testing.T) {
		ti := map[string]any{"subagent_type": reviewerAgentType, "prompt": "review the change"}
		if !attachReviewPacket(ti, dir, base) {
			t.Fatal("packet not attached")
		}
		got, _ := ti["prompt"].(string)
		if !strings.HasPrefix(got, "review the change") {
			t.Error("the spawning agent's own prompt must survive, in front")
		}
		if !promptCarriesDiff(got) {
			t.Error("the prompt must end up carrying the diff")
		}
	})

	t.Run("a worker that already sent the diff is not given a second copy", func(t *testing.T) {
		ti := map[string]any{
			"subagent_type": reviewerAgentType,
			"prompt":        "review this:\ndiff --git a/x b/x\n@@ -1 +1 @@\n",
		}
		if attachReviewPacket(ti, dir, base) {
			t.Error("a prompt that already carries a diff must be left alone")
		}
	})

	t.Run("a custom type is left alone", func(t *testing.T) {
		ti := map[string]any{"subagent_type": "project-linter", "prompt": "lint"}
		if attachReviewPacket(ti, dir, base) {
			t.Error("pm must not rewrite the prompt of a type it does not own")
		}
	})
}

// The reviewer must not be able to shell out: Bash was 340 calls and 0.69M
// characters across the 16 reviewers of orbit-106, and a reviewer holding
// the diff has nothing to shell out for.
func TestReviewerHasNoBash(t *testing.T) {
	var got map[string]struct {
		Tools []string `json:"tools"`
	}
	if err := json.Unmarshal([]byte(reviewerAgentsJSON("sonnet")), &got); err != nil {
		t.Fatal(err)
	}
	for _, tool := range got[reviewerAgentType].Tools {
		if tool == "Bash" {
			t.Error("the reviewer definition must not carry Bash")
		}
	}
	if len(got[reviewerAgentType].Tools) == 0 {
		t.Error("a reviewer with no tools at all cannot read a file the diff points at")
	}
}

// End to end through the hook, because the packet is only delivered if the
// rewrite actually reaches Claude Code.
func TestWorkerGuardAttachesThePacket(t *testing.T) {
	dir, base := packetRepo(t, map[string]string{"main.go": "package main\n\nvar changed = true\n"})
	payload, _ := json.Marshal(map[string]any{
		"tool_name": "Agent",
		"cwd":       dir,
		"tool_input": map[string]any{
			"subagent_type": reviewerAgentType,
			"prompt":        "Review the branch diff against the AC.",
		},
	})
	var out, errOut bytes.Buffer
	code := runWorkerGuard(bytes.NewReader(payload), &out, &errOut,
		guardOptions{diffBase: base, reviewModel: "sonnet", telemetryPath: filepath.Join(t.TempDir(), "t.jsonl")})
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, errOut.String())
	}
	var resp struct {
		Hook struct {
			Reason  string         `json:"permissionDecisionReason"`
			Updated map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("stdout: %v (%q)", err, out.String())
	}
	prompt, _ := resp.Hook.Updated["prompt"].(string)
	if !promptCarriesDiff(prompt) {
		t.Errorf("the rewritten prompt must carry the diff, got %q", prompt)
	}
	if !strings.Contains(prompt, "var changed = true") {
		t.Error("the packet must carry the actual change")
	}
	// The reason is what a human reads in a transcript when wondering why the
	// prompt is not what the worker wrote.
	if !strings.Contains(resp.Hook.Reason, "attached the diff") {
		t.Errorf("reason = %q", resp.Hook.Reason)
	}
}
