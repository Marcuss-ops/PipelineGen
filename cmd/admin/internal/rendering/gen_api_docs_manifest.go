package rendering

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

type runtimeRouteManifest struct {
	Routes []runtimeManifestRoute `yaml:"routes"`
}

type runtimeManifestRoute struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	Source string `yaml:"source"`
}

func writeRuntimeRouteManifest(path string, routes []gin.RouteInfo) error {
	data, err := runtimeRouteManifestData(routes)
	if err != nil {
		return err
	}
	return writeAtomicFile(path, data, 0644)
}

func runtimeRouteManifestData(routes []gin.RouteInfo) ([]byte, error) {
	seen := make(map[string]struct{}, len(routes))
	manifest := runtimeRouteManifest{Routes: make([]runtimeManifestRoute, 0, len(routes))}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		manifest.Routes = append(manifest.Routes, runtimeManifestRoute{
			Method: route.Method,
			Path:   route.Path,
			Source: "cmd/admin/gen_api_docs.go",
		})
	}
	sort.Slice(manifest.Routes, func(i, j int) bool {
		if manifest.Routes[i].Method != manifest.Routes[j].Method {
			return manifest.Routes[i].Method < manifest.Routes[j].Method
		}
		return manifest.Routes[i].Path < manifest.Routes[j].Path
	})
	return yaml.Marshal(manifest)
}

// publishRuntimeRouteArtifacts stages both generated files in their target
// directories before publishing either one. Because the targets live in
// different directories, the filesystem cannot provide one cross-file atomic
// rename; rollback is therefore best-effort after the first rename. All
// serialization and staging failures still occur before either target changes.
func publishRuntimeRouteArtifacts(docsPath, manifestPath string, markdown []byte, routes []gin.RouteInfo) error {
	manifest, err := runtimeRouteManifestData(routes)
	if err != nil {
		return fmt.Errorf("marshal route manifest: %w", err)
	}
	docsTemp, err := stageAtomicFile(docsPath, markdown, 0644)
	if err != nil {
		return fmt.Errorf("stage docs: %w", err)
	}
	manifestTemp, err := stageAtomicFile(manifestPath, manifest, 0644)
	if err != nil {
		_ = os.Remove(docsTemp)
		return fmt.Errorf("stage manifest: %w", err)
	}

	docsBackup, docsExisted, err := backupExistingFile(docsPath)
	if err != nil {
		_ = os.Remove(docsTemp)
		_ = os.Remove(manifestTemp)
		return fmt.Errorf("backup docs: %w", err)
	}
	manifestBackup, manifestExisted, err := backupExistingFile(manifestPath)
	if err != nil {
		_ = os.Remove(docsTemp)
		_ = os.Remove(manifestTemp)
		_ = restoreExistingFile(docsBackup, docsPath, docsExisted)
		return fmt.Errorf("backup manifest: %w", err)
	}

	publishedDocs := false
	publishedManifest := false
	rollback := func() {
		if publishedManifest {
			_ = os.Remove(manifestPath)
		}
		if publishedDocs {
			_ = os.Remove(docsPath)
		}
		_ = restoreExistingFile(manifestBackup, manifestPath, manifestExisted)
		_ = restoreExistingFile(docsBackup, docsPath, docsExisted)
		_ = os.Remove(docsTemp)
		_ = os.Remove(manifestTemp)
	}

	if err := os.Rename(docsTemp, docsPath); err != nil {
		rollback()
		return fmt.Errorf("publish docs: %w", err)
	}
	publishedDocs = true
	if err := os.Rename(manifestTemp, manifestPath); err != nil {
		rollback()
		return fmt.Errorf("publish manifest: %w", err)
	}
	publishedManifest = true
	_ = os.Remove(docsBackup)
	_ = os.Remove(manifestBackup)
	return nil
}

func backupExistingFile(path string) (string, bool, error) {
	backup, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".bak-*")
	if err != nil {
		return "", false, err
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		_ = os.Remove(backupPath)
		return "", false, err
	}
	_ = os.Remove(backupPath)
	if err := os.Rename(path, backupPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return backupPath, true, nil
}

func restoreExistingFile(backupPath, path string, existed bool) error {
	if !existed {
		return nil
	}
	if err := os.Rename(backupPath, path); err != nil {
		return err
	}
	return nil
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
	temp, err := stageAtomicFile(path, data, mode)
	if err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func stageAtomicFile(path string, data []byte, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	temp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(temp)
	}
	if err := file.Chmod(mode); err != nil {
		cleanup()
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return "", err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return "", err
	}
	return temp, nil
}
