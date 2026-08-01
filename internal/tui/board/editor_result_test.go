package board

import (
	"errors"
	"strings"
	"testing"
)

// TestEditorResultMsgSurfacesToast covers pm-cli-74-2: openEditor's
// tea.ExecProcess callback used to ignore the process error and always return
// reloadMsg{} unconditionally - pressing "e" with no $EDITOR/nvim on PATH
// blanked the screen and silently returned with no trace of why.
func TestEditorResultMsgSurfacesToast(t *testing.T) {
	t.Run("editor failure toasts", func(t *testing.T) {
		m := newBoardModel(t)
		result, cmd := m.Update(editorResultMsg{err: errors.New("exec: \"nvim\": executable file not found in $PATH")})
		if cmd != nil {
			t.Error("editorResultMsg must not return a tick cmd - the loop from Init already runs forever")
		}
		m2, ok := result.(Model)
		if !ok {
			t.Fatalf("Update returned %T, want Model", result)
		}
		if !strings.Contains(m2.toastMsg, "editor failed") || !strings.Contains(m2.toastMsg, "not found") {
			t.Errorf("toastMsg = %q, want it to mention the editor failure and the reason", m2.toastMsg)
		}
	})

	t.Run("editor success reloads without an error toast", func(t *testing.T) {
		m := newBoardModel(t)
		m.toastMsg = "untouched"
		result, _ := m.Update(editorResultMsg{})
		m2, ok := result.(Model)
		if !ok {
			t.Fatalf("Update returned %T, want Model", result)
		}
		if m2.toastMsg != "untouched" {
			t.Errorf("toastMsg = %q, want unchanged on a clean editor exit", m2.toastMsg)
		}
	})
}
