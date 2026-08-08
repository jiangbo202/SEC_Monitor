package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	EligibilityStatusPass        = "pass"
	EligibilityStatusFail        = "fail"
	EligibilityStatusUnavailable = "unavailable"
)

// SmallCapEligibilityCheckInput intentionally contains only a ticker. The
// check is a local explanation of the currently published selection rules,
// not an ad-hoc market-data refresh.
type SmallCapEligibilityCheckInput struct {
	Ticker string `json:"ticker"`
}

type SmallCapEligibilityCondition struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	AppliesTo   string `json:"applies_to"`
	Requirement string `json:"requirement"`
	Actual      string `json:"actual"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
}

// SmallCapEligibilityEvidenceSnapshot preserves the exact locally available
// source rows used by a manual check. It is embedded in ResultJSON instead of
// being reconstructed later from mutable current snapshots, so a future check
// can be compared with the evidence that was available on the original date.
type SmallCapEligibilityEvidenceSnapshot struct {
	Identity            *SecurityBatchIdentity          `json:"identity,omitempty"`
	Classification      *ClassificationSnapshot         `json:"classification,omitempty"`
	Market              *UniverseSnapshot               `json:"market,omitempty"`
	Price               *PriceSnapshot                  `json:"price,omitempty"`
	Shares              *ShareSnapshot                  `json:"shares,omitempty"`
	Financial           *FinancialMetricSnapshot        `json:"financial,omitempty"`
	InsiderCoverage     *InsiderCoverageSnapshot        `json:"insider_coverage,omitempty"`
	InsiderTransactions []InsiderTransactionSnapshot    `json:"insider_transactions,omitempty"`
	CapitalRisks        []CapitalRiskSnapshot           `json:"capital_risks,omitempty"`
	BusinessModel       *CandidateBusinessModelOverride `json:"business_model,omitempty"`
}

type SmallCapEligibilityConditionChange struct {
	Key            string `json:"key"`
	Label          string `json:"label"`
	PreviousActual string `json:"previous_actual"`
	CurrentActual  string `json:"current_actual"`
	PreviousStatus string `json:"previous_status"`
	CurrentStatus  string `json:"current_status"`
}

type SmallCapEligibilityComparison struct {
	PreviousCheckedAt    time.Time                            `json:"previous_checked_at"`
	PreviousGrade        string                               `json:"previous_grade"`
	PreviousMarketAsOf   string                               `json:"previous_market_as_of,omitempty"`
	PreviousSecurityAsOf string                               `json:"previous_security_as_of,omitempty"`
	Changes              []SmallCapEligibilityConditionChange `json:"changes"`
}

type SmallCapEligibilityCheckResult struct {
	Ticker          string                               `json:"ticker"`
	CompanyName     string                               `json:"company_name"`
	SecurityID      uint                                 `json:"security_id,omitempty"`
	MarketBatchID   string                               `json:"market_batch_id,omitempty"`
	SecurityBatchID string                               `json:"security_batch_id,omitempty"`
	MarketAsOf      string                               `json:"market_as_of,omitempty"`
	SecurityAsOf    string                               `json:"security_as_of,omitempty"`
	InSmallCapPool  bool                                 `json:"in_small_cap_pool"`
	EligibleA       bool                                 `json:"eligible_a"`
	EligibleB       bool                                 `json:"eligible_b"`
	Grade           string                               `json:"grade"`
	Summary         string                               `json:"summary"`
	Conditions      []SmallCapEligibilityCondition       `json:"conditions"`
	CheckedAt       time.Time                            `json:"checked_at"`
	Criteria        CandidateSelectionCriteria           `json:"criteria"`
	Evidence        *SmallCapEligibilityEvidenceSnapshot `json:"evidence,omitempty"`
	Comparison      *SmallCapEligibilityComparison       `json:"comparison,omitempty"`
}

type SmallCapEligibilityCheckHistoryItem struct {
	ID              uint                           `json:"id"`
	RequestedTicker string                         `json:"requested_ticker"`
	Ticker          string                         `json:"ticker"`
	CompanyName     string                         `json:"company_name"`
	SecurityID      uint                           `json:"security_id,omitempty"`
	MarketBatchID   string                         `json:"market_batch_id,omitempty"`
	SecurityBatchID string                         `json:"security_batch_id,omitempty"`
	InSmallCapPool  bool                           `json:"in_small_cap_pool"`
	EligibleA       bool                           `json:"eligible_a"`
	EligibleB       bool                           `json:"eligible_b"`
	Grade           string                         `json:"grade"`
	Result          SmallCapEligibilityCheckResult `json:"result"`
	CreatedAt       time.Time                      `json:"created_at"`
}

type SmallCapEligibilityCheckHistoryPage struct {
	Page     int                                   `json:"page"`
	PageSize int                                   `json:"page_size"`
	Total    int64                                 `json:"total"`
	Items    []SmallCapEligibilityCheckHistoryItem `json:"items"`
}

func CheckSmallCapEligibility(ctx context.Context, db *gorm.DB, input SmallCapEligibilityCheckInput, now time.Time) (SmallCapEligibilityCheckResult, error) {
	result := SmallCapEligibilityCheckResult{Grade: CandidateGradeExcluded, Conditions: []SmallCapEligibilityCondition{}, Criteria: CurrentCandidateSelectionCriteria()}
	if db == nil || ctx == nil {
		return result, errors.New("database and context are required")
	}
	ticker := strings.ToUpper(strings.TrimSpace(input.Ticker))
	if ticker == "" {
		return result, errors.New("ticker is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result.Ticker, result.CheckedAt = ticker, now

	marketBatch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil {
		return result, err
	}
	if !ok {
		result.Summary = "尚无已发布的小盘候选批次，请先完成一次候选工作流。"
		result.Conditions = append(result.Conditions, eligibilityCondition("published_batch", "当前已发布候选批次", "基础", "存在可用的已发布批次", "未找到", EligibilityStatusUnavailable, result.Summary))
		return persistSmallCapEligibilityCheck(ctx, db, ticker, result)
	}
	result.MarketBatchID, result.MarketAsOf = marketBatch.BatchID, marketBatch.EffectiveDate
	securityBatchID := strings.TrimSpace(marketBatch.UniverseSourceVersion)
	if securityBatchID == "" {
		securityBatchID = marketBatch.BatchID
	}
	result.SecurityBatchID = securityBatchID
	var securityBatch UniverseBatch
	if err := db.WithContext(ctx).First(&securityBatch, "batch_id = ?", securityBatchID).Error; err == nil {
		result.SecurityAsOf = securityBatch.EffectiveDate
	}

	var universe UniverseSnapshot
	err = db.WithContext(ctx).First(&universe, "batch_id = ? AND ticker = ?", marketBatch.BatchID, ticker).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return checkEligibilityWithoutUniverse(ctx, db, ticker, result)
	}
	if err != nil {
		return result, err
	}
	result.SecurityID = universe.SecurityID
	evidence := &SmallCapEligibilityEvidenceSnapshot{Market: &universe}
	result.Evidence = evidence
	var identity SecurityBatchIdentity
	identityErr := db.WithContext(ctx).First(&identity, "batch_id = ? AND security_id = ?", securityBatchID, universe.SecurityID).Error
	if identityErr != nil && !errors.Is(identityErr, gorm.ErrRecordNotFound) {
		return result, identityErr
	}
	identityFound := identityErr == nil
	if identityFound {
		evidence.Identity = &identity
	}
	result.CompanyName = identity.CompanyName
	if result.CompanyName == "" {
		var security Security
		if err := db.WithContext(ctx).First(&security, universe.SecurityID).Error; err == nil {
			result.CompanyName = security.CompanyName
		}
	}
	classification := ClassificationSnapshot{}
	classificationFound := db.WithContext(ctx).First(&classification, "batch_id = ? AND security_id = ?", securityBatchID, universe.SecurityID).Error == nil
	if classificationFound {
		evidence.Classification = &classification
	}
	classificationStatus := EligibilityStatusFail
	classificationActual := "未纳入"
	classificationDetail := "当前安全宇宙未将该证券识别为可研究的普通股。"
	if classificationFound && classification.Included && classification.Status == EffectiveStatusIncluded && identity.MappingStatus == MappingStatusCurrent {
		classificationStatus, classificationActual, classificationDetail = EligibilityStatusPass, "已纳入普通股宇宙", "SEC 身份和交易所映射均处于可用状态。"
	} else if !classificationFound || identity.MappingStatus == "" {
		classificationStatus, classificationActual, classificationDetail = EligibilityStatusUnavailable, "身份快照缺失", "无法确认当前普通股身份。"
	}
	result.Conditions = append(result.Conditions, eligibilityCondition("common_stock_identity", "普通股身份", "基础", "当前安全宇宙已纳入且映射有效", classificationActual, classificationStatus, classificationDetail))

	if universe.PriceSnapshotID != nil {
		var price PriceSnapshot
		if err := db.WithContext(ctx).First(&price, *universe.PriceSnapshotID).Error; err == nil {
			evidence.Price = &price
		}
	}
	if universe.ShareSnapshotID != nil {
		var shares ShareSnapshot
		if err := db.WithContext(ctx).First(&shares, *universe.ShareSnapshotID).Error; err == nil {
			evidence.Shares = &shares
		}
	}
	marketStatus, marketActual, marketDetail := marketCapCondition(universe, result.Criteria, result.MarketAsOf)
	result.Conditions = append(result.Conditions, eligibilityCondition("small_cap_pool", "候选池市值", "基础", fmt.Sprintf("%s ≤ 市值 < %s", formatEligibilityUSD(result.Criteria.MarketCapMinUSD), formatEligibilityUSD(result.Criteria.BMarketCapMaxExclusiveUSD)), marketActual, marketStatus, marketDetail))
	result.InSmallCapPool = marketStatus == EligibilityStatusPass

	var financial FinancialMetricSnapshot
	financialFound := db.WithContext(ctx).First(&financial, "batch_id = ? AND security_id = ?", securityBatchID, universe.SecurityID).Error == nil
	if financialFound {
		evidence.Financial = &financial
	}
	growth, growthAvailable, growthBasis := selectRevenueGrowth(financial)
	growthActual, growthDetail := eligibilityGrowthActual(financial, growth, growthAvailable, growthBasis)
	growthStatus := EligibilityStatusUnavailable
	if growthAvailable {
		growthStatus = EligibilityStatusPass
		if growth <= result.Criteria.BRevenueGrowthMinExclusivePct {
			growthStatus = EligibilityStatusFail
		}
	}
	if !financialFound {
		growthDetail = "当前批次未找到财务指标快照。"
	}
	result.Conditions = append(result.Conditions, eligibilityCondition("revenue_growth_b", "收入增长", "B级", fmt.Sprintf("%s > %.0f%%", result.Criteria.RevenueGrowthSelection, result.Criteria.BRevenueGrowthMinExclusivePct), growthActual, growthStatus, growthDetail))
	growthAStatus := EligibilityStatusUnavailable
	if growthAvailable {
		growthAStatus = EligibilityStatusPass
		if growth <= result.Criteria.ARevenueGrowthMinExclusivePct {
			growthAStatus = EligibilityStatusFail
		}
	}
	result.Conditions = append(result.Conditions, eligibilityCondition("revenue_growth_a", "收入增长", "A级", fmt.Sprintf("%s > %.0f%%", result.Criteria.RevenueGrowthSelection, result.Criteria.ARevenueGrowthMinExclusivePct), growthActual, growthAStatus, growthDetail))

	runwayStatus, runwayActual, runwayDetail := eligibilityRunway(financial, financialFound, result.Criteria)
	result.Conditions = append(result.Conditions, eligibilityCondition("cash_runway", "现金 runway", "A级", fmt.Sprintf("现金 runway ≥ %.0f 个月", result.Criteria.ARunwayMinMonths), runwayActual, runwayStatus, runwayDetail))

	var insiders []InsiderTransactionSnapshot
	if err := db.WithContext(ctx).Where("security_id = ?", universe.SecurityID).Find(&insiders).Error; err != nil {
		return result, err
	}
	evidence.InsiderTransactions = insiders
	var coverage InsiderCoverageSnapshot
	coverageFound := db.WithContext(ctx).First(&coverage, "batch_id = ? AND security_id = ?", securityBatchID, universe.SecurityID).Error == nil
	if coverageFound {
		evidence.InsiderCoverage = &coverage
	}
	insiderStatus, insiderActual, insiderDetail := eligibilityInsider(insiders, coverage, coverageFound, now, result.Criteria)
	result.Conditions = append(result.Conditions, eligibilityCondition("qualified_insider_buy", "合格内幕买入", "A级", result.Criteria.QualifiedInsiderRequirement, insiderActual, insiderStatus, insiderDetail))

	var risks []CapitalRiskSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id = ? AND active = ?", securityBatchID, universe.SecurityID, true).Find(&risks).Error; err != nil {
		return result, err
	}
	evidence.CapitalRisks = risks
	blocksA, blocksB := activeRiskBlocks(risks)
	riskActual := "无活跃阻断风险"
	riskDetail := "未检测到当前有效的融资、稀释或持续经营阻断。"
	if len(risks) > 0 {
		kinds := make([]string, 0, len(risks))
		for _, risk := range risks {
			kinds = append(kinds, risk.Kind)
		}
		riskActual, riskDetail = strings.Join(kinds, "、"), "活跃风险事件来自当前 SEC 提交记录。"
	}
	riskBStatus := EligibilityStatusPass
	if blocksB {
		riskBStatus = EligibilityStatusFail
	}
	result.Conditions = append(result.Conditions, eligibilityCondition("capital_risk_b", "融资/稀释风险", "B级", "无 B 级阻断", riskActual, riskBStatus, riskDetail))
	riskAStatus := EligibilityStatusPass
	if blocksA || blocksB {
		riskAStatus = EligibilityStatusFail
	}
	result.Conditions = append(result.Conditions, eligibilityCondition("capital_risk_a", "融资/稀释风险", "A级", "无 A/B 级阻断", riskActual, riskAStatus, riskDetail))

	sector := SectorRatingForSIC(identity.SIC)
	sectorStatus := EligibilityStatusPass
	if sector.Score < result.Criteria.BMinSectorScore {
		sectorStatus = EligibilityStatusFail
	}
	sectorDetail := fmt.Sprintf("基于 SIC %d 归类为 %s，当前赛道分为 %d/10。", identity.SIC, sector.Category, sector.Score)
	result.Conditions = append(result.Conditions, eligibilityCondition("sector_rating", "赛道评分", "B级", fmt.Sprintf("赛道评分 ≥ %d/10", result.Criteria.BMinSectorScore), fmt.Sprintf("%s · %d/10", sector.Category, sector.Score), sectorStatus, sectorDetail))

	overrides, err := activeCandidateBusinessModels(ctx, db, []uint{universe.SecurityID})
	if err != nil {
		return result, err
	}
	var override *CandidateBusinessModelOverride
	if row, ok := overrides[universe.SecurityID]; ok {
		override = &row
		evidence.BusinessModel = override
	}
	businessModel := candidateBusinessModelEvidence(override, sector.Category == "生物医药")
	modelAllowsA := businessModel.Model != CandidateBusinessModelClinicalPreRevenue && !(businessModel.Model == CandidateBusinessModelMixedOrLicensing && !businessModel.RevenueRepeatableConfirmed) && businessModel.Model != CandidateBusinessModelUnknown
	modelStatus := EligibilityStatusPass
	if !modelAllowsA {
		modelStatus = EligibilityStatusFail
	}
	modelActual := businessModel.Model
	if modelActual == CandidateBusinessModelNotApplicable {
		modelActual = "不适用（非生物医药）"
	}
	result.Conditions = append(result.Conditions, eligibilityCondition("business_model", "业务模型校准", "A级", "非临床前、非未确认的里程碑/授权收入", modelActual, modelStatus, businessModel.RevenueScoreCapReason))

	marketAStatus := EligibilityStatusUnavailable
	if universe.QualityStatus == QualityStatusValid && universe.MarketCapUSD > 0 {
		marketAStatus = EligibilityStatusPass
		if universe.MarketCapUSD < result.Criteria.MarketCapMinUSD || universe.MarketCapUSD >= result.Criteria.AMarketCapMaxExclusiveUSD {
			marketAStatus = EligibilityStatusFail
		}
	}
	result.Conditions = append(result.Conditions, eligibilityCondition("market_cap_a", "市值", "A级", fmt.Sprintf("%s ≤ 市值 < %s", formatEligibilityUSD(result.Criteria.MarketCapMinUSD), formatEligibilityUSD(result.Criteria.AMarketCapMaxExclusiveUSD)), marketActual, marketAStatus, marketDetail))

	result.EligibleB = result.InSmallCapPool && growthStatus == EligibilityStatusPass && riskBStatus == EligibilityStatusPass && sectorStatus == EligibilityStatusPass
	result.EligibleA = marketAStatus == EligibilityStatusPass && growthAStatus == EligibilityStatusPass && runwayStatus == EligibilityStatusPass && insiderStatus == EligibilityStatusPass && riskAStatus == EligibilityStatusPass && modelStatus == EligibilityStatusPass
	switch {
	case result.EligibleA:
		result.Grade, result.Summary = CandidateGradeA, "满足当前 A 级候选规则。"
	case result.EligibleB:
		result.Grade, result.Summary = CandidateGradeB, "满足当前 B 级候选规则。"
	case result.InSmallCapPool:
		result.Grade, result.Summary = CandidateGradeExcluded, "属于小盘候选池，但尚未满足 A/B 级全部条件。"
	default:
		result.Grade, result.Summary = CandidateGradeExcluded, "当前不在小盘候选池或关键证据不可用。"
	}
	return persistSmallCapEligibilityCheck(ctx, db, ticker, result)
}

func checkEligibilityWithoutUniverse(ctx context.Context, db *gorm.DB, ticker string, result SmallCapEligibilityCheckResult) (SmallCapEligibilityCheckResult, error) {
	var identity SecurityBatchIdentity
	err := db.WithContext(ctx).First(&identity, "batch_id = ? AND ticker = ?", result.SecurityBatchID, ticker).Error
	if err == nil {
		result.SecurityID, result.CompanyName = identity.SecurityID, identity.CompanyName
		if result.Evidence == nil {
			result.Evidence = &SmallCapEligibilityEvidenceSnapshot{}
		}
		result.Evidence.Identity = &identity
		result.Summary = "该标的已在安全宇宙中，但当前行情批次没有可用于市值计算的快照。"
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		result.Summary = "当前安全宇宙未找到该 Ticker；请确认代码或先完成一次证券宇宙同步。"
	} else {
		return result, err
	}
	result.Conditions = append(result.Conditions,
		eligibilityCondition("ticker_identity", "Ticker 身份", "基础", "当前安全宇宙可识别", ticker, EligibilityStatusUnavailable, result.Summary),
		eligibilityCondition("small_cap_pool", "候选池市值", "基础", fmt.Sprintf("%s ≤ 市值 < %s", formatEligibilityUSD(result.Criteria.MarketCapMinUSD), formatEligibilityUSD(result.Criteria.BMarketCapMaxExclusiveUSD)), "无当前行情快照", EligibilityStatusUnavailable, "本检查不会发起外部行情请求。"),
	)
	return persistSmallCapEligibilityCheck(ctx, db, ticker, result)
}

func ListSmallCapEligibilityCheckHistory(ctx context.Context, db *gorm.DB, page, pageSize int, ticker string) (SmallCapEligibilityCheckHistoryPage, error) {
	page, pageSize, err := normalizePage(page, pageSize)
	if err != nil {
		return SmallCapEligibilityCheckHistoryPage{}, err
	}
	result := SmallCapEligibilityCheckHistoryPage{Page: page, PageSize: pageSize, Items: []SmallCapEligibilityCheckHistoryItem{}}
	if db == nil || ctx == nil {
		return result, errors.New("database and context are required")
	}
	query := db.WithContext(ctx).Model(&SmallCapEligibilityCheckHistory{})
	if symbol := strings.ToUpper(strings.TrimSpace(ticker)); symbol != "" {
		query = query.Where("ticker = ? OR requested_ticker = ?", symbol, symbol)
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	var rows []SmallCapEligibilityCheckHistory
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		item := SmallCapEligibilityCheckHistoryItem{ID: row.ID, RequestedTicker: row.RequestedTicker, Ticker: row.Ticker, CompanyName: row.CompanyName, SecurityID: row.SecurityID, MarketBatchID: row.MarketBatchID, SecurityBatchID: row.SecurityBatchID, InSmallCapPool: row.InSmallCapPool, EligibleA: row.EligibleA, EligibleB: row.EligibleB, Grade: row.Grade, CreatedAt: row.CreatedAt, Result: SmallCapEligibilityCheckResult{Conditions: []SmallCapEligibilityCondition{}}}
		if err := json.Unmarshal([]byte(row.ResultJSON), &item.Result); err != nil {
			return result, fmt.Errorf("decode eligibility check %d: %w", row.ID, err)
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func persistSmallCapEligibilityCheck(ctx context.Context, db *gorm.DB, requestedTicker string, result SmallCapEligibilityCheckResult) (SmallCapEligibilityCheckResult, error) {
	var previous SmallCapEligibilityCheckHistory
	if err := db.WithContext(ctx).Where("ticker = ?", result.Ticker).Order("created_at DESC, id DESC").First(&previous).Error; err == nil {
		var previousResult SmallCapEligibilityCheckResult
		if json.Unmarshal([]byte(previous.ResultJSON), &previousResult) == nil {
			result.Comparison = compareSmallCapEligibilityResults(previousResult, result)
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	row := SmallCapEligibilityCheckHistory{RequestedTicker: requestedTicker, Ticker: result.Ticker, CompanyName: result.CompanyName, SecurityID: result.SecurityID, MarketBatchID: result.MarketBatchID, SecurityBatchID: result.SecurityBatchID, InSmallCapPool: result.InSmallCapPool, EligibleA: result.EligibleA, EligibleB: result.EligibleB, Grade: result.Grade, ResultJSON: string(payload), CreatedAt: result.CheckedAt}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		return result, err
	}
	return result, nil
}

func compareSmallCapEligibilityResults(previous, current SmallCapEligibilityCheckResult) *SmallCapEligibilityComparison {
	previousByKey := make(map[string]SmallCapEligibilityCondition, len(previous.Conditions))
	for _, condition := range previous.Conditions {
		previousByKey[condition.Key] = condition
	}
	comparison := &SmallCapEligibilityComparison{PreviousCheckedAt: previous.CheckedAt, PreviousGrade: previous.Grade, PreviousMarketAsOf: previous.MarketAsOf, PreviousSecurityAsOf: previous.SecurityAsOf, Changes: []SmallCapEligibilityConditionChange{}}
	for _, condition := range current.Conditions {
		old, ok := previousByKey[condition.Key]
		if !ok || old.Actual != condition.Actual || old.Status != condition.Status {
			comparison.Changes = append(comparison.Changes, SmallCapEligibilityConditionChange{Key: condition.Key, Label: condition.Label, PreviousActual: old.Actual, CurrentActual: condition.Actual, PreviousStatus: old.Status, CurrentStatus: condition.Status})
		}
	}
	return comparison
}

func eligibilityCondition(key, label, appliesTo, requirement, actual, status, detail string) SmallCapEligibilityCondition {
	return SmallCapEligibilityCondition{Key: key, Label: label, AppliesTo: appliesTo, Requirement: requirement, Actual: actual, Status: status, Detail: detail}
}

func formatEligibilityUSD(value int64) string {
	if value >= 1_000_000_000 {
		return fmt.Sprintf("$%.0fB", float64(value)/1_000_000_000)
	}
	return fmt.Sprintf("$%.0fM", float64(value)/1_000_000)
}

func marketCapCondition(universe UniverseSnapshot, criteria CandidateSelectionCriteria, marketAsOf string) (string, string, string) {
	if universe.QualityStatus != QualityStatusValid || universe.MarketCapUSD <= 0 {
		actual := fmt.Sprintf("行情/股本快照缺失（%s）", stringOrDefault(universe.ReasonCode, "无原因码"))
		detail := fmt.Sprintf("行情批次有效日：%s；快照质量：%s；原因：%s。当前没有同时可用的收盘价与 SEC 股本快照，因而不能计算市值；待下一次行情同步补齐后重新检查。", stringOrDefault(marketAsOf, "未知"), stringOrDefault(universe.QualityStatus, QualityStatusMissing), marketCapUnavailableReason(universe.ReasonCode))
		return EligibilityStatusUnavailable, actual, detail
	}
	actual := formatEligibilityUSD(universe.MarketCapUSD)
	if universe.MarketCapUSD >= criteria.MarketCapMinUSD && universe.MarketCapUSD < criteria.BMarketCapMaxExclusiveUSD {
		return EligibilityStatusPass, actual, "由当前批次收盘价 × SEC 股本快照计算。"
	}
	return EligibilityStatusFail, actual, "由当前批次收盘价 × SEC 股本快照计算。"
}

func marketCapUnavailableReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case ReasonShareCapitalEvent:
		return "SEC 显示当前股本快照之后发生了会改变股本的资本事件，旧股本不再可安全用于市值计算"
	case ReasonShareSplitMismatch:
		return "SEC 显示当前股本快照之后发生了拆股或反向拆股，旧股本不再可安全用于市值计算"
	case ReasonShareMultipleClasses:
		return "发现多类别普通股，当前不能安全选定用于市值计算的股本口径"
	case ReasonShareFactStale:
		return "SEC 股本快照已超过允许时效"
	case ReasonShareFactMissing:
		return "未找到可用的 SEC 普通股股数快照"
	}
	return stringOrDefault(reason, "当前行情或股本快照未通过质量校验")
}

func eligibilityGrowthActual(financial FinancialMetricSnapshot, growth float64, available bool, basis string) (string, string) {
	if !available {
		return "收入同比不可用", "当前财务快照无法形成可比季度或年度收入同比。"
	}
	if basis == "quarterly_revenue_yoy_pct" {
		return fmt.Sprintf("季度同比 %.1f%%", growth), fmt.Sprintf("最新可比季度：%s / %s", formatEligibilityUSD(financial.LatestQuarterRevenueUSD), formatEligibilityUSD(financial.PriorYearQuarterRevenueUSD))
	}
	return fmt.Sprintf("年度同比 %.1f%%", growth), fmt.Sprintf("季度不可比，回退年度：%s / %s", formatEligibilityUSD(financial.LatestAnnualRevenueUSD), formatEligibilityUSD(financial.PriorAnnualRevenueUSD))
}

func eligibilityRunway(financial FinancialMetricSnapshot, financialFound bool, criteria CandidateSelectionCriteria) (string, string, string) {
	if !financialFound || !financial.RunwayAvailable {
		return EligibilityStatusUnavailable, "现金 runway 不可用", "需要可用现金与现金流/消耗数据形成 runway。"
	}
	actual := fmt.Sprintf("%.1f 个月", financial.CashRunwayMonths)
	if financial.CashRunwayMonths >= criteria.ARunwayMinMonths {
		return EligibilityStatusPass, actual, fmt.Sprintf("可用现金 %s。", formatEligibilityUSD(int64(financial.AvailableCashUSD)))
	}
	return EligibilityStatusFail, actual, fmt.Sprintf("可用现金 %s。", formatEligibilityUSD(int64(financial.AvailableCashUSD)))
}

func eligibilityInsider(rows []InsiderTransactionSnapshot, coverage InsiderCoverageSnapshot, coverageFound bool, now time.Time, criteria CandidateSelectionCriteria) (string, string, string) {
	if coverageFound && coverage.Status != QualityStatusValid && coverage.Status != "complete" && coverage.Status != "covered" {
		return EligibilityStatusUnavailable, "Form 4 覆盖不可用", fmt.Sprintf("覆盖状态：%s；不能将无记录解释为无买入。", coverage.Status)
	}
	for _, row := range rows {
		if !row.Qualified || row.TransactionDate.Before(now.AddDate(0, 0, -criteria.InsiderLookbackDays)) || row.TransactionDate.After(now) {
			continue
		}
		if row.Role == InsiderRoleCEO || row.Role == InsiderRoleCFO || row.Role == InsiderRoleFounder {
			return EligibilityStatusPass, fmt.Sprintf("%s · %s", row.Role, row.TransactionDate.Format(time.DateOnly)), "Form 4 公开市场买入，且角色符合规则。"
		}
	}
	if !coverageFound {
		return EligibilityStatusUnavailable, "未保留 Form 4 覆盖结论", "无法据此确认近 180 日不存在合格买入。"
	}
	return EligibilityStatusFail, "近 180 日无合格买入", fmt.Sprintf("Form 4 已覆盖 %d 份文件，未发现 CEO/CFO/创始人的合格公开市场买入。", coverage.ParsedDocuments)
}
