// Package session manages request or client-session state, isolating mutable
// state such as extension caches across unrelated requests.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Session holds mutable state that should be isolated across unrelated
// conversations (e.g., thinking blocks from one conversation leaking into
// another).
type Session struct {
	ID            string
	ExtensionData map[string]any
	CreatedAt     time.Time
}

// New creates a new Session with a unique ID.
// Call InitExtensions after creation to populate extension state.
func New() *Session {
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &Session{
		ID:        hex.EncodeToString(id),
		CreatedAt: time.Now(),
	}
}

// InitExtensions populates ExtensionData from a registry's NewSessionData.
func (s *Session) InitExtensions(data map[string]any) {
	s.ExtensionData = data
}

// NewWithID creates a Session with the given ID (useful for testing).
func NewWithID(id string) *Session {
	s := New()
	s.ID = id
	return s
}

// ContextKey is the key used to store/retrieve a Session from a context.Context.
type ContextKey struct{}

// String returns the context key name for debugging.
func (k ContextKey) String() string {
	return "moonbridge-session"
}
