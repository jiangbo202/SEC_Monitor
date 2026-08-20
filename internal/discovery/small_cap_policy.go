package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	SmallCapPolicyStatusDraft     = "draft"
	SmallCapPolicyStatusFinalized = "finalized"

	SmallCapPolicyActivationActivate = "activate"
	SmallCapPolicyActivationRollback = "rollback"
)

var supportedSmallCapExchanges = map[string]string{
	"nasdaq":        "Nasdaq",
	"nyse":          "NYSE",
	"nyse american": "NYSE American",
}

// SmallCapPolicy is the complete value object used by one discovery run. It
// deliberately contains values only: lifecycle metadata lives in
// SmallCapPolicyVersion and is never read by the scoring engine.
type SmallCapPolicy struct {
	MarketCapMinUSD           int64    `json:"market_cap_min_usd"`
	MarketCapMaxUSD           int64    `json:"market_cap_max_usd"`
	AMarketCapMaxExclusiveUSD int64    `json:"a_market_cap_max_exclusive_usd"`
	AllowedExchanges          []string `json:"allowed_exchanges"`
	MaxPriceAgeTradingDays    int      `json:"max_price_age_trading_days"`
	MinimumPriceUSD           float64  `json:"minimum_price_usd"`
	BlockedADVUSD             float64  `json:"blocked_adv_usd"`
	TradableADVUSD            float64  `json:"tradable_adv_usd"`
	MinimumHistoryDays        int      `json:"minimum_history_days"`
	ARevenueGrowthMinPct      float64  `json:"a_revenue_growth_min_pct"`
	BRevenueGrowthMinPct      float64  `json:"b_revenue_growth_min_pct"`
	ARunwayMinMonths          float64  `json:"a_runway_min_months"`
	InsiderLookbackDays       int      `json:"insider_lookback_days"`
	BMinSectorScore           int      `json:"b_min_sector_score"`
}

// SmallCapPolicyVersion is mutable only while Status is draft. Activation
// finalizes it; subsequent changes require a new version.
type SmallCapPolicyVersion struct {
	ID            uint           `json:"id" gorm:"primaryKey"`
	Version       int            `json:"version" gorm:"uniqueIndex"`
	Name          string         `json:"name" gorm:"size:128"`
	Description   string         `json:"description" gorm:"type:text"`
	Status        string         `json:"status" gorm:"size:16;index"`
	PolicyJSON    string         `json:"-" gorm:"type:text"`
	ContentSHA256 string         `json:"content_sha256" gorm:"size:64;index"`
	CreatedBy     string         `json:"created_by" gorm:"size:128"`
	FinalizedAt   *time.Time     `json:"finalized_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	Policy        SmallCapPolicy `json:"policy" gorm:"-"`
}

// SmallCapPolicyActivation is append-only. The most recent row identifies the
// active policy, while PreviousPolicyVersionID preserves the transition.
type SmallCapPolicyActivation struct {
	ID                      uint      `json:"id" gorm:"primaryKey"`
	PolicyVersionID         uint      `json:"policy_version_id" gorm:"index"`
	PreviousPolicyVersionID uint      `json:"previous_policy_version_id" gorm:"index"`
	Action                  string    `json:"action" gorm:"size:16;index"`
	ActivatedBy             string    `json:"activated_by" gorm:"size:128"`
	ActivatedAt             time.Time `json:"activated_at" gorm:"index"`
	CreatedAt               time.Time `json:"created_at"`
}

type SmallCapPolicyDraftInput struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	CreatedBy   string         `json:"created_by"`
	Policy      SmallCapPolicy `json:"policy"`
}

type SmallCapPolicyBinding struct {
	PolicyVersionID     uint           `json:"policy_version_id"`
	PolicyVersion       int            `json:"policy_version"`
	PolicyContentSHA256 string         `json:"policy_content_sha256"`
	PolicySnapshotJSON  string         `json:"policy_snapshot_json"`
	Policy              SmallCapPolicy `json:"policy"`
}

func DefaultSmallCapPolicy() SmallCapPolicy {
	return SmallCapPolicy{
		MarketCapMinUSD:           MinimumSmallCapUSD,
		MarketCapMaxUSD:           MaximumSmallCapUSD,
		AMarketCapMaxExclusiveUSD: CandidateAMarketCapMaxExclusiveUSD,
		AllowedExchanges:          []string{"Nasdaq", "NYSE", "NYSE American"},
		MaxPriceAgeTradingDays:    MaximumPriceAgeDays,
		MinimumPriceUSD:           minimumTradablePriceUSD,
		BlockedADVUSD:             minimumConstrainedADVUSD,
		TradableADVUSD:            minimumTradableADVUSD,
		MinimumHistoryDays:        minimumInvestabilitySampleDays,
		ARevenueGrowthMinPct:      CandidateARevenueGrowthMinPct,
		BRevenueGrowthMinPct:      CandidateBRevenueGrowthMinPct,
		ARunwayMinMonths:          CandidateARunwayMinMonths,
		InsiderLookbackDays:       CandidateInsiderLookbackDays,
		BMinSectorScore:           CandidateBMinSectorScore,
	}
}

// ValidateSmallCapPolicy performs normalization-independent, cross-field
// validation. Callers can safely display the returned error as a draft issue.
func ValidateSmallCapPolicy(policy SmallCapPolicy) error {
	var issues []string
	if policy.MarketCapMinUSD <= 0 {
		issues = append(issues, "market_cap_min_usd must be positive")
	}
	if policy.MarketCapMaxUSD <= policy.MarketCapMinUSD {
		issues = append(issues, "market_cap_max_usd must be greater than market_cap_min_usd")
	}
	if policy.AMarketCapMaxExclusiveUSD <= policy.MarketCapMinUSD || policy.AMarketCapMaxExclusiveUSD >= policy.MarketCapMaxUSD {
		issues = append(issues, "a_market_cap_max_exclusive_usd must be strictly between the minimum and maximum")
	}
	if len(policy.AllowedExchanges) == 0 {
		issues = append(issues, "allowed_exchanges must not be empty")
	}
	seen := map[string]struct{}{}
	for _, exchange := range policy.AllowedExchanges {
		key := strings.ToLower(strings.TrimSpace(exchange))
		if _, ok := supportedSmallCapExchanges[key]; !ok {
			issues = append(issues, fmt.Sprintf("allowed_exchanges contains unsupported exchange %q", exchange))
			continue
		}
		if _, ok := seen[key]; ok {
			issues = append(issues, fmt.Sprintf("allowed_exchanges contains duplicate exchange %q", exchange))
		}
		seen[key] = struct{}{}
	}
	if policy.MaxPriceAgeTradingDays < 0 || policy.MaxPriceAgeTradingDays > 20 {
		issues = append(issues, "max_price_age_trading_days must be between 0 and 20")
	}
	if invalidFinite(policy.MinimumPriceUSD) || policy.MinimumPriceUSD < 0 {
		issues = append(issues, "minimum_price_usd must be finite and non-negative")
	}
	if invalidFinite(policy.BlockedADVUSD) || policy.BlockedADVUSD < 0 {
		issues = append(issues, "blocked_adv_usd must be finite and non-negative")
	}
	if invalidFinite(policy.TradableADVUSD) || policy.TradableADVUSD < policy.BlockedADVUSD {
		issues = append(issues, "tradable_adv_usd must be finite and no less than blocked_adv_usd")
	}
	if policy.MinimumHistoryDays < 1 || policy.MinimumHistoryDays > 252 {
		issues = append(issues, "minimum_history_days must be between 1 and 252")
	}
	if invalidFinite(policy.ARevenueGrowthMinPct) || invalidFinite(policy.BRevenueGrowthMinPct) || policy.ARevenueGrowthMinPct < policy.BRevenueGrowthMinPct {
		issues = append(issues, "a_revenue_growth_min_pct must be finite and no less than b_revenue_growth_min_pct")
	}
	if invalidFinite(policy.ARunwayMinMonths) || policy.ARunwayMinMonths < 0 || policy.ARunwayMinMonths > 240 {
		issues = append(issues, "a_runway_min_months must be finite and between 0 and 240")
	}
	if policy.InsiderLookbackDays < 1 || policy.InsiderLookbackDays > 3650 {
		issues = append(issues, "insider_lookback_days must be between 1 and 3650")
	}
	if policy.BMinSectorScore < 0 || policy.BMinSectorScore > 10 {
		issues = append(issues, "b_min_sector_score must be between 0 and 10")
	}
	if len(issues) > 0 {
		return errors.New(strings.Join(issues, "; "))
	}
	return nil
}

func invalidFinite(value float64) bool { return math.IsNaN(value) || math.IsInf(value, 0) }

func NormalizeSmallCapPolicy(policy SmallCapPolicy) (SmallCapPolicy, error) {
	canonical := make([]string, 0, len(policy.AllowedExchanges))
	for _, exchange := range policy.AllowedExchanges {
		if value, ok := supportedSmallCapExchanges[strings.ToLower(strings.TrimSpace(exchange))]; ok {
			canonical = append(canonical, value)
		} else {
			canonical = append(canonical, strings.TrimSpace(exchange))
		}
	}
	sort.Strings(canonical)
	policy.AllowedExchanges = canonical
	if err := ValidateSmallCapPolicy(policy); err != nil {
		return SmallCapPolicy{}, err
	}
	return policy, nil
}

func PolicyHash(policy SmallCapPolicy) (string, error) {
	normalized, err := NormalizeSmallCapPolicy(policy)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func policyJSON(policy SmallCapPolicy) (string, string, error) {
	normalized, err := NormalizeSmallCapPolicy(policy)
	if err != nil {
		return "", "", err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(payload)
	return string(payload), hex.EncodeToString(digest[:]), nil
}

func decodeSmallCapPolicy(row *SmallCapPolicyVersion) error {
	if err := json.Unmarshal([]byte(row.PolicyJSON), &row.Policy); err != nil {
		return fmt.Errorf("decode small-cap policy version %d: %w", row.Version, err)
	}
	return ValidateSmallCapPolicy(row.Policy)
}

func ListSmallCapPolicies(ctx context.Context, db *gorm.DB) ([]SmallCapPolicyVersion, error) {
	if db == nil || ctx == nil {
		return nil, errors.New("database and context are required")
	}
	var rows []SmallCapPolicyVersion
	if err := db.WithContext(ctx).Order("version DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for index := range rows {
		if err := decodeSmallCapPolicy(&rows[index]); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func GetSmallCapPolicy(ctx context.Context, db *gorm.DB, id uint) (SmallCapPolicyVersion, error) {
	if db == nil || ctx == nil || id == 0 {
		return SmallCapPolicyVersion{}, errors.New("database, context and policy id are required")
	}
	var row SmallCapPolicyVersion
	if err := db.WithContext(ctx).First(&row, id).Error; err != nil {
		return row, err
	}
	return row, decodeSmallCapPolicy(&row)
}

func GetActiveSmallCapPolicy(ctx context.Context, db *gorm.DB) (SmallCapPolicyVersion, error) {
	if db == nil || ctx == nil {
		return SmallCapPolicyVersion{}, errors.New("database and context are required")
	}
	var activation SmallCapPolicyActivation
	if err := db.WithContext(ctx).Order("id DESC").First(&activation).Error; err != nil {
		return SmallCapPolicyVersion{}, err
	}
	return GetSmallCapPolicy(ctx, db, activation.PolicyVersionID)
}

func CreateSmallCapPolicyDraft(ctx context.Context, db *gorm.DB, input SmallCapPolicyDraftInput) (SmallCapPolicyVersion, error) {
	if db == nil || ctx == nil {
		return SmallCapPolicyVersion{}, errors.New("database and context are required")
	}
	if strings.TrimSpace(input.Name) == "" {
		return SmallCapPolicyVersion{}, errors.New("policy name is required")
	}
	payload, hash, err := policyJSON(input.Policy)
	if err != nil {
		return SmallCapPolicyVersion{}, err
	}
	var created SmallCapPolicyVersion
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var latest int
		if err := tx.Model(&SmallCapPolicyVersion{}).Select("COALESCE(MAX(version), 0)").Scan(&latest).Error; err != nil {
			return err
		}
		created = SmallCapPolicyVersion{Version: latest + 1, Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description), Status: SmallCapPolicyStatusDraft, PolicyJSON: payload, ContentSHA256: hash, CreatedBy: strings.TrimSpace(input.CreatedBy)}
		return tx.Create(&created).Error
	})
	if err != nil {
		return SmallCapPolicyVersion{}, err
	}
	created.Policy = input.Policy
	return created, decodeSmallCapPolicy(&created)
}

func UpdateSmallCapPolicyDraft(ctx context.Context, db *gorm.DB, id uint, input SmallCapPolicyDraftInput) (SmallCapPolicyVersion, error) {
	if db == nil || ctx == nil || id == 0 {
		return SmallCapPolicyVersion{}, errors.New("database, context and policy id are required")
	}
	if strings.TrimSpace(input.Name) == "" {
		return SmallCapPolicyVersion{}, errors.New("policy name is required")
	}
	payload, hash, err := policyJSON(input.Policy)
	if err != nil {
		return SmallCapPolicyVersion{}, err
	}
	result := db.WithContext(ctx).Model(&SmallCapPolicyVersion{}).Where("id = ? AND status = ?", id, SmallCapPolicyStatusDraft).Updates(map[string]any{
		"name": strings.TrimSpace(input.Name), "description": strings.TrimSpace(input.Description), "policy_json": payload, "content_sha256": hash,
	})
	if result.Error != nil {
		return SmallCapPolicyVersion{}, result.Error
	}
	if result.RowsAffected != 1 {
		return SmallCapPolicyVersion{}, errors.New("policy is not an editable draft")
	}
	return GetSmallCapPolicy(ctx, db, id)
}

func ActivateSmallCapPolicy(ctx context.Context, db *gorm.DB, id uint, actor string) (SmallCapPolicyVersion, error) {
	return activateSmallCapPolicy(ctx, db, id, actor, SmallCapPolicyActivationActivate, true)
}

func RollbackSmallCapPolicy(ctx context.Context, db *gorm.DB, id uint, actor string) (SmallCapPolicyVersion, error) {
	if db == nil || ctx == nil || id == 0 {
		return SmallCapPolicyVersion{}, errors.New("database, context and policy id are required")
	}
	var rollback SmallCapPolicyVersion
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var target SmallCapPolicyVersion
		if err := tx.First(&target, id).Error; err != nil {
			return err
		}
		if target.Status != SmallCapPolicyStatusFinalized {
			return errors.New("rollback target must be a finalized policy")
		}
		if err := decodeSmallCapPolicy(&target); err != nil {
			return err
		}
		var previous SmallCapPolicyActivation
		if err := tx.Order("id DESC").First(&previous).Error; err != nil {
			return err
		}
		var latest int
		if err := tx.Model(&SmallCapPolicyVersion{}).Select("COALESCE(MAX(version), 0)").Scan(&latest).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		rollback = SmallCapPolicyVersion{
			Version: latest + 1, Name: fmt.Sprintf("回滚至 v%d · %s", target.Version, target.Name),
			Description: fmt.Sprintf("从策略 v%d 创建的不可变回滚版本", target.Version),
			Status:      SmallCapPolicyStatusFinalized, PolicyJSON: target.PolicyJSON,
			ContentSHA256: target.ContentSHA256, CreatedBy: strings.TrimSpace(actor), FinalizedAt: &now,
		}
		if err := tx.Create(&rollback).Error; err != nil {
			return err
		}
		activation := SmallCapPolicyActivation{PolicyVersionID: rollback.ID, PreviousPolicyVersionID: previous.PolicyVersionID, Action: SmallCapPolicyActivationRollback, ActivatedBy: strings.TrimSpace(actor), ActivatedAt: now}
		return tx.Create(&activation).Error
	})
	if err == nil {
		err = decodeSmallCapPolicy(&rollback)
	}
	return rollback, err
}

func activateSmallCapPolicy(ctx context.Context, db *gorm.DB, id uint, actor, action string, allowDraft bool) (SmallCapPolicyVersion, error) {
	if db == nil || ctx == nil || id == 0 {
		return SmallCapPolicyVersion{}, errors.New("database, context and policy id are required")
	}
	var activated SmallCapPolicyVersion
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&activated, id).Error; err != nil {
			return err
		}
		if err := decodeSmallCapPolicy(&activated); err != nil {
			return err
		}
		if activated.Status == SmallCapPolicyStatusDraft {
			if !allowDraft {
				return errors.New("rollback target must be a finalized policy")
			}
			now := time.Now().UTC()
			if err := tx.Model(&SmallCapPolicyVersion{}).Where("id = ? AND status = ?", id, SmallCapPolicyStatusDraft).Updates(map[string]any{"status": SmallCapPolicyStatusFinalized, "finalized_at": now}).Error; err != nil {
				return err
			}
			activated.Status, activated.FinalizedAt = SmallCapPolicyStatusFinalized, &now
		} else if activated.Status != SmallCapPolicyStatusFinalized {
			return fmt.Errorf("policy has unsupported status %q", activated.Status)
		}
		var previous SmallCapPolicyActivation
		if err := tx.Order("id DESC").First(&previous).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if previous.PolicyVersionID == id {
			return errors.New("policy is already active")
		}
		activation := SmallCapPolicyActivation{PolicyVersionID: id, PreviousPolicyVersionID: previous.PolicyVersionID, Action: action, ActivatedBy: strings.TrimSpace(actor), ActivatedAt: time.Now().UTC()}
		return tx.Create(&activation).Error
	})
	return activated, err
}

func SmallCapPolicyBindingForVersion(row SmallCapPolicyVersion) (SmallCapPolicyBinding, error) {
	if row.PolicyJSON == "" {
		if err := decodeSmallCapPolicy(&row); err != nil {
			return SmallCapPolicyBinding{}, err
		}
	}
	if len(row.Policy.AllowedExchanges) == 0 {
		if err := decodeSmallCapPolicy(&row); err != nil {
			return SmallCapPolicyBinding{}, err
		}
	}
	return SmallCapPolicyBinding{PolicyVersionID: row.ID, PolicyVersion: row.Version, PolicyContentSHA256: row.ContentSHA256, PolicySnapshotJSON: row.PolicyJSON, Policy: row.Policy}, nil
}

func SmallCapPolicyForBatch(batch UniverseBatch) (SmallCapPolicy, error) {
	if strings.TrimSpace(batch.PolicySnapshotJSON) == "" {
		return DefaultSmallCapPolicy(), nil
	}
	var policy SmallCapPolicy
	if err := json.Unmarshal([]byte(batch.PolicySnapshotJSON), &policy); err != nil {
		return SmallCapPolicy{}, fmt.Errorf("decode policy for batch %s: %w", batch.BatchID, err)
	}
	policy, err := NormalizeSmallCapPolicy(policy)
	if err != nil {
		return SmallCapPolicy{}, fmt.Errorf("validate policy for batch %s: %w", batch.BatchID, err)
	}
	if batch.PolicyContentSHA256 != "" {
		hash, err := PolicyHash(policy)
		if err != nil {
			return SmallCapPolicy{}, err
		}
		if hash != batch.PolicyContentSHA256 {
			return SmallCapPolicy{}, fmt.Errorf("policy hash mismatch for batch %s", batch.BatchID)
		}
	}
	return policy, nil
}

func SmallCapPolicyBindingForBatch(batch UniverseBatch) (SmallCapPolicyBinding, error) {
	policy, err := SmallCapPolicyForBatch(batch)
	if err != nil {
		return SmallCapPolicyBinding{}, err
	}
	binding := SmallCapPolicyBinding{PolicyVersionID: batch.PolicyVersionID, PolicyVersion: batch.PolicyVersion, PolicyContentSHA256: batch.PolicyContentSHA256, PolicySnapshotJSON: batch.PolicySnapshotJSON, Policy: policy}
	if binding.PolicyContentSHA256 == "" || binding.PolicySnapshotJSON == "" {
		fallback := defaultSmallCapPolicyBinding()
		binding.PolicyContentSHA256, binding.PolicySnapshotJSON = fallback.PolicyContentSHA256, fallback.PolicySnapshotJSON
	}
	return binding, nil
}

func SmallCapPolicyBindingForRun(run DiscoverySyncRun) (SmallCapPolicyBinding, error) {
	batchShape := UniverseBatch{PolicyVersionID: run.PolicyVersionID, PolicyVersion: run.PolicyVersion, PolicyContentSHA256: run.PolicyContentSHA256, PolicySnapshotJSON: run.PolicySnapshotJSON}
	return SmallCapPolicyBindingForBatch(batchShape)
}

func ActiveSmallCapPolicyBinding(ctx context.Context, db *gorm.DB) (SmallCapPolicyBinding, error) {
	row, err := GetActiveSmallCapPolicy(ctx, db)
	if err != nil {
		return SmallCapPolicyBinding{}, err
	}
	return SmallCapPolicyBindingForVersion(row)
}

func defaultSmallCapPolicyBinding() SmallCapPolicyBinding {
	policy := DefaultSmallCapPolicy()
	payload, hash, _ := policyJSON(policy)
	return SmallCapPolicyBinding{PolicyContentSHA256: hash, PolicySnapshotJSON: payload, Policy: policy}
}

func (c *Coordinator) effectivePolicyBinding(ctx context.Context) (SmallCapPolicyBinding, error) {
	if len(c.PolicyBinding.Policy.AllowedExchanges) > 0 {
		if err := ValidateSmallCapPolicy(c.PolicyBinding.Policy); err != nil {
			return SmallCapPolicyBinding{}, err
		}
		return c.PolicyBinding, nil
	}
	if c.DB != nil && c.DB.Migrator().HasTable(&SmallCapPolicyActivation{}) {
		binding, err := ActiveSmallCapPolicyBinding(ctx, c.DB)
		if err == nil {
			c.PolicyBinding = binding
			return binding, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return SmallCapPolicyBinding{}, err
		}
	}
	binding := defaultSmallCapPolicyBinding()
	c.PolicyBinding = binding
	return binding, nil
}

func SeedDefaultSmallCapPolicy(ctx context.Context, db *gorm.DB) error {
	if db == nil || ctx == nil {
		return errors.New("database and context are required")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&SmallCapPolicyVersion{}).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			payload, hash, err := policyJSON(DefaultSmallCapPolicy())
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			row := SmallCapPolicyVersion{Version: 1, Name: "默认小盘股策略 v1", Description: "迁移前代码规则的版本化快照", Status: SmallCapPolicyStatusFinalized, PolicyJSON: payload, ContentSHA256: hash, CreatedBy: "system", FinalizedAt: &now}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		var activationCount int64
		if err := tx.Model(&SmallCapPolicyActivation{}).Count(&activationCount).Error; err != nil {
			return err
		}
		if activationCount > 0 {
			return nil
		}
		var first SmallCapPolicyVersion
		if err := tx.Order("version ASC").First(&first).Error; err != nil {
			return err
		}
		return tx.Create(&SmallCapPolicyActivation{PolicyVersionID: first.ID, Action: SmallCapPolicyActivationActivate, ActivatedBy: "system", ActivatedAt: time.Now().UTC()}).Error
	})
}

func policyAllowsExchange(policy SmallCapPolicy, exchange string) bool {
	for _, allowed := range policy.AllowedExchanges {
		if strings.EqualFold(strings.TrimSpace(allowed), strings.TrimSpace(exchange)) {
			return true
		}
	}
	return false
}
