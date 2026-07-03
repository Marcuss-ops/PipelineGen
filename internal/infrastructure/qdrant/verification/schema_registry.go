package verification

import (
	"fmt"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// SchemaRegistry holds the canonical schema.IndexSchema instances for every
// registered schema version. It is the single source of truth for
// "what does schema X look like" — adding a new schema means
// registering a new entry; cfg.Qdrant.CollectionVersion simply picks
// one entry.
//
// PR 6 (refactor/qdrant-index-document, §#4): the registry replaces
// the previous facade `qdrant.schema.DefaultV3Schema()` that every boot
// path called independently. After this change, NewRuntime reads
// cfg.QdrantCfg.CollectionVersion, calls DefaultSchemaRegistry.Resolve,
// and every subsystem receives the resolved *schema.IndexSchema from the
// runtime. The registry is now THE SSOT for the version-keyed
// manifest.
//
// Frozen tests in composition_test.go pin that the registry is the
// canonical construction path; legacy schema.DefaultV3Schema() calls stay
// only as a reference helper for tests and ad-hoc admin scripts.
type SchemaRegistry struct {
	schemas map[string]*schema.IndexSchema
}

// NewSchemaRegistry constructs an empty registry. Production code
// uses the package-level DefaultSchemaRegistry (pre-loaded); tests
// that need isolation (or a custom fake schema) construct their
// own registry via this constructor.
func NewSchemaRegistry() *SchemaRegistry {
	return &SchemaRegistry{
		schemas: make(map[string]*schema.IndexSchema),
	}
}

// Register adds schema to the registry. Failures PANIC at boot
// because they indicate a config-level invariant violation (the
// alternative — silently overwrite — masks regressions). The
// panics only fire from init() in production; tests that register
// bad schemas recover via a deferred recover() if they exercise
// the failure path explicitly.
func (r *SchemaRegistry) Register(schema *schema.IndexSchema) {
	if schema == nil {
		panic("qdrant.SchemaRegistry.Register: schema is nil (boot code passed nil — fail-fast)")
	}
	if schema.Version == "" {
		panic(fmt.Sprintf("qdrant.SchemaRegistry.Register: schema.Version must be non-empty (got %+v)", schema))
	}
	if _, exists := r.schemas[schema.Version]; exists {
		panic(fmt.Sprintf("qdrant.SchemaRegistry.Register: duplicate version %q (boot code registered twice; would silently overwrite otherwise)", schema.Version))
	}
	if err := schema.Validate(); err != nil {
		panic(fmt.Sprintf("qdrant.SchemaRegistry.Register: invalid schema for version %q: %v", schema.Version, err))
	}
	r.schemas[schema.Version] = schema
}

// Resolve returns the schema registered under `version`. Empty
// `version` defaults to "v3" — this preserves boot compatibility
// with legacy operator configs that don't set cfg.collection_version.
// Unknown version returns ErrSchemaVersionNotFound so callers can
// surface a meaningful error to the operator instead of panicking
// from a nil deref.
func (r *SchemaRegistry) Resolve(version string) (*schema.IndexSchema, error) {
	if version == "" {
		version = "v3"
	}
	s, ok := r.schemas[version]
	if !ok {
		available := r.Versions()
		return nil, fmt.Errorf("%w: version %q not registered (available: %v)",
			ErrSchemaVersionNotFound, version, available)
	}
	return s, nil
}

// MustResolve is Resolve-or-panic. Use ONLY in composition roots
// (boot-time configuration interpretation where a missing version
// is a config error and the process must fail loud at startup
// before serving any query). Runtime callers in the request path
// use Resolve + error propagation.
func (r *SchemaRegistry) MustResolve(version string) *schema.IndexSchema {
	s, err := r.Resolve(version)
	if err != nil {
		panic(err.Error())
	}
	return s
}

// Versions returns the sorted list of registered versions. Useful
// for dry-run diagnostics ("all registered schemas") and the boot
// probe test that asserts the registry holds ≥2 entries (so the
// mechanism is not a single-value facade).
func (r *SchemaRegistry) Versions() []string {
	out := make([]string, 0, len(r.schemas))
	for v := range r.schemas {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// ErrSchemaVersionNotFound is the typed error returned by
// SchemaRegistry.Resolve when the requested version is not
// registered. Callers gate on errors.Is(err, ErrSchemaVersionNotFound)
// to surface a meaningful operator error.
var ErrSchemaVersionNotFound = fmt.Errorf("qdrant: schema version not found")

// defaultSchemaRegistry is the package-level registry populated at
// init() with the canonical PipelineGen schemas. Unexported per PR #11
// (July 2026) — the registry is sealed after init(); external callers
// resolve via the package-level ResolveSchema() / MustResolveSchema() /
// RegisteredVersions() which return deep copies so the registry's
// internal instances stay immutable.
var defaultSchemaRegistry = NewSchemaRegistry()

// ResolveSchema returns a deep copy of the schema registered under `version`.
// Empty `version` defaults to "v3". Callers receive a mutable copy —
// the registry's canonical instance is never mutated through this path.
func ResolveSchema(version string) (*schema.IndexSchema, error) {
	s, err := defaultSchemaRegistry.Resolve(version)
	if err != nil {
		return nil, err
	}
	return s.DeepCopy(), nil
}

// MustResolveSchema is ResolveSchema-or-panic.
func MustResolveSchema(version string) *schema.IndexSchema {
	s, err := ResolveSchema(version)
	if err != nil {
		panic(err.Error())
	}
	return s
}

// RegisteredVersions returns the sorted list of registered versions.
func RegisteredVersions() []string { return defaultSchemaRegistry.Versions() }

func init() {
	defaultSchemaRegistry.Register(schema.DefaultV3Schema())
	defaultSchemaRegistry.Register(DefaultV3SpeakerSchema())
}

// DefaultV3SpeakerSchema is the second-registered schema
// (PR 6 §#4 acceptance: registry must hold ≥2 schemas so the
// mechanism is not a single-value facade). It differs from
// schema.DefaultV3Schema by adding a dedicated `speaker` dense vector
// channel (256-dim, Cosine) for accent / speaker-diarisation use
// cases that the canonical v3 doesn't carry. schema.CompareSchema() reports
// the missing vector on the wire side; payload indexes that DID
// exist in v3 stay identical so the only observable structural
// delta is the dense channel set.
//
// Operators that opt into `collection_version: "v3-multilingual-speaker"`
// in cfg.Qdrant trigger a fresh collection creation; the new physical
// name (`media_assets_v3_e5_768_siglip_768_speaker_256`) reflects the
// extra channel in the deterministic suffix.
func DefaultV3SpeakerSchema() *schema.IndexSchema {
	s := schema.DefaultV3Schema() // copy base
	s.Version = "v3-multilingual-speaker"
	s.PhysicalName = "media_assets_v3_e5_768_siglip_768_speaker_256"
	s.DenseVectors = append(s.DenseVectors, schema.EmbeddingSpec{
		Channel:       "speaker",
		Model:         "pyannote-embedding-256",
		ModelVersion:  "2026-06-16-v1",
		Dimensions:    256,
		Distance:      "Cosine",
		Normalized:    true,
		PreprocessVer: "v1-speaker",
	})
	return s
}
