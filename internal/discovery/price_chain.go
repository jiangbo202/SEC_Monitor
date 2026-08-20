package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type PriceProviderChainOptions struct {
	Provider    string
	Providers   []PriceProvider
	Calendar    MarketCalendar
	Now         func() time.Time
	Diagnostics func(PriceProviderChainDiagnostic)
}

type PriceProviderChainDiagnostic struct {
	Event       string
	Provider    string
	Expected    int
	Records     int
	Remaining   int
	CoveragePct float64
	Elapsed     time.Duration
	Error       string
}

type PriceProviderChain struct {
	provider    string
	providers   []PriceProvider
	names       []string
	calendar    MarketCalendar
	now         func() time.Time
	diagnostics func(PriceProviderChainDiagnostic)
}

func NewPriceProviderChain(options PriceProviderChainOptions) (*PriceProviderChain, error) {
	provider := strings.ToLower(strings.TrimSpace(options.Provider))
	if provider == "" {
		provider = "chain"
	}
	if len(options.Providers) == 0 {
		return nil, errors.New("price provider chain requires at least one provider")
	}
	if options.Calendar == nil {
		return nil, errors.New("price provider chain requires market calendar")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	names := make([]string, 0, len(options.Providers))
	for _, child := range options.Providers {
		if child == nil {
			return nil, errors.New("price provider chain contains nil provider")
		}
		name := providerName(child)
		if name == "" {
			return nil, errors.New("price provider chain child provider name is required")
		}
		names = append(names, name)
	}
	return &PriceProviderChain{provider: provider, providers: append([]PriceProvider(nil), options.Providers...), names: names, calendar: options.Calendar, now: now, diagnostics: options.Diagnostics}, nil
}

func (p *PriceProviderChain) ProviderName() string { return p.provider }

func (p *PriceProviderChain) AllowedRecordSources() []string {
	return append([]string(nil), p.names...)
}

func (p *PriceProviderChain) Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error) {
	return p.load(ctx, expected, "")
}

func (p *PriceProviderChain) LoadForDate(ctx context.Context, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error) {
	return p.load(ctx, expected, effectiveDate)
}

func (p *PriceProviderChain) load(ctx context.Context, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error) {
	remaining := append([]Listing(nil), expected...)
	covered := make(map[string]struct{})
	// When the caller supplies an effective date, covered means an exact quote
	// for that date. A previous-trading-day quote remains in records as a
	// fallback, but must not stop a later provider from trying to fill the
	// current trading day.
	exactDateRequired := strings.TrimSpace(effectiveDate) != ""
	recordsBySymbol := make(map[string]PriceRecord, len(expected))
	childVersions := make([]string, 0, len(p.providers))
	attempts := make([]ProviderAttempt, 0, len(p.providers))
	var lastErr error
	var effective time.Time
	for _, child := range p.providers {
		if len(remaining) == 0 {
			break
		}
		name := providerName(child)
		started := time.Now()
		p.emitDiagnostic(PriceProviderChainDiagnostic{
			Event:     "start",
			Provider:  name,
			Expected:  len(remaining),
			Remaining: len(remaining),
		})
		childRecords, childResult, err := loadPriceProviderChild(ctx, child, remaining, effectiveDate)
		if err != nil {
			elapsed := time.Since(started)
			lastErr = err
			childVersions = append(childVersions, name+":error:"+err.Error())
			attempts = append(attempts, ProviderAttempt{Provider: name, Status: "failed", Expected: len(remaining), Remaining: len(remaining), ElapsedMS: elapsed.Milliseconds(), ErrorMessage: providerAttemptError(err)})
			p.emitDiagnostic(PriceProviderChainDiagnostic{
				Event:     "error",
				Provider:  name,
				Expected:  len(remaining),
				Remaining: len(remaining),
				Elapsed:   elapsed,
				Error:     err.Error(),
			})
			continue
		}
		childVersions = append(childVersions, childResult.Provider+":"+childResult.SourceVersion)
		attemptExpected := len(remaining)
		if effective.IsZero() && !childResult.EffectiveDate.IsZero() {
			effective = childResult.EffectiveDate
		}
		for _, record := range childRecords {
			symbol := strings.ToUpper(strings.TrimSpace(record.Symbol))
			if symbol == "" {
				continue
			}
			record.Symbol = symbol
			current, exists := recordsBySymbol[symbol]
			if !exists || priceRecordIsNewer(record, current) {
				recordsBySymbol[symbol] = record
			}
			if !exactDateRequired || record.TradeDate.Format(time.DateOnly) == effectiveDate {
				covered[symbol] = struct{}{}
			}
		}
		remaining = missingListings(expected, covered)
		elapsed := time.Since(started)
		attemptStatus := "success"
		if childResult.Records == 0 {
			attemptStatus = "empty"
		} else if len(remaining) > 0 {
			attemptStatus = "partial"
		}
		attempts = append(attempts, ProviderAttempt{Provider: name, Status: attemptStatus, SourceVersion: childResult.SourceVersion, Expected: attemptExpected, Records: childResult.Records, Remaining: len(remaining), CoveragePct: childResult.CoveragePct, ElapsedMS: elapsed.Milliseconds()})
		p.emitDiagnostic(PriceProviderChainDiagnostic{
			Event:       "success",
			Provider:    name,
			Expected:    childResult.Expected,
			Records:     childResult.Records,
			Remaining:   len(remaining),
			CoveragePct: childResult.CoveragePct,
			Elapsed:     elapsed,
		})
	}
	records := make([]PriceRecord, 0, len(recordsBySymbol))
	for _, record := range recordsBySymbol {
		records = append(records, record)
	}
	if len(records) == 0 && lastErr != nil {
		return nil, ProviderResult{}, lastErr
	}
	if effective.IsZero() {
		if strings.TrimSpace(effectiveDate) != "" {
			parsed, err := parseNYCivilDate(effectiveDate)
			if err != nil {
				return nil, ProviderResult{}, err
			}
			effective = parsed
		} else {
			effective = latestPriceRecordDate(records)
		}
	}
	sortPriceRecordsByExpected(records, expected)
	sourceVersion, sha := chainSourceVersion(p.provider, effective, childVersions, records)
	result, err := validatePriceBatch(ctx, records, PriceValidationOptions{
		Provider:                      p.provider,
		SourceVersion:                 sourceVersion,
		EffectiveDate:                 effective,
		Now:                           p.now(),
		Calendar:                      p.calendar,
		Expected:                      expected,
		AllowPreviousTradingDatePrice: strings.TrimSpace(effectiveDate) != "",
	})
	if err != nil {
		return nil, ProviderResult{}, err
	}
	result.SHA256 = sha
	result.Attempts = attempts
	result.FallbackUsed = len(attempts) > 1
	return records, result, nil
}

func providerAttemptError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	if len(value) > 2_000 {
		value = value[:2_000]
	}
	return value
}

func priceRecordIsNewer(candidate, current PriceRecord) bool {
	if candidate.TradeDate.After(current.TradeDate) {
		return true
	}
	if candidate.TradeDate.Before(current.TradeDate) {
		return false
	}
	// Keep the first provider's quote on an equal trade date. Provider order is
	// the configured reliability preference and preserves deterministic output.
	return false
}

func (p *PriceProviderChain) emitDiagnostic(event PriceProviderChainDiagnostic) {
	if p != nil && p.diagnostics != nil {
		p.diagnostics(event)
	}
}

func loadPriceProviderChild(ctx context.Context, provider PriceProvider, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error) {
	if dated, ok := provider.(DatedPriceProvider); ok && strings.TrimSpace(effectiveDate) != "" {
		return dated.LoadForDate(ctx, expected, effectiveDate)
	}
	return provider.Load(ctx, expected)
}

func providerName(provider PriceProvider) string {
	if named, ok := provider.(NamedPriceProvider); ok {
		return strings.ToLower(strings.TrimSpace(named.ProviderName()))
	}
	return ""
}

func missingListings(expected []Listing, covered map[string]struct{}) []Listing {
	missing := make([]Listing, 0, len(expected))
	for _, listing := range expected {
		ticker := strings.ToUpper(strings.TrimSpace(listing.Ticker))
		if ticker == "" {
			continue
		}
		if _, exists := covered[ticker]; !exists {
			missing = append(missing, listing)
		}
	}
	return missing
}

func sortPriceRecordsByExpected(records []PriceRecord, expected []Listing) {
	order := make(map[string]int, len(expected))
	for index, listing := range expected {
		ticker := strings.ToUpper(strings.TrimSpace(listing.Ticker))
		if ticker != "" {
			order[ticker] = index
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		left, leftOK := order[strings.ToUpper(records[i].Symbol)]
		right, rightOK := order[strings.ToUpper(records[j].Symbol)]
		if leftOK && rightOK {
			return left < right
		}
		if leftOK != rightOK {
			return leftOK
		}
		return records[i].Symbol < records[j].Symbol
	})
}

func chainSourceVersion(provider string, effective time.Time, childVersions []string, records []PriceRecord) (string, string) {
	h := sha256.New()
	_, _ = h.Write([]byte(provider))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(effective.Format(time.DateOnly)))
	for _, version := range childVersions {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(version))
	}
	for _, record := range records {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(fmt.Sprintf("%s|%s|%s|%d|%d", record.Source, record.Symbol, record.TradeDate.Format(time.DateOnly), record.CloseMicros, record.Volume)))
	}
	sha := hex.EncodeToString(h.Sum(nil))
	return provider + ":" + effective.Format(time.DateOnly) + ":" + sha[:12], sha
}
