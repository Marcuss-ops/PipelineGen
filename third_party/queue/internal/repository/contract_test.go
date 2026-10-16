package repository

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
)

// TestErrJobExistsIsOneSentinel pins the single-value rule for the duplicate-job
// condition. The repository and the public client contract used to declare two
// independent errors.New("job already exists"): the HTTP server maps only the
// repository copy to 409, while producers match only the client copy, so a
// future path returning the other value would silently stop being recognized —
// reporting a replay as a failure, or a storage outage as an idempotent
// duplicate. The public wire contract owns the value; the repository aliases it.
func TestErrJobExistsIsOneSentinel(t *testing.T) {
	if ErrJobExists != client.ErrJobExists {
		t.Fatal("repository.ErrJobExists must alias client.ErrJobExists, not declare a second sentinel for the same fact")
	}
	wrapped := fmt.Errorf("%w: job j1", ErrJobExists)
	if !errors.Is(wrapped, client.ErrJobExists) {
		t.Fatal("a wrapped repository duplicate error must match the client sentinel through errors.Is")
	}
	if client.ErrJobExists.Error() != ErrJobExists.Error() {
		t.Fatalf("sentinel messages diverged: %q vs %q", client.ErrJobExists.Error(), ErrJobExists.Error())
	}
}
