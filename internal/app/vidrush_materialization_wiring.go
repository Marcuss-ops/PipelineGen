package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets"
	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	imagesapp "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	sqliteinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"go.uber.org/zap"
)

func buildVidRushCache(root *wiring.ComposeRoot, log *zap.Logger) scriptports.VidRushCachePort {
	if root == nil || root.DB == nil || root.DB.DB == nil {
		return nil
	}
	return sqliteinfra.NewSQLiteVidRushCacheAdapter(root.DB.DB, log)
}

// vidRushProviderWiring is composition-root-only. It creates one closed
// registry and one common finalizer; providers never write canonical tables.
func buildVidRushMaterialization(root *wiring.ComposeRoot, artlistWiring *wiring.ArtlistWiring, log *zap.Logger) (*adapters.VidRushAssetProviderRegistry, scriptports.VidRushArtifactFinalizer) {
	if root == nil || root.DB == nil || root.DB.DB == nil || root.Drive == nil || root.Drive.Publisher == nil || root.Outbox == nil || root.Outbox.EventsRepo == nil {
		return nil, nil
	}
	committer := assets.NewSQLiteAssetCommitter(root.DB.DB, root.Outbox.EventsRepo, log)
	assetTx := assetfinalizer.NewAssetTxFinalizer(log, committer)
	preparation := assetfinalizer.NewArtifactPreparation(drive.NewArtifactPublisherAdapter(root.Drive.Publisher, log), log)
	finalizer := &vidRushArtifactFinalizer{db: root.DB.DB, preparation: preparation, assetTx: assetTx}

	registry := adapters.NewVidRushAssetProviderRegistry()
	if artlistWiring != nil && artlistWiring.ArtlistDownloader != nil {
		_ = registry.Register(&vidRushArtlistProvider{search: artlistWiring.ProviderAssets, downloader: artlistWiring.ArtlistDownloader, probe: ffmpeg.NewProcessor("ffmpeg")})
	}
	if root.Domains != nil && root.Domains.ImageSearchResolver != nil {
		_ = registry.Register(&vidRushInternetImageProvider{searcher: newInternetImageSearchAdapter(root.Domains.ImageSearchResolver, log)})
	}
	if root.Domains != nil && root.Domains.ImageService != nil {
		_ = registry.Register(&vidRushImageGenerationProvider{generator: root.Domains.ImageService})
	}
	registry.Freeze()
	log.Info("VidRush asset provider registry composed",
		zap.Strings("providers", registry.Names()),
	)
	if len(registry.Names()) == 0 {
		return nil, nil
	}
	return registry, finalizer
}

type vidRushArtifactFinalizer struct {
	db          *sql.DB
	preparation finalization.ArtifactPreparationService
	assetTx     finalization.AssetFinalizerTx
}

func (f *vidRushArtifactFinalizer) Finalize(ctx context.Context, artifact scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	if f == nil || f.db == nil || f.preparation == nil || f.assetTx == nil {
		return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("vidrush finalizer: canonical dependencies unavailable")
	}
	candidate := artifact.Candidate
	if strings.TrimSpace(candidate.AssetID) == "" || strings.TrimSpace(artifact.LocalPath) == "" || strings.TrimSpace(artifact.FileHash) == "" {
		return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("vidrush finalizer: artifact identity, local path and hash are required")
	}
	kind := finalization.KindImage
	if candidate.Provider == scriptpkg.VidRushProviderArtlist {
		kind = finalization.KindVideo
	}
	ext := filepath.Ext(artifact.LocalPath)
	if ext == "" {
		if kind == finalization.KindVideo {
			ext = ".mp4"
		} else if strings.EqualFold(artifact.MIMEType, "image/png") {
			ext = ".png"
		} else {
			ext = ".jpg"
		}
	}
	filename := safeArtifactFilename(candidate.AssetID, ext)
	verified := finalization.VerifiedArtifact{
		ArtifactID: candidate.AssetID, Kind: kind, Filename: filename,
		LocalPath: artifact.LocalPath, MIMEType: firstNonEmpty(artifact.MIMEType, "application/octet-stream"),
		SizeBytes: artifact.SizeBytes, SHA256: artifact.FileHash, SourceVersion: 1,
		Requirement:    finalization.ArtifactRequirementOptional,
		IdempotencyKey: "vidrush:" + candidate.Provider + ":" + candidate.AssetID + ":" + artifact.FileHash,
		Description:    candidate.Query, Source: candidate.Provider,
	}
	published, err := f.preparation.Prepare(ctx, verified)
	if err != nil {
		return scriptpkg.SegmentAssetCandidate{}, err
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("vidrush finalizer: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	published.ArtifactMetadata = map[string]any{
		"source_url": candidate.SourceURL, "source_page_url": candidate.SourcePageURL,
		"query": candidate.Query, "rights_status": candidate.RightsStatus,
		"rights_basis": candidate.RightsBasis, "width": artifact.Width, "height": artifact.Height,
		"duration_ms": artifact.DurationMs,
		"local_path":  artifact.LocalPath,
	}
	_, _, err = f.assetTx.FinalizeAsset(ctx, assetfinalizer.WrapTx(tx), published)
	if err != nil {
		return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("vidrush finalizer: commit asset: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("vidrush finalizer: commit tx: %w", err)
	}
	committed = true
	candidate.DriveLink = published.Location.WebViewLink
	if candidate.DriveLink == "" {
		candidate.DriveLink = published.Location.DownloadLink
	}
	candidate.FileHash, candidate.MIMEType = artifact.FileHash, verified.MIMEType
	candidate.LocalPath = artifact.LocalPath
	candidate.Width, candidate.Height, candidate.DurationMs = artifact.Width, artifact.Height, artifact.DurationMs
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	// The transactional outbox request is durable at this boundary, while
	// the projection worker owns the physical Qdrant write. Waiting here is
	// only a read-side acknowledgement: the finalizer never calls Qdrant.
	// If the worker is unavailable, keep the candidate pending and fail the
	// binding closed; a later reconciliation/run can observe INDEXED.
	candidate.IndexStatus = "pending"
	if current, err := readVidRushIndexState(ctx, f.db, candidate.AssetID); err == nil && current != "" {
		candidate.IndexStatus = current
	}
	if waitForVidRushIndex(ctx, f.db, candidate.AssetID, 30*time.Second) {
		candidate.IndexStatus = scriptpkg.VidRushStatusIndexed
	}
	return candidate, nil
}

func readVidRushIndexState(ctx context.Context, db *sql.DB, assetID string) (string, error) {
	var state string
	err := db.QueryRowContext(ctx, `SELECT COALESCE(index_state, '') FROM media_assets WHERE id = ?`, assetID).Scan(&state)
	return strings.TrimSpace(state), err
}

func waitForVidRushIndex(ctx context.Context, db *sql.DB, assetID string, maxWait time.Duration) bool {
	if db == nil || strings.TrimSpace(assetID) == "" {
		return false
	}
	deadline := time.NewTimer(maxWait)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var state string
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(index_state, '') FROM media_assets WHERE id = ?`, assetID).Scan(&state); err == nil && strings.EqualFold(strings.TrimSpace(state), "INDEXED") {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

type vidRushArtlistProvider struct {
	search     *providerassets.Registry
	downloader artlistpkg.Downloader
	probe      *ffmpeg.Processor
}

func (p *vidRushArtlistProvider) Name() string { return scriptpkg.VidRushProviderArtlist }
func (p *vidRushArtlistProvider) Search(ctx context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if p.search == nil {
		return nil, fmt.Errorf("vidrush artlist: search registry unavailable")
	}
	result, err := p.search.Search(ctx, "artlist", providerassets.SearchRequest{Query: req.Query, Limit: req.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(result.Assets))
	for _, a := range result.Assets {
		assetID := firstNonEmpty(a.ID, a.ExternalID)
		out = append(out, scriptpkg.SegmentAssetCandidate{AssetID: assetID, Provider: p.Name(), Query: req.Query, SourceURL: a.SourceRef, SourcePageURL: a.PageURL, PreviewURL: a.PreviewURL, Score: a.Score, RightsStatus: "unknown", AcquisitionStatus: scriptpkg.VidRushStatusCandidateFound})
	}
	return out, nil
}
func (p *vidRushArtlistProvider) Acquire(ctx context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	if p == nil || p.downloader == nil {
		return scriptports.LocalArtifact{}, fmt.Errorf("vidrush artlist: downloader unavailable")
	}
	source := firstNonEmpty(candidate.SourceURL, candidate.PreviewURL)
	result, err := p.downloader.Download(ctx, artlistpkg.DownloadRequest{SourceRef: source, DestinationID: os.TempDir(), Filename: safeArtifactFilename(candidate.AssetID, ".mp4"), ClipPageURL: candidate.SourcePageURL})
	if err != nil {
		return scriptports.LocalArtifact{}, err
	}
	if result == nil || result.LocalPath == "" || result.Bytes <= 0 {
		return scriptports.LocalArtifact{}, fmt.Errorf("vidrush artlist: empty download")
	}
	hash, size, err := hashFile(result.LocalPath)
	if err != nil {
		return scriptports.LocalArtifact{}, err
	}
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	candidate.LocalPath = result.LocalPath
	candidate.FileHash = hash
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: result.LocalPath, MIMEType: "video/mp4", SizeBytes: size, FileHash: hash}, nil
}
func (p *vidRushArtlistProvider) Verify(ctx context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	if p == nil || p.probe == nil {
		return scriptports.VerifiedArtifact{}, fmt.Errorf("vidrush artlist: ffprobe unavailable")
	}
	info, err := p.probe.Probe(ctx, artifact.LocalPath)
	if err != nil || info == nil || !info.HasVideo || info.Duration <= 0 || info.Width <= 0 || info.Height <= 0 {
		if err == nil {
			err = fmt.Errorf("invalid video stream")
		}
		return scriptports.VerifiedArtifact{}, err
	}
	candidate := artifact.Candidate
	candidate.Width, candidate.Height = info.Width, info.Height
	candidate.DurationMs = info.Duration.Milliseconds()
	candidate.RightsStatus = "verified"
	candidate.RightsBasis = "artlist licensed-provider policy"
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, FileHash: artifact.FileHash, DurationMs: candidate.DurationMs, Width: info.Width, Height: info.Height, RightsStatus: candidate.RightsStatus, VerificationNote: "ffprobe video stream validated"}, nil
}

type vidRushInternetImageProvider struct {
	searcher adapters.InternetImageSearcher
}

// vidRushImageGenerationProvider is the VidRush adapter for the existing
// registry-backed image.generate.google use case. It returns the existing
// ArtifactManifest to the common finalization boundary; it never writes
// media_assets itself.
type vidRushImageGenerationProvider struct {
	generator *imagesapp.Service
}

func (p *vidRushImageGenerationProvider) Name() string {
	return scriptpkg.VidRushProviderImageGeneration
}
func (p *vidRushImageGenerationProvider) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (p *vidRushImageGenerationProvider) Acquire(ctx context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	if p == nil || p.generator == nil {
		return scriptports.LocalArtifact{}, fmt.Errorf("vidrush image generation: generator unavailable")
	}
	output, err := p.generator.GenerateArtifact(ctx, "vidrush-"+candidate.AssetID, candidate.Query, "cinematic", 1920, 1080, nil)
	if err != nil {
		return scriptports.LocalArtifact{}, err
	}
	if output == nil || output.Result == nil || strings.TrimSpace(output.OutputPath) == "" || output.Manifest == nil {
		return scriptports.LocalArtifact{}, fmt.Errorf("vidrush image generation: incomplete ArtifactManifest output")
	}
	hash, size, err := hashFile(output.OutputPath)
	if err != nil {
		return scriptports.LocalArtifact{}, err
	}
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	candidate.LocalPath, candidate.FileHash = output.OutputPath, hash
	candidate.MIMEType = firstNonEmpty(output.Result.Format, "image/png")
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: output.OutputPath, MIMEType: candidate.MIMEType, SizeBytes: size, FileHash: hash, Manifest: output.Manifest}, nil
}
func (p *vidRushImageGenerationProvider) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	verified, err := adapters.VerifyVidRushImageFile(artifact.Candidate, artifact.LocalPath, adapters.DefaultVidRushImagePolicy())
	if err != nil {
		return scriptports.VerifiedArtifact{}, err
	}
	candidate := verified.Candidate
	candidate.RightsStatus = "verified"
	candidate.RightsBasis = "image.generate.google provider manifest"
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: verified.MIMEType, SizeBytes: verified.SizeBytes, FileHash: verified.FileHash, Width: verified.Width, Height: verified.Height, RightsStatus: candidate.RightsStatus, VerificationNote: "generated ArtifactManifest and image decode validated", Manifest: artifact.Manifest}, nil
}

func (p *vidRushInternetImageProvider) Name() string { return scriptpkg.VidRushProviderInternetImages }
func (p *vidRushInternetImageProvider) Search(ctx context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if p == nil || p.searcher == nil {
		return nil, fmt.Errorf("vidrush images: search resolver unavailable")
	}
	results, err := p.searcher.SearchImages(ctx, adapters.InternetImageSearchRequest{SegmentID: req.SegmentID, Query: req.Query, TextHash: req.TextHash, Limit: req.Limit, Provider: scriptpkg.VidRushProviderInternetImages})
	return results, err
}
func (p *vidRushInternetImageProvider) Acquire(ctx context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	data, mime, err := adapters.DownloadVidRushImageForCandidate(ctx, http.DefaultClient, candidate, adapters.DefaultVidRushImagePolicy())
	if err != nil {
		return scriptports.LocalArtifact{}, err
	}
	file, err := os.CreateTemp("", "vidrush-image-*")
	if err != nil {
		return scriptports.LocalArtifact{}, err
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return scriptports.LocalArtifact{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return scriptports.LocalArtifact{}, err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	candidate.LocalPath, candidate.MIMEType, candidate.FileHash = path, mime, hash
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: path, MIMEType: mime, SizeBytes: int64(len(data)), FileHash: hash}, nil
}
func (p *vidRushInternetImageProvider) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	verified, err := adapters.VerifyVidRushImageFile(artifact.Candidate, artifact.LocalPath, adapters.DefaultVidRushImagePolicy())
	if err != nil {
		return scriptports.VerifiedArtifact{}, err
	}
	candidate := verified.Candidate
	if !strings.EqualFold(strings.TrimSpace(candidate.RightsStatus), "verified") {
		candidate.RightsStatus = "unknown_allowed"
		if strings.TrimSpace(candidate.RightsBasis) == "" {
			candidate.RightsBasis = "source-license metadata required"
		}
	}
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	// Production binding remains verified-only; unknown_allowed is retained
	// as a durable candidate but cannot become the scene winner.
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: verified.MIMEType, SizeBytes: verified.SizeBytes, FileHash: verified.FileHash, Width: verified.Width, Height: verified.Height, RightsStatus: candidate.RightsStatus, VerificationNote: "image decoded and dimensions validated"}, nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(digest.Sum(nil)), stat.Size(), nil
}
func safeArtifactFilename(assetID, ext string) string {
	name := filepath.Base(strings.TrimSpace(assetID))
	if name == "." || name == "" {
		name = "vidrush-asset"
	}
	return name + ext
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

var _ scriptports.VidRushAssetProvider = (*vidRushArtlistProvider)(nil)
var _ scriptports.VidRushAssetProvider = (*vidRushInternetImageProvider)(nil)
var _ scriptports.VidRushArtifactFinalizer = (*vidRushArtifactFinalizer)(nil)
