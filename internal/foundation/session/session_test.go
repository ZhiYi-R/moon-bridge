package session

import (
	"sync"
	"testing"
	"time"
)

func TestNewCreatesUniqueIDs(t *testing.T) {
	s1 := New()
	s2 := New()
	if s1.ID == "" || s2.ID == "" {
		t.Fatal("session IDs should not be empty")
	}
	if s1.ID == s2.ID {
		t.Fatal("session IDs should be unique")
	}
}

func TestNewSetsCreatedAt(t *testing.T) {
	before := time.Now()
	s := New()
	after := time.Now()
	if s.CreatedAt.Before(before) || s.CreatedAt.After(after) {
		t.Fatalf("CreatedAt %v not between %v and %v", s.CreatedAt, before, after)
	}
}

func TestNewWithIDUsesGivenID(t *testing.T) {
	s := NewWithID("test-id")
	if s.ID != "test-id" {
		t.Fatalf("expected ID test-id, got %s", s.ID)
	}
}

func TestInitExtensionsPopulatesData(t *testing.T) {
	s := New()
	data := map[string]any{"plugin_a": "state_a", "plugin_b": 42}
	s.InitExtensions(data)
	if s.ExtensionData == nil {
		t.Fatal("ExtensionData should not be nil after InitExtensions")
	}
	if s.ExtensionData["plugin_a"] != "state_a" {
		t.Fatalf("expected plugin_a = state_a, got %v", s.ExtensionData["plugin_a"])
	}
	if s.ExtensionData["plugin_b"] != 42 {
		t.Fatalf("expected plugin_b = 42, got %v", s.ExtensionData["plugin_b"])
	}
}

func TestInitExtensionsWithNil(t *testing.T) {
	s := New()
	s.InitExtensions(nil)
	if s.ExtensionData != nil {
		t.Fatal("ExtensionData should be nil when initialized with nil")
	}
}

func TestContextKeyString(t *testing.T) {
	k := ContextKey{}
	if k.String() != "moonbridge-session" {
		t.Fatalf("unexpected ContextKey string: %s", k.String())
	}
}

func TestSessionConcurrentReadAccess(t *testing.T) {
	s := New()
	s.InitExtensions(map[string]any{"count": 0})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.ID
			_ = s.CreatedAt
			_ = s.ExtensionData
		}()
	}
	wg.Wait()
}
