package artifactcache_test

import (
	"testing"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestPreparationUnitAndArtifactKeyShareIdentity(t *testing.T) {
	const (
		sourceSHA256     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		processorVersion = "elevenlabs-v4"
		params           = `{"language":"en","voice_id":"voice-42","sample_rate":48000}`
	)

	unit := job.PreparationUnit{
		Kind:             "tts.synthesize",
		ProcessorVersion: processorVersion,
		Inputs: job.InputManifest{
			"language":    "en",
			"voice_id":    "voice-42",
			"sample_rate": 48000,
		},
	}
	key := capcache.Key{SourceSHA256: sourceSHA256, Operation: "tts.synthesize", ParametersJSON: params, ProcessorVersion: processorVersion}
	unitDigest, err := unit.ArtifactIdentityDigest(sourceSHA256)
	if err != nil {
		t.Fatalf("unit digest: %v", err)
	}
	keyDigest, err := key.Digest()
	if err != nil {
		t.Fatalf("cache digest: %v", err)
	}
	if unitDigest != keyDigest {
		t.Fatalf("identities differ: unit=%q key=%q", unitDigest, keyDigest)
	}
}

func TestPreparationUnitManifestCanonicalParams(t *testing.T) {
	u := job.PreparationUnit{Inputs: job.InputManifest{"voice_id": "voice-42", "language": "en"}}
	params, err := u.CanonicalParametersJSON()
	if err != nil {
		t.Fatalf("canonical params: %v", err)
	}
	if params == "" {
		t.Fatal("canonical parameters must not be empty")
	}
	if got, err := (job.PreparationUnit{}).CanonicalParametersJSON(); err != nil || got != "{}" {
		t.Fatalf("empty manifest = %q, err=%v", got, err)
	}
}

func TestPreparationFingerprintCanonicalizesPayloadForArtifactCache(t *testing.T) {
	const source = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first, err := jobs.PreparationUnitFingerprint("tts.synthesize", "script.generate", []byte(`{"voice":"v","rate":1}`), nil, nil, "tts-v1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := jobs.PreparationUnitFingerprint("tts.synthesize", "script.generate", []byte(` { "rate" : 1, "voice" : "v" } `), nil, nil, "tts-v1")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical payload fingerprints differ: %q != %q", first, second)
	}

	unit := job.PreparationUnit{Kind: "tts.synthesize", ProcessorVersion: "tts-v1", Inputs: job.InputManifest{"rate": 1, "voice": "v"}}
	unitDigest, err := unit.ArtifactIdentityDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	cacheDigest, err := (capcache.Key{SourceSHA256: source, Operation: "tts.synthesize", ParametersJSON: `{"voice":"v","rate":1}`, ProcessorVersion: "tts-v1"}).Digest()
	if err != nil {
		t.Fatal(err)
	}
	if unitDigest != cacheDigest {
		t.Fatalf("artifact identities differ: %q != %q", unitDigest, cacheDigest)
	}
}
