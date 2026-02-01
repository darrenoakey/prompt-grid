package session

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	// SocketDir is where session sockets live
	SocketDir = "/tmp/claude-term-sessions"

	// HistorySize is how much PTY output to buffer for reconnection
	HistorySize = 256 * 1024 // 256KB
)

// DaemonInfo is stored in a JSON file alongside the socket for validation
type DaemonInfo struct {
	PID       int       `json:"pid"`
	StartTime time.Time `json:"start_time"`
	Name      string    `json:"name"`
	SSHHost   string    `json:"ssh_host,omitempty"`
	Cols      uint16    `json:"cols"`
	Rows      uint16    `json:"rows"`
}

// Daemon manages a single PTY session and accepts client connections
type Daemon struct {
	name     string
	sshHost  string
	cols     uint16
	rows     uint16
	ptyFile  *os.File
	cmd      *exec.Cmd
	listener net.Listener

	// Circular buffer for history replay
	history    []byte
	historyPos int
	historyMu  sync.RWMutex

	// Connected clients
	clients   map[net.Conn]struct{}
	clientsMu sync.RWMutex

	done chan struct{}
	mu   sync.Mutex
}

// NewDaemon creates a new session daemon
func NewDaemon(name string, cols, rows uint16, sshHost string) *Daemon {
	return &Daemon{
		name:    name,
		sshHost: sshHost,
		cols:    cols,
		rows:    rows,
		history: make([]byte, 0, HistorySize),
		clients: make(map[net.Conn]struct{}),
		done:    make(chan struct{}),
	}
}

// SocketPath returns the Unix socket path for this session
func SocketPath(name string) string {
	return filepath.Join(SocketDir, name+".sock")
}

// InfoPath returns the info file path for this session
func InfoPath(name string) string {
	return filepath.Join(SocketDir, name+".json")
}

// Run starts the daemon - this blocks until the session exits
func (d *Daemon) Run() error {
	// Ensure socket directory exists
	if err := os.MkdirAll(SocketDir, 0755); err != nil {
		return fmt.Errorf("failed to create socket dir: %w", err)
	}

	// Clean up any stale socket
	socketPath := SocketPath(d.name)
	os.Remove(socketPath)
	os.Remove(InfoPath(d.name))

	// Start listening before starting PTY (so clients can connect immediately)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}
	d.listener = listener

	// Write info file for validation
	info := DaemonInfo{
		PID:       os.Getpid(),
		StartTime: time.Now(),
		Name:      d.name,
		SSHHost:   d.sshHost,
		Cols:      d.cols,
		Rows:      d.rows,
	}
	infoData, _ := json.Marshal(info)
	if err := os.WriteFile(InfoPath(d.name), infoData, 0644); err != nil {
		listener.Close()
		return fmt.Errorf("failed to write info file: %w", err)
	}

	// Start PTY
	if err := d.startPTY(); err != nil {
		listener.Close()
		os.Remove(socketPath)
		os.Remove(InfoPath(d.name))
		return fmt.Errorf("failed to start PTY: %w", err)
	}

	// Accept connections in goroutine
	go d.acceptLoop()

	// Read from PTY and broadcast to clients
	d.readLoop()

	// Cleanup
	d.cleanup()

	return nil
}

func (d *Daemon) startPTY() error {
	var cmd *exec.Cmd
	if d.sshHost != "" {
		cmd = exec.Command("ssh", d.sshHost)
	} else {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/zsh"
		}
		cmd = exec.Command(shell, "-l", "-i")
	}

	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: d.cols,
		Rows: d.rows,
	})
	if err != nil {
		return err
	}

	d.ptyFile = ptmx
	d.cmd = cmd

	return nil
}

func (d *Daemon) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := d.ptyFile.Read(buf)
		if err != nil {
			return
		}
		if n > 0 {
			data := buf[:n]

			// Add to history buffer
			d.addToHistory(data)

			// Broadcast to all clients
			d.broadcast(data)
		}
	}
}

func (d *Daemon) addToHistory(data []byte) {
	d.historyMu.Lock()
	defer d.historyMu.Unlock()

	// Simple append with cap check - if we exceed, keep last half
	d.history = append(d.history, data...)
	if len(d.history) > HistorySize {
		// Keep the most recent half
		copy(d.history, d.history[len(d.history)-HistorySize/2:])
		d.history = d.history[:HistorySize/2]
	}
}

func (d *Daemon) getHistory() []byte {
	d.historyMu.RLock()
	defer d.historyMu.RUnlock()
	result := make([]byte, len(d.history))
	copy(result, d.history)
	return result
}

func (d *Daemon) broadcast(data []byte) {
	d.clientsMu.RLock()
	clients := make([]net.Conn, 0, len(d.clients))
	for c := range d.clients {
		clients = append(clients, c)
	}
	d.clientsMu.RUnlock()

	for _, conn := range clients {
		if err := WriteData(conn, data); err != nil {
			// Client disconnected
			d.removeClient(conn)
		}
	}
}

func (d *Daemon) acceptLoop() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			return // Listener closed
		}
		go d.handleClient(conn)
	}
}

func (d *Daemon) handleClient(conn net.Conn) {
	defer conn.Close()

	// Send session info
	info := Info{
		Name:    d.name,
		Cols:    d.cols,
		Rows:    d.rows,
		SSHHost: d.sshHost,
	}
	if err := WriteInfo(conn, info); err != nil {
		return
	}

	// Send history
	history := d.getHistory()
	if len(history) > 0 {
		if err := WriteData(conn, history); err != nil {
			return
		}
	}

	// Send history complete marker
	if err := WriteHistoryComplete(conn); err != nil {
		return
	}

	// Add to clients list
	d.clientsMu.Lock()
	d.clients[conn] = struct{}{}
	d.clientsMu.Unlock()

	// Read from client and write to PTY
	for {
		msgType, payload, err := ReadMessage(conn)
		if err != nil {
			break
		}

		switch msgType {
		case MsgData:
			d.ptyFile.Write(payload)
		case MsgResize:
			cols, rows, err := ParseResize(payload)
			if err == nil {
				d.resize(cols, rows)
			}
		case MsgInfo:
			WriteInfo(conn, info)
		case MsgClose:
			// Client requested session termination
			d.ptyFile.Close()
			return
		}
	}

	d.removeClient(conn)
}

func (d *Daemon) removeClient(conn net.Conn) {
	d.clientsMu.Lock()
	delete(d.clients, conn)
	d.clientsMu.Unlock()
	conn.Close()
}

func (d *Daemon) resize(cols, rows uint16) {
	d.mu.Lock()
	d.cols = cols
	d.rows = rows
	d.mu.Unlock()

	if d.ptyFile != nil {
		pty.Setsize(d.ptyFile, &pty.Winsize{
			Cols: cols,
			Rows: rows,
		})
	}
}

func (d *Daemon) cleanup() {
	close(d.done)

	// Close all clients
	d.clientsMu.Lock()
	for conn := range d.clients {
		conn.Close()
	}
	d.clientsMu.Unlock()

	// Close listener
	if d.listener != nil {
		d.listener.Close()
	}

	// Close PTY
	if d.ptyFile != nil {
		d.ptyFile.Close()
	}

	// Kill process if still running
	if d.cmd != nil && d.cmd.Process != nil {
		d.cmd.Process.Signal(syscall.SIGTERM)
	}

	// Remove socket and info file
	os.Remove(SocketPath(d.name))
	os.Remove(InfoPath(d.name))
}

// ListSessions returns names of all running sessions
// It validates each session is actually alive
func ListSessions() []string {
	entries, err := os.ReadDir(SocketDir)
	if err != nil {
		return nil
	}

	var sessions []string
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".sock" {
			continue
		}
		name := entry.Name()[:len(entry.Name())-5] // Remove .sock

		// Validate session is alive
		if IsSessionAlive(name) {
			sessions = append(sessions, name)
		} else {
			// Clean up stale files
			os.Remove(SocketPath(name))
			os.Remove(InfoPath(name))
		}
	}

	return sessions
}

// IsSessionAlive checks if a session daemon is actually running
func IsSessionAlive(name string) bool {
	// Check info file
	infoData, err := os.ReadFile(InfoPath(name))
	if err != nil {
		return false
	}

	var info DaemonInfo
	if err := json.Unmarshal(infoData, &info); err != nil {
		return false
	}

	// Check if process is running and is the same process (by start time)
	// On Unix, we can check if PID exists and matches
	proc, err := os.FindProcess(info.PID)
	if err != nil {
		return false
	}

	// Signal 0 checks if process exists without actually sending a signal
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}

	// Try to connect to validate the daemon is responsive
	conn, err := net.DialTimeout("unix", SocketPath(name), 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()

	return true
}

// SpawnDaemon starts a new session daemon as a background process
func SpawnDaemon(name string, cols, rows uint16, sshHost string) error {
	// Get the path to the executable
	// Use CLAUDE_TERM_BIN env var if set (for testing), otherwise use os.Executable()
	exe := os.Getenv("CLAUDE_TERM_BIN")
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return err
		}
	}

	args := []string{
		"--session-daemon", name,
		"--cols", fmt.Sprintf("%d", cols),
		"--rows", fmt.Sprintf("%d", rows),
	}
	if sshHost != "" {
		args = append(args, "--ssh", sshHost)
	}

	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	// Detach from parent process group
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	// Don't wait for it - it's a daemon
	go cmd.Wait()

	// Wait a moment for it to start and create socket
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if IsSessionAlive(name) {
			return nil
		}
	}

	return fmt.Errorf("daemon failed to start")
}
