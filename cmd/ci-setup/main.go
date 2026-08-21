// ci-setup is the CI-time binary that ensures per-service connection-string
// environment variables exist on the db-provisioner Railway service.
//
// It replaces the old provision-db-vars.sh shell script. All logic (parsing,
// password generation, URL building, idempotency) is in Go; the Railway CLI
// is used only for the actual API calls (fetch/set variables, deploy).
//
// Usage:
//
//	ci-setup
//
// Environment:
//
//	RAILWAY_TOKEN        - Railway API token (required)
//	RAILWAY_SERVICE_NAME - name of the db-provisioner Railway service (required)
//	SERVICES_FILE        - path to services.txt (default: services.txt)
//
// The following variables are read from the Railway service (not the CI env):
//
//	POSTGRES_SERVICE_NAME - name of the Postgres plugin/service in Railway,
//	                       used to build Railway variable references for host:port
package main

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/pecataToshev/railway-DB-Provisioner/internal/buildinfo"
	"github.com/pecataToshev/railway-DB-Provisioner/internal/config"
	"github.com/pecataToshev/railway-DB-Provisioner/internal/railway"
)

const passwordLength = 64

func main() {
	// Text handler for CI — human-readable in pipeline console output.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	servicesPath := os.Getenv("SERVICES_FILE")
	if servicesPath == "" {
		servicesPath = "services.txt"
	}

	slog.Info("ci-setup starting",
		"commit", buildinfo.Commit,
		"build_time", buildinfo.BuildTime,
		"source", buildinfo.Source,
		"servicesPath", servicesPath)

	token := os.Getenv("RAILWAY_TOKEN")
	if token == "" {
		slog.Error("RAILWAY_TOKEN not set")
		os.Exit(1)
	}

	serviceName := os.Getenv("RAILWAY_SERVICE_NAME")
	if serviceName == "" {
		slog.Error("RAILWAY_SERVICE_NAME not set")
		os.Exit(1)
	}

	content, err := os.ReadFile(servicesPath)
	if err != nil {
		slog.Error("failed to read services file", "path", servicesPath, "error", err)
		os.Exit(1)
	}

	groups, err := config.LoadServices(string(content))
	if err != nil {
		slog.Error("failed to parse services", "error", err)
		os.Exit(1)
	}

	if len(groups) == 0 {
		slog.Error("no services defined in services.txt")
		os.Exit(1)
	}

	client := railway.NewClient(token)

	// Fetch existing variables so we only set what's missing.
	existing, err := client.GetVariables(serviceName)
	if err != nil {
		slog.Error("failed to fetch existing variables", "service", serviceName, "error", err)
		os.Exit(1)
	}

	// Read the Postgres service name from the Railway service variables.
	// It's used to build Railway variable references for host:port, e.g.
	//   ${{Postgres-18.PGHOST}}  ${{Postgres-18.PGPORT}}
	// Using references means the host is always current — Railway resolves
	// them at runtime, and references follow service renames automatically.
	postgresServiceName, ok := existing["POSTGRES_SERVICE_NAME"]
	if !ok || postgresServiceName == "" {
		slog.Error("POSTGRES_SERVICE_NAME not found in service variables", "service", serviceName)
		os.Exit(1)
	}

	set := 0
	skipped := 0
	updated := 0

	for dbType, entries := range groups {
		for _, entry := range entries {
			urlVar := config.BuildEnvVarName(entry.Prefix, dbType, "URL")

			if existingVal, ok := existing[urlVar]; ok && existingVal != "" {
				if hasCurrentHostRef(existingVal, postgresServiceName) {
					slog.Info("variable already uses current Railway references, skipping", "var", urlVar)
					skipped++
					continue
				}
				slog.Info("variable needs update, regenerating with Railway references", "var", urlVar)
			}

			prefixLower := strings.ToLower(entry.Prefix)
			dbName := prefixLower + "_db"
			dbUser := prefixLower + "_user"
			dbPass, err := generatePassword(passwordLength)
			if err != nil {
				slog.Error("failed to generate password", "var", urlVar, "error", err)
				os.Exit(1)
			}

			connURL := buildConnURL(dbUser, dbPass, dbName, postgresServiceName)

			slog.Info("setting variable", "var", urlVar, "value", "<redacted>")
			if err := client.SetVariable(serviceName, urlVar, connURL); err != nil {
				slog.Error("failed to set variable", "var", urlVar, "error", err)
				os.Exit(1)
			}
			if _, ok := existing[urlVar]; ok {
				updated++
			} else {
				set++
			}
		}
	}

	slog.Info("ci-setup complete", "set", set, "updated", updated, "skipped", skipped)
}

// hasCurrentHostRef checks whether a connection URL already contains the
// expected Railway host:port reference for the given Postgres service name,
// e.g. "@${{Postgres-18.PGHOST}}:${{Postgres-18.PGPORT}}". This ensures we
// skip only variables that are both reference-based AND use the current
// service name for both host and port. Hardcoded hosts (from v1.3), stale
// service names, or partial references won't match and will be regenerated.
func hasCurrentHostRef(connURL, postgresServiceName string) bool {
	expected := fmt.Sprintf("@${{%s.PGHOST}}:${{%s.PGPORT}}", postgresServiceName, postgresServiceName)
	return strings.Contains(connURL, expected)
}

// buildConnURL builds a PostgreSQL connection URL using Railway variable
// references for host and port, so the host is always resolved at runtime.
func buildConnURL(user, pass, dbName, postgresServiceName string) string {
	hostRef := fmt.Sprintf("${{%s.PGHOST}}", postgresServiceName)
	portRef := fmt.Sprintf("${{%s.PGPORT}}", postgresServiceName)
	return fmt.Sprintf("postgresql://%s:%s@%s:%s/%s", user, pass, hostRef, portRef, dbName)
}

// generatePassword returns a cryptographically secure alphanumeric string.
// Alphanumeric only: never needs quoting/escaping in SQL or URLs.
// Uses rejection sampling to eliminate modulo bias.
func generatePassword(length int) (string, error) {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	const charsLen = byte(len(chars))
	// max is the largest multiple of charsLen that fits in a byte.
	// Values >= max are rejected and re-rolled to avoid modulo bias.
	const max = byte(256 - (256 % int(charsLen)))

	buf := make([]byte, length)
	for i := 0; i < length; {
		var randBuf [1]byte
		if _, err := rand.Read(randBuf[:]); err != nil {
			return "", err
		}
		if randBuf[0] >= max {
			continue
		}
		buf[i] = chars[randBuf[0]%charsLen]
		i++
	}
	return string(buf), nil
}
