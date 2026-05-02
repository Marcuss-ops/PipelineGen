package google_docs

import (
	"context"
	"fmt"

  "velox/go-master/internal/core/scriptdoc"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/option"
)

type Publisher struct {
	docsService *docs.Service
}

func NewPublisher(credentialsFile string) (*Publisher, error) {
	ctx := context.Background()

	creds, err := google.CredentialsFromJSON(ctx, []byte(credentialsFile), docs.DriveScope, docs.DocumentsScope)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}

	service, err := docs.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("failed to create docs service: %w", err)
	}

	return &Publisher{docsService: service}, nil
}

func (p *Publisher) Publish(ctx context.Context, doc scriptdoc.Document) (*scriptdoc.PublishedDocument, error) {
	docInfo := &docs.Document{
		Title: doc.Title,
	}

	created, err := p.docsService.Documents.Create(docInfo).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to create document: %w", err)
	}

	requests := []*docs.Request{
		{
			InsertText: &docs.InsertTextRequest{
				Location: &docs.Location{Index: 1},
				Text:     doc.Content,
			},
		},
	}

	_, err = p.docsService.Documents.BatchUpdate(created.DocumentId, &docs.BatchUpdateDocumentRequest{
		Requests: requests,
	}).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to update document: %w", err)
	}

	return &scriptdoc.PublishedDocument{
		URL: fmt.Sprintf("https://docs.google.com/document/d/%s/edit", created.DocumentId),
	}, nil
}
