package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

type documentPublisherStub struct {
	ref     ports.DocumentReference
	err     error
	updates int
}

func (p *documentPublisherStub) CreateDocument(context.Context, string, string, string, string, bool) (ports.DocumentReference, error) {
	return p.ref, p.err
}
func (p *documentPublisherStub) UpdateDocument(context.Context, string, string, string) error {
	p.updates++
	return p.err
}

func TestDocumentsService_CreateDocMissingPublisherFailsClosed(t *testing.T) {
	_, _, err := NewDocumentsService(nil, nil, "").CreateDoc(context.Background(), "title", "body", nil, "", "", false)
	if !errors.Is(err, ErrDocumentPublisherUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}
}

func TestDocumentsService_CreateDocProviderErrorFailsClosed(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	_, _, err := NewDocumentsService(&documentPublisherStub{err: providerErr}, nil, "").CreateDoc(context.Background(), "title", "body", nil, "", "", false)
	if err == nil || !strings.Contains(err.Error(), providerErr.Error()) {
		t.Fatalf("error = %v, want wrapped provider error", err)
	}
}

func TestDocumentsService_CreateDocPreservesValidPartialReference(t *testing.T) {
	link, id, err := NewDocumentsService(&documentPublisherStub{ref: ports.DocumentReference{ID: "doc-1", URL: "https://docs.example/doc-1"}, err: ports.ErrDocumentReferencePreserved}, nil, "").CreateDoc(context.Background(), "title", "body", nil, "", "key", false)
	if err != nil || link == "" || id == "" {
		t.Fatalf("partial reference = (%q, %q, %v), want preserved success", link, id, err)
	}
}

func TestDocumentsService_CreateDocIncompleteReferenceFailsClosed(t *testing.T) {
	_, _, err := NewDocumentsService(&documentPublisherStub{ref: ports.DocumentReference{ID: "doc-1"}}, nil, "").CreateDoc(context.Background(), "title", "body", nil, "", "", false)
	if !errors.Is(err, ErrDocumentCreationFailed) {
		t.Fatalf("error = %v, want incomplete reference", err)
	}
}

func TestDocumentsService_CreateDocResolverErrorFailsClosed(t *testing.T) {
	resolverErr := errors.New("folder resolver unavailable")
	_, _, err := NewDocumentsService(&documentPublisherStub{ref: ports.DocumentReference{ID: "doc-1", URL: "https://docs.example/doc-1"}}, nil, "default").CreateDoc(context.Background(), "title", "body", func(context.Context, string, string) (string, error) { return "", resolverErr }, "requested", "", false)
	if err == nil || !strings.Contains(err.Error(), resolverErr.Error()) {
		t.Fatalf("error = %v, want resolver error", err)
	}
}

func TestDocumentsUseCase_NilReceiverFailsClosed(t *testing.T) {
	var uc *DocumentsUseCase
	_, _, err := uc.BuildAndCreate(context.Background(), "title", "body", nil, "")
	if !errors.Is(err, ErrDocumentPublisherUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}
}

func TestDocumentsService_UpdateDocMissingPublisherFailsClosed(t *testing.T) {
	if err := NewDocumentsService(nil, nil, "").UpdateDoc(context.Background(), "doc-1", "title", "body"); !errors.Is(err, ErrDocumentPublisherUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}
}

func TestDocumentsService_UpdateDocUsesPublisherPort(t *testing.T) {
	stub := &documentPublisherStub{}
	if err := NewDocumentsService(stub, nil, "").UpdateDoc(context.Background(), "doc-1", "title", "body"); err != nil {
		t.Fatal(err)
	}
	if stub.updates != 1 {
		t.Fatalf("updates = %d, want 1", stub.updates)
	}
}
