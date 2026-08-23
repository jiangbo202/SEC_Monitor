package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const smallCapPolicyPreviewChangeLimit = 100

var ErrSmallCapPolicyConflict = errors.New("small-cap policy changed concurrently")

type SmallCapPolicyCounts struct {
	PricedUniverse   int `json:"priced_universe"`
	InMarketCapScope int `json:"in_market_cap_scope"`
	GradeA           int `json:"grade_a"`
	GradeB           int `json:"grade_b"`
	Excluded         int `json:"excluded"`
}

type SmallCapPolicyCountDelta struct {
	PricedUniverse   int `json:"priced_universe"`
	InMarketCapScope int `json:"in_market_cap_scope"`
	GradeA           int `json:"grade_a"`
	GradeB           int `json:"grade_b"`
	Excluded         int `json:"excluded"`
}

type SmallCapPolicyChange struct {
	Ticker       string `json:"ticker"`
	MarketCapUSD int64  `json:"market_cap_usd"`
	BeforeGrade  string `json:"before_grade"`
	AfterGrade   string `json:"after_grade"`
	ChangeType   string `json:"change_type"`
}

type SmallCapPolicyPreview struct {
	BaseBatchID      string                     `json:"base_batch_id"`
	DataAsOf         string                     `json:"data_as_of"`
	ActivePolicy     SmallCapPolicyVersion      `json:"active_policy"`
	ProposedCriteria CandidateSelectionCriteria `json:"proposed_criteria"`
	Before           SmallCapPolicyCounts       `json:"before"`
	After            SmallCapPolicyCounts       `json:"after"`
	Delta            SmallCapPolicyCountDelta   `json:"delta"`
	ChangedCount     int                        `json:"changed_count"`
	Changes          []SmallCapPolicyChange     `json:"changes"`
	ChangesTruncated bool                       `json:"changes_truncated"`
	CanActivate      bool                       `json:"can_activate"`
	Warnings         []string                   `json:"warnings"`
}

type SmallCapPolicyRescoreResult struct {
	SourceBatchID    string               `json:"source_batch_id"`
	PublishedBatchID string               `json:"published_batch_id"`
	ScoredCount      int                  `json:"scored_count"`
	Before           SmallCapPolicyCounts `json:"before"`
	After            SmallCapPolicyCounts `json:"after"`
	DurationMS       int64                `json:"duration_ms"`
}

type SmallCapPolicyApplyResult struct {
	Status  string                      `json:"status"`
	Policy  SmallCapPolicyVersion       `json:"policy"`
	Rescore SmallCapPolicyRescoreResult `json:"rescore"`
}

// RescoreActiveSmallCapPolicy publishes a new immutable prescreen batch when
// the scoring engine version changed but the active policy values did not.
// It reuses only frozen local evidence and never invokes an external provider.
func RescoreActiveSmallCapPolicy(ctx context.Context, db *gorm.DB) (SmallCapPolicyRescoreResult, error) {
	started := time.Now()
	if db == nil || ctx == nil {
		return SmallCapPolicyRescoreResult{}, errors.New("database and context are required")
	}
	active, err := GetActiveSmallCapPolicy(ctx, db)
	if err != nil {
		return SmallCapPolicyRescoreResult{}, err
	}
	data, err := loadSmallCapProjectionData(ctx, db)
	if err != nil {
		return SmallCapPolicyRescoreResult{}, err
	}
	result := SmallCapPolicyRescoreResult{SourceBatchID: data.base.BatchID, PublishedBatchID: data.base.BatchID}
	var staleScores, totalScores int64
	if err := db.WithContext(ctx).Model(&CandidateScoreSnapshot{}).Where("batch_id = ?", data.base.BatchID).Count(&totalScores).Error; err != nil {
		return result, err
	}
	if err := db.WithContext(ctx).Model(&CandidateScoreSnapshot{}).
		Where("batch_id = ? AND (scoring_version = '' OR scoring_version <> ?)", data.base.BatchID, DiscoveryScoringVersion).
		Count(&staleScores).Error; err != nil {
		return result, err
	}
	if totalScores == 0 {
		staleScores++
	}
	result.Before, err = storedSmallCapPolicyCounts(ctx, db, data, active.Policy)
	if err != nil {
		return result, err
	}
	if staleScores == 0 {
		result.After = result.Before
		result.DurationMS = time.Since(started).Milliseconds()
		return result, nil
	}
	projected, afterCounts := projectSmallCapPolicy(data, active.Policy)
	result.After = afterCounts
	now := time.Now().UTC()
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var running int64
		if txErr := tx.Model(&DiscoverySyncRun{}).Where("status = ?", "running").Count(&running).Error; txErr != nil {
			return txErr
		}
		if running > 0 {
			return fmt.Errorf("%w: discovery sync is running", ErrSmallCapPolicyConflict)
		}
		currentPolicy, txErr := getActiveSmallCapPolicyTx(tx)
		if txErr != nil {
			return txErr
		}
		if currentPolicy.ID != active.ID {
			return ErrSmallCapPolicyConflict
		}
		var pointer CurrentBatchPointer
		if txErr := tx.First(&pointer, "kind = ?", BatchKindPrescreen).Error; txErr != nil {
			return txErr
		}
		if pointer.BatchID != data.base.BatchID {
			return ErrSmallCapPolicyConflict
		}
		batch, universeRows, scoreRows, providerRuns, txErr := buildDerivedSmallCapPolicyBatch(data, projected, active, now)
		if txErr != nil {
			return txErr
		}
		if txErr = tx.Create(&batch).Error; txErr != nil {
			return txErr
		}
		if len(universeRows) > 0 {
			if txErr = tx.CreateInBatches(universeRows, universeChunkSize).Error; txErr != nil {
				return txErr
			}
		}
		if len(scoreRows) > 0 {
			if txErr = tx.CreateInBatches(scoreRows, universeChunkSize).Error; txErr != nil {
				return txErr
			}
		}
		if len(providerRuns) > 0 {
			if txErr = tx.CreateInBatches(providerRuns, universeChunkSize).Error; txErr != nil {
				return txErr
			}
		}
		if txErr = persistCandidateSignalEvents(ctx, tx, batch, now); txErr != nil {
			return fmt.Errorf("persist score-version candidate signal events: %w", txErr)
		}
		pointer = CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID, UpdatedAt: now}
		if txErr = tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "kind"}}, DoUpdates: clause.AssignmentColumns([]string{"batch_id", "updated_at"})}).Create(&pointer).Error; txErr != nil {
			return txErr
		}
		result.PublishedBatchID = batch.BatchID
		result.ScoredCount = len(scoreRows)
		return nil
	})
	result.DurationMS = time.Since(started).Milliseconds()
	return result, err
}

func storedSmallCapPolicyCounts(ctx context.Context, db *gorm.DB, data smallCapProjectionData, policy SmallCapPolicy) (SmallCapPolicyCounts, error) {
	counts := SmallCapPolicyCounts{}
	for _, row := range data.universe {
		if row.SecurityID == 0 || row.QualityStatus != QualityStatusValid || row.MarketCapUSD <= 0 {
			continue
		}
		counts.PricedUniverse++
		if row.MarketCapUSD >= policy.MarketCapMinUSD && row.MarketCapUSD < policy.MarketCapMaxUSD {
			counts.InMarketCapScope++
		}
	}
	var rows []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ?", data.base.BatchID).Find(&rows).Error; err != nil {
		return counts, err
	}
	for _, row := range rows {
		switch row.Grade {
		case CandidateGradeA:
			counts.GradeA++
		case CandidateGradeB:
			counts.GradeB++
		default:
			counts.Excluded++
		}
	}
	return counts, nil
}

type smallCapProjectionRow struct {
	universe UniverseSnapshot
	score    CandidateScoreSnapshot
	inScope  bool
}

type smallCapProjectionData struct {
	base           UniverseBatch
	universe       []UniverseSnapshot
	metrics        map[uint]FinancialMetricSnapshot
	insiders       map[uint][]InsiderTransactionSnapshot
	risks          map[uint][]CapitalRiskSnapshot
	identities     map[uint]SecurityBatchIdentity
	businessModels map[uint]CandidateBusinessModelOverride
	providerRuns   []ProviderRun
	projectionAsOf time.Time
}

// PreviewSmallCapPolicyChange is deliberately read-only. It reuses the frozen
// local security, price, financial, insider and risk evidence from the current
// published batch and never invokes a provider.
func PreviewSmallCapPolicyChange(ctx context.Context, db *gorm.DB, proposed SmallCapPolicy) (SmallCapPolicyPreview, error) {
	if db == nil || ctx == nil {
		return SmallCapPolicyPreview{}, errors.New("database and context are required")
	}
	proposed, err := NormalizeSmallCapPolicy(proposed)
	if err != nil {
		return SmallCapPolicyPreview{}, err
	}
	active, err := GetActiveSmallCapPolicy(ctx, db)
	if err != nil {
		return SmallCapPolicyPreview{}, err
	}
	preview := SmallCapPolicyPreview{
		ActivePolicy: active, ProposedCriteria: CandidateSelectionCriteriaForPolicy(proposed),
		CanActivate: true, Changes: []SmallCapPolicyChange{}, Warnings: []string{},
	}
	data, err := loadSmallCapProjectionData(ctx, db)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		preview.Warnings = append(preview.Warnings, "needs_bootstrap")
		return preview, nil
	}
	if err != nil {
		return SmallCapPolicyPreview{}, err
	}
	preview.BaseBatchID, preview.DataAsOf = data.base.BatchID, data.base.EffectiveDate
	beforeRows, beforeCounts := projectSmallCapPolicy(data, active.Policy)
	afterRows, afterCounts := projectSmallCapPolicy(data, proposed)
	preview.Before, preview.After = beforeCounts, afterCounts
	preview.Delta = smallCapPolicyCountDelta(beforeCounts, afterCounts)
	preview.Changes, preview.ChangedCount = compareSmallCapProjections(beforeRows, afterRows)
	if len(preview.Changes) > smallCapPolicyPreviewChangeLimit {
		preview.Changes = preview.Changes[:smallCapPolicyPreviewChangeLimit]
		preview.ChangesTruncated = true
	}
	return preview, nil
}

// ApplySmallCapPolicy creates one immutable finalized policy version. When a
// market batch exists, it also creates a derived batch and changes the active
// policy and current batch pointer in the same database transaction.
func ApplySmallCapPolicy(ctx context.Context, db *gorm.DB, expectedActiveVersionID uint, input SmallCapPolicyDraftInput) (SmallCapPolicyApplyResult, error) {
	return applySmallCapPolicy(ctx, db, expectedActiveVersionID, input, SmallCapPolicyActivationActivate)
}

func applySmallCapPolicy(ctx context.Context, db *gorm.DB, expectedActiveVersionID uint, input SmallCapPolicyDraftInput, action string) (SmallCapPolicyApplyResult, error) {
	started := time.Now()
	if db == nil || ctx == nil || expectedActiveVersionID == 0 {
		return SmallCapPolicyApplyResult{}, errors.New("database, context and expected active policy id are required")
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return SmallCapPolicyApplyResult{}, errors.New("policy name is required")
	}
	policy, err := NormalizeSmallCapPolicy(input.Policy)
	if err != nil {
		return SmallCapPolicyApplyResult{}, err
	}
	input.Policy = policy
	active, err := GetActiveSmallCapPolicy(ctx, db)
	if err != nil {
		return SmallCapPolicyApplyResult{}, err
	}
	if active.ID != expectedActiveVersionID {
		return SmallCapPolicyApplyResult{}, ErrSmallCapPolicyConflict
	}
	hash, err := PolicyHash(policy)
	if err != nil {
		return SmallCapPolicyApplyResult{}, err
	}
	if hash == active.ContentSHA256 {
		return SmallCapPolicyApplyResult{Status: "unchanged", Policy: active}, nil
	}

	data, loadErr := loadSmallCapProjectionData(ctx, db)
	needsBootstrap := errors.Is(loadErr, gorm.ErrRecordNotFound)
	if loadErr != nil && !needsBootstrap {
		return SmallCapPolicyApplyResult{}, loadErr
	}
	var beforeRows, afterRows []smallCapProjectionRow
	var beforeCounts, afterCounts SmallCapPolicyCounts
	if !needsBootstrap {
		beforeRows, beforeCounts = projectSmallCapPolicy(data, active.Policy)
		afterRows, afterCounts = projectSmallCapPolicy(data, policy)
		_ = beforeRows
	}

	now := time.Now().UTC()
	result := SmallCapPolicyApplyResult{Status: "published"}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var running int64
		if txErr := tx.Model(&DiscoverySyncRun{}).Where("status = ?", "running").Count(&running).Error; txErr != nil {
			return txErr
		}
		if running > 0 {
			return fmt.Errorf("%w: discovery sync is running", ErrSmallCapPolicyConflict)
		}
		current, txErr := getActiveSmallCapPolicyTx(tx)
		if txErr != nil {
			return txErr
		}
		if current.ID != expectedActiveVersionID {
			return ErrSmallCapPolicyConflict
		}
		if !needsBootstrap {
			var pointer CurrentBatchPointer
			if txErr := tx.First(&pointer, "kind = ?", BatchKindPrescreen).Error; txErr != nil {
				return txErr
			}
			if pointer.BatchID != data.base.BatchID {
				return ErrSmallCapPolicyConflict
			}
		}

		created, txErr := createFinalizedSmallCapPolicyVersionTx(tx, input, now)
		if txErr != nil {
			return txErr
		}
		if needsBootstrap {
			if txErr := appendSmallCapPolicyActivationTx(tx, created.ID, current.ID, action, input.CreatedBy, now); txErr != nil {
				return txErr
			}
			result.Status, result.Policy = "needs_bootstrap", created
			return nil
		}

		batch, universeRows, scoreRows, providerRuns, txErr := buildDerivedSmallCapPolicyBatch(data, afterRows, created, now)
		if txErr != nil {
			return txErr
		}
		if txErr := tx.Create(&batch).Error; txErr != nil {
			return txErr
		}
		if len(universeRows) > 0 {
			if txErr := tx.CreateInBatches(universeRows, universeChunkSize).Error; txErr != nil {
				return txErr
			}
		}
		if len(scoreRows) > 0 {
			if txErr := tx.CreateInBatches(scoreRows, universeChunkSize).Error; txErr != nil {
				return txErr
			}
		}
		if len(providerRuns) > 0 {
			if txErr := tx.CreateInBatches(providerRuns, universeChunkSize).Error; txErr != nil {
				return txErr
			}
		}
		if txErr := persistCandidateSignalEvents(ctx, tx, batch, now); txErr != nil {
			return fmt.Errorf("persist policy candidate signal events: %w", txErr)
		}
		if txErr := appendSmallCapPolicyActivationTx(tx, created.ID, current.ID, action, input.CreatedBy, now); txErr != nil {
			return txErr
		}
		pointer := CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID, UpdatedAt: now}
		if txErr := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "kind"}}, DoUpdates: clause.AssignmentColumns([]string{"batch_id", "updated_at"})}).Create(&pointer).Error; txErr != nil {
			return txErr
		}
		result.Policy = created
		result.Rescore = SmallCapPolicyRescoreResult{
			SourceBatchID: data.base.BatchID, PublishedBatchID: batch.BatchID, ScoredCount: len(scoreRows),
			Before: beforeCounts, After: afterCounts,
		}
		return nil
	})
	if err != nil {
		return SmallCapPolicyApplyResult{}, err
	}
	result.Rescore.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

// RollbackSmallCapPolicyWithRescore copies the target policy into a new
// immutable version; historical rows are never made active again.
func RollbackSmallCapPolicyWithRescore(ctx context.Context, db *gorm.DB, expectedActiveVersionID, targetVersionID uint, actor, note string) (SmallCapPolicyApplyResult, error) {
	target, err := GetSmallCapPolicy(ctx, db, targetVersionID)
	if err != nil {
		return SmallCapPolicyApplyResult{}, err
	}
	if target.Status != SmallCapPolicyStatusFinalized {
		return SmallCapPolicyApplyResult{}, errors.New("rollback target must be finalized")
	}
	name := fmt.Sprintf("回滚至策略 v%d", target.Version)
	description := strings.TrimSpace(note)
	if description == "" {
		description = fmt.Sprintf("复制策略 v%d 作为新的不可变版本", target.Version)
	}
	return applySmallCapPolicy(ctx, db, expectedActiveVersionID, SmallCapPolicyDraftInput{
		Name: name, Description: description, CreatedBy: strings.TrimSpace(actor), Policy: target.Policy,
	}, SmallCapPolicyActivationRollback)
}

func loadSmallCapProjectionData(ctx context.Context, db *gorm.DB) (smallCapProjectionData, error) {
	var pointer CurrentBatchPointer
	if err := db.WithContext(ctx).First(&pointer, "kind = ?", BatchKindPrescreen).Error; err != nil {
		return smallCapProjectionData{}, err
	}
	var base UniverseBatch
	if err := db.WithContext(ctx).First(&base, "batch_id = ? AND kind = ? AND status = ?", pointer.BatchID, BatchKindPrescreen, BatchStatusPublished).Error; err != nil {
		return smallCapProjectionData{}, err
	}
	data := smallCapProjectionData{base: base}
	if err := db.WithContext(ctx).Where("batch_id = ?", base.BatchID).Order("security_id ASC").Find(&data.universe).Error; err != nil {
		return data, err
	}
	securityIDs := make([]uint, 0, len(data.universe))
	for _, row := range data.universe {
		if row.SecurityID != 0 {
			securityIDs = append(securityIDs, row.SecurityID)
		}
	}
	data.metrics = map[uint]FinancialMetricSnapshot{}
	data.insiders = map[uint][]InsiderTransactionSnapshot{}
	data.risks = map[uint][]CapitalRiskSnapshot{}
	data.identities = map[uint]SecurityBatchIdentity{}
	data.businessModels = map[uint]CandidateBusinessModelOverride{}
	data.projectionAsOf = base.StartedAt
	if data.projectionAsOf.IsZero() {
		data.projectionAsOf = time.Now().UTC()
	}
	if len(securityIDs) == 0 {
		return data, nil
	}
	var metrics []FinancialMetricSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", base.UniverseSourceVersion, securityIDs).Find(&metrics).Error; err != nil {
		return data, err
	}
	for _, row := range metrics {
		data.metrics[row.SecurityID] = row
	}
	var insiders []InsiderTransactionSnapshot
	if err := db.WithContext(ctx).Where("security_id IN ?", securityIDs).Find(&insiders).Error; err != nil {
		return data, err
	}
	for _, row := range insiders {
		data.insiders[row.SecurityID] = append(data.insiders[row.SecurityID], row)
	}
	var risks []CapitalRiskSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", base.UniverseSourceVersion, securityIDs).Find(&risks).Error; err != nil {
		return data, err
	}
	for _, row := range risks {
		data.risks[row.SecurityID] = append(data.risks[row.SecurityID], row)
	}
	var identities []SecurityBatchIdentity
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", base.UniverseSourceVersion, securityIDs).Find(&identities).Error; err != nil {
		return data, err
	}
	for _, row := range identities {
		data.identities[row.SecurityID] = row
	}
	models, err := activeCandidateBusinessModels(ctx, db, securityIDs)
	if err != nil {
		return data, err
	}
	data.businessModels = models
	if err := db.WithContext(ctx).Where("batch_id = ?", base.BatchID).Order("id ASC").Find(&data.providerRuns).Error; err != nil {
		return data, err
	}
	return data, nil
}

func projectSmallCapPolicy(data smallCapProjectionData, policy SmallCapPolicy) ([]smallCapProjectionRow, SmallCapPolicyCounts) {
	policy, err := NormalizeSmallCapPolicy(policy)
	if err != nil {
		policy = DefaultSmallCapPolicy()
	}
	rows := make([]smallCapProjectionRow, 0, len(data.universe))
	counts := SmallCapPolicyCounts{}
	for _, universe := range data.universe {
		if universe.SecurityID == 0 || universe.QualityStatus != QualityStatusValid || universe.MarketCapUSD <= 0 {
			continue
		}
		counts.PricedUniverse++
		identity := data.identities[universe.SecurityID]
		inMarketCapScope := universe.MarketCapUSD >= policy.MarketCapMinUSD && universe.MarketCapUSD < policy.MarketCapMaxUSD
		if inMarketCapScope {
			counts.InMarketCapScope++
		}
		inScope := inMarketCapScope && policyAllowsExchange(policy, identity.Exchange)
		metric := data.metrics[universe.SecurityID]
		sector := SectorRatingForSIC(identity.SIC)
		var override *CandidateBusinessModelOverride
		if value, ok := data.businessModels[universe.SecurityID]; ok {
			override = &value
		}
		score := ScoreDiscoveryCandidateWithPolicy(DiscoveryScoreInput{
			SecurityID: universe.SecurityID, Ticker: universe.Ticker, MarketCapUSD: universe.MarketCapUSD,
			Financial: metric, Insiders: data.insiders[universe.SecurityID], Risks: data.risks[universe.SecurityID],
			GrossMarginPct: metric.GrossMarginPct, SectorScore: sector.Score,
			BusinessModel: candidateBusinessModelEvidence(override, sector.Category == "生物医药"), AsOf: data.projectionAsOf,
		}, policy)
		if !inScope {
			score.EligibleA, score.EligibleB, score.Grade, score.ReasonCode = false, false, CandidateGradeExcluded, "policy_scope_excluded"
		}
		snapshot := CandidateScoreToSnapshot("", score, data.projectionAsOf)
		switch snapshot.Grade {
		case CandidateGradeA:
			counts.GradeA++
		case CandidateGradeB:
			counts.GradeB++
		default:
			counts.Excluded++
		}
		rows = append(rows, smallCapProjectionRow{universe: universe, score: snapshot, inScope: inScope})
	}
	return rows, counts
}

func compareSmallCapProjections(before, after []smallCapProjectionRow) ([]SmallCapPolicyChange, int) {
	beforeBySecurity := make(map[uint]smallCapProjectionRow, len(before))
	for _, row := range before {
		beforeBySecurity[row.universe.SecurityID] = row
	}
	changes := make([]SmallCapPolicyChange, 0)
	for _, current := range after {
		previous, ok := beforeBySecurity[current.universe.SecurityID]
		if !ok || (previous.score.Grade == current.score.Grade && previous.inScope == current.inScope) {
			continue
		}
		changeType := "grade_changed"
		switch {
		case !previous.inScope && current.inScope:
			changeType = "entered_scope"
		case previous.inScope && !current.inScope:
			changeType = "exited_scope"
		}
		changes = append(changes, SmallCapPolicyChange{
			Ticker: current.universe.Ticker, MarketCapUSD: current.universe.MarketCapUSD,
			BeforeGrade: previous.score.Grade, AfterGrade: current.score.Grade, ChangeType: changeType,
		})
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].ChangeType != changes[j].ChangeType {
			return changes[i].ChangeType < changes[j].ChangeType
		}
		return changes[i].Ticker < changes[j].Ticker
	})
	return changes, len(changes)
}

func smallCapPolicyCountDelta(before, after SmallCapPolicyCounts) SmallCapPolicyCountDelta {
	return SmallCapPolicyCountDelta{
		PricedUniverse:   after.PricedUniverse - before.PricedUniverse,
		InMarketCapScope: after.InMarketCapScope - before.InMarketCapScope,
		GradeA:           after.GradeA - before.GradeA,
		GradeB:           after.GradeB - before.GradeB,
		Excluded:         after.Excluded - before.Excluded,
	}
}

func getActiveSmallCapPolicyTx(tx *gorm.DB) (SmallCapPolicyVersion, error) {
	var activation SmallCapPolicyActivation
	if err := tx.Order("id DESC").First(&activation).Error; err != nil {
		return SmallCapPolicyVersion{}, err
	}
	var row SmallCapPolicyVersion
	if err := tx.First(&row, activation.PolicyVersionID).Error; err != nil {
		return row, err
	}
	return row, decodeSmallCapPolicy(&row)
}

func createFinalizedSmallCapPolicyVersionTx(tx *gorm.DB, input SmallCapPolicyDraftInput, now time.Time) (SmallCapPolicyVersion, error) {
	payload, hash, err := policyJSON(input.Policy)
	if err != nil {
		return SmallCapPolicyVersion{}, err
	}
	var latest int
	if err := tx.Model(&SmallCapPolicyVersion{}).Select("COALESCE(MAX(version), 0)").Scan(&latest).Error; err != nil {
		return SmallCapPolicyVersion{}, err
	}
	row := SmallCapPolicyVersion{
		Version: latest + 1, Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description),
		Status: SmallCapPolicyStatusFinalized, PolicyJSON: payload, ContentSHA256: hash,
		CreatedBy: strings.TrimSpace(input.CreatedBy), FinalizedAt: &now, Policy: input.Policy,
	}
	if err := tx.Create(&row).Error; err != nil {
		return SmallCapPolicyVersion{}, err
	}
	return row, decodeSmallCapPolicy(&row)
}

func appendSmallCapPolicyActivationTx(tx *gorm.DB, policyID, previousID uint, action, actor string, now time.Time) error {
	return tx.Create(&SmallCapPolicyActivation{
		PolicyVersionID: policyID, PreviousPolicyVersionID: previousID, Action: action,
		ActivatedBy: strings.TrimSpace(actor), ActivatedAt: now,
	}).Error
}

func buildDerivedSmallCapPolicyBatch(data smallCapProjectionData, projected []smallCapProjectionRow, policy SmallCapPolicyVersion, now time.Time) (UniverseBatch, []UniverseSnapshot, []CandidateScoreSnapshot, []ProviderRun, error) {
	var versions []SourceVersion
	if err := json.Unmarshal([]byte(data.base.SourceVersionsJSON), &versions); err != nil {
		return UniverseBatch{}, nil, nil, nil, fmt.Errorf("decode current market source versions: %w", err)
	}
	filtered := versions[:0]
	for _, version := range versions {
		if version.Source != "policy:small-cap" && version.Source != "scoring:small-cap" {
			filtered = append(filtered, version)
		}
	}
	effectiveAt, err := parseNYCivilDate(data.base.EffectiveDate)
	if err != nil {
		return UniverseBatch{}, nil, nil, nil, err
	}
	rubricJSON, rubricSHA := candidateScoringRubricJSON(policy.Policy)
	_ = rubricJSON
	versions, err = normalizeSourceVersions(data.base.EffectiveDate, append(filtered,
		SourceVersion{Source: "policy:small-cap", Version: fmt.Sprintf("v%d", policy.Version), SHA256: policy.ContentSHA256, EffectiveAt: effectiveAt},
		SourceVersion{Source: "scoring:small-cap", Version: DiscoveryScoringVersion, SHA256: rubricSHA, EffectiveAt: effectiveAt},
	)...)
	if err != nil {
		return UniverseBatch{}, nil, nil, nil, err
	}
	content := sha256.Sum256([]byte(data.base.ContentSHA256 + "\x00" + policy.ContentSHA256 + "\x00" + DiscoveryScoringVersion + "\x00" + rubricSHA))
	contentSHA := hex.EncodeToString(content[:])
	batchID, sourceJSON, err := batchIdentityWithPolicy(BatchKindPrescreen, data.base.EffectiveDate, versions, contentSHA, policy.ContentSHA256)
	if err != nil {
		return UniverseBatch{}, nil, nil, nil, err
	}
	batch := UniverseBatch{
		BatchID: batchID, Kind: BatchKindPrescreen, Status: BatchStatusPublished,
		EffectiveDate: data.base.EffectiveDate, SourceVersionsJSON: sourceJSON, ContentSHA256: contentSHA,
		RecordCount: len(data.universe), UniverseSourceVersion: data.base.UniverseSourceVersion,
		PriceSourceVersion: data.base.PriceSourceVersion, ShareSourceVersion: data.base.ShareSourceVersion,
		PolicyVersionID: policy.ID, PolicyVersion: policy.Version, PolicyContentSHA256: policy.ContentSHA256,
		PolicySnapshotJSON: policy.PolicyJSON, StartedAt: now, CompletedAt: &now,
	}

	projectionBySecurity := make(map[uint]smallCapProjectionRow, len(projected))
	for _, row := range projected {
		projectionBySecurity[row.universe.SecurityID] = row
	}
	universeRows := make([]UniverseSnapshot, len(data.universe))
	for index, source := range data.universe {
		source.ID, source.BatchID, source.CreatedAt = 0, batchID, now
		if row, ok := projectionBySecurity[source.SecurityID]; ok {
			if row.inScope {
				source.Included, source.Status, source.ReasonCode = true, EffectiveStatusPrescreen, ReasonQualifiedSmallCap
			} else {
				source.Included, source.Status, source.ReasonCode = false, EffectiveStatusExcluded, ReasonOutsideMarketCap
			}
		}
		universeRows[index] = source
	}
	scoreRows := make([]CandidateScoreSnapshot, 0, len(projected))
	for _, row := range projected {
		score := row.score
		score.ID, score.BatchID, score.CreatedAt = 0, batchID, now
		scoreRows = append(scoreRows, score)
	}
	providerRuns := make([]ProviderRun, len(data.providerRuns))
	for index, source := range data.providerRuns {
		source.ID, source.BatchID, source.CreatedAt = 0, batchID, now
		providerRuns[index] = source
	}
	return batch, universeRows, scoreRows, providerRuns, nil
}
