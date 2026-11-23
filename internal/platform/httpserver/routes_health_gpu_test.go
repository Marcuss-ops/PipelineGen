package httpserver

import "testing"

// TestGPUAdmissionSnapshotDefaultsToShippedLanes pins the drift probe: with no
// lane declaration the worker is still assumed to run the shipped 2 lanes, so
// raising ONLY RENDERINGGEN_GPU_SLOTS to 3 must report parity_ok=false. Before
// this test the snapshot defaulted lanes to slots, which made every
// slots-only drift look healthy.
func TestGPUAdmissionSnapshotDefaultsToShippedLanes(t *testing.T) {
	t.Setenv("RENDERINGGEN_GPU_SLOTS", "")
	t.Setenv("RENDERINGGEN_GPU_LANES", "")

	got := gpuAdmissionSnapshot()
	if got.Lanes != 2 || got.Slots != 2 || !got.ParityOK {
		t.Fatalf("unset snapshot = %+v, want 2/2 parity", got)
	}

	t.Setenv("RENDERINGGEN_GPU_SLOTS", "3")
	got = gpuAdmissionSnapshot()
	if got.Lanes != 2 || got.Slots != 3 || got.ParityOK {
		t.Fatalf("slots-only drift = %+v, want lanes=2 slots=3 parity=false", got)
	}

	t.Setenv("RENDERINGGEN_GPU_LANES", "3")
	got = gpuAdmissionSnapshot()
	if got.Lanes != 3 || got.Slots != 3 || !got.ParityOK {
		t.Fatalf("declared 3/3 = %+v, want parity", got)
	}
}

func TestGPUAdmissionSnapshotInvalidValueFailsParity(t *testing.T) {
	t.Setenv("RENDERINGGEN_GPU_SLOTS", "three")
	t.Setenv("RENDERINGGEN_GPU_LANES", "2")

	got := gpuAdmissionSnapshot()
	if got.ParityOK || got.Slots != 0 {
		t.Fatalf("invalid slots = %+v, want slots=0 parity=false", got)
	}
}
