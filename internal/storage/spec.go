package storage

import "strings"

// Spec block markers. The body of a task has two zones:
//   - the Spec zone (between these markers): the current-truth document. It is
//     editable in place - rewritten wholesale via ApplySpec on each update.
//   - everything outside the markers: the append-only Log (session history),
//     written via body_append and never rewritten.
const (
	SpecStart = "<!-- spec:start -->"
	SpecEnd   = "<!-- spec:end -->"
)

// ApplySpec replaces the Spec block in body with spec, leaving the Log zone
// untouched. If the markers are absent, a new Spec block is created at the top
// of the body and the existing body becomes the Log below it.
func ApplySpec(body, spec string) string {
	spec = strings.TrimSpace(spec)
	block := SpecStart + "\n" + spec + "\n" + SpecEnd

	start := strings.Index(body, SpecStart)
	end := strings.Index(body, SpecEnd)
	if start != -1 && end != -1 && end > start {
		before := strings.TrimRight(body[:start], " \t\n")
		after := strings.TrimLeft(body[end+len(SpecEnd):], " \t\n")
		var sb strings.Builder
		if before != "" {
			sb.WriteString(before)
			sb.WriteString("\n\n")
		}
		sb.WriteString(block)
		if after != "" {
			sb.WriteString("\n\n")
			sb.WriteString(after)
		}
		return sb.String()
	}

	// No markers yet: prepend a fresh Spec block above the existing body.
	if strings.TrimSpace(body) == "" {
		return block
	}
	return block + "\n\n" + strings.TrimLeft(body, " \t\n")
}

// ExtractSpec returns the content of the Spec block (without markers), or ""
// if there is no well-formed Spec block.
func ExtractSpec(body string) string {
	start := strings.Index(body, SpecStart)
	end := strings.Index(body, SpecEnd)
	if start == -1 || end == -1 || end <= start {
		return ""
	}
	return strings.TrimSpace(body[start+len(SpecStart) : end])
}

// ExtractSection returns the trimmed content of the first markdown section whose
// "## " heading matches heading (case-insensitive), from the line after the
// heading up to the next "## " heading or EOF. Returns "" when not found.
func ExtractSection(md, heading string) string {
	want := strings.ToLower(strings.TrimSpace(heading))
	lines := strings.Split(md, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		if strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## "))) != want {
			continue
		}
		var body []string
		for _, next := range lines[i+1:] {
			if strings.HasPrefix(next, "## ") {
				break
			}
			body = append(body, next)
		}
		return strings.TrimSpace(strings.Join(body, "\n"))
	}
	return ""
}
