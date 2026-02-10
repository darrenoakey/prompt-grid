package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"claude-term/src/tmux"
)

// SocketPath returns the path to the IPC socket.
// Uses the session realm for test isolation.
func SocketPath() string {
	return filepath.Join(tmux.GetSocketDir(), "ipc.sock")
}

// Request represents a request to create a new session
type Request struct {
	SessionName string `json:"session_name"`
	SSHHost     string `json:"ssh_host,omitempty"`
}

// Response from the primary instance
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// TryConnect attempts to connect to an existing instance
// Returns true if connected and request was sent successfully
func TryConnect(req Request) (bool, error) {
	conn, err := net.Dial("unix", SocketPath())
	if err != nil {
		// No existing instance
		return false, nil
	}
	defer conn.Close()

	// Send request
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(req); err != nil {
		return false, fmt.Errorf("failed to send request: %w", err)
	}

	// Read response
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	if !resp.OK {
		return true, fmt.Errorf("server error: %s", resp.Error)
	}

	return true, nil
}

// Server listens for incoming session requests
type Server struct {
	listener net.Listener
	onRequest func(Request) error
}

// NewServer creates a new IPC server
func NewServer(onRequest func(Request) error) (*Server, error) {
	// Ensure socket directory exists
	sockPath := SocketPath()
	if err := os.MkdirAll(filepath.Dir(sockPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create socket dir: %w", err)
	}

	// Remove stale socket
	os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create socket: %w", err)
	}

	return &Server{
		listener:  listener,
		onRequest: onRequest,
	}, nil
}

// Run starts accepting connections
func (s *Server) Run() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Listener closed
			return
		}
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Prevent hung connections from leaking goroutines
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Read request
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return
	}

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		s.sendResponse(conn, Response{OK: false, Error: "invalid request"})
		return
	}

	// Process request
	if err := s.onRequest(req); err != nil {
		s.sendResponse(conn, Response{OK: false, Error: err.Error()})
		return
	}

	s.sendResponse(conn, Response{OK: true})
}

func (s *Server) sendResponse(conn net.Conn, resp Response) {
	data, _ := json.Marshal(resp)
	conn.Write(append(data, '\n'))
}

// Close shuts down the server
func (s *Server) Close() error {
	os.Remove(SocketPath())
	return s.listener.Close()
}
