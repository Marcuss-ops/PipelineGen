package adminconsole

import (
	"context"
	"errors"
	"fmt"
)

// Service is the application-layer entry point for admin entity operations.
type Service struct {
	registry     *Registry
	audit        AuditLogger
	versionStore EntityVersionStore
}

// NewService creates the service from a registry, audit logger and version store.
// audit and versionStore may be nil; the service degrades gracefully by
// skipping audit logging and optimistic version checks in that case.
func NewService(r *Registry, audit AuditLogger, versionStore EntityVersionStore) *Service {
	if audit == nil {
		audit = NoOpAuditLogger{}
	}
	return &Service{registry: r, audit: audit, versionStore: versionStore}
}

// Registry returns the underlying registry.
func (s *Service) Registry() *Registry { return s.registry }

// SchemaFor returns the public schema for one entity.
func (s *Service) SchemaFor(entity string) (SchemaResponse, error) {
	desc, ok := s.registry.Get(entity)
	if !ok {
		return SchemaResponse{}, fmt.Errorf("unknown entity: %s", entity)
	}
	return SchemaResponse{
		Entity:       desc.Key,
		Label:        desc.Label,
		PrimaryKey:   desc.PrimaryKey,
		Readable:     desc.Readable,
		Editable:     desc.Editable,
		BulkEditable: desc.BulkEditable,
		Fields:       desc.ListFields,
		Actions:      desc.Actions,
	}, nil
}

// ListEntities returns the metadata of all registered entities.
func (s *Service) ListEntities() []SchemaResponse {
	entities := s.registry.List()
	out := make([]SchemaResponse, 0, len(entities))
	for _, desc := range entities {
		out = append(out, SchemaResponse{
			Entity:       desc.Key,
			Label:        desc.Label,
			PrimaryKey:   desc.PrimaryKey,
			Readable:     desc.Readable,
			Editable:     desc.Editable,
			BulkEditable: desc.BulkEditable,
			Fields:       desc.ListFields,
			Actions:      desc.Actions,
		})
	}
	return out
}

// List returns a paginated list for an entity.
func (s *Service) List(ctx context.Context, entity string, opts ListOptions) (ListResponse, error) {
	desc, ok := s.registry.Get(entity)
	if !ok {
		return ListResponse{}, fmt.Errorf("unknown entity: %s", entity)
	}
	if desc.Repository == nil {
		return ListResponse{Items: []map[string]any{}, Total: 0}, nil
	}
	if opts.Limit <= 0 {
		opts.Limit = 25
	}
	if opts.Limit > 500 {
		opts.Limit = 500
	}
	result, err := desc.Repository.List(ctx, opts)
	if err != nil {
		return ListResponse{}, err
	}
	return ListResponse{
		Items:  result.Items,
		Total:  result.Total,
		Limit:  opts.Limit,
		Offset: opts.Offset,
	}, nil
}

// Get returns a single entity row.
func (s *Service) Get(ctx context.Context, entity, id string) (map[string]any, error) {
	desc, ok := s.registry.Get(entity)
	if !ok {
		return nil, fmt.Errorf("unknown entity: %s", entity)
	}
	if desc.Repository == nil {
		return nil, errors.New("entity not readable")
	}
	return desc.Repository.Get(ctx, id)
}

// Patch applies a partial update to an entity row.
func (s *Service) Patch(ctx context.Context, entity, id string, changes map[string]any, expectedVersion int) (map[string]any, error) {
	desc, ok := s.registry.Get(entity)
	if !ok {
		return nil, fmt.Errorf("unknown entity: %s", entity)
	}
	if !desc.Editable || desc.Mutator == nil {
		return nil, errors.New("entity is not editable")
	}
	return desc.Mutator.Patch(ctx, id, changes, expectedVersion)
}

// Action executes a named action on an entity row.
func (s *Service) Action(ctx context.Context, entity, id, action string, payload map[string]any) (map[string]any, error) {
	desc, ok := s.registry.Get(entity)
	if !ok {
		return nil, fmt.Errorf("unknown entity: %s", entity)
	}
	if desc.Mutator == nil {
		return nil, errors.New("entity does not support actions")
	}
	return desc.Mutator.Action(ctx, id, action, payload)
}
