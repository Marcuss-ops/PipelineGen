package script

import "strings"

// SupportedLanguages is the canonical allowlist of target output
// languages for script generation. ISO 639-1 two-letter codes.
var SupportedLanguages = []string{
	"aa", "ab", "af", "am", "ar", "as", "ay", "az", "ba", "be",
	"bg", "bh", "bi", "bn", "bo", "br", "bs", "ca", "ce", "ch",
	"co", "cr", "cs", "cv", "cy", "da", "de", "dv", "dz", "ee",
	"el", "en", "eo", "es", "et", "eu", "fa", "ff", "fi", "fj",
	"fo", "fr", "fy", "ga", "gd", "gl", "gn", "gu", "gv", "ha",
	"he", "hi", "ho", "hr", "ht", "hu", "hy", "hz", "ia", "id",
	"ie", "ig", "ii", "ik", "io", "is", "it", "iu", "ja", "jv",
	"ka", "kg", "ki", "kj", "kk", "kl", "km", "kn", "ko", "kr",
	"ks", "ku", "kv", "kw", "ky", "la", "lb", "lg", "li", "ln",
	"lo", "lt", "lu", "lv", "mg", "mh", "mi", "mk", "ml", "mn",
	"mr", "ms", "mt", "my", "na", "nb", "nd", "ne", "ng", "nl",
	"nn", "no", "nr", "nv", "ny", "oc", "oj", "om", "or", "os",
	"pa", "pi", "pl", "ps", "pt", "qu", "rm", "rn", "ro", "ru",
	"rw", "sa", "sc", "sd", "se", "sg", "si", "sk", "sl", "sm",
	"sn", "so", "sq", "sr", "ss", "st", "su", "sv", "sw", "ta",
	"te", "tg", "th", "ti", "tk", "tl", "tn", "to", "tr", "ts",
	"tt", "tw", "ty", "ug", "uk", "ur", "uz", "ve", "vi", "vo",
	"wa", "wo", "xh", "yi", "yo", "za", "zh", "zu",
}

// IsSupportedLanguage returns true when code is a supported ISO 639-1
// language code. Empty string is treated as supported (caller will
// apply the configured default language).
func IsSupportedLanguage(code string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return true
	}
	for _, lang := range SupportedLanguages {
		if lang == code {
			return true
		}
	}
	return false
}
