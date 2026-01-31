package pty

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrSessionNotFound is returned when a session is not found
var ErrSessionNotFound = errors.New("session not found")

// Manager orchestrates multiple PTY sessions
type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	onCreate func(*Session)
	onClose  func(*Session)
}

// NewManager creates a new session manager
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

// SetOnCreate sets the callback for when a session is created
func (m *Manager) SetOnCreate(fn func(*Session)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onCreate = fn
}

// SetOnClose sets the callback for when a session is closed
func (m *Manager) SetOnClose(fn func(*Session)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onClose = fn
}

// NewSession creates and registers a new session
func (m *Manager) NewSession(name string) (*Session, error) {
	m.mu.Lock()
	if _, exists := m.sessions[name]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("session %q already exists", name)
	}

	session := NewSession(name)
	m.sessions[name] = session
	onCreate := m.onCreate
	m.mu.Unlock()

	// Set up exit handler
	session.SetOnExit(func(err error) {
		m.removeSession(name)
	})

	if onCreate != nil {
		onCreate(session)
	}

	return session, nil
}

// Get returns a session by name
func (m *Manager) Get(name string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[name]
}

// List returns all session names, sorted alphabetically
func (m *Manager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.sessions))
	for name := range m.sessions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Count returns the number of active sessions
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// Close closes a session by name
func (m *Manager) Close(name string) error {
	m.mu.RLock()
	session, exists := m.sessions[name]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("session %q not found", name)
	}

	return session.Close()
}

// removeSession removes a session from the manager
func (m *Manager) removeSession(name string) {
	m.mu.Lock()
	session, exists := m.sessions[name]
	if exists {
		delete(m.sessions, name)
	}
	onClose := m.onClose
	m.mu.Unlock()

	if exists && onClose != nil {
		onClose(session)
	}
}

// CloseAll closes all sessions
func (m *Manager) CloseAll() {
	m.mu.RLock()
	names := make([]string, 0, len(m.sessions))
	for name := range m.sessions {
		names = append(names, name)
	}
	m.mu.RUnlock()

	for _, name := range names {
		m.Close(name)
	}
}
