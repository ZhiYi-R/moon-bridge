package util

import "testing"

func TestPtr(t *testing.T) {
	p := Ptr(42)
	if p == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *p != 42 {
		t.Fatalf("expected 42, got %d", *p)
	}

	bp := Ptr(true)
	if !*bp {
		t.Fatal("expected true")
	}
}

func TestDeref(t *testing.T) {
	v := true
	if got := Deref(&v, false); got != true {
		t.Fatalf("expected true, got %v", got)
	}
	if got := Deref[bool](nil, true); got != true {
		t.Fatalf("expected fallback true, got %v", got)
	}
	if got := Deref[int](nil, 7); got != 7 {
		t.Fatalf("expected fallback 7, got %d", got)
	}
}

func TestOrDefault(t *testing.T) {
	if got := OrDefault("value", "fallback"); got != "value" {
		t.Fatalf("expected value, got %q", got)
	}
	if got := OrDefault("", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
	if got := OrDefault(5, 10); got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
	if got := OrDefault(0, 10); got != 10 {
		t.Fatalf("expected fallback 10, got %d", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := FirstNonEmpty("", "", "third"); got != "third" {
		t.Fatalf("expected third, got %q", got)
	}
	if got := FirstNonEmpty("first", "second"); got != "first" {
		t.Fatalf("expected first, got %q", got)
	}
	if got := FirstNonEmpty("", ""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := FirstNonEmpty(); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
