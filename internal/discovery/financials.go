package discovery

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"time"
)

const FinancialParserVersion = "financial-facts-v1"

const (
	FinancialMetricRevenue              = "revenue"
	FinancialMetricCash                 = "cash"
	FinancialMetricShortTermInvestments = "short_term_investments"
	FinancialMetricOperatingCashFlow    = "operating_cash_flow"
	FinancialMetricCapitalExpenditure   = "capital_expenditure"
	FinancialMetricGrossProfit          = "gross_profit"
	FinancialMetricCostOfRevenue        = "cost_of_revenue"
	FinancialMetricNetIncomeCommon      = "net_income_common"
	FinancialMetricDebtCurrent          = "debt_current"
)

type FinancialFact struct {
	CIK, Metric, Concept, Unit, Form, Accession string
	PeriodStart, PeriodEnd, FiledAt, AcceptedAt time.Time
	AmountMicros                                int64
	SourceURL                                   string
}

type FinancialFactSource interface {
	LoadFinancialFacts(context.Context, map[string]struct{}) ([]FinancialFact, SourceVersion, error)
}

type FinancialSummary struct {
	RevenueGrowthAvailable bool
	RunwayAvailable        bool
	QualityFlags           []string

	LatestQuarterRevenueUSD    int64
	PriorYearQuarterRevenueUSD int64
	QuarterlyRevenueYoYPct     float64
	LatestAnnualRevenueUSD     int64
	PriorAnnualRevenueUSD      int64
	AnnualRevenueYoYPct        float64

	AvailableCashUSD         float64
	TTMOperatingCashFlowUSD  float64
	TTMCapitalExpenditureUSD float64
	CFOBurnMonthlyUSD        float64
	FCFBurnMonthlyUSD        float64
	CashRunwayMonths         float64
}

type financialConceptSpec struct {
	Metric, Namespace, Concept, Unit string
	Instant, PositiveAbs             bool
	Priority                         int
}

var financialConceptsV1 = []financialConceptSpec{
	{Metric: FinancialMetricRevenue, Namespace: "us-gaap", Concept: "RevenueFromContractWithCustomerExcludingAssessedTax", Unit: "USD", Priority: 1},
	{Metric: FinancialMetricRevenue, Namespace: "us-gaap", Concept: "Revenues", Unit: "USD", Priority: 2},
	{Metric: FinancialMetricRevenue, Namespace: "us-gaap", Concept: "SalesRevenueNet", Unit: "USD", Priority: 3},
	{Metric: FinancialMetricCash, Namespace: "us-gaap", Concept: "CashAndCashEquivalentsAtCarryingValue", Unit: "USD", Instant: true, Priority: 1},
	{Metric: FinancialMetricCash, Namespace: "us-gaap", Concept: "Cash", Unit: "USD", Instant: true, Priority: 2},
	{Metric: FinancialMetricShortTermInvestments, Namespace: "us-gaap", Concept: "ShortTermInvestments", Unit: "USD", Instant: true, Priority: 1},
	{Metric: FinancialMetricShortTermInvestments, Namespace: "us-gaap", Concept: "MarketableSecuritiesCurrent", Unit: "USD", Instant: true, Priority: 2},
	{Metric: FinancialMetricShortTermInvestments, Namespace: "us-gaap", Concept: "AvailableForSaleSecuritiesCurrent", Unit: "USD", Instant: true, Priority: 3},
	{Metric: FinancialMetricOperatingCashFlow, Namespace: "us-gaap", Concept: "NetCashProvidedByUsedInOperatingActivitiesContinuingOperations", Unit: "USD", Priority: 1},
	{Metric: FinancialMetricOperatingCashFlow, Namespace: "us-gaap", Concept: "NetCashProvidedByUsedInOperatingActivities", Unit: "USD", Priority: 2},
	{Metric: FinancialMetricCapitalExpenditure, Namespace: "us-gaap", Concept: "PaymentsToAcquirePropertyPlantAndEquipment", Unit: "USD", PositiveAbs: true, Priority: 1},
	{Metric: FinancialMetricCapitalExpenditure, Namespace: "us-gaap", Concept: "PaymentsForAdditionsToPropertyPlantAndEquipment", Unit: "USD", PositiveAbs: true, Priority: 2},
	{Metric: FinancialMetricGrossProfit, Namespace: "us-gaap", Concept: "GrossProfit", Unit: "USD", Priority: 1},
	{Metric: FinancialMetricCostOfRevenue, Namespace: "us-gaap", Concept: "CostOfRevenue", Unit: "USD", PositiveAbs: true, Priority: 1},
	{Metric: FinancialMetricCostOfRevenue, Namespace: "us-gaap", Concept: "CostOfGoodsAndServicesSold", Unit: "USD", PositiveAbs: true, Priority: 2},
	{Metric: FinancialMetricCostOfRevenue, Namespace: "us-gaap", Concept: "CostOfGoodsSold", Unit: "USD", PositiveAbs: true, Priority: 3},
	{Metric: FinancialMetricNetIncomeCommon, Namespace: "us-gaap", Concept: "NetIncomeLossAvailableToCommonStockholdersBasic", Unit: "USD", Priority: 1},
	{Metric: FinancialMetricNetIncomeCommon, Namespace: "us-gaap", Concept: "NetIncomeLoss", Unit: "USD", Priority: 2},
	{Metric: FinancialMetricDebtCurrent, Namespace: "us-gaap", Concept: "LongTermDebtCurrent", Unit: "USD", Instant: true, Priority: 1},
	{Metric: FinancialMetricDebtCurrent, Namespace: "us-gaap", Concept: "CurrentPortionOfLongTermDebt", Unit: "USD", Instant: true, Priority: 2},
	{Metric: FinancialMetricDebtCurrent, Namespace: "us-gaap", Concept: "ShortTermBorrowings", Unit: "USD", Instant: true, Priority: 3},
	{Metric: FinancialMetricDebtCurrent, Namespace: "us-gaap", Concept: "ShortTermDebtCurrent", Unit: "USD", Instant: true, Priority: 4},
}

func ParseSECFinancialFactsZIP(z *zip.Reader, allowed map[string]struct{}, limits ZIPParseLimits) ([]FinancialFact, error) {
	if err := validateParseLimits(limits); err != nil {
		return nil, err
	}
	specs := financialSpecsByConcept()
	out := []FinancialFact{}
	entriesByCIK := map[string][]FinancialFact{}
	var total int64
	for i, f := range z.File {
		if f.FileInfo().IsDir() {
			if !safeZIPName(f.Name) {
				return nil, fmt.Errorf("invalid SEC companyfacts ZIP directory %q", f.Name)
			}
			continue
		}
		if limits.MaxEntries > 0 && i >= limits.MaxEntries {
			return nil, fmt.Errorf("ZIP entry count exceeds limit")
		}
		m := secZIPName.FindStringSubmatch(f.Name)
		if m == nil {
			return nil, fmt.Errorf("invalid SEC companyfacts ZIP entry %q", f.Name)
		}
		if _, ok := allowed[m[1]]; !ok {
			continue
		}
		if err := checkDeclaredZIPSize(f, limits.MaxEntryBytes, limits.MaxTotalBytes-total); err != nil {
			return nil, err
		}
		var doc companyFinancialFactsDocument
		n, err := decodeZIPJSON(f, limits.MaxEntryBytes, &doc, true)
		if err != nil {
			return nil, fmt.Errorf("companyfacts CIK %s: %w", m[1], err)
		}
		if n > limits.MaxTotalBytes-total {
			return nil, fmt.Errorf("ZIP aggregate decoded bytes exceed limit")
		}
		total += n
		cik, err := normalizeCIK(doc.CIK)
		if err != nil || cik != m[1] {
			return nil, fmt.Errorf("companyfacts CIK %s invalid cik", m[1])
		}
		entryFacts := []FinancialFact{}
		seen := map[string]FinancialFact{}
		for namespace, concepts := range doc.Facts {
			for conceptName, concept := range concepts {
				spec, ok := specs[namespace+":"+conceptName]
				if !ok {
					continue
				}
				for unit, facts := range concept.Units {
					if unit != spec.Unit {
						return nil, fmt.Errorf("companyfacts %s/%s:%s: invalid unit %q", cik, namespace, conceptName, unit)
					}
					for _, x := range facts {
						fact, err := financialFactFromCompanyFact(cik, namespace, conceptName, spec, x)
						if err != nil {
							return nil, err
						}
						key := strings.Join([]string{fact.CIK, fact.Concept, fact.PeriodStart.Format(time.DateOnly), fact.PeriodEnd.Format(time.DateOnly), fact.Accession}, "|")
						if old, ok := seen[key]; ok {
							if old != fact {
								return nil, fmt.Errorf("companyfacts %s conflicting duplicate financial fact", fact.Concept)
							}
							continue
						}
						seen[key] = fact
						entryFacts = append(entryFacts, fact)
					}
				}
			}
		}
		sortFinancialFacts(entryFacts)
		if old, ok := entriesByCIK[cik]; ok {
			if !reflect.DeepEqual(old, entryFacts) {
				return nil, fmt.Errorf("companyfacts CIK %s conflicting duplicate archive entry", cik)
			}
			continue
		}
		entriesByCIK[cik] = entryFacts
		out = append(out, entryFacts...)
	}
	sortFinancialFacts(out)
	return out, nil
}

type companyFinancialFactsDocument struct {
	CIK   json.RawMessage                           `json:"cik"`
	Facts map[string]map[string]companyFactsConcept `json:"facts"`
}

func financialFactFromCompanyFact(cik, namespace, conceptName string, spec financialConceptSpec, x companyFactsFact) (FinancialFact, error) {
	context := cik + "/" + namespace + ":" + conceptName
	end, err := time.Parse(time.DateOnly, x.End)
	if err != nil {
		return FinancialFact{}, fmt.Errorf("companyfacts %s: invalid end date", context)
	}
	var start time.Time
	if !spec.Instant {
		start, err = time.Parse(time.DateOnly, x.Start)
		if err != nil {
			return FinancialFact{}, fmt.Errorf("companyfacts %s: invalid start date", context)
		}
	}
	filed, err := time.Parse(time.DateOnly, x.Filed)
	if err != nil {
		return FinancialFact{}, fmt.Errorf("companyfacts %s: invalid filed date", context)
	}
	amount, err := parseDecimalMicros(x.Val)
	if err != nil {
		return FinancialFact{}, fmt.Errorf("companyfacts %s: %w", context, err)
	}
	if spec.PositiveAbs && amount < 0 {
		amount = -amount
	}
	source := "https://data.sec.gov/api/xbrl/companyfacts/CIK" + cik + ".json"
	if x.Accn != "" {
		source = "https://www.sec.gov/Archives/edgar/data/" + strings.TrimLeft(cik, "0") + "/" + strings.ReplaceAll(x.Accn, "-", "") + "/"
	}
	return FinancialFact{CIK: cik, Metric: spec.Metric, Concept: namespace + ":" + conceptName, Unit: spec.Unit, PeriodStart: start, PeriodEnd: end, FiledAt: filed, AmountMicros: amount, Form: x.Form, Accession: x.Accn, SourceURL: source}, nil
}

func BuildFinancialSummary(facts []FinancialFact, asOf time.Time) FinancialSummary {
	facts = selectLatestFinancialFacts(facts, asOf)
	out := FinancialSummary{}
	latestQ, okLatestQ := latestDurationFact(facts, FinancialMetricRevenue, 75, 105)
	if okLatestQ {
		priorQ, okPriorQ := matchingYearAgoFact(facts, latestQ, FinancialMetricRevenue, 75, 105)
		if okPriorQ && priorQ.AmountMicros > 0 {
			out.LatestQuarterRevenueUSD = latestQ.AmountMicros / 1_000_000
			out.PriorYearQuarterRevenueUSD = priorQ.AmountMicros / 1_000_000
			out.QuarterlyRevenueYoYPct = (float64(latestQ.AmountMicros)/float64(priorQ.AmountMicros) - 1) * 100
			out.RevenueGrowthAvailable = true
			if out.LatestQuarterRevenueUSD < 5_000_000 {
				out.QualityFlags = append(out.QualityFlags, "low_revenue_base")
			}
			if out.QuarterlyRevenueYoYPct > 200 {
				out.QualityFlags = append(out.QualityFlags, "extreme_revenue_growth")
			}
		}
	}
	latestAnnual, okLatestAnnual := latestDurationFact(facts, FinancialMetricRevenue, 330, 400)
	if okLatestAnnual {
		priorAnnual, okPriorAnnual := matchingPriorAnnualFact(facts, latestAnnual, FinancialMetricRevenue)
		if okPriorAnnual && priorAnnual.AmountMicros > 0 {
			out.LatestAnnualRevenueUSD = latestAnnual.AmountMicros / 1_000_000
			out.PriorAnnualRevenueUSD = priorAnnual.AmountMicros / 1_000_000
			out.AnnualRevenueYoYPct = (float64(latestAnnual.AmountMicros)/float64(priorAnnual.AmountMicros) - 1) * 100
		}
	}
	cash, okCash := latestInstantAmount(facts, FinancialMetricCash)
	investments, _ := latestInstantAmount(facts, FinancialMetricShortTermInvestments)
	ttmCFO, okCFO := latestTTMAmount(facts, FinancialMetricOperatingCashFlow)
	ttmCapex, okCapex := latestTTMAmount(facts, FinancialMetricCapitalExpenditure)
	if okCash && okCFO {
		out.AvailableCashUSD = float64(cash+investments) / 1_000_000
		out.TTMOperatingCashFlowUSD = float64(ttmCFO) / 1_000_000
		out.CFOBurnMonthlyUSD = math.Max(0, -out.TTMOperatingCashFlowUSD/12)
		if okCapex {
			out.TTMCapitalExpenditureUSD = float64(ttmCapex) / 1_000_000
			out.FCFBurnMonthlyUSD = math.Max(0, -(out.TTMOperatingCashFlowUSD-out.TTMCapitalExpenditureUSD)/12)
		}
		conservativeBurn := math.Max(out.CFOBurnMonthlyUSD, out.FCFBurnMonthlyUSD)
		if conservativeBurn == 0 {
			out.CashRunwayMonths = math.Inf(1)
			out.RunwayAvailable = true
		} else if out.AvailableCashUSD > 0 {
			out.CashRunwayMonths = out.AvailableCashUSD / conservativeBurn
			out.RunwayAvailable = true
		}
	}
	return out
}

func financialSpecsByConcept() map[string]financialConceptSpec {
	out := map[string]financialConceptSpec{}
	for _, spec := range financialConceptsV1 {
		out[spec.Namespace+":"+spec.Concept] = spec
	}
	return out
}

func selectLatestFinancialFacts(facts []FinancialFact, asOf time.Time) []FinancialFact {
	byIdentity := map[string]FinancialFact{}
	for _, fact := range facts {
		if !fact.AcceptedAt.IsZero() && fact.AcceptedAt.After(asOf) {
			continue
		}
		key := strings.Join([]string{fact.CIK, fact.Metric, fact.Concept, fact.PeriodStart.Format(time.DateOnly), fact.PeriodEnd.Format(time.DateOnly)}, "|")
		old, ok := byIdentity[key]
		if !ok || factFiledAfter(fact, old) {
			byIdentity[key] = fact
		}
	}
	out := make([]FinancialFact, 0, len(byIdentity))
	for _, fact := range byIdentity {
		out = append(out, fact)
	}
	sortFinancialFacts(out)
	return out
}

func latestDurationFact(facts []FinancialFact, metric string, minDays, maxDays int) (FinancialFact, bool) {
	var selected FinancialFact
	ok := false
	for _, fact := range facts {
		if fact.Metric != metric || fact.PeriodStart.IsZero() {
			continue
		}
		days := int(fact.PeriodEnd.Sub(fact.PeriodStart).Hours()/24) + 1
		if days < minDays || days > maxDays {
			continue
		}
		if !ok || fact.PeriodEnd.After(selected.PeriodEnd) || (fact.PeriodEnd.Equal(selected.PeriodEnd) && factFiledAfter(fact, selected)) {
			selected, ok = fact, true
		}
	}
	return selected, ok
}

func matchingYearAgoFact(facts []FinancialFact, target FinancialFact, metric string, minDays, maxDays int) (FinancialFact, bool) {
	wantEnd := target.PeriodEnd.AddDate(-1, 0, 0)
	var selected FinancialFact
	ok := false
	for _, fact := range facts {
		if fact.Metric != metric || fact.PeriodStart.IsZero() {
			continue
		}
		days := int(fact.PeriodEnd.Sub(fact.PeriodStart).Hours()/24) + 1
		if days < minDays || days > maxDays || absDurationDays(fact.PeriodEnd.Sub(wantEnd)) > 7 {
			continue
		}
		if !ok || factFiledAfter(fact, selected) {
			selected, ok = fact, true
		}
	}
	return selected, ok
}

func matchingPriorAnnualFact(facts []FinancialFact, target FinancialFact, metric string) (FinancialFact, bool) {
	wantEnd := target.PeriodEnd.AddDate(-1, 0, 0)
	var selected FinancialFact
	ok := false
	for _, fact := range facts {
		if fact.Metric != metric || fact.PeriodStart.IsZero() {
			continue
		}
		days := int(fact.PeriodEnd.Sub(fact.PeriodStart).Hours()/24) + 1
		if days < 330 || days > 400 || absDurationDays(fact.PeriodEnd.Sub(wantEnd)) > 7 {
			continue
		}
		if !ok || factFiledAfter(fact, selected) {
			selected, ok = fact, true
		}
	}
	return selected, ok
}

func latestInstantAmount(facts []FinancialFact, metric string) (int64, bool) {
	var selected FinancialFact
	ok := false
	for _, fact := range facts {
		if fact.Metric != metric || !fact.PeriodStart.IsZero() {
			continue
		}
		if !ok || fact.PeriodEnd.After(selected.PeriodEnd) || (fact.PeriodEnd.Equal(selected.PeriodEnd) && factFiledAfter(fact, selected)) {
			selected, ok = fact, true
		}
	}
	return selected.AmountMicros, ok
}

func latestTTMAmount(facts []FinancialFact, metric string) (int64, bool) {
	quarters := make([]FinancialFact, 0)
	for _, fact := range facts {
		if fact.Metric != metric || fact.PeriodStart.IsZero() {
			continue
		}
		days := int(fact.PeriodEnd.Sub(fact.PeriodStart).Hours()/24) + 1
		if days >= 75 && days <= 105 {
			quarters = append(quarters, fact)
		}
	}
	sort.Slice(quarters, func(i, j int) bool { return quarters[i].PeriodEnd.After(quarters[j].PeriodEnd) })
	if len(quarters) < 4 {
		return 0, false
	}
	var total int64
	for _, fact := range quarters[:4] {
		total += fact.AmountMicros
	}
	return total, true
}

func parseDecimalMicros(n json.Number) (int64, error) {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return 0, fmt.Errorf("invalid decimal value")
	}
	r.Mul(r, big.NewRat(1_000_000, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, fmt.Errorf("value must fit integral micros")
	}
	return r.Num().Int64(), nil
}

func sortFinancialFacts(facts []FinancialFact) {
	sort.Slice(facts, func(i, j int) bool {
		a, b := facts[i], facts[j]
		if a.CIK != b.CIK {
			return a.CIK < b.CIK
		}
		if a.Metric != b.Metric {
			return a.Metric < b.Metric
		}
		if a.Concept != b.Concept {
			return a.Concept < b.Concept
		}
		if !a.PeriodEnd.Equal(b.PeriodEnd) {
			return a.PeriodEnd.Before(b.PeriodEnd)
		}
		return a.Accession < b.Accession
	})
}

func factFiledAfter(a, b FinancialFact) bool {
	if !a.AcceptedAt.Equal(b.AcceptedAt) {
		return a.AcceptedAt.After(b.AcceptedAt)
	}
	if !a.FiledAt.Equal(b.FiledAt) {
		return a.FiledAt.After(b.FiledAt)
	}
	return a.Accession > b.Accession
}

func absDurationDays(d time.Duration) int {
	if d < 0 {
		d = -d
	}
	return int(d.Hours() / 24)
}
