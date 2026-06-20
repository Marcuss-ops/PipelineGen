package scripts

// buildBatchResponse constructs the final typed response for ExecuteBatchGeneration.
func buildBatchResponse(
	req *GenerateBatchRequest,
	docTitle, cleanScript, docURL string,
	translations map[string]map[string]any,
	guidelinesBlock string,
	targetWordsPerChapter, splitItemCount int,
	batchItems []BatchTopic,
	timings []chapterTiming,
	failedChapters []string,
	failedChapterCount int,
	failedLanguages []string,
) BatchGenerateResponse {
	resp := BatchGenerateResponse{
		OK:                    true,
		Title:                 docTitle,
		Script:                cleanScript,
		DocURL:                docURL,
		Translations:          translations,
		Guidelines:            guidelinesBlock,
		ChapterStructure:      req.ChapterStructure,
		TargetWordsPerItem:    targetWordsPerChapter,
		TargetWordsPerChapter: targetWordsPerChapter,
		SourcePreprocessing: &SourcePreprocessing{
			OriginalItems: len(batchItems),
			ExpandedItems: len(timings),
			SplitItems:    splitItemCount,
		},
		PromptVersion:       req.PromptVersion,
		EditorPromptVersion: req.EditorPromptVersion,
		QAPromptVersion:     req.QAPromptVersion,
		Timings:             timings,
		FailedChapters:      failedChapters,
		FailedChapterCount:  failedChapterCount,
		FailedLanguages:     failedLanguages,
		FailedLanguageCount: len(failedLanguages),
	}

	if req.Voiceover {
		resp.VoiceoverLink = ""
		resp.VoiceoverStatus = "processing"
		resp.VoiceoverNote = "Voiceover files are being generated in the background. Check Google Drive voiceover folder for completed files."
	}

	return resp
}
