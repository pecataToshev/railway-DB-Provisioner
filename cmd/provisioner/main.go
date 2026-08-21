package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/pecataToshev/railway-DB-Provisioner/internal/buildinfo"
	"github.com/pecataToshev/railway-DB-Provisioner/internal/config"
	"github.com/pecataToshev/railway-DB-Provisioner/internal/provisioner"
)

// prefixLogger wraps a logger to prepend [DB_TYPE:SERVICE] to messages.
type prefixLogger struct {
	logger *slog.Logger
	prefix string
}

func (p *prefixLogger) Info(msg string, args ...any) {
	p.logger.Info(p.prefix+" "+msg, args...)
}

func (p *prefixLogger) Error(msg string, args ...any) {
	p.logger.Error(p.prefix+" "+msg, args...)
}

// prefixedLogger returns a logger with [DB_TYPE:SERVICE] prefix in the message.
func prefixedLogger(dbType config.DBType, service string) *prefixLogger {
	prefix := fmt.Sprintf("[%s:%s]", dbType, service)
	return &prefixLogger{
		logger: slog.With("db_type", dbType, "service", service),
		prefix: prefix,
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))

	// Read services.txt
	servicesPath := os.Getenv("SERVICES_FILE")
	if servicesPath == "" {
		servicesPath = "services.txt"
	}

	slog.Info("db-provisioner starting",
		"commit", buildinfo.Commit,
		"build_time", buildinfo.BuildTime,
		"source", buildinfo.Source,
		"servicesPath", servicesPath)

	content, err := os.ReadFile(servicesPath)
	if err != nil {
		slog.Error("failed to read services file", "path", servicesPath, "error", err)
		os.Exit(1)
	}

	// Parse services.txt
	groups, err := config.LoadServices(string(content))
	if err != nil {
		slog.Error("failed to load services", "error", err)
		os.Exit(1)
	}

	if len(groups) == 0 {
		slog.Error("no services defined in services.txt")
		os.Exit(1)
	}

	serviceName := os.Getenv("RAILWAY_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "DB Provisioner"
	}

	// Print all Railway reference URLs first
	printAllRailwayURLs(groups, serviceName)

	// Resolve all environment variables
	configs, err := config.ResolveAllConfigs(groups)
	if err != nil {
		slog.Error("missing environment variables", "error", err)
		os.Exit(1)
	}

	slog.Info("starting provisioning", "total_services", countConfigs(configs))

	// Provision each database type
	for dbType, cfgs := range configs {
		connVar := config.GetConnectionEnvVar(dbType)
		superURL := os.Getenv(connVar)
		if superURL == "" {
			slog.Error("connection URL not set", "env_var", connVar, "db_type", dbType)
			os.Exit(1)
		}

		// Create provisioner with internal connection management
		prov, err := provisioner.New(dbType, superURL)
		if err != nil {
			slog.Error("failed to create provisioner", "db_type", dbType, "error", err)
			os.Exit(1)
		}

		// Instance-wide hardening, applied on every run (idempotent)
		if err := prov.SecureInstance(); err != nil {
			prov.Close()
			slog.Error("failed to secure instance", "db_type", dbType, "error", err)
			os.Exit(1)
		}

		for _, cfg := range cfgs {
			// Allow logs to be grouped per service
			time.Sleep(10 * time.Millisecond)

			log := prefixedLogger(dbType, cfg.Prefix)
			log.Info("provisioning service",
				"database", cfg.DBName,
				"user", cfg.User)

			if err := prov.Provision(cfg); err != nil {
				prov.Close()
				log.Error("provisioning failed", "error", err)
				os.Exit(1)
			}

			// Print the Railway variable reference URL for this service
			time.Sleep(2 * time.Millisecond)
			railwayURL := prov.BuildRailwayURL(cfg, serviceName)
			log.Info("service provisioned", "railway_url", railwayURL)
		}

		prov.Close()
	}

	slog.Info("all services provisioned successfully")
}

func printAllRailwayURLs(groups map[config.DBType][]config.ServiceEntry, serviceName string) {
	for dbType, entries := range groups {
		slog.Info(string(dbType) + " URLs using Railway variable references")

		for _, entry := range entries {
			log := prefixedLogger(dbType, entry.Prefix)
			ref := config.BuildRailwayRefURL(dbType, entry.Prefix, serviceName)
			log.Info("  " + entry.Prefix + " -> " + ref)
		}
	}
}

func countConfigs(configs map[config.DBType][]config.ServiceConfig) int {
	count := 0
	for _, cfgs := range configs {
		count += len(cfgs)
	}
	return count
}
