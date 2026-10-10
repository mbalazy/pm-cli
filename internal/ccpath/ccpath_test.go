package ccpath

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
			got := Encode(tt.path)
			if got != tt.want {
				t.Errorf("Encode(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
