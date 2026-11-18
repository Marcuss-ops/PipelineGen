// Package overlays — long_phrase_lane.go: long-lane phrase motion families.
//
// Split out of preset_motion_selection.go to respect max_lines_per_file_strict=600
// (same package, no behavior change): the long-lane bucket rotation keeps its
// own block, so adding a family stays a registry change + this one block.
package overlays

// longPhrase visual family buckets, in pinned rotation order.
const (
	longFamilyDocumentary = iota
	longFamily3D
	longFamilyModernApple
	longFamilyClassicApple
	longFamilyCount
)
