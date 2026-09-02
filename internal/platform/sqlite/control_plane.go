// Package storage — control_plane.go: durable control-plane identity and
// single-writer topology validation. Split out of set.go (2026-09-02) to
// keep the DatabaseSet entry-point surface under the strict 600-LOC cap.
//
// The runtime enforces exactly one writable CANONICAL control-plane database.
// This file owns the metadata row contract (control_plane_meta) and the
// configured-writer policy consumed by OpenSet and
// ValidateControlPlaneIdentity.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ControlPlaneRole identifies how a configured database participates in the
// control-plane topology. Only CANONICAL may be an operational writer.
type ControlPlaneRole string

const (
	ControlPlaneRoleCanonical       ControlPlaneRole = "CANONICAL"
	ControlPlaneRoleReadOnly        ControlPlaneRole = "READ_ONLY"
	ControlPlaneRoleMigrationSource ControlPlaneRole = "MIGRATION_SOURCE"
	ControlPlaneRoleArchive         ControlPlaneRole = "ARCHIVE"
)

const canonicalControlPlaneSchemaFamily = "pipelinegen-control-plane"

// ControlPlaneMeta is the durable identity stored in control_plane_meta.
type ControlPlaneMeta struct {
	DatabaseID       string
	SchemaFamily     string
	InstanceRole     ControlPlaneRole
	CanonicalVersion int
	CreatedAt        string
}

// ConfiguredDatabase describes a database known to the application when it
// evaluates the single-writer invariant. It deliberately contains topology
// metadata rather than a storage implementation so the policy is testable
// without opening a second SQLite handle.
type ConfiguredDatabase struct {
	Name         string
	Path         string
	Role         ControlPlaneRole
	Writable     bool
	ControlPlane bool
}

// ReadControlPlaneMeta reads and validates the singleton control-plane
// identity row. Missing, duplicated, or malformed metadata is a hard error.
func ReadControlPlaneMeta(ctx context.Context, db *sql.DB) (ControlPlaneMeta, error) {
	if db == nil {
		return ControlPlaneMeta{}, errors.New("control plane identity: nil database")
	}

	rows, err := db.QueryContext(ctx, `
		SELECT database_id, schema_family, instance_role, canonical_version, created_at
		FROM control_plane_meta`)
	if err != nil {
		return ControlPlaneMeta{}, fmt.Errorf("control plane identity: read metadata: %w", err)
	}
	defer rows.Close()

	var meta ControlPlaneMeta
	count := 0
	for rows.Next() {
		count++
		if count > 1 {
			return ControlPlaneMeta{}, errors.New("control plane identity: control_plane_meta must contain exactly one row")
		}
		if err := rows.Scan(&meta.DatabaseID, &meta.SchemaFamily, &meta.InstanceRole, &meta.CanonicalVersion, &meta.CreatedAt); err != nil {
			return ControlPlaneMeta{}, fmt.Errorf("control plane identity: scan metadata: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return ControlPlaneMeta{}, fmt.Errorf("control plane identity: iterate metadata: %w", err)
	}
	if count != 1 {
		return ControlPlaneMeta{}, fmt.Errorf("control plane identity: control_plane_meta rows=%d, want exactly 1", count)
	}
	if err := validateControlPlaneMeta(meta); err != nil {
		return ControlPlaneMeta{}, err
	}
	return meta, nil
}

func validateControlPlaneMeta(meta ControlPlaneMeta) error {
	if strings.TrimSpace(meta.DatabaseID) == "" {
		return errors.New("control plane identity: database_id is empty")
	}
	if meta.SchemaFamily != canonicalControlPlaneSchemaFamily {
		return fmt.Errorf("control plane identity: schema_family=%q, want %q", meta.SchemaFamily, canonicalControlPlaneSchemaFamily)
	}
	switch meta.InstanceRole {
	case ControlPlaneRoleCanonical, ControlPlaneRoleReadOnly, ControlPlaneRoleMigrationSource, ControlPlaneRoleArchive:
	default:
		return fmt.Errorf("control plane identity: unknown instance_role=%q", meta.InstanceRole)
	}
	if meta.CanonicalVersion <= 0 {
		return fmt.Errorf("control plane identity: canonical_version=%d, want positive version", meta.CanonicalVersion)
	}
	if strings.TrimSpace(meta.CreatedAt) == "" {
		return errors.New("control plane identity: created_at is empty")
	}
	return nil
}

// ValidateConfiguredControlPlaneWriters enforces exactly one writable
// CANONICAL control-plane database. It also rejects aliases of one physical
// SQLite file being configured as multiple writable databases.
func ValidateConfiguredControlPlaneWriters(databases []ConfiguredDatabase) error {
	if len(databases) == 0 {
		return errors.New("control plane identity: no configured databases")
	}

	for _, database := range databases {
		if strings.TrimSpace(database.Name) == "" {
			return errors.New("control plane identity: configured database has empty name")
		}
	}

	// Check physical aliases first so a same-file collision is diagnosed even
	// when the role declarations are also inconsistent.
	for i := range databases {
		if !databases[i].ControlPlane || !databases[i].Writable {
			continue
		}
		for j := i + 1; j < len(databases); j++ {
			if !databases[j].ControlPlane || !databases[j].Writable || !samePhysicalFile(databases[i].Path, databases[j].Path) {
				continue
			}
			return fmt.Errorf("multiple control-plane writers detected: writable databases %q and %q resolve to the same SQLite file", databases[i].Name, databases[j].Name)
		}
	}

	canonicalWriters := make([]string, 0, len(databases))
	for _, database := range databases {
		if database.ControlPlane && database.Writable {
			if database.Role != ControlPlaneRoleCanonical {
				return fmt.Errorf("control plane identity: writable Control Plane database %q has role %q, want %q", database.Name, database.Role, ControlPlaneRoleCanonical)
			}
			canonicalWriters = append(canonicalWriters, database.Name)
		}
	}
	if len(canonicalWriters) != 1 {
		return fmt.Errorf("multiple control-plane writers detected: writable CANONICAL databases=%s (want exactly one)", strings.Join(canonicalWriters, ", "))
	}
	return nil
}

func samePhysicalFile(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(filepath.Clean(left))
	rightAbs, rightErr := filepath.Abs(filepath.Clean(right))
	if leftErr == nil && rightErr == nil && leftAbs == rightAbs {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
