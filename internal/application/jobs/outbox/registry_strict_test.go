package outbox

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type strictTestIndexer struct{}

func (strictTestIndexer) IndexClip(context.Context, string) error { return nil }

type strictTestSourceQuerier struct{}

func (strictTestSourceQuerier) SourceVersionFor(context.Context, string) (string, error) {
	return "", nil
}

type strictTestVectorDeleter struct{}

func (strictTestVectorDeleter) DeletePoints(context.Context, []string) error { return nil }

func TestRegisterCoreHandlersFailsClosedOnMandatoryDependencies(t *testing.T) {
	log := zap.NewNop()
	cases := []struct {
		name    string
		indexer IndexClipper
		deps    *Deps
		want    string
	}{
		{
			name: "indexer",
			deps: &Deps{},
			want: "indexer=nil",
		},
		{
			name:    "deps",
			indexer: strictTestIndexer{},
			want:    "deps is nil",
		},
		{
			name:    "source version querier",
			indexer: strictTestIndexer{},
			deps:    &Deps{},
			want:    "SourceVersionQuerier=nil",
		},
		{
			name:    "vector point deleter",
			indexer: strictTestIndexer{},
			deps: &Deps{Jobs: JobDeps{
				SourceVersionQuerier: strictTestSourceQuerier{},
			}},
			want: "VectorPointDeleter=nil",
		},
		{
			name:    "asset deleter",
			indexer: strictTestIndexer{},
			deps: &Deps{Jobs: JobDeps{
				SourceVersionQuerier: strictTestSourceQuerier{},
				VectorPointDeleter:   strictTestVectorDeleter{},
			}},
			want: "AssetDeleter=nil",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := outboxevents.NewHandlerRegistry()
			err := RegisterCoreHandlers(registry, log, tc.indexer, tc.deps)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
			_, indexingRegistered := registry.Get(outboxevents.EventAssetIndexRequested)
			require.False(t, indexingRegistered, "failed strict registration must not leave partial handlers")
			_, deleteRegistered := registry.Get(outboxevents.EventAssetIndexDeleteRequested)
			require.False(t, deleteRegistered, "failed strict registration must not leave partial handlers")
		})
	}
}

func TestRegisterCoreHandlersRejectsNilRegistry(t *testing.T) {
	err := RegisterCoreHandlers(nil, zap.NewNop(), strictTestIndexer{}, &Deps{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "registry is nil")
}
