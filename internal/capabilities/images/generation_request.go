// Package images — generation_request.go: pure request normalization
// helpers for AI image generation (PR-GODOBJ-3-IMAGES-GENERATION, July 2026).
//
// godlike/06 SSOT: one canonical owner per fact — request normalization
// for image  All helpers in this file are side-effect free so
// they can be unit-tested without a registry / imageGen / storage wire.
//
// PR-GODOBJ-3 KILL LIST (c) applied: account/project parameters removed
// from the legacy GenerateSmartImageWithAccount surface. This file's
// canonical surface does NOT carry those fields; tenant identity belongs
// in a separate auth/tenancy port, not in image-generation request types.
package images

import (
	"fmt"
	"strings"
)

// pickImagePrompt extracts the most specific prompt from a list.
// Priority:
//  1. prompts[0] (first non-empty)
//  2. subject + topic combinations (cinematic-landscape suffix)
//  3. topic, then subject, then empty
//
// Moved from generation_service.go per PR-GODOBJ-3.
func pickImagePrompt(subject, topic string, prompts []string) string {
	for _, p := range prompts {
		if p = strings.TrimSpace(p); p != "" {
			return p
		}
	}
	subject = strings.TrimSpace(subject)
	topic = strings.TrimSpace(topic)
	switch {
	case subject != "" && topic != "":
		return fmt.Sprintf("%s, %s, cinematic landscape", subject, topic)
	case subject != "":
		return fmt.Sprintf("%s, cinematic landscape", subject)
	case topic != "":
		return fmt.Sprintf("%s, cinematic landscape", topic)
	default:
		return ""
	}
}

// NOTE: GenerateImageRequest is canonically defined in ports.go:28
// (with JSON tags + AssetProvider enum). This file intentionally does
// NOT redeclare it (PR-GODOBJ-3 closure of the previous duplicate
// identified at code-reviewer check-in). godlike/06 SSOT: the canonical
// shape lives in ports.go only.
