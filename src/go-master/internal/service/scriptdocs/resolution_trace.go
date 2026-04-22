package scriptdocs

import "strings"

func newAssetResolution(resolver string, order ...string) *AssetResolution {
	resolver = strings.TrimSpace(resolver)
	cleaned := make([]string, 0, len(order))
	for _, item := range order {
		item = strings.TrimSpace(item)
		if item != "" {
			cleaned = append(cleaned, item)
		}
	}
	return &AssetResolution{
		Resolver:       resolver,
		SelectionOrder: cleaned,
	}
}

func (r *AssetResolution) withOutcome(selectedFrom, reason string, cached bool) *AssetResolution {
	if r == nil {
		return nil
	}
	r.SelectedFrom = strings.TrimSpace(selectedFrom)
	r.Reason = strings.TrimSpace(reason)
	r.Cached = cached
	return r
}

func (r *AssetResolution) addNote(note string) *AssetResolution {
	if r == nil {
		return nil
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return r
	}
	r.Notes = append(r.Notes, note)
	return r
}

func cloneAssetResolution(res *AssetResolution) *AssetResolution {
	if res == nil {
		return nil
	}
	copy := *res
	if len(res.SelectionOrder) > 0 {
		copy.SelectionOrder = append([]string(nil), res.SelectionOrder...)
	}
	if len(res.Notes) > 0 {
		copy.Notes = append([]string(nil), res.Notes...)
	}
	return &copy
}
