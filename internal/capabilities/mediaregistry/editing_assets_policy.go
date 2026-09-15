package mediaregistry

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"gopkg.in/yaml.v3"
)

// ── Editing-asset SELECTION policy ────────────────────────────────────────
//
// This is a policy, not a catalog. The catalogs (editorial_catalog.go,
// editorial_backgrounds.go) own WHICH assets exist and their identity; this
// file owns WHICH of them a job picks when the job does not name one, and with
// which default audio settings.
//
// The split matters: adding or retiring an asset is a catalog change, while
// changing the pool/default gain a job uses is a policy change. Keeping them
// in one file is what created the duplicated catalogs this registry replaced.

var (
	// ErrEmptyEditingPool is returned when a policy pool has no candidate.
	// An empty pool is a misconfiguration, never an instruction to skip the
	// asset: the caller must fail closed rather than render silence.
	ErrEmptyEditingPool = errors.New("editing assets policy: pool must not be empty")

	// ErrUnknownEditingAsset is returned when a pool names an alias that the
	// catalog does not bind. A policy may only select registered assets.
	ErrUnknownEditingAsset = errors.New("editing assets policy: unknown catalog asset")

	// ErrEditingAssetKindMismatch is returned when a pool places an asset in
	// the wrong domain (e.g. a BGM in the transition SFX pool).
	ErrEditingAssetKindMismatch = errors.New("editing assets policy: asset kind does not match the pool")
)

// EditingAssetsPolicy is the canonical selection policy for editorial assets.
// The zero value is invalid by design: DefaultEditingAssetsPolicy is the only
// canonical configuration and every override must still validate against the
// catalog.
type EditingAssetsPolicy struct {
	Backgrounds EditingBackgroundsPolicy `yaml:"backgrounds"`
	BGM         EditingBGMPolicy         `yaml:"bgm"`
	SFX         EditingSFXPolicy         `yaml:"sfx"`
}

// EditingBackgroundsPolicy selects the clip/overlay background plate.
type EditingBackgroundsPolicy struct {
	Pool []string `yaml:"pool"`
}

// EditingBGMPolicy selects the background music track and its mix defaults.
type EditingBGMPolicy struct {
	Pool               []string `yaml:"pool"`
	GainDB             float64  `yaml:"gain_db"`
	Loop               bool     `yaml:"loop"`
	DuckUnderVoiceover bool     `yaml:"duck_under_voiceover"`
	DuckGainDB         float64  `yaml:"duck_gain_db"`
}

// EditingSFXPolicy selects the transition sound effect and its gain default.
type EditingSFXPolicy struct {
	TransitionPool []string `yaml:"transition_pool"`
	GainDB         float64  `yaml:"gain_db"`
}

// EditingAssetsDocument is the on-disk projection shape (editing_assets:).
type EditingAssetsDocument struct {
	EditingAssets EditingAssetsPolicy `yaml:"editing_assets"`
}

// DefaultEditingAssetsPolicy derives the canonical policy from the catalog:
// every pool is the complete editorial pool of its domain, so a new catalog
// asset becomes selectable by default without a second edit here.
func DefaultEditingAssetsPolicy() EditingAssetsPolicy {
	bgmPool := make([]string, 0, len(editorialAudioAssets))
	transitionPool := make([]string, 0, len(editorialAudioAssets))
	for _, asset := range editorialAudioAssets {
		switch asset.Family {
		case "music":
			bgmPool = append(bgmPool, asset.Alias)
		case "transition":
			transitionPool = append(transitionPool, asset.Alias)
		}
	}
	return EditingAssetsPolicy{
		Backgrounds: EditingBackgroundsPolicy{Pool: EditorialBackgroundIDs()},
		BGM: EditingBGMPolicy{
			Pool:               bgmPool,
			GainDB:             -28,
			Loop:               true,
			DuckUnderVoiceover: true,
			DuckGainDB:         -32,
		},
		SFX: EditingSFXPolicy{TransitionPool: transitionPool, GainDB: -12},
	}
}

// ParseEditingAssetsPolicy decodes the YAML projection. Unknown fields fail
// closed so a typo in the policy is not silently ignored.
func ParseEditingAssetsPolicy(data []byte) (EditingAssetsPolicy, error) {
	var doc EditingAssetsDocument
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return EditingAssetsPolicy{}, fmt.Errorf("editing assets policy: decode: %w", err)
	}
	return doc.EditingAssets, nil
}

// Validate fails closed on an empty pool, an unknown alias or a domain
// mismatch. It is the only gate; callers that select must call it first.
func (p EditingAssetsPolicy) Validate() error {
	if err := validateBackgroundPool(p.Backgrounds.Pool); err != nil {
		return err
	}
	if err := validateAudioPool("bgm", p.BGM.Pool, "music"); err != nil {
		return err
	}
	if err := validateAudioPool("sfx.transition", p.SFX.TransitionPool, "transition"); err != nil {
		return err
	}
	if p.BGM.GainDB > 0 || p.BGM.DuckGainDB > 0 || p.SFX.GainDB > 0 {
		return fmt.Errorf("editing assets policy: gains must be <= 0 dB (bgm=%g duck=%g sfx=%g)", p.BGM.GainDB, p.BGM.DuckGainDB, p.SFX.GainDB)
	}
	return nil
}

func validateBackgroundPool(pool []string) error {
	if len(pool) == 0 {
		return fmt.Errorf("backgrounds: %w", ErrEmptyEditingPool)
	}
	for _, id := range pool {
		if _, ok := LookupEditorialBackground(id); !ok {
			return fmt.Errorf("backgrounds: %q: %w", id, ErrUnknownEditingAsset)
		}
	}
	return nil
}

func validateAudioPool(domain string, pool []string, family string) error {
	if len(pool) == 0 {
		return fmt.Errorf("%s: %w", domain, ErrEmptyEditingPool)
	}
	for _, alias := range pool {
		asset, ok := lookupEditorialAudioAsset(alias)
		if !ok {
			return fmt.Errorf("%s: %q: %w", domain, alias, ErrUnknownEditingAsset)
		}
		if asset.Family != family {
			return fmt.Errorf("%s: %q family=%q: %w", domain, alias, asset.Family, ErrEditingAssetKindMismatch)
		}
	}
	return nil
}

// SelectBackground deterministically picks a background plate from the pool.
// Selection is content-addressed (SHA-256 over purpose+seed) so a rerun of the
// same job is byte-identical; no RNG is involved.
func (p EditingAssetsPolicy) SelectBackground(seed string) (EditorialBackgroundAsset, error) {
	if err := validateBackgroundPool(p.Backgrounds.Pool); err != nil {
		return EditorialBackgroundAsset{}, err
	}
	id := p.Backgrounds.Pool[deterministicIndex(seed, "background", len(p.Backgrounds.Pool))]
	asset, ok := LookupEditorialBackground(id)
	if !ok {
		return EditorialBackgroundAsset{}, fmt.Errorf("backgrounds: %q: %w", id, ErrUnknownEditingAsset)
	}
	return asset, nil
}

// SelectBGM deterministically picks a background-music track from the pool.
func (p EditingAssetsPolicy) SelectBGM(seed string) (EditorialAudioAsset, error) {
	if err := validateAudioPool("bgm", p.BGM.Pool, "music"); err != nil {
		return EditorialAudioAsset{}, err
	}
	alias := p.BGM.Pool[deterministicIndex(seed, "bgm", len(p.BGM.Pool))]
	asset, _ := lookupEditorialAudioAsset(alias)
	return asset, nil
}

// SelectTransitionSFX deterministically picks a transition effect from the pool.
func (p EditingAssetsPolicy) SelectTransitionSFX(seed string) (EditorialAudioAsset, error) {
	if err := validateAudioPool("sfx.transition", p.SFX.TransitionPool, "transition"); err != nil {
		return EditorialAudioAsset{}, err
	}
	alias := p.SFX.TransitionPool[deterministicIndex(seed, "sfx.transition", len(p.SFX.TransitionPool))]
	asset, _ := lookupEditorialAudioAsset(alias)
	return asset, nil
}

// deterministicIndex maps a seed to a stable index in [0,n) using the
// canonical digest SSOT (kernel/digest). No RNG is involved, so a rerun of the
// same job selects the same asset.
func deterministicIndex(seed, purpose string, n int) int {
	fingerprint := digest.Fingerprint(purpose, seed)
	// Fingerprint always returns 64 lowercase hex characters, so this parse
	// cannot fail; if it ever did, index 0 keeps the caller fail-closed to a
	// deterministic in-pool asset instead of panicking mid-render.
	value, err := strconv.ParseUint(fingerprint[:16], 16, 64)
	if err != nil {
		return 0
	}
	return int(value % uint64(n))
}

// lookupEditorialAudioAsset resolves a canonical audio alias.
func lookupEditorialAudioAsset(alias string) (EditorialAudioAsset, bool) {
	alias = strings.ToLower(strings.TrimSpace(alias))
	for _, asset := range editorialAudioAssets {
		if asset.Alias == alias {
			return asset, true
		}
	}
	return EditorialAudioAsset{}, false
}
