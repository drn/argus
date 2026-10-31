package pricing

// TokenDeltas is one accrual increment's five raw-count classes — the
// difference between a freshly-resummed transcript total and the
// previously-persisted total for the same binding (design.md Decision 2).
type TokenDeltas struct {
	Input        int64
	CacheWrite1h int64
	CacheWrite5m int64
	CacheRead    int64
	Output       int64
}

// PriceDelta prices a single accrual increment against t, keyed by the same
// model-alias string RoleView.AppliedModel already produces. ok is false
// when model has no entry in t — the caller MUST treat that as "not
// priced," never zero (design.md Decision 3/6): the increment is skipped,
// not counted at $0.
func (t *Table) PriceDelta(model string, d TokenDeltas) (usd float64, ok bool) {
	rate, found := t.Models[model]
	if !found {
		return 0, false
	}
	return rate.price(d), true
}

// PriceCall prices ONE provider API call (add-codex-cost-accrual Decision 3):
// like PriceDelta, but a call whose total input tokens (cached included)
// exceed the model's LongContextThreshold is priced at its LongContext rates.
// ok is false when model has no entry in t, with the same "not priced, never
// zero" contract as PriceDelta.
func (t *Table) PriceCall(model string, d TokenDeltas, callInputTokens int64) (usd float64, ok bool) {
	rate, found := t.Models[model]
	if !found {
		return 0, false
	}
	if rate.LongContext != nil && rate.LongContextThreshold > 0 && callInputTokens > rate.LongContextThreshold {
		return rate.LongContext.price(d), true
	}
	return rate.price(d), true
}

func (r Rate) price(d TokenDeltas) float64 {
	const perToken = 1.0 / 1_000_000
	return float64(d.Input)*r.Input*perToken +
		float64(d.CacheWrite1h)*r.CacheWrite1h*perToken +
		float64(d.CacheWrite5m)*r.CacheWrite5m*perToken +
		float64(d.CacheRead)*r.CacheRead*perToken +
		float64(d.Output)*r.Output*perToken
}
