package ollama

// getSystemPrompt returns the system prompt based on language and tone
func getSystemPrompt(language, tone string) string {
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
			"default": "Você é um contador de histórias excepcional e redator sênior. Sua tarefa é escrever roteiros de vídeo ENVOLVENTES, RICOS EM DETALHES e NARRATIVAMENTE PODEROSOS.",
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
		"spanish": {
			"professional": "Usa un estilo documental, autoritario y serio. Analiza profundamente cada aspecto.",
			"casual":       "Usa un estilo coloquial, moderno y amigable. Sé atractivo como un creador de YouTube.",
			"enthusiastic": "Usa un estilo enérgico, épico y motivacional. Cada frase debe transmitir pasión.",
			"calm":         "Usa un estilo calmado, reflexivo y poético. Crea una atmósfera inmersiva.",
			"funny":        "Usa un estilo irónico, brillante y divertido. Inserta chistes u observaciones ingeniosas.",
			"educational":  "Usa un estilo claro, pedagógico y estructurado. Explica conceptos de forma sencilla pero exhaustiva.",
		},
		"french": {
			"professional": "Utilisez un style documentaire, autoritaire et sérieux. Analysez profondément chaque aspect.",
			"casual":       "Utilisez un style familier, moderne et amical. Soyez captivant comme un créateur YouTube.",
			"enthusiastic": "Utilisez un style énergique, épique et motivant. Chaque phrase doit transmettre la passion.",
			"calm":         "Utilisez un style calme, réfléchi et poétique. Créez une atmosphère immersive.",
			"funny":        "Utilisez un style ironique, brillant et amusant. Insérez des blagues ou des observations astucieuses.",
			"educational":  "Utilisez un style clair, pédagogique et structuré. Expliquez les concepts simplement mais exhaustivement.",
		},
		"german": {
			"professional": "Verwenden Sie einen dokumentarischen, autoritären und seriösen Stil. Analysieren Sie jeden Aspekt tiefgreifend.",
			"casual":       "Verwenden Sie einen umgangssprachlichen, modernen und freundlichen Stil. Seien Sie fesselnd wie ein YouTube-Creator.",
			"enthusiastic": "Verwenden Sie einen energischen, epischen und motivierenden Stil. Jeder Satz muss Leidenschaft vermitteln.",
			"calm":         "Verwenden Sie einen ruhigen, reflektierten und poetischen Stil. Schaffen Sie eine immersive Atmosphäre.",
			"funny":        "Verwenden Sie einen ironischen, brillanten und lustigen Stil. Fügen Sie Witze oder geistreiche Beobachtungen ein.",
			"educational":  "Verwenden Sie einen klaren, pädagogischen und strukturierten Stil. Erklären Sie Konzepte einfach, aber gründlich.",
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