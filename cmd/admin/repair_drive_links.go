// cmd/admin/repair_drive_links.go — repair broken Drive links in
// a completed script.generate job.
//
// Reads the result_json from the jobs table, extracts every
// SpecScene across all items, verifies every drive_link via the
// canonical AssetLocationResolver, and produces an audit report.
// With --remove-invalid, cleared links are persisted back to the
// job's result_json. With --refresh-docs, existing Google Docs
// are refreshed with the reconciled SpecScene.
//
// Usage:
//
//	admin repair-drive-links --job-id job_xxx
//	admin repair-drive-links --job-id job_xxx --remove-invalid --refresh-docs
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

func runRepairDriveLinks(args []string) error {
	fs := flag.NewFlagSet("repair-drive-links", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jobID := fs.String("job-id", "", "Job ID to repair (required)")
	removeInvalid := fs.Bool("remove-invalid", false, "Clear broken links in result_json and persist")
	refreshDocs := fs.Bool("refresh-docs", false, "Refresh existing Google Docs with reconciled SpecScene")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*jobID) == "" {
		return fmt.Errorf("--job-id is required")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	if root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive reader port is not available")
	}
	if root.DB == nil || root.DB.DB == nil {
		return fmt.Errorf("database port is not available")
	}

	resolver := drive.NewAssetLocationResolverAdapter(root.Drive.Reader)
	db := root.DB.DB
	ctx := cmdContext()

	// ── Step 1: Read the job ──────────────────────────────────
	fmt.Printf("=== Repair Drive Links ===\n")
	fmt.Printf("Job ID:    %s\n", *jobID)
	fmt.Printf("Remove:    %v\n", *removeInvalid)
	fmt.Printf("Refresh:   %v\n\n", *refreshDocs)

	fmt.Println("Step 1: Reading job...")
	var resultJSON string
	var status string
	err = db.QueryRowContext(ctx,
		"SELECT status, COALESCE(result_json, '{}') FROM jobs WHERE id = ?",
		*jobID,
	).Scan(&status, &resultJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("job not found: %s", *jobID)
		}
		return fmt.Errorf("failed to read job: %w", err)
	}
	fmt.Printf("  Status: %s\n", status)
	if resultJSON == "" || resultJSON == "{}" {
		return fmt.Errorf("job %s has no result_json", *jobID)
	}

	// ── Step 2: Parse result_json ─────────────────────────────
	fmt.Println("\nStep 2: Parsing result_json...")
	var envelope scriptpkg.GenerationEnvelopeResult
	if err := json.Unmarshal([]byte(resultJSON), &envelope); err != nil {
		return fmt.Errorf("failed to parse result_json as GenerationEnvelopeResult: %w", err)
	}
	fmt.Printf("  Items:    %d\n", len(envelope.Items))

	// ── Step 3: Collect all links ─────────────────────────────
	fmt.Println("\nStep 3: Collecting Drive links from SpecScene bindings...")
	type linkRef struct {
		itemIdx int
		sceneID string
		label   string
		assetID string
		fileID  string
		link    string
		linkPtr *string // pointer into the parsed struct for mutation
	}

	var links []linkRef
	for i := range envelope.Items {
		item := &envelope.Items[i]
		if item.Result == nil {
			continue
		}
		scenes := item.Result.Output.SpecScene.Scenes
		for j := range scenes {
			scene := &scenes[j]
			bindings := &scene.Bindings

			if bindings.Clip != nil {
				if l := strings.TrimSpace(bindings.Clip.DriveLink); l != "" {
					links = append(links, linkRef{
						itemIdx: i, sceneID: scene.ID,
						label: "clip", assetID: bindings.Clip.ClipID,
						link: l, linkPtr: &bindings.Clip.DriveLink,
					})
				}
				if l := strings.TrimSpace(bindings.Clip.SubtitleLink); l != "" {
					links = append(links, linkRef{
						itemIdx: i, sceneID: scene.ID,
						label:   "subtitle",
						assetID: bindings.Clip.ClipID,
						fileID:  bindings.Clip.SubtitleFileID,
						link:    l, linkPtr: &bindings.Clip.SubtitleLink,
					})
				}
			}
			if bindings.Stock != nil {
				if l := strings.TrimSpace(bindings.Stock.DriveLink); l != "" {
					links = append(links, linkRef{
						itemIdx: i, sceneID: scene.ID,
						label: "stock", assetID: bindings.Stock.AssetID,
						link: l, linkPtr: &bindings.Stock.DriveLink,
					})
				}
			}
			if bindings.Voiceover != nil {
				if l := strings.TrimSpace(bindings.Voiceover.Link); l != "" {
					links = append(links, linkRef{
						itemIdx: i, sceneID: scene.ID,
						label: "voiceover", assetID: "voiceover:" + scene.ID,
						link: l, linkPtr: &bindings.Voiceover.Link,
					})
				}
			}
			for k := range bindings.Media {
				if l := strings.TrimSpace(bindings.Media[k].DriveLink); l != "" {
					links = append(links, linkRef{
						itemIdx: i, sceneID: scene.ID,
						label:   fmt.Sprintf("media[%d]", k),
						assetID: bindings.Media[k].AssetID,
						link:    l, linkPtr: &bindings.Media[k].DriveLink,
					})
				}
			}
		}
	}
	fmt.Printf("  Links found: %d\n", len(links))
	if len(links) == 0 {
		fmt.Println("\nNo Drive links to verify. Done.")
		return nil
	}

	// ── Step 4: Verify every link ─────────────────────────────
	fmt.Println("\nStep 4: Verifying links against Drive API...")
	var (
		verified, updated, missing, trashed, inaccessible, malformed int
		transportErrors                                              int
	)

	for idx, ref := range links {
		result, err := resolver.ResolveAndVerify(ctx, ref.assetID, ref.fileID, ref.link)
		if err != nil {
			fmt.Printf("  [%d/%d] %-12s %s → TRANSPORT ERROR: %v\n", idx+1, len(links), ref.label, ref.sceneID, err)
			transportErrors++
			continue
		}
		if result == nil {
			continue
		}
		switch result.State {
		case scriptpkg.LocationStateVerified:
			fmt.Printf("  [%d/%d] %-12s %s → VERIFIED\n", idx+1, len(links), ref.label, ref.sceneID)
			verified++
		case scriptpkg.LocationStateUpdated:
			fmt.Printf("  [%d/%d] %-12s %s → UPDATED  (new: %s)\n", idx+1, len(links), ref.label, ref.sceneID, result.DriveLink)
			updated++
			if *removeInvalid {
				*ref.linkPtr = result.DriveLink
			}
		case scriptpkg.LocationStateMissing:
			fmt.Printf("  [%d/%d] %-12s %s → MISSING  (asset: %s)\n", idx+1, len(links), ref.label, ref.sceneID, ref.assetID)
			missing++
			if *removeInvalid {
				*ref.linkPtr = ""
			}
		case scriptpkg.LocationStateTrashed:
			fmt.Printf("  [%d/%d] %-12s %s → TRASHED\n", idx+1, len(links), ref.label, ref.sceneID)
			trashed++
			if *removeInvalid {
				*ref.linkPtr = ""
			}
		case scriptpkg.LocationStateInaccessible:
			fmt.Printf("  [%d/%d] %-12s %s → INACCESSIBLE\n", idx+1, len(links), ref.label, ref.sceneID)
			inaccessible++
			if *removeInvalid {
				*ref.linkPtr = ""
			}
		case scriptpkg.LocationStateMalformed:
			fmt.Printf("  [%d/%d] %-12s %s → MALFORMED\n", idx+1, len(links), ref.label, ref.sceneID)
			malformed++
			if *removeInvalid {
				*ref.linkPtr = ""
			}
		}
	}

	// ── Summary ───────────────────────────────────────────────
	fmt.Println("\n──────────────────────────────────────────────")
	fmt.Println("Summary")
	fmt.Println("──────────────────────────────────────────────")
	fmt.Printf("  Scenes inspected:        %d\n", countScenes(envelope))
	fmt.Printf("  Drive links inspected:   %d\n", len(links))
	fmt.Printf("  Valid links:             %d\n", verified)
	fmt.Printf("  Updated links:           %d\n", updated)
	fmt.Printf("  Removed links:           %d\n", missing+trashed+inaccessible+malformed)
	fmt.Printf("    Missing:               %d\n", missing)
	fmt.Printf("    Trashed:               %d\n", trashed)
	fmt.Printf("    Inaccessible:          %d\n", inaccessible)
	fmt.Printf("    Malformed:             %d\n", malformed)
	fmt.Printf("  Transport errors:        %d\n", transportErrors)

	// ── Step 5: Persist ───────────────────────────────────────
	if *removeInvalid && (updated > 0 || missing > 0 || trashed > 0 || inaccessible > 0 || malformed > 0) {
		fmt.Println("\nStep 5: Persisting reconciled result_json...")
		raw, err := json.Marshal(envelope)
		if err != nil {
			return fmt.Errorf("failed to marshal reconciled envelope: %w", err)
		}
		_, err = db.ExecContext(ctx,
			"UPDATE jobs SET result_json = ? WHERE id = ?",
			string(raw), *jobID)
		if err != nil {
			return fmt.Errorf("failed to update result_json: %w", err)
		}
		fmt.Println("  result_json updated successfully.")
	} else if *removeInvalid {
		fmt.Println("\nStep 5: No changes to persist.")
	}

	// ── Step 6: Refresh Google Docs ───────────────────────────
	if *refreshDocs {
		fmt.Println("\nStep 6: Refreshing Google Docs...")
		if root.Drive == nil || root.Drive.DocClient == nil {
			fmt.Println("  SKIPPED: DocClient not available.")
		} else {
			refreshed := 0
			for i := range envelope.Items {
				item := &envelope.Items[i]
				if item.Result == nil || item.Result.Provenance == nil {
					continue
				}
				docID := strings.TrimSpace(item.Result.Provenance.DocID)
				if docID == "" {
					continue
				}
				title := item.Result.Title
				if title == "" {
					title = "Reconciled script"
				}
				model := &scriptpkg.ModelScriptOutputV1{
					SchemaVersion: 1,
					Text:          item.Result.Output.Text,
					SpecScene:     item.Result.Output.SpecScene,
					WordCount:     item.Result.Output.WordCount,
				}
				content := buildRepairHTML(model, title)
				if err := root.Drive.DocClient.UpdateDoc(ctx, docID, title, content); err != nil {
					fmt.Printf("  [%d/%d] doc %s → FAILED: %v\n", i+1, len(envelope.Items), docID, err)
				} else {
					fmt.Printf("  [%d/%d] doc %s → REFRESHED\n", i+1, len(envelope.Items), docID)
					refreshed++
				}
			}
			fmt.Printf("  Documents refreshed: %d\n", refreshed)
		}
	}

	if transportErrors > 0 {
		return fmt.Errorf("completed with %d transport errors", transportErrors)
	}
	return nil
}

// countScenes returns the total number of scenes across all items.
func countScenes(envelope scriptpkg.GenerationEnvelopeResult) int {
	n := 0
	for i := range envelope.Items {
		if envelope.Items[i].Result != nil {
			n += len(envelope.Items[i].Result.Output.SpecScene.Scenes)
		}
	}
	return n
}

// buildRepairHTML renders a minimal SpecScene HTML document for the
// repair refresh. Uses the same structure as
// BuildSpecSceneDocumentHTML but without importing the adapters
// package (keeps the admin CLI free of application-layer deps).
func buildRepairHTML(model *scriptpkg.ModelScriptOutputV1, title string) string {
	if model == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")
	if strings.TrimSpace(title) != "" {
		b.WriteString("<h1>")
		writeEscaped(&b, strings.TrimSpace(title))
		b.WriteString("</h1>")
	}
	if len(model.SpecScene.Scenes) > 0 {
		b.WriteString("<h2>Scenes</h2>")
		for i := range model.SpecScene.Scenes {
			scene := &model.SpecScene.Scenes[i]
			b.WriteString("<section>")
			b.WriteString("<h3>")
			writeEscaped(&b, scene.ID)
			b.WriteString("</h3>")
			if text := strings.TrimSpace(scene.Text); text != "" {
				b.WriteString("<p>")
				writeEscaped(&b, text)
				b.WriteString("</p>")
			}
			if clip := scene.Bindings.Clip; clip != nil {
				b.WriteString("<p><strong>Clip:</strong> ")
				writeLink(&b, clip.DriveLink, clip.ClipTitle, clip.ClipID)
				b.WriteString("</p>")
				if clip.SubtitleLink != "" {
					b.WriteString("<p><strong>Subtitles ASS:</strong> ")
					writeLink(&b, clip.SubtitleLink, clip.SubtitleFileID, clip.SubtitleFileID)
					b.WriteString("</p>")
				}
			}
			if stock := scene.Bindings.Stock; stock != nil {
				b.WriteString("<p><strong>Clip:</strong> ")
				writeLink(&b, stock.DriveLink, stock.Name, stock.AssetID)
				b.WriteString("</p>")
			}
			b.WriteString("</section>")
		}
	}
	raw, err := json.MarshalIndent(model.SpecScene, "", "  ")
	if err == nil {
		b.WriteString("<h2>SpecScene JSON</h2><pre>")
		writeEscaped(&b, string(raw))
		b.WriteString("</pre>")
	}
	b.WriteString("</body></html>")
	return b.String()
}

func writeEscaped(b *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
}

func writeLink(b *strings.Builder, url, label, fallback string) {
	url = strings.TrimSpace(url)
	if url == "" {
		if fallback == "" {
			b.WriteString("(no link)")
			return
		}
		writeEscaped(b, fallback)
		return
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = url
	}
	b.WriteString("<a href=\"")
	writeEscaped(b, url)
	b.WriteString("\">")
	writeEscaped(b, label)
	b.WriteString("</a>")
}
