// Package ytdlp — section performance pins (October 2026 GPU cutover).
//
// Sectioned (--download-sections) fetches are bounded ranges, not channel
// scrapes: they use a wider fragment budget (8), carry an input-side GPU
// decode flag, and prefer hardware-decodable codecs (H.264 first) so the
// unavoidable yt-dlp-internal re-encode never pays a libdav1d software
// AV1 decode.
package ytdlp

import (
	"reflect"
	"strings"
	"testing"
)

func TestSectionConcurrentFragmentsArgDefault(t *testing.T) {
	t.Setenv(EnvSectionConcurrentFragments, "")
	t.Setenv(EnvConcurrentFragments, "")
	b := &CommandBuilder{}
	if got, want := b.SectionConcurrentFragmentsArg(), []string{"--concurrent-fragments", "8"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SectionConcurrentFragmentsArg() = %v, want %v", got, want)
	}
}

func TestSectionConcurrentFragmentsArgEnvOverrideAndClamp(t *testing.T) {
	b := &CommandBuilder{}

	t.Setenv(EnvSectionConcurrentFragments, "12")
	if got := b.SectionConcurrentFragmentsArg(); !reflect.DeepEqual(got, []string{"--concurrent-fragments", "12"}) {
		t.Fatalf("override 12: got %v", got)
	}

	t.Setenv(EnvSectionConcurrentFragments, "99")
	if got := b.SectionConcurrentFragmentsArg(); !reflect.DeepEqual(got, []string{"--concurrent-fragments", "16"}) {
		t.Fatalf("override 99: got %v, want clamp to 16", got)
	}

	t.Setenv(EnvSectionConcurrentFragments, "0")
	if got := b.SectionConcurrentFragmentsArg(); !reflect.DeepEqual(got, []string{"--concurrent-fragments", "1"}) {
		t.Fatalf("override 0: got %v, want clamp to 1", got)
	}

	t.Setenv(EnvSectionConcurrentFragments, "garbage")
	t.Setenv(EnvConcurrentFragments, "")
	if got := b.SectionConcurrentFragmentsArg(); !reflect.DeepEqual(got, []string{"--concurrent-fragments", "8"}) {
		t.Fatalf("override garbage: got %v, want default 8", got)
	}
}

func TestSectionConcurrentFragmentsArgFallsBackToGenericKnob(t *testing.T) {
	t.Setenv(EnvSectionConcurrentFragments, "")
	t.Setenv(EnvConcurrentFragments, "6")
	b := &CommandBuilder{}
	if got := b.SectionConcurrentFragmentsArg(); !reflect.DeepEqual(got, []string{"--concurrent-fragments", "6"}) {
		t.Fatalf("generic fallback: got %v, want width 6", got)
	}
	// The full-source default is untouched by the section budget.
	if got := b.ConcurrentFragmentsArg(); !reflect.DeepEqual(got, []string{"--concurrent-fragments", "6"}) {
		t.Fatalf("full-source knob: got %v, want width 6", got)
	}
}

func TestSectionHWAccelDownloaderArgs(t *testing.T) {
	b := &CommandBuilder{}

	t.Run("default auto on the input side", func(t *testing.T) {
		t.Setenv(EnvHWAccel, "")
		got := b.SectionHWAccelDownloaderArgs()
		want := []string{"--downloader-args", "ffmpeg_i:-hwaccel auto"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("explicit cuda passthrough", func(t *testing.T) {
		t.Setenv(EnvHWAccel, "cuda")
		got := b.SectionHWAccelDownloaderArgs()
		if !reflect.DeepEqual(got, []string{"--downloader-args", "ffmpeg_i:-hwaccel cuda"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("off disables", func(t *testing.T) {
		t.Setenv(EnvHWAccel, "off")
		if got := b.SectionHWAccelDownloaderArgs(); got != nil {
			t.Fatalf("off must return nil, got %v", got)
		}
	})
}

// TestSectionFormatPrefersHardwareDecodableCodecs pins the October 2026
// codec order: H.264 (avc1) first, VP9 second, unconstrained fallback last.
// Without the preference the selector matched AV1 (itag 399) and forced a
// libdav1d software decode + re-encode per section.
func TestSectionFormatPrefersHardwareDecodableCodecs(t *testing.T) {
	sel := DefaultYouTubeSectionFormatSelectors
	avc := strings.Index(sel, "vcodec^=avc1")
	vp9 := strings.Index(sel, "vcodec^=vp09")
	if avc < 0 {
		t.Fatalf("section selector must prefer avc1 first: %q", sel)
	}
	if vp9 < 0 {
		t.Fatalf("section selector must include a vp09 alternative: %q", sel)
	}
	if avc > vp9 {
		t.Fatalf("avc1 preference must precede vp09: %q", sel)
	}
	// The unconstrained fallback stays so exotic sources still download.
	if !strings.Contains(sel, "bv*[height<=1080][ext=mp4]+ba[ext=m4a]") {
		t.Fatalf("section selector must keep the unconstrained fallback: %q", sel)
	}
}
