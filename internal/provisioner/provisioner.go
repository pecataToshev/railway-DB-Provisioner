package provisioner

import (
	"fmt"

	"github.com/pecataToshev/railway-DB-Provisioner/internal/config"
)

// Provisioner defines the interface for database provisioning.
type Provisioner interface {
	// SecureInstance applies instance-wide hardening (idempotent),
	// run once per instance before provisioning services.
	SecureInstance() error
	// Provision creates or updates the database and user for a service.
	Provision(cfg config.ServiceConfig) error
	// BuildRailwayURL returns a Railway variable reference URL.
	BuildRailwayURL(cfg config.ServiceConfig, serviceName string) string
	// Close closes the database connection.
	Close() error
}

// New creates a Provisioner for the given DB type and connects to the database.
func New(dbType config.DBType, superURL string) (Provisioner, error) {
	switch dbType {
	case config.Postgres:
		return newPostgresProvisioner(superURL)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}
}
