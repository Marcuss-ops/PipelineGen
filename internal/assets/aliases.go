package assets

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Type aliases ────────────────────────────────────────────────────
// All canonical domain types are defined in internal/domain/asset/.
// These aliases allow existing code to continue using assets.Asset,
// assets.Filter, etc. without import changes.

type Asset = asset.Asset
type Source = asset.Source
type MediaType = asset.MediaType
type Metadata = asset.Metadata
type LifecycleState = asset.LifecycleState
type Location = asset.Location
type LocationKind = asset.LocationKind
type ProcessingRecord = asset.ProcessingRecord
type ProcessingStatus = asset.ProcessingStatus
type ProcessingStage = asset.ProcessingStage
type Version = asset.Version
type Artifact = asset.Artifact
type ArtifactStatus = asset.ArtifactStatus
type Delivery = asset.Delivery
type DeliveryStatus = asset.DeliveryStatus
type DeliveryDestination = asset.DeliveryDestination
type Details = asset.Details
type Summary = asset.Summary
type Filter = asset.Filter
type SearchQuery = asset.SearchQuery
type SearchResult = asset.SearchResult

// ── Re-exported constants ───────────────────────────────────────────

const (
	StateStaging    = asset.StateStaging
	StateProcessing = asset.StateProcessing
	StateActive     = asset.StateActive
	StateDeleted    = asset.StateDeleted
	StateReady      = asset.StateReady
	StatePending    = asset.StatePending

	LocationKindLocal         = asset.LocationKindLocal
	LocationKindDrive         = asset.LocationKindDrive
	LocationKindObjectStorage = asset.LocationKindObjectStorage

	StatusPending   = asset.StatusPending
	StatusRunning   = asset.StatusRunning
	StatusCompleted = asset.StatusCompleted
	StatusFailed    = asset.StatusFailed

	StageDownload      = asset.StageDownload
	StageNormalize     = asset.StageNormalize
	StageTranscription = asset.StageTranscription
	StageEmbedding     = asset.StageEmbedding
	StageIndexing      = asset.StageIndexing
	StageUpload        = asset.StageUpload
	StageVerify        = asset.StageVerify
	StageCleanup       = asset.StageCleanup

	ArtifactStaging     = asset.ArtifactStaging
	ArtifactVerifying   = asset.ArtifactVerifying
	ArtifactReady       = asset.ArtifactReady
	ArtifactFailed      = asset.ArtifactFailed
	ArtifactQuarantined = asset.ArtifactQuarantined
	ArtifactDeleted     = asset.ArtifactDeleted

	DeliveryPending     = asset.DeliveryPending
	DeliveryLeased      = asset.DeliveryLeased
	DeliveryRunning     = asset.DeliveryRunning
	DeliveryRetryWait   = asset.DeliveryRetryWait
	DeliverySucceeded   = asset.DeliverySucceeded
	DeliveryFailed      = asset.DeliveryFailed
	DeliveryBlockedAuth = asset.DeliveryBlockedAuth
	DeliveryCancelled   = asset.DeliveryCancelled
)

// ── Re-exported domain interfaces ───────────────────────────────────

type Repository = asset.Repository
type LocationRepository = asset.LocationRepository
type ProcessingRepository = asset.ProcessingRepository
type VersionRepository = asset.VersionRepository
type ArtifactStore = asset.ArtifactStore
type DeliveryStore = asset.DeliveryStore
type Searcher = asset.Searcher
