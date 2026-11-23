package script

import (
	"os"
	"strings"
)

// ChannelProfilesPath resolves the optional channel-profile document path.
// The profile document evolves independently of config.yaml's section schema.
func ChannelProfilesPath() string {
	if path := strings.TrimSpace(os.Getenv("PIPELINEGEN_CHANNEL_PROFILES")); path != "" {
		return path
	}
	return "config/channel_profiles.yaml"
}
