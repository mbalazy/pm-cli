package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func TestReviewerAgentsJSON(t *testing.T) {
	var got map[string]struct {
		Description string   `json:"description"`
		Prompt      string   `json:"prompt"`
		Tools       []string `json:"tools"`
		Model       string   `json:"model"`
	}
	if err := json.Unmarshal([]byte(reviewerAgentsJSON("sonnet")), &got); err != nil {
		t.Fatalf("the payload must be the JSON object claude --agents accepts: %v", err)
	}
	def, ok := got[reviewerAgentType]
	if !ok {
		t.Fatalf("no %q key in %v", reviewerAgentType, got)
	}
	if def.Model != "sonnet" {
		t.Errorf("model = %q, want sonnet", def.Model)
	}
	if def.Description == "" || def.Prompt == "" {
		t.Error("description and prompt are both required by the flag's schema")
	}
	for _, tool := range def.Tools {
		// A reviewer spawning its own subagent is refused by the guard; leaving
		// the tool off the definition means it never spends a turn finding out.
		if tool == "Agent" || tool == "Task" {
			t.Errorf("reviewer tools must not include %q", tool)
		}
	}

	// "inherit" drops the model key and NOTHING else: the type still has to
	// exist, because the prompt names it and a named type that does not exist is
	// a failed spawn.
	var inherit map[string]map[string]any
	if err := json.Unmarshal([]byte(reviewerAgentsJSON("")), &inherit); err != nil {
		t.Fatal(err)
	}
	def2, ok := inherit[reviewerAgentType]
	if !ok {
		t.Fatalf("the type must be defined even with no model to pin")
	}
	if _, has := def2["model"]; has {
		t.Error("inherit must leave the model key out entirely")
	}
	if _, has := def2["tools"]; !has {
		t.Error("inherit must keep the tool list")
	}
}

func TestPinReviewerAgent(t *testing.T) {
	cases := []struct {
		name        string
		in          map[string]any
		model       string
		wantChanged bool
		wantType    string
		wantModel   any
	}{
		{
			name:        "no type at all becomes a reviewer",
			in:          map[string]any{"prompt": "review this"},
			model:       "sonnet",
			wantChanged: true, wantType: reviewerAgentType, wantModel: "sonnet",
		},
		{
			// The case this whole layer exists for: orbit-106-3 wrote
			// model: "opus" into all three of its spawns unasked.
			name:        "an explicit opus is overwritten",
			in:          map[string]any{"subagent_type": "Explore", "model": "opus"},
			model:       "sonnet",
			wantChanged: true, wantType: "Explore", wantModel: "sonnet",
		},
		{
			name:        "a generic type with no model gets one",
			in:          map[string]any{"subagent_type": "general-purpose"},
			model:       "sonnet",
			wantChanged: true, wantType: "general-purpose", wantModel: "sonnet",
		},
		{
			name:        "the reviewer type itself is still pinned",
			in:          map[string]any{"subagent_type": reviewerAgentType},
			model:       "sonnet",
			wantChanged: true, wantType: reviewerAgentType, wantModel: "sonnet",
		},
		{
			// A custom type carries its own definition, and its own model with
			// it. Overruling that would be a different change from this one.
			name:        "a custom type is left completely alone",
			in:          map[string]any{"subagent_type": "project-linter", "model": "opus"},
			model:       "sonnet",
			wantChanged: false, wantType: "project-linter", wantModel: "opus",
		},
		{
			name:        "already on the target model is not a change",
			in:          map[string]any{"subagent_type": "Explore", "model": "sonnet"},
			model:       "sonnet",
			wantChanged: false, wantType: "Explore", wantModel: "sonnet",
		},
		{
			// review_model: inherit - pm touches nothing, which is the rollback
			// for this change alone.
			name:        "no review model means no rewrite",
			in:          map[string]any{"subagent_type": "Explore", "model": "opus"},
			model:       "",
			wantChanged: false, wantType: "Explore", wantModel: "opus",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			changed := pinReviewerAgent(c.in, c.model)
			if changed != c.wantChanged {
				t.Errorf("changed = %v, want %v", changed, c.wantChanged)
			}
			if got := c.in["subagent_type"]; got != c.wantType {
				t.Errorf("subagent_type = %v, want %v", got, c.wantType)
			}
			if got := c.in["model"]; got != c.wantModel {
				t.Errorf("model = %v, want %v", got, c.wantModel)
			}
		})
	}

	if pinReviewerAgent(nil, "sonnet") {
		t.Error("a nil input must be a no-op, not a panic")
	}
}

// updatedInput REPLACES the whole tool input, so anything pm does not model has
// to survive the round trip - `description` and `run_in_background` are both
// really sent (measured on claude 2.1.224), and a later build will add more.
func TestWorkerGuardRewriteKeepsFieldsPmDoesNotModel(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"tool_name": "Agent",
		"tool_input": map[string]any{
			"subagent_type":     "Explore",
			"prompt":            "review the diff",
			"model":             "opus",
			"description":       "Review round 1",
			"run_in_background": false,
			"something_new":     42.0,
		},
	})
	var out, errOut bytes.Buffer
	code := runWorkerGuard(bytes.NewReader(payload), &out, &errOut, guardOptions{reviewModel: "sonnet"})
	if code != 0 {
		t.Fatalf("exit = %d (%s), want 0", code, errOut.String())
	}
	var resp struct {
		Hook struct {
			Decision string         `json:"permissionDecision"`
			Updated  map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("stdout is not the PreToolUse response shape: %v (%q)", err, out.String())
	}
	if resp.Hook.Decision != "allow" {
		t.Errorf("decision = %q, want allow", resp.Hook.Decision)
	}
	if resp.Hook.Updated["model"] != "sonnet" {
		t.Errorf("model = %v, want sonnet", resp.Hook.Updated["model"])
	}
	for k, want := range map[string]any{
		"subagent_type": "Explore",
		"prompt":        "review the diff",
		"description":   "Review round 1",
		"something_new": 42.0,
	} {
		if got := resp.Hook.Updated[k]; got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}
	if _, has := resp.Hook.Updated["run_in_background"]; !has {
		t.Error("run_in_background must survive the rewrite")
	}
}

// With nothing to change, the hook must stay silent: a stdout payload on every
// single tool call is output nobody asked for, and an empty rewrite would look
// like pm had an opinion when it did not.
func TestWorkerGuardWritesNothingWhenItChangesNothing(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"tool_name":  "Agent",
		"tool_input": map[string]any{"subagent_type": "Explore", "model": "sonnet"},
	})
	var out bytes.Buffer
	if code := runWorkerGuard(bytes.NewReader(payload), &out, io.Discard, guardOptions{reviewModel: "sonnet"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

// A refused spawn must not also be rewritten: the deny is the whole response,
// and a stdout payload alongside an exit 2 is two answers to one question.
func TestWorkerGuardDeniedSpawnIsNotRewritten(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"tool_name":  "Agent",
		"tool_input": map[string]any{"subagent_type": "Explore"},
		"agent_id":   "nested-1",
		"agent_type": "Explore",
	})
	var out, errOut bytes.Buffer
	if code := runWorkerGuard(bytes.NewReader(payload), &out, &errOut, guardOptions{reviewModel: "sonnet"}); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Errorf("a denied spawn must produce no stdout, got %q", out.String())
	}
}

// The prompt has to name the type pm actually defines, or every spawn fails on
// an unknown subagent_type.
func TestReviewPromptNamesTheReviewerType(t *testing.T) {
	p := genericPhasePrompt(storage.PhaseReview)
	if !strings.Contains(p, reviewerAgentType) {
		t.Errorf("review prompt must name the %q type", reviewerAgentType)
	}
	if !strings.Contains(p, "`model`") {
		t.Error("review prompt must tell the worker not to pass a model")
	}
	if !strings.Contains(reviewerAgentsJSON("sonnet"), reviewerAgentType) {
		t.Error("the type the prompt names must be the one pm defines")
	}
}

func TestBuildClaudeArgsCarriesTheAgentDefinition(t *testing.T) {
	args := buildClaudeArgs("p", "sp", "sess", "opus", 10, false, guardOptions{reviewModel: "sonnet"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--agents") {
		t.Fatalf("argv must carry --agents: %v", args)
	}
	if !strings.Contains(joined, reviewerAgentType) {
		t.Error("argv must carry the reviewer type definition")
	}
	// --model is the WORKER's model and must stay independent of the reviewer's:
	// pinning reviewers to sonnet is not a demotion of the run.
	if !strings.Contains(joined, "--model opus") {
		t.Error("the run's own model must be untouched by the reviewer pin")
	}
	if !strings.Contains(joined, "--review-model sonnet") {
		t.Error("the guard hook must be told what to pin")
	}
}
