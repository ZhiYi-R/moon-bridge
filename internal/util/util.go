// Package util provides small, generic helpers shared across the codebase.
//
// These consolidate value-fallback and pointer patterns that were previously
// duplicated as unexported helpers in multiple packages.
package util

// Ptr returns a pointer to v. It is useful for populating optional
// pointer-typed struct fields from concrete values.
func Ptr[T any](v T) *T {
	return &v
}

// Deref returns *p when p is non-nil, otherwise fallback.
func Deref[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

// OrDefault returns value when it is not the zero value of T, otherwise
// fallback. It works for any comparable type (e.g. strings, ints).
func OrDefault[T comparable](value, fallback T) T {
	var zero T
	if value == zero {
		return fallback
	}
	return value
}

// FirstNonEmpty returns the first non-empty string in vals, or "" if all are
// empty.
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
