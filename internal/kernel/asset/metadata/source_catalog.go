package metadata

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// SourceDefinition is the canonical description of a media source.
// Source aliases and metadata are owned here so application and
// infrastructure consumers cannot drift into parallel normalizers.
type SourceDefinition struct {
	Canonical string
	Aliases   []string
	MediaType string
}

var (
	ErrSourceCatalogNilDefinition = errors.New("asset source catalog: nil definition")
	ErrSourceCatalogEmptyName     = errors.New("asset source catalog: canonical name is empty")
	ErrSourceCatalogDuplicate     = errors.New("asset source catalog: duplicate source")
	ErrSourceCatalogAliasConflict = errors.New("asset source catalog: alias conflict")
)

// SourceCatalogBuilder constructs the immutable source catalog used by
// the application and infrastructure layers. Add is intended for startup
// composition only; Build returns a read-only snapshot.
type SourceCatalogBuilder struct {
	definitions map[string]SourceDefinition
	aliases     map[string]string
}

// NewSourceCatalogBuilder returns an empty source catalog builder.
func NewSourceCatalogBuilder() *SourceCatalogBuilder {
	return &SourceCatalogBuilder{
		definitions: make(map[string]SourceDefinition),
		aliases:     make(map[string]string),
	}
}

// Add registers one canonical source and all of its aliases.
func (b *SourceCatalogBuilder) Add(def SourceDefinition) error {
	if b == nil {
		return ErrSourceCatalogNilDefinition
	}
	def.Canonical = normalizeSourceName(def.Canonical)
	if def.Canonical == "" {
		return ErrSourceCatalogEmptyName
	}
	if _, exists := b.definitions[def.Canonical]; exists {
		return fmt.Errorf("%w: %q", ErrSourceCatalogDuplicate, def.Canonical)
	}

	aliases := append([]string(nil), def.Aliases...)
	aliases = append(aliases, def.Canonical)
	for _, raw := range aliases {
		alias := normalizeSourceName(raw)
		if alias == "" {
			continue
		}
		if existing, exists := b.aliases[alias]; exists && existing != def.Canonical {
			return fmt.Errorf("%w: %q already maps to %q", ErrSourceCatalogAliasConflict, alias, existing)
		}
		b.aliases[alias] = def.Canonical
	}
	def.Aliases = uniqueSorted(aliases)
	b.definitions[def.Canonical] = def
	return nil
}

// Build returns an immutable source catalog snapshot.
func (b *SourceCatalogBuilder) Build() *SourceCatalog {
	if b == nil {
		return &SourceCatalog{}
	}
	definitions := make(map[string]SourceDefinition, len(b.definitions))
	for name, def := range b.definitions {
		def.Aliases = append([]string(nil), def.Aliases...)
		definitions[name] = def
	}
	aliases := make(map[string]string, len(b.aliases))
	for alias, canonical := range b.aliases {
		aliases[alias] = canonical
	}
	return &SourceCatalog{definitions: definitions, aliases: aliases}
}

// SourceCatalog is an immutable, deterministic source registry.
type SourceCatalog struct {
	definitions map[string]SourceDefinition
	aliases     map[string]string
}

// Canonical resolves a source name or alias. Unknown names return "".
func (c *SourceCatalog) Canonical(source string) string {
	if c == nil {
		return ""
	}
	return c.aliases[normalizeSourceName(source)]
}

// Definition returns the canonical definition for a source or alias.
func (c *SourceCatalog) Definition(source string) (SourceDefinition, bool) {
	canonical := c.Canonical(source)
	if canonical == "" {
		return SourceDefinition{}, false
	}
	def, ok := c.definitions[canonical]
	return def, ok
}

// Definitions returns canonical definitions in deterministic order.
func (c *SourceCatalog) Definitions() []SourceDefinition {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.definitions))
	for name := range c.definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]SourceDefinition, 0, len(names))
	for _, name := range names {
		def := c.definitions[name]
		def.Aliases = append([]string(nil), def.Aliases...)
		out = append(out, def)
	}
	return out
}

// Names returns canonical source names in deterministic order.
func (c *SourceCatalog) Names() []string {
	defs := c.Definitions()
	names := make([]string, len(defs))
	for i, def := range defs {
		names[i] = def.Canonical
	}
	return names
}

func normalizeSourceName(source string) string {
	return strings.ToLower(strings.TrimSpace(source))
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeSourceName(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

var (
	defaultSourceCatalog     *SourceCatalog
	defaultSourceCatalogOnce sync.Once
)

// DefaultSourceCatalog returns the process-wide canonical source catalog.
// It is built once and never mutated after startup.
func DefaultSourceCatalog() *SourceCatalog {
	defaultSourceCatalogOnce.Do(func() {
		builder := NewSourceCatalogBuilder()
		for _, def := range []SourceDefinition{
			{Canonical: "artlist", Aliases: []string{"artlist"}, MediaType: "video"},
			{Canonical: "clips", Aliases: []string{"youtube", "youtube_clip", "clip", "clips"}, MediaType: "video"},
			{Canonical: "stock", Aliases: []string{"stock", "ai_generated"}, MediaType: "video"},
			{Canonical: "voiceover", Aliases: []string{"voiceover", "audio"}, MediaType: "audio"},
			{Canonical: "images", Aliases: []string{"image", "images"}, MediaType: "image"},
			{Canonical: "sound_effect", Aliases: []string{"sound_effect", "sound_effects", "sfx"}, MediaType: "audio"},
		} {
			if err := builder.Add(def); err != nil {
				panic(err)
			}
		}
		defaultSourceCatalog = builder.Build()
	})
	return defaultSourceCatalog
}
