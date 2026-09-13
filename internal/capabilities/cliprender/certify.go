package cliprender

// certify.go owns the CERTIFIED-FACTS RECONCILIATION between the artifact
// RenderingGen certified and the local probe of the materialized bytes.
//
// Why it exists (clip.render audit 2026-09-13, finding P1 "double
// certification"): the render boundary probes the output it produced and
// certifies the artifact; PipelineGen then probes the downloaded file again.
// Today the second probe is the only thing ValidateContract reads, but it is
// structurally WEAKER than the certified artifact: the Rust probe cannot
// report the codec profile, level, timebase, SAR, colour, GOP or channel
// layout, so every contract dimension carrying one of those is silently
// skipped (`if c.X > 0 && p.X != 0 && ...`) and a violated render passes.
//
// This file makes the two owners compose instead of choosing one:
//
//   - DISAGREEMENT is fail-closed. If the local probe and the certified
//     artifact report different container, codec, pixel format, geometry, fps
//     or audio stream count, the render is rejected rather than published:
//     the two independent observations of "what was written" must agree.
//   - UNREPORTED dimensions are filled from the certified owner. The codec
//     profile in particular is certified by RenderingGen from ffprobe and is
//     the ONLY source for that contract dimension, so it is projected onto the
//     probe instead of being skipped.
//
// Deleting the local probe entirely (the audit's end state) is gated on an
// A/B study: 100–1000 renders with zero disagreement. This reconciliation is
// the runtime form of that study — it records every disagreement as a typed
// error instead of trusting the boundary blindly.

import (
	"errors"
	"fmt"
	"strings"
)

// ErrCertifiedFactsMismatch is returned when the locally probed output does
// not describe the same media RenderingGen certified. Fail-closed: a render
// whose two independent observations disagree is never published.
var ErrCertifiedFactsMismatch = errors.New("clip.render: local probe disagrees with the certified artifact")

// ReconcileCertifiedFacts merges the local OutputProbe with the certified
// structural facts carried on the render outcome.
//
// It returns a probe that is safe to hand to ValidateContract:
//
//   - a non-nil error (wrapping ErrCertifiedFactsMismatch) when both owners
//     report a shared dimension and the values disagree;
//   - otherwise the local probe with its UNREPORTED dimensions filled from the
//     certified artifact, so contract dimensions the local probe cannot see
//     (codec profile) are actually validated.
//
// A nil outcome (a legacy or test renderer that certified nothing beyond the
// bytes) leaves the probe untouched: reconciliation never invents facts it
// does not have.
func ReconcileCertifiedFacts(probe *OutputProbe, outcome *RenderOutcome) (*OutputProbe, error) {
	if probe == nil {
		return nil, fmt.Errorf("%w: probe is nil", ErrCertifiedFactsMismatch)
	}
	if outcome == nil {
		return probe, nil
	}

	var disagreements []string

	if certified := normalizeContainer(outcome.Container); certified != "" &&
		normalizeContainer(probe.Container) != "" && certified != normalizeContainer(probe.Container) {
		disagreements = append(disagreements, fmt.Sprintf("container local=%q certified=%q", probe.Container, outcome.Container))
	}
	// The codec/profile comparison is case-insensitive: ffprobe reports
	// "High" while the contract declares "high", so a case difference is not
	// a mismatch (it is normalized on the fill path below).
	if outcome.VideoCodec != "" && probe.VideoCodec != "" &&
		normalizeVideoProfile(outcome.VideoCodec) != normalizeVideoProfile(probe.VideoCodec) {
		disagreements = append(disagreements, fmt.Sprintf("video codec local=%q certified=%q", probe.VideoCodec, outcome.VideoCodec))
	}
	if outcome.PixelFormat != "" && probe.PixelFormat != "" && outcome.PixelFormat != probe.PixelFormat {
		disagreements = append(disagreements, fmt.Sprintf("pixel format local=%q certified=%q", probe.PixelFormat, outcome.PixelFormat))
	}
	if outcome.Width != 0 && probe.Width != 0 && int(outcome.Width) != probe.Width {
		disagreements = append(disagreements, fmt.Sprintf("width local=%d certified=%d", probe.Width, outcome.Width))
	}
	if outcome.Height != 0 && probe.Height != 0 && int(outcome.Height) != probe.Height {
		disagreements = append(disagreements, fmt.Sprintf("height local=%d certified=%d", probe.Height, outcome.Height))
	}
	// FPS: exact rational via cross-multiplication — no float epsilon.
	if outcome.FPSNum > 0 && outcome.FPSDen > 0 && probe.FPSNum > 0 && probe.FPSDen > 0 &&
		uint64(probe.FPSNum)*uint64(outcome.FPSDen) != uint64(outcome.FPSNum)*uint64(probe.FPSDen) {
		disagreements = append(disagreements,
			fmt.Sprintf("fps local=%d/%d certified=%d/%d", probe.FPSNum, probe.FPSDen, outcome.FPSNum, outcome.FPSDen))
	}
	if outcome.AudioStreams > 0 && probe.AudioStreams > 0 && outcome.AudioStreams != probe.AudioStreams {
		disagreements = append(disagreements, fmt.Sprintf("audio streams local=%d certified=%d", probe.AudioStreams, outcome.AudioStreams))
	}

	if len(disagreements) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrCertifiedFactsMismatch, strings.Join(disagreements, "; "))
	}

	// No disagreement: fill the dimensions the local probe left unreported
	// from the certified owner. Computed only from the certified facts, never
	// guessed.
	merged := *probe
	if merged.Container == "" && outcome.Container != "" {
		merged.Container = normalizeContainer(outcome.Container)
	}
	if merged.VideoCodec == "" && outcome.VideoCodec != "" {
		merged.VideoCodec = outcome.VideoCodec
	}
	// The contract declares the canonical lowercase profile, so normalize the
	// certified ffprobe casing ("High" → "high") when projecting it.
	if merged.VideoProfile == "" && outcome.VideoProfile != "" {
		merged.VideoProfile = normalizeVideoProfile(outcome.VideoProfile)
	}
	if merged.PixelFormat == "" && outcome.PixelFormat != "" {
		merged.PixelFormat = outcome.PixelFormat
	}
	if merged.Width == 0 && outcome.Width != 0 {
		merged.Width = int(outcome.Width)
	}
	if merged.Height == 0 && outcome.Height != 0 {
		merged.Height = int(outcome.Height)
	}
	if (merged.FPSNum == 0 || merged.FPSDen == 0) && outcome.FPSNum > 0 && outcome.FPSDen > 0 {
		merged.FPSNum = int(outcome.FPSNum)
		merged.FPSDen = int(outcome.FPSDen)
	}
	if merged.AudioStreams == 0 && outcome.AudioStreams > 0 {
		merged.AudioStreams = outcome.AudioStreams
	}
	return &merged, nil
}

// normalizeContainer projects an ffprobe format_name family onto the
// canonical contract container. ffprobe reports MP4 as the compatible family
// "mov,mp4,m4a,3gp,3g2,mj2", and the certified artifact carries that string
// verbatim — the same normalization the Rust probe applies.
func normalizeContainer(raw string) string {
	container := strings.TrimSpace(raw)
	if i := strings.IndexByte(container, ','); i >= 0 {
		container = strings.TrimSpace(container[:i])
	}
	if container == "mov" && strings.Contains(raw, "mp4") {
		container = "mp4"
	}
	return container
}

// normalizeVideoProfile lowercases a codec profile so ffprobe casing ("High")
// compares equal to the canonical contract value ("high").
func normalizeVideoProfile(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
