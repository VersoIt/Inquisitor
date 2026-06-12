package hypothesis

import "strings"

func canonicalize(h Hypothesis) Hypothesis {
	h.Name = strings.TrimSpace(h.Name)
	h.Version = strings.TrimSpace(h.Version)
	h.Status = Status(normalizedStatus(h.Status))
	h.Description = strings.TrimSpace(h.Description)
	h.Thesis = strings.TrimSpace(h.Thesis)
	h.Market.Exchange = strings.ToLower(strings.TrimSpace(h.Market.Exchange))
	h.Market.Category = strings.ToLower(strings.TrimSpace(h.Market.Category))
	h.Market.Symbols = canonicalStrings(h.Market.Symbols, normalizeUpper)
	h.Market.Intervals = canonicalStrings(h.Market.Intervals, strings.TrimSpace)
	h.Regime.Allowed = canonicalStrings(h.Regime.Allowed, normalizeUpper)
	h.Regime.Blocked = canonicalStrings(h.Regime.Blocked, normalizeUpper)
	h.Direction = Direction(normalizedDirection(h.Direction))
	h.Tags = canonicalStrings(h.Tags, normalizeLower)

	for i := range h.Signals {
		h.Signals[i].Name = strings.TrimSpace(h.Signals[i].Name)
		h.Signals[i].Description = strings.TrimSpace(h.Signals[i].Description)
		h.Signals[i].Feature = strings.TrimSpace(h.Signals[i].Feature)
		h.Signals[i].Operator = strings.ToLower(strings.TrimSpace(h.Signals[i].Operator))
		h.Signals[i].Value = Scalar(strings.TrimSpace(h.Signals[i].Value.String()))
	}
	return h
}

func canonicalStrings(values []string, normalize func(string) string) []string {
	if values == nil {
		return nil
	}
	canonical := make([]string, 0, len(values))
	for _, value := range values {
		canonical = append(canonical, normalize(value))
	}
	return canonical
}
