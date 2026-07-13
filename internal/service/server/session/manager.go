// Package session provides session management for the server.
//
// It extracts Session state management from the Server god object,
// making it independently testable and replaceable.
package session

import (
	"sync"
	"time"

	"github.com/ZhiYi-R/moon-bridge/internal/extension/plugin"
	"github.com/ZhiYi-R/moon-bridge/internal/service/api"
	"github.com/ZhiYi-R/moon-bridge/internal/session"
)

// Manager handles session lifecycle: lookup, creation, TTL enforcement,
// LRU eviction, and background pruning.
type Manager interface {
	// GetOrCreate returns the session for the given key, creating one if needed.
	// now is the current time used for staleness checks and lastUsed updates.
	GetOrCreate(key string, now time.Time) *session.Session

	// List returns a snapshot of all active sessions.
	List() []api.SessionInfo

	// Prune removes expired sessions. now is the current time.
	Prune(now time.Time)

	// Stop signals the background pruner goroutine to exit.
	Stop()

	// NewEphemeral creates a request-scoped session not tracked in the manager.
	NewEphemeral() *session.Session
}

// ConfigAccessor provides session-related configuration values.
type ConfigAccessor interface {
	SessionTTL() time.Duration
	MaxSessions() int
}

// serverSession wraps a session.Session with its last-used timestamp.
type serverSession struct {
	sess     *session.Session
	lastUsed time.Time
}

// InMemoryManager is the default Manager implementation that stores sessions
// in an in-memory map with periodic pruning.
type InMemoryManager struct {
	mu             sync.Mutex
	sessions       map[string]serverSession
	pruneStop      chan struct{}
	config         ConfigAccessor
	pluginRegistry *plugin.Registry
}

// NewInMemoryManager creates a new InMemoryManager and starts the background
// pruning goroutine. Call Stop() to cleanly shut down.
func NewInMemoryManager(config ConfigAccessor, pluginRegistry *plugin.Registry) *InMemoryManager {
	m := &InMemoryManager{
		sessions:       make(map[string]serverSession),
		pruneStop:      make(chan struct{}),
		config:         config,
		pluginRegistry: pluginRegistry,
	}
	go m.startPruning()
	return m
}

// GetOrCreate returns the session for key, creating a new one if absent.
// When creating, it enforces MaxSessions by LRU eviction.
func (m *InMemoryManager) GetOrCreate(key string, now time.Time) *session.Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pruneLocked(now)
	if entry, ok := m.sessions[key]; ok {
		entry.lastUsed = now
		m.sessions[key] = entry
		// Backfill ExtensionData if the session was created before plugins were initialized.
		if entry.sess != nil && entry.sess.ExtensionData == nil && m.pluginRegistry != nil {
			entry.sess.InitExtensions(m.pluginRegistry.NewSessionData())
		}
		return entry.sess
	}

	// Enforce max sessions limit.
	if maxSessions := m.config.MaxSessions(); maxSessions > 0 && len(m.sessions) >= maxSessions {
		for len(m.sessions) >= maxSessions {
			if !m.evictLRULocked() {
				break
			}
		}
	}

	sess := session.NewWithID(key)
	if m.pluginRegistry != nil {
		sess.InitExtensions(m.pluginRegistry.NewSessionData())
	} else {
		sess.InitExtensions(nil)
	}
	m.sessions[key] = serverSession{sess: sess, lastUsed: now}
	return sess
}

// List returns a snapshot of all active sessions.
func (m *InMemoryManager) List() []api.SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]api.SessionInfo, 0, len(m.sessions))
	for key, entry := range m.sessions {
		result = append(result, api.SessionInfo{
			Key:       key,
			CreatedAt: entry.sess.CreatedAt.Format(time.RFC3339),
			LastUsed:  entry.lastUsed.Format(time.RFC3339),
		})
	}
	return result
}

// Prune removes expired sessions.
func (m *InMemoryManager) Prune(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(now)
}

// Stop signals the background pruner to exit.
func (m *InMemoryManager) Stop() {
	close(m.pruneStop)
}

// NewEphemeral creates a request-scoped in-memory session and initializes
// extension state the same way as tracked sessions.
func (m *InMemoryManager) NewEphemeral() *session.Session {
	sess := session.New()
	if m.pluginRegistry != nil {
		sess.InitExtensions(m.pluginRegistry.NewSessionData())
	} else {
		sess.InitExtensions(nil)
	}
	return sess
}

// startPruning runs a background goroutine that prunes every hour.
func (m *InMemoryManager) startPruning() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.Prune(time.Now())
		case <-m.pruneStop:
			return
		}
	}
}

// pruneLocked removes expired sessions. Must be called with m.mu held.
func (m *InMemoryManager) pruneLocked(now time.Time) {
	ttl := m.config.SessionTTL()
	for key, entry := range m.sessions {
		if now.Sub(entry.lastUsed) > ttl {
			delete(m.sessions, key)
		}
	}
}

// evictLRULocked removes the session with the oldest lastUsed timestamp.
// Must be called with m.mu held.
func (m *InMemoryManager) evictLRULocked() bool {
	var oldestKey string
	var oldestTime time.Time
	first := true
	for key, entry := range m.sessions {
		if first || entry.lastUsed.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.lastUsed
			first = false
		}
	}
	if oldestKey != "" {
		delete(m.sessions, oldestKey)
		return true
	}
	return false
}
