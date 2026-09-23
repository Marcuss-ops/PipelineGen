// Package jobs — handler_registration.go: handler binding surface.
//
// PR-GODOBJ-6 (July 2026): mechanically extracted from service.go
// per the god-object decomposition plan. Zero behaviour changes on
// the canonical HandlerFunc path (pre-extraction shape was identical).
//
// PR-REFLECT-ELIM-HANDLER-REGISTRATION (2026-07-04, godlike/07 win):
// the reflection-based RegisterHandler fallback (reflect.ValueOf/Call +
// runtime ArgCount / AssignableTo / In-Out shape-validation) has been
// RETIRED per the AGENTS.md §Pattern 0 + godlike/07 typed-error
// discipline. The implementation now strictly accepts ONLY
// appjobs.HandlerFunc via a tight type-switch; any other `any` shape
// (struct, raw string, raw int, anonymous func literal of the
// structural signature, etc.) is rejected at registration time with a
// typed error — no silent-success class per godlike/07.
//
// The surface signature REMAINS `(jobType string, handler any) error`
// because 4 lock-step interface contracts depend on it (changing the
// surface breaks each compile-time assertion below):
//
//   - internal/kernel/job/service.go::Service             (kernel canonical Service)
//   - internal/capabilities/scripts/ports/ports.go::Broker (scripts broker port)
//   - internal/api/module_descriptor.go::JobRegistrar     (api capability-standard)
//   - internal/app/creator_runtime.go::brokerAdapter      (creator runtime inline adapter)
//
// Locked by the corresponding assertions at each interface declaration site
// (e.g. `var _ job.Service = (*appjobs.Service)(nil)`). Per godlike/07
// minimal-blast-radius, we tighten the IMPLEMENTATION while preserving
// the surface — the typed-error gate at registration time surfaces the
// reflection-elimination as a runtime contract that production callers
// can errors.Is / errors.As against.
//
// What changed for production callers: the canonical handler-registration
// idiom at every call site MUST now wrap the method value in
// `appjobs.HandlerFunc(h.HandleJob)` (cf. artlist precedent at
// internal/capabilities/assets/providers/artlist/job_core.go:247). The
// type-switch accepts method values whose signature structurally matches
// HandlerFunc (Go's structural subtyping auto-converts at the case
// branch), but explicit casts are canonical for human-readability
// per godlike/06 SSOT — future maintainers reading the call site see
// "this IS a HandlerFunc" without inspecting the method signature.
package jobs

import (
	"fmt"
	"sort"

	jobqueue "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// MaxJobsPerType is the canonical upper bound on registered handlers per
// job type string (P0 #15 against arbitrary dict-growth, July 2026).
// Kept here rather than types.go to colocate it with the registration
// surface; the dispatcher enforces the cap at Register time.
const MaxJobsPerType = 16

// RegisterHandler registers a handler for the given job type. Surface
// signature is locked at `(jobType string, handler any) error` by the
// 4 interface contracts listed in the package doc; the IMPLEMENTATION
// accepts ONLY `appjobs.HandlerFunc` via strict type-switch. The
// previous reflection-based fallback + structural-anonymous-function
// case have been retired (godlike/07 no-fake-availability audit-pin);
// see PR-REFLECT-ELIM-HANDLER-REGISTRATION in architecture/current.yaml.
//
// To register a method like `h.HandleJob` (whose signature matches
// HandlerFunc structurally) wrap it at the call site:
//
//	jobsSvc.RegisterHandler(jobType, appjobs.HandlerFunc(h.HandleJob))
//
// This explicit cast is canonical (cf. artlist/job_core.go:247).
// Method values without the cast are accepted by the type-switch
// (structural subtyping), but explicit casts are preferred for
// human-readability + future-proofing against signature drift.
func (s *Service) RegisterHandler(jobType string, handler any) error {
	h, ok := handler.(HandlerFunc)
	if !ok {
		return fmt.Errorf("job.Service.RegisterHandler: handler must be appjobs.HandlerFunc (apply explicit `appjobs.HandlerFunc(method)` cast at the call site); got %T for jobType=%q", handler, jobType)
	}
	return s.dispatcher.Register(jobType, h)
}

// HasHandler reports whether the broker has a handler registered
// for the given job type. Issue 7 / P1 (June 2026): added so the
// composition root can fail-fast on a script.generate wiring gap
// without leaking the Dispatcher type into the API surface.
//
// The query is branch-free -- the Dispatcher.AllHandlers() map is
// the canonical record. Returns false when:
//
//   - the receiver is nil (defensive guard)
//   - the dispatcher is nil (composition bug)
//   - no handler is registered for jobType
//
// Nil-tolerant: this method never panics; nil-receiver callers get
// false (so composition-root code can pass s.Service==nil through
// the validateScriptGenerateWiring helper without pre-checking).
func (s *Service) HasHandler(jobType string) bool {
	if s == nil {
		return false
	}
	if s.dispatcher == nil {
		return false
	}
	if jobType == "" {
		return false
	}
	_, ok := s.dispatcher.AllHandlers()[jobType]
	return ok
}

// HandlerTypes returns the sorted set of job types with a live consumer
// bound in this Master process. It is retained for internal diagnostics;
// remote callers must use AutomationCatalog so policy-only and internal jobs
// are not exposed merely because a handler exists.
func (s *Service) HandlerTypes() []string {
	if s == nil || s.dispatcher == nil {
		return nil
	}
	handlers := s.dispatcher.AllHandlers()
	out := make([]string, 0, len(handlers))
	for jobType := range handlers {
		out = append(out, jobType)
	}
	sort.Strings(out)
	return out
}

// AutomationCatalog returns the single agent-facing projection of the
// canonical registry and live handler set. It is rebuilt as a snapshot so
// callers cannot mutate process wiring through the returned value.
func (s *Service) AutomationCatalog() *AutomationCatalog {
	if s == nil {
		return NewAutomationCatalog(nil, nil)
	}
	return NewAutomationCatalog(s.registry, s.HandlerTypes())
}

// ValidateHandlerCompleteness checks that every job type registered in
// the canonical Registry has a handler bound to the Dispatcher. Returns
// nil when every job type is consumable; returns an error listing the
// first missing handler when a registration gap is detected.
//
// §15.9 (July 2026): the voiceover parent-child fan-out pair is the
// canonical trigger — when voiceover.generate_item has no handler, the
// server MUST NOT start because the parent's fan-out creates child jobs
// that can never be executed. ValidateHandlerCompleteness is the gate
// the composition root calls before Freeze().
//
// Nil-tolerant: nil receiver, nil dispatcher, and nil registry all
// return nil (the belt-and-suspenders check runs later, after the
// composition root has wired both).
func (s *Service) ValidateHandlerCompleteness(reg *Registry) error {
	if s == nil || s.dispatcher == nil || reg == nil {
		return nil
	}
	if err := jobqueue.ValidateConsumers(reg, s); err != nil {
		return fmt.Errorf("job.Service.ValidateHandlerCompleteness: %w — the server MUST NOT start with a consumable-type-without-handler gap (§15.9 registrazione incompleta)", err)
	}
	return nil
}

// compile-time assertion: appjobs.Service satisfies the kernel canonical
// job.Service interface (RegisterHandler + Enqueue + Get + Cancel + ...).
// appjobs.Service is the canonical producer — interface satisfaction is
// asserted at the *application* boundary rather than at the kernel
// declaration site; the kernel package is upstream and references
// appjobs by alias only.
//
// (job alias import retained for any future kernel-layer consumers that
// reference domain types — currently zero in this file.)
var _ job.Service = (*Service)(nil)

// ── agent-facing automation catalog ─────────────────────────────────────
//
// Colocated with the handler surface on purpose: internal/capabilities/jobs is
// a REGISTERED carry-forward hotspot whose production-file count is ratcheted at
// its measured baseline (architecture/package_hotspots.json,
// percheck_legacy_hotspot_growth), and that ratchet fails closed on growth — a
// brand-new file in this package is a hard violation even for a coherent
// addition, because the registry's own target is to SPLIT this package into
// subpackages, not to grow it. The catalog is the agent-facing projection of the
// handler surface owned here (Service.HandlerTypes / Service.AutomationCatalog),
// so it belongs beside that surface.

// AutomationCapability is the agent-facing projection of a runnable job.
// Internal implementation jobs remain out of this projection by default.
type AutomationCapability struct {
	Type                   string   `json:"type"`
	Version                string   `json:"version"`
	Description            string   `json:"description"`
	AgentVisible           bool     `json:"agent_visible"`
	M2MSubmittable         bool     `json:"m2m_submittable"`
	InputSchema            string   `json:"input_schema,omitempty"`
	ResultSchema           string   `json:"result_schema,omitempty"`
	ArtifactKinds          []string `json:"artifact_kinds,omitempty"`
	RequiredScope          string   `json:"required_scope,omitempty"`
	RequiredCapabilities   []string `json:"required_capabilities,omitempty"`
	EstimatedResourceClass string   `json:"estimated_resource_class,omitempty"`
}

// AutomationCatalog is the single agent-facing capability catalog. It is
// constructed from the canonical job registry and filtered by the live
// handler set, so policy-only or orphaned jobs cannot be submitted remotely.
type AutomationCatalog struct {
	items map[string]AutomationCapability
}

// NewAutomationCatalog builds the catalog from the canonical registry and
// the process' live handlers. The allow-list is intentionally explicit:
// internal maintenance, child, and persistence jobs never become agent tools
// merely because a handler happens to be registered.
func NewAutomationCatalog(reg *Registry, liveTypes []string) *AutomationCatalog {
	live := make(map[string]struct{}, len(liveTypes))
	for _, typ := range liveTypes {
		live[typ] = struct{}{}
	}
	catalog := &AutomationCatalog{items: make(map[string]AutomationCapability)}
	if reg == nil {
		return catalog
	}

	// These are the stable external capabilities currently implemented by
	// PipelineGen. video.assemble stays absent: assembly is not a job type
	// (it runs on the VeloxEditing media plane inside video.create), and
	// advertising a type without a durable handler would create fake
	// availability. video.create is present since Sept 2026: its durable
	// handler (internal/capabilities/videocreate) is registered and live.
	safe := map[string]AutomationCapability{
		TypeScriptGenerate: {
			Type: TypeScriptGenerate, Version: "v1", Description: "Generate a script and editorial plan",
			InputSchema: "script.generate.v1", ResultSchema: "script.generate.result.v1", ArtifactKinds: []string{"script"}, EstimatedResourceClass: "llm",
		},
		TypeVoiceoverGenerate: {
			Type: TypeVoiceoverGenerate, Version: "v1", Description: "Generate voiceover audio",
			InputSchema: "voiceover.generate.v1", ResultSchema: "voiceover.generate.result.v1", ArtifactKinds: []string{"audio"}, EstimatedResourceClass: "audio",
		},
		TypeMediaStock: {
			Type: TypeMediaStock, Version: "v1", Description: "Acquire stock media",
			InputSchema: "media.stock.v1", ResultSchema: "media.stock.result.v1", ArtifactKinds: []string{"video", "image"}, EstimatedResourceClass: "network",
		},
		TypeYouTubeClipExtract: {
			Type: TypeYouTubeClipExtract, Version: "v1", Description: "Extract a YouTube clip",
			InputSchema: "youtube_clip.extract.v1", ResultSchema: "youtube_clip.extract.result.v1", ArtifactKinds: []string{"video"}, EstimatedResourceClass: "media",
		},
		TypeImageGenerateGoogle: {
			Type: TypeImageGenerateGoogle, Version: "v1", Description: "Generate an image",
			InputSchema: "image.generate.google.v1", ResultSchema: "image.generate.google.result.v1", ArtifactKinds: []string{"image"}, EstimatedResourceClass: "gpu",
		},
		TypeClipRender: {
			Type: TypeClipRender, Version: "v1", Description: "Render a localized clip",
			InputSchema: "clip.render.v1", ResultSchema: "clip.render.result.v1", ArtifactKinds: []string{"video"}, EstimatedResourceClass: "render",
		},
		TypeVideoCreate: {
			Type: TypeVideoCreate, Version: "v1", Description: "End-to-end durable video workflow (script, media, voiceover, audio, render, assemble, mux, verify, publish)",
			InputSchema: "video.create.v1", ResultSchema: "video.create.result.v1", ArtifactKinds: []string{"video", "script", "audio"}, EstimatedResourceClass: "render",
		},
	}
	for typ, item := range safe {
		if _, registered := live[typ]; !registered {
			continue
		}
		if _, registered := reg.Get(typ); !registered {
			continue
		}
		item.AgentVisible = true
		item.M2MSubmittable = true
		item.RequiredScope = ScopeJobsSubmit
		catalog.items[typ] = item
	}
	return catalog
}

// List returns a stable, defensive snapshot for GET /api/v1/jobs/types.
func (c *AutomationCatalog) List() []AutomationCapability {
	if c == nil {
		return []AutomationCapability{}
	}
	keys := make([]string, 0, len(c.items))
	for typ := range c.items {
		keys = append(keys, typ)
	}
	sort.Strings(keys)
	out := make([]AutomationCapability, 0, len(keys))
	for _, typ := range keys {
		item := c.items[typ]
		item.ArtifactKinds = append([]string{}, item.ArtifactKinds...)
		item.RequiredCapabilities = append([]string{}, item.RequiredCapabilities...)
		out = append(out, item)
	}
	return out
}

// Allows reports whether a type is agent-visible and M2M-submittable.
func (c *AutomationCatalog) Allows(typ string) bool {
	if c == nil {
		return false
	}
	item, ok := c.items[typ]
	return ok && item.AgentVisible && item.M2MSubmittable
}
