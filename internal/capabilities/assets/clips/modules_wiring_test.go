package clips_test

import (
	"fmt"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/bulk"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/processing"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/publication"
	"github.com/gin-gonic/gin"
)

type descriptor interface {
	Name() string
	RegisterRoutes(*gin.RouterGroup)
}

type routeStub struct{ routes int }

func (s routeStub) RegisterRoutes(g *gin.RouterGroup, _ gin.HandlerFunc) {
	for i := 0; i < s.routes; i++ {
		g.POST(fmt.Sprintf("/stub-%d", i), func(*gin.Context) {})
	}
}

type handlerStub struct{}

func (handlerStub) ReprocessClip(*gin.Context)      {}
func (handlerStub) EnrichMedia(*gin.Context)        {}
func (handlerStub) DownloadClip(*gin.Context)       {}
func (handlerStub) ReuploadClip(*gin.Context)       {}
func (handlerStub) VerifyClip(*gin.Context)         {}
func (handlerStub) HandleFixHash(*gin.Context)      {}
func (handlerStub) TrashClip(*gin.Context)          {}
func (handlerStub) Reconcile(*gin.Context)          {}
func (handlerStub) Cleanup(*gin.Context)            {}
func (handlerStub) RegenerateManifest(*gin.Context) {}
func (handlerStub) TrashFolder(*gin.Context)        {}

func TestClipsSubmodulesShareRegistrationShape(t *testing.T) {
	enabled := func() bool { return true }
	cases := []struct {
		name       string
		build      func() (descriptor, error)
		wantRoutes int
	}{
		{
			name: "clips-bulk",
			build: func() (descriptor, error) {
				return bulk.Build(bulk.Dependencies{
					Transport: routeStub{routes: 1}, EnabledFunc: enabled,
				})
			},
			wantRoutes: 1,
		},
		{
			name: "clips-ingest",
			build: func() (descriptor, error) {
				return ingest.Build(ingest.Dependencies{
					Ingest: routeStub{routes: 1}, EnabledFunc: enabled,
				})
			},
			wantRoutes: 1,
		},
		{
			name: "clips-processing",
			build: func() (descriptor, error) {
				return processing.Build(processing.Dependencies{
					Processing: handlerStub{}, EnabledFunc: enabled,
				})
			},
			wantRoutes: 2,
		},
		{
			name: "clips-publication",
			build: func() (descriptor, error) {
				return publication.Build(publication.Dependencies{
					Publication: handlerStub{}, EnabledFunc: enabled,
				})
			},
			wantRoutes: 2,
		},
		{
			name: "clips-operations",
			build: func() (descriptor, error) {
				return operations.Build(operations.Dependencies{
					Ops: handlerStub{}, EnabledFunc: enabled,
				})
			},
			wantRoutes: 7,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			d, err := tc.build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got := d.Name(); got != tc.name {
				t.Fatalf("Name() = %q, want %q", got, tc.name)
			}
			r := gin.New()
			d.RegisterRoutes(r.Group("/api"))
			if got := len(r.Routes()); got != tc.wantRoutes {
				t.Fatalf("registered %d routes, want %d", got, tc.wantRoutes)
			}
		})
	}
}

func TestClipsSubmodulesRejectMissingEnabledFunc(t *testing.T) {
	enabledDeps := []struct {
		name  string
		build func() error
	}{
		{
			name: "clips-bulk",
			build: func() error {
				_, err := bulk.Build(bulk.Dependencies{Transport: routeStub{routes: 1}})
				return err
			},
		},
		{
			name: "clips-ingest",
			build: func() error {
				_, err := ingest.Build(ingest.Dependencies{Ingest: routeStub{routes: 1}})
				return err
			},
		},
		{
			name: "clips-processing",
			build: func() error {
				_, err := processing.Build(processing.Dependencies{Processing: handlerStub{}})
				return err
			},
		},
		{
			name: "clips-publication",
			build: func() error {
				_, err := publication.Build(publication.Dependencies{Publication: handlerStub{}})
				return err
			},
		},
		{
			name: "clips-operations",
			build: func() error {
				_, err := operations.Build(operations.Dependencies{Ops: handlerStub{}})
				return err
			},
		},
	}

	for _, tc := range enabledDeps {
		if err := tc.build(); err == nil {
			t.Fatalf("%s.Build accepted a nil EnabledFunc", tc.name)
		}
	}
}
