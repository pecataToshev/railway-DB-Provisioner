package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// DBType represents the type of database.
type DBType string

const (
	Postgres DBType = "POSTGRES"
)

// ValidDBTypes is the list of valid database types.
var ValidDBTypes = []DBType{Postgres}

// IsValid checks if the DBType is valid.
func (d DBType) IsValid() bool {
	switch d {
	case Postgres:
		return true
	}
	return false
}

// ServiceEntry is a parsed entry from services.txt before env var resolution.
type ServiceEntry struct {
	Prefix string
}

// ServiceConfig holds the configuration for a single service.
// The connection string is parsed into User/Pass/DBName so provisioners can
// create the role/database; the original URL is preserved for reference.
type ServiceConfig struct {
	DBType DBType
	Prefix string
	URL    string
	DBName string
	User   string
	Pass   string
}

// ParseServiceEntry parses a line like "POSTGRES:QUIZZER"
// into (POSTGRES, ServiceEntry{Prefix: QUIZZER}).
// Extra role attributes (e.g. CREATEROLE) are intentionally not supported:
// services that need them must be provisioned manually outside this tool.
func ParseServiceEntry(line string) (DBType, ServiceEntry, error) {
	parts := strings.Split(line, ":")
	if len(parts) != 2 {
		return "", ServiceEntry{}, fmt.Errorf("invalid format: %q (expected DBTYPE:SERVICE)", line)
	}

	dbType := DBType(strings.ToUpper(strings.TrimSpace(parts[0])))
	prefix := strings.ToUpper(strings.TrimSpace(parts[1]))

	if !dbType.IsValid() {
		return "", ServiceEntry{}, fmt.Errorf("invalid DB type: %q (expected POSTGRES)", dbType)
	}

	if prefix == "" {
		return "", ServiceEntry{}, fmt.Errorf("empty service prefix")
	}

	return dbType, ServiceEntry{Prefix: prefix}, nil
}

// LoadServices parses services.txt content and groups entries by DB type.
func LoadServices(content string) (map[DBType][]ServiceEntry, error) {
	groups := make(map[DBType][]ServiceEntry)
	var errors []string

	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		dbType, entry, err := ParseServiceEntry(line)
		if err != nil {
			errors = append(errors, fmt.Sprintf("line %d: %v", lineNum, err))
			continue
		}

		groups[dbType] = append(groups[dbType], entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading services: %w", err)
	}

	if len(errors) > 0 {
		return nil, fmt.Errorf("parsing errors:\n  %s", strings.Join(errors, "\n  "))
	}

	return groups, nil
}

// BuildEnvVarName constructs the environment variable name.
// Example: QUIZZER, POSTGRES, URL -> QUIZZER_POSTGRES_URL
func BuildEnvVarName(prefix string, dbType DBType, suffix string) string {
	return fmt.Sprintf("%s_%s_%s", prefix, dbType, suffix)
}

// ResolveServiceConfig resolves the connection-string environment variable for
// a service and parses it into its components.
//
// The single env var <PREFIX>_<DBTYPE>_URL holds a full connection string, e.g.
//
//	postgresql://quizzer_user:pass@postgres.railway.internal:5432/quizzer_db
//
// The user, password and database name are extracted so the provisioner can
// create the role/database; the host is taken from the superuser connection
// (POSTGRES_URL) at provisioning time, not from this var.
func ResolveServiceConfig(dbType DBType, entry ServiceEntry) (*ServiceConfig, error) {
	urlVar := BuildEnvVarName(entry.Prefix, dbType, "URL")
	rawURL := os.Getenv(urlVar)
	if rawURL == "" {
		return nil, fmt.Errorf("missing environment variable: %s", urlVar)
	}

	user, pass, dbName, err := parseConnURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", urlVar, err)
	}

	return &ServiceConfig{
		DBType: dbType,
		Prefix: entry.Prefix,
		URL:    rawURL,
		DBName: dbName,
		User:   user,
		Pass:   pass,
	}, nil
}

// parseConnURL extracts the username, password and database name from a
// postgresql connection URL.
func parseConnURL(raw string) (user, pass, dbName string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", fmt.Errorf("parse connection URL: %w", err)
	}
	if u.User == nil {
		return "", "", "", fmt.Errorf("connection URL missing user info")
	}
	user = u.User.Username()
	pass, _ = u.User.Password()
	if user == "" || pass == "" {
		return "", "", "", fmt.Errorf("connection URL missing user or password")
	}
	dbName = strings.TrimPrefix(u.Path, "/")
	if dbName == "" {
		return "", "", "", fmt.Errorf("connection URL missing database name")
	}
	return user, pass, dbName, nil
}

// ResolveAllConfigs resolves all service configs grouped by DB type.
func ResolveAllConfigs(groups map[DBType][]ServiceEntry) (map[DBType][]ServiceConfig, error) {
	result := make(map[DBType][]ServiceConfig)
	var allMissing []string

	for dbType, entries := range groups {
		for _, entry := range entries {
			cfg, err := ResolveServiceConfig(dbType, entry)
			if err != nil {
				allMissing = append(allMissing, err.Error())
				continue
			}
			result[dbType] = append(result[dbType], *cfg)
		}
	}

	if len(allMissing) > 0 {
		return nil, fmt.Errorf("missing configuration:\n  %s", strings.Join(allMissing, "\n  "))
	}

	return result, nil
}

// GetConnectionEnvVar returns the connection URL env var name for a DB type.
func GetConnectionEnvVar(dbType DBType) string {
	switch dbType {
	case Postgres:
		return "POSTGRES_URL"
	default:
		return ""
	}
}

// BuildRailwayRefURL returns the Railway variable reference for a service's
// connection-string variable, e.g. ${{ "DB Provisioner".QUIZZER_POSTGRES_URL }}.
// The consuming service can paste this directly as its DATABASE_URL.
func BuildRailwayRefURL(dbType DBType, prefix, serviceName string) string {
	urlVar := BuildEnvVarName(prefix, dbType, "URL")
	return fmt.Sprintf(`${{ %s.%s }}`, railwayServiceRef(serviceName), urlVar)
}

// railwayServiceRef quotes the Railway service name when it contains whitespace.
func railwayServiceRef(serviceName string) string {
	if strings.ContainsAny(serviceName, " \t") {
		return fmt.Sprintf(`"%s"`, serviceName)
	}
	return serviceName
}
