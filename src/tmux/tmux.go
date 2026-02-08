package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	// RealmEnvVar is the environment variable for test isolation
	RealmEnvVar = "CLAUDE_TERM_REALM"
)

// ServerName returns the tmux server name, realm-aware for test isolation
func ServerName() string {
	if realm := os.Getenv(RealmEnvVar); realm != "" {
		return "claude-term-" + realm
	}
	return "claude-term"
}

// GetSocketDir returns the directory for IPC sockets (realm-aware)
func GetSocketDir() string {
	if realm := os.Getenv(RealmEnvVar); realm != "" {
		return "/tmp/claude-term-" + realm
	}
	return "/tmp/claude-term-sessions"
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

// NewSession creates a new tmux session with the given name and size.
// For SSH sessions, sshHost is set as the initial command.
// The session is created detached and configured to be invisible (no status bar, no prefix key).
func NewSession(name, sshHost string, cols, rows uint16) error {
	args := []string{
		"-L", ServerName(),
		"new-session", "-d",
		"-s", name,
		"-x", fmt.Sprintf("%d", cols),
		"-y", fmt.Sprintf("%d", rows),
	}

	if sshHost != "" {
		args = append(args, "ssh", sshHost)
	}

	cmd := exec.Command("tmux", args...)
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session failed: %w: %s", err, out)
	}

	// Configure session to be invisible: status off, prefix none, unbind all keys
	for _, setting := range [][]string{
		{"-L", ServerName(), "set-option", "-t", name, "status", "off"},
		{"-L", ServerName(), "set-option", "-t", name, "prefix", "None"},
		{"-L", ServerName(), "unbind-key", "-t", name, "-a"},
	} {
		cmd := exec.Command("tmux", setting...)
		cmd.Run() // best-effort
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

// KillServer kills the entire tmux server (for test cleanup)
func KillServer() error {
	cmd := exec.Command("tmux", "-L", ServerName(), "kill-server")
	cmd.Run() // best-effort, may already be dead
	return nil
}
