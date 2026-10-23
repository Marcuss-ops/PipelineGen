package wiring

import (
	"os"
	"testing"
)

// P3-5: default abilitato (sicuro pre-cutover), gate esplicito per disattivare.
func TestLocalOverlayRenderGate(t *testing.T) {
	if err := os.Unsetenv("PIPELINEGEN_LOCAL_RENDER"); err != nil {
		t.Fatalf("unset PIPELINEGEN_LOCAL_RENDER: %v", err)
	}
	if !LocalOverlayRenderEnabled() {
		t.Fatalf("default must keep local render enabled until consumer migration is verified")
	}
	if err := os.Setenv("PIPELINEGEN_LOCAL_RENDER", "0"); err != nil {
		t.Fatalf("set PIPELINEGEN_LOCAL_RENDER: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("PIPELINEGEN_LOCAL_RENDER"); err != nil {
			t.Errorf("unset PIPELINEGEN_LOCAL_RENDER: %v", err)
		}
	}()
	if LocalOverlayRenderEnabled() {
		t.Fatalf("PIPELINEGEN_LOCAL_RENDER=0 must disable local render")
	}
}
