package cliprender

// contract_projection_completeness_test.go — the structural gate for the
// output contract (ticket CR-19-02).
//
// Why this file exists: OutputFacts (what RenderingGen certifies),
// OutputProbe (what this package's gate consumes) and contractChecks (the
// dimensions the gate enforces) are three representations of one truth. Before
// this gate, adding a fact without projecting it produced NO error: the probe
// field stayed empty and ValidateContract silently skipped the dimension —
// exactly the "double certification" regression the 2026-09-13 audit claimed
// to have closed.
//
// The three tests below make the coupling mechanical:
//
//	1. field parity: every OutputFacts field has a same-named OutputProbe field
//	   and is projected verbatim by OutputProbeFromCertified;
//	2. no dead probe fields: every OutputProbe field is reachable from some
//	   fact (this is what removed StreamOrder);
//	3. every contractChecks dimension is reachable: a probe that differs on the
//	   dimension makes that specific check fail.
//
// Adding a fact now fails 1 until it is projected, and adding a check fails 3
// until a violation is expressible.

import (
	"reflect"
	"testing"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// factFieldIsProjected reports the projection of one OutputFacts field into the
// probe, by name (the projection is name-for-name by contract).
func factFieldIsProjected(t *testing.T, name string, probe *OutputProbe) reflect.Value {
	t.Helper()
	probeValue := reflect.ValueOf(probe).Elem()
	field := probeValue.FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("OutputProbe has no field %q: OutputFacts.%s cannot be projected (add the field AND the projection)", name, name)
	}
	return field
}

// TestOutputFactsProbeProjectsEveryFact sets exactly one fact at a time to a
// distinctive non-zero value and requires the same-named probe field to carry
// it. A missing projection passes the zero value through, which this catches.
func TestOutputFactsProbeProjectsEveryFact(t *testing.T) {
	factsType := reflect.TypeOf(OutputFacts{})

	// Container and VideoProfile are the two dimensions the projection is
	// allowed to NORMALIZE (ffprobe reports "mov,mp4,..." and "High"); the test
	// feeds the canonical spelling so normalization is a no-op.
	canonicalValue := map[string]any{
		"Container":    "mp4",
		"VideoProfile": "high",
	}

	for i := 0; i < factsType.NumField(); i++ {
		field := factsType.Field(i)
		t.Run(field.Name, func(t *testing.T) {
			var facts OutputFacts
			factsValue := reflect.ValueOf(&facts).Elem().FieldByName(field.Name)

			if value, ok := canonicalValue[field.Name]; ok {
				factsValue.Set(reflect.ValueOf(value))
			} else {
				switch field.Type.Kind() {
				case reflect.String:
					factsValue.SetString("7")
				case reflect.Bool:
					factsValue.SetBool(true)
				case reflect.Int:
					factsValue.SetInt(7)
				case reflect.Int64:
					factsValue.SetInt(7)
				default:
					t.Fatalf("OutputFacts.%s has unsupported kind %s: extend this gate", field.Name, field.Type.Kind())
				}
			}

			probe, err := OutputProbeFromCertified(&RenderOutcome{Facts: &facts})
			if err != nil {
				t.Fatalf("projection failed: %v", err)
			}
			projected := factFieldIsProjected(t, field.Name, probe)
			if !reflect.DeepEqual(projected.Interface(), factsValue.Interface()) {
				t.Fatalf("OutputFacts.%s = %v was not projected: OutputProbe.%s = %v",
					field.Name, factsValue.Interface(), field.Name, projected.Interface())
			}
		})
	}
}

// TestOutputProbeHasNoUnprojectedFields forbids dead fields: a probe field no
// fact can fill is a dimension that LOOKS validated and never is.
func TestOutputProbeHasNoUnprojectedFields(t *testing.T) {
	reachable := map[string]bool{
		// FPS is the derived float projection of FPSNum/FPSDen, documented on
		// the field itself.
		"FPS": true,
	}
	factsType := reflect.TypeOf(OutputFacts{})
	for i := 0; i < factsType.NumField(); i++ {
		reachable[factsType.Field(i).Name] = true
	}
	probeType := reflect.TypeOf(OutputProbe{})
	for i := 0; i < probeType.NumField(); i++ {
		name := probeType.Field(i).Name
		if !reachable[name] {
			t.Errorf("OutputProbe.%s is never populated by OutputProbeFromCertified: remove it or project it from a certified fact", name)
		}
	}
}

// satisfyingProbe builds the probe the canonical contract expects: every
// dimension set to the contract's own value. It is the baseline the per-check
// violation test mutates.
func satisfyingProbe(c kernelmedia.AssemblyMediaContract) *OutputProbe {
	return &OutputProbe{
		Container:         c.Container,
		HasVideo:          true,
		VideoStreams:      c.VideoStreams,
		VideoCodec:        c.VideoCodec,
		VideoProfile:      normalizeVideoProfile(c.VideoProfile),
		VideoLevel:        c.VideoLevel,
		CodecConfigSHA256: c.VideoExtradataSHA256,
		PixelFormat:       c.PixelFormat,
		Width:             c.Width,
		Height:            c.Height,
		FPSNum:            c.FPS.Num,
		FPSDen:            c.FPS.Den,
		VideoTimeBaseNum:  c.VideoTimeBase.Num,
		VideoTimeBaseDen:  c.VideoTimeBase.Den,
		AudioTimeBaseNum:  c.AudioTimeBase.Num,
		AudioTimeBaseDen:  c.AudioTimeBase.Den,
		SARNum:            c.SAR.Num,
		SARDen:            c.SAR.Den,
		ColorRange:        c.ColorRange,
		ColorSpace:        c.ColorSpace,
		ColorTransfer:     c.ColorTransfer,
		ColorPrimaries:    c.ColorPrimaries,
		FieldOrder:        c.FieldOrder,
		KeyframeInterval:  c.KeyframeInterval,
		StartPTS:          c.StartPTS,
		HasAudio:          true,
		AudioStreams:      c.AudioStreams,
		AudioCodec:        c.AudioCodec,
		AudioProfile:      c.AudioProfile,
		SampleRate:        c.AudioSampleRate,
		Channels:          c.AudioChannels,
		ChannelLayout:     c.AudioChannelLayout,
		AudioBitrate:      c.AudioBitrate,
	}
}

// TestContractChecksAreReachable proves every declared dimension can actually
// fail. A check whose guard is never satisfiable (an empty contract value, a
// field nobody projects) is a dimension that is documented as enforced and is
// not.
func TestContractChecksAreReachable(t *testing.T) {
	contract := kernelmedia.DefaultAssemblyMediaContract()
	resolved := &ResolvedContract{AssemblyMediaContract: contract}

	if err := ValidateContract(resolved, satisfyingProbe(contract)); err != nil {
		t.Fatalf("the canonical satisfying probe must pass the gate: %v", err)
	}

	mutators := map[string]func(*OutputProbe){
		"container":            func(p *OutputProbe) { p.Container = "mkv" },
		"video-stream-present": func(p *OutputProbe) { p.HasVideo = false },
		"video-codec":          func(p *OutputProbe) { p.VideoCodec = "hevc" },
		"video-profile":        func(p *OutputProbe) { p.VideoProfile = "main" },
		"video-level":          func(p *OutputProbe) { p.VideoLevel = "5.1" },
		"pixel-format":         func(p *OutputProbe) { p.PixelFormat = "yuv422p" },
		"geometry":             func(p *OutputProbe) { p.Width = p.Width + 1 },
		"fps":                  func(p *OutputProbe) { p.FPSNum = p.FPSNum + 1 },
		"video-timebase":       func(p *OutputProbe) { p.VideoTimeBaseNum, p.VideoTimeBaseDen = 1, 7 },
		"audio-timebase":       func(p *OutputProbe) { p.AudioTimeBaseNum, p.AudioTimeBaseDen = 1, 7 },
		"sar":                  func(p *OutputProbe) { p.SARNum = p.SARNum + 1 },
		"color-range":          func(p *OutputProbe) { p.ColorRange = "pc" },
		"color-space":          func(p *OutputProbe) { p.ColorSpace = "bt470bg" },
		"color-transfer":       func(p *OutputProbe) { p.ColorTransfer = "smpte2084" },
		"color-primaries":      func(p *OutputProbe) { p.ColorPrimaries = "bt2020" },
		"field-order":          func(p *OutputProbe) { p.FieldOrder = "tt" },
		"keyframe-interval":    func(p *OutputProbe) { p.KeyframeInterval = p.KeyframeInterval + 1 },
		"audio-contract":       func(p *OutputProbe) { p.HasAudio = false },
		"video-streams":        func(p *OutputProbe) { p.VideoStreams = p.VideoStreams + 1 },
		"audio-streams":        func(p *OutputProbe) { p.AudioStreams = p.AudioStreams + 1 },
		"start-pts":            func(p *OutputProbe) { p.StartPTS = p.StartPTS + 1 },
	}

	known := make(map[string]bool, len(contractChecks))
	for _, check := range contractChecks {
		known[check.dim] = true
		mutate, ok := mutators[check.dim]
		if !ok {
			t.Fatalf("contract check %q has no violation mutator: add one to prove the dimension is reachable", check.dim)
		}
		probe := satisfyingProbe(contract)
		mutate(probe)
		if bad, _ := check.apply(resolved, probe); !bad {
			t.Errorf("contract check %q did not fail on a violating probe: the dimension is not actually enforced", check.dim)
		}
	}

	for dim := range mutators {
		if !known[dim] {
			t.Errorf("violation mutator %q addresses no contract check: stale test row or removed dimension", dim)
		}
	}
}
