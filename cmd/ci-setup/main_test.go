package main

import (
	"strings"
	"testing"
)

// TestHasCurrentHostRef verifies that the reference check correctly
// identifies URLs that use the current Postgres service name for both
// host and port.
func TestHasCurrentHostRef(t *testing.T) {
	tests := []struct {
		name        string
		connURL     string
		pgService   string
		wantCurrent bool
	}{
		{
			name:        "references with matching service name",
			connURL:     "postgresql://user:pass@${{Postgres-18.PGHOST}}:${{Postgres-18.PGPORT}}/db",
			pgService:   "Postgres-18",
			wantCurrent: true,
		},
		{
			name:        "references with different service name",
			connURL:     "postgresql://user:pass@${{Postgres-18.PGHOST}}:${{Postgres-18.PGPORT}}/db",
			pgService:   "Postgres-19",
			wantCurrent: false,
		},
		{
			name:        "hardcoded host from v1.3",
			connURL:     "postgresql://user:pass@postgres.railway.internal:5432/db",
			pgService:   "Postgres-18",
			wantCurrent: false,
		},
		{
			name:        "host reference correct, port reference wrong service",
			connURL:     "postgresql://user:pass@${{Postgres-18.PGHOST}}:${{Postgres-19.PGPORT}}/db",
			pgService:   "Postgres-18",
			wantCurrent: false,
		},
		{
			name:        "host reference correct, port hardcoded",
			connURL:     "postgresql://user:pass@${{Postgres-18.PGHOST}}:5432/db",
			pgService:   "Postgres-18",
			wantCurrent: false,
		},
		{
			name:        "reference without @ prefix",
			connURL:     "postgresql://user:pass${{Postgres-18.PGHOST}}:${{Postgres-18.PGPORT}}/db",
			pgService:   "Postgres-18",
			wantCurrent: false,
		},
		{
			name:        "empty URL",
			connURL:     "",
			pgService:   "Postgres-18",
			wantCurrent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasCurrentHostRef(tt.connURL, tt.pgService)
			if got != tt.wantCurrent {
				t.Errorf("hasCurrentHostRef(%q, %q) = %v, want %v",
					tt.connURL, tt.pgService, got, tt.wantCurrent)
			}
		})
	}
}

// TestBuildConnURL verifies that generated URLs use Railway references.
func TestBuildConnURL(t *testing.T) {
	got := buildConnURL("myuser", "mypass", "mydb", "Postgres-18")
	want := "postgresql://myuser:mypass@${{Postgres-18.PGHOST}}:${{Postgres-18.PGPORT}}/mydb"
	if got != want {
		t.Errorf("buildConnURL() = %q, want %q", got, want)
	}

	// Verify the built URL passes the reference check.
	if !hasCurrentHostRef(got, "Postgres-18") {
		t.Error("buildConnURL output failed hasCurrentHostRef check")
	}
}

// TestBuildConnURLPositions verifies that PGHOST and PGPORT references are
// in the correct positions within the URL: host after @, port after host:,
// and the database name after the port.
func TestBuildConnURLPositions(t *testing.T) {
	const pgService = "Postgres-18"
	url := buildConnURL("user", "pass", "test_db", pgService)

	hostRef := "${{" + pgService + ".PGHOST}}"
	portRef := "${{" + pgService + ".PGPORT}}"

	atIdx := strings.Index(url, "@")
	hostIdx := strings.Index(url, hostRef)
	colonAfterHost := strings.Index(url[hostIdx:], ":") + hostIdx
	portIdx := strings.Index(url, portRef)
	slashAfterPort := strings.Index(url[portIdx:], "/") + portIdx

	if atIdx < 0 {
		t.Fatal("URL missing @ separator")
	}
	if hostIdx < 0 {
		t.Fatal("URL missing PGHOST reference")
	}
	if portIdx < 0 {
		t.Fatal("URL missing PGPORT reference")
	}

	// @ must come before PGHOST
	if hostIdx < atIdx {
		t.Errorf("PGHOST ref at %d before @ at %d", hostIdx, atIdx)
	}
	// PGHOST must come before the colon that separates host:port
	if colonAfterHost < hostIdx {
		t.Errorf("host:port colon at %d before PGHOST at %d", colonAfterHost, hostIdx)
	}
	// PGPORT must come after the host:port colon
	if portIdx < colonAfterHost {
		t.Errorf("PGPORT ref at %d before host:port colon at %d", portIdx, colonAfterHost)
	}
	// / must come after PGPORT
	if slashAfterPort < portIdx {
		t.Errorf("database slash at %d before PGPORT at %d", slashAfterPort, portIdx)
	}

	// PGPORT must not appear before PGHOST (no swap)
	if portIdx < hostIdx {
		t.Errorf("PGPORT at %d appears before PGHOST at %d (swapped)", portIdx, hostIdx)
	}
}

// TestHasCurrentHostRefSwappedRefs verifies that swapped PGHOST/PGPORT
// references are detected as needing update.
func TestHasCurrentHostRefSwappedRefs(t *testing.T) {
	swapped := "postgresql://user:pass@${{Postgres-18.PGPORT}}:${{Postgres-18.PGHOST}}/db"
	if hasCurrentHostRef(swapped, "Postgres-18") {
		t.Error("hasCurrentHostRef returned true for swapped PGHOST/PGPORT")
	}
}

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
