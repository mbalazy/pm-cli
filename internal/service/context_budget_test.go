package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// The cold-start budget (pm-cli-149).
//
// pm_context is the call every session opens with, so its size is paid on
// every session. Claude Code refuses a tool result above its token cap
// (25k tokens by default): the result lands in a file instead of the
// model's context, and the session starts blind. Measured on a real store
// on 2026-09-21 (13 active projects, the two largest with 7 doing tasks and
// 11 / 23 trackers), the cross-project call produced 96 767 chars and was
// refused, while a 49 630-char project-scoped call went through - so the
// cap sits between the two, at roughly 3 chars per token for JSON carrying
// prose. The budget below is 60 000 chars: about 20k tokens at that rate,
// a fifth under the cap.
//
// METHOD: the fixture below is sized like that real store (the SHAPE, not
// the content - names are fixtures), and each result is measured as
// len(json.Marshal(result)), which is byte-for-byte what the MCP server
// puts in the tool result (jsonText in internal/mcpserver). The number is
// a ratchet: a change that makes cold start grow past it fails here, and
// the fix is to cut the payload, not to raise the constant - raising it
// needs a new measurement on a real store showing the cap moved.
const contextCharBudget = 60_000

// Fixture shape, modeled on the 2026-09-21 measurement (re-measured
// 2026-09-22 after the cut: 55.5k / 52.9k chars project-scoped on the two
// large projects, 37.3k cross-project). Every count is at or above what
// the real store had, so the fixture is a heavy case of the same shape.
const (
	fixtureProjects       = 13
	fixtureBigProjects    = 2 // the two large ones carry the counts below
	fixtureBigDoing       = 8 // doing tasks, standalone (not under a tracker)
	fixtureBigOpenEpics   = 12
	fixtureBigClosedEpics = 12
	fixtureEpicChildren   = 5 // per open tracker: 3 open + 2 the human closed
	fixtureSmallDoing     = 1
	fixtureSmallOpenEpics = 2
	fixtureSmallChildren  = 3
	fixtureBodyRunes      = 6_000 // well over ContextBodyLimit: the cap must bite
	fixtureBriefRunes     = 1_500 // a full brief in the project-scoped view
)

// seedBudgetFixture writes fixtureProjects projects into store.Root.
// Returns the slug of one big project.
func seedBudgetFixture(t *testing.T, store *storage.Store) string {
	t.Helper()
	brief := strings.Repeat("Goal: land the thing; Decisions: kept X over Y; Status: waiting on the second review. ", 1+fixtureBriefRunes/86)
	brief = string([]rune(brief)[:fixtureBriefRunes])
	body := "<!-- spec:start -->\n## Description\n" + strings.Repeat("A paragraph of specification prose that a cold session would read. ", 1+fixtureBodyRunes/68)
	body = string([]rune(body)[:fixtureBodyRunes])
	childBrief := strings.Repeat("child status line ", 8) // ~140 runes, BriefLine cuts at 120
	links := map[string]string{
		"pr":     "https://github.com/acme/acme-api/pull/1234",
		"ticket": "https://jira.example.com/browse/ACME-253",
		"design": "https://example.com/design/acme-onboarding-v3",
	}

	big := ""
	for p := 1; p <= fixtureProjects; p++ {
		slug := fmt.Sprintf("acme-%02d", p)
		isBig := p <= fixtureBigProjects
		if isBig && big == "" {
			big = slug
		}
		dir := filepath.Join(store.Root, slug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		proj := &storage.Project{
			Name:     "Acme " + slug,
			Prefix:   fmt.Sprintf("a%02d", p),
			Repo:     "https://github.com/acme/" + slug,
			Stack:    "Go, React, Postgres",
			Notes:    "Fixture project: a notes paragraph of the size a real project.yaml carries, naming the deploy path and the review rule.",
			Links:    map[string]string{"board": "https://jira.example.com/board/" + slug, "docs": "https://example.com/" + slug},
			Statuses: []string{"todo", "doing", "waiting", "pushed", "merged", "done"},
		}
		if err := storage.WriteProject(filepath.Join(dir, "project.yaml"), proj); err != nil {
			t.Fatal(err)
		}
		n := 0
		mk := func(title, parent string, st storage.TaskStatus, brief, body string, withLinks bool) string {
			n++
			id := fmt.Sprintf("%s-%d", proj.Prefix, n)
			if parent != "" {
				id = fmt.Sprintf("%s-%d", parent, n)
			}
			task := &storage.Task{
				Meta: storage.TaskMeta{
					ID: id, Title: title, Status: st, Parent: parent,
					Created: "2026-08-01", Updated: "2026-09-20T10:00:00+02:00", StatusChanged: "2026-09-01T10:00:00+02:00",
					Brief: brief, Branch: "feat/" + slug + "-" + storage.Slugify(title),
					Tags: []string{"fixture", "backend"},
				},
				Body:     body,
				FilePath: filepath.Join(dir, id+".md"),
				Project:  slug,
			}
			if withLinks {
				task.Meta.Links = links
			}
			if err := storage.WriteTask(task); err != nil {
				t.Fatal(err)
			}
			return id
		}
		doing, open, closed, kids := fixtureSmallDoing, fixtureSmallOpenEpics, 0, fixtureSmallChildren
		if isBig {
			doing, open, closed, kids = fixtureBigDoing, fixtureBigOpenEpics, fixtureBigClosedEpics, fixtureEpicChildren
		}
		for i := 0; i < doing; i++ {
			mk(fmt.Sprintf("ACME-%d: a doing task title of realistic length for the row", 200+i), "", storage.StatusDoing, brief, body, true)
		}
		for i := 0; i < open; i++ {
			parent := mk(fmt.Sprintf("Batch: ACME-%d, ACME-%d, ACME-%d (2026-09-0%d)", 300+i, 310+i, 320+i, 1+i%9), "", storage.StatusDoing, brief, body, false)
			for k := 0; k < kids; k++ {
				st := []storage.TaskStatus{storage.StatusTodo, "pushed", storage.StatusDone, storage.StatusWaiting, storage.StatusArchived}[k%5]
				mk(fmt.Sprintf("ACME-%d: sub %d of the batch, a title of realistic length", 300+i+k, k+1), parent, st, childBrief, "", false)
			}
		}
		for i := 0; i < closed; i++ {
			parent := mk(fmt.Sprintf("Closed epic %d", i), "", storage.StatusDone, brief, body, false)
			for k := 0; k < kids; k++ {
				mk(fmt.Sprintf("closed sub %d", k), parent, storage.StatusDone, childBrief, "", false)
			}
		}
		// A few waiting tasks so the attention block has rows to carry.
		for i := 0; i < 3; i++ {
			id := mk(fmt.Sprintf("Waiting task %d", i), "", storage.StatusWaiting, "waiting brief", "", false)
			task, err := store.FindTaskExact(slug, id)
			if err != nil {
				t.Fatal(err)
			}
			task.Meta.WaitingFor = "review by reviewer-two on the PR"
			if err := storage.WriteTask(task); err != nil {
				t.Fatal(err)
			}
		}
		if isBig {
			state := "where we stand: the batch is landing\n" + strings.Repeat("- a state line with its provenance [assumed]\n", 15)
			if err := storage.AppendTimelineEntry(dir, &storage.TimelineEntry{Kind: "state", Text: state}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return big
}

func measureJSON(t *testing.T, v any) int {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return len(data)
}

func TestContextColdStartBudget(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	big := seedBudgetFixture(t, store)

	t.Run("cross-project", func(t *testing.T) {
		out, err := CrossProjectContext(store, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Projects) != fixtureProjects {
			t.Fatalf("projects = %d, want %d", len(out.Projects), fixtureProjects)
		}
		n := measureJSON(t, out)
		t.Logf("cross-project pm_context: %d chars (budget %d)", n, contextCharBudget)
		if n > contextCharBudget {
			t.Errorf("cross-project pm_context = %d chars, over the %d budget - cut the payload, do not raise the constant", n, contextCharBudget)
		}
		// The shape the budget relies on: headers only, open trackers only.
		for _, ps := range out.Projects {
			for _, tr := range ps.Trackers {
				if len(tr.Children) != 0 || !tr.ChildrenOmitted {
					t.Errorf("%s/%s: cross-project trackers carry no children", ps.Slug, tr.ID)
				}
				if tr.Status == string(storage.StatusDone) {
					t.Errorf("%s/%s: a finished tracker is left out of the cross-project rollup", ps.Slug, tr.ID)
				}
			}
		}
		if out.TrackersNote == "" {
			t.Errorf("trackers_note must say where the child rollup went")
		}
	})

	t.Run("project-scoped", func(t *testing.T) {
		out, err := ProjectContext(store, big)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.DoingTasks) != fixtureBigDoing {
			t.Fatalf("doing tasks = %d, want %d", len(out.DoingTasks), fixtureBigDoing)
		}
		n := measureJSON(t, out)
		t.Logf("project-scoped pm_context: %d chars (budget %d)", n, contextCharBudget)
		if n > contextCharBudget {
			t.Errorf("project-scoped pm_context = %d chars, over the %d budget - cut the payload, do not raise the constant", n, contextCharBudget)
		}
		// The two designed properties the budget assumes.
		for _, d := range out.DoingTasks {
			if len([]rune(d.Brief)) != fixtureBriefRunes {
				t.Errorf("%s: the project-scoped brief stays FULL, got %d runes", d.ID, len([]rune(d.Brief)))
			}
			if len([]rune(d.Body)) > ContextBodyLimit+100 {
				t.Errorf("%s: body = %d runes, the ContextBodyLimit cap must bite", d.ID, len([]rune(d.Body)))
			}
		}
		if len(out.Trackers) != fixtureBigOpenEpics+fixtureBigClosedEpics {
			t.Errorf("trackers = %d, want every tracker (finished ones collapsed)", len(out.Trackers))
		}
	})
}
