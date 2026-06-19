// Package worker defines canonical domain types for the worker system.
package worker

// Worker represents a registered worker node.
type Worker struct {
	ID           string   `json:"id"`
	Capabilities []string `json:"capabilities"`
	MaxLeases    int      `json:"max_leases"`
}
