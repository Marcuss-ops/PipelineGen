// Package storage fornisce interfacce e implementazioni per la persistenza dei dati
package storage

// StorageType rappresenta il backend primario di persistenza.
type StorageType string

const (
	// StorageTypeJSON utilizza file JSON per la persistenza legacy / dev.
	StorageTypeJSON StorageType = "json"
	// StorageTypePostgres utilizza PostgreSQL come persistence primaria.
	StorageTypePostgres StorageType = "postgres"
)

// CacheType rappresenta il backend secondario di cache locale.
type CacheType string

const (
	// CacheTypeNone disabilita la cache locale.
	CacheTypeNone CacheType = "none"
	// CacheTypeSQLite usa SQLite solo come cache locale/materialized state.
	CacheTypeSQLite CacheType = "sqlite"
)

// Config contiene la configurazione per lo storage.
type Config struct {
	Type            StorageType
	DataDir         string
	EnableCache     bool
	CacheType       CacheType
	CacheMaxSize    int
	PostgresDSN     string
	MigrationsTable string
}

// DefaultConfig restituisce una configurazione di default.
// Legacy-safe: JSON resta il default finché il bootstrap non viene migrato.
func DefaultConfig() *Config {
	return &Config{
		Type:            StorageTypeJSON,
		DataDir:         "./data",
		EnableCache:     true,
		CacheType:       CacheTypeSQLite,
		CacheMaxSize:    1000,
		PostgresDSN:     "",
		MigrationsTable: "schema_migrations",
	}
}
