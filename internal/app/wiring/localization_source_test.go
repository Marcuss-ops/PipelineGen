package wiring

// localization_source_test.go — the SourceResolver adapter: asset registry →
// materialized bytes → verified hash → rational frame rate, fail-closed at
// every step.

import (
	"context"
	"errors"
	"strings"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
)

// fakeLocalizationAssetResolver returns a fixed AssetRef (or error), recording
// the requested asset id.
type fakeLocalizationAssetResolver struct {
	ref   *cliprender.AssetRef
	err   error
	gotID string
}

func (f *fakeLocalizationAssetResolver) ResolveAsset(_ context.Context, assetID string) (*cliprender.AssetRef, error) {
	f.gotID = assetID
	if f.err != nil {
		return nil, f.err
	}
	return f.ref, nil
}

// fakeLocalizationAssetMaterializer returns a fixed MaterializedAsset (or
// error), recording the ref it was handed.
type fakeLocalizationAssetMaterializer struct {
	mat    *cliprender.MaterializedAsset
	err    error
	gotRef cliprender.AssetRef
}

func (f *fakeLocalizationAssetMaterializer) Materialize(_ context.Context, ref cliprender.AssetRef) (*cliprender.MaterializedAsset, error) {
	f.gotRef = ref
	if f.err != nil {
		return nil, f.err
	}
	return f.mat, nil
}

// fakeLocalizationMediaProber returns a fixed MediaInfo (or error), recording
// the probed path.
type fakeLocalizationMediaProber struct {
	info    *mediaexec.MediaInfo
	err     error
	gotPath string
}

func (f *fakeLocalizationMediaProber) Probe(_ context.Context, path string) (*mediaexec.MediaInfo, error) {
	f.gotPath = path
	if f.err != nil {
		return nil, f.err
	}
	return f.info, nil
}

func srcTestSHA(n int) string { return strings.Repeat(string(rune('a'+n)), 64) }

func srcTestRef(hash string) *cliprender.AssetRef {
	return &cliprender.AssetRef{AssetID: "source-1", LegacyFileMD5: hash}
}

func srcTestMaterial(sha string) *cliprender.MaterializedAsset {
	return &cliprender.MaterializedAsset{AssetID: "source-1", LocalPath: "/media/source.mp4", SHA256: sha}
}

func srcTestInfo(num, den int) *mediaexec.MediaInfo {
	return &mediaexec.MediaInfo{FPSNum: num, FPSDen: den, FPS: float64(num) / float64(den)}
}

func newTestSourceResolver(t *testing.T, resolve cliprender.AssetResolver, material cliprender.AssetMaterializer, probe mediaProber) *localizationSourceResolver {
	t.Helper()
	return newLocalizationSourceResolver(resolve, material, probe)
}

func TestLocalizationSourceResolver_ResolvesVerifiedFacts(t *testing.T) {
	sha := srcTestSHA(0)
	ref := srcTestRef(sha)
	mat := srcTestMaterial(sha)
	resolve := &fakeLocalizationAssetResolver{ref: ref}
	material := &fakeLocalizationAssetMaterializer{mat: mat}
	probe := &fakeLocalizationMediaProber{info: srcTestInfo(30000, 1001)}

	r := newTestSourceResolver(t, resolve, material, probe)
	facts, err := r.ResolveSource(context.Background(), "source-1")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if facts.AssetID != "source-1" || facts.LocalPath != "/media/source.mp4" || facts.SHA256 != sha {
		t.Fatalf("facts: %+v", facts)
	}
	if facts.FrameRate.Numerator != 30000 || facts.FrameRate.Denominator != 1001 {
		t.Fatalf("frame rate: got %d/%d", facts.FrameRate.Numerator, facts.FrameRate.Denominator)
	}
	if resolve.gotID != "source-1" {
		t.Errorf("resolver got id %q", resolve.gotID)
	}
	if material.gotRef.AssetID != "source-1" || material.gotRef.LegacyFileMD5 != sha {
		t.Errorf("materializer got ref %+v", material.gotRef)
	}
	if probe.gotPath != "/media/source.mp4" {
		t.Errorf("probe got path %q", probe.gotPath)
	}
}

func TestLocalizationSourceResolver_RejectsRegistryHashMismatch(t *testing.T) {
	sha := srcTestSHA(0)
	other := srcTestSHA(1)
	resolve := &fakeLocalizationAssetResolver{ref: srcTestRef(sha)}
	material := &fakeLocalizationAssetMaterializer{mat: srcTestMaterial(other)}
	probe := &fakeLocalizationMediaProber{info: srcTestInfo(30, 1)}

	r := newTestSourceResolver(t, resolve, material, probe)
	if _, err := r.ResolveSource(context.Background(), "source-1"); err == nil {
		t.Fatal("ResolveSource must reject a registry/local hash mismatch")
	}
}

func TestLocalizationSourceResolver_SkipsNonCleanRegistryHash(t *testing.T) {
	sha := srcTestSHA(0)
	// A prefixed legacy hash must not produce a false mismatch.
	resolve := &fakeLocalizationAssetResolver{ref: srcTestRef("sha256:" + sha)}
	material := &fakeLocalizationAssetMaterializer{mat: srcTestMaterial(sha)}
	probe := &fakeLocalizationMediaProber{info: srcTestInfo(30, 1)}

	r := newTestSourceResolver(t, resolve, material, probe)
	facts, err := r.ResolveSource(context.Background(), "source-1")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if facts.SHA256 != sha {
		t.Fatalf("SHA256: got %q, want %q", facts.SHA256, sha)
	}
}

func TestLocalizationSourceResolver_RejectsNotFound(t *testing.T) {
	r := newTestSourceResolver(t, &fakeLocalizationAssetResolver{ref: nil}, &fakeLocalizationAssetMaterializer{}, &fakeLocalizationMediaProber{})
	if _, err := r.ResolveSource(context.Background(), "missing"); err == nil {
		t.Fatal("ResolveSource must reject a not-found asset")
	}
}

func TestLocalizationSourceResolver_PropagatesResolverError(t *testing.T) {
	r := newTestSourceResolver(t, &fakeLocalizationAssetResolver{err: errors.New("registry down")}, &fakeLocalizationAssetMaterializer{}, &fakeLocalizationMediaProber{})
	if _, err := r.ResolveSource(context.Background(), "source-1"); err == nil {
		t.Fatal("ResolveSource must propagate a resolver error")
	}
}

func TestLocalizationSourceResolver_PropagatesMaterializerError(t *testing.T) {
	r := newTestSourceResolver(t, &fakeLocalizationAssetResolver{ref: srcTestRef("")}, &fakeLocalizationAssetMaterializer{err: errors.New("drive down")}, &fakeLocalizationMediaProber{})
	if _, err := r.ResolveSource(context.Background(), "source-1"); err == nil {
		t.Fatal("ResolveSource must propagate a materializer error")
	}
}

func TestLocalizationSourceResolver_RejectsIncompleteMaterial(t *testing.T) {
	r := newTestSourceResolver(t, &fakeLocalizationAssetResolver{ref: srcTestRef("")}, &fakeLocalizationAssetMaterializer{mat: &cliprender.MaterializedAsset{AssetID: "source-1", LocalPath: "/x.mp4"}}, &fakeLocalizationMediaProber{})
	if _, err := r.ResolveSource(context.Background(), "source-1"); err == nil {
		t.Fatal("ResolveSource must reject an incomplete materialized asset")
	}
}

func TestLocalizationSourceResolver_PropagatesProbeError(t *testing.T) {
	sha := srcTestSHA(0)
	r := newTestSourceResolver(t, &fakeLocalizationAssetResolver{ref: srcTestRef(sha)}, &fakeLocalizationAssetMaterializer{mat: srcTestMaterial(sha)}, &fakeLocalizationMediaProber{err: errors.New("probe failed")})
	if _, err := r.ResolveSource(context.Background(), "source-1"); err == nil {
		t.Fatal("ResolveSource must propagate a probe error")
	}
}

func TestLocalizationSourceResolver_RejectsInvalidFrameRate(t *testing.T) {
	sha := srcTestSHA(0)
	for name, info := range map[string]*mediaexec.MediaInfo{
		"nil info":         nil,
		"zero numerator":   {FPSNum: 0, FPSDen: 1},
		"zero denominator": {FPSNum: 30, FPSDen: 0},
	} {
		t.Run(name, func(t *testing.T) {
			r := newTestSourceResolver(t, &fakeLocalizationAssetResolver{ref: srcTestRef(sha)}, &fakeLocalizationAssetMaterializer{mat: srcTestMaterial(sha)}, &fakeLocalizationMediaProber{info: info})
			if _, err := r.ResolveSource(context.Background(), "source-1"); err == nil {
				t.Fatal("ResolveSource must reject an invalid frame rate")
			}
		})
	}
}

func TestLocalizationSourceResolver_NilDepsFailClosed(t *testing.T) {
	sha := srcTestSHA(0)
	if _, err := newLocalizationSourceResolver(nil, &fakeLocalizationAssetMaterializer{mat: srcTestMaterial(sha)}, &fakeLocalizationMediaProber{info: srcTestInfo(30, 1)}).ResolveSource(context.Background(), "source-1"); err == nil {
		t.Fatal("nil resolver must fail closed")
	}
	if _, err := newLocalizationSourceResolver(&fakeLocalizationAssetResolver{ref: srcTestRef(sha)}, nil, &fakeLocalizationMediaProber{info: srcTestInfo(30, 1)}).ResolveSource(context.Background(), "source-1"); err == nil {
		t.Fatal("nil materializer must fail closed")
	}
	if _, err := newLocalizationSourceResolver(&fakeLocalizationAssetResolver{ref: srcTestRef(sha)}, &fakeLocalizationAssetMaterializer{mat: srcTestMaterial(sha)}, nil).ResolveSource(context.Background(), "source-1"); err == nil {
		t.Fatal("nil probe must fail closed")
	}
}
