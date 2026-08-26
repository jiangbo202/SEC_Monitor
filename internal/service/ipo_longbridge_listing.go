package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"

	lbconfig "github.com/longbridge/openapi-go/config"
	lbfundamental "github.com/longbridge/openapi-go/fundamental"
)

const (
	longbridgeIPOListingConcurrency     = 4
	longbridgeIPOListingEscalationCount = 6
	longbridgeIPOListingEscalationDelay = 7 * 24 * time.Hour
)

// longbridgeIPOListingOverview contains the two fields needed to turn a SEC
// ticker-only mapping into a conservative listing confirmation. The ticker
// itself always remains SEC-sourced; Longbridge only verifies market presence.
type longbridgeIPOListingOverview struct {
	Market      string
	ListingDate string
}

type longbridgeIPOListingClient interface {
	Company(context.Context, string) (longbridgeIPOListingOverview, error)
}

type longbridgeIPOListingSDKClient struct {
	fundamental *lbfundamental.FundamentalContext
}

func newLongbridgeIPOListingClient(appKey, appSecret, accessToken string) (longbridgeIPOListingClient, error) {
	cfg, err := lbconfig.New(lbconfig.WithConfigKey(appKey, appSecret, accessToken))
	if err != nil {
		return nil, err
	}
	client, err := lbfundamental.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	return &longbridgeIPOListingSDKClient{fundamental: client}, nil
}

func (c *longbridgeIPOListingSDKClient) Company(ctx context.Context, symbol string) (longbridgeIPOListingOverview, error) {
	overview, err := c.fundamental.Company(ctx, symbol)
	if err != nil {
		return longbridgeIPOListingOverview{}, err
	}
	if overview == nil {
		return longbridgeIPOListingOverview{}, errors.New("Longbridge returned an empty company overview")
	}
	return longbridgeIPOListingOverview{Market: overview.Market, ListingDate: overview.ListingDate}, nil
}

func (s *IPORadarService) longbridgeListingConfig(ctx context.Context) (config.DiscoveryConfig, error) {
	cfg := s.longbridgeRuntime
	if s.configs == nil {
		return cfg, nil
	}
	return s.configs.ApplyDiscoveryConfig(ctx, cfg)
}

// confirmListedCompaniesWithLongbridge automates the last leg of an IPO
// listing decision when SEC has already supplied a CIK-bound ticker but not a
// usable exchange. It never treats a Longbridge miss as an unlisted company.
func (s *IPORadarService) confirmListedCompaniesWithLongbridge(ctx context.Context, settings IPORadarSettings) (map[string]bool, string) {
	confirmed := map[string]bool{}
	if !settings.LongbridgeListingVerificationEnabled || settings.LongbridgeListingRequestBudget == 0 {
		return confirmed, ""
	}
	cfg, err := s.longbridgeListingConfig(ctx)
	if err != nil {
		return confirmed, "Longbridge listing configuration: " + err.Error()
	}
	if strings.TrimSpace(cfg.LongbridgeAppKey) == "" || strings.TrimSpace(cfg.LongbridgeAppSecret) == "" || strings.TrimSpace(cfg.LongbridgeAccessToken) == "" {
		return confirmed, ""
	}

	now := time.Now().UTC()
	recheckBefore := now.Add(-time.Duration(settings.LongbridgeListingRecheckHours) * time.Hour)
	activeFilingCutoff := now.Add(-ipoListingAutomaticPauseAfter)
	activeCIKs := s.db.WithContext(ctx).Model(&model.IPOFiling{}).
		Select("cik").
		Where("filing_date >= ?", activeFilingCutoff).
		Group("cik")
	var candidates []model.IPOCompanyMarketData
	if err := s.db.WithContext(ctx).
		Where("TRIM(ticker) <> '' AND TRIM(exchange) = '' AND (listing_checked_at IS NULL OR listing_checked_at < ?)", recheckBefore).
		Where("longbridge_listing_next_retry_at IS NULL OR longbridge_listing_next_retry_at <= ?", now).
		Where("cik IN (?)", activeCIKs).
		Order("listing_checked_at ASC, cik ASC").
		Limit(settings.LongbridgeListingRequestBudget).
		Find(&candidates).Error; err != nil {
		return confirmed, "load Longbridge listing candidates: " + err.Error()
	}
	if len(candidates) == 0 {
		return confirmed, ""
	}
	client, err := s.newLongbridgeListingClient(cfg.LongbridgeAppKey, cfg.LongbridgeAppSecret, cfg.LongbridgeAccessToken)
	if err != nil {
		return confirmed, "create Longbridge listing client: " + err.Error()
	}

	type outcome struct {
		candidate model.IPOCompanyMarketData
		overview  longbridgeIPOListingOverview
		err       error
	}
	jobs := make(chan model.IPOCompanyMarketData)
	results := make(chan outcome, len(candidates))
	workers := longbridgeIPOListingConcurrency
	if workers > len(candidates) {
		workers = len(candidates)
	}
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for candidate := range jobs {
				symbol := strings.ToUpper(strings.TrimSpace(candidate.Ticker)) + ".US"
				overview, fetchErr := client.Company(ctx, symbol)
				results <- outcome{candidate: candidate, overview: overview, err: fetchErr}
			}
		}()
	}
	go func() {
		for _, candidate := range candidates {
			jobs <- candidate
		}
		close(jobs)
		group.Wait()
		close(results)
	}()

	failures := 0
	for result := range results {
		candidate, overview, fetchErr := result.candidate, result.overview, result.err
		candidate.ListingCheckedAt = &now
		candidate.LongbridgeListingCheckCount++
		market := strings.TrimSpace(overview.Market)
		if fetchErr != nil || market == "" {
			result := "no_data"
			if fetchErr != nil {
				result = "unavailable"
			}
			// A missing Longbridge profile is not evidence that an SEC filing is
			// unlisted. After several attempts, retain the audit trail but move it
			// to a weekly observation window and stop marking every hourly run as
			// partially failed.
			activeRetry := candidate.LongbridgeListingCheckCount < longbridgeIPOListingEscalationCount
			if !activeRetry {
				result += "_review"
			} else {
				failures++
			}
			nextRetryAt := any(nil)
			if !activeRetry {
				next := now.Add(longbridgeIPOListingEscalationDelay)
				nextRetryAt = &next
			} else if result == "unavailable" {
				next := nextLongbridgeIPOListingRetryAt(candidate.LongbridgeListingCheckCount, now)
				nextRetryAt = &next
			}
			if saveErr := s.db.WithContext(ctx).Model(&model.IPOCompanyMarketData{}).Where("id = ?", candidate.ID).Updates(map[string]any{
				"listing_checked_at":               &now,
				"longbridge_listing_check_count":   candidate.LongbridgeListingCheckCount,
				"longbridge_listing_last_result":   result,
				"longbridge_listing_next_retry_at": nextRetryAt,
				"updated_at":                       now,
			}).Error; saveErr != nil {
				return confirmed, "record Longbridge listing check: " + saveErr.Error()
			}
			continue
		}

		candidate.Exchange = market
		candidate.ListedVerifiedAt = &now
		candidate.ListingSource = "longbridge"
		candidate.ListingConfidence = "medium"
		candidate.LongbridgeListingLastResult = "confirmed"
		candidate.LongbridgeListingNextRetryAt = nil
		if listingDate, ok := parseLongbridgeIPOListingDate(overview.ListingDate); ok {
			candidate.ListingDate = &listingDate
		}
		if err := s.db.WithContext(ctx).Save(&candidate).Error; err != nil {
			return confirmed, "save Longbridge listing confirmation: " + err.Error()
		}
		confirmed[normalizedIPOCompanyCIK(candidate.CIK)] = true
	}
	if failures > 0 {
		return confirmed, fmt.Sprintf("Longbridge listing verification deferred for %d company(s)", failures)
	}
	return confirmed, ""
}

func nextLongbridgeIPOListingRetryAt(checkCount int, now time.Time) time.Time {
	if checkCount >= longbridgeIPOListingEscalationCount {
		return now.Add(longbridgeIPOListingEscalationDelay)
	}
	delays := []time.Duration{30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 24 * time.Hour}
	index := checkCount - 1
	if index < 0 {
		index = 0
	}
	if index >= len(delays) {
		index = len(delays) - 1
	}
	return now.Add(delays[index])
}

func parseLongbridgeIPOListingDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.DateOnly, "2006/01/02", "2006.01.02", "2006-1-2"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}
