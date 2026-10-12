package scriptgeneration

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type animationGroupKey struct {
	target, groupID, subgroupID string
}

// toOverlayAnimationPolicies projects the script payload onto the rendering
// contract without changing the caller's selections.
func toOverlayAnimationPolicies(policies []scriptpkg.AnimationPolicy) []capabilityoverlay.AnimationPolicy {
	out := make([]capabilityoverlay.AnimationPolicy, len(policies))
	for i, policy := range policies {
		out[i] = capabilityoverlay.AnimationPolicy{
			Target: policy.Target, GroupID: policy.GroupID, SubgroupID: policy.SubgroupID,
			MotionID: policy.MotionID, MotionParams: policy.MotionParams,
		}
	}
	return out
}

// applyAnimationPolicies overlays exact payload choices after automatic motion
// assignment. Missing choices retain the first seeded motion in each group and
// reuse it for the remaining compatible items, so a group has one treatment.
func applyAnimationPolicies(items []capabilityoverlay.OverlayItem, policies []scriptpkg.AnimationPolicy, planID, videoID string, entityImagePool []string) error {
	selected := make(map[animationGroupKey]scriptpkg.AnimationPolicy, len(policies))
	for i, policy := range policies {
		if strings.TrimSpace(policy.Target) != policy.Target || strings.TrimSpace(policy.GroupID) != policy.GroupID || strings.TrimSpace(policy.SubgroupID) != policy.SubgroupID || strings.TrimSpace(policy.MotionID) != policy.MotionID {
			return fmt.Errorf("animation_policies[%d] selector fields must be trimmed", i)
		}
		if policy.Target != "important_phrase" && policy.Target != "image" && policy.Target != "caption" && policy.Target != "date" && policy.Target != "metric" {
			return fmt.Errorf("animation_policies[%d] has unsupported target %q", i, policy.Target)
		}
		if policy.GroupID == "" {
			return fmt.Errorf("animation_policies[%d] requires group_id", i)
		}
		if utf8.RuneCountInString(policy.GroupID) > 128 || utf8.RuneCountInString(policy.SubgroupID) > 128 {
			return fmt.Errorf("animation_policies[%d] group_id/subgroup_id may not exceed 128 characters", i)
		}
		if policy.MotionID != "" && !certifiedPolicyMotion(policy.Target, policy.MotionID, entityImagePool) {
			return fmt.Errorf("animation_policies[%d] motion %q is not certified for target %q", i, policy.MotionID, policy.Target)
		}
		if policy.MotionID == "" && len(policy.MotionParams) > 0 {
			return fmt.Errorf("animation_policies[%d] motion_params requires motion_id", i)
		}
		if err := validatePolicyMotionParams(policy.MotionParams); err != nil {
			return fmt.Errorf("animation_policies[%d]: %w", i, err)
		}
		key := animationGroupKey{policy.Target, policy.GroupID, policy.SubgroupID}
		if _, exists := selected[key]; exists {
			return fmt.Errorf("duplicate animation policy for target %q group %q subgroup %q", policy.Target, policy.GroupID, policy.SubgroupID)
		}
		selected[key] = policy
	}

	groupMotions := make(map[animationGroupKey]scriptpkg.AnimationPolicy)
	seed := digest.SHA256String(planID + "\x00" + videoID)
	declaredGroups := make(map[animationGroupKey]struct{}, len(policies))
	for _, policy := range policies {
		if policy.SubgroupID == "" {
			declaredGroups[animationGroupKey{target: policy.Target, groupID: policy.GroupID}] = struct{}{}
		}
	}
	resolve := func(target, groupID, subgroupID string) (string, map[string]any) {
		if groupID == "" {
			return "", nil
		}
		key := animationGroupKey{target, groupID, subgroupID}
		policy, hasPolicy := selected[key]
		if !hasPolicy && subgroupID != "" {
			key.subgroupID = ""
			policy, hasPolicy = selected[key]
		}
		if hasPolicy {
			if policy.MotionID == "" {
				selectionSubgroup := policy.SubgroupID
				policy.MotionID = seededGroupMotion(target, groupID, seed+"\x00"+selectionSubgroup, entityImagePool)
				if policy.MotionID == "" {
					return "", nil
				}
			}
			policy.MotionParams = clonePolicyParams(policy.MotionParams)
			groupMotions[key] = policy
			return policy.MotionID, policy.MotionParams
		}
		fallbackKey := animationGroupKey{target: target, groupID: groupID}
		if motion, ok := groupMotions[fallbackKey]; ok {
			return motion.MotionID, clonePolicyParams(motion.MotionParams)
		}
		if _, declared := declaredGroups[fallbackKey]; declared {
			if motionID := seededGroupMotion(target, groupID, seed, entityImagePool); motionID != "" {
				groupMotions[fallbackKey] = scriptpkg.AnimationPolicy{Target: target, GroupID: groupID, MotionID: motionID}
				return motionID, nil
			}
		}
		return "", nil
	}

	for itemIndex := range items {
		item := &items[itemIndex]
		groupID := firstNonBlank(item.GroupID, item.SceneID)
		if groupID == "" {
			groupID = item.ID
		}
		item.GroupID = groupID
		item.SubgroupID = firstNonBlank(item.SubgroupID, item.ID)
		if item.Kind == "text_phrase" {
			if motionID, motionParams := resolve("important_phrase", groupID, item.SubgroupID); motionID != "" {
				item.MotionID, item.MotionParams = motionID, motionParams
			}
		}
		if item.Kind == "number" {
			target := ""
			switch item.TemplateID {
			case "TIMELINE_DATE_CARD":
				target = "date"
			case "METRIC_STAT_CARD":
				target = "metric"
			}
			if motionID, motionParams := resolve(target, groupID, item.SubgroupID); motionID != "" {
				item.MotionID, item.MotionParams = motionID, motionParams
			}
		}
		if item.EntityCaption != "" {
			if motionID, motionParams := resolve("caption", groupID, item.SubgroupID); motionID != "" {
				item.CaptionMotionID, item.CaptionMotionParams = motionID, motionParams
			}
		}
		for childIndex := range item.ImageLayers {
			child := &item.ImageLayers[childIndex]
			childGroupID := firstNonBlank(child.GroupID, groupID)
			childSubgroupID := firstNonBlank(child.SubgroupID, child.ID)
			child.GroupID = childGroupID
			child.SubgroupID = childSubgroupID
			if motionID, motionParams := resolve("image", childGroupID, childSubgroupID); motionID != "" {
				child.MotionID, child.MotionParams = motionID, motionParams
			}
			if child.Caption != "" {
				if captionMotion, captionParams := resolve("caption", childGroupID, childSubgroupID); captionMotion != "" {
					child.CaptionMotionID = captionMotion
					child.CaptionMotionParams = captionParams
				}
			}
		}
		if isImageOverlayKind(item.Kind) && len(item.ImageLayers) == 0 {
			if motionID, motionParams := resolve("image", groupID, item.SubgroupID); motionID != "" {
				item.MotionID, item.MotionParams = motionID, motionParams
			}
		}
	}
	return nil
}

func seededGroupMotion(target, groupID, seed string, entityImagePool []string) string {
	var pool []string
	switch target {
	case "important_phrase":
		pool = capabilityoverlay.CertifiedPhraseMotions()
	case "image":
		pool = capabilityoverlay.CertifiedSingleImageMotions()
		if len(entityImagePool) > 0 {
			pool = append(pool, entityImagePool...)
		}
		pool = append(pool, capabilityoverlay.CertifiedImageWithTextMotions()...)
		// RenderingGen's standalone and composite image lowering accepts
		// visual-accents recipes against the image target (including light
		// leaks); do not make policy admission depend on automatic motion pools.
		pool = append(pool, capabilityoverlay.CertifiedLightLeakMotions()...)
	case "caption":
		pool = capabilityoverlay.CertifiedAutomaticEntityCaptionMotions()
	case "date":
		pool = capabilityoverlay.DatePresentationMotionCandidates()
	case "metric":
		pool = capabilityoverlay.MetricPresentationMotionCandidates()
	}
	if len(pool) == 0 {
		return ""
	}
	digestValue := digest.SHA256String(seed + "\x00" + target + "\x00" + groupID)
	var index uint64
	for _, digit := range []byte(digestValue[:16]) {
		index = index*16 + uint64(hexNibble(digit))
	}
	return pool[index%uint64(len(pool))]
}

func hexNibble(digit byte) byte {
	if digit >= '0' && digit <= '9' {
		return digit - '0'
	}
	return digit - 'a' + 10
}

func validatePolicyMotionParams(params map[string]any) error {
	for _, name := range []string{"enter_frames", "exit_frames"} {
		value, exists := params[name]
		if !exists {
			continue
		}
		number, ok := value.(float64)
		if !ok {
			if integer, intOK := value.(int); intOK {
				number, ok = float64(integer), true
			}
		}
		if !ok || number != float64(int(number)) || number < 0 || number > 240 {
			return fmt.Errorf("motion_params.%s must be an integer from 0 to 240", name)
		}
	}
	return nil
}

func certifiedPolicyMotion(target, motionID string, entityImagePool []string) bool {
	var pool []string
	switch target {
	case "important_phrase":
		pool = capabilityoverlay.CertifiedPhraseMotions()
	case "image":
		pool = append(capabilityoverlay.CertifiedSingleImageMotions(), entityImagePool...)
		pool = append(pool, capabilityoverlay.CertifiedImageWithTextMotions()...)
		// Visual-accents recipes (including light leaks) are valid for the
		// standalone image target. RenderingGen owns their recipe lowering.
		pool = append(pool, capabilityoverlay.CertifiedLightLeakMotions()...)
	case "caption":
		pool = capabilityoverlay.CertifiedEntityCaptionMotions()
	case "date":
		pool = capabilityoverlay.DatePresentationMotionCandidates()
	case "metric":
		pool = capabilityoverlay.MetricPresentationMotionCandidates()
	}
	for _, id := range pool {
		if id == motionID {
			return true
		}
	}
	return false
}

func clonePolicyParams(params map[string]any) map[string]any {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]any, len(params))
	for key, value := range params {
		out[key] = value
	}
	return out
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
