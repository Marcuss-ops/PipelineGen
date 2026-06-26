package scripts

type ScenesService struct{}

func NewScenesService(imgSvc, voSvc, log, cfg, resolveFolder, groupsRes interface{}, albumCapacity int) *ScenesService {
	return &ScenesService{}
}

func BuildScenesWithMarkers(script string, pack interface{}) []ClipScene {
	m, _ := pack.(map[string]any)
	return mapScriptToClipScenes(script, m)
}
