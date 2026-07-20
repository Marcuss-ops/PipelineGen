package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

type KidsMusicMetadata struct {
	DriveID      string
	OriginalName string
	Category     string
	Subcategory  string
	Description  string
	Tags         []string
}

func runIndexKidsMusicMetadata(args []string) error {
	metadataList := []KidsMusicMetadata{
		{
			DriveID:      "1uNj528n86OA8oPIVXErSz3SJSedpk761",
			OriginalName: "sfx_music_kids_splashing_around_01.mp3",
			Category:     "music",
			Subcategory:  "kids_background",
			Description:  "Cheerful and bouncy glockenspiel melody, perfect for playful outdoor activities or water fun scenes.",
			Tags:         []string{"kids", "playful", "happy", "glockenspiel", "bright", "upbeat", "splashing"},
		},
		{
			DriveID:      "19_-yDyTPdaED9O3e2CLzzal_TIeRtZuI",
			OriginalName: "sfx_music_kids_after_school_jamboree_01.mp3",
			Category:     "music",
			Subcategory:  "kids_background",
			Description:  "Quirky and rhythmic old-school cartoon track, great for funny transitions or mischievous moments.",
			Tags:         []string{"kids", "jamboree", "cartoon", "quirky", "retro", "funny", "rhythmic"},
		},
		{
			DriveID:      "1catJ78Ve0F9QxgeJfMhdp6bflDS4TLjl",
			OriginalName: "sfx_music_kids_bike_rides_01.mp3",
			Category:     "music",
			Subcategory:  "kids_background",
			Description:  "Bright acoustic track driven by ukulele and a steady rhythm, evoking a sunny bike ride or summer afternoon.",
			Tags:         []string{"kids", "ukulele", "sunny", "acoustic", "happy", "carefree", "outdoor"},
		},
		{
			DriveID:      "11UkYi35l01iP5wFzG7TPdFQW5d_hr0M0",
			OriginalName: "sfx_music_kids_bunny_hop_01.mp3",
			Category:     "music",
			Subcategory:  "kids_background",
			Description:  "Playful hopping rhythm with soft synths, ideal for simple animations, jumping animals, or interactive games.",
			Tags:         []string{"kids", "bunny hop", "playful", "synth", "cute", "jumping", "game"},
		},
		{
			DriveID:      "1AF4zJlndCX_G-geEA_Q94xMMjMiJ1dik",
			OriginalName: "sfx_music_kids_claudio_the_worm_01.mp3",
			Category:     "music",
			Subcategory:  "kids_background",
			Description:  "Cute and curious little march with a relaxed, slightly whimsical tone, perfect for storytelling or cartoons.",
			Tags:         []string{"kids", "march", "whimsical", "cute", "curious", "storytelling", "cartoon"},
		},
		{
			DriveID:      "1age5XewoBELovLIp3g0vmcPi3xz2S0o5",
			OriginalName: "sfx_music_kids_itsy_bitsy_spider_01.mp3",
			Category:     "music",
			Subcategory:  "kids_background",
			Description:  "Instrumental track with a relaxed yet playful chill-beat rhythm, excellent for modern nursery rhymes.",
			Tags:         []string{"kids", "nursery rhyme", "chill", "beat", "playful", "spider", "instrumental"},
		},
		{
			DriveID:      "1zrMoov4UZRcqGE0Sw6bLywttjrCqqZnt",
			OriginalName: "sfx_music_kids_lovable_clown_sit_com_01.mp3",
			Category:     "music",
			Subcategory:  "kids_background",
			Description:  "Expressive circus-style comedy fanfare, great for sitcom intros, funny sketches, or character entrances.",
			Tags:         []string{"kids", "clown", "sitcom", "circus", "funny", "fanfare", "comedy"},
		},
		{
			DriveID:      "1oi3TWTyMmQbAwrSY6ry6sYE0xR5gEuY6",
			OriginalName: "sfx_music_kids_monkeys_spinning_monkeys_01.mp3",
			Category:     "music",
			Subcategory:  "kids_background",
			Description:  "The famous viral, lighthearted track with pizzicato flute, widely used for meme content, silly behaviors, or chaos.",
			Tags:         []string{"kids", "viral", "meme", "tiktok", "funny", "silly", "monkeys", "pizzicato"},
		},
		{
			DriveID:      "10FlTug0bPNeOOjykNvwQ4qLXLSSUdbwr",
			OriginalName: "sfx_music_kids_mr_turtle_01.mp3",
			Category:     "music",
			Subcategory:  "kids_background",
			Description:  "Slow, rhythmic, and whimsical track that mimics a turtle's slow pace. Great for educational animal videos.",
			Tags:         []string{"kids", "slow", "turtle", "educational", "cute", "whimsical", "calm"},
		},
		{
			DriveID:      "1DsVCkLdDzy2SMYq2XukAH8NhXk02tOCC",
			OriginalName: "sfx_music_kids_rainy_day_games_01.mp3",
			Category:     "music",
			Subcategory:  "kids_background",
			Description:  "Cozy, sweet yet rhythmic melody for afternoons spent playing indoors. Conveys creativity and carefree domestic vibes.",
			Tags:         []string{"kids", "cozy", "indoor", "rainy day", "sweet", "creative", "gentle"},
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

	if root == nil || root.Repos == nil || root.Repos.ClipsRepo == nil || root.Outbox == nil || root.Outbox.Dispatcher == nil {
		return fmt.Errorf("ClipsRepo and Outbox dispatcher are required")
	}

	ctx := context.Background()

	for _, m := range metadataList {
		fmt.Printf("Updating metadata for %s (DriveID: %s)...\n", m.OriginalName, m.DriveID)
		asset, err := root.Repos.ClipsRepo.GetClip(ctx, m.DriveID)
		if err != nil {
			return fmt.Errorf("retrieve asset %s: %w", m.OriginalName, err)
		}
		if asset == nil {
			fmt.Printf("Warning: asset %s (DriveID: %s) not found in database. Skipping.\n", m.OriginalName, m.DriveID)
			continue
		}

		// Update fields
		asset.Category = m.Category
		asset.Tags = m.Tags
		asset.SearchTerms = m.Tags
		asset.SearchText = fmt.Sprintf("%s %s %s %s %s", asset.Name, m.Category, m.Subcategory, m.Description, strings.Join(m.Tags, " "))
		asset.UpdatedAt = time.Now().UTC()

		asset.SetMetadataString("description", m.Description)
		asset.SetMetadataString("sfx_subcategory", m.Subcategory)
		asset.SetMetadataString("sfx_tags", strings.Join(m.Tags, ","))

		// Persist via transactional outbox to reconcile with Qdrant
		hash := ""
		if h, ok := asset.Metadata["file_hash"]; ok {
			hash, _ = h.(string)
		}
		if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, asset, hash); err != nil {
			return fmt.Errorf("index asset %s: %w", m.OriginalName, err)
		}
		fmt.Printf("Successfully updated and re-indexed %s\n", m.OriginalName)
	}

	fmt.Println("\nAll kids music metadata updated and indexed in Qdrant + SQLite successfully!")
	return nil
}
