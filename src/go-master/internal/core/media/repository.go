package media

import "context"

type Repository interface {
	CreateItem(ctx context.Context, item *Item) error
	UpdateItem(ctx context.Context, item *Item) error
	GetItem(ctx context.Context, id string) (*Item, error)
	DeleteItem(ctx context.Context, id string) error
	ListItems(ctx context.Context, query SearchQuery) ([]*Item, error)

	CreateFile(ctx context.Context, file *File) error
	UpdateFile(ctx context.Context, file *File) error
	ListFiles(ctx context.Context, mediaItemID string) ([]*File, error)

	CreateSource(ctx context.Context, source *Source) error
	GetSource(ctx context.Context, id string) (*Source, error)
	ListSources(ctx context.Context, workspaceID string) ([]*Source, error)

	AddTags(ctx context.Context, mediaItemID string, tagNames []string) error
	RemoveTags(ctx context.Context, mediaItemID string, tagNames []string) error
	ListTags(ctx context.Context, workspaceID string) ([]*Tag, error)

	RecordUsage(ctx context.Context, usage *Usage) error
	ListUsage(ctx context.Context, mediaItemID string) ([]*Usage, error)
}
