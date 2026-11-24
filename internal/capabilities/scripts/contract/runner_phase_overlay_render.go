package contract

import (
	"strings"
)

func (r *GenerateResult) OverlayPlanLanguage() Language {
	if r == nil || r.OverlayPlan == nil {
		return ""
	}
	return Language(strings.TrimSpace(r.OverlayPlan.Language))
}
