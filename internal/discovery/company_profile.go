package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// CompanyProfile is a compact issuer overview assembled only from the local
// SEC security-universe metadata. It deliberately keeps the source explicit:
// SEC's SIC description is an industry classification, not a generated or
// inferred description of every product the company sells.
type CompanyProfile struct {
	Ticker               string     `json:"ticker"`
	CompanyName          string     `json:"company_name"`
	CIK                  string     `json:"cik"`
	Exchange             string     `json:"exchange"`
	SIC                  int        `json:"sic"`
	SICDescription       string     `json:"sic_description"`
	SectorCategory       string     `json:"sector_category"`
	StateOfIncorporation string     `json:"state_of_incorporation"`
	LatestAnnualForm     string     `json:"latest_annual_form"`
	BusinessSummary      string     `json:"business_summary"`
	SummarySource        string     `json:"summary_source"`
	ProfileProvider      string     `json:"profile_provider,omitempty"`
	ProfileFetchedAt     *time.Time `json:"profile_fetched_at,omitempty"`
	ProfileFreshness     string     `json:"profile_freshness,omitempty"`
	Website              string     `json:"website,omitempty"`
	Founded              string     `json:"founded,omitempty"`
	ListingDate          string     `json:"listing_date,omitempty"`
	Market               string     `json:"market,omitempty"`
	Address              string     `json:"address,omitempty"`
	Employees            string     `json:"employees,omitempty"`
	Manager              string     `json:"manager,omitempty"`
	YearEnd              string     `json:"year_end,omitempty"`
	MetadataAsOf         *time.Time `json:"metadata_as_of,omitempty"`
	Status               string     `json:"status"`
}

// GetCompanyProfile resolves the latest local SEC identity snapshot. The
// function does not call SEC at request time, which keeps detail pages fast,
// reproducible, and independent from SEC rate limits.
func GetCompanyProfile(ctx context.Context, db *gorm.DB, ticker, cik string) (CompanyProfile, error) {
	result := CompanyProfile{Ticker: strings.ToUpper(strings.TrimSpace(ticker)), CIK: strings.TrimSpace(cik), Status: "partial"}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	if result.Ticker == "" && result.CIK == "" {
		return result, errors.New("ticker or cik is required")
	}

	var identity SecurityBatchIdentity
	// Only published security-universe batches are eligible. A partially
	// staged/failed run must never change the issuer details shown to users.
	query := db.WithContext(ctx).Model(&SecurityBatchIdentity{}).
		Joins("JOIN universe_batches ON universe_batches.batch_id = security_batch_identities.batch_id").
		Where("universe_batches.kind = ? AND universe_batches.status = ?", BatchKindSecurity, BatchStatusPublished)
	if result.CIK != "" {
		query = query.Where("cik = ?", result.CIK)
	}
	if result.Ticker != "" {
		query = query.Where("ticker = ?", result.Ticker)
	}
	err := query.Order("created_at DESC, id DESC").First(&identity).Error
	if err == nil {
		applyCompanyProfileIdentity(&result, identity)
		return hydrateCompanyProfileProvider(ctx, db, finalizeCompanyProfile(result), identity.SecurityID), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, fmt.Errorf("load SEC company metadata: %w", err)
	}

	// A security can exist before the next universe snapshot is published.
	// Return the durable issuer metadata when it is available rather than
	// making the detail view fail solely because an identity snapshot is absent.
	var security Security
	securityQuery := db.WithContext(ctx).Model(&Security{})
	if result.CIK != "" {
		securityQuery = securityQuery.Where("cik = ?", result.CIK)
	} else {
		securityQuery = securityQuery.Joins("JOIN listings ON listings.security_id = securities.id").Where("listings.ticker = ?", result.Ticker)
	}
	if err := securityQuery.First(&security).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return finalizeCompanyProfile(result), nil
		}
		return result, fmt.Errorf("load SEC issuer: %w", err)
	}
	result.CIK = stringOrDefault(result.CIK, security.CIK)
	result.CompanyName = security.CompanyName
	result.SIC = security.SIC
	result.SICDescription = security.SICDescription
	result.StateOfIncorporation = security.StateOfIncorporation
	result.LatestAnnualForm = security.LatestAnnualForm
	if !security.UpdatedAt.IsZero() {
		at := security.UpdatedAt
		result.MetadataAsOf = &at
	}
	return hydrateCompanyProfileProvider(ctx, db, finalizeCompanyProfile(result), security.ID), nil
}

func applyCompanyProfileIdentity(profile *CompanyProfile, identity SecurityBatchIdentity) {
	if profile == nil {
		return
	}
	profile.Ticker = stringOrDefault(profile.Ticker, strings.ToUpper(strings.TrimSpace(identity.Ticker)))
	profile.CIK = stringOrDefault(profile.CIK, strings.TrimSpace(identity.CIK))
	profile.CompanyName = strings.TrimSpace(identity.CompanyName)
	profile.Exchange = strings.TrimSpace(identity.Exchange)
	profile.SIC = identity.SIC
	profile.SICDescription = strings.TrimSpace(identity.SICDescription)
	profile.StateOfIncorporation = strings.TrimSpace(identity.StateOfIncorporation)
	profile.LatestAnnualForm = strings.TrimSpace(identity.LatestAnnualForm)
	if !identity.CreatedAt.IsZero() {
		at := identity.CreatedAt
		profile.MetadataAsOf = &at
	}
}

func finalizeCompanyProfile(profile CompanyProfile) CompanyProfile {
	rating := SectorRatingForSIC(profile.SIC)
	profile.SectorCategory = rating.Category
	if description := strings.TrimSpace(profile.SICDescription); description != "" {
		profile.BusinessSummary = fmt.Sprintf("SEC 将该发行人归入“%s”（SIC %d）。这是申报资料中的行业分类，用于概览其主要业务领域，并非完整产品介绍。", description, profile.SIC)
		profile.SummarySource = "SEC submissions / sicDescription"
		profile.Status = "available"
		return profile
	}
	if profile.SIC != 0 && rating.Category != "赛道数据缺失" {
		profile.BusinessSummary = fmt.Sprintf("SEC 行业描述暂未同步；依据 SIC %d，该发行人当前归入“%s”。下一次 SEC 安全宇宙同步后会优先展示 SEC 的原始行业描述。", profile.SIC, rating.Category)
		profile.SummarySource = "SEC submissions / SIC（赛道回退）"
		return profile
	}
	profile.BusinessSummary = "尚未获得可核验的 SEC 行业描述；请在下一次 SEC 安全宇宙同步完成后重新查看。"
	profile.SummarySource = "待 SEC metadata 同步"
	return profile
}

// hydrateCompanyProfileProvider overlays the last successful, locally cached
// company description. A provider outage or an expired row never hides the
// SEC profile; freshness stays visible so research users can judge it.
func hydrateCompanyProfileProvider(ctx context.Context, db *gorm.DB, profile CompanyProfile, securityID uint) CompanyProfile {
	if securityID == 0 {
		return profile
	}
	var snapshot CompanyProfileSnapshot
	err := db.WithContext(ctx).Where("provider = ? AND security_id = ? AND fetched_at IS NOT NULL", longbridgeCompanyProfileProvider, securityID).
		Order("fetched_at DESC, id DESC").First(&snapshot).Error
	if err != nil {
		return profile
	}
	profile.ProfileProvider = "Longbridge company overview"
	profile.ProfileFetchedAt = snapshot.FetchedAt
	profile.ProfileFreshness = "fresh"
	if snapshot.FetchedAt != nil && snapshot.FetchedAt.AddDate(0, 0, 30).Before(time.Now().UTC()) {
		profile.ProfileFreshness = "stale"
	}
	profile.Website = snapshot.Website
	profile.Founded = snapshot.Founded
	profile.ListingDate = snapshot.ListingDate
	profile.Market = snapshot.Market
	profile.Address = snapshot.Address
	profile.Employees = snapshot.Employees
	profile.Manager = snapshot.Manager
	profile.YearEnd = snapshot.YearEnd
	if strings.TrimSpace(snapshot.Profile) != "" {
		profile.BusinessSummary = snapshot.Profile
		profile.SummarySource = "Longbridge company overview（本地缓存）"
		profile.Status = "available"
	}
	return profile
}
