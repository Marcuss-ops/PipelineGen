package config

import "testing"

func TestPostgreSQLMediaConfigValidateDisabledAllowsEmptyDSN(t *testing.T) {
	if err := (PostgreSQLMediaConfig{}).Validate(); err != nil {
		t.Fatalf("disabled PostgreSQL config should be accepted: %v", err)
	}
}

func TestPostgreSQLMediaConfigValidateEnabledRequiresDSN(t *testing.T) {
	cfg := PostgreSQLMediaConfig{Enabled: true}
	if err := cfg.Validate(); err == nil {
		t.Fatal("enabled PostgreSQL config must reject an empty DSN")
	}
}

func TestPostgreSQLMediaConfigValidateRejectsInvalidPool(t *testing.T) {
	cases := []PostgreSQLMediaConfig{
		{Enabled: true, DSN: "postgres://localhost/media", MaxOpenConnections: 0, MaxIdleConnections: 0},
		{Enabled: true, DSN: "postgres://localhost/media", MaxOpenConnections: 2, MaxIdleConnections: 3},
		{Enabled: true, DSN: "postgres://localhost/media", MaxOpenConnections: 2, MaxIdleConnections: 1, ConnMaxLifetimeSeconds: -1},
	}
	for i, cfg := range cases {
		if err := cfg.Validate(); err == nil {
			t.Errorf("case %d should be rejected", i)
		}
	}
}
