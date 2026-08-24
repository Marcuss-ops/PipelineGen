// Package destinations (FASE 2D EXPAND, July 2026) — image destination routing.
// Smoke-test write to verify worktree persistence in this session.
package images

import "errors"

type Destination struct {
	DriveFolderID string `json:"drive_folder_id" yaml:"drive_folder_id"`
}

func (d Destination) IsZero() bool { return d.DriveFolderID == "" }

var ErrDestinationNotFound = errors.New("destination not found")

type destinationsFile struct {
	Destinations map[string]string `yaml:"destinations"`
}
