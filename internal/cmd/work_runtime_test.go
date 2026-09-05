package cmd

import (
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func TestRuntimeDirective(t *testing.T) {
	on := &storage.Task{Meta: storage.TaskMeta{ID: "a-1", Runtime: storage.RuntimeOn}}
	off := &storage.Task{Meta: storage.TaskMeta{ID: "a-2"}}
	bound := storage.Executor{Phases: map[string]storage.PhaseBinding{storage.PhaseRuntime: {Skill: "simulator-verify"}}}

	t.Run("runtime off = skip, whatever the binding", func(t *testing.T) {
		got := runtimeDirective(off, bound)
		mustContain(t, got, "skip (not enabled for this task")
		mustContain(t, got, "do not drive any runtime")
		if strings.Contains(got, "simulator-verify") {
			t.Error("a runtime-off task must not even see the binding")
		}
	})
	t.Run("on + rig configured = gated by the rig section", func(t *testing.T) {
		withRig := bound
		withRig.Rig = "rig-check.sh"
		got := runtimeDirective(on, withRig)
		mustContain(t, got, "run the project skill `simulator-verify`")
		mustContain(t, got, `ONLY when the "## Runtime rig" section below says the rig is UP`)
		mustContain(t, got, "`OBSERVED:` line")
	})
	t.Run("on without a rig hook = the binding's own gate", func(t *testing.T) {
		got := runtimeDirective(on, bound)
		mustContain(t, got, "executor.rig is unset")
		mustContain(t, got, "TODO: runtime verification not run - rig DEAD: <why>")
	})
	t.Run("on + generic binding = generic", func(t *testing.T) {
		got := runtimeDirective(on, storage.Executor{})
		mustContain(t, got, "use the built-in generic for this phase")
	})
	t.Run("on + skip binding = plain skip", func(t *testing.T) {
		got := runtimeDirective(on, storage.Executor{Phases: map[string]storage.PhaseBinding{storage.PhaseRuntime: {Skip: true}}})
		if got != "skip this phase" {
			t.Errorf("got %q", got)
		}
	})
}

func TestBuildWorkerPromptRuntimeLine(t *testing.T) {
	proj := &storage.Project{Name: "App", Path: "/repo"}
	exec := storage.Executor{FixRounds: 1, Rig: "rig-check.sh",
		Phases: map[string]storage.PhaseBinding{storage.PhaseRuntime: {Skill: "simulator-verify"}}}
	off := &storage.Task{Meta: storage.TaskMeta{ID: "a-1", Title: "T"}, Body: "b"}
	on := &storage.Task{Meta: storage.TaskMeta{ID: "a-2", Title: "T", Runtime: storage.RuntimeOn}, Body: "b"}

	gotOff := buildWorkerPrompt(off, nil, proj, "app", exec, "feat/t", "/repo", false)
	mustContain(t, gotOff, "- runtime: skip (not enabled for this task")
	gotOn := buildWorkerPrompt(on, nil, proj, "app", exec, "feat/t", "/repo", false)
	mustContain(t, gotOn, "- runtime: run the project skill `simulator-verify`")
	// The phase order in the prompt is verify, runtime, pr.
	if strings.Index(gotOn, "- verify:") > strings.Index(gotOn, "- runtime:") {
		t.Error("runtime must be listed after verify")
	}
}

func TestSystemPromptRuntimePhase(t *testing.T) {
	exec := storage.Executor{FixRounds: 1}
	for _, tc := range []struct{ standalone, independent bool }{{true, false}, {false, false}, {false, true}} {
		got := buildWorkerSystemPrompt(exec, tc.standalone, tc.independent)
		mustContain(t, got, "6. runtime - AFTER a green verify")
		mustContain(t, got, "never boot, build, install, relaunch or re-point the runtime yourself")
		mustContain(t, got, "a runtime reading can neither upgrade nor demote the verify verdict")
		mustContain(t, got, "- runtime: Use the repo's own runtime-driving skill")
		mustContain(t, got, `prefixed "OBSERVED: "`)
		mustContain(t, got, "positive control:")
		mustContain(t, got, "negative control:")
		mustContain(t, got, "Both controls are REQUIRED on every OBSERVED line")
		mustContain(t, got, "An OBSERVED line is a HYPOTHESIS")
		mustContain(t, got, `never "verified on the simulator"`)
	}
	indep := buildWorkerSystemPrompt(exec, false, true)
	mustContain(t, indep, "A runtime reading you DID take (runtime phase enabled, rig UP) is not a TODO")
}

func TestBuildClaudeArgsAllowExtra(t *testing.T) {
	extra := []string{"Bash(xcrun simctl:*)", "Bash(curl:*)"}
	t.Run("extra patterns are appended to the curated allowlist", func(t *testing.T) {
		args := buildClaudeArgs("p", "sp", "s", "opus", "", 10, false, guardOptions{}, "", false, extra)
		for i, a := range args {
			if a == "--allowedTools" {
				list := args[i+1]
				if !strings.HasPrefix(list, workerAllowedTools) {
					t.Errorf("curated allowlist must stay first: %q", list)
				}
				mustContain(t, list, "Bash(xcrun simctl:*) Bash(curl:*)")
				return
			}
		}
		t.Fatal("no --allowedTools in argv")
	})
	t.Run("nil keeps the envelope byte-identical", func(t *testing.T) {
		a := strings.Join(buildClaudeArgs("p", "sp", "s", "opus", "", 10, false, guardOptions{}, "", false, nil), "\x00")
		b := strings.Join(buildClaudeArgs("p", "sp", "s", "opus", "", 10, false, guardOptions{}, "", false, []string{}), "\x00")
		if a != b || strings.Contains(a, "xcrun") {
			t.Error("no extra tools -> unchanged argv")
		}
	})
	t.Run("yolo carries no allowlist at all", func(t *testing.T) {
		joined := strings.Join(buildClaudeArgs("p", "sp", "s", "opus", "", 10, true, guardOptions{}, "", false, extra), " ")
		if strings.Contains(joined, "--allowedTools") || strings.Contains(joined, "xcrun") {
			t.Error("--yolo must not emit an allowlist")
		}
	})
}

func TestPlanWorkRuntimeTools(t *testing.T) {
	for _, tc := range []struct {
		runtime string
		want    bool
	}{{storage.RuntimeOn, true}, {"", false}, {storage.RuntimeOff, false}} {
		store, task := rigFixture(t, tc.runtime)
		if _, err := store.MutateProject("app", func(p *storage.Project) error {
			p.Executor.RuntimeTools = []string{"Bash(xcrun simctl:*)"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		plan, err := planWork(store, task, "app", workOptions{standalone: true, additional: true})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(plan.cmdArgs, " ")
		if got := strings.Contains(joined, "Bash(xcrun simctl:*)"); got != tc.want {
			t.Errorf("runtime %q: runtime_tools in argv = %v, want %v", tc.runtime, got, tc.want)
		}
		// retarget re-renders the argv; the extra list must survive it.
		plan.retarget(plan.workDir, plan.env)
		if got := strings.Contains(strings.Join(plan.cmdArgs, " "), "Bash(xcrun simctl:*)"); got != tc.want {
			t.Errorf("runtime %q: after retarget runtime_tools in argv = %v, want %v", tc.runtime, got, tc.want)
		}
	}
}

func TestDoctorRuntimePhase(t *testing.T) {
	levels := func(cs []check) (warn, ok int) {
		for _, c := range cs {
			switch c.Level {
			case levelWarn:
				warn++
			case levelOK:
				ok++
			}
		}
		return
	}
	t.Run("nothing set = silence", func(t *testing.T) {
		if cs := checkRuntimePhase(storage.Executor{}); cs != nil {
			t.Errorf("unused phase must produce no finding: %+v", cs)
		}
	})
	t.Run("rig without a binding warns", func(t *testing.T) {
		cs := checkRuntimePhase(storage.Executor{Rig: "rig-check.sh"})
		warn, _ := levels(cs)
		if warn != 1 || !strings.Contains(cs[0].Msg, "runtime phase is generic") {
			t.Errorf("checks = %+v", cs)
		}
	})
	t.Run("binding without rig and tools warns twice", func(t *testing.T) {
		cs := checkRuntimePhase(storage.Executor{Phases: map[string]storage.PhaseBinding{storage.PhaseRuntime: {Skill: "simulator-verify"}}})
		warn, ok := levels(cs)
		if warn != 2 || ok != 1 {
			t.Errorf("checks = %+v", cs)
		}
	})
	t.Run("skip binding with a rig warns", func(t *testing.T) {
		cs := checkRuntimePhase(storage.Executor{Rig: "x", Phases: map[string]storage.PhaseBinding{storage.PhaseRuntime: {Skip: true}}})
		if !strings.Contains(cs[0].Msg, "bound to `false`") {
			t.Errorf("checks = %+v", cs)
		}
	})
	t.Run("all three set = all ok", func(t *testing.T) {
		cs := checkRuntimePhase(storage.Executor{Rig: "rig-check.sh", RuntimeTools: []string{"Bash(curl:*)"},
			Phases: map[string]storage.PhaseBinding{storage.PhaseRuntime: {Cmd: "./smoke.sh"}}})
		warn, ok := levels(cs)
		if warn != 0 || ok != 2 {
			t.Errorf("checks = %+v", cs)
		}
	})
}
