// Package tts provides voice mappings for TTS.
package tts

// DefaultVoices mappatura voce di default per lingua
var DefaultVoices = map[string]string{
	"it": "it-IT-ElsaNeural",
	"en": "en-US-JennyNeural",
	"es": "es-ES-ElviraNeural",
	"fr": "fr-FR-DeniseNeural",
	"de": "de-DE-KatjaNeural",
	"pt": "pt-BR-FranciscaNeural",
	"ru": "ru-RU-SvetlanaNeural",
	"ja": "ja-JP-NanamiNeural",
	"ko": "ko-KR-SunHiNeural",
	"zh": "zh-CN-XiaoxiaoNeural",
	"nl": "nl-NL-ColetteNeural",
	"pl": "pl-PL-ZofiaNeural",
	"tr": "tr-TR-EmelNeural",
	"ar": "ar-AE-FatimaNeural",
	"hi": "hi-IN-SwaraNeural",
}

// AvailableLanguages lista lingue disponibili
var AvailableLanguages = []Language{
	{Code: "it", Name: "Italian", Voices: []string{"it-IT-ElsaNeural", "it-IT-IsabellaNeural", "it-IT-DiegoNeural"}},
	{Code: "en", Name: "English", Voices: []string{"en-US-JennyNeural", "en-US-GuyNeural", "en-GB-SoniaNeural", "en-GB-RyanNeural"}},
	{Code: "es", Name: "Spanish", Voices: []string{"es-ES-ElviraNeural", "es-ES-AlvaroNeural", "es-MX-DaliaNeural"}},
	{Code: "fr", Name: "French", Voices: []string{"fr-FR-DeniseNeural", "fr-FR-HenriNeural", "fr-CA-SylvieNeural"}},
	{Code: "de", Name: "German", Voices: []string{"de-DE-KatjaNeural", "de-DE-ConradNeural", "de-AT-IngridNeural"}},
	{Code: "pt", Name: "Portuguese", Voices: []string{"pt-BR-FranciscaNeural", "pt-BR-AntonioNeural", "pt-PT-RaquelNeural"}},
	{Code: "ru", Name: "Russian", Voices: []string{"ru-RU-SvetlanaNeural", "ru-RU-DmitryNeural"}},
	{Code: "ja", Name: "Japanese", Voices: []string{"ja-JP-NanamiNeural", "ja-JP-KeitaNeural"}},
	{Code: "ko", Name: "Korean", Voices: []string{"ko-KR-SunHiNeural", "ko-KR-InJoonNeural"}},
	{Code: "zh", Name: "Chinese", Voices: []string{"zh-CN-XiaoxiaoNeural", "zh-CN-YunxiNeural", "zh-TW-HsiaoChenNeural"}},
	{Code: "nl", Name: "Dutch", Voices: []string{"nl-NL-ColetteNeural", "nl-NL-MaartenNeural"}},
	{Code: "pl", Name: "Polish", Voices: []string{"pl-PL-ZofiaNeural", "pl-PL-MarekNeural"}},
	{Code: "tr", Name: "Turkish", Voices: []string{"tr-TR-EmelNeural", "tr-TR-AhmetNeural"}},
	{Code: "ar", Name: "Arabic", Voices: []string{"ar-AE-FatimaNeural", "ar-EG-SalmaNeural"}},
	{Code: "hi", Name: "Hindi", Voices: []string{"hi-IN-SwaraNeural", "hi-IN-MadhurNeural"}},
}

// GetDefaultVoice restituisce la voce di default per una lingua
func GetDefaultVoice(lang string) string {
	if voice, ok := DefaultVoices[lang]; ok {
		return voice
	}
	return DefaultVoices["en"] // fallback
}

// GetLanguageFromVoice estrae il codice lingua dalla voce
func GetLanguageFromVoice(voice string) string {
	for lang, v := range DefaultVoices {
		if v == voice {
			return lang
		}
	}
	// Try to extract from voice name (e.g., "it-IT-ElsaNeural" -> "it")
	if len(voice) >= 2 {
		return voice[:2]
	}
	return "en"
}

// ListLanguages restituisce la lista delle lingue disponibili
func ListLanguages() []Language {
	return AvailableLanguages
}

// IsValidLanguage verifica se una lingua è supportata
func IsValidLanguage(lang string) bool {
	_, ok := DefaultVoices[lang]
	return ok
}

// IsValidVoice verifica se una voce è valida
func IsValidVoice(voice string) bool {
	for _, lang := range AvailableLanguages {
		for _, v := range lang.Voices {
			if v == voice {
				return true
			}
		}
	}
	return false
}