// Package health — readyz_checkers_script.go (sister file E).
//
// Script-generation readiness probe (July 2026). Verifies that
// script.generate can accept traffic by checking its concrete
// dependencies: database, job registry/worker, Ollama, document
// service, Drive, and the /api/script route.
package system

import (
	"context"
	"strings"
	"time"
)

// ScriptGenerateChecker evaluates readiness of the script.generate
// capability. nil-safe: a nil checker reports applicable=false.
type ScriptGenerateChecker interface {
	CheckScriptGenerate(ctx context.Context) CheckResult
}

// CompositeScriptGenerateChecker runs the seven sub-checks that
// together determine whether script.generate is ready.
type CompositeScriptGenerateChecker struct {
	svc           *Service
	ollama        OllamaChecker
	driveFolder   DriveFolderChecker
	driveFolderID string
	publisher     PublisherChecker
	docAvailable  func() bool
	hasHandler    func(string) bool
	routeMounted  func() bool
}

// NewScriptGenerateChecker builds a composite checker for
// script.generate readiness.
//
//   - svc: the health Service used to run db/jobs sub-checks.
//   - ollama: Ollama reachability probe (nil → skipped).
//   - driveFolder: Drive folder accessibility probe (nil → fails).
//   - driveFolderID: target Drive folder ID (empty → fails).
//   - publisher: Publisher wiring probe (nil → fails).
//   - docAvailable: closure returning true when the Google Docs
//     client / document service is wired.
//   - hasHandler: closure returning true when a job handler is
//     registered for the supplied job type.
func NewScriptGenerateChecker(
	svc *Service,
	ollama OllamaChecker,
	driveFolder DriveFolderChecker,
	driveFolderID string,
	publisher PublisherChecker,
	docAvailable func() bool,
	hasHandler func(string) bool,
) *CompositeScriptGenerateChecker {
	return &CompositeScriptGenerateChecker{
		svc:           svc,
		ollama:        ollama,
		driveFolder:   driveFolder,
		driveFolderID: driveFolderID,
		publisher:     publisher,
		docAvailable:  docAvailable,
		hasHandler:    hasHandler,
	}
}

// SetRouteMounted wires the route-mounted probe. Called by the
// transport layer after the gin engine has been fully built.
func (c *CompositeScriptGenerateChecker) SetRouteMounted(fn func() bool) {
	c.routeMounted = fn
}

// CheckScriptGenerate runs the seven sub-checks and returns a single
// CheckResult with a `details` map.
func (c *CompositeScriptGenerateChecker) CheckScriptGenerate(ctx context.Context) CheckResult {
	start := time.Now()
	if c.svc == nil {
		return CheckResult{
			"ok":          false,
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       "health service not wired",
		}
	}

	details := map[string]any{}
	var failures []string
	ok := true

	// 1. Database
	dbRes := c.svc.Check(ctx, []string{"db"})
	dbCheck := dbRes.Checks["db"]
	details["db"] = dbCheck
	if !dbRes.OK {
		ok = false
		failures = append(failures, "database unhealthy")
	}

	// 2. Job registry / worker (broker heartbeat + handler presence)
	jobsRes := c.svc.Check(ctx, []string{"jobs"})
	details["jobs"] = jobsRes.Checks["jobs"]
	if !jobsRes.OK {
		ok = false
		failures = append(failures, "job broker unhealthy")
	}

	// 3. script.generate handler registered
	hasHandler := c.hasHandler != nil && c.hasHandler("script.generate")
	details["script_generate_handler"] = map[string]any{"registered": hasHandler}
	if !hasHandler {
		ok = false
		failures = append(failures, "script.generate handler not registered")
	}

	// 4. Ollama (required for script generation).
	if c.ollama == nil {
		details["ollama"] = map[string]any{"ok": false, "error": "Ollama checker not wired"}
		ok = false
		failures = append(failures, "ollama checker not wired")
	} else {
		ollamaErr := c.ollama.CheckOllama(ctx)
		ollamaOK := ollamaErr == nil
		details["ollama"] = map[string]any{"ok": ollamaOK, "error": errorString(ollamaErr)}
		if !ollamaOK {
			ok = false
			failures = append(failures, "ollama unreachable")
		}
	}

	// 5. Drive (Publisher + folder)
	driveOK := true
	if c.publisher == nil || !c.publisher.IsWired() {
		driveOK = false
		details["drive_publisher"] = map[string]any{"ok": false, "error": "delivery.Publisher not wired"}
		failures = append(failures, "drive publisher not wired")
	} else {
		details["drive_publisher"] = map[string]any{"ok": true, "wired": true}
	}
	if c.driveFolder == nil || c.driveFolderID == "" {
		driveOK = false
		details["drive_folder"] = map[string]any{"ok": false, "error": "Drive folder not configured"}
		failures = append(failures, "drive folder not configured")
	} else {
		folderErr := c.driveFolder.CheckFolder(ctx, c.driveFolderID)
		folderOK := folderErr == nil
		details["drive_folder"] = map[string]any{"ok": folderOK, "error": errorString(folderErr)}
		if !folderOK {
			driveOK = false
			failures = append(failures, "drive folder inaccessible")
		}
	}
	details["drive"] = map[string]any{"ok": driveOK}
	if !driveOK {
		ok = false
	}

	// 6. Document service
	docOK := c.docAvailable != nil && c.docAvailable()
	details["document_service"] = map[string]any{"ok": docOK}
	if !docOK {
		ok = false
		failures = append(failures, "document service not available")
	}

	// 7. Route mounted
	routeMounted := c.routeMounted != nil && c.routeMounted()
	details["route"] = map[string]any{"mounted": routeMounted}
	if !routeMounted {
		ok = false
		failures = append(failures, "script route not mounted")
	}

	result := CheckResult{
		"ok":          ok,
		"duration_ms": time.Since(start).Milliseconds(),
		"details":     details,
	}
	if !ok {
		result["error"] = strings.Join(failures, "; ")
	}
	return result
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
