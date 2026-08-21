package main

import (
	"strings"
	"testing"
)

// TestGeneratePasswordLength verifies the password has the requested length.
func TestGeneratePasswordLength(t *testing.T) {
	for _, length := range []int{1, 16, 32, 64, 128} {
		pw, err := generatePassword(length)
		if err != nil {
			t.Fatalf("generatePassword(%d): unexpected error: %v", length, err)
		}
		if len(pw) != length {
			t.Errorf("generatePassword(%d): got length %d, want %d", length, len(pw), length)
		}
	}
}

// TestGeneratePasswordCharset verifies every character is alphanumeric
// (URL-safe and SQL-safe — no special chars that need escaping).
func TestGeneratePasswordCharset(t *testing.T) {
	const allowed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	for i := 0; i < 1000; i++ {
		pw, err := generatePassword(64)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, c := range pw {
			if !strings.ContainsRune(allowed, c) {
				t.Fatalf("password %q contains non-alphanumeric char %q", pw, c)
			}
		}
	}
}

// TestGeneratePasswordNoDuplicates verifies that 10k generated passwords
// are all unique — confirms the entropy source is working and we're not
// accidentally seeding or reusing state.
func TestGeneratePasswordNoDuplicates(t *testing.T) {
	const count = 10_000
	seen := make(map[string]struct{}, count)

	for i := 0; i < count; i++ {
		pw, err := generatePassword(64)
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}
		if _, dup := seen[pw]; dup {
			t.Fatalf("duplicate password generated on iteration %d: %q", i, pw)
		}
		seen[pw] = struct{}{}
	}

	if len(seen) != count {
		t.Errorf("expected %d unique passwords, got %d", count, len(seen))
	}
}

// TestGeneratePasswordUniquenessShort verifies uniqueness even with short
// passwords where the keyspace is smaller (still should not collide in
// a small sample).
func TestGeneratePasswordUniquenessShort(t *testing.T) {
	const count = 1000
	seen := make(map[string]struct{}, count)

	for i := 0; i < count; i++ {
		pw, err := generatePassword(8)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, dup := seen[pw]; dup {
			t.Fatalf("duplicate short password on iteration %d: %q", i, pw)
		}
		seen[pw] = struct{}{}
	}
}
