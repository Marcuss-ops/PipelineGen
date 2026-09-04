package ingest

import (
	"context"
	"errors"
)

var ErrEmptyDescription = errors.New("subject/tags: empty description")
var ErrNoSubjectDerivable = errors.New("subject/tags: no subject derivable from description")

type SubjectTagsService interface {
	ExtractSubjectAndTags(ctx context.Context, description string) (subject string, tags []string, err error)
}
