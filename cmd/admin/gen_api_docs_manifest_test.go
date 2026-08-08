package main

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

func TestPublishRuntimeRouteArtifactsPublishesMarkdownAndManifestTogether(t *testing.T) {
	dir := t.TempDir()
	docsPath := dir + "/docs.md"
	manifestPath := dir + "/routes.yaml"
	routes := []gin.RouteInfo{{Method: "GET", Path: "/health"}}
	markdown := []byte("# generated\n")

	if err := publishRuntimeRouteArtifacts(docsPath, manifestPath, markdown, routes); err != nil {
		t.Fatalf("publish artifacts: %v", err)
	}
	gotMarkdown, err := os.ReadFile(docsPath)
	if err != nil {
		t.Fatalf("read docs: %v", err)
	}
	if string(gotMarkdown) != string(markdown) {
		t.Fatalf("markdown=%q, want %q", gotMarkdown, markdown)
	}
	gotManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var decoded runtimeRouteManifest
	if err := yaml.Unmarshal(gotManifest, &decoded); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(decoded.Routes) != 1 || decoded.Routes[0].Method != "GET" || decoded.Routes[0].Path != "/health" {
		t.Fatalf("manifest=%#v, want one health route", decoded.Routes)
	}
}

func TestPublishRuntimeRouteArtifactsLeavesTargetsUntouchedWhenManifestStagingFails(t *testing.T) {
	dir := t.TempDir()
	docsPath := dir + "/docs.md"
	manifestPath := dir + "/missing/routes.yaml"
	original := []byte("old docs\n")
	if err := os.WriteFile(docsPath, original, 0644); err != nil {
		t.Fatalf("write original docs: %v", err)
	}

	err := publishRuntimeRouteArtifacts(docsPath, manifestPath, []byte("new docs\n"), []gin.RouteInfo{{Method: "GET", Path: "/health"}})
	if err == nil {
		t.Fatal("publish unexpectedly succeeded with a missing manifest directory")
	}
	got, readErr := os.ReadFile(docsPath)
	if readErr != nil {
		t.Fatalf("read docs after failed publish: %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("docs changed after failed staging: %q, want %q", got, original)
	}
}

func TestWriteRuntimeRouteManifestDeduplicatesAndSorts(t *testing.T) {
	path := t.TempDir() + "/routes.yaml"
	routes := []gin.RouteInfo{
		{Method: "POST", Path: "/api/z"},
		{Method: "GET", Path: "/api/b"},
		{Method: "POST", Path: "/api/z"},
		{Method: "GET", Path: "/"},
	}
	if err := writeRuntimeRouteManifest(path, routes); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var got runtimeRouteManifest
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(got.Routes) != 3 {
		t.Fatalf("routes=%d, want duplicate-free 3", len(got.Routes))
	}
	if got.Routes[0].Method != "GET" || got.Routes[0].Path != "/" || got.Routes[1].Path != "/api/b" || got.Routes[2].Method != "POST" {
		t.Fatalf("routes are not deterministically sorted: %#v", got.Routes)
	}
	for _, route := range got.Routes {
		if route.Source != "cmd/admin/gen_api_docs.go" {
			t.Fatalf("route source=%q, want runtime generator", route.Source)
		}
	}
}
