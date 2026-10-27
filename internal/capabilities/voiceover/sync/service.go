// Package sync owns the voiceover Drive reconciliation capability:
// walking the canonical voiceover Drive folder and upserting the
// discovered audio files into the voiceovers repository + asset tree.
//
// Canonical owner: internal/capabilities/voiceover (moved from
// internal/capabilities/assets/reconciliation/voiceover in the
// voiceover owner-unification cleanup).
package sync

import (
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
)

const folderMimeType = "application/vnd.google-apps.folder"

type Service struct {
	uploader     *driveup.Uploader
	log          *zap.Logger
	repo         *assets.VoiceoversRepository
	assetTreeSvc *assettree.Service
	rootFolderID string
}

func NewService(uploader *driveup.Uploader, repo *assets.VoiceoversRepository, assetTreeSvc *assettree.Service, rootFolderID string, log *zap.Logger) *Service {
	return &Service{
		uploader:     uploader,
		log:          log,
		repo:         repo,
		assetTreeSvc: assetTreeSvc,
		rootFolderID: rootFolderID,
	}
}

type Summary struct {
	OK        bool      `json:"ok"`
	RootID    string    `json:"root_id"`
	Synced    int       `json:"synced"`
	Failed    int       `json:"failed"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Error     string    `json:"error,omitempty"`
}

// validLanguageCodes is an allowlist of valid language codes.
// This prevents naive extraction from misidentifying filenames like "my_audio_final.mp3" as language "final".
var validLanguageCodes = map[string]bool{
	"aa": true, "ab": true, "ae": true, "af": true, "ak": true, "am": true, "an": true, "ar": true, "as": true, "av": true,
	"ay": true, "az": true, "ba": true, "be": true, "bg": true, "bh": true, "bi": true, "bm": true, "bn": true,
	"bo": true, "br": true, "bs": true, "ca": true, "ce": true, "ch": true, "co": true, "cr": true, "cs": true,
	"cu": true, "cv": true, "cy": true, "da": true, "de": true, "dv": true, "dz": true, "ee": true, "el": true,
	"en": true, "eo": true, "es": true, "et": true, "eu": true, "fa": true, "ff": true, "fi": true, "fj": true,
	"fo": true, "fr": true, "fy": true, "ga": true, "gd": true, "gl": true, "gn": true, "gu": true, "gv": true,
	"ha": true, "he": true, "hi": true, "ho": true, "hr": true, "ht": true, "hu": true, "hy": true, "hz": true,
	"ia": true, "id": true, "ie": true, "ig": true, "ii": true, "ik": true, "io": true, "is": true, "it": true,
	"iu": true, "ja": true, "jv": true, "ka": true, "kg": true, "ki": true, "kj": true, "kk": true, "kl": true,
	"km": true, "kn": true, "ko": true, "kr": true, "ks": true, "ku": true, "kv": true, "kw": true, "ky": true,
	"la": true, "lb": true, "lg": true, "li": true, "ln": true, "lo": true, "lt": true, "lu": true, "lv": true,
	"mg": true, "mh": true, "mi": true, "mk": true, "ml": true, "mn": true, "mr": true, "ms": true, "mt": true,
	"my": true, "na": true, "nb": true, "nd": true, "ne": true, "ng": true, "nl": true, "nn": true, "no": true,
	"nr": true, "nv": true, "ny": true, "oc": true, "oj": true, "om": true, "or": true, "os": true, "pa": true,
	"pi": true, "pl": true, "ps": true, "pt": true, "qu": true, "rm": true, "rn": true, "ro": true, "ru": true,
	"rw": true, "sa": true, "sc": true, "sd": true, "se": true, "sg": true, "si": true, "sk": true, "sl": true,
	"sm": true, "sn": true, "so": true, "sq": true, "sr": true, "ss": true, "st": true, "su": true, "sv": true,
	"sw": true, "ta": true, "te": true, "tg": true, "th": true, "ti": true, "tk": true, "tl": true, "tn": true,
	"to": true, "tr": true, "ts": true, "tt": true, "tw": true, "ty": true, "ug": true, "uk": true, "ur": true,
	"uz": true, "ve": true, "vi": true, "vo": true, "wa": true, "wo": true, "xh": true, "yi": true, "yo": true,
	"za": true, "zh": true, "zu": true,
	// Common locale variants
	"en-US": true, "en-GB": true, "en-AU": true, "en-CA": true, "en-IN": true,
	"fr-FR": true, "fr-CA": true,
	"pt-BR": true, "pt-PT": true,
	"es-ES": true, "es-MX": true, "es-AR": true,
	"de-DE": true, "de-AT": true, "de-CH": true,
	"it-IT": true,
	"ja-JP": true, "ko-KR": true, "zh-CN": true, "zh-TW": true,
}
