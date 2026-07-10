package scene

import (
	"fmt"
	"testing"
)

func TestScratchFromProse(t *testing.T) {
	text := "Il ruggito della folla è una forza viscerale, un'entità vivente che vibra attraverso le assi del pavimento dell'arena. È una sinfonia di anticipazione, un sussulto collettivo mentre due titani sono pronti a combattere. L'aria vibra di elettricità."
	s := NewSceneSynthesizer()
	res := s.FromProse(text, 8)
	fmt.Printf("LEN OF RES: %d\n", len(res))
	for i, sc := range res {
		fmt.Printf("Scene %d: Kind=%s Text=%q\n", i, sc.Kind, sc.Text)
	}
}
