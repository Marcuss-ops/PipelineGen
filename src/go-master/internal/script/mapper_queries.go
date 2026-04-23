package script

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"
)

// buildSearchQueries costruisce query di ricerca per una scena
func (m *Mapper) buildSearchQueries(scene *Scene) []string {
	var queries []string

	if len(scene.Keywords) > 0 {
		min3 := util.Min(3, len(scene.Keywords))
		queries = append(queries, strings.Join(scene.Keywords[:min3], " "))
	}

	for _, entity := range scene.Entities {
		queries = append(queries, entity.Text)
	}

	if len(scene.Emotions) > 0 {
		queries = append(queries, fmt.Sprintf("%s %s", scene.Emotions[0], scene.Type))
	}

	if scene.Title != "" {
		queries = append(queries, scene.Title)
	}

	return queries
}

// buildSearchQueriesFromTranslated costruisce query usando keywords tradotte in inglese
func (m *Mapper) buildSearchQueriesFromTranslated(scene *Scene, keywords, entities, emotions []string) []string {
	var queries []string

	if len(keywords) > 0 {
		min3k := util.Min(3, len(keywords))
		queries = append(queries, strings.Join(keywords[:min3k], " "))
	}

	for _, entity := range entities {
		queries = append(queries, entity)
	}

	if len(emotions) > 0 {
		queries = append(queries, fmt.Sprintf("%s %s", emotions[0], scene.Type))
	}

	if scene.Title != "" {
		translatedTitle := m.translator.TranslateQuery(scene.Title)
		queries = append(queries, translatedTitle)
	}

	return queries
}

// buildYouTubeQueries costruisce query specifiche per YouTube (in inglese)
func (m *Mapper) buildYouTubeQueries(scene *Scene) []string {
	var queries []string

	translatedKeywords := m.translator.TranslateKeywords(scene.Keywords)
	translatedEntities := m.translator.TranslateKeywords(scene.EntitiesText())

	min3t := util.Min(3, len(translatedKeywords))
	baseQuery := strings.Join(translatedKeywords[:min3t], " ")

	switch scene.Type {
	case SceneIntro:
		queries = append(queries, baseQuery+" introduction")
		queries = append(queries, baseQuery+" overview")
	case SceneContent:
		queries = append(queries, baseQuery+" explained")
		queries = append(queries, baseQuery+" tutorial")
		queries = append(queries, baseQuery+" documentary")
	case SceneConclusion:
		queries = append(queries, baseQuery+" summary")
		queries = append(queries, baseQuery+" conclusion")
	}

	for _, entity := range translatedEntities {
		queries = append(queries, entity+" "+string(scene.Type))
	}

	logger.Debug("Built YouTube queries (translated to English)",
		zap.Int("scene_number", scene.SceneNumber),
		zap.Strings("queries", queries),
	)

	return queries
}
