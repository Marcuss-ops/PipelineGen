// Package storage fornisce interfacce e implementazioni per la persistenza dei dati
package storage

// StorageType rappresenta il tipo di storage da utilizzare
type StorageType string

const (
	// StorageTypeJSON utilizza file JSON per la persistenza
	StorageTypeJSON StorageType = "json"
)

// Config contiene la configurazione per lo storage
type Config struct {
	Type         StorageType
	DataDir      string
	EnableCache  bool
	CacheMaxSize int
}

// DefaultConfig restituisce una configurazione di default
func DefaultConfig() *Config {
	return &Config{
		Type:         StorageTypeJSON,
		DataDir:      "./data",
		EnableCache:  true,
		CacheMaxSize: 1000,
	}
}