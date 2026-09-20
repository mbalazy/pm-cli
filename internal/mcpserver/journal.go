package mcpserver

import (
	"context"

	"github.com/mbalazy/pm-cli/internal/service"
	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The journal's MCP surface; the semantics live in service.JournalAdd /
// service.JournalList. The write tool matters more than the read one: the
// agent that just burned twenty minutes on a flaky subsystem is the only
// party who still has the symptom, the wrong conclusion and the real cause
// in hand, and a journal nobody writes to at the moment of the loss is a
// journal that stays empty.

type (
	journalAddInput      = service.JournalAddInput
	journalListInput     = service.JournalListInput
	journalEntriesOutput = service.JournalEntriesOutput
)

const (
	defaultJournalLimit = service.DefaultJournalLimit
	maxJournalLimit     = service.MaxJournalLimit
)

func registerJournalTools(s *mcp.Server, store storage.TaskStore) {
	// pm_journal_add
	mcp.AddTool(s, &mcp.Tool{
		Name: "pm_journal_add",
		Description: "Record one incident in a project's subsystem journal - a per-project running record of how a chosen, repeatedly-troublesome subsystem (a simulator rig, a flaky sandbox, a deploy pipeline) actually behaves. " +
			"Write an entry WHEN IT HAPPENS, while the symptom and the wrong conclusion are still in hand: that is the only moment the information exists. " +
			"An entry is an EVENT (dated, append-only, never rewritten), which is what separates it from a memory file or a doc holding a RULE. Put the rule where rules live; put the event here, and let the entry name the rule it produced via 'fix'. " +
			"Leave 'fix' empty while the fix has not been made - the open set is the backlog. To close entries that were already open, list their ids in 'resolves'. Journals must be declared in project.yaml; pm_journal_list shows which exist.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in journalAddInput) (*mcp.CallToolResult, any, error) {
		return respond(service.JournalAdd(store, in))
	})

	// pm_journal_list
	mcp.AddTool(s, &mcp.Tool{
		Name: "pm_journal_list",
		Description: "Read a project's subsystem journals. Without 'name': the journals declared for the project with entry and open counts. With 'name': that journal's entries newest first, plus the rollup that says whether it recurs (per-tag and per-month counts, total cost, open count). " +
			"Read the relevant journal BEFORE touching the subsystem it covers - it holds the failures that already cost time, including the ones that produced confident wrong answers.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in journalListInput) (*mcp.CallToolResult, any, error) {
		return respond(service.JournalList(store, in))
	})
}
