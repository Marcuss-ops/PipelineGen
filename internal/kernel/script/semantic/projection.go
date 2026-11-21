// Package semantic owns the grounded semantic projection wire contracts
// produced by the BrainRust semantic engine. They live in the kernel so the
// scripts capability and platform adapters can share them without the adapter
// importing the scripts business package.
package semantic

// SemanticPlace is one exact-source place mention with its conservative KB link.
type SemanticPlace struct {
	ID, EntityLocalID, KBID, LinkStatus, Kind, Name, Role, Evidence string
	ParentIDs                                                       []string
	Lat, Lon                                                        *float64
	SentenceIndex, Start, End                                       int
}

// SemanticRoute is a grounded, scene-local movement route.
type SemanticRoute struct {
	ID, Mode, SubjectID, Status, Evidence string
	Stops                                 []SemanticRouteStop
	StraightLineKM                        *float64
	Start, End                            int
}

type SemanticRouteStop struct {
	PlaceID, Role string
}

// SemanticCrossSceneRoute records a route assembled from consecutive scenes.
type SemanticCrossSceneRoute struct {
	ID, SubjectID, Mode, Status string
	Stops                       []SemanticCrossSceneRouteStop
	Evidence                    []SemanticRouteEvidence
	StraightLineKM              *float64
}

type SemanticCrossSceneRouteStop struct {
	SceneID, PlaceID, Role string
}

type SemanticRouteEvidence struct {
	SceneID, RouteID, Evidence string
	Start, End                 int
}

type SemanticPersonRole struct {
	ID, PersonID, RoleCode, RoleSurface, Gender, Rank, OrganizationID, PlaceID, Tense, Status, Evidence string
	Start, End, SentenceIndex                                                                           int
	Temporal                                                                                            *SemanticRoleTemporal
}

type SemanticRoleTemporal struct {
	Surface, NormalizedDate, Precision, Kind, Era string
	Start, End                                    int
	Ambiguous                                     bool
}

type SemanticPersonAgeFact struct {
	PersonID, Evidence, AsOf       string
	Age, Start, End, SentenceIndex int
	DerivedBirthYear               *int
}

type SemanticPersonRelation struct {
	FromID, ToID, Kind, Status, Evidence string
	Start, End, SentenceIndex            int
}

type SemanticEventFrame struct {
	ID, EventType, Trigger, Status, PlaceID  string
	TriggerStart, TriggerEnd                 int
	SceneIDs, ConfidenceBasis, SourceFactIDs []string
	Participants                             []SemanticEventParticipant
	Evidence                                 []SemanticEventEvidence
	Time                                     *SemanticEventTime
}

type SemanticEventTime struct {
	FactIndex int
	Date      string
	Derived   bool
}

type SemanticDiscourseProjection struct {
	Schema          string
	References      []SemanticDiscourseReference
	Threads         []SemanticEntityThread
	SceneContinuity []SemanticSceneContinuity
	Counts          SemanticDiscourseCounts
}

type SemanticDiscourseReference struct {
	ID, SceneID, Mention, Type, Status, ResolutionBasis, Reason, Evidence string
	SentenceIndex, Start, End, SentenceStart, SentenceEnd                 int
	TargetEntityIDs, CandidateEntityIDs                                   []string
}

type SemanticThreadBeat struct {
	ReferenceID, SceneID, State string
	SentenceIndex               int
}

type SemanticEntityThread struct {
	EntityID, CanonicalSurface, Type, FirstSceneID, LastSceneID string
	SceneIDs                                                    []string
	MentionCount, ReturnCount                                   int
	Beats                                                       []SemanticThreadBeat
}

type SemanticSceneContinuity struct {
	SceneID, Status                                                     string
	Score                                                               float64
	ActiveEntityIDs, SharedEntityIDs, EnteredEntityIDs, ExitedEntityIDs []string
}

type SemanticDiscourseCounts struct {
	Total, Resolved, Ambiguous, Unresolved, Threads, CrossSceneThreads int
}

type SemanticEventParticipant struct {
	Role, EntityID string
}

type SemanticEventEvidence struct {
	SceneID, Text string
	Start, End    int
}
