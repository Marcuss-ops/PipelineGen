// Package realtime — IndexHealth implements the canonical PR3-5b SQLite↔Qdrant
// cross-check. Replaces the legacy /api/media/index-health handler's raw-SQL
// approach with a sample-driven diff: clip-asset ids from SQLite vs. point
// asset_ids from Qdrant, capped to a configurable sample (default 5000).
//
// Reporting semantics:
//   - sqlite_assets = total media_assets rows (excluding soft-deleted) via clips.CountAll
//   - sqlite_indexed = media_assets rows with a populated embedding_json via clips.CountIndexed
//   - qdrant_points = point count of the ALIAS-SERVED collection via
//     vectorstore.OperationCollectionInfo (NOT the physical versioned
//     collection — drift between the alias and SQLite is what users see)
//   - missing_in_qdrant = ids in the SQLite indexed sample that are absent from
//     the Qdrant sample (UNDER-count if samples saturated the cap)
//   - orphan_in_qdrant = ids in the Qdrant sample that are absent from the
//     SQLite indexed sample (UNDER-count if samples saturated the cap)
//   - pending_outbox = media_index_outbox rows in 'pending' state
//   - dead_letter = media_index_outbox rows in 'dead_letter' state
//   - qdrant_healthy = the /readyz probe succeeded at call time
//   - checks_complete = every independent source (qdrant + sqlite + outbox)
//     responded without error during this call
//   - sample_limit / sample_saturated / counts_are_lower_bounds = cap
//     applied to the diff and whether it actually hit the cap.
//   - degraded = any per-leg probe failure (clips_listing, qdrant_info,
//     qdrant, sqlite, outbox); independent of OK — drift-only
//     configurations produce Degraded=false because drift is ingestion,
//     not operational.
//   - degraded_sources = granular per-leg names appended in this
//     canonical order — data-path legs first, then qdrant-side legs,
//     then operational side-channel legs:
//     1. clips_listing (clips.ListIndexedIDs) — the diff side
//     2. qdrant_info   (OperationCollectionInfo/ListPointIDs)
//     3. qdrant        (Health probe)
//     4. sqlite        (CountAll/CountIndexed)
//     5. outbox        (CountByStatus)
//     Mirrors the on-call mental model: "did our data land? were the
//     searches correct? was infra healthy? was housekeeping up to date?".
//     nil deps never contribute to the slice.
//   - OK = ChecksComplete AND qdrant_healthy AND
//     missing_in_qdrant == 0 AND orphan_in_qdrant == 0 AND
//     dead_letter == 0.
package realtime

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
)

// IndexHealthSampleCap bounds the cross-check sample size.
const IndexHealthSampleCap = 5000

// IndexHealthTimeout bounds the entire call so a stuck Qdrant scroll does
// not hang the HTTP handler that invokes this.
const IndexHealthTimeout = 60 * time.Second

// IndexHealthClips narrows the canonical clips repo surface to only the
// methods IndexHealth needs. Real *assets.ClipsRepository satisfies this
// interface structurally; test fakes inject failing clones without
// touching the concrete repository.
//
// Moved from the concrete *assets.ClipsRepository field on Service (Task 7) so
// attribution tests can swap in a fake whose ListIndexedIDs returns an
// error without requiring a misconfigured DB to drive that path.
type IndexHealthClips interface {
	CountAll(ctx context.Context) (int64, error)
	CountIndexed(ctx context.Context) (int64, error)
	ListIndexedIDs(ctx context.Context, limit int) ([]string, error)
}

// IndexHealthOutbox narrows the canonical outbox repo surface to only
// CountByStatus — the only call IndexHealth makes against outbox.
// *outbox.Repository satisfies this interface structurally.
type IndexHealthOutbox interface {
	CountByStatus(ctx context.Context, status string) (int64, error)
}

// IndexHealth computes the canonical cross-check report. See package doc.
// Per-leg granularity: fetchQdrantScene returns (qdrantOK,
// sqliteListOK) so the diff side (clips.ListIndexedIDs) and the qdrant
// side (OperationCollectionInfo/ListPointIDs) are attributed to
// distinct DegradedSources entries — operators see WHICH leg broke
// rather than a coarse "qdrant_info" badge that hides SQLite failures.
// A nil dep falls back to zero fields + a logged warning rather than
// failing the call — the HTTP handler decides whether to surface the
// gap as degraded.
func (s *Service) IndexHealth(ctx context.Context) (*vectorstore.IndexHealthReport, error) {
	if s == nil {
		return nil, errors.New("realtime.Service is nil")
	}
	if s.vectorSvc == nil {
		return nil, errors.New("realtime.IndexHealth: vector store not configured")
	}

	// Bounded timeout: a stuck Qdrant scroll or an SQLite lock should
	// not stall the HTTP handler that invoked us.
	ctx, cancel := context.WithTimeout(ctx, IndexHealthTimeout)
	defer cancel()

	report := &vectorstore.IndexHealthReport{
		SampleLimit: IndexHealthSampleCap,
	}

	// Per-source success flags. fetchQdrantScene now returns TWO flags so
	// we can attribute qdrant_info vs clips_listing separately.
	qdrantHealthOK := s.probeQdrantHealth(ctx, report)
	qdrantOK, sqliteListOK := s.fetchQdrantScene(ctx, report)
	sqliteOK := s.fetchSQLiteCounts(ctx, report)
	outboxOK := s.fetchOutboxCounts(ctx, report)

	report.ChecksComplete = qdrantHealthOK && qdrantOK && sqliteListOK && sqliteOK && outboxOK

	// Granular per-leg failure breakdown. nil deps never contribute (the
	// leg is vacuously OK, see fetchSQLiteCounts / fetchOutboxCounts).
	// Append order matches the package-doc enumeration
	// (clips_listing, qdrant_info, qdrant, sqlite, outbox) so operators
	// grepping the report see the failure legs in the same order they
	// appear in the prose documentation. Tests use slices.Contains and
	// are order-independent; an additional explicit order assertion is
	// in TestIndexHealth_DegradedSourcesAppendOrderPinned.
	if !sqliteListOK {
		report.DegradedSources = append(report.DegradedSources, "clips_listing")
	}
	if !qdrantOK {
		report.DegradedSources = append(report.DegradedSources, "qdrant_info")
	}
	if !qdrantHealthOK {
		report.DegradedSources = append(report.DegradedSources, "qdrant")
	}
	if !sqliteOK {
		report.DegradedSources = append(report.DegradedSources, "sqlite")
	}
	if !outboxOK {
		report.DegradedSources = append(report.DegradedSources, "outbox")
	}

	report.OK = report.ChecksComplete && report.QdrantHealthy &&
		report.MissingInQdrant == 0 && report.OrphanInQdrant == 0 &&
		report.DeadLetter == 0
	report.Degraded = len(report.DegradedSources) > 0

	if s.log != nil {
		s.log.Info("IndexHealth report",
			zap.Int64("sqlite_assets", report.SQLiteAssets),
			zap.Int64("sqlite_indexed", report.SQLiteIndexed),
			zap.Int64("qdrant_points", report.QdrantPoints),
			zap.Int64("missing_in_qdrant", report.MissingInQdrant),
			zap.Int64("orphan_in_qdrant", report.OrphanInQdrant),
			zap.Int64("pending_outbox", report.PendingOutbox),
			zap.Int64("dead_letter", report.DeadLetter),
			zap.Bool("qdrant_healthy", report.QdrantHealthy),
			zap.Bool("checks_complete", report.ChecksComplete),
			zap.Bool("sample_saturated", report.SampleSaturated),
			zap.Strings("degraded_sources", report.DegradedSources),
			zap.Bool("degraded", report.Degraded),
			zap.Bool("ok", report.OK),
		)
	}
	return report, nil
}

// probeQdrantHealth issues the /readyz probe and updates report.QdrantHealthy.
func (s *Service) probeQdrantHealth(ctx context.Context, report *vectorstore.IndexHealthReport) bool {
	if err := s.vectorSvc.Health(ctx); err != nil {
		report.QdrantHealthy = false
		if s.log != nil {
			s.log.Warn("IndexHealth: qdrant health probe failed", zap.Error(err))
		}
		return false
	}
	report.QdrantHealthy = true
	return true
}

// fetchQdrantScene queries the alias-scoped collection point count + a
// capped asset_id sample and computes the SQLite↔Qdrant diff. Returns
// (qdrantOK, sqliteListOK) independently so each leg can be attributed
// to its degradation source ("qdrant_info" for OperationCollectionInfo /
// ListPointIDs failures; "clips_listing" for ListIndexedIDs failures).
//
// Symmetric vacuously-OK + mutual-exclusion contract:
//
// (1) qdrant_info and clips_listing are mutually exclusive in any given
//
//	report — they share a single code path in fetchQdrantScene, gated
//	on qdrant_info succeeding. If qdrant_info succeeds, clips_listing
//	IS probed and may enter DegradedSources. If qdrant_info fails
//	(OperationCollectionInfo / ListPointIDs error), clips_listing is
//	UNPROBED — NOT failed, NOT healthy.
//
// (2) When qdrant_info fails, sqliteListOK stays at its initial value
//
//	(true) and clips.ListIndexedIDs is never called. Operators
//	reading DegradedSources=["qdrant_info"] must understand that
//	clips_listing is UNPROBED. fetchSQLiteCounts runs independently
//	so clips IS probed for bulk counts, just not for the diff-
//	specific ID listing.
//
// (3) The append order in IndexHealth prioritises clips_listing before
//
//	qdrant_info so this operator-readable invariant holds: a single
//	failure scenario never produces both names simultaneously.
func (s *Service) fetchQdrantScene(ctx context.Context, report *vectorstore.IndexHealthReport) (qdrantOK bool, sqliteListOK bool) {
	qdrantOK = false
	sqliteListOK = true // vacuously OK when s.clips is nil OR when the qdrant leg fails first

	// (info, err) == (nil, nil) is treated as a soft failure so the
	// per-source flag still tracks an empty response as degraded rather
	// than reporting a misleading QdrantPoints=0.
	info, err := s.vectorSvc.OperationCollectionInfo(ctx)
	switch {
	case err != nil:
		if s.log != nil {
			s.log.Warn("IndexHealth: operation collection info failed", zap.Error(err))
		}
		return false, sqliteListOK
	case info == nil:
		if s.log != nil {
			s.log.Warn("IndexHealth: operation collection info returned nil")
		}
		return false, sqliteListOK
	default:
		report.QdrantPoints = info.PointsCount
	}

	ids, err := s.vectorSvc.ListPointIDs(ctx, IndexHealthSampleCap)
	if err != nil {
		if s.log != nil {
			s.log.Warn("IndexHealth: list point IDs failed", zap.Error(err))
		}
		return false, sqliteListOK
	}
	qdrantOK = true

	qdrantSample := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		qdrantSample[id] = struct{}{}
	}

	// SQLite diff — only the ID list lives here; bulk counts are owned
	// by fetchSQLiteCounts so each per-source success flag tracks
	// exactly one set of reads. clips_listing attribution kicks in
	// here when ListIndexedIDs errors. `sqliteListOK` is already true
	// from the function-entry default when s.clips is nil, so no
	// explicit branch is needed for the nil-dep case.
	if s.clips != nil {
		idsSQLite, err := s.clips.ListIndexedIDs(ctx, IndexHealthSampleCap)
		if err != nil {
			if s.log != nil {
				s.log.Warn("IndexHealth: clips.ListIndexedIDs failed", zap.Error(err))
			}
			sqliteListOK = false
			// qdrantOK stays true — the qdrant leg succeeded; the
			// diff side failed because clips is in trouble. Surface
			// only "clips_listing" in DegradedSources (not "qdrant_info").
		} else {
			sqliteSample := make(map[string]struct{}, len(idsSQLite))
			for _, id := range idsSQLite {
				if id == "" {
					continue
				}
				sqliteSample[id] = struct{}{}
			}
			report.MissingInQdrant, report.MissingInQdrantIDs = diffIDs(sqliteSample, qdrantSample, IndexHealthSampleCap)
			report.OrphanInQdrant, report.OrphanInQdrantIDs = diffIDs(qdrantSample, sqliteSample, IndexHealthSampleCap)
		}
	}

	// Sample-saturation flag — apply BEFORE returning so the JSON
	// payload reflects it even when the SQLite diff is empty.
	saturated := int64(len(ids)) >= int64(IndexHealthSampleCap) && report.QdrantPoints > int64(IndexHealthSampleCap)
	report.SampleSaturated = saturated
	report.CountsAreLowerBounds = saturated
	return qdrantOK, sqliteListOK
}

// fetchSQLiteCounts reads CountAll + CountIndexed. nil-clips is treated as
// vacuously OK (wiring gap logged at startup by NewService).
func (s *Service) fetchSQLiteCounts(ctx context.Context, report *vectorstore.IndexHealthReport) bool {
	if s.clips == nil {
		return true
	}
	allOK := true
	if n, err := s.clips.CountAll(ctx); err != nil {
		if s.log != nil {
			s.log.Warn("IndexHealth: clips.CountAll failed", zap.Error(err))
		}
		allOK = false
	} else {
		report.SQLiteAssets = n
		report.DBTotal = n
		if report.QdrantPoints > 0 {
			report.DBToQdrantDelta = n - report.QdrantPoints
		}
	}
	if n, err := s.clips.CountIndexed(ctx); err != nil {
		if s.log != nil {
			s.log.Warn("IndexHealth: clips.CountIndexed failed", zap.Error(err))
		}
		allOK = false
	} else {
		report.SQLiteIndexed = n
		report.WithEmbedding = n
	}
	return allOK
}

// fetchOutboxCounts reads pending + dead_letter. nil-outbox is vacuously OK.
func (s *Service) fetchOutboxCounts(ctx context.Context, report *vectorstore.IndexHealthReport) bool {
	if s.outbox == nil {
		return true
	}
	allOK := true
	if n, err := s.outbox.CountByStatus(ctx, "pending"); err != nil {
		if s.log != nil {
			s.log.Warn("IndexHealth: outbox.CountByStatus(pending) failed", zap.Error(err))
		}
		allOK = false
	} else {
		report.PendingOutbox = n
	}
	if n, err := s.outbox.CountByStatus(ctx, "dead_letter"); err != nil {
		if s.log != nil {
			s.log.Warn("IndexHealth: outbox.CountByStatus(dead_letter) failed", zap.Error(err))
		}
		allOK = false
	} else {
		report.DeadLetter = n
	}
	return allOK
}

// diffIDs returns the keys present in `source` but absent from `reference`,
// along with the count. Caps `out` at `cap` ids (sample-saturation guard).
func diffIDs(source, reference map[string]struct{}, cap int) (int64, []string) {
	if source == nil || reference == nil {
		return 0, nil
	}
	out := make([]string, 0, cap)
	for id := range source {
		if id == "" {
			continue
		}
		if _, ok := reference[id]; ok {
			continue
		}
		out = append(out, id)
		if len(out) >= cap {
			break
		}
	}
	return int64(len(out)), out
}
