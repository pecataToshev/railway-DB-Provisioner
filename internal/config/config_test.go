package config

import (
	"testing"
)

func TestParseServiceEntry(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantErr bool
		wantDB  DBType
		wantPfx string
	}{
		{"standard", "POSTGRES:QUIZZER", false, Postgres, "QUIZZER"},
		{"lowercase normalized", "postgres:quizzer", false, Postgres, "QUIZZER"},
		{"with spaces", "  POSTGRES : AUTH  ", false, Postgres, "AUTH"},
		{"missing colon", "POSTGRESQUIZZER", true, "", ""},
		{"too many colons", "POSTGRES:QUIZ:ZER", true, "", ""},
		{"invalid db type", "MYSQL:QUIZZER", true, "", ""},
		{"empty prefix", "POSTGRES:", true, "", ""},
		{"empty line", "", true, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbType, entry, err := ParseServiceEntry(tt.line)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseServiceEntry(%q): err = %v, wantErr = %v", tt.line, err, tt.wantErr)
			}
			if !tt.wantErr {
				if dbType != tt.wantDB {
					t.Errorf("dbType: got %q, want %q", dbType, tt.wantDB)
				}
				if entry.Prefix != tt.wantPfx {
					t.Errorf("prefix: got %q, want %q", entry.Prefix, tt.wantPfx)
				}
			}
		})
	}
}

func TestLoadServices(t *testing.T) {
	content := `# comment line
POSTGRES:QUIZZER

POSTGRES:AUTH
# another comment
POSTGRES:LOG_TO
`
	groups, err := LoadServices(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pg := groups[Postgres]
	if len(pg) != 3 {
		t.Fatalf("expected 3 postgres entries, got %d", len(pg))
	}

	want := []string{"QUIZZER", "AUTH", "LOG_TO"}
	for i, w := range want {
		if pg[i].Prefix != w {
			t.Errorf("entry %d: got prefix %q, want %q", i, pg[i].Prefix, w)
		}
	}
}

func TestLoadServicesEmpty(t *testing.T) {
	content := "# only comments\n\n# nothing else\n"
	groups, err := LoadServices(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("expected 0 groups, got %d", len(groups))
	}
}

func TestLoadServicesInvalidLine(t *testing.T) {
	content := "POSTGRES:QUIZZER\nINVALID_LINE\n"
	_, err := LoadServices(content)
	if err == nil {
		t.Fatal("expected error for invalid line, got nil")
	}
}

func TestBuildEnvVarName(t *testing.T) {
	got := BuildEnvVarName("QUIZZER", Postgres, "URL")
	want := "QUIZZER_POSTGRES_URL"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRailwayRefURL(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		want        string
	}{
		{"no spaces", "DB-Provisioner", `${{ DB-Provisioner.QUIZZER_POSTGRES_URL }}`},
		{"with spaces", "DB Provisioner", `${{ "DB Provisioner".QUIZZER_POSTGRES_URL }}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildRailwayRefURL(Postgres, "QUIZZER", tt.serviceName)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseConnURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantU   string
		wantP   string
		wantDB  string
		wantErr bool
	}{
		{"standard", "postgresql://user:pass@host:5432/dbname", "user", "pass", "dbname", false},
		{"no password", "postgresql://user@host:5432/dbname", "", "", "", true},
		{"no user", "postgresql://:pass@host:5432/dbname", "", "", "", true},
		{"no db name", "postgresql://user:pass@host:5432/", "user", "pass", "", true},
		{"invalid url", "://broken", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, p, db, err := parseConnURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseConnURL(%q): err = %v, wantErr = %v", tt.url, err, tt.wantErr)
			}
			if !tt.wantErr {
				if u != tt.wantU || p != tt.wantP || db != tt.wantDB {
					t.Errorf("got (user=%q, pass=%q, db=%q), want (%q, %q, %q)",
						u, p, db, tt.wantU, tt.wantP, tt.wantDB)
				}
			}
		})
	}
}
