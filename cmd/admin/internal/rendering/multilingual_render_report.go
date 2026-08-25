package rendering

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/multilingual"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"go.uber.org/zap"
)

func hashLocalFile(path string) (string, error) {
	h, _, err := digest.SHA256File(path)
	if err != nil {
		return "", err
	}
	return h, nil
}

func isSHA256Hex(value string) bool {
	if len(value) != digest.SHA256HexLength {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func opMetadata(assetID, lang string, extra map[string]any) string {
	m := map[string]any{"asset_id": assetID, "language": lang}
	for k, v := range extra {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func resolveLanguages(cfg *config.Config, sourceLangFlag, langsFlag string) (string, []string) {
	srcDefault := "en"
	if cfg.Media.Multilingual.SourceLanguage != "" {
		srcDefault = cfg.Media.Multilingual.SourceLanguage
	}
	if langsFlag != "" {
		parts := cli.SplitCSV(langsFlag)
		if len(parts) == 0 {
			return srcDefault, nil
		}
		src := parts[0]
		if src == "" {
			src = srcDefault
		}
		return src, parts[1:]
	}
	src := sourceLangFlag
	if src == "" {
		src = srcDefault
	}
	out := make([]string, 0)
	for _, spec := range cfg.Media.Multilingual.Languages {
		if !spec.Enabled || !spec.TranslateClips || spec.Code == src {
			continue
		}
		out = append(out, spec.Code)
	}
	sort.Strings(out)
	return src, out
}

func resolveDriveFolder(cfg *config.Config, item *asset.Asset, override string) string {
	if override != "" {
		return override
	}
	if item != nil && item.FolderID() != "" {
		return item.FolderID()
	}
	return cfg.Drive.ClipsFolder()
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func countValidated(reports []langReport) int {
	n := 0
	for _, r := range reports {
		if r.Validation == "ok" {
			n++
		}
	}
	return n
}

func writeCertification(path string, summaries []multilingual.RunMetrics, validatedCounts []int) error {
	certs := make([]multilingual.CertificationReport, 0, len(summaries))
	for i, s := range summaries {
		validated := 0
		if i < len(validatedCounts) {
			validated = validatedCounts[i]
		}
		certs = append(certs, multilingual.BuildCertification(s, validated, 0))
	}
	var b []byte
	var err error
	if len(certs) == 1 {
		b, err = json.MarshalIndent(certs[0], "", "  ")
	} else {
		b, err = json.MarshalIndent(certs, "", "  ")
	}
	if err != nil {
		return err
	}
	if path == "-" {
		fmt.Println(string(b))
		return nil
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("certification report written to %s\n", path)
	return nil
}

func derefDocRefs(refs []*multilingual.LocalizationDocRef) []multilingual.LocalizationDocRef {
	out := make([]multilingual.LocalizationDocRef, 0, len(refs))
	for _, r := range refs {
		if r != nil {
			out = append(out, *r)
		}
	}
	return out
}

func printLocalizationDocs(refs []*multilingual.LocalizationDocRef) {
	if len(refs) == 0 {
		return
	}
	fmt.Println("=== Localization manifest (Google Docs) ===")
	for _, r := range refs {
		if r == nil {
			continue
		}
		fmt.Printf("doc: %s (%d entries)\n", r.Link, len(r.Entries))
		for _, e := range r.Entries {
			fmt.Printf("  #%d %-6s %-9s %s\n", e.Priority, e.Language, e.Status, e.DriveLink)
		}
	}
}

func publishLocalizationDoc(ctx context.Context, docClient drive.DocClient, id, base, docsFolder, fallbackFolder string, variants []multilingual.VariantResult, force bool, log *zap.Logger) *multilingual.LocalizationDocRef {
	entries := multilingual.AssembleLocalizationEntries(variants)
	ref := &multilingual.LocalizationDocRef{Entries: entries}
	folder := docsFolder
	if folder == "" {
		folder = fallbackFolder
	}
	if docClient == nil || folder == "" {
		log.Info("multilingual-render.localization_doc.skipped", zap.String("asset_id", id), zap.String("reason", "no doc client or no destination folder"), zap.Int("entries", len(entries)))
		return ref
	}
	title := "Localization — " + base
	if base == "" {
		title = "Localization — " + id
	}
	doc, err := docClient.CreateDocIdempotent(ctx, title, multilingual.RenderLocalizationDoc(title, entries), folder, "localization:asset:"+id, force)
	if err != nil {
		log.Warn("multilingual-render.localization_doc.failed", zap.String("asset_id", id), zap.Error(err))
		return ref
	}
	if doc == nil {
		return ref
	}
	ref.ID, ref.Link = doc.ID, doc.URL
	log.Info("multilingual-render.localization_doc.published", zap.String("asset_id", id), zap.String("doc_id", doc.ID), zap.String("link", doc.URL), zap.Int("entries", len(entries)))
	return ref
}

func printParallelism(summaries []multilingual.RunMetrics) {
	for _, s := range summaries {
		r, tr := s.RenderConcurrency, s.TranslateConcurrency
		fmt.Println("=== Parallelism (observed) ===")
		fmt.Printf("render:    configured=%d max_observed=%d avg_observed=%.2f wall_ms=%d work_ms=%d queue_ms=%d (max %d)\n", r.Configured, r.MaxObserved, r.AvgObserved, r.WallMS, r.TotalWorkMS, r.TotalQueueMS, r.MaxQueueMS)
		fmt.Printf("translate: configured=%d max_observed=%d avg_observed=%.2f wall_ms=%d work_ms=%d queue_ms=%d (max %d)\n", tr.Configured, tr.MaxObserved, tr.AvgObserved, tr.WallMS, tr.TotalWorkMS, tr.TotalQueueMS, tr.MaxQueueMS)
		tp := s.Throughput
		fmt.Printf("throughput: clips/min=%.2f media_min/min=%.2f render_rtf=%.2f\n", tp.ClipsPerMinute, tp.MediaMinutesPerMinute, tp.RenderRTF)
		c := s.Operations
		fmt.Printf("exec:       download=%d probe=%d transcribe=%d translate=%d fulltext_translate=%d ass=%d render=%d validate=%d upload=%d\n", c.Download, c.Probe, c.Transcribe, c.Translate, c.TranslateFullText, c.ASS, c.Render, c.Validate, c.Upload)
	}
}

func printLangReport(reports []langReport) {
	fmt.Println("=== Multilingual Render Report ===")
	fmt.Printf("%-8s %-10s %-11s %-11s %-5s %-7s %-7s %-8s %-6s %-9s %-6s %s\n", "lang", "transcript", "translation", "translate_ms", "ass", "ass_ms", "render", "render_ms", "rtf", "size_mb", "valid", "drive_link")
	var totalTranslate, totalASS, totalRender int64
	var rendered, validated int
	for _, r := range reports {
		sizeMB := ""
		if r.SizeBytes > 0 {
			sizeMB = fmt.Sprintf("%.2f", float64(r.SizeBytes)/1024/1024)
		}
		fmt.Printf("%-8s %-10s %-11s %-11d %-5s %-7d %-7s %-8d %-6.2f %-9s %-6s %s\n", r.Language, r.Transcript, r.Translation, r.TranslateMS, r.ASSStatus, r.ASSMS, r.RenderStatus, r.RenderMS, r.RTF, sizeMB, r.Validation, r.DriveLink)
		totalTranslate += r.TranslateMS
		totalASS += r.ASSMS
		totalRender += r.RenderMS
		if r.RenderStatus == "ready" || r.RenderStatus == "reused" {
			rendered++
		}
		if r.Validation == "ok" {
			validated++
		}
	}
	fmt.Println("---")
	fmt.Printf("totals: translate_ms=%d ass_ms=%d render_ms=%d (per-language, summed) | rendered=%d\n", totalTranslate, totalASS, totalRender, rendered)
	fmt.Printf("validation: %d/%d PASS\n", validated, len(reports))
}

func printPerLangTiming(reports []langReport) {
	fmt.Println("=== Per-language timing ===")
	fmt.Printf("%-3s %-8s %-24s %-24s %-24s %-24s %-24s %-8s %-9s\n", "pri", "lang", "text_ready_at", "queued_at", "render_started_at", "render_completed_at", "upload_completed_at", "worker", "render_ms")
	var srcStarted, firstTargetReady, firstTargetCompleted time.Time
	for _, r := range reports {
		started, ready, completed := parseTS(r.RenderStartedAt), parseTS(r.TextReadyAt), parseTS(r.RenderCompletedAt)
		fmt.Printf("%-3d %-8s %-24s %-24s %-24s %-24s %-24s %-8d %-9d\n", r.Priority, r.Language, r.TextReadyAt, r.QueuedAt, r.RenderStartedAt, r.RenderCompletedAt, r.UploadCompletedAt, r.WorkerID, r.RenderMS)
		if r.Priority == 0 {
			srcStarted = started
		} else {
			if firstTargetReady.IsZero() || ready.Before(firstTargetReady) {
				firstTargetReady = ready
			}
			if firstTargetCompleted.IsZero() || completed.Before(firstTargetCompleted) {
				firstTargetCompleted = completed
			}
		}
	}
	fmt.Println("---")
	if !srcStarted.IsZero() && !firstTargetCompleted.IsZero() {
		fmt.Printf("certify: source render_started_at < first target render_completed_at => %v\n", srcStarted.Before(firstTargetCompleted))
	}
	if !srcStarted.IsZero() && !firstTargetReady.IsZero() {
		fmt.Printf("certify: source render_started_at < first target text_ready_at => %v\n", srcStarted.Before(firstTargetReady))
	}
}

func formatTS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func probeSourceFPS(ffmpegPath, srcPath string) (float64, bool) {
	if srcPath == "" {
		return 0, false
	}
	ffprobe := "ffprobe"
	if ffmpegPath != "" && ffmpegPath != "ffmpeg" {
		ffprobe = filepath.Join(filepath.Dir(ffmpegPath), "ffprobe")
	}
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=avg_frame_rate", "-of", "default=noprint_wrappers=1:nokey=1", srcPath).Output()
	if err != nil {
		return 0, false
	}
	return multilingual.ParseFPS(strings.TrimSpace(string(out))), true
}
