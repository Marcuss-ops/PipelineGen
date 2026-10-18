// Package overlays — selection_seed.go: single owner for deterministic seeding.
//
// P1-2: un unico helper di seeding/ordinamento/rotazione/no-repeat.
// Tutti i sampler (planner, animation_policy legacy, AnimationSelector)
// usano queste primitive: stesso hash SHA256, stesso walk no-repeat.
// I piani sigillati storici restano byte-identici perché l'algoritmo è
// estratto verbatim, non reinventato.
package overlays

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// SeededGroupIndex è l'unico hash seed→indice per i draw seeded di gruppo.
// Verbatim dell'ex seededGroupMotion: SHA256(seed\x00target\x00groupID),
// primi 16 hex come uint64, modulo len(pool). n<=0 → 0.
func SeededGroupIndex(seed, target, groupID string, n int) int {
	if n <= 1 {
		return 0
	}
	digestValue := digest.SHA256String(seed + "\x00" + target + "\x00" + groupID)
	var index uint64
	for _, digit := range []byte(digestValue[:16]) {
		index = index*16 + uint64(hexNibbleValue(digit))
	}
	return int(index % uint64(n))
}

// SelectImageGroupMotion picks the single choreography of a composite image
// group from the certified image.group.count_N candidates. The draw is seeded by
// (motionSeed, videoID, groupID), so retries and replays get the same motion.
// When more than one candidate exists, the previous group's motion is excluded
// so consecutive groups do not repeat. It returns "" when no candidate exists.
func SelectImageGroupMotion(motionSeed, videoID, groupID string, count int, previous string) string {
	candidates := AnimationCandidates(fmt.Sprintf("image.group.count_%d", count))
	pool := make([]string, 0, len(candidates))
	for _, id := range candidates {
		if id != previous {
			pool = append(pool, id)
		}
	}
	if len(pool) == 0 {
		pool = candidates
	}
	if len(pool) == 0 {
		return ""
	}
	return pool[SeededGroupIndex(motionSeed, videoID, groupID, len(pool))]
}

func hexNibbleValue(digit byte) byte {
	if digit >= '0' && digit <= '9' {
		return digit - '0'
	}
	return digit - 'a' + 10
}
