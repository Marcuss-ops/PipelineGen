package script

import "testing"

func TestChannelProfilesPathUsesOverride(t *testing.T) {
	t.Setenv("PIPELINEGEN_CHANNEL_PROFILES", " /tmp/profiles.yaml ")
	if got := ChannelProfilesPath(); got != "/tmp/profiles.yaml" {
		t.Fatalf("ChannelProfilesPath() = %q", got)
	}
}

func TestChannelProfilesPathUsesDefault(t *testing.T) {
	t.Setenv("PIPELINEGEN_CHANNEL_PROFILES", "  ")
	if got := ChannelProfilesPath(); got != "config/channel_profiles.yaml" {
		t.Fatalf("ChannelProfilesPath() = %q", got)
	}
}
