// Package adminconsole — typed admin entity registry (godlike/06 SSOT).
//
// The registry exposes a schema-driven view of every administrative
// entity in PipelineGen. It is consumed by the HTTP facade in
// internal/api/adminconsole and by future UI pages (Database Explorer,
// Audit Log, etc.).
package adminconsole

import (
	"context"
	"errors"
)

// Sentinel errors returned by the admin console layer.
var (
	ErrNotSupported       = errors.New("adminconsole: operation not supported")
	ErrNotEditable        = errors.New("adminconsole: entity is not editable")
	ErrActionNotSupported = errors.New("adminconsole: action not supported")
)

// FieldType is the canonical wire type for a schema field.
type FieldType string

const (
	FieldString      FieldType = "string"
	FieldInt         FieldType = "int"
	FieldBool        FieldType = "bool"
	FieldStringArray FieldType = "string_array"
	FieldDate        FieldType = "date"
	FieldJSON        FieldType = "json"
	FieldEnum        FieldType = "enum"
)

// FieldDescriptor describes one column/field of an entity.
type FieldDescriptor struct {
	Key         string    `json:"key"`
	Label       string    `json:"label"`
	Type        FieldType `json:"type"`
	Editable    bool      `json:"editable"`
	Required    bool      `json:"required"`
	Filterable  bool      `json:"filterable"`
	Sortable    bool      `json:"sortable"`
	Options     []string  `json:"options,omitempty"`
	Description string    `json:"description,omitempty"`
}

// ActionDescriptor describes an action available on an entity row.
type ActionDescriptor struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Dangerous   bool   `json:"dangerous,omitempty"`
}

// EntityDescriptor is the schema and behaviour contract for one entity.
type EntityDescriptor struct {
	Key            string             `json:"key"`
	Label          string             `json:"label"`
	Readable       bool               `json:"readable"`
	Editable       bool               `json:"editable"`
	BulkEditable   bool               `json:"bulk_editable"`
	PrimaryKey     string             `json:"primary_key"`
	ListFields     []FieldDescriptor  `json:"list_fields"`
	DetailFields   []FieldDescriptor  `json:"detail_fields"`
	EditableFields []FieldDescriptor  `json:"editable_fields"`
	Actions        []ActionDescriptor `json:"actions"`
	Repository     EntityReader
	Mutator        EntityMutator
}

// EntityReader is the read-only port for an entity.
type EntityReader interface {
	// List returns paginated items and the total count.
	List(ctx context.Context, opts ListOptions) (ListResult, error)
	// Get returns a single item by ID.
	Get(ctx context.Context, id string) (map[string]any, error)
}

// EntityMutator is the write port for an entity.
type EntityMutator interface {
	// Patch applies the requested changes and returns the updated item.
	Patch(ctx context.Context, id string, changes map[string]any, expectedVersion int) (map[string]any, error)
	// Action executes a named action on the item.
	Action(ctx context.Context, id, action string, payload map[string]any) (map[string]any, error)
}

// ListOptions is passed to EntityReader.List.
type ListOptions struct {
	Filters  map[string]string
	OrderBy  string
	OrderDir string
	Limit    int
	Offset   int
}

// ListResult is returned by EntityReader.List.
type ListResult struct {
	Items []map[string]any
	Total int
}

// SchemaResponse is the wire shape for GET /entities/:entity/schema.
type SchemaResponse struct {
	Entity       string             `json:"entity"`
	Label        string             `json:"label"`
	PrimaryKey   string             `json:"primary_key"`
	Readable     bool               `json:"readable"`
	Editable     bool               `json:"editable"`
	BulkEditable bool               `json:"bulk_editable"`
	Fields       []FieldDescriptor  `json:"fields"`
	Actions      []ActionDescriptor `json:"actions"`
}

// ListResponse is the wire shape for GET /entities/:entity.
type ListResponse struct {
	Items  []map[string]any `json:"items"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}
