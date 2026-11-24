package rustexec

import (
	"fmt"
	"strings"
	"unicode/utf8"

	semantic "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/semantic"
)

func projectSemanticQuantities(source string, quantities []semanticEngineQuantity, comparisons []semanticEngineQuantityComparison) ([]semantic.SemanticQuantity, []semantic.SemanticQuantityComparison, error) {
	out := make([]semantic.SemanticQuantity, 0, len(quantities))
	byID := make(map[string]semantic.SemanticQuantity, len(quantities))
	for _, quantity := range quantities {
		if quantity.ID == "" || quantity.Dimension == "" || quantity.Unit == "" || quantity.UnitSurface == "" {
			return nil, nil, fmt.Errorf("quantity %q omits its identity, dimension, or unit", quantity.ID)
		}
		if !groundedSpan(source, quantity.StartByte, quantity.EndByte, quantity.Evidence) {
			return nil, nil, fmt.Errorf("quantity %q is not grounded in source_text", quantity.ID)
		}
		unitWindowEnd := quantity.EndByte + 64
		if unitWindowEnd > len(source) {
			unitWindowEnd = len(source)
		}
		if !strings.Contains(source[quantity.StartByte:unitWindowEnd], quantity.UnitSurface) {
			return nil, nil, fmt.Errorf("quantity %q unit surface %q is not grounded after its numeric span", quantity.ID, quantity.UnitSurface)
		}
		if quantity.Value != nil && !isFinite(*quantity.Value) || quantity.RawValue != nil && !isFinite(*quantity.RawValue) {
			return nil, nil, fmt.Errorf("quantity %q has a non-finite numeric value", quantity.ID)
		}
		if quantity.Scale != nil && (!isFinite(*quantity.Scale) || *quantity.Scale <= 0) {
			return nil, nil, fmt.Errorf("quantity %q has an invalid scale", quantity.ID)
		}
		if quantity.Period != nil && (quantity.Period.Year < 1 || quantity.Period.Year > 9999 || quantity.Period.FactIndex < 0) {
			return nil, nil, fmt.Errorf("quantity %q has an invalid period", quantity.ID)
		}
		candidate := semantic.SemanticQuantity{
			ID: quantity.ID, Dimension: quantity.Dimension, Unit: quantity.Unit,
			UnitSurface: quantity.UnitSurface, Qualifier: quantity.Qualifier,
			Evidence: quantity.Evidence, Status: quantity.Status,
			Value: quantity.Value, RawValue: quantity.RawValue, Scale: quantity.Scale,
			SentenceIndex: quantity.SentenceIndex, Start: quantity.StartByte, End: quantity.EndByte,
		}
		if quantity.RangeID != nil {
			candidate.RangeID = *quantity.RangeID
		}
		if quantity.Period != nil {
			candidate.Period = &semantic.SemanticQuantityPeriod{Year: quantity.Period.Year, FactIndex: quantity.Period.FactIndex}
		}
		if quantity.SubjectEntityID != nil {
			candidate.SubjectEntityID = *quantity.SubjectEntityID
		}
		if quantity.Predicate != nil {
			candidate.Predicate = *quantity.Predicate
		}
		if _, duplicate := byID[candidate.ID]; duplicate {
			return nil, nil, fmt.Errorf("duplicate quantity id %q", candidate.ID)
		}
		byID[candidate.ID] = candidate
		out = append(out, candidate)
	}

	projectedComparisons := make([]semantic.SemanticQuantityComparison, 0, len(comparisons))
	for _, comparison := range comparisons {
		left, hasLeft := byID[comparison.Left]
		right, hasRight := byID[comparison.Right]
		if !hasLeft || !hasRight || comparison.Left == comparison.Right || comparison.Dimension == "" ||
			left.Dimension != comparison.Dimension || right.Dimension != comparison.Dimension || left.Unit != right.Unit {
			return nil, nil, fmt.Errorf("quantity comparison %q/%q has incompatible references", comparison.Left, comparison.Right)
		}
		if !groundedSpan(source, comparison.EvidenceStartByte, comparison.EvidenceEndByte,
			sourceSpan(source, comparison.EvidenceStartByte, comparison.EvidenceEndByte)) {
			return nil, nil, fmt.Errorf("quantity comparison %q/%q has an invalid evidence span", comparison.Left, comparison.Right)
		}
		if comparison.Ratio != nil && (!isFinite(*comparison.Ratio) || *comparison.Ratio < 0) ||
			comparison.Difference != nil && !isFinite(*comparison.Difference) {
			return nil, nil, fmt.Errorf("quantity comparison %q/%q has non-finite arithmetic", comparison.Left, comparison.Right)
		}
		if comparison.Dimension == "currency" && (left.Period == nil || right.Period == nil || left.Period.Year != right.Period.Year) {
			return nil, nil, fmt.Errorf("currency comparison %q/%q has mismatched periods", comparison.Left, comparison.Right)
		}
		start, end := comparison.EvidenceStartByte, comparison.EvidenceEndByte
		projectedComparisons = append(projectedComparisons, semantic.SemanticQuantityComparison{
			Kind: comparison.Kind, Left: comparison.Left, Right: comparison.Right,
			Dimension: comparison.Dimension, Evidence: source[start:end],
			Ratio: comparison.Ratio, Difference: comparison.Difference,
			EvidenceStart: start, EvidenceEnd: end,
		})
	}
	return out, projectedComparisons, nil
}

func sourceSpan(source string, start, end int) string {
	if start < 0 || end <= start || end > len(source) ||
		(start < len(source) && !utf8.RuneStart(source[start])) ||
		(end < len(source) && !utf8.RuneStart(source[end])) {
		return ""
	}
	return source[start:end]
}
