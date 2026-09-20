package cmd

import "testing"

func TestEncodeProjectPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/Users/alice/.claude/pm-cli", "-Users-alice--claude-pm-cli"},
		{"/Users/alice/.config/nvim", "-Users-alice--config-nvim"},
		{"/Users/alice/repos/atlas", "-Users-alice-repos-atlas"},
		{"/Users/alice/repos/atlas/atlas-app", "-Users-alice-repos-atlas-atlas-app"},
		// Dot in a path segment must map to "-" too (CC's real encoding).
		{"/Users/alice/repos/orbit/app.orbit", "-Users-alice-repos-orbit-app-orbit"},
		{"/Users/alice/.claude/pm-cli/sub.dir", "-Users-alice--claude-pm-cli-sub-dir"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := encodeProjectPath(tt.path)
			if got != tt.want {
				t.Errorf("encodeProjectPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

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
