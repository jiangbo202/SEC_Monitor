package discovery

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

const profitHistoryYears = 3

// ProfitHistory is a compact, SEC-derived net-income history for a single
// issuer. Quarterly values use reported three-month periods where available;
// Q4 is derived from an annual filing only when the preceding three quarters
// for that same fiscal year are present.
type ProfitHistory struct {
	Ticker    string               `json:"ticker"`
	AsOf      time.Time            `json:"as_of"`
	Quarterly []ProfitHistoryPoint `json:"quarterly"`
	Annual    []ProfitHistoryPoint `json:"annual"`
}

type ProfitHistoryPoint struct {
	Period       string    `json:"period"`
	PeriodStart  time.Time `json:"period_start"`
	PeriodEnd    time.Time `json:"period_end"`
	NetIncomeUSD float64   `json:"net_income_usd"`
	Form         string    `json:"form"`
	Concept      string    `json:"concept"`
	SourceURL    string    `json:"source_url"`
	Derived      bool      `json:"derived"`
}

func GetProfitHistory(ctx context.Context, db *gorm.DB, ticker string) (ProfitHistory, error) {
	result := ProfitHistory{Ticker: strings.ToUpper(strings.TrimSpace(ticker)), Quarterly: []ProfitHistoryPoint{}, Annual: []ProfitHistoryPoint{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	if result.Ticker == "" {
		return result, errors.New("ticker is required")
	}
	var identity SecurityBatchIdentity
	err := db.WithContext(ctx).
		Where("ticker = ?", result.Ticker).
		Order("created_at DESC, id DESC").
		First(&identity).Error
	if err != nil {
		return result, err
	}
	return getProfitHistoryForSecurity(ctx, db, identity.SecurityID, result.Ticker, time.Now().UTC())
}

func getProfitHistoryForSecurity(ctx context.Context, db *gorm.DB, securityID uint, ticker string, asOf time.Time) (ProfitHistory, error) {
	result := ProfitHistory{Ticker: strings.ToUpper(strings.TrimSpace(ticker)), AsOf: asOf, Quarterly: []ProfitHistoryPoint{}, Annual: []ProfitHistoryPoint{}}
	if securityID == 0 {
		return result, errors.New("security is required")
	}
	if asOf.IsZero() {
		asOf = time.Now().UTC()
		result.AsOf = asOf
	}
	cutoff := asOf.AddDate(-profitHistoryYears, 0, 0)
	var facts []FinancialFactSnapshot
	if err := db.WithContext(ctx).
		Where("security_id = ? AND metric = ? AND quality_status = ? AND period_end >= ?", securityID, FinancialMetricNetIncomeCommon, QualityStatusValid, cutoff).
		Order("period_end ASC, filed_at DESC, accepted_at DESC, id DESC").
		Find(&facts).Error; err != nil {
		return result, err
	}
	result.Quarterly, result.Annual = buildProfitHistory(facts, cutoff)
	return result, nil
}

func buildProfitHistory(facts []FinancialFactSnapshot, cutoff time.Time) ([]ProfitHistoryPoint, []ProfitHistoryPoint) {
	quarterlyFacts := map[string]FinancialFactSnapshot{}
	annualFacts := map[string]FinancialFactSnapshot{}
	for _, fact := range facts {
		if fact.PeriodStart.IsZero() || fact.PeriodEnd.IsZero() || fact.PeriodEnd.Before(cutoff) {
			continue
		}
		days := financialPeriodDays(fact)
		key := fact.PeriodEnd.Format(time.DateOnly)
		switch {
		case days >= 70 && days <= 120:
			if prior, ok := quarterlyFacts[key]; !ok || financialFactIsNewer(fact, prior) {
				quarterlyFacts[key] = fact
			}
		case days >= 300 && days <= 400:
			if prior, ok := annualFacts[key]; !ok || financialFactIsNewer(fact, prior) {
				annualFacts[key] = fact
			}
		}
	}
	quarterly := make([]ProfitHistoryPoint, 0, len(quarterlyFacts)+len(annualFacts))
	for _, fact := range quarterlyFacts {
		quarterly = append(quarterly, profitHistoryPoint(fact, quarterLabel(fact.PeriodEnd), false))
	}
	annual := make([]ProfitHistoryPoint, 0, len(annualFacts))
	for _, fact := range annualFacts {
		annual = append(annual, profitHistoryPoint(fact, "FY "+fact.PeriodEnd.Format("2006"), false))
		if q4, ok := derivedFourthQuarter(fact, quarterly); ok {
			quarterly = append(quarterly, q4)
		}
	}
	sort.Slice(quarterly, func(i, j int) bool { return quarterly[i].PeriodEnd.Before(quarterly[j].PeriodEnd) })
	sort.Slice(annual, func(i, j int) bool { return annual[i].PeriodEnd.Before(annual[j].PeriodEnd) })
	return quarterly, annual
}

func financialPeriodDays(fact FinancialFactSnapshot) int {
	return int(fact.PeriodEnd.Sub(fact.PeriodStart).Hours()/24) + 1
}

func financialFactIsNewer(candidate, current FinancialFactSnapshot) bool {
	if candidate.FiledAt.After(current.FiledAt) {
		return true
	}
	if candidate.FiledAt.Before(current.FiledAt) {
		return false
	}
	if candidate.AcceptedAt.After(current.AcceptedAt) {
		return true
	}
	if candidate.AcceptedAt.Before(current.AcceptedAt) {
		return false
	}
	return candidate.ID > current.ID
}

func profitHistoryPoint(fact FinancialFactSnapshot, label string, derived bool) ProfitHistoryPoint {
	return ProfitHistoryPoint{
		Period: label, PeriodStart: fact.PeriodStart, PeriodEnd: fact.PeriodEnd,
		NetIncomeUSD: float64(fact.AmountMicros) / 1_000_000, Form: fact.Form,
		Concept: fact.Concept, SourceURL: fact.SourceURL, Derived: derived,
	}
}

func quarterLabel(periodEnd time.Time) string {
	quarter := (int(periodEnd.Month())-1)/3 + 1
	return periodEnd.Format("2006") + " Q" + string(rune('0'+quarter))
}

func derivedFourthQuarter(annual FinancialFactSnapshot, quarters []ProfitHistoryPoint) (ProfitHistoryPoint, bool) {
	items := make([]ProfitHistoryPoint, 0, 3)
	for _, quarter := range quarters {
		if quarter.PeriodStart.Before(annual.PeriodStart) || quarter.PeriodEnd.After(annual.PeriodEnd) {
			continue
		}
		items = append(items, quarter)
	}
	if len(items) != 3 {
		return ProfitHistoryPoint{}, false
	}
	var sum float64
	for _, item := range items {
		sum += item.NetIncomeUSD
	}
	return ProfitHistoryPoint{
		Period: annual.PeriodEnd.Format("2006") + " Q4", PeriodStart: items[len(items)-1].PeriodEnd.AddDate(0, 0, 1), PeriodEnd: annual.PeriodEnd,
		NetIncomeUSD: float64(annual.AmountMicros)/1_000_000 - sum, Form: annual.Form, Concept: annual.Concept, SourceURL: annual.SourceURL, Derived: true,
	}, true
}
