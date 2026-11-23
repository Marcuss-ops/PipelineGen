package soundeffects

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

type NewSFXItem struct {
	DriveID       string
	OriginalName  string
	SuggestedName string
	Category      string
	Subcategory   string
	Description   string
	Tags          []string
}

type ReorgSFXItem struct {
	DriveID       string
	OriginalName  string
	SuggestedName string
	Category      string
	Subcategory   string
	Description   string
	Tags          []string
}

// uiDriveFolderID is the shared UI-sounds folder. The root folder ID lives in
// organize_sound_effects_drive.go: both commands act on the same tree, so the
// root must have exactly one declaration.
const uiDriveFolderID = "1YLE7cVlXUwA9Sa4dqJbkaSGQ2colp-Wy"

// cartoonSFXSubfolderIDs are the pre-existing Cartoon subfolders (Anime, Meme,
// Comico, Cartoni). A missing key yields "", so the move fails visibly instead
// of materializing a second folder tree.
var cartoonSFXSubfolderIDs = map[string]string{
	"Anime":   "1x88rRIou0zWOdpZCr2TEh8hXZAv-WbH6",
	"Meme":    "18EGpeeCkfQCG4_2ddgK2SHDb4MCcGo2n",
	"Comico":  "1eiK7PSDsOI4RTiTjiaSMS3NBZ9d81jgZ",
	"Cartoni": "1kFWcCHdb2C0ThJWrEYsxFi_RCgIPuLI2",
}

// newSFXItems is the first workload: files already in the Drive tree that only
// need a canonical name, a folder and an index entry.
var newSFXItems = []NewSFXItem{

	{
		DriveID:       "1OhLwthiEdJR5EVNHZ5nbFQlFr38AFFkN",
		OriginalName:  "_What_ Bottom Text.m4a",
		SuggestedName: "retro_game_laser_shoot.m4a",
		Category:      "sfx",
		Subcategory:   "sci_fi_ui",
		Description:   "Classic 8-bit or 16-bit electronic laser blast, reminiscent of vintage arcade space shooters.",
		Tags:          []string{"laser", "retro", "arcade", "sci-fi", "8-bit", "shoot", "game"},
	},
	{
		DriveID:       "1Gp1ue8wLDdQcRowBxC3bkRQxDTZAPtow",
		OriginalName:  "cartoon-toy-whistle.wav",
		SuggestedName: "cartoon_slide_whistle.wav",
		Category:      "sfx",
		Subcategory:   "cartoon_comedy",
		Description:   "Classic ascending and descending slide whistle sound, typical of vintage cartoons and comedic moments.",
		Tags:          []string{"cartoon", "whistle", "slide", "funny", "comedy", "retro", "accent"},
	},
	{
		DriveID:       "1GS5oIf0t7XNBnGqpY1GLY6o9zSjeuesD",
		OriginalName:  "FLASHBACK.wav",
		SuggestedName: "magical_sparkle_chime.wav",
		Category:      "sfx",
		Subcategory:   "magical_fantasy",
		Description:   "Glissando of high-pitched chimes and shimmers, perfect for magic spells, transformations, or transitions.",
		Tags:          []string{"magic", "chimes", "sparkle", "glissando", "fantasy", "bright", "transition"},
	},
	{
		DriveID:       "17uwNw-tNLY01Aoczczp_mKfJjsaMoWHs",
		OriginalName:  "taco bell.m4a",
		SuggestedName: "cinematic_sub_boom.m4a",
		Category:      "sfx",
		Subcategory:   "cinematic_impacts",
		Description:   "Deep, low-frequency sub-bass hit with an echoing tail, designed for dramatic emphasis or movie trailers.",
		Tags:          []string{"impact", "sub-bass", "boom", "cinematic", "heavy", "trailer", "tension"},
	},
	{
		DriveID:       "1zWvh-HZKOU-aLJCuQLNSE6kdCwK7R8Mw",
		OriginalName:  "awww.mp3",
		SuggestedName: "crowd_disappointed_aww.mp3",
		Category:      "sfx",
		Subcategory:   "human_vocals",
		Description:   "A collective, sympathetic, or disappointed 'aww' sound from a small crowd or studio audience.",
		Tags:          []string{"crowd", "vocals", "audience", "disappointment", "sad", "reaction", "tv"},
	},
	{
		DriveID:       "1uflDEEC1BuYnFoWIxXcqfNsDHb2ZlOjW",
		OriginalName:  "faahhhhh.mp3",
		SuggestedName: "buzzer_wrong_answer.mp3",
		Category:      "sfx",
		Subcategory:   "ui_notifications",
		Description:   "Harsh, low electronic buzz indicating an incorrect response, failure, or time expiration.",
		Tags:          []string{"buzzer", "wrong", "error", "fail", "game-show", "alert", "incorrect"},
	},
	{
		DriveID:       "1ccwMih8dUjp1K_dJ3KGyc8YeV_dEMHVF",
		OriginalName:  "Nope sound effect.mp3",
		SuggestedName: "vocal_male_nope.mp3",
		Category:      "sfx",
		Subcategory:   "human_vocals",
		Description:   "A flat, direct, and comedic male voice saying 'Nope' to indicate refusal or failure.",
		Tags:          []string{"nope", "vocal", "male", "funny", "fail", "reaction", "speech"},
	},
	{
		DriveID:       "1Rk0JtHvuScGSgvmoFzfPvzQ6chp-53Y3",
		OriginalName:  "Spider-Man 3 Meme Sound.mp3",
		SuggestedName: "dramatic_orchestral_reveal.mp3",
		Category:      "music",
		Subcategory:   "orchestral_hits",
		Description:   "Sudden and intense orchestral swell followed by a sharp hit, maximizing suspense or sudden realizations.",
		Tags:          []string{"orchestra", "dramatic", "suspense", "hit", "reveal", "sting", "shock"},
	},
	{
		DriveID:       "1MyI9E0eovary0YiuA9yLrXJRdOnxE3MS",
		OriginalName:  "TOY DUCK.mp3",
		SuggestedName: "cute_rubber_duck_squeak.mp3",
		Category:      "sfx",
		Subcategory:   "objects_toys",
		Description:   "High-pitched, clean squeak of a classic rubber bath duck being squeezed twice.",
		Tags:          []string{"toy", "duck", "squeak", "cute", "rubber", "kids", "playful"},
	},
}

// reorganizedSFXItems is the second workload: floating Cartoon SFX that already
// exist in the catalog (or not) and must be renamed, moved and re-indexed.
var reorganizedSFXItems = []ReorgSFXItem{

	{
		DriveID:       "1yRGRX5Y2MuOS7m28mkEhcrRl7yEiNOoj",
		OriginalName:  "Disappointed.mp3",
		SuggestedName: "sfx_cartoon_disappointed_horn.mp3",
		Category:      "sfx",
		Subcategory:   "Comico",
		Description:   "Il classico effetto sonoro della tromba discendente calante (sad trombone), usato per enfatizzare un fallimento o una delusione in chiave comica.",
		Tags:          []string{"tromba", "cartoon", "comico", "fail", "delusione", "sad_trombone"},
	},
	{
		DriveID:       "1m9sfpCH_hpclEZJcD29_sOWyFR2qo43D",
		OriginalName:  "Among us sound effect.mp3",
		SuggestedName: "sfx_gaming_among_us_dead.mp3",
		Category:      "sfx",
		Subcategory:   "Meme",
		Description:   "L'effetto sonoro synth drammatico e pungente tratto da Among Us, associato alla scoperta di un corpo o all'eliminazione.",
		Tags:          []string{"among_us", "gaming", "synth", "allarme", "suspense", "meme"},
	},
	{
		DriveID:       "1nWtGtXfZMIDbTWrT9zhn-gQLHBATyKKb",
		OriginalName:  "AUGGHH  AHHHHH sound effect.mp3",
		SuggestedName: "sfx_vocals_auughh_snore.mp3",
		Category:      "sfx",
		Subcategory:   "Meme",
		Description:   "Il celebre effetto sonoro meme del lamento/russamento grottesco e distorto ('AUGGHH'), usato per reazioni assurde o di sfinimento comico.",
		Tags:          []string{"auughh", "rutto", "russare", "voce", "meme", "comico"},
	},
	{
		DriveID:       "1lij1oARt92GZEXZmiLO2aXVh-FM9kxQI",
		OriginalName:  "Awkward Pause 02.mp3",
		SuggestedName: "sfx_ambience_awkward_crickets.mp3",
		Category:      "sfx",
		Subcategory:   "Comico",
		Description:   "Suono minimale e ripetitivo di grilli notturni, utilizzato universalmente per sottolineare un silenzio imbarazzante o una battuta fallita.",
		Tags:          []string{"grilli", "silenzio", "imbarazzo", "awkward", "pausa", "comico"},
	},
	{
		DriveID:       "1iqekszmyp2bVtQIyvaiKVSICs01Ve6Ds",
		OriginalName:  "Bamboo Hit (AnimeCartoon Bonk) - Sound Effect for Editing.mp3",
		SuggestedName: "sfx_cartoon_bamboo_bonk.mp3",
		Category:      "sfx",
		Subcategory:   "Cartoni",
		Description:   "Un colpo secco e legnoso in stile 'bonk' di bambù, tipico degli anime o dei cartoni per indicare una bastonata in testa o un impatto buffo.",
		Tags:          []string{"bonk", "bambu", "legno", "colpo", "anime", "cartoon"},
	},
	{
		DriveID:       "1PpyjIolyjFhS7TusqJeC28JV80RLCBGw",
		OriginalName:  "Bruh Sound Effect.mp3",
		SuggestedName: "sfx_vocals_bruh_reaction.mp3",
		Category:      "sfx",
		Subcategory:   "Meme",
		Description:   "L'intramontabile effetto sonoro vocale 'Bruh', utilizzato come reazione immediata a situazioni stupide, deludenti o prive di senso.",
		Tags:          []string{"bruh", "meme", "reazione", "voce", "ironico", "social"},
	},
	{
		DriveID:       "1BidNxLQhn_8KNYLkckTPyw5z9tIM-tnd",
		OriginalName:  "Core.mp3",
		SuggestedName: "sfx_bass_distorted_boom.mp3",
		Category:      "sfx",
		Subcategory:   "Meme",
		Description:   "Un impatto di basso profondo, distorto e pesantemente amplificato (bass boosted), ideale per climax di meme o shock improvvisi.",
		Tags:          []string{"bass_boost", "basso", "distorto", "core", "impatto", "boom"},
	},
}

func RunReorganizeAndIndexSFX(args []string) error {
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

	ctx := context.Background()
	dataDir := cfg.Storage.DataDir

	fmt.Println("--- Phase 1: Downloading and Indexing new SFX list ---")
	for _, item := range newSFXItems {
		if err := indexNewSFXItem(ctx, root, dataDir, item); err != nil {
			return err
		}
	}

	fmt.Println("\n--- Phase 2: Reorganizing, renaming and Indexing existing floating Cartoon SFX ---")
	for _, item := range reorganizedSFXItems {
		if err := reorganizeAndIndexSFXItem(ctx, root, dataDir, item); err != nil {
			return err
		}
	}

	fmt.Println("\nAll SFX files reorganized, renamed and indexed successfully!")
	return nil
}

// downloadSFXToDisk materializes one Drive file under media/sound_effects with
// its canonical lowercase name and returns the local path and SHA-256. errLabel
// keeps the two phases' operator-facing error prefixes ("download file" vs
// "download existing file") distinct.
func downloadSFXToDisk(ctx context.Context, reader drive.Reader, dataDir, driveID, originalName, suggestedName, errLabel string) (string, string, error) {
	fmt.Printf("Processing %s (Drive ID: %s)...\n", originalName, driveID)

	body, _, err := reader.DownloadFile(ctx, driveID)
	if err != nil {
		return "", "", fmt.Errorf("%s %s: %w", errLabel, originalName, err)
	}
	defer func() { _ = body.Close() }()

	sfxDir := filepath.Join(dataDir, "media", "sound_effects")
	_ = os.MkdirAll(sfxDir, 0755)
	localPath := filepath.Join(sfxDir, strings.ToLower(suggestedName))

	f, err := os.Create(localPath)
	if err != nil {
		return "", "", fmt.Errorf("create local file: %w", err)
	}
	_, copyErr := io.Copy(f, body)
	_ = f.Close()
	if copyErr != nil {
		return "", "", fmt.Errorf("write local file: %w", copyErr)
	}

	hash, err := cli.Sha256File(localPath)
	if err != nil {
		return "", "", fmt.Errorf("hash file: %w", err)
	}
	return localPath, hash, nil
}

// newSFXTargetFolder resolves where a new SFX belongs: the shared UI folder,
// the Cartoni subfolder for comedy stings, or a Title-cased subfolder derived
// from the subcategory (created on demand).
func newSFXTargetFolder(ctx context.Context, root *wiring.ComposeRoot, item NewSFXItem) (string, error) {
	switch item.Subcategory {
	case "sci_fi_ui", "ui_notifications":
		return uiDriveFolderID, nil
	case "cartoon_comedy":
		return cartoonSFXSubfolderIDs["Cartoni"], nil
	default:
		folderName := cases.Title(language.Und).String(strings.ReplaceAll(item.Subcategory, "_", " "))
		folderID, err := root.Drive.Admin.GetOrCreateFolder(ctx, folderName, soundEffectsDriveFolderID)
		if err != nil {
			return "", fmt.Errorf("create sound effect subfolder: %w", err)
		}
		return folderID, nil
	}
}

func indexNewSFXItem(ctx context.Context, root *wiring.ComposeRoot, dataDir string, item NewSFXItem) error {
	localPath, hash, err := downloadSFXToDisk(ctx, root.Drive.Reader, dataDir, item.DriveID, item.OriginalName, item.SuggestedName, "download file")
	if err != nil {
		return err
	}

	targetFolderID, err := newSFXTargetFolder(ctx, root, item)
	if err != nil {
		return err
	}

	if err := root.Drive.Lifecycle.Rename(ctx, item.DriveID, strings.ToLower(item.SuggestedName)); err != nil {
		fmt.Printf("Warning: rename drive file failed: %v\n", err)
	}

	if err := root.Drive.Admin.MoveFile(ctx, item.DriveID, soundEffectsDriveFolderID, targetFolderID); err != nil {
		_ = root.Drive.Admin.MoveFile(ctx, item.DriveID, uiDriveFolderID, targetFolderID)
	}

	now := time.Now().UTC()
	clip := &asset.Asset{ID: item.DriveID, CreatedAt: now}
	applySFXAssetSpec(clip, sfxAssetSpec{
		driveID:        item.DriveID,
		suggestedName:  item.SuggestedName,
		subcategory:    item.Subcategory,
		description:    item.Description,
		tags:           item.Tags,
		group:          item.Subcategory,
		family:         "sfx",
		searchPrefix:   "sfx",
		targetFolderID: targetFolderID,
		localPath:      localPath,
		hash:           hash,
	}, now)
	clip.Duration = 5 * time.Second

	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
		fmt.Printf("Warning: failed to index %s: %v\n", strings.ToLower(item.SuggestedName), err)
	} else {
		fmt.Printf("Indexed successfully: %s -> FolderID: %s\n", strings.ToLower(item.SuggestedName), targetFolderID)
	}
	return nil
}

func reorganizeAndIndexSFXItem(ctx context.Context, root *wiring.ComposeRoot, dataDir string, item ReorgSFXItem) error {
	localPath, hash, err := downloadSFXToDisk(ctx, root.Drive.Reader, dataDir, item.DriveID, item.OriginalName, item.SuggestedName, "download existing file")
	if err != nil {
		return err
	}

	if err := root.Drive.Lifecycle.Rename(ctx, item.DriveID, strings.ToLower(item.SuggestedName)); err != nil {
		fmt.Printf("Warning: rename drive file failed: %v\n", err)
	}

	targetFolderID := cartoonSFXSubfolderIDs[item.Subcategory]
	_ = root.Drive.Admin.MoveFile(ctx, item.DriveID, soundEffectsDriveFolderID, targetFolderID)

	clip, err := root.GetMediaClip(ctx, item.DriveID)
	if err != nil {
		return fmt.Errorf("get clip record: %w", err)
	}

	now := time.Now().UTC()
	if clip == nil {
		clip = &asset.Asset{ID: item.DriveID, CreatedAt: now}
	}
	applySFXAssetSpec(clip, sfxAssetSpec{
		driveID:        item.DriveID,
		suggestedName:  item.SuggestedName,
		subcategory:    item.Subcategory,
		description:    item.Description,
		tags:           item.Tags,
		group:          "Cartoon",
		family:         "cartoon",
		searchPrefix:   "sfx Cartoon",
		targetFolderID: targetFolderID,
		localPath:      localPath,
		hash:           hash,
	}, now)

	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
		fmt.Printf("Warning: failed to index %s: %v\n", strings.ToLower(item.SuggestedName), err)
	} else {
		fmt.Printf("Successfully updated, moved and indexed %s -> FolderID: %s\n", strings.ToLower(item.SuggestedName), targetFolderID)
	}
	return nil
}

// sfxAssetSpec is the canonical field set both phases write onto an asset. It
// exists so a field added to the index contract is added once, not twice with a
// chance of drifting between the "new" and the "reorganized" path.
type sfxAssetSpec struct {
	driveID        string
	suggestedName  string
	subcategory    string
	description    string
	tags           []string
	group          string
	family         string
	searchPrefix   string
	targetFolderID string
	localPath      string
	hash           string
}

func applySFXAssetSpec(clip *asset.Asset, spec sfxAssetSpec, now time.Time) {
	suggestedFilename := strings.ToLower(spec.suggestedName)
	nameWithoutExt := strings.TrimSuffix(suggestedFilename, filepath.Ext(suggestedFilename))
	clip.Name = nameWithoutExt
	clip.Filename = suggestedFilename
	clip.Source = asset.Source("sfx_drive")
	clip.MediaType = asset.MediaType("audio")
	clip.Category = "sfx"
	clip.Group = spec.group
	clip.Tags = spec.tags
	clip.LifecycleState = asset.StateActive
	clip.UpdatedAt = now
	clip.SearchTerms = spec.tags
	clip.SearchText = fmt.Sprintf("%s %s %s %s %s", nameWithoutExt, spec.searchPrefix, spec.subcategory, spec.description, strings.Join(spec.tags, " "))
	clip.SetDriveFileID(spec.driveID)
	clip.SetDriveLink("https://drive.google.com/file/d/" + spec.driveID + "/view")
	clip.SetDownloadLink("https://drive.google.com/uc?export=download&id=" + spec.driveID)
	clip.SetLocalPath(spec.localPath)
	clip.SetContentHash(spec.hash)
	clip.SetMetadataString("mime_type", "audio/mpeg")
	clip.SetMetadataString("sfx_family", spec.family)
	clip.SetMetadataString("sfx_category", spec.subcategory)
	clip.SetMetadataString("sfx_tags", strings.Join(spec.tags, ","))
	clip.SetMetadataString("parent_folder_id", spec.targetFolderID)
	clip.SetMetadataString("description", spec.description)
}
