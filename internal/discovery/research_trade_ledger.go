package discovery

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	ResearchTradeActionOpen   = "open"
	ResearchTradeActionAdd    = "add"
	ResearchTradeActionReduce = "reduce"
	ResearchTradeActionClose  = "close"
	ResearchTradeActionHold   = "hold"
	ResearchTradeSideBuy      = "buy"
	ResearchTradeSideSell     = "sell"
)

type ResearchTradeDecisionInput struct {
	Ticker           string     `json:"ticker"`
	Action           string     `json:"action"`
	DecidedAt        *time.Time `json:"decided_at"`
	PlannedPriceUSD  *float64   `json:"planned_price_usd"`
	TargetWeightPct  *float64   `json:"target_weight_pct"`
	StopLossUSD      *float64   `json:"stop_loss_usd"`
	TakeProfitUSD    *float64   `json:"take_profit_usd"`
	EvidenceAsOf     *time.Time `json:"evidence_as_of"`
	EvidenceSnapshot string     `json:"evidence_snapshot"`
	ScoringVersion   string     `json:"scoring_version"`
	Rationale        string     `json:"rationale"`
	Note             string     `json:"note"`
}

type ResearchTradeExecutionInput struct {
	DecisionID *uint      `json:"decision_id"`
	Ticker     string     `json:"ticker"`
	Side       string     `json:"side"`
	Shares     float64    `json:"shares"`
	PriceUSD   float64    `json:"price_usd"`
	FeesUSD    float64    `json:"fees_usd"`
	ExecutedAt *time.Time `json:"executed_at"`
	Note       string     `json:"note"`
}

type ResearchTradePosition struct {
	Ticker           string     `json:"ticker"`
	Shares           float64    `json:"shares"`
	AverageCostUSD   float64    `json:"average_cost_usd"`
	CurrentPriceUSD  *float64   `json:"current_price_usd,omitempty"`
	MarketValueUSD   *float64   `json:"market_value_usd,omitempty"`
	RealizedPnLUSD   float64    `json:"realized_pnl_usd"`
	UnrealizedPnLUSD *float64   `json:"unrealized_pnl_usd,omitempty"`
	NetPnLUSD        *float64   `json:"net_pnl_usd,omitempty"`
	TotalFeesUSD     float64    `json:"total_fees_usd"`
	FirstExecutionAt time.Time  `json:"first_execution_at"`
	LastExecutionAt  time.Time  `json:"last_execution_at"`
	LatestDecision   string     `json:"latest_decision"`
	LatestDecisionAt *time.Time `json:"latest_decision_at,omitempty"`
	OutcomeStatus    string     `json:"outcome_status"`
}

type ResearchTradeLedger struct {
	Positions        []ResearchTradePosition  `json:"positions"`
	Decisions        []ResearchTradeDecision  `json:"decisions"`
	Executions       []ResearchTradeExecution `json:"executions"`
	OpenPositions    int                      `json:"open_positions"`
	TotalMarketValue float64                  `json:"total_market_value_usd"`
	RealizedPnLUSD   float64                  `json:"realized_pnl_usd"`
	UnrealizedPnLUSD float64                  `json:"unrealized_pnl_usd"`
	NetPnLUSD        float64                  `json:"net_pnl_usd"`
	PricedPositions  int                      `json:"priced_positions"`
	GeneratedAt      time.Time                `json:"generated_at"`
}

func CreateResearchTradeDecision(ctx context.Context, db *gorm.DB, input ResearchTradeDecisionInput, now time.Time) (ResearchTradeDecision, error) {
	if db == nil {
		return ResearchTradeDecision{}, errors.New("database is required")
	}
	ticker := normalizeTicker(input.Ticker)
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if ticker == "" || !oneOf(action, ResearchTradeActionOpen, ResearchTradeActionAdd, ResearchTradeActionReduce, ResearchTradeActionClose, ResearchTradeActionHold) {
		return ResearchTradeDecision{}, errors.New("ticker and valid action are required")
	}
	if len([]rune(strings.TrimSpace(input.Rationale))) < 5 {
		return ResearchTradeDecision{}, errors.New("decision rationale must contain at least 5 characters")
	}
	for _, value := range []*float64{input.PlannedPriceUSD, input.StopLossUSD, input.TakeProfitUSD} {
		if value != nil && !finitePositive(*value) {
			return ResearchTradeDecision{}, errors.New("decision prices must be positive finite numbers")
		}
	}
	if input.TargetWeightPct != nil && !validResearchPercent(*input.TargetWeightPct) {
		return ResearchTradeDecision{}, errors.New("target weight must be between 0 and 100")
	}
	decidedAt := now.UTC()
	if input.DecidedAt != nil {
		decidedAt = input.DecidedAt.UTC()
	}
	decision := ResearchTradeDecision{
		Ticker: ticker, Action: action, DecidedAt: decidedAt, PlannedPriceUSD: input.PlannedPriceUSD,
		TargetWeightPct: input.TargetWeightPct, StopLossUSD: input.StopLossUSD, TakeProfitUSD: input.TakeProfitUSD,
		EvidenceAsOf: input.EvidenceAsOf, EvidenceSnapshot: strings.TrimSpace(input.EvidenceSnapshot),
		ScoringVersion: strings.TrimSpace(input.ScoringVersion), Rationale: strings.TrimSpace(input.Rationale),
		Note: strings.TrimSpace(input.Note), CreatedAt: now.UTC(),
	}
	if score, ok, err := currentCandidateScoreByTicker(ctx, db, ticker); err != nil {
		return ResearchTradeDecision{}, err
	} else if ok {
		decision.SecurityID = score.SecurityID
		if decision.ScoringVersion == "" {
			decision.ScoringVersion = score.ScoringVersion
		}
	}
	if err := db.WithContext(ctx).Create(&decision).Error; err != nil {
		return ResearchTradeDecision{}, err
	}
	return decision, nil
}

func CreateResearchTradeExecution(ctx context.Context, db *gorm.DB, input ResearchTradeExecutionInput, now time.Time) (ResearchTradeExecution, error) {
	if db == nil {
		return ResearchTradeExecution{}, errors.New("database is required")
	}
	ticker := normalizeTicker(input.Ticker)
	side := strings.ToLower(strings.TrimSpace(input.Side))
	if ticker == "" || !oneOf(side, ResearchTradeSideBuy, ResearchTradeSideSell) {
		return ResearchTradeExecution{}, errors.New("ticker and valid side are required")
	}
	if !finitePositive(input.Shares) || !finitePositive(input.PriceUSD) || math.IsNaN(input.FeesUSD) || math.IsInf(input.FeesUSD, 0) || input.FeesUSD < 0 {
		return ResearchTradeExecution{}, errors.New("shares and price must be positive; fees cannot be negative")
	}
	executedAt := now.UTC()
	if input.ExecutedAt != nil {
		executedAt = input.ExecutedAt.UTC()
	}
	if input.DecisionID != nil {
		var decision ResearchTradeDecision
		if err := db.WithContext(ctx).First(&decision, *input.DecisionID).Error; err != nil {
			return ResearchTradeExecution{}, errors.New("linked decision was not found")
		}
		if decision.Ticker != ticker {
			return ResearchTradeExecution{}, errors.New("linked decision ticker does not match execution")
		}
	}
	if side == ResearchTradeSideSell {
		quantity, err := researchQuantityBefore(ctx, db, ticker, executedAt)
		if err != nil {
			return ResearchTradeExecution{}, err
		}
		if input.Shares > quantity+1e-9 {
			return ResearchTradeExecution{}, errors.New("sell shares exceed the recorded long position")
		}
	}
	execution := ResearchTradeExecution{
		DecisionID: input.DecisionID, Ticker: ticker, Side: side, Shares: input.Shares,
		PriceUSD: input.PriceUSD, FeesUSD: input.FeesUSD, ExecutedAt: executedAt,
		Note: strings.TrimSpace(input.Note), CreatedAt: now.UTC(),
	}
	if score, ok, err := currentCandidateScoreByTicker(ctx, db, ticker); err != nil {
		return ResearchTradeExecution{}, err
	} else if ok {
		execution.SecurityID = score.SecurityID
	}
	if err := db.WithContext(ctx).Create(&execution).Error; err != nil {
		return ResearchTradeExecution{}, err
	}
	return execution, nil
}

func ListResearchTradeLedger(ctx context.Context, db *gorm.DB, ticker string, now time.Time) (ResearchTradeLedger, error) {
	result := ResearchTradeLedger{Positions: []ResearchTradePosition{}, Decisions: []ResearchTradeDecision{}, Executions: []ResearchTradeExecution{}, GeneratedAt: now.UTC()}
	if db == nil {
		return result, errors.New("database is required")
	}
	queryTicker := normalizeTicker(ticker)
	decisionsQuery := db.WithContext(ctx).Order("decided_at DESC, id DESC")
	executionsQuery := db.WithContext(ctx).Order("executed_at ASC, id ASC")
	if queryTicker != "" {
		decisionsQuery = decisionsQuery.Where("ticker = ?", queryTicker)
		executionsQuery = executionsQuery.Where("ticker = ?", queryTicker)
	}
	if err := decisionsQuery.Find(&result.Decisions).Error; err != nil {
		return result, err
	}
	if err := executionsQuery.Find(&result.Executions).Error; err != nil {
		return result, err
	}
	type state struct {
		qty, avg, realized, fees float64
		first, last              time.Time
	}
	states := map[string]*state{}
	for _, execution := range result.Executions {
		item := states[execution.Ticker]
		if item == nil {
			item = &state{first: execution.ExecutedAt}
			states[execution.Ticker] = item
		}
		item.last, item.fees = execution.ExecutedAt, item.fees+execution.FeesUSD
		if execution.Side == ResearchTradeSideBuy {
			item.avg = (item.avg*item.qty + execution.PriceUSD*execution.Shares + execution.FeesUSD) / (item.qty + execution.Shares)
			item.qty += execution.Shares
		} else {
			item.realized += (execution.PriceUSD-item.avg)*execution.Shares - execution.FeesUSD
			item.qty -= execution.Shares
			if math.Abs(item.qty) < 1e-9 {
				item.qty, item.avg = 0, 0
			}
		}
	}
	latestDecision := map[string]ResearchTradeDecision{}
	for _, decision := range result.Decisions {
		if _, exists := latestDecision[decision.Ticker]; !exists {
			latestDecision[decision.Ticker] = decision
		}
	}
	for symbol, item := range states {
		position := ResearchTradePosition{Ticker: symbol, Shares: item.qty, AverageCostUSD: item.avg, RealizedPnLUSD: item.realized, TotalFeesUSD: item.fees, FirstExecutionAt: item.first, LastExecutionAt: item.last, OutcomeStatus: "closed"}
		if decision, ok := latestDecision[symbol]; ok {
			position.LatestDecision, position.LatestDecisionAt = decision.Action, &decision.DecidedAt
		}
		if item.qty > 0 {
			position.OutcomeStatus = "open"
			result.OpenPositions++
		}
		if price, ok, err := latestResearchClose(ctx, db, symbol); err != nil {
			return result, err
		} else if ok {
			position.CurrentPriceUSD = &price
			marketValue, unrealized := price*item.qty, (price-item.avg)*item.qty
			net := item.realized + unrealized
			position.MarketValueUSD, position.UnrealizedPnLUSD, position.NetPnLUSD = &marketValue, &unrealized, &net
			result.TotalMarketValue += marketValue
			result.UnrealizedPnLUSD += unrealized
			result.PricedPositions++
		}
		result.RealizedPnLUSD += item.realized
		result.Positions = append(result.Positions, position)
	}
	result.NetPnLUSD = result.RealizedPnLUSD + result.UnrealizedPnLUSD
	sort.Slice(result.Positions, func(i, j int) bool { return result.Positions[i].Ticker < result.Positions[j].Ticker })
	sort.Slice(result.Executions, func(i, j int) bool { return result.Executions[i].ExecutedAt.After(result.Executions[j].ExecutedAt) })
	return result, nil
}

func researchQuantityBefore(ctx context.Context, db *gorm.DB, ticker string, at time.Time) (float64, error) {
	var executions []ResearchTradeExecution
	if err := db.WithContext(ctx).Where("ticker = ? AND executed_at <= ?", ticker, at).Order("executed_at ASC, id ASC").Find(&executions).Error; err != nil {
		return 0, err
	}
	quantity := 0.0
	for _, item := range executions {
		if item.Side == ResearchTradeSideBuy {
			quantity += item.Shares
		} else {
			quantity -= item.Shares
		}
	}
	return quantity, nil
}

func latestResearchClose(ctx context.Context, db *gorm.DB, ticker string) (float64, bool, error) {
	var price PriceSnapshot
	err := db.WithContext(ctx).Where("symbol = ? AND close_micros > 0 AND quality_status = ?", ticker, QualityStatusValid).Order("trade_date DESC, id DESC").First(&price).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return float64(price.CloseMicros) / 1_000_000, true, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
