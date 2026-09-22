// Package app — wire_script_channel_profiles.go owns the one knob that
// locates the channel-profile document.
//
// Why a file + env var instead of a config.yaml section: the profile set is
// edited far more often than the deployment is reconfigured (an operator
// tunes a channel's look, not its DSN), and the document carries its own
// version field so it can evolve independently of config.yaml's section
// ratchet. PIPELINEGEN_CHANNEL_PROFILES is the override a container or a
// second checkout uses when the default relative path does not hold.
//
// The default path is relative to the process working directory, the same
// convention config/generation_styles.yaml already uses
// (build_bundles_drive.go), because the server runs from the checkout root
// where config.yaml lives.
package wiring

import (
	"os"
	"strings"
)

// channelProfilesPath returns the channel-profile document path: the
// PIPELINEGEN_CHANNEL_PROFILES override when set, else the checked-in default.
func channelProfilesPath() string {
	if p := strings.TrimSpace(os.Getenv("PIPELINEGEN_CHANNEL_PROFILES")); p != "" {
		return p
	}
	return "config/channel_profiles.yaml"
}
