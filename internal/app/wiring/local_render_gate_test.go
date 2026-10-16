package wiring

import (
	"os"
	"testing"
)

// P3-5: default abilitato (sicuro pre-cutover), gate esplicito per disattivare.
func TestLocalOverlayRenderGate(t *testing.T) {
	os.Unsetenv("PIPELINEGEN_LOCAL_RENDER")
	if !LocalOverlayRenderEnabled() {
		t.Fatalf("default must keep local render enabled until consumer migration is verified")
	}
	os.Setenv("PIPELINEGEN_LOCAL_RENDER", "0")
	defer os.Unsetenv("PIPELINEGEN_LOCAL_RENDER")
	if LocalOverlayRenderEnabled() {
		t.Fatalf("PIPELINEGEN_LOCAL_RENDER=0 must disable local render")
	}
}
