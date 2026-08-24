package semantic

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"

type ArtifactsMetadataAdapter struct{}

func NewArtifactsMetadataAdapter() artifacts.MetadataPort { return ArtifactsMetadataAdapter{} }
func (ArtifactsMetadataAdapter) MetadataMapFromJSON(raw string) map[string]any {
	return MetadataMapFromJSON(raw)
}
func (ArtifactsMetadataAdapter) MetadataMapToJSON(meta map[string]any) string {
	return MetadataMapToJSON(meta)
}
func (ArtifactsMetadataAdapter) MergeMetadataSearchText(parts ...string) string {
	return MergeMetadataSearchText(parts...)
}
func (ArtifactsMetadataAdapter) AssetTypeForMediaType(mediaType string) string {
	return AssetTypeForMediaType(mediaType)
}
func (ArtifactsMetadataAdapter) BuildAssetMetadata(input artifacts.MetadataInput, existing map[string]any) map[string]any {
	return BuildAssetMetadata(AssetSemanticInput{
		AssetID: input.AssetID, AssetType: input.AssetType, Source: input.Source, MediaType: input.MediaType,
		Generator: input.Generator, PromptOriginal: input.PromptOriginal, SemanticDescription: input.SemanticDescription,
		SearchText: input.SearchText, Subjects: input.Subjects, SubjectSlugs: input.SubjectSlugs, Tags: input.Tags,
		Categories: input.Categories, Mood: input.Mood, Style: input.Style, Confidence: input.Confidence,
		EmbeddingStatus: input.EmbeddingStatus, VisualEmbeddingJSON: input.VisualEmbeddingJSON, PHash: input.PHash,
		VisualDimensions: input.VisualDimensions, Assets: input.Assets, Extra: input.Extra,
	}, existing)
}

var _ artifacts.MetadataPort = ArtifactsMetadataAdapter{}
