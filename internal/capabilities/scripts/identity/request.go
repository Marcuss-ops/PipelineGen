package identity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Request contains the byte-affecting fields sent to the render queue.
// Transport locators are intentionally absent; asset content and logical
// materialization paths are the stable inputs.
type Request struct {
	ID              string
	RunID           string
	JobType         string
	ChunkIndex      int
	FrameRange      *FrameRange
	Plan            json.RawMessage
	Assets          []Asset
	PreparedPackage *PreparedPackage
}

type PreparedPackage struct {
	PackageID        string `json:"package_id"`
	PlanSHA256       string `json:"plan_sha256"`
	AssetManifestSHA string `json:"asset_manifest_sha256"`
	TotalFrames      int64  `json:"total_frames"`
	CompilerVersion  string `json:"compiler_version"`
	ProfileID        string `json:"profile_id,omitempty"`
}

type FrameRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type Asset struct {
	SHA256      string
	LogicalPath string
}

// RenderRequestIdentity is the immutable identity of a render request. It is
// deliberately separate from RenderQueueJob.ID: the operational id may be
// reused across retries, while this identity changes whenever the bytes the
// worker would produce can change.
type RenderRequestIdentity struct {
	SchemaVersion      string `json:"schema_version"`
	RunID              string `json:"run_id"`
	JobType            string `json:"job_type"`
	ChunkIndex         int    `json:"chunk_index"`
	FrameRangeSHA      string `json:"frame_range_sha256"`
	PlanSHA256         string `json:"plan_sha256"`
	AssetsSHA256       string `json:"assets_sha256"`
	PreparedPackageSHA string `json:"prepared_package_sha256,omitempty"`
	ContractID         string `json:"contract_id"`
	FamilySHA256       string `json:"family_sha256,omitempty"`
}

const renderRequestIdentitySchemaV2 = "pipelinegen.render-request.v2"

// RenderRequestIdentityFor derives the identity from a canonical plan JSON and
// the content-addressed asset set. Locators (SourceURL) are excluded on
// purpose: they describe where the bytes can be fetched, not which bytes the
// render consumes. Logical paths stay in the assets digest because they bind a
// hash to the workspace location the worker will materialize.
func For(request Request) (RenderRequestIdentity, error) {
	planSHA, contractID, err := canonicalRenderRequestPlan(request.Plan)
	if err != nil {
		return RenderRequestIdentity{}, err
	}
	assetsSHA, err := canonicalRenderRequestAssets(request.Assets)
	if err != nil {
		return RenderRequestIdentity{}, err
	}
	frameRangeSHA, err := canonicalRenderRequestFrameRange(request.FrameRange)
	if err != nil {
		return RenderRequestIdentity{}, err
	}
	preparedPackageSHA := ""
	if request.PreparedPackage != nil {
		encoded, marshalErr := json.Marshal(request.PreparedPackage)
		if marshalErr != nil {
			return RenderRequestIdentity{}, fmt.Errorf("render request identity: canonicalize prepared package: %w", marshalErr)
		}
		preparedPackageSHA = digest.SHA256Bytes(encoded)
	}
	return RenderRequestIdentity{
		SchemaVersion:      renderRequestIdentitySchemaV2,
		RunID:              strings.TrimSpace(request.RunID),
		JobType:            strings.TrimSpace(request.JobType),
		ChunkIndex:         request.ChunkIndex,
		FrameRangeSHA:      frameRangeSHA,
		PlanSHA256:         planSHA,
		AssetsSHA256:       assetsSHA,
		PreparedPackageSHA: preparedPackageSHA,
		ContractID:         contractID,
	}, nil
}

// RenderRequestFamilyIdentities binds each child identity to the complete,
// ordered batch. A per-job identity alone cannot prove that a retry uses the
// same family membership or chunk ordering.
func FamilyIdentities(jobs []Request) ([]RenderRequestIdentity, error) {
	if len(jobs) == 0 {
		return nil, fmt.Errorf("render request identity: chunk family is empty")
	}
	identities := make([]RenderRequestIdentity, len(jobs))
	entries := make([]struct {
		ID       string `json:"id"`
		Identity string `json:"identity"`
	}, len(jobs))
	for i, job := range jobs {
		identity, err := For(job)
		if err != nil {
			return nil, fmt.Errorf("render request identity: chunk %d: %w", i, err)
		}
		identities[i] = identity
		entries[i] = struct {
			ID       string `json:"id"`
			Identity string `json:"identity"`
		}{ID: strings.TrimSpace(job.ID), Identity: identity.Digest()}
	}
	canonical, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("render request identity: canonicalize chunk family: %w", err)
	}
	familySHA := digest.SHA256Bytes(canonical)
	for i := range identities {
		identities[i].FamilySHA256 = familySHA
	}
	return identities, nil
}

func canonicalRenderRequestFrameRange(frameRange *FrameRange) (string, error) {
	if frameRange == nil {
		return digest.SHA256Bytes([]byte("null")), nil
	}
	if frameRange.Start < 0 || frameRange.End <= frameRange.Start {
		return "", fmt.Errorf("render request identity: invalid frame range [%d,%d)", frameRange.Start, frameRange.End)
	}
	canonical, err := json.Marshal(frameRange)
	if err != nil {
		return "", fmt.Errorf("render request identity: canonicalize frame range: %w", err)
	}
	return digest.SHA256Bytes(canonical), nil
}

// canonicalRenderRequestPlan decodes and re-encodes the plan so map key order
// and insignificant whitespace cannot change the identity. encoding/json
// sorts map keys, which makes the result deterministic for the same document.
func canonicalRenderRequestPlan(raw json.RawMessage) (string, string, error) {
	if len(raw) == 0 {
		return digest.SHA256Bytes(nil), "", nil
	}
	if !json.Valid(raw) {
		return "", "", fmt.Errorf("render request identity: plan is not valid JSON")
	}
	var decoded any
	// UseNumber preserves the exact JSON numeric literal. Decoding into
	// float64 would collapse integers above 2^53 (frame/time values can reach
	// that range) and make two different plans hash identically.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return "", "", fmt.Errorf("render request identity: plan is not valid JSON: %w", err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return "", "", fmt.Errorf("render request identity: canonicalize plan: %w", err)
	}
	return digest.SHA256Bytes(canonical), renderRequestContractID(decoded), nil
}

func renderRequestContractID(plan any) string {
	object, ok := plan.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"contract_id", "media_contract", "output_profile_id"} {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type renderRequestIdentityAsset struct {
	SHA256      string `json:"sha256"`
	LogicalPath string `json:"logical_path"`
}

func canonicalRenderRequestAssets(assets []Asset) (string, error) {
	entries := make([]renderRequestIdentityAsset, 0, len(assets))
	for _, asset := range assets {
		hash := strings.ToLower(strings.TrimSpace(asset.SHA256))
		if hash == "" {
			return "", fmt.Errorf("render request identity: asset %q has no content hash", strings.TrimSpace(asset.LogicalPath))
		}
		entries = append(entries, renderRequestIdentityAsset{
			SHA256:      hash,
			LogicalPath: strings.TrimSpace(asset.LogicalPath),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].SHA256 != entries[j].SHA256 {
			return entries[i].SHA256 < entries[j].SHA256
		}
		return entries[i].LogicalPath < entries[j].LogicalPath
	})
	canonical, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("render request identity: canonicalize assets: %w", err)
	}
	return digest.SHA256Bytes(canonical), nil
}

// Digest is the content identity. It contains only strings, so the marshal
// below cannot fail; an empty digest is returned rather than a fabricated
// stable value if that invariant is ever broken.
func (i RenderRequestIdentity) Digest() string {
	encoded, err := json.Marshal(i)
	if err != nil {
		return ""
	}
	return digest.SHA256Bytes(encoded)
}

// IdempotencyKey binds the content identity to the operational id. Including
// the id keeps two intentionally distinct jobs (fresh renders) with identical
// bytes from aliasing each other, while the same id with changed content still
// produces a different key.
func (i RenderRequestIdentity) IdempotencyKey(jobID string) string {
	return digest.SHA256String(strings.TrimSpace(jobID) + "\x00" + i.Digest())
}

// LegacyV1IdempotencyKey identifies a persisted v1 job during rolling-upgrade
// collision checks. It is not used for new submissions.
func (i RenderRequestIdentity) LegacyV1IdempotencyKey(jobID string) string {
	legacy := struct {
		SchemaVersion string `json:"schema_version"`
		RunID         string `json:"run_id"`
		PlanSHA256    string `json:"plan_sha256"`
		AssetsSHA256  string `json:"assets_sha256"`
		ContractID    string `json:"contract_id"`
	}{
		SchemaVersion: "pipelinegen.render-request.v1",
		RunID:         i.RunID,
		PlanSHA256:    i.PlanSHA256,
		AssetsSHA256:  i.AssetsSHA256,
		ContractID:    i.ContractID,
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		return ""
	}
	return digest.SHA256String(strings.TrimSpace(jobID) + "\x00" + digest.SHA256Bytes(encoded))
}
