package channelmonitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

const protagonistMergeThreshold = 0.88

var monitorDefaultCategories = []string{"Boxe", "Crime", "Discovery", "HipHop", "Music", "Various", "Wwe"}

var protagonistNoiseWords = map[string]struct{}{
	"official": {}, "video": {}, "audio": {}, "lyrics": {}, "lyric": {},
	"interview": {}, "interviews": {}, "highlights": {}, "highlight": {},
	"training": {}, "best": {}, "moment": {}, "moments": {}, "full": {},
	"fight": {}, "fights": {}, "compilation": {}, "analysis": {}, "reaction": {},
	"documentary": {}, "episode": {}, "podcast": {}, "news": {},
	"press": {}, "conference": {}, "weighin": {}, "weigh-in": {}, "faceoff": {},
	"vs": {}, "v": {}, "feat": {}, "ft": {},
}

// resolveFolder determines the Drive folder where clips for a video should be uploaded.
// It extracts the protagonist from the title, classifies the entity via Ollama,
// finds or creates the matching category folder, then creates/finds the subfolder.
// Returns: folderPath (e.g. "HipHop/ArtistName"), folderID, folderExisted, category decision, error.
func (m *Monitor) resolveFolder(ctx context.Context, ch ChannelConfig, videoTitle string) (string, string, bool, CategoryDecision, error) {
	// Step 1: Determine the folder name for this channel/video.
	// If the channel provides a stable folder name, prefer it so recurring
	// runs keep writing into the same Drive subfolder.
	protagonist := strings.TrimSpace(ch.FolderName)
	if protagonist == "" {
		protagonist = extractProtagonist(videoTitle)
	}
	if protagonist == "" {
		protagonist = "Unknown"
	}

	// Step 2: Classify entity via Ollama to determine category
	category := ch.Category
	decision := CategoryDecision{
		Category:   category,
		Source:     "override",
		Confidence: 1.0,
	}
	if category == "" {
		classified, reason, err := m.classifyEntity(ctx, videoTitle, protagonist)
		if err != nil {
			logger.Warn("Ollama classification failed, using fallback category",
				zap.String("title", videoTitle),
				zap.Error(err),
			)
			category = fallbackCategory(videoTitle, protagonist)
			decision = CategoryDecision{
				Category:   category,
				Source:     "fallback",
				Reason:     reason,
				Confidence: classificationConfidence(category, "fallback"),
			}
		} else {
			category = classified
			decision = CategoryDecision{
				Category:   category,
				Source:     "gemma",
				Reason:     reason,
				Confidence: classificationConfidence(category, "gemma"),
			}
		}
	}
	if category == "" {
		category = "Various"
	}
	decision.Category = category
	decision.NeedsReview = decision.Confidence < 0.60 || category == "Various"
	if decision.Source == "override" && decision.Category != "" {
		decision.Confidence = 1.0
		decision.NeedsReview = false
	}

	// Step 3: Fuzzy match category to known categories
	canonicalCategory := fuzzyMatchFolder(category)
	if canonicalCategory == "" {
		canonicalCategory = category // use as-is if no match
	}
	decision.Category = canonicalCategory
	decision.NeedsReview = decision.NeedsReview || canonicalCategory == "Various"

	// Step 4: Find or create the category folder under root
	categoryFolderID, existed, err := m.getOrCreateCategoryFolder(ctx, canonicalCategory)
	if err != nil {
		return "", "", false, decision, fmt.Errorf("failed to get/create category folder: %w", err)
	}

	// Step 5: Reuse/create protagonist subfolder in selected category
	sanitizedName := sanitizeFolderName(protagonist)
	if sanitizedName == "" {
		sanitizedName = "Unknown"
	}

	chosenName := sanitizedName
	chosenID := ""

	if existingName, existingID, score, ok := m.findBestProtagonistFolder(ctx, categoryFolderID, sanitizedName); ok && score >= protagonistMergeThreshold {
		chosenName = existingName
		chosenID = existingID
		logger.Info("Reusing existing protagonist folder by fuzzy match",
			zap.String("requested", sanitizedName),
			zap.String("matched", existingName),
			zap.Float64("score", score),
			zap.String("folder_id", existingID),
		)
	}

	subfolderPath := canonicalCategory + "/" + chosenName

	if chosenID == "" {
		chosenID, err = m.driveClient.GetOrCreateFolder(ctx, chosenName, categoryFolderID)
		if err != nil {
			return "", "", false, decision, fmt.Errorf("failed to get/create subfolder %s: %w", subfolderPath, err)
		}
	}

	logger.Info("Folder resolved",
		zap.String("path", subfolderPath),
		zap.String("folder_id", chosenID),
		zap.Bool("category_existed", existed),
	)

	return subfolderPath, chosenID, existed, decision, nil
}

// classifyEntity uses Ollama to classify a video title into a category
func (m *Monitor) classifyEntity(ctx context.Context, title, protagonist string) (string, string, error) {
	candidates := m.categoryChoices(ctx)
	prompt := fmt.Sprintf(`You are a strict content router.
Choose exactly ONE category from this list: %s

Definitions:
- Boxe: boxing, fighters, sparring, press conference, weigh-in, bout, ring.
- Wwe: WWE/wrestling shows, wrestlers, RAW/SmackDown/PPV.
- Music: songs, albums, performances, music artists (including rappers), artist interviews.
- HipHop: hip-hop culture/news/scene in general (not mainly one artist's music interview).
- Various: mixed topics, uncategorized content, or content that doesn't fit the others.
- Crime: crime stories, arrests, gangs, investigations, court crime topics.
- Discovery: documentaries, science, education, nature, general knowledge.

Rule: if the main person is a rapper/singer/music artist, prefer Music.
Return JSON only: {"category":"<one from list>","reason":"<max 12 words>"}.

Title: "%s"
Protagonist: "%s"`, strings.Join(candidates, ", "), title, protagonist)

	reqBody := map[string]interface{}{
		"model":  "gemma3:4b",
		"prompt": prompt,
		"stream": false,
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.ollamaURL+"/api/generate", bytes.NewReader(reqJSON))
	if err != nil {
		return "", "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", "", fmt.Errorf("failed to decode response: %w", err)
	}

	category, reason := parseCategoryFromGemmaResponse(ollamaResp.Response, candidates)
	if category == "" {
		return "", "", fmt.Errorf("invalid category from model: %q", strings.TrimSpace(ollamaResp.Response))
	}
	category = applyCategoryGuardrails(category, title, protagonist)
	if normalized := fuzzyMatchFolder(category); normalized != "" {
		category = normalized
	}

	logger.Debug("Ollama classification result",
		zap.String("title", title),
		zap.String("protagonist", protagonist),
		zap.String("category", category),
	)

	return category, reason, nil
}

func parseCategoryFromGemmaResponse(raw string, candidates []string) (string, string) {
	candidateSet := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		candidateSet[c] = true
	}

	var payload struct {
		Category string `json:"category"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &payload); err == nil {
		cat := strings.TrimSpace(payload.Category)
		if cat != "" {
			if canonical := fuzzyMatchFolder(cat); canonical != "" && candidateSet[canonical] {
				return canonical, strings.TrimSpace(payload.Reason)
			}
			if candidateSet[cat] {
				return cat, strings.TrimSpace(payload.Reason)
			}
		}
	}

	clean := strings.TrimSpace(raw)
	clean = strings.Trim(clean, `"'.,;: `)
	if canonical := fuzzyMatchFolder(clean); canonical != "" && candidateSet[canonical] {
		return canonical, ""
	}

	lower := strings.ToLower(raw)
	for _, c := range candidates {
		if strings.Contains(lower, strings.ToLower(c)) {
			return c, ""
		}
	}
	return "", ""
}

func classificationConfidence(category, source string) float64 {
	switch source {
	case "override":
		return 1.0
	case "gemma":
		if category == "Various" {
			return 0.45
		}
		return 0.86
	case "fallback":
		if category == "Various" {
			return 0.35
		}
		return 0.60
	default:
		return 0.50
	}
}

func (m *Monitor) categoryChoices(ctx context.Context) []string {
	choices := append([]string{}, monitorDefaultCategories...)
	if m.driveClient == nil {
		return choices
	}
	clipRootID := strings.TrimSpace(m.clipRootID())
	if clipRootID == "" {
		if id, err := m.findClipRootNoCreate(ctx); err == nil && strings.TrimSpace(id) != "" {
			clipRootID = id
		}
	}
	if clipRootID == "" {
		return choices
	}
	folders, err := m.driveClient.ListFoldersNoRecursion(ctx, drive.ListFoldersOptions{
		ParentID: clipRootID,
		MaxItems: 100,
	})
	if err != nil {
		return choices
	}
	set := make(map[string]bool, len(choices))
	for _, c := range choices {
		set[c] = true
	}
	for _, f := range folders {
		if canonical := fuzzyMatchFolder(f.Name); canonical != "" {
			set[canonical] = true
		}
	}
	out := make([]string, 0, len(set))
	for _, c := range monitorDefaultCategories {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}

func fallbackCategory(title, protagonist string) string {
	titleLower := strings.ToLower(title)
	protagonistLower := strings.ToLower(protagonist)

	boxeTerms := []string{"boxing", "boxe", "fight", "fighter", "ring", "weigh", "mayweather", "gervonta", "tyson"}
	wweTerms := []string{"wwe", "wrestling", "raw", "smackdown", "royal rumble", "wrestlemania", "roman reigns"}
	crimeTerms := []string{"crime", "murder", "arrest", "mafia", "gang", "cartel", "court case", "investigation"}
	musicTerms := []string{"song", "album", "official video", "lyrics", "live", "concert", "music", "rapper", "feat", "ft"}
	discoveryTerms := []string{"documentary", "science", "history", "nature", "education", "discovery"}

	if containsAny(titleLower, wweTerms) {
		return "Wwe"
	}
	if containsAny(titleLower, boxeTerms) {
		return "Boxe"
	}
	if containsAny(titleLower, crimeTerms) {
		return "Crime"
	}
	if containsAny(titleLower, musicTerms) || isLikelyMusicEntity(protagonistLower) {
		return "Music"
	}
	if containsAny(titleLower, discoveryTerms) {
		return "Discovery"
	}
	return "Various"
}

func applyCategoryGuardrails(category, title, protagonist string) string {
	canonical := category
	if normalized := fuzzyMatchFolder(category); normalized != "" {
		canonical = normalized
	}
	if canonical == "HipHop" {
		titleLower := strings.ToLower(title)
		if strings.Contains(titleLower, "interview") || strings.Contains(titleLower, "podcast") || strings.Contains(titleLower, "talk") {
			if isLikelyMusicEntity(strings.ToLower(protagonist)) {
				return "Music"
			}
		}
	}
	if canonical == "Various" {
		return "Various"
	}
	return canonical
}

func isLikelyMusicEntity(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return false
	}
	knownArtists := []string{
		"50 cent", "eminem", "drake", "kanye west", "jay z", "kendrick lamar",
		"travis scott", "rihanna", "beyonce", "taylor swift", "nicki minaj",
	}
	for _, artist := range knownArtists {
		if strings.Contains(name, artist) || strings.Contains(artist, name) {
			return true
		}
	}
	return false
}

func containsAny(text string, terms []string) bool {
	for _, t := range terms {
		if strings.Contains(text, t) {
			return true
		}
	}
	return false
}

// fuzzyMatchFolder maps a category string to a known canonical category
func fuzzyMatchFolder(category string) string {
	normalized := strings.ToLower(strings.TrimSpace(category))

	if canonical, ok := knownCategories[normalized]; ok {
		return canonical
	}

	for key, canonical := range knownCategories {
		if strings.Contains(normalized, key) || strings.Contains(key, normalized) {
			return canonical
		}
	}

	for _, canonical := range knownCategories {
		if strings.EqualFold(category, canonical) {
			return canonical
		}
	}

	return ""
}

// getOrCreateCategoryFolder finds or creates a category folder under the root
func (m *Monitor) getOrCreateCategoryFolder(ctx context.Context, category string) (string, bool, error) {
	cacheKey := "Clips/" + category
	if folderID, ok := m.getCachedFolder(cacheKey); ok {
		return folderID, true, nil
	}

	clipRootID := m.clipRootID()
	if clipRootID == "" {
		var err error
		clipRootID, err = m.findClipRoot(ctx)
		if err != nil {
			return "", false, fmt.Errorf("Clips root folder not found: %w", err)
		}
	}

	result, err := m.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: clipRootID,
		MaxDepth: 1,
		MaxItems: 100,
	})
	if err != nil {
		folderID, err := m.driveClient.CreateFolder(ctx, category, clipRootID)
		if err != nil {
			return "", false, fmt.Errorf("failed to create category folder: %w", err)
		}
		m.setCachedFolder(cacheKey, folderID)
		return folderID, false, nil
	}

	for _, f := range result {
		if strings.EqualFold(f.Name, category) {
			m.setCachedFolder(cacheKey, f.ID)
			return f.ID, true, nil
		}
	}

	folderID, err := m.driveClient.CreateFolder(ctx, category, clipRootID)
	if err != nil {
		return "", false, fmt.Errorf("failed to create category folder: %w", err)
	}
	m.setCachedFolder(cacheKey, folderID)

	return folderID, false, nil
}

// findClipRoot searches for the Clips root folder by name
func (m *Monitor) findClipRoot(ctx context.Context) (string, error) {
	result, err := m.driveClient.ListFoldersNoRecursion(ctx, drive.ListFoldersOptions{MaxItems: 100})
	if err != nil {
		return "", err
	}

	for _, f := range result {
		if strings.EqualFold(f.Name, "Clips") {
			m.config.ClipRootID = f.ID
			return f.ID, nil
		}
	}

	folderID, err := m.driveClient.CreateFolder(ctx, "Clips", "root")
	if err != nil {
		return "", fmt.Errorf("failed to create Clips root: %w", err)
	}
	m.config.ClipRootID = folderID
	return folderID, nil
}

func (m *Monitor) findClipRootNoCreate(ctx context.Context) (string, error) {
	result, err := m.driveClient.ListFoldersNoRecursion(ctx, drive.ListFoldersOptions{MaxItems: 100})
	if err != nil {
		return "", err
	}
	for _, f := range result {
		if strings.EqualFold(f.Name, "Clips") {
			return f.ID, nil
		}
	}
	return "", nil
}

// extractProtagonist extracts the main subject/person name from a video title.
func extractProtagonist(title string) string {
	cleaned := normalizeWhitespace(removeBracketed(title))
	cleaned = cutAtSeparator(cleaned)
	tokens := strings.Fields(cleaned)
	if len(tokens) == 0 {
		return ""
	}

	var picked []string
	seenName := false
	for _, tok := range tokens {
		trimTok := strings.Trim(tok, ".,:;!?\"'")
		if trimTok == "" {
			continue
		}
		lower := strings.ToLower(trimTok)

		if (isNoiseWord(lower) || isConnector(lower)) && seenName {
			break
		}
		if looksLikeNameToken(trimTok) {
			picked = append(picked, trimTok)
			seenName = true
			if len(picked) >= 4 {
				break
			}
			continue
		}
		if seenName {
			break
		}
	}

	if len(picked) >= 2 {
		return sanitizeFolderName(strings.Join(picked, " "))
	}

	legacy := regexp.MustCompile(`(?i)\b(official\s+(music\s+)?video|lyrics?\s*video|audio|ft\.?\s+\w+|feat\.?\s+.+?)\b`).ReplaceAllString(cleaned, "")
	legacy = regexp.MustCompile(`[^a-zA-Z0-9\s'&-]`).ReplaceAllString(legacy, "")
	legacy = normalizeWhitespace(legacy)
	legacy = trimTrailingNoise(legacy)

	if idx := strings.Index(legacy, " - "); idx > 0 {
		name := strings.TrimSpace(legacy[:idx])
		if isValidName(name) {
			return name
		}
	}
	if idx := strings.Index(legacy, ":"); idx > 0 {
		name := strings.TrimSpace(legacy[:idx])
		if isValidName(name) {
			return name
		}
	}

	vsPatterns := regexp.MustCompile(`(?i)\b(?:vs\.?|and|&)\b`)
	if vsPatterns.MatchString(legacy) {
		parts := vsPatterns.Split(legacy, 2)
		if len(parts) == 2 {
			name := strings.TrimSpace(parts[0])
			if isValidName(name) {
				return name
			}
		}
	}

	words := strings.Fields(legacy)
	if len(words) <= 4 {
		return legacy
	}

	re2 := regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)*)`)
	matches := re2.FindAllString(legacy, -1)
	if len(matches) > 0 {
		longest := ""
		for _, m := range matches {
			if len(m) > len(longest) {
				longest = m
			}
		}
		if isValidName(longest) {
			return longest
		}
	}

	if len(words) >= 2 {
		return strings.Join(words[:2], " ")
	}
	if len(words) == 1 {
		return words[0]
	}

	return legacy
}

func (m *Monitor) findBestProtagonistFolder(ctx context.Context, categoryFolderID, candidate string) (string, string, float64, bool) {
	if strings.TrimSpace(candidate) == "" {
		return "", "", 0, false
	}
	result, err := m.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: categoryFolderID,
		MaxDepth: 1,
		MaxItems: 500,
	})
	if err != nil {
		return "", "", 0, false
	}

	bestScore := 0.0
	bestName := ""
	bestID := ""
	for _, f := range result {
		score := nameSimilarityScore(candidate, f.Name)
		if score > bestScore {
			bestScore = score
			bestName = f.Name
			bestID = f.ID
		}
	}
	if bestName == "" {
		return "", "", 0, false
	}
	return bestName, bestID, bestScore, true
}

func nameSimilarityScore(a, b string) float64 {
	nA := normalizeProtagonistKey(a)
	nB := normalizeProtagonistKey(b)
	if nA == "" || nB == "" {
		return 0
	}
	if nA == nB {
		return 1
	}

	score := tokenJaccard(nA, nB)
	if strings.Contains(nA, nB) || strings.Contains(nB, nA) {
		score = math.Max(score, 0.92)
	}
	if leadingTokensEqual(a, b, 2) {
		score = math.Max(score, 0.90)
	}
	return math.Min(score, 1)
}

func normalizeProtagonistKey(name string) string {
	s := strings.ToLower(name)
	s = regexp.MustCompile(`[^a-z0-9\s]`).ReplaceAllString(s, " ")
	s = normalizeWhitespace(s)
	if s == "" {
		return ""
	}
	var out []string
	for _, t := range strings.Fields(s) {
		if isNoiseWord(t) || len(t) <= 1 {
			continue
		}
		out = append(out, t)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func tokenJaccard(a, b string) float64 {
	as := strings.Fields(a)
	bs := strings.Fields(b)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	setA := make(map[string]struct{}, len(as))
	setB := make(map[string]struct{}, len(bs))
	for _, t := range as {
		setA[t] = struct{}{}
	}
	for _, t := range bs {
		setB[t] = struct{}{}
	}
	inter := 0
	for t := range setA {
		if _, ok := setB[t]; ok {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func leadingTokensEqual(a, b string, n int) bool {
	ta := strings.Fields(strings.ToLower(normalizeWhitespace(a)))
	tb := strings.Fields(strings.ToLower(normalizeWhitespace(b)))
	if len(ta) < n || len(tb) < n {
		return false
	}
	for i := 0; i < n; i++ {
		if ta[i] != tb[i] {
			return false
		}
	}
	return true
}

func isNoiseWord(token string) bool {
	_, ok := protagonistNoiseWords[strings.ToLower(strings.TrimSpace(token))]
	return ok
}

func isConnector(token string) bool {
	switch strings.ToLower(token) {
	case "-", "|", ":", "vs", "v", "and", "&":
		return true
	default:
		return false
	}
}

func looksLikeNameToken(token string) bool {
	if token == "" {
		return false
	}
	r := rune(token[0])
	if r >= 'A' && r <= 'Z' {
		return true
	}
	allUpper := true
	for _, ch := range token {
		if ch >= 'a' && ch <= 'z' {
			allUpper = false
			break
		}
	}
	return allUpper && len(token) >= 2
}

func removeBracketed(s string) string {
	re := regexp.MustCompile(`[\(\[\{][^\)\]\}]*[\)\]\}]`)
	return re.ReplaceAllString(s, " ")
}

func normalizeWhitespace(s string) string {
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func cutAtSeparator(s string) string {
	separators := []string{" | ", " - ", " : "}
	for _, sep := range separators {
		if idx := strings.Index(s, sep); idx > 0 {
			return strings.TrimSpace(s[:idx])
		}
	}
	return s
}

func trimTrailingNoise(s string) string {
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return s
	}
	end := len(parts)
	for end > 0 {
		if isNoiseWord(parts[end-1]) {
			end--
			continue
		}
		break
	}
	if end == 0 {
		return s
	}
	return strings.Join(parts[:end], " ")
}

// isValidName checks if a string looks like a valid name
func isValidName(name string) bool {
	if len(name) < 2 || len(name) > 50 {
		return false
	}
	words := strings.Fields(name)
	for _, w := range words {
		if len(w) > 0 && w[0] >= 'A' && w[0] <= 'Z' {
			return true
		}
	}
	return false
}

// sanitizeFolderName removes invalid characters from folder names for Google Drive
func sanitizeFolderName(name string) string {
	re := regexp.MustCompile(`[<>:"/\\|?*]`)
	cleaned := re.ReplaceAllString(name, "")
	cleaned = regexp.MustCompile(`[\x00-\x1f]`).ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)
	cleaned = regexp.MustCompile(`\s+`).ReplaceAllString(cleaned, " ")
	if len(cleaned) > 100 {
		cleaned = cleaned[:100]
	}
	if cleaned == "" {
		return "Unnamed"
	}
	return cleaned
}
