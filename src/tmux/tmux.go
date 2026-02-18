package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

const (
	// RealmEnvVar is the environment variable for test isolation
	RealmEnvVar = "CLAUDE_TERM_REALM"
)

// ServerName returns the tmux server name, realm-aware for test isolation
func ServerName() string {
	if realm := os.Getenv(RealmEnvVar); realm != "" {
		return "prompt-grid-" + realm
	}
	return "prompt-grid"
}

// GetSocketDir returns the directory for IPC sockets (realm-aware)
func GetSocketDir() string {
	if realm := os.Getenv(RealmEnvVar); realm != "" {
		return "/tmp/prompt-grid-" + realm
	}
	return "/tmp/prompt-grid-sessions"
}

// EnsureInstalled checks that tmux is available, installs via brew if missing
func EnsureInstalled() error {
	if _, err := exec.LookPath("tmux"); err != nil {
		cmd := exec.Command("brew", "install", "tmux")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("tmux not found and brew install failed: %w", err)
		}
	}
	return nil
}

// serverConfigOnce ensures global tmux server options are set exactly once.
var serverConfigOnce sync.Once

// ConfigureServer sets global options on the tmux server (status off, prefix disabled).
// Safe to call multiple times — uses sync.Once internally.
func ConfigureServer() {
	serverConfigOnce.Do(func() {
		cmd := exec.Command("tmux",
			"-L", ServerName(),
			"set-option", "-g", "status", "off", ";",
			"set-option", "-g", "prefix", "None",
		)
		cmd.Run() // best-effort
	})
}

// NewSession creates a new tmux session with the given name and size.
// workDir sets the initial working directory (empty = tmux default).
// cmd is an optional initial command (e.g., ["ssh", "host"]).
// The session is created detached and configured to be invisible (no status bar, no prefix key).
func NewSession(name, workDir string, cols, rows uint16, cmd ...string) error {
	args := []string{
		"-L", ServerName(),
		"new-session", "-d",
		"-s", name,
		"-x", fmt.Sprintf("%d", cols),
		"-y", fmt.Sprintf("%d", rows),
	}

	if workDir != "" {
		args = append(args, "-c", workDir)
	}

	if len(cmd) > 0 {
		args = append(args, cmd...)
	}

	tmuxCmd := exec.Command("tmux", args...)
	tmuxCmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)
	if out, err := tmuxCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session failed: %w: %s", err, out)
	}

	// Set global server options once (status off, prefix disabled).
	ConfigureServer()

	return nil
}

// SendKeys sends keystrokes to a tmux session
func SendKeys(name string, keys ...string) error {
	args := []string{"-L", ServerName(), "send-keys", "-t", name}
	args = append(args, keys...)
	cmd := exec.Command("tmux", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys failed: %w: %s", err, out)
	}
	return nil
}

// AttachArgs returns the command and arguments for attaching to a tmux session.
// Used with pty.StartCommand().
func AttachArgs(name string) (string, []string) {
	return "tmux", []string{"-L", ServerName(), "attach-session", "-t", name}
}

// ListSessions returns names of all sessions on our tmux server
func ListSessions() ([]string, error) {
	cmd := exec.Command("tmux", "-L", ServerName(), "list-sessions", "-F", "#{session_name}")
	out, err := cmd.Output()
	if err != nil {
		// No server running = no sessions
		return nil, nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var sessions []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions, nil
}

// HasSession returns true if the named session exists
func HasSession(name string) bool {
	cmd := exec.Command("tmux", "-L", ServerName(), "has-session", "-t", name)
	return cmd.Run() == nil
}

// KillSession kills a specific tmux session
func KillSession(name string) error {
	cmd := exec.Command("tmux", "-L", ServerName(), "kill-session", "-t", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session failed: %w: %s", err, out)
	}
	return nil
}

// RenameSession renames a tmux session
func RenameSession(old, new string) error {
	cmd := exec.Command("tmux", "-L", ServerName(), "rename-session", "-t", old, new)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux rename-session failed: %w: %s", err, out)
	}
	return nil
}

// GetPaneCurrentPath returns the current working directory of a session's active pane.
// Returns an empty string and error if the session doesn't exist.
func GetPaneCurrentPath(name string) (string, error) {
	cmd := exec.Command("tmux", "-L", ServerName(), "display-message", "-p", "-t", name, "#{pane_current_path}")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// KillServer kills the entire tmux server (for test cleanup)
func KillServer() error {
	cmd := exec.Command("tmux", "-L", ServerName(), "kill-server")
	cmd.Run() // best-effort, may already be dead
	return nil
}
