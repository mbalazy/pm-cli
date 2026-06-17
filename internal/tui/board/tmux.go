package board

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func tmuxWindowName(prefix, taskID, sessionID string) string {
	short := sessionID
	if len(short) > 4 {
		short = short[:4]
	}
	return fmt.Sprintf("%s:%s:%s", prefix, short, taskID)
}

func tmuxGetWindowName() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#W").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func tmuxRenameWindow(name string) {
	if name != "" {
		exec.Command("tmux", "rename-window", name).Run()
	}
}

// tmuxSessionForProc walks the process tree up from the current pid and
// returns the session name of the tmux pane whose pane_pid matches an
// ancestor. This is more reliable than $TMUX, which becomes stale when a
// window is moved between sessions after the shell started. Returns "" if
// the process is not inside any detectable tmux pane.
func tmuxSessionForProc() string {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid} #{session_name}").Output()
	if err != nil {
		return ""
	}
	panes := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), " ", 2)
		if len(parts) != 2 {
			continue
		}
		if pid, err := strconv.Atoi(parts[0]); err == nil {
			panes[pid] = parts[1]
		}
	}
	pid := os.Getpid()
	for i := 0; i < 32 && pid > 1; i++ {
		if sess, ok := panes[pid]; ok {
			return sess
		}
		ppid, err := tmuxParentPID(pid)
		if err != nil || ppid == 0 || ppid == pid {
			return ""
		}
		pid = ppid
	}
	return ""
}

func tmuxParentPID(pid int) (int, error) {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// tmuxNewWindow creates a new tmux window running shellCmd via sh -c.
// Targets the detected session explicitly so the window appears where
// the user can see it (not where $TMUX env happens to point). Returns
// the target session name and any error from tmux (with stderr content
// surfaced in the error message).
func tmuxNewWindow(name, shellCmd string) (string, error) {
	sess := tmuxSessionForProc()
	args := []string{"new-window", "-n", name}
	if sess != "" {
		args = append(args, "-t", sess+":")
	}
	args = append(args, "sh", "-c", shellCmd)
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return sess, fmt.Errorf("%s", msg)
	}
	return sess, nil
}

func tmuxLaunchToast(label, winName, sess string) string {
	if sess != "" {
		return fmt.Sprintf("%s [%s]: %s", label, sess, winName)
	}
	return label + ": " + winName
}
