// Package finalize owns the neutral contract for stock finalization.
//
// It intentionally contains no dependency on the parent stockpipeline package
// or on infrastructure types. The parent package supplies an adapter that
// translates these values to the existing finalization spine.
package finalize

import (
	"context"
	"time"
)

// Lease is the neutral ownership proof used by a finalize request.
type Lease struct {
	JobID     string
	WorkerID  string
	LeaseID   string
	Attempt   int
	ExpiresAt time.Time
}

// Artifact is the neutral published chunk projection consumed by finalization.
type Artifact struct {
	Index                    int
	ArtifactID               string
	Filename                 string
	LocalPath                string
	SourceURL                string
	SourceProvider           string
	SourceVideoID            string
	TotalChunks              int
	DrivePath                string
	PolicyVersion            string
	TimestampDriveFolderLink string
	TimestampFolderID        string
	StartSec                 float64
	EndSec                   float64
	Title                    string
	Description              string
	Round                    int
	Tags                     []string
	Category                 string
	Slug                     string
	SHA256                   string
	SizeBytes                int64
	RemoteFileID             string
	RemoteWebViewLink        string
	RemoteDownloadLink       string
}

// Metadata is the neutral published metadata.json projection.
type Metadata struct {
	LocalPath         string
	SHA256            string
	SizeBytes         int64
	RemoteFileID      string
	RemoteWebViewLink string
}

// Request is the neutral input to the stock finalization boundary.
type Request struct {
	JobID       string
	Lease       Lease
	ResultData  []byte
	Fingerprint string
	Artifacts   []Artifact
	Metadata    Metadata
}

// ArtifactRef is the neutral output reference returned after finalization.
type ArtifactRef struct {
	ArtifactID    string
	AssetID       string
	Kind          string
	SourceVersion int64
	ContentHash   string
}

// Result is the neutral result of a successful finalization.
type Result struct {
	JobID        string
	Status       string
	CompletedAt  time.Time
	ArtifactRefs []ArtifactRef
}

// Port is the neutral finalization boundary used by StockFinalizeStep.
type Port interface {
	Complete(context.Context, Request) (Result, error)
}
