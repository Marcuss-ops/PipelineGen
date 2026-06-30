package images

// extractSubjectAndTags is a temporary stub returning ("", nil). The
// inference logic — which previously parsed `description` into a slug-ish
// subject plus a comma/semicolon separated tag list — is being reworked in
// a follow-up PR that will land the proper subject/tag extractor behind a
// dedicated service. Today this no-op keeps the direct ingestion callsite
// compiling while we rewire the pipeline. This stub MUST be replaced
// before the asset pipeline is considered feature-complete; behaviour of
// tags-only / unknown-slug paths will degrade silently otherwise.
func extractSubjectAndTags(description string) (string, []string) {
	// The real parser derives subject (slug + alias-tolerant) and tags
	// (tokenized against textutil.TermsFromText). Today the build needs a
	// no-op so direct ingestion callsites compile; a follow-up PR will
	// reintroduce the SubjectTagsService and route this call through it.
	_ = description
	return "", nil
}
