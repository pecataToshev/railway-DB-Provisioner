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

	// Derive host:port from the db-provisioner's own POSTGRES_URL.
	postgresURL, ok := existing["POSTGRES_URL"]
	if !ok || postgresURL == "" {
		slog.Error("POSTGRES_URL not found in service variables", "service", serviceName)
		os.Exit(1)
	}

	host, err := extractHost(postgresURL)
	if err != nil {
		slog.Error("failed to extract host from POSTGRES_URL", "error", err)
		os.Exit(1)
	}

	slog.Info("derived database host", "host", host)

	set := 0
	skipped := 0

	for dbType, entries := range groups {
		for _, entry := range entries {
			urlVar := config.BuildEnvVarName(entry.Prefix, dbType, "URL")

			if v, ok := existing[urlVar]; ok && v != "" {
				slog.Info("variable already set, skipping", "var", urlVar)
				skipped++
				continue
			}

			prefixLower := strings.ToLower(entry.Prefix)
			dbName := prefixLower + "_db"
			dbUser := prefixLower + "_user"
			dbPass, err := generatePassword(passwordLength)
			if err != nil {
				slog.Error("failed to generate password", "var", urlVar, "error", err)
				os.Exit(1)
			}

			connURL := fmt.Sprintf("postgresql://%s:%s@%s/%s", dbUser, dbPass, host, dbName)

			slog.Info("setting variable", "var", urlVar, "value", "<redacted>")
			if err := client.SetVariable(serviceName, urlVar, connURL); err != nil {
				slog.Error("failed to set variable", "var", urlVar, "error", err)
				os.Exit(1)
			}
			set++
		}
	}

	slog.Info("ci-setup complete", "set", set, "skipped", skipped)
}

// extractHost parses a postgresql connection URL and returns "host:port".
func extractHost(rawURL string) (string, error) {
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
