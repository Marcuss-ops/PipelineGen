package images

import (
	"context"
	"errors"
)

// ErrEmptyDescription is returned when the description is empty or whitespace-only.
var ErrEmptyDescription = errors.New("subject/tags: empty description")

// ErrNoSubjectDerivable is returned when no capitalized word can be derived.
var ErrNoSubjectDerivable = errors.New("subject/tags: no subject derivable from description")

// SubjectTagsService extracts a subject slug and tag list from free-form text.
type SubjectTagsService interface {
	ExtractSubjectAndTags(ctx context.Context, description string) (subject string, tags []string, err error)
}
