package discovery

const (
	TradeSetupUnavailable    = "unavailable"
	TradeSetupWatching       = "watching"
	TradeSetupEntryCandidate = "entry_candidate"
	TradeSetupExitWarning    = "exit_warning"
	TradeSetupInvalidated    = "invalidated"
)

const (
	tradeSetupMinimumAverageDollarVolumeUSD = 5_000_000
	tradeSetupNearHighLimitPct              = -15.0
	tradeSetupMaximumInitialRiskPct         = 8.0
	tradeSetupMA20StopBufferPct             = 2.0
	tradeSetupProfitTargetLowPct            = 20.0
	tradeSetupProfitTargetHighPct           = 30.0
)

// CandidateTradeSetup is a deterministic daily-close trade plan. It is
// deliberately independent from the fundamental candidate score and does not
// claim intraday execution precision, ATR, or structural-low support.
type CandidateTradeSetup struct {
	Status                string   `json:"status"`
	Bias                  string   `json:"bias"`
	EntryTrigger          string   `json:"entry_trigger"`
	StopLossUSD           float64  `json:"stop_loss_usd"`
	RiskPct               float64  `json:"risk_pct"`
	TakeProfitZoneLowUSD  float64  `json:"take_profit_zone_low_usd"`
	TakeProfitZoneHighUSD float64  `json:"take_profit_zone_high_usd"`
	ExitReason            string   `json:"exit_reason"`
	Reasons               []string `json:"reasons"`
}

func unavailableCandidateTradeSetup(technicalStatus string) CandidateTradeSetup {
	reason := "日线历史不足，暂不生成交易计划"
	if technicalStatus == TechnicalStatusMissing {
		reason = "暂无可用日线，暂不生成交易计划"
	}
	return CandidateTradeSetup{
		Status:  TradeSetupUnavailable,
		Bias:    "neutral",
		Reasons: []string{reason},
	}
}

func buildCandidateTradeSetup(analysis CandidateTechnicalAnalysis) CandidateTradeSetup {
	if analysis.Status != TechnicalStatusReady || analysis.CloseUSD <= 0 {
		return unavailableCandidateTradeSetup(analysis.Status)
	}

	setup := CandidateTradeSetup{
		Status:                TradeSetupWatching,
		Bias:                  "neutral",
		Reasons:               []string{},
		TakeProfitZoneLowUSD:  analysis.CloseUSD * (1 + tradeSetupProfitTargetLowPct/100),
		TakeProfitZoneHighUSD: analysis.CloseUSD * (1 + tradeSetupProfitTargetHighPct/100),
	}
	setup.StopLossUSD, setup.RiskPct = candidateTradeStop(analysis)

	if analysis.MA50USD <= 0 || !analysis.MA200Available || analysis.MA200USD <= 0 {
		setup.Reasons = append(setup.Reasons, "等待 MA50 与 MA200 历史完整后确认长期趋势")
		return setup
	}
	if analysis.CloseUSD <= analysis.MA50USD {
		setup.Status = TradeSetupInvalidated
		setup.Bias = "defensive"
		setup.ExitReason = "收盘跌破 50 日均线，趋势条件失效"
		setup.Reasons = append(setup.Reasons, setup.ExitReason)
		return setup
	}
	if analysis.CloseUSD <= analysis.MA20USD {
		setup.Status = TradeSetupExitWarning
		setup.Bias = "defensive"
		setup.ExitReason = "收盘跌破 20 日均线，减仓或等待下一日确认"
		setup.Reasons = append(setup.Reasons, setup.ExitReason)
		return setup
	}

	trendReady := analysis.CloseUSD > analysis.MA20USD && analysis.MA20USD > analysis.MA50USD && analysis.MA50USD > analysis.MA200USD
	if !trendReady {
		setup.Reasons = append(setup.Reasons, "等待价格、MA20、MA50、MA200 形成多头排列")
		return setup
	}
	setup.Bias = "bullish"
	setup.Reasons = append(setup.Reasons, "价格与 MA20、MA50、MA200 维持多头排列")

	nearHigh := analysis.High200DayUSD > 0 && analysis.DistanceTo200DayHighPct >= tradeSetupNearHighLimitPct
	if !nearHigh {
		setup.Reasons = append(setup.Reasons, "距离 200 日高点超过 15%，等待强势结构修复")
	}
	liquid := analysis.AverageDollarVolume20 >= tradeSetupMinimumAverageDollarVolumeUSD
	if !liquid {
		setup.Reasons = append(setup.Reasons, "20 日平均成交额低于 $5M，暂不建议作为可执行入场")
	}
	if analysis.RelativeStrength.Status == "ready" && analysis.RelativeStrength.ExcessReturn20DPct != nil && *analysis.RelativeStrength.ExcessReturn20DPct <= 0 {
		setup.Reasons = append(setup.Reasons, "相对 IWM 的 20 日超额收益不为正")
	}
	if analysis.AnchoredVWAP.Status == "ready" && analysis.AnchoredVWAP.DistancePct <= 0 {
		setup.Reasons = append(setup.Reasons, "价格低于研究事件锚定价，等待重新站稳")
	}

	hasBreakout := candidateHasTechnicalSignal(analysis, TechnicalSignalBreakout20DayHigh)
	hasVolumeBreakout := candidateHasTechnicalSignal(analysis, TechnicalSignalVolumeBackedBreakout)
	hasMA20Reclaim := candidateHasTechnicalSignal(analysis, TechnicalSignalCrossAboveMA20) && analysis.VolumeRatio20 >= 1.2
	triggerReady := hasVolumeBreakout || hasBreakout || hasMA20Reclaim
	if !triggerReady {
		setup.Reasons = append(setup.Reasons, "等待放量突破 20 日高点或放量重回 MA20")
		return setup
	}
	if !nearHigh || !liquid || (analysis.RelativeStrength.Status == "ready" && analysis.RelativeStrength.ExcessReturn20DPct != nil && *analysis.RelativeStrength.ExcessReturn20DPct <= 0) || (analysis.AnchoredVWAP.Status == "ready" && analysis.AnchoredVWAP.DistancePct <= 0) {
		return setup
	}

	setup.Status = TradeSetupEntryCandidate
	if hasVolumeBreakout {
		setup.EntryTrigger = "放量突破 20 日最高收盘价"
	} else if hasBreakout {
		setup.EntryTrigger = "突破 20 日最高收盘价"
	} else {
		setup.EntryTrigger = "放量重回 20 日均线"
	}
	setup.Reasons = append(setup.Reasons, "满足日线入场筛选；仅在市场环境允许时执行")
	return setup
}

func candidateTradeStop(analysis CandidateTechnicalAnalysis) (float64, float64) {
	if analysis.CloseUSD <= 0 {
		return 0, 0
	}
	maximumLossStop := analysis.CloseUSD * (1 - tradeSetupMaximumInitialRiskPct/100)
	stop := maximumLossStop
	if analysis.MA20USD > 0 {
		ma20Stop := analysis.MA20USD * (1 - tradeSetupMA20StopBufferPct/100)
		if ma20Stop > stop && ma20Stop < analysis.CloseUSD {
			stop = ma20Stop
		}
	}
	riskPct := (analysis.CloseUSD - stop) / analysis.CloseUSD * 100
	return stop, riskPct
}
