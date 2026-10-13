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
// The optional trailing pool is the run-level entity-caption pool
// (entity_caption_motions): it drives seeded caption choices and the
// automatic caption rotation. Empty keeps the certified automatic pool.
func applyAnimationPolicies(items []capabilityoverlay.OverlayItem, policies []scriptpkg.AnimationPolicy, planID, videoID string, entityImagePool []string, entityCaptionPools ...[]string) error {
	var entityCaptionPool []string
	if len(entityCaptionPools) > 0 {
		entityCaptionPool = entityCaptionPools[0]
	}
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
	// seededGroups marks group motions that came from a seeded draw rather
	// than an exact payload choice, so word-count mismatches re-seed seeded
	// draws per item while explicit choices fail closed.
	seededGroups := make(map[animationGroupKey]bool)
	seed := digest.SHA256String(planID + "\x00" + videoID)
	declaredGroups := make(map[animationGroupKey]struct{}, len(policies))
	for _, policy := range policies {
		if policy.SubgroupID == "" {
			declaredGroups[animationGroupKey{target: policy.Target, groupID: policy.GroupID}] = struct{}{}
		}
	}
	// resolve maps one item onto its policy motion. words carries the phrase
	// word count for important_phrase targets so seeded choices land in the
	// item's exact-cardinality word group; other targets pass 0. explicit
	// reports whether the motion was an exact payload choice (as opposed to
	// a seeded group draw) so callers can fail closed on explicit mismatches
	// while re-seeding seeded draws per item.
	resolve := func(target, groupID, subgroupID string, words int) (motionID string, params map[string]any, explicit bool) {
		if groupID == "" {
			return "", nil, false
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
				policy.MotionID = seededGroupMotion(target, groupID, seed+"\x00"+selectionSubgroup, words, entityImagePool, entityCaptionPool)
				if policy.MotionID == "" {
					return "", nil, false
				}
				policy.MotionParams = clonePolicyParams(policy.MotionParams)
				groupMotions[key] = policy
				seededGroups[key] = true
				return policy.MotionID, policy.MotionParams, false
			}
			policy.MotionParams = clonePolicyParams(policy.MotionParams)
			groupMotions[key] = policy
			return policy.MotionID, policy.MotionParams, true
		}
		fallbackKey := animationGroupKey{target: target, groupID: groupID}
		if motion, ok := groupMotions[fallbackKey]; ok {
			return motion.MotionID, clonePolicyParams(motion.MotionParams), !seededGroups[fallbackKey]
		}
		if _, declared := declaredGroups[fallbackKey]; declared {
			if motionID := seededGroupMotion(target, groupID, seed, words, entityImagePool, entityCaptionPool); motionID != "" {
				groupMotions[fallbackKey] = scriptpkg.AnimationPolicy{Target: target, GroupID: groupID, MotionID: motionID}
				seededGroups[fallbackKey] = true
				return motionID, nil, false
			}
		}
		return "", nil, false
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
			words := len(strings.Fields(item.Text))
			if motionID, motionParams, explicit := resolve("important_phrase", groupID, item.SubgroupID, words); motionID != "" {
				if err := checkPolicyPhraseMotionCompatible(*item, motionID); err != nil {
					if explicit {
						return fmt.Errorf("animation policy for target %q group %q: %w", "important_phrase", groupID, err)
					}
					// A seeded group draw is word-locked: the cached group
					// motion can belong to a sibling's word count. Re-seed
					// this item from its own count instead of failing the
					// plan; uniformity cannot hold across word counts
					// because recipes are exact-cardinality.
					motionID = seededGroupMotion("important_phrase", groupID, seed+"\x00"+item.SubgroupID, words, entityImagePool, entityCaptionPool)
					if motionID == "" {
						return fmt.Errorf("animation policy for target %q group %q: no seeded motion for %d-word phrase", "important_phrase", groupID, words)
					}
					motionParams = nil
					if err := checkPolicyPhraseMotionCompatible(*item, motionID); err != nil {
						return fmt.Errorf("animation policy for target %q group %q: %w", "important_phrase", groupID, err)
					}
				}
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
			if motionID, motionParams, _ := resolve(target, groupID, item.SubgroupID, 0); motionID != "" {
				item.MotionID, item.MotionParams = motionID, motionParams
			}
		}
		if item.EntityCaption != "" {
			if motionID, motionParams, _ := resolve("caption", groupID, item.SubgroupID, 0); motionID != "" {
				item.CaptionMotionID, item.CaptionMotionParams = motionID, motionParams
			}
		}
		for childIndex := range item.ImageLayers {
			child := &item.ImageLayers[childIndex]
			childGroupID := firstNonBlank(child.GroupID, groupID)
			childSubgroupID := firstNonBlank(child.SubgroupID, child.ID)
			child.GroupID = childGroupID
			child.SubgroupID = childSubgroupID
			if motionID, motionParams, _ := resolve("image", childGroupID, childSubgroupID, 0); motionID != "" {
				child.MotionID, child.MotionParams = motionID, motionParams
			}
			if child.Caption != "" {
				if captionMotion, captionParams, _ := resolve("caption", childGroupID, childSubgroupID, 0); captionMotion != "" {
					child.CaptionMotionID = captionMotion
					child.CaptionMotionParams = captionParams
				}
			}
		}
		if isImageOverlayKind(item.Kind) && len(item.ImageLayers) == 0 {
			if motionID, motionParams, _ := resolve("image", groupID, item.SubgroupID, 0); motionID != "" {
				item.MotionID, item.MotionParams = motionID, motionParams
			}
		}
	}
	return nil
}

// checkPolicyPhraseMotionCompatible fails closed when a policy motion cannot
// render the phrase wording. It classifies the real item with the same
// EditorialSectionForItem routing the automatic planner uses: short-lane
// items (1-5 words) only accept their exact word-count group; longer copy
// only rejects the exact-cardinality short recipes, which would mis-render
// it.
func checkPolicyPhraseMotionCompatible(item capabilityoverlay.OverlayItem, motionID string) error {
	words := len(strings.Fields(item.Text))
	if capabilityoverlay.EditorialSectionForItem(item) == capabilityoverlay.EditorialSectionShortPhrase {
		// Membership is checked against the item's own word-count group: the
		// groups may share motions, so a reverse lookup would report the first
		// group instead of the one the phrase actually belongs to.
		for _, id := range capabilityoverlay.ShortPhraseMotionsForWordCount(words) {
			if id == motionID {
				return nil
			}
		}
		return fmt.Errorf("motion %q is not in the %d-word short-phrase group for %q", motionID, words, item.Text)
	}
	if capabilityoverlay.IsShortPhraseRecipe(motionID) {
		return fmt.Errorf("motion %q is an exact-cardinality short-phrase recipe, incompatible with %d-word phrase %q", motionID, words, item.Text)
	}
	return nil
}

func seededGroupMotion(target, groupID, seed string, words int, entityImagePool, entityCaptionPool []string) string {
	var pool []string
	switch target {
	case "important_phrase":
		// Short-lane phrases draw from their exact word-count group, like
		// the automatic planner; longer copy walks the general catalog.
		if words >= 1 && words <= 5 {
			pool = capabilityoverlay.ShortPhraseMotionsForWordCount(words)
		} else {
			pool = capabilityoverlay.CertifiedPhraseMotions()
		}
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
		// A run-level entity_caption_motions pool overrides the automatic
		// camera-safe pool; it is validated fail-closed where assigned.
		if len(entityCaptionPool) > 0 {
			pool = entityCaptionPool
		} else {
			pool = capabilityoverlay.CertifiedAutomaticEntityCaptionMotions()
		}
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
	// A retired animation is refused for every target, even when a payload
	// names one explicitly: no selector may emit it again.
	if capabilityoverlay.IsRemovedGeneratedPhraseMotion(motionID) {
		return false
	}
	var pool []string
	switch target {
	case "important_phrase":
		// Short-lane recipes live outside the general catalog; admit the
		// exact-cardinality groups too. Word-count compatibility is enforced
		// per item at application time.
		if _, ok := capabilityoverlay.ShortPhraseMotionWordCount(motionID); ok {
			return true
		}
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
