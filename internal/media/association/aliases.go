// Package association is the backward-compatibility shim for
// internal/media/association. The canonical implementation lives in
// internal/application/association (PR-D.1 migration). The 7 in-repo
// importers that still reference the legacy path keep working unchanged;
// new code MUST import internal/application/association directly so that
// this file can be retired when migrations complete.
//
// Symbol coverage rationale: every public identifier exported from the
// original 13 file package (service, types, engine, embeddings, drive,
// clip_search, candidates, artlist, clips, artlist_folder, scoring,
// providers, terms) is re-exported here. Private helpers stay where
// they belong — the moved files in application/association.
package association

import (
	appassoc "github.com/Marcuss-ops/PipelineGen/internal/application/association"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/clips"
)

// ── Type aliases ────────────────────────────────────────────────────
// `type X = appassoc.X` is a true alias; methods defined on appassoc.X
// carry over automatically. Receivers of form (s *Service) etc. work
// transparently for callers writing `association.Service{...}`.

type (
	Service                  = appassoc.Service
	Engine                   = appassoc.Engine
	SegmentInput             = appassoc.SegmentInput
	Association              = appassoc.Association
	ScoredMatch              = appassoc.ScoredMatch
	AssetSource              = appassoc.AssetSource
	FolderCandidate          = appassoc.FolderCandidate
	CandidatesRequest        = appassoc.CandidatesRequest
	Candidate                = appassoc.Candidate
	CandidatesResponse       = appassoc.CandidatesResponse
	DriveStockAssociation    = appassoc.DriveStockAssociation
	ArtlistStockAssociation  = appassoc.ArtlistStockAssociation
	ClipDriveAssociation     = appassoc.ClipDriveAssociation
	ClipSearchAssociation    = appassoc.ClipSearchAssociation
	ArtlistFolderAssociation = appassoc.ArtlistFolderAssociation
)

// ── Const re-exports ─────────────────────────────────────────────────
// Go does not alias constants, so each value is re-declared here. The
// underlying typed const AssetSourceStockDrive keeps the same
// appassoc.AssetSource type, so callers writing
// `models.AssetSourceStockDrive` (sic. via media/association) still type-check.

const (
	AssetSourceStockDrive     = appassoc.AssetSourceStockDrive
	AssetSourceArtlistFolder  = appassoc.AssetSourceArtlistFolder
	AssetSourceArtlistDynamic = appassoc.AssetSourceArtlistDynamic
	AssetSourceClipDrive      = appassoc.AssetSourceClipDrive
)

// ── Function re-exports ─────────────────────────────────────────────
// Domain owns the implementation; this package forwards the call so that
// bytecode-level callers (`association.NewService(...)`) keep working.
// Method forms (Service.Associate, Engine.ScoreMedia, ...) are inherited
// through the type aliases above and do not need explicit forwarding.

// NewService mirrors appassoc.NewService verbatim.
func NewService(
	dataDir, nodeScraperDir, scriptsDir string,
	stockRepo, artlistRepo, clipsRepo *clips.Repository,
	catalogRepo *catalog.Repository,
) *Service {
	return appassoc.NewService(dataDir, nodeScraperDir, scriptsDir, stockRepo, artlistRepo, clipsRepo, catalogRepo)
}

// NewEngine mirrors appassoc.NewEngine.
func NewEngine(sources ...Association) *Engine {
	return appassoc.NewEngine(sources...)
}

// NewDriveStockAssociation mirrors appassoc.NewDriveStockAssociation.
func NewDriveStockAssociation(stockRepo, artlistRepo *clips.Repository) *DriveStockAssociation {
	return appassoc.NewDriveStockAssociation(stockRepo, artlistRepo)
}

// NewArtlistStockAssociation mirrors appassoc.NewArtlistStockAssociation.
func NewArtlistStockAssociation(repo *clips.Repository) *ArtlistStockAssociation {
	return appassoc.NewArtlistStockAssociation(repo)
}

// NewClipDriveAssociation mirrors appassoc.NewClipDriveAssociation.
func NewClipDriveAssociation(repo *clips.Repository) *ClipDriveAssociation {
	return appassoc.NewClipDriveAssociation(repo)
}

// NewClipSearchAssociation mirrors appassoc.NewClipSearchAssociation.
func NewClipSearchAssociation(artlistRepo *clips.Repository) *ClipSearchAssociation {
	return appassoc.NewClipSearchAssociation(artlistRepo)
}

// NewArtlistFolderAssociation mirrors appassoc.NewArtlistFolderAssociation.
func NewArtlistFolderAssociation(s *Service) *ArtlistFolderAssociation {
	return appassoc.NewArtlistFolderAssociation(s)
}

// DotProduct mirrors appassoc.DotProduct (package-level helper for cosine-style scoring).
func DotProduct(a, b []float32) float64 {
	return appassoc.DotProduct(a, b)
}

// ParseEmbeddingJSON mirrors appassoc.ParseEmbeddingJSON (decoder used by ClipSearchAssociation).
func ParseEmbeddingJSON(jsonStr string) []float32 {
	return appassoc.ParseEmbeddingJSON(jsonStr)
}
