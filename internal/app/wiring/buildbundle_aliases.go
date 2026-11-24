package wiring

import (
	buildbundle "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/buildbundle"
)

// Compatibility facade for the bundle vocabulary. The constructors and their
// data types live in buildbundle; the composition root keeps these aliases so
// existing call sites and tests do not depend on the implementation package.
type (
	IOpaqueStartFunc = buildbundle.IOpaqueStartFunc
	Databases        = buildbundle.Databases

	DriveBundle       = buildbundle.DriveBundle
	DriveDestinations = buildbundle.DriveDestinations

	RepoBundle      = buildbundle.RepoBundle
	MediaRepoBundle = buildbundle.MediaRepoBundle
	SearchBundle    = buildbundle.SearchBundle
	ProcessBundle   = buildbundle.ProcessBundle
	IndexDeps       = buildbundle.IndexDeps

	OutboxBundle      = buildbundle.OutboxBundle
	SyncBundle        = buildbundle.SyncBundle
	MaintBundle       = buildbundle.MaintBundle
	UtilityBundle     = buildbundle.UtilityBundle
	AssetsWiring      = buildbundle.AssetsWiring
	FolderReadHandler = buildbundle.FolderReadHandler
	StagingBundle     = buildbundle.StagingBundle
	FinalizerBundle   = buildbundle.FinalizerBundle
	AIBundle          = buildbundle.AIBundle
	DomainBundle      = buildbundle.DomainBundle
	TextTrackBundle   = buildbundle.TextTrackBundle
	AcquirePorts      = buildbundle.AcquirePorts

	CoreDeps         = buildbundle.CoreDeps
	RepositoryDeps   = buildbundle.RepositoryDeps
	ServiceDeps      = buildbundle.ServiceDeps
	SearchDeps       = buildbundle.SearchDeps
	DeliveryDeps     = buildbundle.DeliveryDeps
	BackgroundDeps   = buildbundle.BackgroundDeps
	AssetsModuleDeps = buildbundle.AssetsModuleDeps

	CriticalHandler    = buildbundle.CriticalHandler
	AssetLifecycleDeps = buildbundle.AssetLifecycleDeps
	JobsBundle         = buildbundle.JobsBundle

	StockBundleDeps        = buildbundle.StockBundleDeps
	StockSourceCacheDeps   = buildbundle.StockSourceCacheDeps
	StockRuntimeDeps       = buildbundle.StockRuntimeDeps
	StockDeliveryDeps      = buildbundle.StockDeliveryDeps
	StockDriveReader       = buildbundle.StockDriveReader
	StockAcquisitionDeps   = buildbundle.StockAcquisitionDeps
	StockMediaDeps         = buildbundle.StockMediaDeps
	StockOrchestrationDeps = buildbundle.StockOrchestrationDeps
	StockFeatureGate       = buildbundle.StockFeatureGate
	StockEnrichmentDeps    = buildbundle.StockEnrichmentDeps
)
