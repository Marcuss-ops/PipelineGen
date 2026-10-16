package model_test

import (
	"reflect"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestArtifactIsWireType pins that model.Artifact is an ALIAS of the public
// wire contract, not a second declaration that merely agrees field-for-field
// today. The compiler now enforces the identity; this test makes the intent
// explicit so a future contributor cannot quietly reintroduce a mirror struct.
func TestArtifactIsWireType(t *testing.T) {
	modelType := reflect.TypeOf(model.Artifact{})
	wireType := reflect.TypeOf(client.Artifact{})
	if modelType != wireType {
		t.Fatalf("model.Artifact (%v) must alias client.Artifact (%v)", modelType, wireType)
	}
}
