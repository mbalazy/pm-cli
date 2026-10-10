package cmd

import "testing"

func TestDetectSessionPrefersEnv(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "env-authoritative-uuid")
	got, err := detectSession()
	if err != nil {
		t.Fatalf("detectSession returned error: %v", err)
	}
	if got != "env-authoritative-uuid" {
		t.Errorf("detectSession() = %q, want env-authoritative-uuid", got)
	}
}

func TestParseResumeArg(t *testing.T) {
	tests := []struct {
		cmdline string
		want    string
	}{
		{"claude --resume abc-123", "abc-123"},
		{"claude -r abc-123", "abc-123"},
		{"claude --continue abc-123", "abc-123"},
		{"claude -c abc-123", "abc-123"},
		{"claude", ""},
		{"claude --help", ""},
		{"/bin/zsh -c source foo.sh", "source"}, // guarded by isClaudeProcess in real usage
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			got := parseResumeArg(tt.cmdline)
			if got != tt.want {
				t.Errorf("parseResumeArg(%q) = %q, want %q", tt.cmdline, got, tt.want)
			}
		})
	}
}

func TestIsClaudeProcess(t *testing.T) {
	tests := []struct {
		cmdline string
		want    bool
	}{
		{"claude", true},
		{"claude --resume abc", true},
		{"/usr/local/bin/claude", true},
		{"/bin/zsh -c source foo.sh", false},
		{"node /path/to/something", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			got := isClaudeProcess(tt.cmdline)
			if got != tt.want {
				t.Errorf("isClaudeProcess(%q) = %v, want %v", tt.cmdline, got, tt.want)
			}
		})
	}
}
