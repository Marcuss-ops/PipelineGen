// cmd/admin/rehydrate_clip.go — restore a YouTube clip set whose media and
// catalog rows were removed by retention.
//
// Why (2026-11-26, OVL-06): the Ilary replay stopped in GENERATING_SCENE_TEXT
// with "clip yt_JP6BrYSa_yw_0_58_v1 has no Drive link". The clip was not just
// missing its local file: the media_assets rows had been deleted too, so no
// existing command could repair it. `index-drive-clip` creates an asset whose
// id is the Drive file id (not the canonical yt_<video>_<start>_<end>_<policy>
// identity), and the ingest cache only reuses rows that still exist.
//
// This command rebuilds the canonical rows from the clip files already
// published on Drive (the Drive inventory is the source of file ids) and
// commits them through the same canonical AssetCommitter every producer uses:
//
//	admin rehydrate-clip --inventory ops/drive_inventory/latest/clips.json \
//	  --video-id JP6BrYSa_yw [--apply] [--limit N]
//
// It never writes to Drive and never re-downloads from YouTube: the published
// mp4 is copied into the canonical local path, hashed, probed, and committed
// with the original identity (source_video_id + start_ms + end_ms +
// policy_version), which is what keeps the DB-level YouTube identity index
// from collapsing the whole set onto the first clip.
package drive

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// driveInventoryDoc mirrors the subset of ops/drive_inventory/latest/clips.json
// this command needs: the recursive tree with its file records.
type driveInventoryDoc struct {
	Tree *inventoryNode `json:"tree"`
}

type inventoryNode struct {
	Type     string          `json:"type"`
	Files    []inventoryFile `json:"files"`
	Children []inventoryNode `json:"children"`
}

type inventoryFile struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	ID       string `json:"id"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	MimeType string `json:"mime_type"`
}

// rehydrateCandidate is one clip to restore. It is derived from the Drive file
// name, which carries the canonical identity:
//
//	yt_<videoID>_<startSec>_<endSec>_<policy>_<slug>.mp4
type rehydrateCandidate struct {
	ClipID        string
	VideoID       string
	DriveFileID   string
	DriveName     string
	DrivePath     string
	SizeBytes     int64
	StartMs       int64
	EndMs         int64
	PolicyVersion string
	Name          string
	Filename      string
}

func RunRehydrateClip(args []string) error {
	fs := flag.NewFlagSet("rehydrate-clip", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	inventoryPath := fs.String("inventory", "", "Path to the Drive inventory clips.json (required unless --clip-ids is set)")
	videoID := fs.String("video-id", "", "YouTube video id whose yt_<video>_<start>_<end>_<policy> clips are restored (required unless --clip-ids is set)")
	clipIDs := fs.String("clip-ids", "", "Comma-separated canonical clip ids to restore without an inventory (e.g. yt_abc_0_58_v1)")
	localSubdir := fs.String("local-subdir", "", "Storage subdir under MediaDir (default clips/general/yt_<videoID>)")
	category := fs.String("category", "general", "Asset category for the restored clips")
	limit := fs.Int("limit", 0, "Restore at most N clips (0 = all)")
	force := fs.Bool("force", false, "Re-download and re-commit clips whose local file already exists")
	fromLocal := fs.Bool("from-local", false, "Commit the media already present at the canonical local path instead of downloading from Drive (use when the published copy is gone but the clip was re-cut locally)")
	apply := fs.Bool("apply", false, "Download and commit; without this flag the plan is only printed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	videoIDValue := strings.TrimSpace(*videoID)
	var candidates []rehydrateCandidate
	if strings.TrimSpace(*clipIDs) != "" {
		parsed, err := candidatesFromClipIDs(*clipIDs, videoIDValue)
		if err != nil {
			return err
		}
		candidates = parsed
	} else {
		if strings.TrimSpace(*inventoryPath) == "" {
			return fmt.Errorf("--inventory is required (or pass --clip-ids)")
		}
		if videoIDValue == "" {
			return fmt.Errorf("--video-id is required (or pass --clip-ids)")
		}
		doc, err := loadDriveInventory(*inventoryPath)
		if err != nil {
			return err
		}
		parsed, err := collectRehydrateCandidates(doc, videoIDValue)
		if err != nil {
			return err
		}
		candidates = parsed
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no clip candidates for video %q", videoIDValue)
	}
	if *limit > 0 && len(candidates) > *limit {
		candidates = candidates[:*limit]
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive reader is required")
	}
	if root.CanonicalAssetWriter == nil {
		return fmt.Errorf("canonical asset committer is not wired")
	}
	baseLocalDir := ""
	if subdir := strings.TrimSpace(*localSubdir); subdir != "" {
		baseLocalDir = cfg.Storage.FullPath(filepath.Join(cfg.Storage.MediaDir, subdir))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	restored, skipped, failed := 0, 0, 0
	for _, cand := range candidates {
		localDir := baseLocalDir
		if localDir == "" {
			localDir = cfg.Storage.FullPath(filepath.Join(cfg.Storage.MediaDir, "clips", "general", "yt_"+cand.VideoID))
		}
		localPath := filepath.Join(localDir, cand.Filename)
		if !*fromLocal && cand.DriveFileID == "" {
			failed++
			fmt.Fprintf(os.Stderr, "rehydrate %s: no Drive file id (use --from-local for media already on disk)\n", cand.ClipID)
			continue
		}
		existing, err := root.GetMediaClip(ctx, cand.ClipID)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "rehydrate %s: read catalog: %v\n", cand.ClipID, err)
			continue
		}
		if existing != nil && !*force {
			if info, statErr := os.Stat(existing.LocalPath()); statErr == nil && info.Size() > 0 {
				skipped++
				fmt.Printf("skip   %s (already present: %s)\n", cand.ClipID, existing.LocalPath())
				continue
			}
		}
		if !*apply {
			fmt.Printf("plan   %s drive=%s local=%s start_ms=%d end_ms=%d\n",
				cand.ClipID, cand.DriveFileID, localPath, cand.StartMs, cand.EndMs)
			continue
		}
		videoForClip := cand.VideoID
		if videoForClip == "" {
			videoForClip = videoIDValue
		}
		if err := rehydrateOneClip(ctx, root, log, cand, localPath, *category, videoForClip, *fromLocal); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "rehydrate %s: %v\n", cand.ClipID, err)
			continue
		}
		restored++
		fmt.Printf("ok     %s local=%s\n", cand.ClipID, localPath)
	}

	if !*apply {
		fmt.Printf("plan: %d clip(s) would be restored from %s (dry-run; pass --apply)\n", len(candidates), *inventoryPath)
		return nil
	}
	fmt.Printf("rehydrate-clip: restored=%d skipped=%d failed=%d video=%s\n", restored, skipped, failed, videoIDValue)
	if failed > 0 {
		return fmt.Errorf("rehydrate-clip: %d clip(s) failed", failed)
	}
	return nil
}

// rehydrateOneClip downloads the published mp4 (or reads the already-cut
// local file), measures it and commits the canonical asset. The commit is the
// only write: no Drive call mutates state.
func rehydrateOneClip(ctx context.Context, root *wiring.ComposeRoot, log *zap.Logger, cand rehydrateCandidate, localPath, category, videoID string, fromLocal bool) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return fmt.Errorf("create local directory: %w", err)
	}
	if !fromLocal {
		reader, _, err := root.Drive.Reader.DownloadFile(ctx, cand.DriveFileID)
		if err != nil {
			return fmt.Errorf("download Drive clip: %w", err)
		}
		defer func() { _ = reader.Close() }()
		out, err := os.Create(localPath)
		if err != nil {
			return fmt.Errorf("create local clip: %w", err)
		}
		if _, err := io.Copy(out, reader); err != nil {
			_ = out.Close()
			return fmt.Errorf("save local clip: %w", err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("close local clip: %w", err)
		}
	}
	info, err := os.Stat(localPath)
	if err != nil || info.Size() == 0 {
		return fmt.Errorf("local clip is empty: %v", err)
	}

	duration, err := cli.ProbeSoundEffectDuration(ctx, localPath)
	if err != nil || duration <= 0 {
		return fmt.Errorf("probe clip duration: %v", err)
	}
	hash, err := cli.Sha256File(localPath)
	if err != nil {
		return fmt.Errorf("hash clip: %w", err)
	}

	req, err := buildRehydrateCommitRequest(cand, localPath, category, videoID, duration, hash, info.Size(), !fromLocal)
	if err != nil {
		return err
	}
	if _, err := root.CanonicalAssetWriter.CommitAndIndex(ctx, req); err != nil {
		return fmt.Errorf("commit canonical asset: %w", err)
	}
	if log != nil {
		log.Info("rehydrated clip from Drive",
			zap.String("clip_id", cand.ClipID), zap.String("drive_file_id", cand.DriveFileID),
			zap.String("local_path", localPath), zap.Int64("start_ms", cand.StartMs), zap.Int64("end_ms", cand.EndMs))
	}
	return nil
}

// buildRehydrateCommitRequest is pure so the identity mapping (the part the
// YouTube uniqueness index depends on) is unit-testable without Drive.
func buildRehydrateCommitRequest(cand rehydrateCandidate, localPath, category, videoID string, duration time.Duration, hash string, sizeBytes int64, withDrive bool) (persistence.CommitRequest, error) {
	if strings.TrimSpace(cand.ClipID) == "" {
		return persistence.CommitRequest{}, fmt.Errorf("clip id is empty")
	}
	if cand.EndMs <= cand.StartMs {
		return persistence.CommitRequest{}, fmt.Errorf("clip %s has an invalid source window (%d..%d ms)", cand.ClipID, cand.StartMs, cand.EndMs)
	}
	if strings.TrimSpace(hash) == "" {
		return persistence.CommitRequest{}, fmt.Errorf("clip %s has no content hash", cand.ClipID)
	}
	taxonomy, err := capregistry.ResolveTaxonomy(capregistry.TaxonomyInput{
		AssetID:   cand.ClipID,
		Provider:  "youtube",
		MediaType: capregistry.MediaVideo,
	})
	if err != nil {
		return persistence.CommitRequest{}, fmt.Errorf("resolve taxonomy: %w", err)
	}
	driveLink := "https://drive.google.com/file/d/" + cand.DriveFileID + "/view"
	downloadLink := "https://drive.google.com/uc?export=download&id=" + cand.DriveFileID
	extra := map[string]any{
		"local_path":       localPath,
		"content_hash":     hash,
		"index_state":      string(asset.StateDiscovered),
		"source_video_id":  videoID,
		"youtube_video_id": videoID,
		"youtube_url":      "https://www.youtube.com/watch?v=" + videoID,
		"start_ms":         cand.StartMs,
		"end_ms":           cand.EndMs,
		"duration_source":  "measured",
		"rehydrated_at":    time.Now().UTC().Format(time.RFC3339),
	}
	locations := []asset.LocationCommit{
		{
			Kind: "local", Provider: "local", URI: localPath,
			ContentSHA256: hash, IsPrimary: true,
		},
	}
	if withDrive {
		extra["drive_file_id"] = cand.DriveFileID
		extra["drive_link"] = driveLink
		extra["download_link"] = downloadLink
		driveLocation := asset.LocationCommit{
			Kind: "drive", Provider: "drive", ExternalID: cand.DriveFileID,
			WebViewLink: driveLink, DownloadURL: downloadLink, MimeType: "video/mp4",
			FileSizeBytes: sizeBytes, IsPrimary: true,
		}
		locations = append([]asset.LocationCommit{driveLocation}, locations...)
		locations[1].IsPrimary = false
	}
	return persistence.CommitRequest{
		AssetID:        cand.ClipID,
		Source:         "youtube",
		Name:           cand.Name,
		Filename:       cand.Filename,
		MediaType:      "video",
		Category:       category,
		DurationMs:     duration.Milliseconds(),
		ContentHash:    hash,
		SearchText:     cand.Name + " " + cand.ClipID,
		LifecycleState: string(asset.StateActive),
		IndexState:     string(asset.StateDiscovered),
		LocalPath:      localPath,
		SourceURL:      "https://www.youtube.com/watch?v=" + videoID,
		SourceProvider: "youtube",
		SourceVideoID:  videoID,
		PolicyVersion:  cand.PolicyVersion,
		StartMs:        cand.StartMs,
		EndMs:          cand.EndMs,
		Metadata: persistence.TypedMetadata{
			Title:          cand.Name,
			SourceProvider: "youtube",
			SourceVideoID:  videoID,
			StartSec:       float64(cand.StartMs) / 1000,
			EndSec:         float64(cand.EndMs) / 1000,
			SizeBytes:      sizeBytes,
			Extra:          extra,
		},
		Locations:      locations,
		Taxonomy:       taxonomy,
		EmitIndexEvent: true,
	}, nil
}

// loadDriveInventory decodes the inventory document strictly: a malformed tree
// must fail the command, never restore a partial set silently.
func loadDriveInventory(path string) (*driveInventoryDoc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read inventory: %w", err)
	}
	var doc driveInventoryDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode inventory: %w", err)
	}
	if doc.Tree == nil {
		return nil, fmt.Errorf("inventory %s has no tree", path)
	}
	return &doc, nil
}

// collectRehydrateCandidates walks the tree and derives one candidate per
// clip file of the requested video. Duplicate identities are rejected: the
// canonical identity must map to exactly one Drive file.
func collectRehydrateCandidates(doc *driveInventoryDoc, videoID string) ([]rehydrateCandidate, error) {
	pattern, err := regexp.Compile(`^(yt_` + regexp.QuoteMeta(videoID) + `_(\d+)_(\d+)_(v\d+))_`)
	if err != nil {
		return nil, fmt.Errorf("compile clip identity pattern: %w", err)
	}
	byID := make(map[string]rehydrateCandidate)
	var walk func(node *inventoryNode) error
	walk = func(node *inventoryNode) error {
		if node == nil {
			return nil
		}
		for _, file := range node.Files {
			if !strings.EqualFold(file.MimeType, "video/mp4") {
				continue
			}
			match := pattern.FindStringSubmatch(file.Name)
			if match == nil {
				continue
			}
			startSec, err1 := strconv.ParseInt(match[2], 10, 64)
			endSec, err2 := strconv.ParseInt(match[3], 10, 64)
			if err1 != nil || err2 != nil || endSec <= startSec {
				return fmt.Errorf("clip file %q has an invalid source window", file.Name)
			}
			cand := rehydrateCandidate{
				ClipID:        match[1],
				VideoID:       videoID,
				DriveFileID:   file.ID,
				DriveName:     file.Name,
				DrivePath:     file.Path,
				SizeBytes:     file.Size,
				StartMs:       startSec * 1000,
				EndMs:         endSec * 1000,
				PolicyVersion: match[4],
				Filename:      filepath.Base(file.Name),
				Name:          clipDisplayName(file.Name, match[0]),
			}
			if cand.DriveFileID == "" {
				return fmt.Errorf("clip file %q has no Drive id", file.Name)
			}
			if previous, exists := byID[cand.ClipID]; exists && previous.DriveFileID != cand.DriveFileID {
				return fmt.Errorf("clip %s maps to two Drive files (%s, %s)", cand.ClipID, previous.DriveFileID, cand.DriveFileID)
			}
			byID[cand.ClipID] = cand
		}
		for i := range node.Children {
			if err := walk(&node.Children[i]); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(doc.Tree); err != nil {
		return nil, err
	}
	out := make([]rehydrateCandidate, 0, len(byID))
	for _, cand := range byID {
		out = append(out, cand)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartMs != out[j].StartMs {
			return out[i].StartMs < out[j].StartMs
		}
		return out[i].ClipID < out[j].ClipID
	})
	return out, nil
}

// candidatesFromClipIDs builds the plan for clips whose canonical ids are
// known (the payload binds them) but whose Drive inventory entry is gone. The
// id itself carries video, source window and policy; the media must already be
// cut at the canonical local path (--from-local).
func candidatesFromClipIDs(csv, videoID string) ([]rehydrateCandidate, error) {
	pattern := regexp.MustCompile(`^yt_(.+?)_(\d+)_(\d+)_(v\d+)$`)
	var out []rehydrateCandidate
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(csv, ",") {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		match := pattern.FindStringSubmatch(id)
		if match == nil {
			return nil, fmt.Errorf("clip id %q is not the canonical yt_<video>_<start>_<end>_<policy> shape", id)
		}
		if videoID != "" && match[1] != videoID {
			return nil, fmt.Errorf("clip id %q belongs to video %q, not %q", id, match[1], videoID)
		}
		startSec, err1 := strconv.ParseInt(match[2], 10, 64)
		endSec, err2 := strconv.ParseInt(match[3], 10, 64)
		if err1 != nil || err2 != nil || endSec <= startSec {
			return nil, fmt.Errorf("clip id %q has an invalid source window", id)
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, rehydrateCandidate{
			ClipID:        id,
			VideoID:       match[1],
			StartMs:       startSec * 1000,
			EndMs:         endSec * 1000,
			PolicyVersion: match[4],
			Filename:      id + ".mp4",
			Name:          id,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--clip-ids is empty")
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].VideoID != out[j].VideoID {
			return out[i].VideoID < out[j].VideoID
		}
		return out[i].StartMs < out[j].StartMs
	})
	return out, nil
}

// clipDisplayName turns the Drive filename slug into a readable asset name.
// The canonical title lived in the deleted row; the slug is the only fact the
// published file still carries.
func clipDisplayName(filename, identityPrefix string) string {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	slug := strings.TrimPrefix(base, identityPrefix)
	slug = strings.Trim(slug, "_- ")
	slug = strings.ReplaceAll(slug, "-", " ")
	slug = strings.Join(strings.Fields(slug), " ")
	if slug == "" {
		return base
	}
	return strings.ToUpper(slug[:1]) + slug[1:]
}
