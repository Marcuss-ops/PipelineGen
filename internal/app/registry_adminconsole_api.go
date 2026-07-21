package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	adminconsoleapi "github.com/Marcuss-ops/PipelineGen/internal/api/adminconsole"
	"github.com/Marcuss-ops/PipelineGen/internal/application/adminconsole"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	adminconsolesqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/adminconsole"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// registerAdminConsoleAPI wires the schema-driven admin console API.
// It registers entities backed by the existing application services
// without duplicating business logic.
func registerAdminConsoleAPI(registry *module.Registry, log *zap.Logger, cfg *config.Config, root *ComposeRoot) error {
	if root == nil || root.Repos == nil || root.Repos.Assets == nil {
		return fmt.Errorf("wire registry: adminconsole-api: asset service not available")
	}

	reg := adminconsole.NewRegistry()

	// ── assets ───────────────────────────────────────────────────────────
	reg.Register(adminconsole.EntityDescriptor{
		Key:          "assets",
		Label:        "Media Assets",
		Readable:     true,
		Editable:     true,
		BulkEditable: true,
		PrimaryKey:   "id",
		ListFields: []adminconsole.FieldDescriptor{
			{Key: "id", Type: adminconsole.FieldString, Label: "ID", Filterable: true, Sortable: true},
			{Key: "name", Type: adminconsole.FieldString, Label: "Name", Filterable: true, Sortable: true, Editable: true},
			{Key: "category", Type: adminconsole.FieldString, Label: "Category", Filterable: true, Sortable: true, Editable: true},
			{Key: "group", Type: adminconsole.FieldString, Label: "Group", Filterable: true, Sortable: true, Editable: true},
			{Key: "review_status", Type: adminconsole.FieldString, Label: "Review Status", Filterable: true, Sortable: true, Editable: true},
			{Key: "lifecycle_state", Type: adminconsole.FieldString, Label: "Lifecycle", Filterable: true, Sortable: true},
		},
		DetailFields: []adminconsole.FieldDescriptor{
			{Key: "id", Type: adminconsole.FieldString, Label: "ID"},
			{Key: "name", Type: adminconsole.FieldString, Label: "Name", Editable: true},
			{Key: "category", Type: adminconsole.FieldString, Label: "Category", Editable: true},
			{Key: "group", Type: adminconsole.FieldString, Label: "Group", Editable: true},
			{Key: "tags", Type: adminconsole.FieldStringArray, Label: "Tags", Editable: true},
			{Key: "search_terms", Type: adminconsole.FieldStringArray, Label: "Search Terms", Editable: true},
			{Key: "search_text", Type: adminconsole.FieldString, Label: "Search Text", Editable: true},
			{Key: "review_status", Type: adminconsole.FieldString, Label: "Review Status", Editable: true},
			{Key: "description", Type: adminconsole.FieldString, Label: "Description", Editable: true},
			{Key: "language", Type: adminconsole.FieldString, Label: "Language", Editable: true},
		},
		EditableFields: []adminconsole.FieldDescriptor{
			{Key: "name", Type: adminconsole.FieldString, Label: "Name", Required: true},
			{Key: "category", Type: adminconsole.FieldString, Label: "Category"},
			{Key: "group", Type: adminconsole.FieldString, Label: "Group"},
			{Key: "tags", Type: adminconsole.FieldStringArray, Label: "Tags"},
			{Key: "search_terms", Type: adminconsole.FieldStringArray, Label: "Search Terms"},
			{Key: "search_text", Type: adminconsole.FieldString, Label: "Search Text"},
			{Key: "review_status", Type: adminconsole.FieldString, Label: "Review Status"},
			{Key: "description", Type: adminconsole.FieldString, Label: "Description"},
			{Key: "language", Type: adminconsole.FieldString, Label: "Language"},
		},
		Actions: []adminconsole.ActionDescriptor{
			{Key: "reindex", Label: "Reindex"},
			{Key: "verify", Label: "Verify"},
			{Key: "reprocess", Label: "Reprocess"},
			{Key: "archive", Label: "Archive"},
		},
		Repository: &adminconsole.Adapter{
			ListFn: func(ctx context.Context, opts adminconsole.ListOptions) (adminconsole.ListResult, error) {
				filter := assetFilterFromOptions(opts)
				assets, err := root.Repos.Assets.List(ctx, filter)
				if err != nil {
					return adminconsole.ListResult{}, err
				}
				items := make([]map[string]any, 0, len(assets))
				for _, a := range assets {
					items = append(items, structToMap(a))
				}
				return adminconsole.ListResult{Items: items, Total: len(items)}, nil
			},
			GetFn: func(ctx context.Context, id string) (map[string]any, error) {
				details, err := root.Repos.Assets.Get(ctx, id)
				if err != nil {
					return nil, err
				}
				return structToMap(details), nil
			},
		},
		Mutator: &adminconsole.Adapter{
			PatchFn: func(ctx context.Context, id string, changes map[string]any, _ int) (map[string]any, error) {
				details, err := root.Repos.Assets.Get(ctx, id)
				if err != nil {
					return nil, err
				}
				if details.Asset == nil {
					return nil, fmt.Errorf("asset not found")
				}
				applyAssetChanges(details.Asset, changes)
				if err := root.Repos.Assets.Save(ctx, details); err != nil {
					return nil, err
				}
				return structToMap(details), nil
			},
			ActionFn: func(ctx context.Context, id, action string, payload map[string]any) (map[string]any, error) {
				// Placeholder: actions can be wired to the mutations dispatcher.
				_ = id
				_ = action
				_ = payload
				return nil, fmt.Errorf("action %q not yet implemented", action)
			},
		},
	})

	// ── clips ────────────────────────────────────────────────────────────
	reg.Register(adminconsole.EntityDescriptor{
		Key:        "clips",
		Label:      "Clips",
		Readable:   true,
		Editable:   false,
		PrimaryKey: "id",
		ListFields: []adminconsole.FieldDescriptor{
			{Key: "id", Type: adminconsole.FieldString, Label: "ID", Filterable: true, Sortable: true},
			{Key: "asset_id", Type: adminconsole.FieldString, Label: "Asset ID", Filterable: true},
			{Key: "status", Type: adminconsole.FieldString, Label: "Status", Filterable: true, Sortable: true},
		},
		Repository: &adminconsole.Adapter{},
	})

	// ── images ─────────────────────────────────────────────────────────────
	reg.Register(adminconsole.EntityDescriptor{
		Key:        "images",
		Label:      "Images",
		Readable:   true,
		Editable:   false,
		PrimaryKey: "id",
		ListFields: []adminconsole.FieldDescriptor{
			{Key: "id", Type: adminconsole.FieldString, Label: "ID", Filterable: true, Sortable: true},
			{Key: "url", Type: adminconsole.FieldString, Label: "URL", Filterable: true},
		},
		Repository: &adminconsole.Adapter{},
	})

	// ── scripts ────────────────────────────────────────────────────────────
	reg.Register(adminconsole.EntityDescriptor{
		Key:        "scripts",
		Label:      "Scripts",
		Readable:   true,
		Editable:   false,
		PrimaryKey: "id",
		ListFields: []adminconsole.FieldDescriptor{
			{Key: "id", Type: adminconsole.FieldString, Label: "ID", Filterable: true, Sortable: true},
			{Key: "title", Type: adminconsole.FieldString, Label: "Title", Filterable: true, Sortable: true},
		},
		Repository: &adminconsole.Adapter{},
	})

	// ── search_queries ─────────────────────────────────────────────────────
	reg.Register(adminconsole.EntityDescriptor{
		Key:        "search_queries",
		Label:      "Search Queries",
		Readable:   true,
		Editable:   false,
		PrimaryKey: "id",
		ListFields: []adminconsole.FieldDescriptor{
			{Key: "id", Type: adminconsole.FieldString, Label: "ID", Filterable: true, Sortable: true},
			{Key: "query", Type: adminconsole.FieldString, Label: "Query", Filterable: true, Sortable: true},
		},
		Repository: &adminconsole.Adapter{},
	})

	// ── jobs ───────────────────────────────────────────────────────────────
	reg.Register(adminconsole.EntityDescriptor{
		Key:        "jobs",
		Label:      "Jobs",
		Readable:   true,
		Editable:   false,
		PrimaryKey: "id",
		ListFields: []adminconsole.FieldDescriptor{
			{Key: "id", Type: adminconsole.FieldString, Label: "ID", Filterable: true, Sortable: true},
			{Key: "type", Type: adminconsole.FieldString, Label: "Type", Filterable: true},
			{Key: "status", Type: adminconsole.FieldString, Label: "Status", Filterable: true, Sortable: true},
		},
		Actions: []adminconsole.ActionDescriptor{
			{Key: "retry", Label: "Retry"},
			{Key: "cancel", Label: "Cancel"},
		},
		Repository: &adminconsole.Adapter{},
	})

	// ── outbox ─────────────────────────────────────────────────────────────
	reg.Register(adminconsole.EntityDescriptor{
		Key:        "outbox",
		Label:      "Outbox",
		Readable:   true,
		Editable:   false,
		PrimaryKey: "id",
		ListFields: []adminconsole.FieldDescriptor{
			{Key: "id", Type: adminconsole.FieldString, Label: "ID", Filterable: true, Sortable: true},
			{Key: "event_type", Type: adminconsole.FieldString, Label: "Event Type", Filterable: true},
			{Key: "status", Type: adminconsole.FieldString, Label: "Status", Filterable: true, Sortable: true},
		},
		Actions: []adminconsole.ActionDescriptor{
			{Key: "replay", Label: "Replay"},
		},
		Repository: &adminconsole.Adapter{},
	})

	// ── stock_batches ──────────────────────────────────────────────────────
	reg.Register(adminconsole.EntityDescriptor{
		Key:        "stock_batches",
		Label:      "Stock Batches",
		Readable:   true,
		Editable:   false,
		PrimaryKey: "id",
		ListFields: []adminconsole.FieldDescriptor{
			{Key: "id", Type: adminconsole.FieldString, Label: "ID", Filterable: true, Sortable: true},
			{Key: "status", Type: adminconsole.FieldString, Label: "Status", Filterable: true, Sortable: true},
		},
		Repository: &adminconsole.Adapter{},
	})

	// ── stock_artifacts ────────────────────────────────────────────────────
	reg.Register(adminconsole.EntityDescriptor{
		Key:        "stock_artifacts",
		Label:      "Stock Artifacts",
		Readable:   true,
		Editable:   false,
		PrimaryKey: "id",
		ListFields: []adminconsole.FieldDescriptor{
			{Key: "id", Type: adminconsole.FieldString, Label: "ID", Filterable: true, Sortable: true},
			{Key: "batch_id", Type: adminconsole.FieldString, Label: "Batch ID", Filterable: true},
		},
		Repository: &adminconsole.Adapter{},
	})

	auditStore := adminconsolesqlite.NewAuditStore(root.DB.DB)
	versionStore := adminconsolesqlite.NewVersionStore(root.DB.DB)

	svc := adminconsole.NewService(reg, auditStore, versionStore)
	desc := adminconsoleapi.Build(svc, log)

	if err := tryRegisterModuleStrict(registry, log, desc, WithRegistrationPoint("register.AdminConsole")); err != nil {
		return fmt.Errorf("wire registry: adminconsole-api module: %w", err)
	}
	return nil
}

// assetFilterFromOptions translates admin list options into the asset
// domain filter. Currently only limit/offset are propagated.
func assetFilterFromOptions(opts adminconsole.ListOptions) asset.Filter {
	return asset.Filter{
		Limit:  opts.Limit,
		Offset: opts.Offset,
	}
}

// applyAssetChanges updates the mutable fields of an asset from a
// change map. It supports the editable fields declared in the entity
// descriptor.
func applyAssetChanges(a *asset.Asset, changes map[string]any) {
	if a == nil || len(changes) == 0 {
		return
	}
	if v, ok := changes["name"].(string); ok {
		a.Name = v
	}
	if v, ok := changes["category"].(string); ok {
		a.Category = v
	}
	if v, ok := changes["group"].(string); ok {
		a.Group = v
	}
	if v, ok := changes["search_text"].(string); ok {
		a.SearchText = v
	}
	if v, ok := changes["review_status"].(string); ok {
		a.ReviewStatus = asset.ReviewStatus(v)
	}
	if raw, ok := changes["tags"]; ok {
		a.Tags = toStringSlice(raw)
	}
	if raw, ok := changes["search_terms"]; ok {
		a.SearchTerms = toStringSlice(raw)
	}
	if raw, ok := changes["description"]; ok {
		desc := ""
		if s, ok := raw.(string); ok {
			desc = s
		}
		if a.Metadata == nil {
			a.Metadata = make(map[string]any)
		}
		a.Metadata["description"] = desc
	}
	if raw, ok := changes["language"]; ok {
		lang := ""
		if s, ok := raw.(string); ok {
			lang = s
		}
		if a.Metadata == nil {
			a.Metadata = make(map[string]any)
		}
		a.Metadata["language"] = lang
	}
}

// toStringSlice coerces a value into a []string. It accepts []string,
// []any, or a single string (split by comma).
func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch vv := v.(type) {
	case []string:
		return vv
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if vv == "" {
			return nil
		}
		parts := []string{}
		for _, p := range strings.Split(vv, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				parts = append(parts, p)
			}
		}
		return parts
	}
	return nil
}

// structToMap converts a struct (or pointer) to a map[string]any using
// JSON round-tripping. It never returns nil; a nil input returns an
// empty map so callers can safely iterate.
func structToMap(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{"error": err.Error()}
	}
	return m
}
