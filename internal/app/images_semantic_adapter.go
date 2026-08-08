package app

import (
	"context"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
)

type imagesSemanticAdapter struct {
	inner semantic.MetadataWriterPort
}

func newImagesSemanticAdapter(inner semantic.MetadataWriterPort) imgservice.SemanticPort {
	if inner == nil {
		return nil
	}
	return &imagesSemanticAdapter{inner: inner}
}

func (a *imagesSemanticAdapter) GeneratePayload(ctx context.Context, req imgservice.SemanticWriteRequest) (*imgservice.SemanticPayload, string, error) {
	payload, marker, err := a.inner.GeneratePayload(ctx, semantic.WriteRequest{
		AssetID: req.AssetID, AssetType: req.AssetType, MediaType: req.MediaType,
		Source: req.Source, SourceType: req.SourceType, Generator: req.Generator,
		Retriever: req.Retriever, PageURL: req.PageURL, ImageURL: req.ImageURL,
		License: req.License, Author: req.Author, Style: req.Style, Prompt: req.Prompt,
		LocalPath: req.LocalPath, TempDir: req.TempDir, Extensions: req.Extensions,
		GroupID: req.GroupID, Assets: req.Assets,
	})
	if err != nil || payload == nil {
		return nil, marker, err
	}
	return imagesSemanticPayload(payload), marker, nil
}

func (a *imagesSemanticAdapter) Write(ctx context.Context, req imgservice.SemanticWriteRequest) (*imgservice.SemanticWriteResult, error) {
	result, err := a.inner.Write(ctx, semantic.WriteRequest{
		AssetID: req.AssetID, AssetType: req.AssetType, MediaType: req.MediaType,
		Source: req.Source, SourceType: req.SourceType, Generator: req.Generator,
		Retriever: req.Retriever, PageURL: req.PageURL, ImageURL: req.ImageURL,
		License: req.License, Author: req.Author, Style: req.Style, Prompt: req.Prompt,
		LocalPath: req.LocalPath, TempDir: req.TempDir, Extensions: req.Extensions,
		GroupID: req.GroupID, Assets: req.Assets,
	})
	if err != nil || result == nil {
		return nil, err
	}
	return &imgservice.SemanticWriteResult{LocalPath: result.LocalPath, Payload: imagesSemanticPayload(result.Payload)}, nil
}

func imagesSemanticPayload(payload *semantic.Payload) *imgservice.SemanticPayload {
	if payload == nil {
		return nil
	}
	return &imgservice.SemanticPayload{
		AssetID: payload.AssetID, PromptOriginal: payload.PromptOriginal, Style: payload.Style,
		Tags: payload.Tags, Subjects: payload.Subjects, SearchText: payload.SearchText,
		AssetType: payload.AssetType, SemanticDescription: payload.SemanticDescription,
		ConceptTags: payload.ConceptTags, Mood: payload.Mood, Categories: payload.Categories,
		VisualObjects: payload.VisualObjects, EmotionalTone: payload.EmotionalTone,
		RetrievalScore: payload.RetrievalScore,
	}
}

var _ imgservice.SemanticPort = (*imagesSemanticAdapter)(nil)
