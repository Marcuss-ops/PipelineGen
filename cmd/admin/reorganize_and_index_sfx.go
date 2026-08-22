package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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

func runReorganizeAndIndexSFX(args []string) error {
	list1 := []NewSFXItem{
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

	list2 := []ReorgSFXItem{
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

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()

	ctx := context.Background()

	soundEffectsDriveFolderID := "1vfZQHVNZab-pU2fBaj4qzR3iSz1sOVhW"
	uiDriveFolderID := "1YLE7cVlXUwA9Sa4dqJbkaSGQ2colp-Wy"

	// Cartoon subfolder IDs (Anime, Meme, Comico, Cartoni)
	cartoonSubfolderIDs := make(map[string]string)
	for sfName, sfID := range map[string]string{
		"Anime":   "1x88rRIou0zWOdpZCr2TEh8hXZAv-WbH6",
		"Meme":    "18EGpeeCkfQCG4_2ddgK2SHDb4MCcGo2n",
		"Comico":  "1eiK7PSDsOI4RTiTjiaSMS3NBZ9d81jgZ",
		"Cartoni": "1kFWcCHdb2C0ThJWrEYsxFi_RCgIPuLI2",
	} {
		cartoonSubfolderIDs[sfName] = sfID
	}

	fmt.Println("--- Phase 1: Downloading and Indexing new SFX list ---")
	for _, item := range list1 {
		fmt.Printf("Processing %s (Drive ID: %s)...\n", item.OriginalName, item.DriveID)

		// 1. Download file
		body, _, err := root.Drive.Reader.DownloadFile(ctx, item.DriveID)
		if err != nil {
			return fmt.Errorf("download file %s: %w", item.OriginalName, err)
		}

		sfxDir := filepath.Join(cfg.Storage.DataDir, "media", "sound_effects")
		_ = os.MkdirAll(sfxDir, 0755)

		suggestedFilename := strings.ToLower(item.SuggestedName)
		localPath := filepath.Join(sfxDir, suggestedFilename)

		f, err := os.Create(localPath)
		if err != nil {
			body.Close()
			return fmt.Errorf("create local file: %w", err)
		}

		_, err = io.Copy(f, body)
		f.Close()
		body.Close()
		if err != nil {
			return fmt.Errorf("write local file: %w", err)
		}

		hash, err := sha256File(localPath)
		if err != nil {
			return fmt.Errorf("hash file: %w", err)
		}

		// 2. Upload to correct folder or rename in place
		var targetFolderID string
		if item.Subcategory == "sci_fi_ui" || item.Subcategory == "ui_notifications" {
			targetFolderID = uiDriveFolderID
		} else if item.Subcategory == "cartoon_comedy" {
			targetFolderID = cartoonSubfolderIDs["Cartoni"]
		} else {
			folderName := strings.Title(strings.ReplaceAll(item.Subcategory, "_", " "))
			targetFolderID, err = root.Drive.Admin.GetOrCreateFolder(ctx, folderName, soundEffectsDriveFolderID)
			if err != nil {
				return fmt.Errorf("create sound effect subfolder: %w", err)
			}
		}

		if err := root.Drive.Admin.RenameFile(ctx, item.DriveID, suggestedFilename); err != nil {
			fmt.Printf("Warning: rename drive file failed: %v\n", err)
		}

		if err := root.Drive.Admin.MoveFile(ctx, item.DriveID, soundEffectsDriveFolderID, targetFolderID); err != nil {
			_ = root.Drive.Admin.MoveFile(ctx, item.DriveID, uiDriveFolderID, targetFolderID)
		}

		// 3. Index asset
		now := time.Now().UTC()
		nameWithoutExt := strings.TrimSuffix(suggestedFilename, filepath.Ext(suggestedFilename))
		clip := &asset.Asset{
			ID:             item.DriveID,
			Name:           nameWithoutExt,
			Filename:       suggestedFilename,
			Source:         asset.Source("sfx_drive"),
			MediaType:      asset.MediaType("audio"),
			Category:       "sfx",
			Group:          item.Subcategory,
			Duration:       5 * time.Second,
			Tags:           item.Tags,
			LifecycleState: asset.StateActive,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		clip.SearchTerms = item.Tags
		clip.SearchText = fmt.Sprintf("%s sfx %s %s %s", nameWithoutExt, item.Subcategory, item.Description, strings.Join(item.Tags, " "))
		clip.SetDriveFileID(item.DriveID)
		clip.SetDriveLink("https://drive.google.com/file/d/" + item.DriveID + "/view")
		clip.SetDownloadLink("https://drive.google.com/uc?export=download&id=" + item.DriveID)
		clip.SetLocalPath(localPath)
		clip.SetLegacyFileMD5(hash)
		clip.SetMetadataString("mime_type", "audio/mpeg")
		clip.SetMetadataString("sfx_family", "sfx")
		clip.SetMetadataString("sfx_category", item.Subcategory)
		clip.SetMetadataString("sfx_tags", strings.Join(item.Tags, ","))
		clip.SetMetadataString("parent_folder_id", targetFolderID)
		clip.SetMetadataString("description", item.Description)

		if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
			fmt.Printf("Warning: failed to index %s: %v\n", suggestedFilename, err)
		} else {
			fmt.Printf("Indexed successfully: %s -> FolderID: %s\n", suggestedFilename, targetFolderID)
		}
	}

	fmt.Println("\n--- Phase 2: Reorganizing, renaming and Indexing existing floating Cartoon SFX ---")
	for _, item := range list2 {
		fmt.Printf("Processing %s (Drive ID: %s)...\n", item.OriginalName, item.DriveID)

		// 1. Download file
		body, _, err := root.Drive.Reader.DownloadFile(ctx, item.DriveID)
		if err != nil {
			return fmt.Errorf("download existing file %s: %w", item.OriginalName, err)
		}

		sfxDir := filepath.Join(cfg.Storage.DataDir, "media", "sound_effects")
		_ = os.MkdirAll(sfxDir, 0755)

		suggestedFilename := strings.ToLower(item.SuggestedName)
		localPath := filepath.Join(sfxDir, suggestedFilename)

		f, err := os.Create(localPath)
		if err != nil {
			body.Close()
			return fmt.Errorf("create local file: %w", err)
		}
		_, err = io.Copy(f, body)
		f.Close()
		body.Close()
		if err != nil {
			return fmt.Errorf("write local file: %w", err)
		}

		hash, err := sha256File(localPath)
		if err != nil {
			return fmt.Errorf("hash file: %w", err)
		}

		// 2. Rename Drive file to suggested name
		if err := root.Drive.Admin.RenameFile(ctx, item.DriveID, suggestedFilename); err != nil {
			fmt.Printf("Warning: rename drive file failed: %v\n", err)
		}

		// 3. Move Drive file to cartoon subfolder (Comico, Meme, Cartoni, Anime)
		targetFolderID := cartoonSubfolderIDs[item.Subcategory]
		_ = root.Drive.Admin.MoveFile(ctx, item.DriveID, soundEffectsDriveFolderID, targetFolderID)

		// 4. Index asset
		clip, err := root.Repos.ClipsRepo.GetClip(ctx, item.DriveID)
		if err != nil {
			return fmt.Errorf("get clip record: %w", err)
		}

		now := time.Now().UTC()
		nameWithoutExt := strings.TrimSuffix(suggestedFilename, filepath.Ext(suggestedFilename))
		if clip == nil {
			clip = &asset.Asset{
				ID:        item.DriveID,
				CreatedAt: now,
			}
		}

		clip.Name = nameWithoutExt
		clip.Filename = suggestedFilename
		clip.Source = asset.Source("sfx_drive")
		clip.MediaType = asset.MediaType("audio")
		clip.Category = "sfx"
		clip.Group = "Cartoon"
		clip.Tags = item.Tags
		clip.LifecycleState = asset.StateActive
		clip.UpdatedAt = now
		clip.SearchTerms = item.Tags
		clip.SearchText = fmt.Sprintf("%s sfx Cartoon %s %s %s", nameWithoutExt, item.Subcategory, item.Description, strings.Join(item.Tags, " "))
		clip.SetDriveFileID(item.DriveID)
		clip.SetDriveLink("https://drive.google.com/file/d/" + item.DriveID + "/view")
		clip.SetDownloadLink("https://drive.google.com/uc?export=download&id=" + item.DriveID)
		clip.SetLocalPath(localPath)
		clip.SetLegacyFileMD5(hash)
		clip.SetMetadataString("mime_type", "audio/mpeg")
		clip.SetMetadataString("sfx_family", "cartoon")
		clip.SetMetadataString("sfx_category", item.Subcategory)
		clip.SetMetadataString("sfx_tags", strings.Join(item.Tags, ","))
		clip.SetMetadataString("parent_folder_id", targetFolderID)
		clip.SetMetadataString("description", item.Description)

		if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
			fmt.Printf("Warning: failed to index %s: %v\n", suggestedFilename, err)
		} else {
			fmt.Printf("Successfully updated, moved and indexed %s -> FolderID: %s\n", suggestedFilename, targetFolderID)
		}
	}

	fmt.Println("\nAll SFX files reorganized, renamed and indexed successfully!")
	return nil
}
