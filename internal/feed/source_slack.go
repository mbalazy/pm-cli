package feed

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// SlackSource reads Slack through the user's Slack MCP server(s) - pm
// serve as an MCP client, one subprocess per workspace per refresh
// (start -> a few tool calls -> stop). Per workspace: the channels the
// projects map (project.yaml `slack: {workspace, channels}`) since the
// cutoff, each message an event of that project; and, when the server's
// `me` is set, mentions of the user and DMs/threads with the user - ALWAYS,
// project-less (the feed's "no project" rows). A workspace whose server
// fails is a per-workspace error on the source's status; the others still
// run. It carries titles and first lines, never whole threads: this is a
// feed, not a reader.
type SlackSource struct {
	// Dial connects to a server; nil = DialStdio.
	Dial Dialer
	cfg  storage.SlackSourceConfig
}

func (s *SlackSource) Name() string { return "slack" }

// Configure takes the server list from cockpit.slack (feed.Configurable).
func (s *SlackSource) Configure(cfg *storage.CockpitConfig) {
	if cfg != nil {
		s.cfg = cfg.Slack
	}
}

// SlackTimeout bounds one workspace: the server start, the handshake and
// every call. A hung `npx` must not hold the refresh.
var SlackTimeout = 90 * time.Second

// slackMaxPerChannel caps the messages read per channel per refresh.
const slackMaxPerChannel = 100

// channelMapping is one project's claim on one channel of one workspace.
type channelMapping struct {
	project, group, channel string
}

func (s *SlackSource) Fetch(ctx context.Context, from, to time.Time, projects []Project) ([]Event, error) {
	if len(s.cfg.Servers) == 0 {
		return nil, fmt.Errorf("no Slack servers configured (cockpit.slack.servers in config.yaml)")
	}
	// workspace -> channel -> mapping. A channel is read once even when two
	// projects map it (the first wins; the event names that project).
	byWorkspace := map[string]map[string]channelMapping{}
	for _, p := range projects {
		if p.Slack == nil {
			continue
		}
		ws := p.Slack.Workspace
		if ws == "" && len(s.cfg.Servers) > 0 {
			ws = s.cfg.Servers[0].Workspace
		}
		for _, ch := range p.Slack.Channels {
			name := normalizeChannel(ch)
			if name == "" {
				continue
			}
			if byWorkspace[ws] == nil {
				byWorkspace[ws] = map[string]channelMapping{}
			}
			if _, taken := byWorkspace[ws][name]; !taken {
				byWorkspace[ws][name] = channelMapping{project: p.Slug, group: p.Group, channel: name}
			}
		}
	}
	dial := s.Dial
	if dial == nil {
		dial = DialStdio
	}
	var out []Event
	var errs []error
	for _, sv := range s.cfg.Servers {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		events, err := s.fetchWorkspace(ctx, dial, sv, from, to, byWorkspace[sv.Workspace])
		out = append(out, events...)
		if err != nil {
			errs = append(errs, &ProjectError{Project: "workspace " + sv.Workspace, Err: err})
		}
	}
	for ws := range byWorkspace {
		if s.cfg.Server(ws) == nil {
			errs = append(errs, &ProjectError{Project: "workspace " + ws, Err: fmt.Errorf("a project maps channels here but cockpit.slack.servers has no such workspace")})
		}
	}
	return out, errors.Join(errs...)
}

// fetchWorkspace runs one server and reads the mapped channels, the
// mentions and the DMs. One error covers the workspace; a channel that
// fails is reported and the next one still read.
func (s *SlackSource) fetchWorkspace(ctx context.Context, dial Dialer, sv storage.SlackServer, from, to time.Time, channels map[string]channelMapping) ([]Event, error) {
	spec, err := ResolveServer(sv, s.cfg.ClaudeConfig)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, SlackTimeout)
	defer cancel()
	session, err := dial(ctx, spec)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	var out []Event
	var errs []error
	seen := map[string]bool{}
	add := func(e Event) {
		if seen[e.ID] {
			return
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	limit := historyLimit(from, to)
	for name, m := range channels {
		text, err := session.CallText(ctx, "conversations_history", map[string]any{"channel_id": "#" + name, "limit": limit})
		if err != nil {
			errs = append(errs, fmt.Errorf("#%s: %w", name, err))
			continue
		}
		msgs, err := parseMessages(text)
		if err != nil {
			errs = append(errs, fmt.Errorf("#%s: %w", name, err))
			continue
		}
		n := 0
		for _, msg := range msgs {
			ts, ok := inWindow(msg.ts, from, to)
			if !ok {
				continue
			}
			// The user's own messages are not news to the user.
			if sameUser(msg.user, sv.Me) || sameUser(msg.userID, sv.Me) {
				continue
			}
			if n++; n > slackMaxPerChannel {
				break
			}
			e := slackEvent(sv.Workspace, name, msg, ts, SeverityInfo)
			e.Project, e.Group = m.project, m.group
			add(e)
		}
	}
	if sv.Me == "" {
		errs = append(errs, fmt.Errorf("no `me` configured - mentions and DMs skipped"))
		return out, errors.Join(errs...)
	}
	after := from.In(time.Local).AddDate(0, 0, -1).Format("2006-01-02")
	for _, q := range []struct {
		what string
		args map[string]any
	}{
		{"mention", map[string]any{"search_query": mentionQuery(sv.Me), "filter_date_after": after, "limit": 50}},
		{"dm", map[string]any{"filter_users_with": sv.Me, "filter_date_after": after, "limit": 50}},
	} {
		text, err := session.CallText(ctx, "conversations_search_messages", q.args)
		if err != nil {
			errs = append(errs, fmt.Errorf("%ss: %w", q.what, err))
			continue
		}
		msgs, err := parseMessages(text)
		if err != nil {
			errs = append(errs, fmt.Errorf("%ss: %w", q.what, err))
			continue
		}
		for _, msg := range msgs {
			ts, ok := inWindow(msg.ts, from, to)
			if !ok {
				continue
			}
			// The user's own messages are not news to the user.
			if sameUser(msg.user, sv.Me) || sameUser(msg.userID, sv.Me) {
				continue
			}
			e := slackEvent(sv.Workspace, msg.channel, msg, ts, SeverityWarn)
			if m, mapped := channels[normalizeChannel(msg.channel)]; mapped {
				e.Project, e.Group = m.project, m.group
			}
			e.Detail = q.what + " · " + e.Detail
			add(e)
		}
	}
	return out, errors.Join(errs...)
}

// historyLimit is the `limit` conversations_history takes: a day count
// covering the window ("1d", "2d", ...).
func historyLimit(from, to time.Time) string {
	days := int(math.Ceil(to.Sub(from).Hours() / 24))
	if days < 1 {
		days = 1
	}
	return fmt.Sprintf("%dd", days)
}

// mentionQuery is the search for a mention of the user: an id searches as
// the Slack markup, a handle as itself.
func mentionQuery(me string) string {
	if strings.HasPrefix(me, "U") && !strings.ContainsAny(me, "@ ") {
		return "<@" + me + ">"
	}
	return "@" + strings.TrimPrefix(me, "@")
}

func sameUser(a, b string) bool {
	a, b = strings.ToLower(strings.TrimPrefix(a, "@")), strings.ToLower(strings.TrimPrefix(b, "@"))
	return a != "" && a == b
}

func normalizeChannel(ch string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ch), "#")))
}

// message is one row of a server's CSV.
type message struct {
	userID, user, channel, thread, text, ts, link string
}

// parseMessages reads the CSV a tool returned, finding the columns by
// NAME: the server's README does not pin them, and a header-driven read
// survives a rename that a positional one would misfile as text. A
// column is matched case-insensitively against a few spellings each.
func parseMessages(text string) ([]message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("csv header: %w", err)
	}
	col := func(names ...string) int {
		for i, h := range header {
			h = strings.ToLower(strings.TrimSpace(h))
			for _, n := range names {
				if h == n {
					return i
				}
			}
		}
		return -1
	}
	iUserID := col("userid", "user_id", "user")
	iUser := col("username", "user_name", "realname", "real_name", "name")
	iChannel := col("channel", "channelid", "channel_id", "channelname", "channel_name")
	iThread := col("threadts", "thread_ts", "thread")
	iText := col("text", "message", "body")
	iTS := col("time", "ts", "timestamp", "date")
	iLink := col("permalink", "url", "link")
	if iText < 0 || iTS < 0 {
		return nil, fmt.Errorf("csv has no text/time columns (header: %s)", strings.Join(header, ","))
	}
	get := func(rec []string, i int) string {
		if i < 0 || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	var out []message
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, fmt.Errorf("csv row: %w", err)
		}
		// The server appends a cursor row (empty but for the last column)
		// for pagination; a row with no text and no time is that.
		m := message{userID: get(rec, iUserID), user: get(rec, iUser), channel: get(rec, iChannel), thread: get(rec, iThread), text: get(rec, iText), ts: normalizeTS(get(rec, iTS)), link: get(rec, iLink)}
		if m.text == "" && m.ts == "" {
			continue
		}
		if m.user == "" {
			m.user = m.userID
		}
		out = append(out, m)
	}
	return out, nil
}

// normalizeTS accepts a Slack ts ("1712345678.123456"), an epoch, or any
// stamp storage.ParseStamp reads, and returns RFC3339 (empty when none).
func normalizeTS(s string) string {
	if s == "" {
		return ""
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 1e9 {
		sec, frac := math.Modf(f)
		return time.Unix(int64(sec), int64(frac*1e9)).Format(time.RFC3339)
	}
	if t, ok := storage.ParseStamp(s); ok {
		return t.Format(time.RFC3339)
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05 -0700 MST", time.RFC1123, time.RFC1123Z} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format(time.RFC3339)
		}
	}
	return ""
}

func slackEvent(workspace, channel string, m message, ts time.Time, severity string) Event {
	title := firstTextLine(m.text)
	who := m.user
	if who == "" {
		who = "someone"
	}
	raw := strings.TrimSpace(channel)
	if raw == "" {
		raw = strings.TrimSpace(m.channel)
	}
	// A DM shows as its "@name_dm" handle, a channel as "#name".
	label := "#" + normalizeChannel(raw)
	if strings.HasPrefix(raw, "@") {
		label = strings.ToLower(raw)
	}
	e := Event{
		ID:       EventID("slack", workspace, label, m.ts, m.userID+m.user, m.text),
		TS:       ts.Format(time.RFC3339),
		Source:   "slack",
		Title:    label + " · " + strings.TrimPrefix(who, "@") + ": " + title,
		Detail:   "workspace " + workspace,
		URL:      m.link,
		Severity: severity,
	}
	if m.thread != "" && m.thread != m.ts {
		e.Detail += " · in a thread"
	}
	return e
}

// firstTextLine is the message's first non-empty line, trimmed to a row.
func firstTextLine(text string) string {
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if r := []rune(l); len(r) > 140 {
				return string(r[:140]) + "…"
			}
			return l
		}
	}
	return "(no text)"
}
