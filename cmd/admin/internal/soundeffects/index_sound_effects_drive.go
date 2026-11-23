package soundeffects

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
	"go.uber.org/zap"
)

// indexSoundEffectsDriveCommand walks the canonical Drive SFX tree (one folder
// per family, audio files below it), materializes every file under
// <data>/media/sound_effects/<family>/, derives the deterministic semantic
// taxonomy and commits each asset through the canonical media dispatcher.
//
// This is the ONE indexer for the supplied SFX library: it does not keep an
// alias table, so a new file dropped into the Drive tree becomes selectable
// after one run. The JSON index it writes is a projection of the same rows, so
// an operator (or the overlay planner) can choose an asset_id without a second
// catalog.
func RunIndexSoundEffectsDrive(args []string) error {
	fs := flag.NewFlagSet("index-sound-effects-drive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folderID := fs.String("folder-id", soundEffectsDriveFolderID, "Drive root folder containing the SFX family tree")
	apply := fs.Bool("apply", false, "download, index and write the artifacts (default: dry-run inventory)")
	maxDepth := fs.Int("max-depth", 4, "maximum Drive folder recursion depth")
	maxFiles := fs.Int("max-files", 0, "stop after N files (0 = all)")
	indexOut := fs.String("index-out", filepath.Join("ops", "jobs", "sfx_index.json"), "JSON index output path (empty = do not write)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *maxDepth < 0 {
		return fmt.Errorf("index-sound-effects-drive: --max-depth must be >= 0")
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
	if *apply {
		if root.Repos == nil || root.Repos.ClipsRepo == nil || root.Outbox == nil ||
			root.Outbox.Dispatcher == nil || root.Outbox.EventsPool == nil || root.Outbox.EventsRepo == nil {
			return fmt.Errorf("clips repository and outbox dispatcher are required for --apply")
		}
	}
	// The catalog is the single owner of the sound-effect SQL: the index write
	// reads through it instead of re-opening media_assets with its own copy of
	// the predicate.
	catalog := pgmedia.NewSoundEffectCatalog(root.MediaPostgres)
	if *apply && catalog == nil {
		return fmt.Errorf("media PostgreSQL SSOT is required for sound-effect indexing")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
	defer cancel()

	files, err := walkSFXDriveTree(ctx, root.Drive.Reader, strings.TrimSpace(*folderID), *maxDepth)
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Family != files[j].Family {
			return files[i].Family < files[j].Family
		}
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})
	if *maxFiles > 0 && len(files) > *maxFiles {
		files = files[:*maxFiles]
	}
	familyCounts := make(map[string]int)
	for _, file := range files {
		familyCounts[file.Family]++
	}
	fmt.Printf("Drive SFX root=%s files=%d families=%d\n", *folderID, len(files), len(familyCounts))
	keys := make([]string, 0, len(familyCounts))
	for family := range familyCounts {
		keys = append(keys, family)
	}
	sort.Strings(keys)
	for _, family := range keys {
		fmt.Printf("  %-18s %d\n", family, familyCounts[family])
	}
	if !*apply {
		fmt.Println("Dry run only. Re-run with --apply to download, index and write the JSON index.")
		return nil
	}

	localRoot := cfg.Storage.FullPath(filepath.Join(cfg.Storage.MediaDir, "sound_effects"))
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		return fmt.Errorf("create sound effects directory: %w", err)
	}
	prober := rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)

	deadLettersBefore, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, outboxevents.EventAssetIndexRequested, "dead_letter")
	if err != nil {
		return fmt.Errorf("read outbox baseline: %w", err)
	}
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()

	indexed, skipped, failed := 0, 0, 0
	for _, file := range files {
		rel := filepath.Join(file.FolderName, file.Name)
		dest := filepath.Join(localRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("create directory for %s: %w", file.Name, err)
		}
		if info, statErr := os.Stat(dest); statErr == nil && info.Size() == file.Size && file.Size > 0 {
			skipped++
		} else if err := downloadSFXFile(ctx, root.Drive.Reader, file.ID, dest); err != nil {
			failed++
			log.Warn("sound effect download failed", zap.String("asset_id", file.ID), zap.String("name", file.Name), zap.Error(err))
			continue
		}

		hash, err := cli.Sha256File(dest)
		if err != nil {
			return fmt.Errorf("hash %s: %w", file.Name, err)
		}
		duration, err := cli.ProbeSoundEffectDuration(ctx, prober, dest)
		if err != nil || duration <= 0 {
			failed++
			log.Warn("sound effect duration probe failed", zap.String("name", file.Name), zap.Error(err))
			continue
		}
		if err := indexSFXFile(ctx, root, file, dest, hash, duration); err != nil {
			return err
		}
		indexed++
	}

	if err := cli.WaitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
	}
	fmt.Printf("SFX indexed=%d skipped=%d failed=%d directory=%s\n", indexed, skipped, failed, localRoot)

	if strings.TrimSpace(*indexOut) != "" {
		if err := writeSFXIndex(ctx, catalog, strings.TrimSpace(*folderID), *indexOut); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("sound-effect indexing completed with %d failures", failed)
	}
	return nil
}

// sfxDriveFile is one audio file discovered in the Drive family tree.
type sfxDriveFile struct {
	ID         string
	Name       string
	FolderName string
	Family     string
	Size       int64
}

// walkSFXDriveTree lists the root folder recursively, one level per family
// folder. Audio files directly in the root are classified from their filename.
func walkSFXDriveTree(ctx context.Context, reader drive.Reader, rootID string, maxDepth int) ([]sfxDriveFile, error) {
	if strings.TrimSpace(rootID) == "" {
		return nil, fmt.Errorf("SFX Drive root folder id is required")
	}
	type pending struct {
		id         string
		relPath    string
		rootFamily string
		depth      int
	}
	queue := []pending{{id: rootID, relPath: "", rootFamily: "", depth: 0}}
	seen := map[string]struct{}{rootID: {}}
	files := make([]sfxDriveFile, 0, 512)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		entries, err := reader.ListFiles(ctx, current.id)
		if err != nil {
			return nil, fmt.Errorf("list Drive folder %s: %w", current.id, err)
		}
		for _, entry := range entries {
			if entry.MimeType == "application/vnd.google-apps.folder" {
				if current.depth >= maxDepth {
					continue
				}
				if _, ok := seen[entry.ID]; ok {
					continue
				}
				seen[entry.ID] = struct{}{}
				// A nested folder keeps its TOP-LEVEL family: Whoosh/Swish &
				// Swings is still a whoosh cue, and the subtype already carries
				// the finer distinction. This keeps the selection pool families
				// stable no matter how the Drive tree is re-organized.
				rootFamily := current.rootFamily
				if rootFamily == "" {
					rootFamily = sfxFamilyFromFolder(entry.Name)
				}
				queue = append(queue, pending{id: entry.ID, relPath: filepath.Join(current.relPath, entry.Name), rootFamily: rootFamily, depth: current.depth + 1})
				continue
			}
			if !isAudioSFXName(entry.Name) {
				continue
			}
			family := current.rootFamily
			if family == "" {
				family = sfxFamilyFromFolder(current.relPath)
			}
			if family == "" {
				family = detail.ClassifySoundEffect(entry.Name, "", nil).Family
			}
			relPath := current.relPath
			if relPath == "" {
				relPath = displaySoundEffectFamily(family)
			}
			files = append(files, sfxDriveFile{
				ID: entry.ID, Name: entry.Name, FolderName: relPath, Family: family, Size: entry.Size,
			})
		}
	}
	return files, nil
}

func isAudioSFXName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3", ".wav", ".m4a", ".aac", ".ogg", ".flac", ".mp4", ".mov":
		return true
	default:
		return false
	}
}

// sfxFamilyFromFolder maps the Drive display folder to the canonical taxonomy
// family used by the selector. Unknown folders keep their normalized name so a
// new family is indexed instead of silently falling into "misc".
func sfxFamilyFromFolder(folder string) string {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(folder, "_", " ")))
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch normalized {
	case "":
		return ""
	case "background music", "music":
		return "music"
	case "cinematic impacts":
		return "impact"
	case "human vocals":
		return "vocals"
	case "objects toys":
		return "objects"
	case "magical fantasy":
		return "magical"
	case "orchestral hits":
		return "orchestral"
	default:
		return strings.ReplaceAll(normalized, " ", "_")
	}
}

func downloadSFXFile(ctx context.Context, reader drive.Reader, driveID, dest string) error {
	body, _, err := reader.DownloadFile(ctx, driveID)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".sfx-index-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := io.Copy(tmp, body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

func indexSFXFile(ctx context.Context, root *wiring.ComposeRoot, file sfxDriveFile, localPath, hash string, duration time.Duration) error {
	taxonomy := detail.ClassifySoundEffect(file.Name, file.Family, nil)
	now := time.Now().UTC()
	name := strings.TrimSuffix(file.Name, filepath.Ext(file.Name))
	clip := &asset.Asset{
		ID:       file.ID,
		Name:     name,
		Filename: file.Name,
		Source:   asset.Source("sound_effect"),
		// The canonical taxonomy gate admits only audio/image/video/text/
		// document, so an effect is stored as `audio` and identified by its
		// source (`sound_effect`) plus the `asset_kind=sfx` declaration. Using
		// MediaTypeSoundEffect here made every indexable commit fail closed
		// with ErrAssetCommitIndexTaxonomyRequired.
		MediaType:      asset.MediaTypeAudio,
		Category:       "file",
		Group:          taxonomy.Family,
		Duration:       duration,
		Tags:           taxonomy.Tags,
		LifecycleState: asset.StateActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	clip.SearchTerms = taxonomy.Tags
	clip.SearchText = strings.Join([]string{
		name, taxonomy.Family, taxonomy.Subtype, taxonomy.Mood, taxonomy.Energy,
		strings.Join(taxonomy.BestFor, " "), strings.Join(taxonomy.Tags, " "),
	}, " ")
	clip.SetDriveFileID(file.ID)
	clip.SetDriveLink("https://drive.google.com/file/d/" + file.ID + "/view")
	clip.SetDownloadLink("https://drive.google.com/uc?export=download&id=" + file.ID)
	clip.SetLocalPath(localPath)
	clip.SetContentHash(hash)
	clip.SetFolderPath(filepath.ToSlash(filepath.Join("sound_effects", file.FolderName, file.Name)))
	clip.SetMetadataString("sfx_family", taxonomy.Family)
	clip.SetMetadataString("sfx_category", taxonomy.Family)
	clip.SetMetadataString("sfx_subtype", taxonomy.Subtype)
	clip.SetMetadataString("sfx_mood", taxonomy.Mood)
	clip.SetMetadataString("sfx_energy", taxonomy.Energy)
	clip.SetMetadataString("sfx_best_for", strings.Join(taxonomy.BestFor, ","))
	clip.SetMetadataString("sfx_tags", strings.Join(taxonomy.Tags, ","))
	clip.SetMetadataString("sfx_drive_folder", file.FolderName)
	clip.SetMetadataString("asset_kind", "sfx")
	clip.SetMetadataString("mime_type", "audio/"+strings.TrimPrefix(strings.ToLower(filepath.Ext(file.Name)), "."))
	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
		return fmt.Errorf("index sound effect %s: %w", file.Name, err)
	}
	return nil
}

// sfxIndexEntry is the operator/AI-facing projection of one indexed effect.
type sfxIndexEntry struct {
	AssetID         string   `json:"asset_id"`
	Name            string   `json:"name"`
	Filename        string   `json:"filename"`
	DriveFileID     string   `json:"drive_file_id"`
	DriveLink       string   `json:"drive_link"`
	Family          string   `json:"family"`
	Subtype         string   `json:"subtype"`
	Mood            string   `json:"mood"`
	Energy          string   `json:"energy"`
	BestFor         []string `json:"best_for,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	DurationSeconds float64  `json:"duration_seconds"`
	FolderPath      string   `json:"folder_path,omitempty"`
}

type sfxIndexDocument struct {
	Version     int             `json:"version"`
	GeneratedAt string          `json:"generated_at"`
	DriveRoot   string          `json:"drive_root"`
	Count       int             `json:"count"`
	Families    map[string]int  `json:"families"`
	Assets      []sfxIndexEntry `json:"assets"`
}

// writeSFXIndex projects the committed rows into the JSON index. It reads the
// media SSOT through the shared SoundEffectCatalog, so the file can never
// describe an asset the resolver cannot materialize and the sound-effect SQL
// keeps a single owner.
func writeSFXIndex(ctx context.Context, catalog *pgmedia.SoundEffectCatalog, rootID, outPath string) error {
	if catalog == nil {
		return fmt.Errorf("media PostgreSQL SSOT is required to write the SFX index")
	}
	rows, err := catalog.ListProjectionRows(ctx)
	if err != nil {
		return fmt.Errorf("list indexed sound effects: %w", err)
	}

	doc := sfxIndexDocument{
		Version:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		DriveRoot:   rootID,
		Families:    map[string]int{},
		Assets:      make([]sfxIndexEntry, 0, len(rows)),
	}
	for _, row := range rows {
		entry := sfxIndexEntry{
			AssetID:         row.AssetID,
			Name:            row.Name,
			Filename:        row.Filename,
			DriveFileID:     row.DriveFileID,
			DriveLink:       row.DriveLink,
			Family:          row.Family,
			Subtype:         row.Subtype,
			Mood:            row.Mood,
			Energy:          row.Energy,
			BestFor:         splitCommaList(row.BestFor),
			Tags:            splitCommaList(row.Tags),
			DurationSeconds: float64(row.DurationMS) / 1000,
			FolderPath:      row.FolderPath,
		}
		doc.Assets = append(doc.Assets, entry)
		doc.Families[entry.Family]++
	}
	// Presentation order is a property of this export (family, then name);
	// the catalog deliberately returns id order.
	sort.SliceStable(doc.Assets, func(i, j int) bool {
		if doc.Assets[i].Family != doc.Assets[j].Family {
			return doc.Assets[i].Family < doc.Assets[j].Family
		}
		return doc.Assets[i].Name < doc.Assets[j].Name
	})
	doc.Count = len(doc.Assets)
	payload, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sound effect index: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create index directory: %w", err)
	}
	// Atomic write: the index is a generated artifact, and a truncated file
	// would describe a library that no longer matches the media SSOT
	// (pkg/atomicwrite enforcement test).
	if err := atomicwrite.WriteFile(outPath, payload, 0o644); err != nil {
		return fmt.Errorf("write sound effect index: %w", err)
	}
	fmt.Printf("SFX index written: path=%s assets=%d families=%d\n", outPath, doc.Count, len(doc.Families))
	return nil
}

func splitCommaList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.ToLower(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
