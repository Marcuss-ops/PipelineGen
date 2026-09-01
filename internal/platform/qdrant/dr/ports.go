package dr

import (
	"context"
	"time"
)

type SnapshotStore interface {
	CreateSnapshot(context.Context, string) (*SnapshotDescription, error)
	RestoreSnapshot(context.Context, string, string) error
	ListSnapshots(context.Context, string) ([]SnapshotDescription, error)
	DeleteSnapshot(context.Context, string, string) error
	GetSnapshotURL(context.Context, string, string) (string, error)
}

type AliasSwitcher interface {
	SwitchAlias(context.Context, string, string, string) error
}

type CollectionCreator interface {
	CreateCollection(context.Context, string) error
}

type Verifier interface {
	VerifyReindex(context.Context, string, int) (*VerifyReport, error)
}

type DRMetrics interface {
	RecordAliasSwitch(string, float64)
	SetAliasCurrent(string, string)
}

type CollectionAgeReader interface {
	CreatedAt(context.Context, string) (time.Time, bool, error)
}

type RetentionExecutor interface {
	CleanupWithConfig(context.Context, RetentionConfig) (*RetentionResult, error)
}
