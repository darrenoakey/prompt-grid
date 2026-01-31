package pty

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Size represents terminal dimensions
type Size struct {
	Cols uint16
	Rows uint16
}

// DefaultSize is the default terminal size (120x24)
var DefaultSize = Size{Cols: 120, Rows: 24}

// Session manages a single PTY process
type Session struct {
	name         string
	pty          *os.File
	cmd          *exec.Cmd
	size         Size
	onData       func([]byte)
	onExit       func(error)
	done         chan struct{}
	mu           sync.RWMutex
	closed       bool
	sshCmd       string // If non-empty, this is an SSH session
	sshHost      string
	autoReconnect bool
}

// NewSession creates a new PTY session with the given name
func NewSession(name string) *Session {
	return &Session{
		name: name,
		size: DefaultSize,
		done: make(chan struct{}),
	}
}

// Name returns the session name
func (s *Session) Name() string {
	return s.name
}

// SetOnData sets the callback for when data is received from the PTY
func (s *Session) SetOnData(fn func([]byte)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onData = fn
}

// SetOnExit sets the callback for when the session exits
func (s *Session) SetOnExit(fn func(error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onExit = fn
}

// Start spawns a shell in the PTY
func (s *Session) Start() error {
	return s.StartCommand(os.Getenv("SHELL"), nil)
}

// StartCommand spawns a command in the PTY
func (s *Session) StartCommand(command string, args []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return io.ErrClosedPipe
	}

	s.cmd = exec.Command(command, args...)
	s.cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)

	ptmx, err := pty.StartWithSize(s.cmd, &pty.Winsize{
		Cols: s.size.Cols,
		Rows: s.size.Rows,
	})
	if err != nil {
		return err
	}
	s.pty = ptmx

	// Start reading from PTY
	go s.readLoop()

	// Wait for command to exit
	go func() {
		err := s.cmd.Wait()
		s.mu.RLock()
		onExit := s.onExit
		s.mu.RUnlock()
		if onExit != nil {
			onExit(err)
		}
		close(s.done)
	}()

	return nil
}

// StartSSH spawns an SSH session with auto-reconnect
func (s *Session) StartSSH(host string) error {
	s.sshHost = host
	s.sshCmd = "ssh"
	s.autoReconnect = true
	return s.startSSHInternal()
}

func (s *Session) startSSHInternal() error {
	err := s.StartCommand("ssh", []string{s.sshHost})
	if err != nil {
		return err
	}

	// Set up auto-reconnect on exit if enabled
	if s.autoReconnect {
		go s.watchForReconnect()
	}

	return nil
}

func (s *Session) watchForReconnect() {
	<-s.done

	s.mu.RLock()
	closed := s.closed
	autoReconnect := s.autoReconnect
	s.mu.RUnlock()

	if closed || !autoReconnect {
		return
	}

	// Wait a moment before reconnecting
	time.Sleep(2 * time.Second)

	// Check again if session was closed
	s.mu.RLock()
	closed = s.closed
	s.mu.RUnlock()

	if closed {
		return
	}

	// Reset for new connection
	s.mu.Lock()
	s.done = make(chan struct{})
	s.mu.Unlock()

	// Notify about reconnection attempt
	s.mu.RLock()
	onData := s.onData
	s.mu.RUnlock()
	if onData != nil {
		onData([]byte("\r\n[Reconnecting to SSH...]\r\n"))
	}

	// Attempt to reconnect
	if err := s.startSSHInternal(); err != nil {
		if onData != nil {
			onData([]byte("\r\n[Reconnection failed: " + err.Error() + "]\r\n"))
		}
	}
}

// readLoop continuously reads from the PTY and calls onData
func (s *Session) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if err != nil {
			return
		}
		if n > 0 {
			s.mu.RLock()
			onData := s.onData
			s.mu.RUnlock()
			if onData != nil {
				data := make([]byte, n)
				copy(data, buf[:n])
				onData(data)
			}
		}
	}
}

// Write sends data to the PTY
func (s *Session) Write(data []byte) (int, error) {
	s.mu.RLock()
	ptyFile := s.pty
	s.mu.RUnlock()

	if ptyFile == nil {
		return 0, io.ErrClosedPipe
	}
	return ptyFile.Write(data)
}

// Resize changes the terminal size
func (s *Session) Resize(size Size) error {
	s.mu.Lock()
	s.size = size
	ptyFile := s.pty
	s.mu.Unlock()

	if ptyFile == nil {
		return nil
	}

	return pty.Setsize(ptyFile, &pty.Winsize{
		Cols: size.Cols,
		Rows: size.Rows,
	})
}

// Size returns the current terminal size
func (s *Session) Size() Size {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.size
}

// Close terminates the session
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ptyFile := s.pty
	cmd := s.cmd
	s.mu.Unlock()

	if ptyFile != nil {
		ptyFile.Close()
	}

	if cmd != nil && cmd.Process != nil {
		cmd.Process.Signal(syscall.SIGTERM)
	}

	// Wait for exit with timeout
	select {
	case <-s.done:
	default:
	}

	return nil
}

// Done returns a channel that closes when the session exits
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// IsSSH returns true if this is an SSH session
func (s *Session) IsSSH() bool {
	return s.sshHost != ""
}

// SSHHost returns the SSH host if this is an SSH session
func (s *Session) SSHHost() string {
	return s.sshHost
}
