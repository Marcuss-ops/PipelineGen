package prompts

// BuildSystemPrompt returns the system prompt based on language and tone
func BuildSystemPrompt(language, tone string) string {
	// Base system prompts per language
	basePrompts := map[string]map[string]string{
		"italian": {
			"default": "Sei un narratore eccezionale e un copywriter senior. Il tuo compito è scrivere script video AVVINCENTI, RICCHI DI DETTAGLI e NARRATIVAMENTE POTENTI.",
		},
		"english": {
			"default": "You are an exceptional storyteller and senior copywriter. Your task is to write COMPELLING, DETAIL-RICH, and NARRATIVELY POWERFUL video scripts.",
		},
		"spanish": {
			"default": "Eres un narrador excepcional e copywriter senior. Tu tarea es escribir guiones de video FASCINANTES, RICOS EN DETALLES y NARRATIVAMENTE PODEROSOS.",
		},
		"french": {
			"default": "Vous êtes un conteur exceptionnel et un rédacteur principal. Votre tâche consiste à rédiger des scripts vidéo CAPTIVANTS, RICHES EN DÉTAILS et NARRATIVEMENT PUISSANTS.",
		},
		"german": {
			"default": "Sie sind un außergewöhnlicher Geschichtenerzähler und Senior Copywriter. Ihre Aufgabe ist es, FESSELNDE, DETAILREICHE und NARRATIV STARKE Video-Skripte zu schreiben.",
		},
		"portuguese": {
			"default": "Você è um contador de histórias excepcional e redator sênior. Sua tarefa è escrever roteiros de vídeo ENVOLVENTES, RICOS EM DETALHES e NARRATIVAMENTE PODEROSOS.",
		},
		"russian": {
			"default": "Вы — выдающийся рассказчик и старший копирайтер. Ваша задача — писать ЗАХВАТЫВАЮЩИЕ, БОГАТЫЕ ДЕТАЛЯМИ и НАРРАТИВНО МОЩНЫЕ сценарии видео.",
		},
	}

	// Tone instructions per language
	toneInstructions := map[string]map[string]string{
		"italian": {
			"professional": "Usa uno stile documentaristico, autorevole e serio. Analizza profondamente ogni aspetto.",
			"casual":       "Usa uno stile colloquiale, moderno e amichevole. Sii coinvolgente come un creator di YouTube.",
			"enthusiastic": "Usa uno stile energico, epico e motivazionale. Ogni frase deve trasmettere passione.",
			"calm":         "Usa uno stile pacato, riflessivo e poetico. Crea un'atmosfera immersiva.",
			"funny":        "Usa uno stile ironico, brillante e divertente. Inserisci battute o osservazioni sagaci.",
			"educational":  "Usa uno stile chiaro, pedagogico e strutturato. Spiega i concetti in modo semplice ma esaustivo.",
		},
		"english": {
			"professional": "Use a documentary, authoritative, and serious style. Deeply analyze every aspect.",
			"casual":       "Use a colloquial, modern, and friendly style. Be engaging like a YouTube creator.",
			"enthusiastic": "Use an energetic, epic, and motivational style. Every sentence must convey passion.",
			"calm":         "Use a calm, reflective, and poetic style. Create an immersive atmosphere.",
			"funny":        "Use an ironic, brilliant, and funny style. Insert jokes or witty observations.",
			"educational":  "Use a clear, pedagogical, and structured style. Explain concepts simply but thoroughly.",
		},
	}

	// Get base prompt for language
	langPrompts, ok := basePrompts[language]
	if !ok {
		langPrompts = basePrompts["english"]
	}
	prompt := langPrompts["default"]

	// Get tone instructions for language
	langTones, ok := toneInstructions[language]
	if !ok {
		langTones = toneInstructions["english"]
	}

	if toneInstr, ok := langTones[tone]; ok {
		prompt += " " + toneInstr
	}

	return prompt
}
