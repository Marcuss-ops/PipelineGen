package deliveries

import (
	"context"
	"fmt"
)

// UpsertDestination creates or updates a delivery destination registry entry.
// Used by admin/CLI tooling to register Drive folders, S3 buckets, etc.
// as valid remote locations for delivery.
func (r *SQLiteRepository) UpsertDestination(ctx context.Context, dest *DeliveryDestination) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO delivery_destinations (destination_id, name, provider, enabled, config_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(destination_id) DO UPDATE SET
			name = excluded.name, provider = excluded.provider,
			enabled = excluded.enabled, config_json = excluded.config_json
	`, dest.DestinationID, dest.Name, dest.Provider, dest.Enabled, dest.ConfigJSON, dest.CreatedAt)
	if err != nil {
		return fmt.Errorf("deliveries: upsert dest: %w", err)
	}
	return nil
}

// GetDestination retrieves a single delivery destination by ID.
// Returns nil, nil when not found (not an error).
func (r *SQLiteRepository) GetDestination(ctx context.Context, destinationID string) (*DeliveryDestination, error) {
	var dest DeliveryDestination
	err := r.db.QueryRowContext(ctx, `SELECT destination_id, name, provider, enabled, config_json, created_at FROM delivery_destinations WHERE destination_id = ?`, destinationID).
		Scan(&dest.DestinationID, &dest.Name, &dest.Provider, &dest.Enabled, &dest.ConfigJSON, &dest.CreatedAt)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("deliveries: get dest: %w", err)
	}
	return &dest, nil
}
