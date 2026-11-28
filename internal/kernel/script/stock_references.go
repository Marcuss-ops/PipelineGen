package script

// stock_references.go — the canonical answer to "which stock sources does this
// envelope bind, and as what?".
//
// Why a kernel walker (2026-11-27, CS27-06): POST /api/script/preflight used to
// verify clips only, so a payload whose stock reference could not be admitted
// still answered ok=true and then died at 85% in PUBLISHING_DOCUMENTS with
// "stock Drive file "…" has no saved media registry entry". The walk lives here,
// next to clip_requirements.go, because it is a payload rule and not a
// transport one: the preflight and any future submit-time gate must name the
// same references.
//
// The walk is deliberately envelope-only (no catalog, no Drive): it says WHAT
// to verify, never whether the source is reachable.
//
// ONE BINDING NAMES ONE SOURCE. The canonical media-identity gate forbids a
// struct that carries a content address (asset id) next to a location (Drive
// file id), because that is two claims about one fact. A stock binding really is
// a single reference — the contract asks for exactly one of asset_id,
// drive_link and folder_id — so the walk normalizes it to (Kind, ID) and keeps
// the raw declarations only when the caller broke that rule.

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// Stock reference kinds. Exactly one of the first three describes a well-formed
// binding; ambiguous and invalid_link describe a binding the preflight must
// reject instead of silently choosing a source for.
const (
	StockReferenceKindAsset     = "asset"
	StockReferenceKindDriveFile = "drive_file"
	StockReferenceKindFolder    = "folder"
	StockReferenceKindAmbiguous = "ambiguous"
	StockReferenceKindInvalid   = "invalid_link"
)

// StockReference is one stock source a placement binds, normalized to the single
// identity the preflight can verify.
type StockReference struct {
	// ItemID is the payload item that declares the binding.
	ItemID string
	// Project is the item's artifact-routing namespace. It is the only project
	// identity the envelope carries, so the folder check compares against it and
	// never against a Drive-side notion of ownership.
	Project string
	// Index is the binding position inside the item.
	Index     int
	SceneID   string
	SegmentID string
	// Kind is StockReferenceKindAsset, ...DriveFile, ...Folder, ...Ambiguous or
	// ...Invalid. Empty means the binding named no source at all.
	Kind string
	// ID is the identity Kind names: a media asset id, a Drive file id or a
	// Drive folder id. It is empty when Kind is ambiguous or empty.
	ID string
	// Declared lists the raw "kind:id" declarations the binding carried. It is
	// what lets a report name an ambiguous binding without inventing an ID.
	Declared []string
	// PoolSlot marks a placement the stock pool fills: the Producer names the
	// folder and the Master picks and certifies the file.
	PoolSlot bool
}

// String renders the reference the way the report shows it.
func (r StockReference) String() string {
	if r.Kind == "" {
		return ""
	}
	if r.ID == "" {
		return r.Kind
	}
	return r.Kind + ":" + r.ID
}

// CollectStockReferences walks every stock binding an envelope declares, in
// item order and binding order.
//
// Only output.stock_bindings is walked: the ingress validator expands the
// per-segment stock_folder_id shorthand into those bindings before the
// preflight sees the envelope, so walking both surfaces would double-count the
// same source and report a phantom conflict.
func CollectStockReferences(env *GenerationEnvelopeV2) []StockReference {
	if env == nil {
		return nil
	}
	var out []StockReference
	for i := range env.Items {
		item := &env.Items[i]
		itemID, project := strings.TrimSpace(item.ID), strings.TrimSpace(item.Project)
		for index, binding := range item.Output.StockBindings {
			out = append(out, stockReferenceFromBinding(itemID, project, index, binding))
		}
	}
	return out
}

// stockReferenceFromBinding normalizes one binding. A drive_link that cannot be
// read as a Drive FILE (a folder URL, a non-Drive host) becomes ...Invalid with
// the raw link as the ID: the verdict then reports the unusable link instead of
// inventing a file id.
func stockReferenceFromBinding(itemID, project string, index int, binding StockBindingInput) StockReference {
	ref := StockReference{
		ItemID:    itemID,
		Project:   project,
		Index:     index,
		SceneID:   strings.TrimSpace(binding.SceneID),
		SegmentID: strings.TrimSpace(binding.SegmentID),
		PoolSlot:  binding.PoolSlot,
	}
	if id := strings.TrimSpace(binding.AssetID); id != "" {
		ref.Declared = append(ref.Declared, StockReferenceKindAsset+":"+id)
	}
	if id := strings.TrimSpace(binding.FolderID); id != "" {
		ref.Declared = append(ref.Declared, StockReferenceKindFolder+":"+id)
	}
	if link := strings.TrimSpace(binding.DriveLink); link != "" {
		if id, err := urlutil.FileIDFromDriveLink(link); err == nil && id != "" {
			ref.Declared = append(ref.Declared, StockReferenceKindDriveFile+":"+id)
		} else {
			ref.Declared = append(ref.Declared, StockReferenceKindInvalid+":"+link)
		}
	}
	ref.settle()
	return ref
}

// settle picks the single identity a well-formed binding names. Two or more
// declarations are ambiguous by contract, so no winner is invented.
func (r *StockReference) settle() {
	switch len(r.Declared) {
	case 0:
	case 1:
		kind, id, _ := strings.Cut(r.Declared[0], ":")
		r.Kind, r.ID = kind, id
	default:
		r.Kind = StockReferenceKindAmbiguous
	}
}
