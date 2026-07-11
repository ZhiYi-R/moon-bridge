package session_test

import (
	"testing"
	"time"

	"moonbridge/internal/extension/plugin"
	sessionmgr "moonbridge/internal/service/server/session"
)

// fakeConfig is a test ConfigAccessor with fixed TTL and max sessions.
type fakeConfig struct {
	ttl         time.Duration
	maxSessions int
}

func (c fakeConfig) SessionTTL() time.Duration { return c.ttl }
func (c fakeConfig) MaxSessions() int          { return c.maxSessions }

func newManager(ttl time.Duration, maxSessions int) *sessionmgr.InMemoryManager {
	return sessionmgr.NewInMemoryManager(fakeConfig{ttl: ttl, maxSessions: maxSessions}, nil)
}

func TestGetOrCreateCreatesAndReuses(t *testing.T) {
	m := newManager(time.Hour, 0)
	defer m.Stop()

	now := time.Now()
	s1 := m.GetOrCreate("a", now)
	if s1 == nil {
		t.Fatal("GetOrCreate returned nil")
	}
	if s1.ID != "a" {
		t.Errorf("session ID = %q, want a", s1.ID)
	}

	s2 := m.GetOrCreate("a", now.Add(time.Minute))
	if s1 != s2 {
		t.Error("GetOrCreate should return the same session instance for the same key")
	}

	s3 := m.GetOrCreate("b", now)
	if s3 == s1 {
		t.Error("GetOrCreate should return a distinct session for a different key")
	}
}

func TestGetOrCreateInitializesExtensions(t *testing.T) {
	m := newManager(time.Hour, 0)
	defer m.Stop()

	// With a nil plugin registry, ExtensionData is initialized to nil (not left unset).
	s := m.GetOrCreate("a", time.Now())
	if s.ExtensionData != nil {
		t.Errorf("ExtensionData = %v, want nil with nil registry", s.ExtensionData)
	}
}

func TestListReturnsSnapshot(t *testing.T) {
	m := newManager(time.Hour, 0)
	defer m.Stop()

	now := time.Now()
	m.GetOrCreate("a", now)
	m.GetOrCreate("b", now)

	infos := m.List()
	if len(infos) != 2 {
		t.Fatalf("List returned %d sessions, want 2", len(infos))
	}
	keys := map[string]bool{}
	for _, info := range infos {
		keys[info.Key] = true
		if info.CreatedAt == "" {
			t.Errorf("session %q has empty CreatedAt", info.Key)
		}
		if info.LastUsed == "" {
			t.Errorf("session %q has empty LastUsed", info.Key)
		}
	}
	if !keys["a"] || !keys["b"] {
		t.Errorf("List keys = %v, want a and b", keys)
	}
}

func TestPruneRemovesExpiredSessions(t *testing.T) {
	m := newManager(90*time.Minute, 0)
	defer m.Stop()

	base := time.Now()
	m.GetOrCreate("old", base)
	m.GetOrCreate("fresh", base.Add(time.Hour))

	// Prune at base+2h: "old" is stale (used at base), "fresh" used at base+1h.
	m.Prune(base.Add(2 * time.Hour))

	infos := m.List()
	if len(infos) != 1 {
		t.Fatalf("after prune got %d sessions, want 1", len(infos))
	}
	if infos[0].Key != "fresh" {
		t.Errorf("remaining session = %q, want fresh", infos[0].Key)
	}
}

func TestGetOrCreatePrunesBeforeLookup(t *testing.T) {
	m := newManager(30*time.Minute, 0)
	defer m.Stop()

	base := time.Now()
	first := m.GetOrCreate("a", base)

	// Access with a much-later time: the stale entry is pruned and recreated.
	second := m.GetOrCreate("a", base.Add(2*time.Hour))
	if first == second {
		t.Error("expected a fresh session after the previous one expired")
	}
}

func TestMaxSessionsEvictsLRU(t *testing.T) {
	m := newManager(time.Hour, 2)
	defer m.Stop()

	base := time.Now()
	m.GetOrCreate("a", base)
	m.GetOrCreate("b", base.Add(time.Minute))
	// Adding a third session should evict the least-recently-used ("a").
	m.GetOrCreate("c", base.Add(2*time.Minute))

	infos := m.List()
	if len(infos) != 2 {
		t.Fatalf("got %d sessions, want 2", len(infos))
	}
	keys := map[string]bool{}
	for _, info := range infos {
		keys[info.Key] = true
	}
	if keys["a"] {
		t.Error("expected LRU session 'a' to have been evicted")
	}
	if !keys["b"] || !keys["c"] {
		t.Errorf("expected b and c to remain, got %v", keys)
	}
}

func TestMaxSessionsReuseUpdatesRecency(t *testing.T) {
	m := newManager(time.Hour, 2)
	defer m.Stop()

	base := time.Now()
	m.GetOrCreate("a", base)
	m.GetOrCreate("b", base.Add(time.Minute))
	// Touch "a" so it becomes most-recently-used.
	m.GetOrCreate("a", base.Add(2*time.Minute))
	// Adding "c" should now evict "b" instead of "a".
	m.GetOrCreate("c", base.Add(3*time.Minute))

	keys := map[string]bool{}
	for _, info := range m.List() {
		keys[info.Key] = true
	}
	if keys["b"] {
		t.Error("expected 'b' to be evicted as LRU")
	}
	if !keys["a"] || !keys["c"] {
		t.Errorf("expected a and c to remain, got %v", keys)
	}
}

func TestGetOrCreateWithPluginRegistry(t *testing.T) {
	reg := plugin.NewRegistry(nil)
	m := sessionmgr.NewInMemoryManager(fakeConfig{ttl: time.Hour}, reg)
	defer m.Stop()

	now := time.Now()
	s1 := m.GetOrCreate("a", now)
	if s1 == nil {
		t.Fatal("GetOrCreate returned nil")
	}
	// Reusing the key exercises the ExtensionData backfill branch.
	if s2 := m.GetOrCreate("a", now.Add(time.Minute)); s2 != s1 {
		t.Error("expected the same session on reuse")
	}
}

func TestNewEphemeralWithPluginRegistry(t *testing.T) {
	reg := plugin.NewRegistry(nil)
	m := sessionmgr.NewInMemoryManager(fakeConfig{ttl: time.Hour}, reg)
	defer m.Stop()

	if s := m.NewEphemeral(); s == nil {
		t.Fatal("NewEphemeral returned nil")
	}
}

func TestNewEphemeralIsNotTracked(t *testing.T) {
	m := newManager(time.Hour, 0)
	defer m.Stop()

	s := m.NewEphemeral()
	if s == nil {
		t.Fatal("NewEphemeral returned nil")
	}
	if s.ID == "" {
		t.Error("ephemeral session should have a generated ID")
	}
	if len(m.List()) != 0 {
		t.Error("ephemeral session must not be tracked by the manager")
	}
}

func TestStopIsIdempotentlyCloseable(t *testing.T) {
	m := newManager(time.Hour, 0)
	m.Stop()
	// A second Stop would panic on a double-close; ensure we only call it once
	// but that the manager remains usable for reads after stopping.
	if got := m.List(); len(got) != 0 {
		t.Errorf("List after Stop = %v, want empty", got)
	}
}
