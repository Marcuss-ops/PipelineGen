package editorialcomposition

import (
	"bytes"
	"strings"
	"testing"
)

const testCatalogJSON = `{
  "schema_version": "motion_capabilities.v1",
  "motions": [
    {"motion_id": "motion_chain", "target": "phrase", "supports_chain": true, "supports_shared_canvas": true, "supports_persistent_hold": true, "max_hold_us": 5000000},
    {"motion_id": "motion_chain_short_hold", "target": "phrase", "supports_chain": true, "supports_shared_canvas": true, "supports_persistent_hold": false},
    {"motion_id": "motion_solo", "target": "phrase", "supports_chain": false, "supports_shared_canvas": false, "supports_persistent_hold": false},
    {"motion_id": "motion_date", "target": "date", "supports_chain": false, "supports_shared_canvas": true, "supports_persistent_hold": true},
    {"motion_id": "motion_image", "target": "image", "supports_chain": true, "supports_shared_canvas": true, "supports_persistent_hold": true}
  ]
}`

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	catalog, err := ParseCatalog([]byte(testCatalogJSON))
	if err != nil {
		t.Fatalf("parse test catalog: %v", err)
	}
	return catalog
}

func testComposition(members ...Member) *Composition {
	return &Composition{
		SchemaVersion: SchemaVersionV1,
		CompositionID: "comp_test",
		SceneID:       "scene_test",
		Window:        Window{StartUS: 0, DurationUS: 6000000},
		Members:       members,
	}
}

func chainablePhrase() *PhraseMember {
	return &PhraseMember{
		MemberBase: MemberBase{
			MemberID: "phrase_a", Kind: KindPhrase, DurationUS: 2000000,
			MotionID: "motion_chain",
			Link:     &Link{GroupID: "g", Order: 1, Hold: HoldHandoff},
		},
		Text: "una frase", Lane: LaneShort,
	}
}

// TestChainAdmissionIsDeclaredNotPrefixed pins the replacement for the
// `short_phrase_` prefix rule: two motions with the same prefix get opposite
// verdicts because their catalog capabilities differ.
func TestChainAdmissionIsDeclaredNotPrefixed(t *testing.T) {
	catalog := testCatalog(t)
	if !catalog.ChainCapable("motion_chain") {
		t.Fatal("motion_chain must be chain capable")
	}
	if catalog.ChainCapable("motion_solo") {
		t.Fatal("motion_solo must not be chain capable")
	}
	if catalog.ChainCapable("unknown_motion") {
		t.Fatal("an unknown motion must fail closed")
	}

	linked := testComposition(chainablePhrase())
	if err := linked.Validate(catalog); err != nil {
		t.Fatalf("declared capability was rejected: %v", err)
	}

	solo := testComposition(chainablePhrase())
	solo.Members[0].Base().MotionID = "motion_solo"
	err := solo.Validate(catalog)
	if CodeOf(err) != CodeMotionNotChainCapable {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeMotionNotChainCapable, err)
	}
}

func TestHoldCapabilityAndCap(t *testing.T) {
	catalog := testCatalog(t)
	persisted := chainablePhrase()
	persisted.Link.Hold = HoldPersist
	if err := testComposition(persisted).Validate(catalog); err != nil {
		t.Fatalf("persist hold rejected: %v", err)
	}

	tooLong := chainablePhrase()
	tooLong.Link.Hold = HoldPersist
	tooLong.DurationUS = 6000000
	err := testComposition(tooLong).Validate(catalog)
	if CodeOf(err) != CodeMotionNotHoldCapable {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeMotionNotHoldCapable, err)
	}

	notHoldable := chainablePhrase()
	notHoldable.MotionID = "motion_chain_short_hold"
	notHoldable.Link.Hold = HoldPersist
	err = testComposition(notHoldable).Validate(catalog)
	if CodeOf(err) != CodeMotionNotHoldCapable {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeMotionNotHoldCapable, err)
	}
}

func TestLowerIsDeterministicAndOrderStable(t *testing.T) {
	catalog := testCatalog(t)
	late := chainablePhrase()
	late.MemberID = "phrase_late"
	late.StartUS = 3000000
	late.Link.Order = 2
	early := chainablePhrase()
	early.MemberID = "phrase_early"
	composition := testComposition(late, early)

	first, err := composition.Lower(catalog)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	second, err := composition.Lower(catalog)
	if err != nil {
		t.Fatalf("lower again: %v", err)
	}
	firstBytes, err := MarshalLayers(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := MarshalLayers(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("lowering is not deterministic")
	}
	if first.Layers[0].LayerID != "phrase_early" || first.Layers[1].LayerID != "phrase_late" {
		t.Fatalf("layers not ordered by time: %v", []string{first.Layers[0].LayerID, first.Layers[1].LayerID})
	}
	if composition.Members[0].Base().MemberID != "phrase_late" {
		t.Fatal("lowering mutated the composition member order")
	}
}

func TestDecodeRejectsUnknownMemberField(t *testing.T) {
	raw := []byte(`{
      "schema_version": "editorial_composition.v1",
      "composition_id": "comp", "scene_id": "scene",
      "window": {"start_us": 0, "duration_us": 1000000},
      "members": [{"member_id":"p","kind":"phrase","start_us":0,"duration_us":100000,"text":"x","lane":"short","future":true}]
    }`)
	_, err := Decode(raw)
	if CodeOf(err) != CodeUnknownField {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeUnknownField, err)
	}
	if !strings.Contains(err.Error(), "future") {
		t.Fatalf("error does not name the rejected field: %v", err)
	}
}

func TestDecodeRejectsUnknownMemberKind(t *testing.T) {
	raw := []byte(`{
      "schema_version": "editorial_composition.v1",
      "composition_id": "comp", "scene_id": "scene",
      "window": {"start_us": 0, "duration_us": 1000000},
      "members": [{"member_id":"x","kind":"hologram","start_us":0,"duration_us":100000}]
    }`)
	_, err := Decode(raw)
	if CodeOf(err) != CodeUnknownMemberKind {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeUnknownMemberKind, err)
	}
}

func TestChapterModes(t *testing.T) {
	catalog := testCatalog(t)
	chapter := func(mode, grounding string, titles ...ChapterTitle) *Chapter {
		return &Chapter{Mode: mode, StyleFamily: StyleDocumentary, DurationMS: 3500, Grounding: grounding, Titles: titles}
	}
	titled := ChapterTitle{SceneID: "scene_test", Title: "La storia", ChapterNumber: 1}

	cases := []struct {
		name    string
		chapter *Chapter
		members []Member
		code    string
	}{
		{name: "off", chapter: chapter(ChapterOff, ""), code: ""},
		{name: "off with titles", chapter: chapter(ChapterOff, GroundingAuthored, titled), code: CodeChapterTitlesUnexpected},
		{name: "auto transcript", chapter: chapter(ChapterAuto, GroundingTranscript), code: ""},
		{name: "auto without transcript grounding", chapter: chapter(ChapterAuto, GroundingAuthored, titled), code: CodeChapterGroundingInvalid},
		{name: "explicit authored", chapter: chapter(ChapterExplicit, GroundingAuthored, titled), code: ""},
		{name: "explicit without titles", chapter: chapter(ChapterExplicit, GroundingAuthored), code: CodeChapterTitlesMissing},
		{name: "chapter_only empty", chapter: chapter(ChapterChapterOnly, GroundingAuthored, titled), code: ""},
		{
			name:    "chapter_only with members",
			chapter: chapter(ChapterChapterOnly, GroundingAuthored, titled),
			members: []Member{chainablePhrase()},
			code:    CodeChapterOnlyHasMembers,
		},
		{name: "unknown mode", chapter: chapter("slideshow", GroundingTranscript), code: CodeChapterModeInvalid},
		{name: "zero duration", chapter: &Chapter{Mode: ChapterAuto, Grounding: GroundingTranscript}, code: CodeChapterDurationInvalid},
		{name: "duplicate chapter numbers", chapter: chapter(ChapterExplicit, GroundingAuthored, titled, titled), code: CodeChapterTitleInvalid},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			composition := testComposition(testCase.members...)
			composition.Chapter = testCase.chapter
			err := composition.Validate(catalog)
			if CodeOf(err) != testCase.code {
				t.Fatalf("code = %q, want %q (%v)", CodeOf(err), testCase.code, err)
			}
		})
	}
}

func TestBackgroundInheritance(t *testing.T) {
	catalog := testCatalog(t)
	persistent := &EntityMember{
		MemberBase: MemberBase{
			MemberID: "image_a", Kind: KindEntity, DurationUS: 6000000,
			MotionID: "motion_image", Persistent: true,
		},
		EntityID: "entity_a",
	}
	calloutless := func(background *Background) *Composition {
		composition := testComposition(persistent)
		composition.Background = background
		return composition
	}

	if err := calloutless(&Background{Mode: BackgroundInherit, SourceMemberID: "image_a"}).Validate(catalog); err != nil {
		t.Fatalf("inheritance rejected: %v", err)
	}

	notPersistent := testComposition(&EntityMember{
		MemberBase: MemberBase{MemberID: "image_b", Kind: KindEntity, DurationUS: 6000000, MotionID: "motion_image"},
		EntityID:   "entity_b",
	})
	notPersistent.Background = &Background{Mode: BackgroundInherit, SourceMemberID: "image_b"}
	if err := notPersistent.Validate(catalog); CodeOf(err) != CodeBackgroundSourceNotPersist {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeBackgroundSourceNotPersist, err)
	}

	snapshotWithoutDigest := testComposition()
	snapshotWithoutDigest.Background = &Background{
		Mode:     BackgroundSnapshot,
		Snapshot: &SnapshotRef{AssetID: "asset", SHA256: "not-a-digest"},
	}
	if err := snapshotWithoutDigest.Validate(catalog); CodeOf(err) != CodeSnapshotNotContentAddressed {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeSnapshotNotContentAddressed, err)
	}

	mixed := calloutless(&Background{Mode: BackgroundInherit, SourceMemberID: "image_a", Snapshot: &SnapshotRef{AssetID: "a", SHA256: strings.Repeat("a", 64)}})
	mixed.Window = Window{DurationUS: 6000000}
	if err := mixed.Validate(catalog); CodeOf(err) != CodeBackgroundSourceUnexpected {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeBackgroundSourceUnexpected, err)
	}
}

func TestDateAttachmentExcludesStandaloneDate(t *testing.T) {
	catalog := testCatalog(t)
	entity := &EntityMember{
		MemberBase: MemberBase{MemberID: "entity_a", Kind: KindEntity, DurationUS: 6000000},
		EntityID:   "entity_a",
		Attachments: []Attachment{
			{AttachmentID: "date_a", Role: RoleDate, Value: "2025", MotionID: "motion_date"},
		},
	}
	composition := testComposition(entity)
	if err := composition.Validate(catalog); err != nil {
		t.Fatalf("date attachment rejected: %v", err)
	}

	composition = testComposition(entity, &DateMember{
		MemberBase: MemberBase{MemberID: "date_standalone", Kind: KindDate, DurationUS: 1000000},
		Value:      "2025",
	})
	if err := composition.Validate(catalog); CodeOf(err) != CodeDuplicateDateOverlay {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeDuplicateDateOverlay, err)
	}

	attachmentWithWrongMotion := &EntityMember{
		MemberBase: MemberBase{MemberID: "entity_b", Kind: KindEntity, DurationUS: 6000000},
		EntityID:   "entity_b",
		Attachments: []Attachment{
			{AttachmentID: "date_b", Role: RoleDate, Value: "2025", MotionID: "motion_chain"},
		},
	}
	if err := testComposition(attachmentWithWrongMotion).Validate(catalog); CodeOf(err) != CodeMotionTargetMismatch {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeMotionTargetMismatch, err)
	}
}

func TestChartPointsKeepTheirBinding(t *testing.T) {
	catalog := testCatalog(t)
	value := int64(56000000)
	chart := &ChartMember{
		MemberBase: MemberBase{MemberID: "chart_a", Kind: KindChart, DurationUS: 6000000},
		Subject:    "Giappone",
		Metric:     "population",
		Unit:       "persons",
		Layout:     "animated_bar_comparison",
		Points:     []ChartPoint{{Period: "2005", Value: &value}},
	}
	if err := testComposition(chart).Validate(catalog); err != nil {
		t.Fatalf("chart rejected: %v", err)
	}
	chart.Points = append(chart.Points, ChartPoint{Period: "2005", Value: &value})
	if err := testComposition(chart).Validate(catalog); CodeOf(err) != CodeChartDuplicatePeriod {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeChartDuplicatePeriod, err)
	}
	chart.Points = []ChartPoint{{Period: "2005"}}
	if err := testComposition(chart).Validate(catalog); CodeOf(err) != CodeChartPointInvalid {
		t.Fatalf("a point without a value must fail closed, got %v", err)
	}
}

func TestMapOwnsItsCanvas(t *testing.T) {
	catalog := testCatalog(t)
	mapMember := &MapMember{
		MemberBase: MemberBase{MemberID: "map_a", Kind: KindMap, DurationUS: 6000000},
		Pins:       []MapPin{{PinID: "milano", Latitude: 45.46, Longitude: 9.19}},
	}
	if err := testComposition(mapMember).Validate(catalog); err != nil {
		t.Fatalf("map rejected: %v", err)
	}
	mapMember.Link = &Link{GroupID: "g", Order: 1, Hold: HoldHandoff}
	if err := testComposition(mapMember).Validate(catalog); CodeOf(err) != CodeMapChainForbidden {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeMapChainForbidden, err)
	}
}

func TestParseCatalogRejectsDuplicates(t *testing.T) {
	_, err := ParseCatalog([]byte(`{
      "schema_version": "motion_capabilities.v1",
      "motions": [
        {"motion_id": "dup", "target": "phrase"},
        {"motion_id": "dup", "target": "phrase"}
      ]
    }`))
	if CodeOf(err) != CodeDuplicateMotion {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeDuplicateMotion, err)
	}
}

func TestValidateRequiresCatalog(t *testing.T) {
	if err := testComposition().Validate(nil); CodeOf(err) != CodeCatalogMissing {
		t.Fatalf("code = %q, want %q (%v)", CodeOf(err), CodeCatalogMissing, err)
	}
}
