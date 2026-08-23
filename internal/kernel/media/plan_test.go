package media

import (
	"encoding/json"
	"testing"
)

func TestMediaPlanLegacyProvidersNormalizeToPolicy(t *testing.T) {
	var plan MediaPlanSpec
	if err := json.Unmarshal([]byte(`{"providers":{"artlist":true,"internet_images":"enabled"}}`), &plan); err != nil {
		t.Fatalf("unmarshal legacy providers: %v", err)
	}
	if !plan.ProviderPolicy.Artlist.AsBool() || !plan.ProviderPolicy.InternetImages.AsBool() {
		t.Fatalf("legacy providers did not normalize: %#v", plan.ProviderPolicy)
	}
}

func TestMediaToggleMarshalRejectsInvalidValue(t *testing.T) {
	if _, err := json.Marshal(MediaToggle("corrupt")); err == nil {
		t.Fatal("invalid media toggle serialized successfully")
	}
}
