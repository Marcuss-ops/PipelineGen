#!/usr/bin/env python3
"""Wave 6: enforce aggregator singleton (collapse duplicate search.NewAggregator).

Idempotent: each edit checks if NEW content already present (skip), or OLD content
present (replace); otherwise fail loudly.
"""
import sys

def apply_edit(path, new_marker, old, new, edit_name):
    with open(path, 'r') as f:
        c = f.read()
    if new_marker in c and old not in c:
        print(f'{edit_name}: ALREADY APPLIED (skip)')
        return
    if old not in c:
        print(f'{edit_name} FAIL: old pattern not found in {path}')
        sys.exit(1)
    c = c.replace(old, new, 1)
    with open(path, 'w') as f:
        f.write(c)
    print(f'{edit_name} OK')

# Edit 1: assets_core.go -- add SearchAggregator field to SearchDeps
apply_edit(
    'internal/app/assets_core.go',
    new_marker='SearchAggregator *search.Aggregator',
    old='SearchBackendRegistry *search.BackendRegistry\n}',
    new='''SearchBackendRegistry *search.BackendRegistry

\t// SearchAggregator is the canonical godlike/06 SSOT one-owner-per-fact
\t// *search.Aggregator singleton constructed at composition time by
\t// BuildCanonicalSearchFanOut (internal/app/search_backends.go) and
\t// plumbed through RegistryWiring.searchAgg into this field. The api/
\t// layer NEVER constructs a second instance and WireAssets MUST consume
\t// this canonical (per percheck_search_aggregator_singleton
\t// forward-prevention + PR-DIAGNOSI-FINALE rule 6).
\tSearchAggregator *search.Aggregator
}''',
    edit_name='EDIT 1 (assets_core.go SearchAggregator field)',
)

# Edit 2: registry.go -- add unexported searchAgg field to RegistryWiring
apply_edit(
    'internal/app/registry.go',
    new_marker='searchAgg          *search.Aggregator',
    old='\tsearchBackends     *search.BackendRegistry\n\tidempotencyHandler gin.HandlerFunc\n',
    new='''\tsearchBackends     *search.BackendRegistry
\t// searchAgg is the canonical godlike/06 SSOT *search.Aggregator
\t// singleton (constructed once at composition time by
\t// BuildCanonicalSearchFanOut inside registerSearchBackend).
\t// Plumbed into AssetsModuleDeps.Search.SearchAggregator so WireAssets
\t// can consume without constructing a duplicate (per percheck_search_aggregator_singleton).
\tsearchAgg          *search.Aggregator
\tidempotencyHandler gin.HandlerFunc
''',
    edit_name='EDIT 2 (registry.go searchAgg field)',
)

# Edit 3: registry_search.go -- assign wiring.searchAgg = searchAgg
apply_edit(
    'internal/app/registry_search.go',
    new_marker='wiring.searchAgg = searchAgg',
    old='\twiring.searchFanOut = searchFanOut\n\twiring.searchBackends = searchBackends\n\treturn searchFanOut, searchBackends, searchAgg\n',
    new='''\twiring.searchFanOut = searchFanOut
\twiring.searchBackends = searchBackends
\twiring.searchAgg = searchAgg
\treturn searchFanOut, searchBackends, searchAgg
''',
    edit_name='EDIT 3 (registry_search.go wiring.searchAgg assignment)',
)

# Edit 4: registry_assets.go -- populate assetsDeps.Search.SearchAggregator
apply_edit(
    'internal/app/registry_assets.go',
    new_marker='SearchAggregator:       wiring.searchAgg,',
    old='\t\tSearchFanOut:          wiring.searchFanOut,\n\t\tSearchBackendRegistry: wiring.searchBackends,\n',
    new='''\t\tSearchFanOut:          wiring.searchFanOut,
\t\tSearchAggregator:       wiring.searchAgg,
\t\tSearchBackendRegistry: wiring.searchBackends,
''',
    edit_name='EDIT 4 (registry_assets.go SearchAggregator population)',
)

# Edit 5: wire_assets.go -- replace duplicate construction with canonical consumption + nil-guard
apply_edit(
    'internal/app/wire_assets.go',
    new_marker='searchAggregator := deps.Search.SearchAggregator',
    old='\tsearchAggregator := search.NewAggregator(searchBackends, &zapSearchLogAdapter{log: log})\n\tlog.Info("WireAssets: consumed pre-built canonical SearchFanOut",\n\t\tzap.Int("backends", len(searchBackends.All())))\n',
    new='''\t// godlike/06 SSOT one-owner-per-fact: consume the CANONICAL
\t// *search.Aggregator constructed exactly once at the composition
\t// root by BuildCanonicalSearchFanOut (internal/app/search_backends.go)
\t// and plumbed through RegistryWiring.searchAgg into
\t// AssetsModuleDeps.Search.SearchAggregator. NEVER instantiate
\t// search.NewAggregator here -- percheck_search_aggregator_singleton
\t// forward-prevention gate forbids a second construction site (a second
\t// aggregator would silently diverge query-routing backend membership:
\t// query A from aggregator-1 sees backend-set X+Y; query B from
\t// aggregator-2 sees X+Z -- godlike/07 NO-FAKE-AVAILABILITY with no error
\t// surface).
\tsearchAggregator := deps.Search.SearchAggregator
\tif err := ClassifyDepGet("WireAssets: deps.Search.SearchAggregator is nil (canonical search.Aggregator must be constructed at composition root in BuildCanonicalSearchFanOut and plumbed through RegistryWiring.searchAgg; the api/ layer NEVER builds a second instance; percheck_search_aggregator_singleton + godlike/07 fail-closed)", searchAggregator == nil, DepRequired, log); err != nil {
\t\treturn nil, err
\t}
\tlog.Info("WireAssets: consumed canonical *search.Aggregator from composition root",
\t\tzap.Int("searchFanOut_backends", len(searchFanOut.Backends())))
''',
    edit_name='EDIT 5 (wire_assets.go duplicate construction removed + nil-guard)',
)

print('=== ALL 5 EDITS APPLIED ===')
