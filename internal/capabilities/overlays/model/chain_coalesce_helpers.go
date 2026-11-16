package model

import "strings"

// IsChainItemKind reports whether an overlay kind is a fused multi-animation
// chain rather than a standalone item.
func IsChainItemKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case KindPhraseChain, KindImageChain, KindMixedChain, KindMapChain:
		return true
	}
	return false
}
