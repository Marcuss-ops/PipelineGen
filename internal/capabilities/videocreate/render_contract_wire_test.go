package videocreate

import (
	"encoding/json"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// TestRenderContractWireCarriesTheCanonicalContract pins the one thing the
// flat contract block must never do: describe a stream other than the canonical
// contract the renderer was held to. The block is emitted in exactly one place
// (cliprender.ContractWireBlock) from the kernel contract, and this workflow
// projects it back into the facts the assembler gate compares.
func TestRenderContractWireCarriesTheCanonicalContract(t *testing.T) {
	contract := cliprender.NewResolvedContract(nil)
	raw, err := json.Marshal(map[string]any{
		"contract_id": contract.ID,
		"contract":    cliprender.ContractWireBlock(contract),
		"render":      map[string]any{"output_path": "/scratch/rendered.mp4", "duration_sec": 12.5},
		"asset":       map[string]any{"asset_id": "asset-rendered"},
	})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	result, err := decodeRenderChildResult(raw)
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Contract.Width != contract.Width || result.Contract.Height != contract.Height {
		t.Fatalf("geometry = %dx%d, want %dx%d", result.Contract.Width, result.Contract.Height, contract.Width, contract.Height)
	}
	if result.Contract.FPSNum != contract.FPS.Num || result.Contract.FPSDen != contract.FPS.Den {
		t.Fatalf("fps = %d/%d, want %d/%d", result.Contract.FPSNum, result.Contract.FPSDen, contract.FPS.Num, contract.FPS.Den)
	}
	if result.Contract.VideoCodec != contract.VideoCodec || result.Contract.PixelFormat != contract.PixelFormat {
		t.Fatalf("codec/pixel format = %s/%s, want %s/%s",
			result.Contract.VideoCodec, result.Contract.PixelFormat, contract.VideoCodec, contract.PixelFormat)
	}
	// The projected facts must still be the assembly identity the copy-only
	// assembler accepts: the wire projection may not weaken it.
	facts := kernelmedia.DefaultAssemblyMediaContract()
	facts.Width, facts.Height = result.Contract.Width, result.Contract.Height
	facts.FPS = kernelmedia.FrameRate{Num: result.Contract.FPSNum, Den: result.Contract.FPSDen}
	facts.VideoCodec = result.Contract.VideoCodec
	facts.AudioCodec = result.Contract.AudioCodec
	facts.PixelFormat = result.Contract.PixelFormat
	if err := facts.ValidateExact(); err != nil {
		t.Fatalf("wire contract is not the assembly contract: %v", err)
	}
}
