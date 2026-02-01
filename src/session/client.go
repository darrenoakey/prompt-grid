package session

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Client connects to a session daemon
type Client struct {
	name   string
	conn   net.Conn
	info   Info
	onData func([]byte)
	onExit func(error)
	mu     sync.RWMutex
	closed bool
	done   chan struct{}
}

// Connect connects to an existing session daemon
func Connect(name string) (*Client, error) {
	socketPath := SocketPath(name)

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to session %s: %w", name, err)
	}

	client := &Client{
		name: name,
		conn: conn,
		done: make(chan struct{}),
	}

	// Read initial info
	msgType, payload, err := ReadMessage(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read session info: %w", err)
	}
	if msgType != MsgInfoResp {
		conn.Close()
		return nil, fmt.Errorf("expected info response, got %d", msgType)
	}

	info, err := ParseInfo(payload)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to parse session info: %w", err)
	}
	client.info = info

	return client, nil
}

// Info returns session information
func (c *Client) Info() Info {
	return c.info
}

// Name returns the session name
func (c *Client) Name() string {
	return c.info.Name
}

// SetOnData sets the callback for received PTY data
func (c *Client) SetOnData(fn func([]byte)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onData = fn
}

// SetOnExit sets the callback for when the session exits
func (c *Client) SetOnExit(fn func(error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onExit = fn
}

// Start begins reading from the daemon
// This should be called after SetOnData to receive history and live data
func (c *Client) Start() {
	go c.readLoop()
}

func (c *Client) readLoop() {
	var exitErr error

	for {
		msgType, payload, err := ReadMessage(c.conn)
		if err != nil {
			if err != io.EOF {
				exitErr = err
			}
			break
		}

		switch msgType {
		case MsgData:
			c.mu.RLock()
			onData := c.onData
			c.mu.RUnlock()
			if onData != nil {
				onData(payload)
			}
		case MsgHistory:
			// History complete - could notify if needed
		case MsgInfoResp:
			// Updated info - could handle if needed
		}
	}

	c.mu.Lock()
	c.closed = true
	onExit := c.onExit
	c.mu.Unlock()

	if onExit != nil {
		onExit(exitErr)
	}

	close(c.done)
}

// Write sends input to the PTY
func (c *Client) Write(data []byte) (int, error) {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()

	if closed {
		return 0, io.ErrClosedPipe
	}

	if err := WriteData(c.conn, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

// Resize sends a resize request to the daemon
func (c *Client) Resize(cols, rows uint16) error {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()

	if closed {
		return io.ErrClosedPipe
	}

	return WriteResize(c.conn, cols, rows)
}

// Close disconnects from the daemon (doesn't kill the session)
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	return c.conn.Close()
}

// Terminate closes the connection AND tells daemon to shut down
func (c *Client) Terminate() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	// Send close message to daemon
	WriteMessage(c.conn, MsgClose, nil)

	return c.Close()
}

// Done returns a channel that closes when the connection ends
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Size returns the current terminal size
func (c *Client) Size() (cols, rows uint16) {
	return c.info.Cols, c.info.Rows
}

// IsSSH returns true if this is an SSH session
func (c *Client) IsSSH() bool {
	return c.info.SSHHost != ""
}

// SSHHost returns the SSH host if this is an SSH session
func (c *Client) SSHHost() string {
	return c.info.SSHHost
}
