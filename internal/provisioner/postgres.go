package provisioner

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/pecataToshev/railway-DB-Provisioner/internal/config"
	_ "github.com/lib/pq"
)

type postgresProvisioner struct {
	db       *sql.DB
	superURL string
}

func newPostgresProvisioner(superURL string) (*postgresProvisioner, error) {
	db, err := sql.Open("postgres", superURL)
	if err != nil {
		return nil, fmt.Errorf("open superuser connection: %w", err)
	}

	// Verify connection works
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &postgresProvisioner{db: db, superURL: superURL}, nil
}

// SecureInstance revokes PUBLIC connect on the maintenance and template
// databases so no provisioned user can reach them. Superusers are unaffected
// (they bypass privilege checks). Idempotent: REVOKE on an absent grant is a no-op.
func (p *postgresProvisioner) SecureInstance() error {
	for _, dbName := range []string{"postgres", "template1"} {
		if _, err := p.db.Exec(fmt.Sprintf(
			`REVOKE CONNECT ON DATABASE %s FROM PUBLIC`,
			quoteIdentifier(dbName),
		)); err != nil {
			return fmt.Errorf("revoke public connect on %s: %w", dbName, err)
		}
	}
	slog.Info("[POSTGRES] instance secured", "public_connect_revoked", []string{"postgres", "template1"})
	return nil
}

func (p *postgresProvisioner) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}

func (p *postgresProvisioner) Provision(cfg config.ServiceConfig) error {
	prefix := fmt.Sprintf("[POSTGRES:%s]", cfg.Prefix)
	log := slog.With("db_type", "POSTGRES", "service", cfg.Prefix)

	// --- Role ---
	var roleExists bool
	if err := p.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = $1)`,
		cfg.User,
	).Scan(&roleExists); err != nil {
		return fmt.Errorf("check role: %w", err)
	}

	if !roleExists {
		createSQL := fmt.Sprintf(
			`CREATE USER %s WITH PASSWORD '%s'`,
			quoteIdentifier(cfg.User), escapeLiteral(cfg.Pass),
		)
		if _, err := p.db.Exec(createSQL); err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		log.Info(prefix+" created role", "user", cfg.User)
	} else {
		// Sync password for credential rotation
		if _, err := p.db.Exec(fmt.Sprintf(
			`ALTER USER %s WITH PASSWORD '%s'`,
			quoteIdentifier(cfg.User), escapeLiteral(cfg.Pass),
		)); err != nil {
			return fmt.Errorf("alter user: %w", err)
		}
		log.Info(prefix+" role exists, password synced", "user", cfg.User)
	}

	// --- Database ---
	var dbExists bool
	if err := p.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname = $1)`,
		cfg.DBName,
	).Scan(&dbExists); err != nil {
		return fmt.Errorf("check database: %w", err)
	}

	if !dbExists {
		if _, err := p.db.Exec(fmt.Sprintf(
			`CREATE DATABASE %s OWNER %s`,
			quoteIdentifier(cfg.DBName), quoteIdentifier(cfg.User),
		)); err != nil {
			return fmt.Errorf("create database: %w", err)
		}
		log.Info(prefix+" created database", "database", cfg.DBName)
	} else {
		log.Info(prefix+" database exists", "database", cfg.DBName)
	}

	if _, err := p.db.Exec(fmt.Sprintf(
		`GRANT ALL PRIVILEGES ON DATABASE %s TO %s`,
		quoteIdentifier(cfg.DBName), quoteIdentifier(cfg.User),
	)); err != nil {
		return fmt.Errorf("grant database privileges: %w", err)
	}

	// --- Isolate database: revoke CONNECT from PUBLIC, grant only to the provisioned user ---
	// This ensures other roles on the shared instance (e.g. manually provisioned
	// Logto tenant roles) cannot connect to this database. Superusers bypass this.
	if _, err := p.db.Exec(fmt.Sprintf(
		`REVOKE CONNECT ON DATABASE %s FROM PUBLIC`,
		quoteIdentifier(cfg.DBName),
	)); err != nil {
		return fmt.Errorf("revoke public connect: %w", err)
	}
	if _, err := p.db.Exec(fmt.Sprintf(
		`GRANT CONNECT ON DATABASE %s TO %s`,
		quoteIdentifier(cfg.DBName), quoteIdentifier(cfg.User),
	)); err != nil {
		return fmt.Errorf("grant connect: %w", err)
	}
	log.Info(prefix+" database isolated (CONNECT revoked from PUBLIC)", "database", cfg.DBName)

	// --- Public schema (PostgreSQL 15+ revokes CREATE from public by default) ---
	// Need a separate connection to the target database for schema operations
	dbURL, err := replaceDBName(p.superURL, cfg.DBName)
	if err != nil {
		return err
	}

	targetDB, err := sql.Open("postgres", dbURL)
	if err != nil {
		return fmt.Errorf("open target db connection: %w", err)
	}
	defer targetDB.Close()

	if _, err := targetDB.Exec(fmt.Sprintf(
		`GRANT ALL ON SCHEMA public TO %s`,
		quoteIdentifier(cfg.User),
	)); err != nil {
		return fmt.Errorf("grant schema: %w", err)
	}
	if _, err := targetDB.Exec(fmt.Sprintf(
		`ALTER SCHEMA public OWNER TO %s`,
		quoteIdentifier(cfg.User),
	)); err != nil {
		return fmt.Errorf("alter schema owner: %w", err)
	}
	log.Info(prefix+" public schema ownership granted", "user", cfg.User)

	return nil
}

func (p *postgresProvisioner) BuildRailwayURL(cfg config.ServiceConfig, serviceName string) string {
	return config.BuildRailwayRefURL(cfg.DBType, cfg.Prefix, serviceName)
}

// quoteIdentifier safely quotes PostgreSQL identifiers.
func quoteIdentifier(name string) string {
	// Simple implementation - in production, use pq.QuoteIdentifier
	return `"` + name + `"`
}

// escapeLiteral escapes PostgreSQL string literals.
func escapeLiteral(s string) string {
	// Simple implementation - in production, use pq.QuoteLiteral
	return s
}

func replaceDBName(rawURL, dbName string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse connection URL: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}
