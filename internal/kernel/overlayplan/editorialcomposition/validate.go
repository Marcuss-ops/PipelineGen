package editorialcomposition

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	yearPattern  = regexp.MustCompile(`^\d{3,4}$`)
	isoPattern   = regexp.MustCompile(`^\d{4}-\d{2}(-\d{2})?$`)
	hex64Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Validate runs every contract rule and returns the first rejection. The
// catalog is required: a composition whose motions cannot be resolved is not
// admissible, so a missing catalog fails closed instead of skipping the check.
func (c *Composition) Validate(catalog *Catalog) error {
	if c == nil {
		return &Error{Code: CodeMissingField, Path: "composition"}
	}
	if catalog == nil {
		return &Error{Code: CodeCatalogMissing, Path: "capabilities"}
	}
	v := &validator{
		composition:   c,
		catalog:       catalog,
		memberIDs:     make(map[string]struct{}, len(c.Members)),
		memberKinds:   make(map[string]string, len(c.Members)),
		linkOrders:    make(map[string]map[int]string),
		persistentIDs: make(map[string]struct{}),
	}
	if err := v.validateComposition(); err != nil {
		return err
	}
	if err := v.validateMembers(); err != nil {
		return err
	}
	if err := v.validateBackground(); err != nil {
		return err
	}
	if err := v.validateCrossMemberRules(); err != nil {
		return err
	}
	return v.validateChapter()
}

type validator struct {
	composition   *Composition
	catalog       *Catalog
	memberIDs     map[string]struct{}
	memberKinds   map[string]string
	linkOrders    map[string]map[int]string
	persistentIDs map[string]struct{}
	dateMembers   int
	dateAttached  bool
}

func (v *validator) fail(code, path, detail string) error {
	return &Error{Code: code, Path: path, Detail: detail}
}

func (v *validator) validateComposition() error {
	c := v.composition
	if c.SchemaVersion != SchemaVersionV1 {
		return v.fail(CodeUnsupportedSchema, "schema_version", c.SchemaVersion)
	}
	if strings.TrimSpace(c.CompositionID) == "" {
		return v.fail(CodeMissingField, "composition_id", "")
	}
	if strings.TrimSpace(c.SceneID) == "" {
		return v.fail(CodeMissingField, "scene_id", "")
	}
	if c.Window.StartUS < 0 || c.Window.DurationUS <= 0 {
		return v.fail(CodeInvalidWindow, "window", fmt.Sprintf("start=%d duration=%d", c.Window.StartUS, c.Window.DurationUS))
	}
	return nil
}

func (v *validator) validateMembers() error {
	c := v.composition
	for index, member := range c.Members {
		path := fmt.Sprintf("members[%d]", index)
		base := member.Base()
		if base == nil {
			return v.fail(CodeUnknownMemberKind, path, "")
		}
		id := strings.TrimSpace(base.MemberID)
		if id == "" {
			return v.fail(CodeMissingField, path+".member_id", "")
		}
		if _, duplicate := v.memberIDs[id]; duplicate {
			return v.fail(CodeDuplicateMember, path+".member_id", id)
		}
		v.memberIDs[id] = struct{}{}
		v.memberKinds[id] = base.Kind
		if base.DurationUS <= 0 {
			return v.fail(CodeInvalidWindow, path+".duration_us", fmt.Sprintf("%d", base.DurationUS))
		}
		if base.StartUS < c.Window.StartUS || base.StartUS+base.DurationUS > c.Window.EndUS() {
			return v.fail(CodeMemberOutsideWindow, path,
				fmt.Sprintf("[%d,%d) outside [%d,%d)", base.StartUS, base.StartUS+base.DurationUS, c.Window.StartUS, c.Window.EndUS()))
		}
		if base.Persistent {
			v.persistentIDs[id] = struct{}{}
		}
		if err := v.validateLink(base, path); err != nil {
			return err
		}
		if base.Link != nil && base.Kind == KindMap {
			return v.fail(CodeMapChainForbidden, path+".link", "a map owns the full georeferenced canvas")
		}
		if err := v.validateMotion(base.MotionID, base.Link, base.DurationUS, path); err != nil {
			return err
		}
		switch typed := member.(type) {
		case *PhraseMember:
			if err := v.validatePhrase(typed, path); err != nil {
				return err
			}
		case *EntityMember:
			if err := v.validateEntity(typed, path); err != nil {
				return err
			}
		case *ChartMember:
			if err := v.validateChart(typed, path); err != nil {
				return err
			}
		case *MapMember:
			if err := v.validateMap(typed, path); err != nil {
				return err
			}
		case *SummaryMember:
			if err := v.validateSummary(typed, path); err != nil {
				return err
			}
		case *DateMember:
			v.dateMembers++
			if err := v.validateDateMember(typed, path); err != nil {
				return err
			}
		default:
			return v.fail(CodeUnknownMemberKind, path+".kind", base.Kind)
		}
	}
	return nil
}

func (v *validator) validateLink(base *MemberBase, path string) error {
	link := base.Link
	if link == nil {
		return nil
	}
	groupID := strings.TrimSpace(link.GroupID)
	if groupID == "" {
		return v.fail(CodeMissingField, path+".link.group_id", "")
	}
	if link.Order < 1 {
		return v.fail(CodeMissingField, path+".link.order", fmt.Sprintf("%d", link.Order))
	}
	switch link.Hold {
	case HoldHandoff, HoldPersist:
	default:
		return v.fail(CodeMissingField, path+".link.hold", link.Hold)
	}
	orders, ok := v.linkOrders[groupID]
	if !ok {
		orders = make(map[int]string, 2)
		v.linkOrders[groupID] = orders
	}
	if other, duplicate := orders[link.Order]; duplicate {
		return v.fail(CodeLinkOrderDuplicate, path+".link.order", other)
	}
	orders[link.Order] = base.MemberID
	return nil
}

func (v *validator) validateMotion(motionID string, link *Link, holdUS int64, path string) error {
	motionID = strings.TrimSpace(motionID)
	if motionID == "" {
		if link != nil {
			return v.fail(CodeMissingField, path+".motion_id", "")
		}
		return nil
	}
	capability, ok := v.catalog.Lookup(motionID)
	if !ok {
		return v.fail(CodeMotionNotInCatalog, path+".motion_id", motionID)
	}
	if link != nil {
		if !capability.SupportsChain || !capability.SupportsSharedCanvas {
			return v.fail(CodeMotionNotChainCapable, path+".motion_id", motionID)
		}
		if link.Hold == HoldPersist && !v.catalog.HoldCapable(motionID, holdUS) {
			return v.fail(CodeMotionNotHoldCapable, path+".link.hold", fmt.Sprintf("%s/%dus", motionID, holdUS))
		}
	}
	return nil
}

func (v *validator) validatePhrase(member *PhraseMember, path string) error {
	if strings.TrimSpace(member.Text) == "" {
		return v.fail(CodeMissingField, path+".text", "")
	}
	switch member.Lane {
	case LaneShort, LaneLong:
	default:
		return v.fail(CodeLaneInvalid, path+".lane", member.Lane)
	}
	if member.MotionID != "" {
		capability, ok := v.catalog.Lookup(member.MotionID)
		if !ok {
			return v.fail(CodeMotionNotInCatalog, path+".motion_id", member.MotionID)
		}
		if capability.Target != TargetPhrase {
			return v.fail(CodeMotionTargetMismatch, path+".motion_id", fmt.Sprintf("%s/%s", member.MotionID, capability.Target))
		}
	}
	return nil
}

func (v *validator) validateEntity(member *EntityMember, path string) error {
	if strings.TrimSpace(member.EntityID) == "" {
		return v.fail(CodeMissingField, path+".entity_id", "")
	}
	if member.Asset != nil {
		if strings.TrimSpace(member.Asset.AssetID) == "" {
			return v.fail(CodeMissingField, path+".asset.asset_id", "")
		}
		if member.Asset.SHA256 != "" && !hex64Pattern.MatchString(member.Asset.SHA256) {
			return v.fail(CodeAssetDigestInvalid, path+".asset.sha256", member.Asset.SHA256)
		}
	}
	seenAttachments := make(map[string]struct{}, len(member.Attachments))
	for index, attachment := range member.Attachments {
		attachmentPath := fmt.Sprintf("%s.attachments[%d]", path, index)
		if id := strings.TrimSpace(attachment.AttachmentID); id != "" {
			if _, duplicate := seenAttachments[id]; duplicate {
				return v.fail(CodeAttachmentDuplicate, attachmentPath+".attachment_id", id)
			}
			seenAttachments[id] = struct{}{}
		}
		if err := v.validateAttachment(attachment, attachmentPath); err != nil {
			return err
		}
	}
	return nil
}

func (v *validator) validateAttachment(attachment Attachment, path string) error {
	switch attachment.Role {
	case RoleDate:
		if !isDateValue(attachment.Value) {
			return v.fail(CodeAttachmentValueInvalid, path+".value", attachment.Value)
		}
		v.dateAttached = true
	case RoleMetric:
		if strings.TrimSpace(attachment.Value) == "" {
			return v.fail(CodeMissingField, path+".value", "")
		}
		if strings.TrimSpace(attachment.Unit) == "" {
			return v.fail(CodeMissingField, path+".unit", "")
		}
	case RoleCaption:
		if strings.TrimSpace(attachment.Value) == "" {
			return v.fail(CodeMissingField, path+".value", "")
		}
	default:
		return v.fail(CodeAttachmentRoleUnsupported, path+".role", attachment.Role)
	}
	if attachment.Position != "" && !validPosition(attachment.Position) {
		return v.fail(CodeAttachmentPositionInvalid, path+".position", attachment.Position)
	}
	if attachment.Evidence != nil && strings.TrimSpace(attachment.Evidence.Text) == "" {
		return v.fail(CodeAttachmentEvidenceInvalid, path+".evidence.text", "")
	}
	if motionID := strings.TrimSpace(attachment.MotionID); motionID != "" {
		capability, ok := v.catalog.Lookup(motionID)
		if !ok {
			return v.fail(CodeMotionNotInCatalog, path+".motion_id", motionID)
		}
		if capability.Target != attachment.Role {
			return v.fail(CodeMotionTargetMismatch, path+".motion_id", fmt.Sprintf("%s/%s", motionID, capability.Target))
		}
	}
	return nil
}

func (v *validator) validateChart(member *ChartMember, path string) error {
	if strings.TrimSpace(member.Subject) == "" {
		return v.fail(CodeMissingField, path+".subject", "")
	}
	if strings.TrimSpace(member.Metric) == "" {
		return v.fail(CodeMissingField, path+".metric", "")
	}
	unit := strings.TrimSpace(member.Unit)
	if unit == "" {
		return v.fail(CodeChartUnitMissing, path+".unit", "a chart keeps its unit or it is not a chart")
	}
	if _, ok := certifiedChartLayouts[member.Layout]; !ok {
		return v.fail(CodeChartLayoutUnsupported, path+".layout", member.Layout)
	}
	if len(member.Series) > 0 {
		if len(member.Points) > 0 {
			return v.fail(CodeChartSeriesInvalid, path+".points", "a multi-series chart declares its points inside series")
		}
		seen := make(map[string]struct{}, len(member.Series))
		for index, series := range member.Series {
			seriesPath := fmt.Sprintf("%s.series[%d]", path, index)
			id := strings.TrimSpace(series.SeriesID)
			if id == "" {
				return v.fail(CodeChartSeriesInvalid, seriesPath+".series_id", "")
			}
			if _, duplicate := seen[id]; duplicate {
				return v.fail(CodeChartSeriesInvalid, seriesPath+".series_id", id)
			}
			seen[id] = struct{}{}
			if err := v.validatePoints(series.Points, unit, seriesPath+".points"); err != nil {
				return err
			}
		}
		return nil
	}
	if len(member.Points) == 0 {
		return v.fail(CodeChartPointsMissing, path+".points", "")
	}
	return v.validatePoints(member.Points, unit, path+".points")
}

func (v *validator) validatePoints(points []ChartPoint, unit, path string) error {
	if len(points) == 0 {
		return v.fail(CodeChartPointsMissing, path, "")
	}
	seen := make(map[string]struct{}, len(points))
	for index, point := range points {
		pointPath := fmt.Sprintf("%s[%d]", path, index)
		period := strings.TrimSpace(point.Period)
		if period == "" {
			return v.fail(CodeChartPointInvalid, pointPath+".period", "")
		}
		if point.Value == nil {
			return v.fail(CodeChartPointInvalid, pointPath+".value", "missing value")
		}
		if pointUnit := strings.TrimSpace(point.Unit); pointUnit != "" && pointUnit != unit {
			return v.fail(CodeChartPointInvalid, pointPath+".unit", fmt.Sprintf("%s != %s", pointUnit, unit))
		}
		if _, duplicate := seen[period]; duplicate {
			return v.fail(CodeChartDuplicatePeriod, pointPath+".period", period)
		}
		seen[period] = struct{}{}
	}
	return nil
}

func (v *validator) validateMap(member *MapMember, path string) error {
	if len(member.Pins) == 0 {
		return v.fail(CodeMapPinsMissing, path+".pins", "")
	}
	if member.MotionID != "" {
		capability, ok := v.catalog.Lookup(member.MotionID)
		if !ok {
			return v.fail(CodeMotionNotInCatalog, path+".motion_id", member.MotionID)
		}
		if capability.Target != TargetMap {
			return v.fail(CodeMotionTargetMismatch, path+".motion_id", fmt.Sprintf("%s/%s", member.MotionID, capability.Target))
		}
	}
	seen := make(map[string]struct{}, len(member.Pins))
	for index, pin := range member.Pins {
		pinPath := fmt.Sprintf("%s.pins[%d]", path, index)
		id := strings.TrimSpace(pin.PinID)
		if id == "" {
			return v.fail(CodeMapPinInvalid, pinPath+".pin_id", "")
		}
		if _, duplicate := seen[id]; duplicate {
			return v.fail(CodeMapPinDuplicate, pinPath+".pin_id", id)
		}
		seen[id] = struct{}{}
		if pin.Latitude < -90 || pin.Latitude > 90 || pin.Longitude < -180 || pin.Longitude > 180 {
			return v.fail(CodeMapPinInvalid, pinPath, fmt.Sprintf("lat=%v lon=%v", pin.Latitude, pin.Longitude))
		}
		if pin.Callout != nil {
			if err := v.validateCallout(pin.Callout, pinPath+".callout"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *validator) validateCallout(callout *MapCallout, path string) error {
	if callout.Kind != CalloutPhoto {
		return v.fail(CodeMapCalloutInvalid, path+".kind", callout.Kind)
	}
	if callout.Asset == nil || strings.TrimSpace(callout.Asset.AssetID) == "" {
		return v.fail(CodeMapCalloutAssetUnverified, path+".asset", "")
	}
	if !hex64Pattern.MatchString(callout.Asset.SHA256) {
		return v.fail(CodeMapCalloutAssetUnverified, path+".asset.sha256", callout.Asset.SHA256)
	}
	switch callout.Connector {
	case "", ConnectorLeader, ConnectorNone:
	default:
		return v.fail(CodeCalloutConnectorUnsupported, path+".connector", callout.Connector)
	}
	switch callout.Placement {
	case "", "auto", "left", "right", "above", "below":
	default:
		return v.fail(CodeCalloutPlacementUnsupported, path+".placement", callout.Placement)
	}
	choreography := strings.TrimSpace(callout.Choreography)
	if choreography == "" {
		return v.fail(CodeMissingField, path+".choreography", "")
	}
	capability, ok := v.catalog.Lookup(choreography)
	if !ok || capability.Target != TargetMapCallout {
		return v.fail(CodeCalloutChoreographyUnknown, path+".choreography", choreography)
	}
	if callout.Evidence != nil && strings.TrimSpace(callout.Evidence.Text) == "" {
		return v.fail(CodeAttachmentEvidenceInvalid, path+".evidence.text", "")
	}
	return nil
}

func (v *validator) validateSummary(member *SummaryMember, path string) error {
	switch member.Reveal {
	case RevealProgressive, RevealReplace:
	default:
		return v.fail(CodeSummaryRevealInvalid, path+".reveal", member.Reveal)
	}
	if len(member.Points) < 2 {
		return v.fail(CodeSummaryPointsInsufficient, path+".points", fmt.Sprintf("%d", len(member.Points)))
	}
	seen := make(map[string]struct{}, len(member.Points))
	var previousEnd int64
	for index, point := range member.Points {
		pointPath := fmt.Sprintf("%s.points[%d]", path, index)
		text := strings.TrimSpace(point.Text)
		if text == "" {
			return v.fail(CodeMissingField, pointPath+".text", "")
		}
		if point.DurationUS <= 0 {
			return v.fail(CodeInvalidWindow, pointPath+".duration_us", fmt.Sprintf("%d", point.DurationUS))
		}
		if point.StartUS < v.composition.Window.StartUS || point.StartUS+point.DurationUS > v.composition.Window.EndUS() {
			return v.fail(CodeMemberOutsideWindow, pointPath, "")
		}
		key := normalizeText(text)
		if _, duplicate := seen[key]; duplicate {
			return v.fail(CodeSummaryDuplicateText, pointPath+".text", text)
		}
		seen[key] = struct{}{}
		if member.Reveal == RevealProgressive && index > 0 && point.StartUS < previousEnd {
			return v.fail(CodeSummaryOverlap, pointPath+".start_us", fmt.Sprintf("%d < %d", point.StartUS, previousEnd))
		}
		previousEnd = point.StartUS + point.DurationUS
	}
	return nil
}

func (v *validator) validateDateMember(member *DateMember, path string) error {
	if !isDateValue(member.Value) {
		return v.fail(CodeAttachmentValueInvalid, path+".value", member.Value)
	}
	if member.Position != "" && !validPosition(member.Position) {
		return v.fail(CodeAttachmentPositionInvalid, path+".position", member.Position)
	}
	if member.MotionID != "" {
		capability, ok := v.catalog.Lookup(member.MotionID)
		if !ok {
			return v.fail(CodeMotionNotInCatalog, path+".motion_id", member.MotionID)
		}
		if capability.Target != TargetDate {
			return v.fail(CodeMotionTargetMismatch, path+".motion_id", fmt.Sprintf("%s/%s", member.MotionID, capability.Target))
		}
	}
	return nil
}

func (v *validator) validateBackground() error {
	background := v.composition.Background
	if background == nil {
		return nil
	}
	switch background.Mode {
	case BackgroundIndependent:
		if background.SourceMemberID != "" || background.Snapshot != nil {
			return v.fail(CodeBackgroundSourceUnexpected, "background", background.Mode)
		}
	case BackgroundInherit:
		sourceID := strings.TrimSpace(background.SourceMemberID)
		if sourceID == "" {
			return v.fail(CodeBackgroundSourceMissing, "background.source_member_id", "")
		}
		if _, ok := v.memberIDs[sourceID]; !ok {
			return v.fail(CodeBackgroundSourceMissing, "background.source_member_id", sourceID)
		}
		if _, ok := v.persistentIDs[sourceID]; !ok {
			return v.fail(CodeBackgroundSourceNotPersist, "background.source_member_id", sourceID)
		}
		if v.memberKinds[sourceID] != KindEntity {
			return v.fail(CodeBackgroundSourceNotPersist, "background.source_member_id",
				fmt.Sprintf("%s is a %s", sourceID, v.memberKinds[sourceID]))
		}
		if background.Snapshot != nil {
			return v.fail(CodeBackgroundSourceUnexpected, "background.snapshot", background.Mode)
		}
	case BackgroundSnapshot:
		if background.SourceMemberID != "" {
			return v.fail(CodeBackgroundSourceUnexpected, "background.source_member_id", background.Mode)
		}
		if background.Snapshot == nil ||
			strings.TrimSpace(background.Snapshot.AssetID) == "" ||
			!hex64Pattern.MatchString(background.Snapshot.SHA256) {
			return v.fail(CodeSnapshotNotContentAddressed, "background.snapshot", "")
		}
	default:
		return v.fail(CodeBackgroundModeInvalid, "background.mode", background.Mode)
	}
	return nil
}

func (v *validator) validateCrossMemberRules() error {
	if v.dateAttached && v.dateMembers > 0 {
		return v.fail(CodeDuplicateDateOverlay, "members", "a date is attached and also standalone")
	}
	return nil
}

// ValidateChapterPolicy applies the chapter rules of `editorial_composition.v1`
// to a standalone policy, i.e. the producer directive before any member exists.
// It is the one chapter validator: the composition boundary and the generation
// ingress call the same function, so a payload cannot be admitted by one and
// rejected by the other.
func ValidateChapterPolicy(chapter *Chapter) error {
	if chapter == nil {
		return nil
	}
	v := &validator{composition: &Composition{Chapter: chapter}}
	return v.validateChapter()
}

func (v *validator) validateChapter() error {
	chapter := v.composition.Chapter
	if chapter == nil {
		return nil
	}
	switch chapter.StyleFamily {
	case "", StyleDocumentary, StyleEditorialMinimal:
	default:
		return v.fail(CodeChapterStyleUnsupported, "chapter.style_family", chapter.StyleFamily)
	}
	switch chapter.Mode {
	case ChapterOff:
		if len(chapter.Titles) > 0 {
			return v.fail(CodeChapterTitlesUnexpected, "chapter.titles", chapter.Mode)
		}
		return nil
	case ChapterAuto:
		if chapter.Grounding != GroundingTranscript {
			return v.fail(CodeChapterGroundingInvalid, "chapter.grounding", chapter.Grounding)
		}
	case ChapterExplicit:
		if chapter.Grounding != GroundingAuthored {
			return v.fail(CodeChapterGroundingInvalid, "chapter.grounding", chapter.Grounding)
		}
	case ChapterChapterOnly:
		if len(v.composition.Members) > 0 {
			return v.fail(CodeChapterOnlyHasMembers, "members", fmt.Sprintf("%d members", len(v.composition.Members)))
		}
		if chapter.Grounding != GroundingAuthored && chapter.Grounding != GroundingTranscript {
			return v.fail(CodeChapterGroundingInvalid, "chapter.grounding", chapter.Grounding)
		}
	default:
		return v.fail(CodeChapterModeInvalid, "chapter.mode", chapter.Mode)
	}
	if chapter.DurationMS <= 0 {
		return v.fail(CodeChapterDurationInvalid, "chapter.duration_ms", fmt.Sprintf("%d", chapter.DurationMS))
	}
	if chapter.Grounding == GroundingAuthored {
		if len(chapter.Titles) == 0 {
			return v.fail(CodeChapterTitlesMissing, "chapter.titles", "")
		}
	} else if len(chapter.Titles) > 0 {
		return v.fail(CodeChapterTitlesUnexpected, "chapter.titles", "transcript grounding picks the title")
	}
	numbers := make(map[int]struct{}, len(chapter.Titles))
	for index, title := range chapter.Titles {
		titlePath := fmt.Sprintf("chapter.titles[%d]", index)
		if strings.TrimSpace(title.SceneID) == "" {
			return v.fail(CodeChapterTitleInvalid, titlePath+".scene_id", "")
		}
		if strings.TrimSpace(title.Title) == "" {
			return v.fail(CodeChapterTitleInvalid, titlePath+".title", "")
		}
		if title.ChapterNumber < 1 {
			return v.fail(CodeChapterTitleInvalid, titlePath+".chapter_number", fmt.Sprintf("%d", title.ChapterNumber))
		}
		if _, duplicate := numbers[title.ChapterNumber]; duplicate {
			return v.fail(CodeChapterTitleInvalid, titlePath+".chapter_number", fmt.Sprintf("%d", title.ChapterNumber))
		}
		numbers[title.ChapterNumber] = struct{}{}
	}
	return nil
}

func validPosition(position string) bool {
	switch position {
	case PositionAuto, PositionBelow, PositionAbove, PositionBadge, PositionSide:
		return true
	default:
		return false
	}
}

func isDateValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	return yearPattern.MatchString(trimmed) || isoPattern.MatchString(trimmed)
}

func normalizeText(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(text)), " ")
}
