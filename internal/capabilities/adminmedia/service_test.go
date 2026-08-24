package adminmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

type testDriveReader struct {
	folders map[string][]DriveAudioFile
	data    map[string][]byte
}

func (r *testDriveReader) ListFiles(_ context.Context, folder string) ([]DriveAudioFile, error) {
	return r.folders[folder], nil
}
func (r *testDriveReader) DownloadFile(_ context.Context, id string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(r.data[id])), nil
}

type testEditor struct {
	probes  []time.Duration
	trimmed []string
}

func (e *testEditor) Probe(context.Context, string) (time.Duration, error) {
	if len(e.probes) == 0 {
		return time.Second, nil
	}
	value := e.probes[0]
	e.probes = e.probes[1:]
	return value, nil
}
func (e *testEditor) Trim(_ context.Context, path string, _ float64) error {
	e.trimmed = append(e.trimmed, path)
	return nil
}

type testUploader struct {
	commands []delivery.AdminUploadCommand
}

func (u *testUploader) Publish(_ context.Context, cmd delivery.AdminUploadCommand) (*delivery.PublishResult, error) {
	u.commands = append(u.commands, cmd)
	return &delivery.PublishResult{FileID: "file-1"}, nil
}

func TestNormalizeDriveSoundEffects_TraversesAndPublishesOnlyLongFiles(t *testing.T) {
	reader := &testDriveReader{
		folders: map[string][]DriveAudioFile{
			"root":   {{ID: "nested", Name: "nested", MimeType: "application/vnd.google-apps.folder"}, {ID: "short", Name: "short.mp3"}},
			"nested": {{ID: "long", Name: "long.mp3"}},
		},
		data: map[string][]byte{"short": []byte("short"), "long": []byte("long")},
	}
	editor := &testEditor{probes: []time.Duration{3 * time.Second, time.Second, time.Second}}
	uploader := &testUploader{}
	report, err := NormalizeDriveSoundEffects(context.Background(), "root", 2, reader, editor, uploader)
	if err != nil {
		t.Fatal(err)
	}
	if report.Checked != 2 || report.Changed != 1 {
		t.Fatalf("report = %+v, want checked=2 changed=1", report)
	}
	if len(uploader.commands) != 1 || uploader.commands[0].FolderID != "nested" || uploader.commands[0].Filename != "long.mp3" {
		t.Fatalf("upload commands = %+v, want nested/long.mp3", uploader.commands)
	}
	if len(editor.trimmed) != 1 {
		t.Fatalf("trim calls = %d, want 1", len(editor.trimmed))
	}
}

type testMetadataSource struct{ items []SoundEffectMetadata }

func (s testMetadataSource) ListSoundEffects(context.Context) ([]SoundEffectMetadata, error) {
	return s.items, nil
}

type inspectingUploader struct {
	command delivery.AdminUploadCommand
	payload []byte
}

func (u *inspectingUploader) Publish(_ context.Context, cmd delivery.AdminUploadCommand) (*delivery.PublishResult, error) {
	u.command = cmd
	data, err := os.ReadFile(cmd.LocalPath)
	if err != nil {
		return nil, err
	}
	u.payload = data
	return &delivery.PublishResult{FileID: "metadata-1", WebViewLink: "https://drive.test/metadata-1"}, nil
}

func TestExportSoundEffectsMetadata_GroupsUncategorized(t *testing.T) {
	uploader := &inspectingUploader{}
	report, err := ExportSoundEffectsMetadata(context.Background(), testMetadataSource{items: []SoundEffectMetadata{
		{ID: "1", Name: "boom", Family: "cinematic"},
		{ID: "2", Name: "click", Family: ""},
	}}, uploader, "folder-1", "metadata.json")
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 2 || report.FamilyCount != 2 || uploader.command.FolderID != "folder-1" {
		t.Fatalf("report=%+v command=%+v", report, uploader.command)
	}
	var document MetadataExport
	if err := json.Unmarshal(uploader.payload, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.ByFamily["cinematic"]) != 1 || len(document.ByFamily["uncategorized"]) != 1 {
		t.Fatalf("families = %+v", document.ByFamily)
	}
}

type testRenderer struct{ calls int }

func (r *testRenderer) Render(context.Context, RenderManifest) error { r.calls++; return nil }

func TestRenderShort_RendersBeforeOptionalUpload(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.mp4")
	output := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(input, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	renderer := &testRenderer{}
	uploader := &testUploader{}
	result, err := RenderShort(context.Background(), RenderManifest{
		Input: input, Output: output, Font: "/font.ttf",
		Overlays: []RenderOverlay{{Text: "hello"}},
		Upload:   &RenderUpload{FolderID: "folder-1", Filename: "out.mp4"},
	}, renderer, uploader)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.FileID != "file-1" {
		t.Fatalf("result = %+v, want uploaded file-1", result)
	}
	if renderer.calls != 1 || len(uploader.commands) != 1 {
		t.Fatalf("renderer calls=%d uploader calls=%d", renderer.calls, len(uploader.commands))
	}
}
