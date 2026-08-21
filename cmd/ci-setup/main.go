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
	"net/url"
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

	hostRef := fmt.Sprintf("${{%s.PGHOST}}", postgresServiceName)
	portRef := fmt.Sprintf("${{%s.PGPORT}}", postgresServiceName)

	// Fetch the current Postgres host:port from the Postgres service itself.
	// Railway resolves references when returning variables, so we compare
	// the resolved host:port in existing *_POSTGRES_URL against the current
	// values to detect stale hardcoded hosts (e.g. from v1.3).
	pgVars, err := client.GetVariables(postgresServiceName)
	if err != nil {
		slog.Error("failed to fetch Postgres service variables", "service", postgresServiceName, "error", err)
		os.Exit(1)
	}
	pgHost := pgVars["PGHOST"]
	pgPort := pgVars["PGPORT"]
	if pgHost == "" || pgPort == "" {
		slog.Error("PGHOST or PGPORT not found in Postgres service variables", "service", postgresServiceName)
		os.Exit(1)
	}
	expectedHostPort := pgHost + ":" + pgPort
	slog.Info("resolved Postgres host:port", "host_port", expectedHostPort)

	set := 0
	skipped := 0
	updated := 0

	for dbType, entries := range groups {
		for _, entry := range entries {
			urlVar := config.BuildEnvVarName(entry.Prefix, dbType, "URL")

			if existingVal, ok := existing[urlVar]; ok && existingVal != "" {
				currentHostPort, err := extractHostPort(existingVal)
				if err != nil {
					slog.Warn("existing variable has unparseable URL, regenerating", "var", urlVar, "error", err)
				} else if currentHostPort == expectedHostPort {
					slog.Info("variable already set, host:port current, skipping", "var", urlVar)
					skipped++
					continue
				} else {
					slog.Info("variable has stale host:port, updating", "var", urlVar, "current", currentHostPort, "expected", expectedHostPort)
				}
			}

			prefixLower := strings.ToLower(entry.Prefix)
			dbName := prefixLower + "_db"
			dbUser := prefixLower + "_user"
			dbPass, err := generatePassword(passwordLength)
			if err != nil {
				slog.Error("failed to generate password", "var", urlVar, "error", err)
				os.Exit(1)
			}

			connURL := fmt.Sprintf("postgresql://%s:%s@%s:%s/%s",
				dbUser, dbPass, hostRef, portRef, dbName)

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

// extractHostPort parses a postgresql connection URL and returns "host:port".
func extractHostPort(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL has no host")
	}
	return u.Host, nil
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
