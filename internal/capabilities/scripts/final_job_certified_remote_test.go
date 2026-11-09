package scriptgeneration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// objectStoreProbe is a minimal Objectstore used to count the requests a
// publication path makes. It serves one known object by content address.
type objectStoreProbe struct {
	sha     string
	body    []byte
	present atomic.Bool
	heads   atomic.Int32
	puts    atomic.Int32
	server  *httptest.Server
}

func newObjectStoreProbe(t *testing.T, body []byte) *objectStoreProbe {
	t.Helper()
	probe := &objectStoreProbe{sha: digest.SHA256String(string(body)), body: body}
	probe.present.Store(true)
	probe.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/objects/"+probe.sha {
			http.Error(w, "unexpected object", http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodHead:
			probe.heads.Add(1)
			if !probe.present.Load() {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(probe.body)))
		case http.MethodPut:
			probe.puts.Add(1)
			data, err := io.ReadAll(r.Body)
			if err != nil || string(data) != string(probe.body) {
				http.Error(w, "incorrect bytes", http.StatusBadRequest)
				return
			}
			probe.present.Store(true)
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(probe.server.Close)
	t.Setenv("RENDERINGGEN_STORE_URL", probe.server.URL)
	t.Setenv("OBJECTSTORE_PUBLIC_URL", "https://objects.example")
	t.Setenv("OBJECTSTORE_SIGNING_SECRET", strings.Repeat("s", 32))
	t.Setenv("OBJECTSTORE_TOKEN", "")
	return probe
}

// TestCertifiedRemoteClipSignsWithoutLocalBytesOrHead pins the P0 handoff
// boundary: a localized render that RenderingGen certified and left in the
// shared store is handed to the final job from its storage key + SHA + size.
// The Producer must not download it, stat it, hash it or probe it again.
func TestCertifiedRemoteClipSignsWithoutLocalBytesOrHead(t *testing.T) {
	body := []byte("remote certified clip bytes")
	probe := newObjectStoreProbe(t, body)
	rendered := LocalizedRenderResult{
		SceneID: "scene-1", Language: "it", ClipID: "clip-1", AssetID: "derived-render-1",
		SHA256: probe.sha, SizeBytes: int64(len(body)), DurationMS: 4600,
		StorageKey: probe.sha, ArtifactURL: "https://objects.example/objects/" + probe.sha,
	}
	if !localizedRenderIsCertifiedClip(rendered) {
		t.Fatal("a remote-only render with storage key, size and duration must be certified")
	}
	ref, duration, err := renderedClipAssetRef(context.Background(), nil, rendered, "scene-1")
	if err != nil {
		t.Fatalf("renderedClipAssetRef: %v", err)
	}
	if probe.heads.Load() != 0 || probe.puts.Load() != 0 {
		t.Fatalf("remote-only handoff touched the objectstore: HEADs=%d PUTs=%d", probe.heads.Load(), probe.puts.Load())
	}
	if ref["asset_id"] != probe.sha || ref["sha256"] != probe.sha || ref["size_bytes"] != int64(len(body)) || ref["duration_ms"] != int64(4600) {
		t.Fatalf("remote-only ref = %#v, want the certified identity + duration", ref)
	}
	if duration != 4600 {
		t.Fatalf("duration = %d, want 4600", duration)
	}
	if source, _ := ref["source_uri"].(string); !strings.Contains(source, "/objects/"+probe.sha) {
		t.Fatalf("source_uri = %q, want the signed object URL", source)
	}
}

// TestRenderedClipLocalPublicationUsesOneHead pins the P0-8 duplicate probe:
// publishing a local certified clip and building its reference is one
// availability check, not two.
func TestRenderedClipLocalPublicationUsesOneHead(t *testing.T) {
	body := []byte("local certified clip bytes")
	probe := newObjectStoreProbe(t, body)
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	rendered := LocalizedRenderResult{
		SceneID: "scene-1", Language: "it", ClipID: "clip-1", AssetID: "derived-render-1",
		SHA256: probe.sha, SizeBytes: int64(len(body)), DurationMS: 4600, LocalPath: path,
	}
	ref, _, err := renderedClipAssetRef(context.Background(), nil, rendered, "scene-1")
	if err != nil {
		t.Fatalf("renderedClipAssetRef: %v", err)
	}
	if probe.heads.Load() != 1 || probe.puts.Load() != 0 {
		t.Fatalf("local publication: HEADs=%d PUTs=%d, want exactly 1/0", probe.heads.Load(), probe.puts.Load())
	}
	if ref["sha256"] != probe.sha {
		t.Fatalf("ref = %#v, want the certified SHA", ref)
	}
}

// TestFinalJobAudioPublicationUsesOneHead pins the same single-publication
// rule on the final audio: PublishFinalJobLocalObject returns the signed
// reference it already verified instead of a second HEAD/ref request.
func TestFinalJobAudioPublicationUsesOneHead(t *testing.T) {
	body := []byte("certified final audio bytes")
	probe := newObjectStoreProbe(t, body)
	path := filepath.Join(t.TempDir(), "final.m4a")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	audio := FinalAudioReference{
		AssetID: "audio-asset", Path: path, FinalAudioSHA256: probe.sha,
		SizeBytes: int64(len(body)), DurationMS: 2500, FinalMix: true, CopyEligible: true,
		Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo",
	}
	ref, err := finalJobAudioAsset(context.Background(), audio)
	if err != nil {
		t.Fatalf("finalJobAudioAsset: %v", err)
	}
	if probe.heads.Load() != 1 || probe.puts.Load() != 0 {
		t.Fatalf("final audio publication: HEADs=%d PUTs=%d, want exactly 1/0", probe.heads.Load(), probe.puts.Load())
	}
	if ref["asset_id"] != probe.sha || ref["duration_ms"] != int64(2500) || ref["strategy"] != "FINAL_AUDIO_COPY" {
		t.Fatalf("final audio ref = %#v, want the certified identity + media contract", ref)
	}
}

// TestLocalizedRenderIsCertifiedClipRejectsASizeLessRemoteClip keeps the
// fail-closed half: a storage key without the certified byte size is not
// enough to hand the bytes on.
func TestLocalizedRenderIsCertifiedClipRejectsASizeLessRemoteClip(t *testing.T) {
	rendered := LocalizedRenderResult{
		SceneID: "scene-1", Language: "it", ClipID: "clip-1",
		SHA256: strings.Repeat("a", 64), DurationMS: 1000,
		StorageKey: strings.Repeat("a", 64), ArtifactURL: "https://objects.example/objects/" + strings.Repeat("a", 64),
	}
	oldPublic := os.Getenv("OBJECTSTORE_PUBLIC_URL")
	t.Setenv("OBJECTSTORE_PUBLIC_URL", "https://objects.example")
	t.Cleanup(func() {
		if oldPublic == "" {
			_ = os.Unsetenv("OBJECTSTORE_PUBLIC_URL")
		} else {
			_ = os.Setenv("OBJECTSTORE_PUBLIC_URL", oldPublic)
		}
	})
	if localizedRenderIsCertifiedClip(rendered) {
		t.Fatal("a remote clip without a certified size must not be handed on")
	}
}
