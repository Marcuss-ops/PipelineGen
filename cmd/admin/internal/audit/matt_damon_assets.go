// Package audit — matt_damon_assets.go.
//
// Read-only verification of the canonical Matt Damon asset set. Membership is
// established only by structured identity facts:
//   - metadata_json.canonical_entity_id / metadata_json.subject_id;
//   - canonical subjects rows resolved to those structured subject IDs;
//   - entity image catalog candidate -> materialization -> media_asset links.
//
// Filename, display name, and source values are deliberately not used to
// establish membership. They are not loaded by the audit engine at all.
//
// Duplicate groups are built only from strong logical identity evidence:
//   - a repeated drive_file_id;
//   - a repeated media_asset_sources (source_type, source_uri) tuple;
//   - a repeated YouTube (video_id, start_ms, end_ms) segment;
//   - a repeated entity-catalog candidate materialization.
//
// SHA-256 equality is reported as shared physical content, not as a logical
// duplicate: CAS explicitly permits several logical assets to reference the
// same immutable bytes. No UPDATE, DELETE, or write-capable SQLite handle is
// used by the CLI command.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	entitycatalog "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

const (
	mattDamonCanonicalEntityID = "person:matt-damon"
	mattDamonCanonicalName     = "Matt Damon"
	mattDamonExpectedAssets    = 65
)

type mattDamonAuditReport struct {
	SchemaVersion       int                       `json:"schema_version"`
	Mode                string                    `json:"mode"`
	Entity              string                    `json:"canonical_entity_id"`
	CanonicalName       string                    `json:"canonical_name"`
	ExpectedAssetCount  int                       `json:"expected_asset_count"`
	AssetCount          int                       `json:"asset_count"`
	CountMatches        bool                      `json:"count_matches"`
	MembershipSources   []string                  `json:"membership_sources"`
	UnresolvedAssetIDs  []string                  `json:"unresolved_structured_asset_ids,omitempty"`
	Assets              []mattDamonAssetRecord    `json:"assets"`
	DuplicateGroups     []mattDamonDuplicateGroup `json:"duplicate_groups"`
	SharedContentGroups []mattDamonContentGroup   `json:"shared_content_groups"`
}

type mattDamonAssetRecord struct {
	AssetID        string   `json:"asset_id"`
	MediaType      string   `json:"media_type,omitempty"`
	DriveFileID    string   `json:"drive_file_id,omitempty"`
	YouTubeVideoID string   `json:"youtube_video_id,omitempty"`
	StartMS        int64    `json:"start_ms,omitempty"`
	EndMS          int64    `json:"end_ms,omitempty"`
	ContentSHA256  string   `json:"content_sha256,omitempty"`
	BinarySHA256   string   `json:"binary_sha256,omitempty"`
	Evidence       []string `json:"structured_membership_evidence"`
}

type mattDamonDuplicateGroup struct {
	AssetIDs []string `json:"asset_ids"`
	Evidence []string `json:"strong_identity_evidence"`
}

type mattDamonContentGroup struct {
	HashType string   `json:"hash_type"`
	Hash     string   `json:"hash"`
	AssetIDs []string `json:"asset_ids"`
}

type mattDamonAssetRow struct {
	ID             string
	MediaType      string
	DriveFileID    string
	YouTubeVideoID string
	StartMS        int64
	EndMS          int64
	ContentSHA256  string
	BinarySHA256   string
	MetadataJSON   string
}

type mattDamonSourceRow struct {
	AssetID    string
	SourceType string
	SourceURI  string
}

// RunMattDamonAssetsAudit is intentionally read-only. It has no --apply flag
// because selecting a survivor or mutating production rows requires a
// separate, explicitly reviewed migration plan.
func RunMattDamonAssetsAudit(args []string) error {
	fs := flag.NewFlagSet("audit-matt-damon-assets", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "emit the report as JSON")
	reportPath := fs.String("report", "", "write the JSON report to a file")
	expectedCount := fs.Int("expected-count", mattDamonExpectedAssets, "expected number of structured Matt Damon assets")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *expectedCount < 0 {
		return errors.New("audit-matt-damon-assets: --expected-count cannot be negative")
	}

	cfg, _, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	// OpenReadOnly is used instead of OpenSQLiteDB so this command cannot
	// create or mutate the operational database as a side effect.
	db, err := storage.OpenReadOnly(cfg.Storage.PrimaryDBFullPath())
	if err != nil {
		return fmt.Errorf("audit-matt-damon-assets: open canonical DB read-only: %w", err)
	}
	defer db.Close()

	report, err := auditMattDamonAssets(cli.CmdContext(), db, *expectedCount)
	if err != nil {
		return err
	}

	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("audit-matt-damon-assets: marshal report: %w", err)
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, append(payload, '\n'), 0o644); err != nil {
			return fmt.Errorf("audit-matt-damon-assets: write report: %w", err)
		}
		fmt.Printf("audit-matt-damon-assets: report written to %s\n", *reportPath)
	} else if *jsonOutput {
		fmt.Println(string(payload))
	} else {
		printMattDamonAuditReport(report)
	}

	if !report.CountMatches {
		return fmt.Errorf("audit-matt-damon-assets: structured asset count=%d, expected=%d", report.AssetCount, report.ExpectedAssetCount)
	}
	if len(report.DuplicateGroups) > 0 {
		return fmt.Errorf("audit-matt-damon-assets: found %d logical duplicate group(s); no rows were modified", len(report.DuplicateGroups))
	}
	return nil
}

func auditMattDamonAssets(ctx context.Context, db *sql.DB, expectedCount int) (mattDamonAuditReport, error) {
	if db == nil {
		return mattDamonAuditReport{}, errors.New("audit-matt-damon-assets: nil database")
	}
	identity, err := entitycatalog.CanonicalizePersonName(mattDamonCanonicalName)
	if err != nil {
		return mattDamonAuditReport{}, fmt.Errorf("resolve Matt Damon canonical identity: %w", err)
	}
	if identity.CanonicalEntityID != mattDamonCanonicalEntityID {
		return mattDamonAuditReport{}, fmt.Errorf("canonical Matt Damon identity drifted: got %q, want %q", identity.CanonicalEntityID, mattDamonCanonicalEntityID)
	}

	subjectTokens, err := mattDamonSubjectTokens(ctx, db, identity)
	if err != nil {
		return mattDamonAuditReport{}, err
	}

	catalogAssetIDs, catalogEvidence, unresolvedCatalogIDs, err := mattDamonCatalogAssets(ctx, db)
	if err != nil {
		return mattDamonAuditReport{}, err
	}
	assets, err := mattDamonStructuredAssets(ctx, db, subjectTokens, catalogAssetIDs, catalogEvidence)
	if err != nil {
		return mattDamonAuditReport{}, err
	}

	assetIDs := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		assetIDs[asset.AssetID] = struct{}{}
	}

	sourceRows, err := mattDamonSourceRows(ctx, db, assetIDs)
	if err != nil {
		return mattDamonAuditReport{}, err
	}
	duplicateGroups := buildMattDamonDuplicateGroups(assets, sourceRows)
	sharedContentGroups := buildMattDamonContentGroups(assets)

	membershipSources := make(map[string]struct{})
	for _, asset := range assets {
		for _, evidence := range asset.Evidence {
			membershipSources[evidence] = struct{}{}
		}
	}
	membershipSourceList := make([]string, 0, len(membershipSources))
	for source := range membershipSources {
		membershipSourceList = append(membershipSourceList, source)
	}
	sort.Strings(membershipSourceList)

	sort.Slice(assets, func(i, j int) bool { return assets[i].AssetID < assets[j].AssetID })
	// Catalog materializations are structured membership evidence. Report only
	// stale links that do not resolve to a live media_assets row; they must not
	// inflate the asset count or become phantom duplicate members.
	unresolvedCatalogIDs = sortedStringSetDifference(catalogAssetIDs, assetIDs)

	return mattDamonAuditReport{
		SchemaVersion:       1,
		Mode:                "audit-matt-damon-assets-read-only",
		Entity:              identity.CanonicalEntityID,
		CanonicalName:       identity.CanonicalName,
		ExpectedAssetCount:  expectedCount,
		AssetCount:          len(assets),
		CountMatches:        len(assets) == expectedCount,
		MembershipSources:   membershipSourceList,
		UnresolvedAssetIDs:  unresolvedCatalogIDs,
		Assets:              assets,
		DuplicateGroups:     duplicateGroups,
		SharedContentGroups: sharedContentGroups,
	}, nil
}

func mattDamonSubjectTokens(ctx context.Context, db *sql.DB, identity entitycatalog.PersonIdentity) (map[string]struct{}, error) {
	tokens := map[string]struct{}{
		identity.CanonicalEntityID: {},
		"matt-damon":               {},
	}
	if !mattDamonTableExists(ctx, db, "subjects") {
		return tokens, nil
	}
	columns, err := mattDamonTableColumns(ctx, db, "subjects")
	if err != nil {
		return nil, err
	}
	selectColumns := []string{"id"}
	where := []string{"id = ?"}
	args := []any{"matt-damon"}
	if columns["slug"] {
		selectColumns = append(selectColumns, "slug")
		where = append(where, "slug = ?")
		args = append(args, "matt-damon")
	}
	if columns["uuid"] {
		selectColumns = append(selectColumns, "uuid")
	}
	if columns["display_name_norm"] {
		selectColumns = append(selectColumns, "display_name_norm")
		where = append(where, "display_name_norm = ?")
		args = append(args, "matt damon")
	}
	query := "SELECT " + strings.Join(selectColumns, ", ") + " FROM subjects WHERE " + strings.Join(where, " OR ")
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("audit-matt-damon-assets: resolve structured subject: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, len(selectColumns))
		pointers := make([]any, len(selectColumns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, fmt.Errorf("audit-matt-damon-assets: scan structured subject: %w", err)
		}
		for _, value := range values {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				tokens[text] = struct{}{}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit-matt-damon-assets: iterate structured subjects: %w", err)
	}
	return tokens, nil
}

func mattDamonCatalogAssets(ctx context.Context, db *sql.DB) (map[string]struct{}, map[string][]string, []string, error) {
	assetIDs := make(map[string]struct{})
	evidence := make(map[string][]string)
	if !mattDamonTableExists(ctx, db, "entity_image_catalog_entities") ||
		!mattDamonTableExists(ctx, db, "entity_image_catalog_candidates") ||
		!mattDamonTableExists(ctx, db, "entity_image_catalog_materializations") {
		return assetIDs, evidence, nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT m.asset_id, c.candidate_id
		FROM entity_image_catalog_candidates c
		JOIN entity_image_catalog_materializations m ON m.candidate_id = c.candidate_id
		WHERE c.canonical_entity_id = ? AND COALESCE(m.asset_id, '') <> ''`, mattDamonCanonicalEntityID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("audit-matt-damon-assets: read entity catalog materializations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var assetID string
		var candidateID int64
		if err := rows.Scan(&assetID, &candidateID); err != nil {
			return nil, nil, nil, fmt.Errorf("audit-matt-damon-assets: scan entity catalog materialization: %w", err)
		}
		assetID = strings.TrimSpace(assetID)
		if assetID == "" {
			continue
		}
		assetIDs[assetID] = struct{}{}
		evidence[assetID] = appendUniqueString(evidence[assetID], fmt.Sprintf("entity_catalog_candidate:%d", candidateID))
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("audit-matt-damon-assets: iterate entity catalog materializations: %w", err)
	}

	// The caller computes stale catalog links after reading media_assets. The
	// catalog IDs are retained as evidence but never count as assets by
	// themselves.
	return assetIDs, evidence, nil, nil
}

func mattDamonStructuredAssets(ctx context.Context, db *sql.DB, subjectTokens, catalogAssetIDs map[string]struct{}, catalogEvidence map[string][]string) ([]mattDamonAssetRecord, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id,
		       COALESCE(media_type, ''),
		       COALESCE(drive_file_id, ''),
		       COALESCE(source_video_id, COALESCE(youtube_video_id, '')),
		       COALESCE(start_ms, 0),
		       COALESCE(end_ms, 0),
		       COALESCE(content_sha256, ''),
		       COALESCE(binary_sha256, ''),
		       COALESCE(metadata_json, '{}')
		FROM media_assets
		WHERE COALESCE(lifecycle_state, '') <> 'DELETED'
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("audit-matt-damon-assets: scan media_assets: %w", err)
	}
	defer rows.Close()

	out := make([]mattDamonAssetRecord, 0)
	seen := make(map[string]struct{})
	for rows.Next() {
		var row mattDamonAssetRow
		if err := rows.Scan(&row.ID, &row.MediaType, &row.DriveFileID, &row.YouTubeVideoID, &row.StartMS, &row.EndMS, &row.ContentSHA256, &row.BinarySHA256, &row.MetadataJSON); err != nil {
			return nil, fmt.Errorf("audit-matt-damon-assets: scan media asset: %w", err)
		}
		row.ID = strings.TrimSpace(row.ID)
		if row.ID == "" {
			continue
		}
		metadata := map[string]any{}
		if strings.TrimSpace(row.MetadataJSON) != "" && strings.TrimSpace(row.MetadataJSON) != "{}" {
			if err := json.Unmarshal([]byte(row.MetadataJSON), &metadata); err != nil {
				return nil, fmt.Errorf("audit-matt-damon-assets: malformed metadata_json for asset %q: %w", row.ID, err)
			}
		}

		evidence := append([]string(nil), catalogEvidence[row.ID]...)
		if structuredMetadataMatches(metadata, subjectTokens) {
			evidence = appendUniqueString(evidence, "metadata_json.structured_entity")
		}
		if _, ok := catalogAssetIDs[row.ID]; ok {
			evidence = appendUniqueString(evidence, "entity_catalog_materialization")
		}
		if len(evidence) == 0 {
			continue
		}
		if _, ok := seen[row.ID]; ok {
			continue
		}
		seen[row.ID] = struct{}{}
		out = append(out, mattDamonAssetRecord{
			AssetID:        row.ID,
			MediaType:      row.MediaType,
			DriveFileID:    row.DriveFileID,
			YouTubeVideoID: row.YouTubeVideoID,
			StartMS:        row.StartMS,
			EndMS:          row.EndMS,
			ContentSHA256:  row.ContentSHA256,
			BinarySHA256:   row.BinarySHA256,
			Evidence:       evidence,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit-matt-damon-assets: iterate media_assets: %w", err)
	}
	return out, nil
}

func structuredMetadataMatches(metadata map[string]any, subjectTokens map[string]struct{}) bool {
	for _, key := range []string{"canonical_entity_id", "subject_id"} {
		value, ok := metadata[key].(string)
		if !ok {
			continue
		}
		if _, ok := subjectTokens[strings.TrimSpace(value)]; ok {
			return true
		}
	}
	return false
}

func mattDamonSourceRows(ctx context.Context, db *sql.DB, assetIDs map[string]struct{}) ([]mattDamonSourceRow, error) {
	if len(assetIDs) == 0 || !mattDamonTableExists(ctx, db, "media_asset_sources") {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT asset_id, source_type, source_uri
		FROM media_asset_sources
		WHERE COALESCE(source_type, '') <> '' AND COALESCE(source_uri, '') <> ''`)
	if err != nil {
		return nil, fmt.Errorf("audit-matt-damon-assets: read source identities: %w", err)
	}
	defer rows.Close()
	out := make([]mattDamonSourceRow, 0)
	for rows.Next() {
		var row mattDamonSourceRow
		if err := rows.Scan(&row.AssetID, &row.SourceType, &row.SourceURI); err != nil {
			return nil, fmt.Errorf("audit-matt-damon-assets: scan source identity: %w", err)
		}
		if _, ok := assetIDs[strings.TrimSpace(row.AssetID)]; ok {
			out = append(out, row)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit-matt-damon-assets: iterate source identities: %w", err)
	}
	return out, nil
}

type mattDamonEvidenceKey struct {
	KeyType  string
	Key      string
	AssetIDs []string
}

func buildMattDamonDuplicateGroups(assets []mattDamonAssetRecord, sources []mattDamonSourceRow) []mattDamonDuplicateGroup {
	keys := make([]mattDamonEvidenceKey, 0)
	byKey := make(map[string]*mattDamonEvidenceKey)
	add := func(keyType, key, assetID string) {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(assetID) == "" {
			return
		}
		mapKey := keyType + "\x00" + key
		entry := byKey[mapKey]
		if entry == nil {
			entry = &mattDamonEvidenceKey{KeyType: keyType, Key: key}
			byKey[mapKey] = entry
			keys = append(keys, *entry)
		}
		for _, existing := range entry.AssetIDs {
			if existing == assetID {
				return
			}
		}
		entry.AssetIDs = append(entry.AssetIDs, assetID)
	}
	for _, asset := range assets {
		if asset.DriveFileID != "" {
			add("drive_file_id", asset.DriveFileID, asset.AssetID)
		}
		if asset.YouTubeVideoID != "" && asset.EndMS > asset.StartMS {
			add("youtube_segment", fmt.Sprintf("%s:%d:%d", asset.YouTubeVideoID, asset.StartMS, asset.EndMS), asset.AssetID)
		}
	}
	assetSet := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		assetSet[asset.AssetID] = struct{}{}
	}
	for _, source := range sources {
		if _, ok := assetSet[source.AssetID]; ok {
			add("source_identity", source.SourceType+":"+source.SourceURI, source.AssetID)
		}
	}
	// A candidate has one materialization row by schema design. Its presence
	// proves membership, but repeating the same candidate is not possible and
	// is therefore not duplicate evidence. Candidate links are already used
	// during membership selection above.

	uf := newMattDamonUnionFind()
	for _, asset := range assets {
		uf.add(asset.AssetID)
	}
	for _, key := range keys {
		if len(key.AssetIDs) < 2 {
			continue
		}
		for i := 1; i < len(key.AssetIDs); i++ {
			uf.union(key.AssetIDs[0], key.AssetIDs[i])
		}
	}

	components := make(map[string]*mattDamonDuplicateGroup)
	for _, key := range keys {
		if len(key.AssetIDs) < 2 {
			continue
		}
		root := uf.find(key.AssetIDs[0])
		group := components[root]
		if group == nil {
			group = &mattDamonDuplicateGroup{}
			components[root] = group
		}
		for _, assetID := range key.AssetIDs {
			group.AssetIDs = appendUniqueString(group.AssetIDs, assetID)
		}
		group.Evidence = appendUniqueString(group.Evidence, key.KeyType+"="+key.Key)
	}
	out := make([]mattDamonDuplicateGroup, 0, len(components))
	for _, group := range components {
		sort.Strings(group.AssetIDs)
		sort.Strings(group.Evidence)
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.Join(out[i].AssetIDs, "\x00") < strings.Join(out[j].AssetIDs, "\x00")
	})
	return out
}

func buildMattDamonContentGroups(assets []mattDamonAssetRecord) []mattDamonContentGroup {
	groups := make(map[string]*mattDamonContentGroup)
	for _, asset := range assets {
		for hashType, hash := range map[string]string{"content_sha256": asset.ContentSHA256, "binary_sha256": asset.BinarySHA256} {
			hash = strings.TrimSpace(hash)
			if hash == "" {
				continue
			}
			key := hashType + "\x00" + hash
			group := groups[key]
			if group == nil {
				group = &mattDamonContentGroup{HashType: hashType, Hash: hash}
				groups[key] = group
			}
			group.AssetIDs = appendUniqueString(group.AssetIDs, asset.AssetID)
		}
	}
	out := make([]mattDamonContentGroup, 0)
	for _, group := range groups {
		if len(group.AssetIDs) < 2 {
			continue
		}
		sort.Strings(group.AssetIDs)
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HashType != out[j].HashType {
			return out[i].HashType < out[j].HashType
		}
		return out[i].Hash < out[j].Hash
	})
	return out
}

func printMattDamonAuditReport(report mattDamonAuditReport) {
	fmt.Println("=== Matt Damon structured asset audit (read-only) ===")
	fmt.Printf("  entity:       %s\n", report.Entity)
	fmt.Printf("  assets:       %d (expected %d; match=%v)\n", report.AssetCount, report.ExpectedAssetCount, report.CountMatches)
	fmt.Printf("  duplicates:   %d logical group(s)\n", len(report.DuplicateGroups))
	fmt.Printf("  shared bytes: %d group(s)\n", len(report.SharedContentGroups))
	if len(report.UnresolvedAssetIDs) > 0 {
		fmt.Printf("  unresolved:   %d catalog link(s)\n", len(report.UnresolvedAssetIDs))
	}
	if len(report.DuplicateGroups) > 0 {
		fmt.Println("  duplicate groups require explicit operator review; no rows were modified")
	}
}
