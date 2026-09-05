package storage

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidateRuntime(t *testing.T) {
	// "" is a second spelling of "off" and MUST stay legal: every task written
	// before the field existed carries it.
	for _, ok := range []string{"", RuntimeOn, RuntimeOff} {
		if err := ValidateRuntime(ok); err != nil {
			t.Errorf("ValidateRuntime(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"On", "ON", "true", "yes", "sim", " on", "on "} {
		if err := ValidateRuntime(bad); err == nil {
			t.Errorf("ValidateRuntime(%q) = nil, want error (case-sensitive lowercase only)", bad)
		}
	}
	if (TaskMeta{Runtime: RuntimeOn}).RuntimeEnabled() != true {
		t.Error("runtime: on must report enabled")
	}
	for _, off := range []string{"", RuntimeOff} {
		if (TaskMeta{Runtime: off}).RuntimeEnabled() {
			t.Errorf("runtime %q must report disabled", off)
		}
	}
}

// The gate sits in writeTask so EVERY writer is covered, and a rejected write
// leaves no file behind (the pm-cli-41 phantom-file rule).
func TestWriteTaskRejectsInvalidRuntime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t-1-sub.md")
	newTask := func(runtime string) *Task {
		return &Task{
			Meta:     TaskMeta{ID: "t-1", Title: "Sub", Status: StatusTodo, Runtime: runtime},
			FilePath: path,
			Project:  "test",
		}
	}
	if err := WriteTask(newTask("true")); err == nil {
		t.Fatal("expected write to reject invalid runtime")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("rejected write must not create the file")
	}
	for _, ok := range []string{"", RuntimeOff, RuntimeOn} {
		if err := WriteTask(newTask(ok)); err != nil {
			t.Errorf("WriteTask with runtime %q: %v", ok, err)
		}
	}
	reloaded, err := ReadTask(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Meta.Runtime != RuntimeOn {
		t.Errorf("runtime = %q, want %q", reloaded.Meta.Runtime, RuntimeOn)
	}
	if err := WriteTask(newTask("sim")); err == nil {
		t.Fatal("expected write to reject invalid runtime")
	}
	again, err := ReadTask(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Meta.Runtime != RuntimeOn {
		t.Errorf("a rejected write clobbered the stored task: runtime = %q", again.Meta.Runtime)
	}
}

func TestExecutorRigAndRuntimeTools(t *testing.T) {
	data := "name: X\nexecutor:\n  enabled: true\n  rig: rig-check.sh --udid $SIM_UDID\n  runtime_tools:\n    - \"Bash(xcrun simctl:*)\"\n    - \"Bash(curl:*)\"\n  phases:\n    runtime:\n      skill: simulator-verify\n"
	var p Project
	if err := yaml.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	e := p.GetExecutor()
	if e.Rig != "rig-check.sh --udid $SIM_UDID" {
		t.Errorf("Rig = %q", e.Rig)
	}
	if len(e.RuntimeTools) != 2 || e.RuntimeTools[0] != "Bash(xcrun simctl:*)" {
		t.Errorf("RuntimeTools = %v", e.RuntimeTools)
	}
	if b := e.Phase(PhaseRuntime); b.Kind() != BindSkill || b.Skill != "simulator-verify" {
		t.Errorf("runtime phase binding = %+v (%s)", b, b.Kind())
	}

	var bare Project
	if err := yaml.Unmarshal([]byte("name: X\nexecutor:\n  enabled: true\n"), &bare); err != nil {
		t.Fatal(err)
	}
	if e := bare.GetExecutor(); e.Rig != "" || len(e.RuntimeTools) != 0 || e.Phase(PhaseRuntime).Kind() != BindGeneric {
		t.Errorf("rig/runtime_tools must default to unset, runtime phase to generic: %+v", e)
	}
}

// runtime sits after verify (the gate) and before pr, so a runtime reading can
// never be what decides verify-green and a PR carries the reading.
func TestExecutorPhasesOrderRuntime(t *testing.T) {
	idx := map[string]int{}
	for i, p := range ExecutorPhases {
		idx[p] = i
	}
	if idx[PhaseRuntime] != idx[PhaseVerify]+1 || idx[PhasePR] != idx[PhaseRuntime]+1 {
		t.Errorf("phase order = %v, want ... verify, runtime, pr", ExecutorPhases)
	}
}
