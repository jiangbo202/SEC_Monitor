export interface ApiResponse<T> {
  code: number
  message: string
  data: T
}

export interface LongbridgeQuoteProbeResult {
  provider: string
  endpoint: string
  symbol: string
  status: 'ok' | 'failed'
  error_kind?: string
  message?: string
  elapsed_millis: number
  quote_received: boolean
  quote_timestamp?: number
  last_done?: string
  volume?: number
}

export interface PageResult<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface InAppNotification {
  id: number
  event_key: string
  source: 'earnings_preview' | 'earnings_release' | 'technical_signal' | string
  scope: 'watch_target' | 'candidate' | string
  entity_kind: string
  target_id?: number
  ticker?: string
  company_name?: string
  severity: 'info' | 'success' | 'warning' | 'danger' | string
  title: string
  body?: string
  link?: string
  occurred_at: string
  created_at: string
  read_at?: string | null
}

export interface WatchTarget {
  id: number
  ticker: string
  company_name: string
  cik: string
  target_type: string
  fund_series_id?: string
  fund_class_id?: string
  identity_source?: string
  identity_note?: string
  group?: string
  status: string
  last_sync_at?: string | null
  last_sync_status?: string
  last_sync_error?: string
  last_new_filings?: number
  created_at: string
  updated_at: string
  technical?: CandidateTechnicalAnalysis
}

export interface EarningsPreview {
  id: number
  target_id: number
  ticker: string
  company_name?: string
  provider: string
  status: 'scheduled' | 'no_coverage' | 'unavailable' | string
  event_key?: string
  report_at?: string | null
  session?: string
  event_content?: string
  fiscal_year?: number
  fiscal_period?: string
  currency?: string
  eps_estimate?: number | null
  eps_actual?: number | null
  eps_surprise?: number | null
  revenue_estimate?: number | null
  revenue_actual?: number | null
  revenue_surprise?: number | null
  provider_updated_at?: string | null
  fetched_at?: string | null
  changed_at?: string | null
  change_summary?: string
  last_error?: string
}

export interface EarningsPreviewView {
  preview?: EarningsPreview | null
  message: string
}

export interface EarningsPreviewRefreshResult {
  target_id: number
  preview?: EarningsPreview
  fetched: boolean
  changed: boolean
  message: string
}

export interface FundIdentity {
  ticker: string
  cik: string
  series_id: string
  class_id: string
  fund_name?: string
  source: string
  evidence_url?: string
}

export interface TickerLookup {
  ticker: string
  cik: string
  company_name: string
  target_type: string
  fund_identity?: FundIdentity
  fund_candidates?: FundIdentity[]
  resolution_reason?: string
}

export interface Filing {
  id: number
  filing_id: string
  accession_number: string
  ticker: string
  cik: string
  company_name: string
  filing_type: string
  filing_date: string
  published_at?: string | null
  filing_url: string
  title: string
  pulled_at: string
  notification_status?: string
  notification_log_id?: number
}

export interface IPOFiling {
  id: number
  filing_id: string
  accession_number: string
  cik: string
  company_name: string
  filing_type: string
  filing_date: string
  accepted_at?: string | null
  filing_url: string
  title: string
  notified_at?: string | null
  created_at: string
  updated_at: string
}

export interface IPOOfferingEvent {
  id: number
  filing_id: string
  cik: string
  company_name: string
  offering_type: 'initial' | 'duplicate' | 'correction' | 'follow_on' | 'unknown'
  parse_status: 'parsed' | 'unsupported'
  parse_message?: string
  offer_price?: string
  shares_offered?: number
  gross_proceeds?: string
  filing_url: string
  filing_date: string
  accepted_at?: string | null
  notified_at?: string | null
}

export interface IPOCompany {
  cik: string
  company_name: string
  status: string
  first_filing_date: string
  latest_filing_date: string
  latest_accepted_at?: string | null
  latest_filing_type: string
  latest_filing_url: string
  latest_title: string
  filing_count: number
  notified: boolean
  matched_ticker?: string
  status_reason: string
  status_confidence: string
  status_source: string
  final_ticker?: string
  exchange?: string
  offer_price?: string
  shares_offered?: number
  gross_proceeds?: string
  listed_verified_at?: string | null
  listing_date?: string | null
  longbridge_listing_check_count?: number
  longbridge_listing_last_result?: string
  longbridge_listing_next_retry_at?: string | null
  market_data_source?: string
  market_data_confidence?: string
  market_data_updated_at?: string | null
  automatic_ticker?: string
  automatic_exchange?: string
  automatic_offer_price?: string
  automatic_shares_offered?: number
  automatic_gross_proceeds?: string
  lifecycle_checked_at?: string | null
  override_final_ticker?: string
  override_exchange?: string
  override_offer_price?: string
  override_shares_offered?: number
  override_listing_date?: string | null
  override_note?: string
  override_updated_at?: string | null
  followed: boolean
}

export interface IPOCalendarEvent {
  id: number
  event_key: string
  symbol?: string
  market?: string
  company_name?: string
  event_date: string
  session?: string
  content?: string
  currency?: string
  source: string
  last_seen_at: string
  created_at: string
  updated_at: string
}

export interface IPORadarRefreshResult {
  checked: number
  new_filings: number
  notified: number
}

export interface IPORadarHealthSync {
  started_at: string
  finished_at?: string | null
  status: string
  new_filings: number
}

export interface IPORadarAction {
  key: string
  severity: 'warning' | 'danger' | string
  disposition?: 'automatic' | 'manual' | string
  count: number
  attention?: string
  route?: string
  status?: string
}

export interface IPORadarHealth {
  pending_listing: number
  missing_market_mapping: number
  stale_lifecycle_checks: number
  unsupported_offering_events: number
  failed_notification_batches: number
  due_retry_batches: number
  dead_letter_batches: number
  latest_sync?: IPORadarHealthSync | null
  actions: IPORadarAction[]
}

export interface CandidateScore {
  id: number
  batch_id: string
  security_id: number
  ticker: string
  market_cap_usd: number
  grade: 'A' | 'B' | 'excluded' | string
  eligible_a: boolean
  eligible_b: boolean
  total_score: number
  revenue_growth_score: number
  cash_runway_score: number
  insider_score: number
  gross_margin_score: number
  dilution_risk_score: number
  sector_score: number
  revenue_growth_pct: number
  cash_runway_months: number
  recent_qualified_insider: boolean
  active_blocks_a: boolean
  active_blocks_b: boolean
  reason_code: string
  scoring_version: string
	scoring_rubric_sha256?: string
  business_model_at_score?: string
  revenue_score_cap_reason?: string
  created_at: string
  score_effective_date?: string
  price_close_usd?: number
  price_volume?: number
  price_trade_date?: string | null
  price_freshness_status?: 'current' | 'previous_trading_day' | 'stale' | 'future' | 'missing' | 'unknown' | string
  price_age_calendar_days?: number
  price_currency?: string
  price_quality_status?: string
  price_source?: string
  price_source_role?: 'primary' | 'fallback' | 'unknown' | string
  quality_tier?: string
  quality_tags?: string[]
  quality_adjusted_score?: number
  review_priority_score?: number
  review_priority_reasons?: ReviewPriorityReason[]
  recent_anomaly_labels?: string[]
  change_status?: string
  change_reasons?: CandidateChangeReason[]
  previous_total_score?: number | null
  previous_grade?: string
  performance?: CandidatePerformance
  sector_category?: string
  sector_label?: string
  sector_sic?: number
  sector_rating_score?: number
  revenue_growth_explanation?: RevenueGrowthExplanation
  capital_risk_summaries?: CapitalRiskSummary[]
  market_quality?: CandidateMarketQuality
  investability?: CandidateInvestability
  dilution_trend?: CandidateDilutionTrend
  technical?: CandidateTechnicalAnalysis
  research_readiness?: CandidateResearchReadiness
  evidence_completeness?: CandidateEvidenceCompleteness
  grade_explanation?: CandidateGradeExplanation
  cash_runway_status?: 'measured' | 'positive_cash_flow' | 'unavailable' | string
  business_model?: CandidateBusinessModelEvidence
  valuation?: CandidateValuation
  followed?: boolean
}

export interface CandidateEvidenceCompleteness {
  status: 'complete' | 'needs_review' | 'missing' | string
  reasons: string[]
}

export interface CandidateGradeExplanation {
  profile: 'a_strong_signal' | 'b_growth_watch' | 'excluded' | string
  summary: string
  unmet_a_conditions: string[]
  near_a: boolean
}

export interface CandidateScoringRule {
	condition: string
	points: number
}

export interface CandidateScoringDimension {
	key: string
	label: string
	max_points: number
	weight_pct: number
	evidence: string
	rules: CandidateScoringRule[]
	adjustment?: string
}

export interface CandidateScoringRubric {
	version: string
	name: string
	formula: string
	max_score: number
	dimensions: CandidateScoringDimension[]
	grade_rule_note: string
	disclaimer: string
	content_sha256: string
}

export interface CandidateValuation {
  status: 'ready' | 'partial' | 'insufficient' | string
  reasons: string[]
  market_cap_usd?: number | null
  cash_usd?: number | null
  total_debt_usd?: number | null
  enterprise_value_usd?: number | null
  ttm_revenue_usd?: number | null
  ttm_gross_profit_usd?: number | null
  ev_sales?: number | null
  ev_gross_profit?: number | null
  price_to_sales?: number | null
  net_cash_to_market_cap?: number | null
  price_trade_date: string
  financial_period_end: string
  share_instant: string
}

export interface CandidateBusinessModelEvidence {
  model: 'commercial' | 'clinical_pre_revenue' | 'mixed_or_licensing' | 'unknown' | 'not_applicable' | string
  revenue_repeatable_confirmed: boolean
  revenue_score_cap: number
  revenue_score_cap_reason: string
  requires_review: boolean
  reason: string
  source_url: string
  operator: string
  confirmed_at?: string | null
  review_due_at?: string | null
}

export interface CandidateResearchReadiness {
  status: 'ready' | 'research_only' | 'blocked' | string
  reasons: string[]
  financial_staleness_days: number
  financial_period_end: string
  insider_evidence_status: string
}

export interface CandidateResearchNextStep {
  status: 'ready' | 'research_only' | 'blocked' | string
  priority: 'normal' | 'review' | 'blocked' | string
  action: string
  rationale: string
  reasons: string[]
}

export interface DiscoveryInsiderCoverage {
  batch_id: string
  security_id: number
  cik: string
  status: string
  eligible_filings: number
  downloaded_documents: number
  parsed_documents: number
  transaction_count: number
  permanent_document_failures: number
  transient_document_failures: number
  malformed_documents: number
  checked_at: string
}

export interface CandidateMarketQuality {
  sample_days: number
  average_dollar_volume_usd: number
  volatility_pct: number
  momentum_pct: number
  max_drawdown_pct: number
  status: string
}

export interface CandidateInvestability {
  status: 'tradable' | 'constrained' | 'blocked' | 'unknown' | string
  reasons: string[]
  sample_days: number
  average_dollar_volume_usd: number
  suggested_max_daily_notional_usd: number
  max_adv_participation_pct: number
  spread_evidence_status: string
}

export interface CandidateDilutionTrend {
  status: 'stable' | 'elevated_dilution' | 'high_dilution' | 'shares_reduced' | 'missing' | string
  reasons: string[]
  share_change_pct: number
  latest_shares: number
  prior_shares: number
  latest_instant: string
  prior_instant: string
  observation_days: number
}

export interface TechnicalHistoryBackfillResult {
  batch_id: string
  effective_date: string
  lookback_calendar_days: number
  candidate_count: number
  already_ready_count: number
  requested_count: number
	deferred_retry_count: number
	pending_retry_count: number
	retry_due_count: number
  benchmark_ticker: string
  benchmark_ready: boolean
  benchmark_requested: boolean
  benchmark_status: 'ready' | 'missing' | 'insufficient' | 'stale' | string
  benchmark_sample_days: number
  benchmark_required_days: number
  benchmark_latest_date: string
  record_count: number
  persisted_count: number
  source_record_counts: Record<string, number>
  failures: Array<{ ticker: string; reason: string; attempt_count: number; sample_days: number; required_days: number; next_retry_at?: string | null }>
  warnings: string[]
}

export interface CandidateTechnicalSignal {
  kind: 'cross_above_ma20' | 'breakout_20d_high' | 'volume_backed_breakout' | string
  label: string
  direction: 'bullish' | string
}

export interface CandidateTechnicalAnalysis {
  status: 'ready' | 'data_insufficient' | 'missing' | string
  sample_days: number
  required_sample_days: number
  trade_date: string
  close_usd: number
  ma20_usd: number
  ma50_usd: number
  ma200_usd: number
  ma200_available: boolean
  prior_close_usd: number
  prior_ma20_usd: number
  distance_to_ma20_pct: number
  prior_20d_high_usd: number
  distance_to_20d_high_pct: number
  average_volume_20: number
  volume_ratio_20: number
  dollar_volume_usd: number
  average_dollar_volume_20: number
  dollar_volume_ratio_20: number
  liquidity_status: 'unknown' | 'low' | 'limited' | 'normal' | string
  high_50d_usd: number
  distance_to_50d_high_pct: number
  high_200d_usd: number
  distance_to_200d_high_pct: number
  relative_strength: CandidateRelativeStrength
  anchored_vwap: CandidateAnchoredVWAP
  oscillator: CandidateOscillatorAnalysis
	signals: CandidateTechnicalSignal[]
	trade_setup: CandidateTradeSetup
  adjustment_review?: {
    status: 'adjusted' | 'unverified' | 'review_required' | string
    quality_status: string
    event_kinds: string[]
    detail: string
  }
}

export interface CandidateOscillatorAnalysis {
  status: 'ready' | 'data_insufficient' | 'missing' | string
  rsi_14?: number | null
  k?: number | null
  d?: number | null
  j?: number | null
  kdj_method: 'ohlc_9_3_3' | 'close_range_9_3_3' | string
  signal: 'bullish' | 'bearish' | 'caution' | 'watch' | 'neutral' | 'unavailable' | string
  label: string
  reasons: string[]
}

export interface CandidateTradeSetup {
  status: 'unavailable' | 'watching' | 'entry_candidate' | 'exit_warning' | 'invalidated' | string
  bias: 'bullish' | 'neutral' | 'defensive' | string
  entry_trigger: string
  stop_loss_usd: number
  risk_pct: number
  take_profit_zone_low_usd: number
  take_profit_zone_high_usd: number
  exit_reason: string
  reasons: string[]
  status_since?: string | null
}

export interface TradeSetupStatusEvent {
  id: number
  ticker: string
  trade_date: string
  status: string
  previous_status?: string
  bias: string
  entry_trigger?: string
  exit_reason?: string
  reasons: string[]
  close_usd: number
  stop_loss_usd: number
  risk_pct: number
  take_profit_zone_low_usd: number
  take_profit_zone_high_usd: number
  started_at: string
  recorded_at: string
}

export interface CandidateAnchoredVWAP {
  status: 'ready' | 'anchor_unavailable' | 'anchor_outside_local_history' | 'insufficient_price_history' | string
  anchor_event_type: string
  anchor_label: string
  anchor_trade_date: string
  price_trade_date: string
  trading_days: number
  approximate_vwap_usd: number
  distance_pct: number
  price_source: string
}

export interface CandidateRelativeStrength {
  status: 'ready' | 'partial' | 'missing' | 'insufficient_candidate_history' | 'insufficient_benchmark_history' | string
  benchmark_ticker: string
  matched_sample_days: number
  candidate_return_20d_pct?: number | null
  benchmark_return_20d_pct?: number | null
  excess_return_20d_pct?: number | null
  candidate_return_60d_pct?: number | null
  benchmark_return_60d_pct?: number | null
  excess_return_60d_pct?: number | null
}

export interface CandidateTechnicalHistoryRow {
  trade_date: string
  open_usd: number
  high_usd: number
  low_usd: number
  close_usd: number
  ohlc_available: boolean
  volume: number
  dollar_volume_usd: number
  source: string
  source_version: string
  backfilled: boolean
  rsi_14?: number | null
  k?: number | null
  d?: number | null
  j?: number | null
  kdj_method: 'ohlc_9_3_3' | 'close_range_9_3_3' | string
}

export interface TickerTechnicalHistory {
  ticker: string
  technical: CandidateTechnicalAnalysis
  history: CandidateTechnicalHistoryRow[]
}

export interface CandidateSelectionCriteria {
  scoring_version: string
	scoring_rubric: CandidateScoringRubric
  market_cap_min_usd: number
  a_market_cap_max_exclusive_usd: number
  b_market_cap_max_exclusive_usd: number
  a_revenue_growth_min_pct: number
  b_revenue_growth_min_pct: number
  a_revenue_growth_min_exclusive_pct: number
  b_revenue_growth_min_exclusive_pct: number
  a_runway_min_months: number
  insider_lookback_days: number
  b_min_sector_score: number
  allowed_exchanges: string[]
  max_price_age_trading_days: number
  minimum_price_usd: number
  blocked_adv_usd: number
  tradable_adv_usd: number
  minimum_history_days: number
  revenue_growth_selection: string
  qualified_insider_requirement: string
  active_capital_risk_requirement: string
}

export interface SmallCapPolicyEditableCriteria {
  market_cap_min_usd: number
  a_market_cap_max_exclusive_usd: number
  b_market_cap_max_exclusive_usd: number
}

export interface SmallCapPolicyVersion {
  id: number
  version: number
  status: 'active' | 'superseded' | 'draft' | string
  content_sha256: string
  name: string
  note?: string
  created_by?: string
  created_at: string
  activated_at?: string | null
  criteria: CandidateSelectionCriteria
}

export interface SmallCapPolicyState {
  active?: SmallCapPolicyVersion | null
  history: SmallCapPolicyVersion[]
}

export interface SmallCapPolicyPreviewCounts {
  priced_universe: number
  in_market_cap_scope: number
  grade_a: number
  grade_b: number
  excluded: number
}

export interface SmallCapPolicyPreviewChange {
  ticker: string
  market_cap_usd: number
  before_grade: string
  after_grade: string
  change_type: string
}

export interface SmallCapPolicyPreviewResult {
  base_batch_id?: string
  data_as_of?: string
  active_policy?: SmallCapPolicyVersion | null
  proposed_criteria: CandidateSelectionCriteria
  before: SmallCapPolicyPreviewCounts
  after: SmallCapPolicyPreviewCounts
  delta?: Partial<SmallCapPolicyPreviewCounts>
  changed_count: number
  changes: SmallCapPolicyPreviewChange[]
  changes_truncated?: boolean
  can_activate: boolean
  warnings: string[]
}

export interface SmallCapPolicyRescoreResult {
  source_batch_id?: string
  published_batch_id?: string
  scored_count: number
  before?: SmallCapPolicyPreviewCounts
  after?: SmallCapPolicyPreviewCounts
  duration_ms?: number
}

export interface SmallCapPolicyActivationResult {
  status: 'published' | 'unchanged' | 'needs_bootstrap' | string
  policy: SmallCapPolicyVersion
  rescore?: SmallCapPolicyRescoreResult
}

export type SmallCapEligibilityStatus = 'pass' | 'fail' | 'unavailable'

export interface SmallCapEligibilityCondition {
  key: string
  label: string
  applies_to: string
  requirement: string
  actual: string
  status: SmallCapEligibilityStatus
  detail?: string
}

export interface SmallCapEligibilityConditionChange {
  key: string
  label: string
  previous_actual: string
  current_actual: string
  previous_status: SmallCapEligibilityStatus
  current_status: SmallCapEligibilityStatus
}

export interface SmallCapEligibilityComparison {
  previous_checked_at: string
  previous_grade: string
  previous_market_as_of?: string
  previous_security_as_of?: string
  changes: SmallCapEligibilityConditionChange[]
}

export interface SmallCapEligibilityCheckResult {
  ticker: string
  company_name: string
  security_id?: number
  market_batch_id?: string
  security_batch_id?: string
  market_as_of?: string
  security_as_of?: string
  in_small_cap_pool: boolean
  eligible_a: boolean
  eligible_b: boolean
  grade: string
  summary: string
  conditions: SmallCapEligibilityCondition[]
  checked_at: string
  criteria: CandidateSelectionCriteria
  comparison?: SmallCapEligibilityComparison
}

export interface SmallCapEligibilityCheckHistoryItem {
  id: number
  requested_ticker: string
  ticker: string
  company_name: string
  security_id?: number
  market_batch_id?: string
  security_batch_id?: string
  in_small_cap_pool: boolean
  eligible_a: boolean
  eligible_b: boolean
  grade: string
  result: SmallCapEligibilityCheckResult
  created_at: string
}

export interface ReviewPriorityReason {
  label: string
  points: number
  kind: string
}

export interface CandidateChangeReason {
  field: string
  label: string
  previous: string
  current: string
  kind: string
}

export interface CandidatePerformance {
  base_date: string
  base_close: number
  date_1d: string
  close_1d: number
  return_1d?: number | null
  date_5d: string
  close_5d: number
  return_5d?: number | null
  date_20d: string
  close_20d: number
  return_20d?: number | null
}

export interface CandidateWatch {
  id: number
  ticker: string
  security_id: number
  cik: string
  company_name: string
  status: string
  note: string
  research_status: 'inbox' | 'researching' | 'conviction' | 'rejected' | string
  company_thesis_status: string
  security_readiness: string
  research_action: string
  action_threshold: string
  threshold_origin: string
  decision_rationale: string
  thesis: string
  risk_notes: string
  invalidation: string
  market_concern: string
  falsifiable_judgment: string
  catalyst: string
  catalyst_source: string
  catalyst_date?: string | null
  next_review_at?: string | null
  source_batch_id: string
  baseline_batch_id?: string
  baseline_captured_at?: string | null
  baseline_json?: string
  baseline?: CandidateWatchMetricSnapshot
  current?: CandidateWatchMetricSnapshot
  metric_changes?: CandidateWatchMetricChanges
  latest_score?: CandidateScore
  created_at: string
  updated_at: string
}

export interface CandidateWatchMetricSnapshot {
  batch_id: string
  score_effective_date: string
  captured_at: string
  price_close_usd: number
  price_volume: number
  price_trade_date?: string | null
  price_source?: string
  market_cap_usd: number
  total_score: number
  grade: string
  revenue_growth_pct: number
  cash_runway_months: number
  quality_tier: string
  research_readiness: string
  sector_category: string
}

export interface CandidateWatchMetricChanges {
  price_change_pct?: number | null
  market_cap_change_pct?: number | null
  volume_change_pct?: number | null
  score_change?: number | null
  revenue_growth_change_pct?: number | null
  cash_runway_change_months?: number | null
}

export interface CandidateReviewQueueItem extends CandidateWatch {
  review_state: 'overdue' | 'due_today' | 'upcoming' | string
  days_until_review: number
  current_candidate: boolean
}

export interface CandidateReviewQueue {
  as_of: string
  overdue_count: number
  due_today_count: number
  upcoming_count: number
  items: CandidateReviewQueueItem[]
}

export interface CandidateResearchMemoVersion {
  id: number
  ticker: string
  version: number
  security_id: number
  author: string
  company_thesis_status: string
  security_readiness: string
  research_action: string
  action_threshold: string
  threshold_origin: string
  decision_rationale: string
  thesis: string
  market_concern: string
  falsifiable_judgment: string
  catalyst: string
  catalyst_source: string
  catalyst_date?: string | null
  risk_notes: string
  invalidation: string
  next_review_at?: string | null
  created_at: string
}

export interface CandidateResearchPosition {
  id: number
  ticker: string
  security_id: number
  max_weight_pct: number
  reference_cost_usd?: number | null
  max_daily_volume_participation_pct: number
  event_risk_note: string
  liquidity_note: string
  note: string
  created_at: string
  updated_at: string
}

export interface DataQualityMetadata {
  layer: 'raw' | 'fact' | 'feature' | 'decision' | string
  source?: string
  source_version?: string
  as_of?: string
  quality_status: string
  fallback_used?: boolean
  coverage_pct?: number | null
}

export interface CandidateResearchPositionView extends CandidateResearchPosition {
  sector_category: string
  current_price_usd?: number | null
  return_since_reference_pct?: number | null
  research_readiness: string
  investability_status: string
  average_dollar_volume_usd: number
	estimated_daily_capacity_usd: number
  next_catalyst_at?: string | null
  risk_flags: string[]
  quality: DataQualityMetadata
}

export interface CandidateResearchPortfolio {
  total_max_weight_pct: number
  position_count: number
  sector_weights: Record<string, number>
  largest_sector: string
  largest_sector_weight_pct: number
	largest_position: string
	largest_position_weight_pct: number
	top_three_weight_pct: number
	concentration_index: number
	reference_weight_pct: number
	weighted_reference_return_pct?: number | null
	estimated_daily_capacity_usd: number
  constrained_count: number
  blocked_count: number
  data_gap_count: number
  event_risk_count: number
  upcoming_catalyst_count: number
	constrained_weight_pct: number
	blocked_weight_pct: number
	data_gap_weight_pct: number
	event_risk_weight_pct: number
	upcoming_catalyst_weight_pct: number
	risk_coverage: Record<string, string>
  warnings: string[]
  items: CandidateResearchPositionView[]
}

export interface ResearchActionGate {
  status: 'ready' | 'blocked' | string
  allowed: boolean
  scoring_version?: string
  effectiveness_status?: string
  outcome_tracking_status?: string
  as_of?: string
  reasons: string[]
  evaluated_at: string
}

export interface CandidateOverview {
  batch_id: string
  total: number
  grade_counts: Record<string, number>
  quality_tier_counts: Record<string, number>
  change_counts: Record<string, number>
  sector_counts: Record<string, number>
  quality_tag_counts: Record<string, number>
  top_candidates: CandidateScore[]
}

export interface CapitalRiskSummary {
  kind: string
  severity: string
  blocks_a: boolean
  blocks_b: boolean
  reason: string
  effective_at: string
}

export interface RevenueGrowthExplanation {
  method: string
  source: string
  revenue_growth_available: boolean
  quarterly_revenue_yoy_pct: number
  quarterly_revenue_qoq_pct: number
  annual_revenue_yoy_pct: number
  annual_revenue_qoq_pct: number
  latest_quarter_revenue_usd: number
  prior_year_quarter_revenue_usd: number
  previous_quarter_revenue_usd: number
  latest_annual_revenue_usd: number
  prior_annual_revenue_usd: number
  selected_revenue_growth_pct: number
  selected_revenue_growth_basis: string
  quality_flags_json: string
}

export interface DiscoverySecurity {
  id: number
  cik: string
  company_name: string
  sic: number
	 sic_description?: string
	 state_of_incorporation?: string
	 latest_annual_form?: string
  catalog_status: string
}

export interface CompanyProfile {
  ticker: string
  company_name: string
  cik: string
  exchange?: string
  sic: number
  sic_description?: string
  sector_category?: string
  state_of_incorporation?: string
  latest_annual_form?: string
  business_summary: string
  summary_source: string
  profile_provider?: string
  profile_fetched_at?: string | null
  profile_freshness?: 'fresh' | 'stale' | string
  website?: string
  founded?: string
  listing_date?: string
  market?: string
  address?: string
  employees?: string
  manager?: string
  year_end?: string
  metadata_as_of?: string | null
  status: 'available' | 'partial' | string
}

export interface CompanyProfileRecoveryItem {
  ticker: string
  company_name: string
  cik: string
  security_id: number
  retry_count: number
  last_attempt_at?: string | null
  next_retry_at?: string | null
  last_error: string
  retry_due: boolean
}

export interface CompanyProfileRecoveryQueue {
  items: CompanyProfileRecoveryItem[]
}

export interface CompanyProfileBulkRetryResult {
  queue_count: number
  budget: number
  attempted: number
  fetched: number
  failed: number
  stopped: boolean
  stop_reason?: string
  skipped: boolean
  message: string
}

export interface MarketPriceRecoveryItem {
  ticker: string
  security_id: number
  grade: string
  market_cap_usd: number
  issue: 'missing' | 'stale' | 'future' | 'local_fallback' | string
  issue_label: string
  price_trade_date?: string | null
  price_freshness_status: string
  price_age_calendar_days: number
  price_source: string
}

export interface MarketPriceRecoveryQueue {
  batch_id: string
  effective_date: string
  items: MarketPriceRecoveryItem[]
}

export interface DiscoveryFinancialMetric {
  quarterly_revenue_yoy_pct: number
  quarterly_revenue_qoq_pct: number
  annual_revenue_yoy_pct: number
  annual_revenue_qoq_pct: number
  cash_runway_months: number
  gross_margin_available: boolean
  gross_margin_pct: number
  revenue_growth_available: boolean
  runway_available: boolean
  quality_flags_json: string
}

export interface DiscoveryInsiderTransaction {
  owner_name: string
  officer_title: string
  role: string
  transaction_date: string
  transaction_code: string
  qualified: boolean
  value_micros: number
  source_url: string
}

export interface DiscoveryCapitalRisk {
  kind: string
  severity: string
  active: boolean
  blocks_a: boolean
  blocks_b: boolean
  reason: string
  effective_at: string
}

export interface DiscoveryEvidence {
  field: string
  value: string
  source: string
}

export interface SectorExplanation {
  sic: number
  category: string
  score: number
  label: string
  rationale: string
}

export interface RecentSECFiling {
  filing_id: string
  accession_number: string
  ticker: string
  cik: string
  company_name: string
  filing_type: string
  filing_date: string
  published_at?: string | null
  filing_url: string
  title: string
}

export interface ProfitHistoryPoint {
  period: string
  period_start: string
  period_end: string
  net_income_usd: number
  form: string
  concept: string
  source_url: string
  derived: boolean
}

export interface ProfitHistory {
  ticker: string
  as_of: string
  quarterly: ProfitHistoryPoint[]
  annual: ProfitHistoryPoint[]
}

export interface CandidateLineageItem {
  key: string
  label: string
  source: string
  as_of: string
  status: string
  detail: string
}

export interface CandidateDataLineage {
  score_batch_id: string
  evidence_batch_id: string
  batch_effective_date: string
  items: CandidateLineageItem[]
}

export interface CandidateScoreHistoryPoint {
  batch_id: string
  effective_date: string
  grade: string
  total_score: number
  score_delta?: number | null
  eligible_a: boolean
  eligible_b: boolean
  revenue_growth_pct: number
  cash_runway_months: number
  market_cap_usd: number
  active_blocks_a: boolean
  active_blocks_b: boolean
  scoring_version: string
  change_status: string
  change_reasons: CandidateChangeReason[]
}

export interface CandidateSignalEvent {
  id: number
  batch_id: string
  ticker: string
  grade: string
  event_type: string
  scoring_version: string
  total_score: number
  signal_date: string
  baseline_trade_date: string
  baseline_close_micros: number
  price_source: string
}

export interface CandidateDetail {
  batch_id: string
  security: DiscoverySecurity
  company_profile: CompanyProfile
	analyst_rating: AnalystRatingView
  market_research: CandidateMarketResearch
  score: CandidateScore
	scoring_rubric: CandidateScoringRubric
	 score_history: CandidateScoreHistoryPoint[]
  signal_events: CandidateSignalEvent[]
  financial?: DiscoveryFinancialMetric

  profit_history?: ProfitHistory
  insiders: DiscoveryInsiderTransaction[]
  insider_coverage?: DiscoveryInsiderCoverage | null
  capital_risks: DiscoveryCapitalRisk[]
  capital_risk_summary: CandidateCapitalRiskSummary
  sector: SectorExplanation
  business_model: CandidateBusinessModelEvidence
  valuation: CandidateValuation
	valuation_research: CandidateValuationResearch
  fair_value: CandidateFairValueEstimate
  research?: CandidateWatch | null
  research_versions: CandidateResearchMemoVersion[]
  research_readiness: CandidateResearchReadiness
  research_next_step: CandidateResearchNextStep
  technical: CandidateTechnicalAnalysis
	trade_setup_history?: TradeSetupStatusEvent[]
	investability: CandidateInvestability
  dilution_trend: CandidateDilutionTrend
	technical_history: CandidateTechnicalHistoryRow[]
  data_quality: Record<string, string>
	data_lineage: CandidateDataLineage
  evidence: DiscoveryEvidence[]
  recent_filings: RecentSECFiling[]
  catalysts?: CandidateCatalystEvent[]
}

export interface CandidateCatalystEvent {
  id: string
  ticker: string
  event_type: string
  title: string
  event_date: string
  timing_status: 'observed' | 'scheduled' | 'needs_source' | string
  evidence_type: 'fact' | 'user_judgment' | string
  source: string
  source_url?: string
  quality: DataQualityMetadata
}

export interface EPSForecastSnapshot {
  id: number
  security_id: number
  provider: string
  ticker: string
  forecast_start_date: string
  forecast_end_date: string
  mean?: number | null
  median?: number | null
  low?: number | null
  high?: number | null
  institution_total: number
  institution_up: number
  institution_down: number
  change_summary?: string
  fetched_at: string
}

export interface MarketAnomalySnapshot {
  id: number
  ticker: string
  name: string
  alert_name: string
  alert_time: string
  values_json: string
  emotion: number
  fetched_at: string
}

export interface InstitutionalHolderSnapshot {
  id: number
  ticker: string
  holder_name: string
  institution_type: string
  percent_of_shares?: number | null
  shares_changed?: number | null
  report_date: string
  source_url: string
  fetched_at: string
}

export interface FundHolderSnapshot {
  id: number
  ticker: string
  fund_code: string
  fund_symbol: string
  fund_name: string
  currency: string
  position_ratio: number
  report_date: string
  source_url: string
  fetched_at: string
}

export interface CandidateMarketResearch {
  eps_forecast: { latest?: EPSForecastSnapshot | null; history: EPSForecastSnapshot[]; message: string; quality?: DataQualityMetadata }
	eps_revision: { status: string; direction: string; forecast_start_date?: string; forecast_end_date?: string; current_median?: number | null; previous_median?: number | null; median_change_pct?: number | null; revision_breadth_pct?: number | null; compared_snapshots: number; message: string; quality?: DataQualityMetadata }
	earnings_surprise: { status: string; message: string }
  anomalies: MarketAnomalySnapshot[]
  institutional_holders: InstitutionalHolderSnapshot[]
  fund_holders: FundHolderSnapshot[]
  quality?: DataQualityMetadata
}

export interface TickerInstitutionalHoldingHistory {
  ticker: string
  institutional_holders: InstitutionalHolderSnapshot[]
  fund_holders: FundHolderSnapshot[]
  message: string
}

export interface ValuationMetricResearch { current?: number | null; low?: number | null; high?: number | null; median?: number | null; history: Array<{ date: string; value?: number | null }> }
export interface ValuationPercentileResearch { value?: number | null; low?: number | null; high?: number | null; median?: number | null; ranking?: number | null; rank_index: string; rank_total: string }
export interface CandidateValuationResearchSnapshot { id: number; ticker: string; metrics: { pe: ValuationMetricResearch; pb: ValuationMetricResearch; ps: ValuationMetricResearch }; percentiles: { pe: ValuationPercentileResearch; pb: ValuationPercentileResearch; ps: ValuationPercentileResearch }; peers: Array<{ symbol: string; name: string; currency: string; pe?: number | null; pb?: number | null; ps?: number | null }>; change_summary?: string; fetched_at: string; source_version?: string }
export interface CandidateValuationResearch { latest?: CandidateValuationResearchSnapshot | null; history: CandidateValuationResearchSnapshot[]; message: string; quality?: DataQualityMetadata }
export interface CandidateFairValueEstimate {
  status: 'available' | 'insufficient' | string
  currency: string
  reference_price?: number | null
  reference_price_date?: string
  reference_price_source?: string
  market_consensus_target?: number | null
  market_consensus_low?: number | null
  market_consensus_high?: number | null
  market_consensus_upside_pct?: number | null
  analyst_count: number
  local_historical_scenario?: { low: number; mid: number; high: number; metrics: number } | null
  metric_scenarios: Array<{ metric: string; current_multiple: number; historical_low: number; historical_mid: number; historical_high: number; price_low: number; price_mid: number; price_high: number }>
  methodology: string
  message: string
  quality?: DataQualityMetadata
}

export interface AnalystRatingSnapshot {
  id: number
  security_id: number
  provider: string
  ticker: string
  status: 'available' | 'no_coverage' | string
  recommendation: string
  strong_buy_count: number
  buy_count: number
  hold_count: number
  underperform_count: number
  sell_count: number
  no_opinion_count: number
  analyst_count: number
  target_average_micros: number
  target_high_micros: number
  target_low_micros: number
  reference_price_micros: number
  currency: string
  provider_updated_at_text: string
  change_summary?: string
  notification_status?: string
  fetched_at: string
  notified_at?: string | null
}

export interface AnalystRatingView {
  latest?: AnalystRatingSnapshot | null
  history: AnalystRatingSnapshot[]
  message: string
  quality?: DataQualityMetadata
}

export interface CandidateCapitalRiskSummary {
  total_events: number
  active_events: number
  recent_inactive_events: number
  historical_inactive_count: number
  latest_effective_at?: string | null
}

export interface CandidateSummary {
  batch_id: string
  total_a: number
  total_b: number
  items_a: CandidateScore[]
  items_b: CandidateScore[]
  event_notes: Record<string, string>
  message: string
}

export interface CandidateNotificationSettings {
  enabled: boolean
  notify_a: boolean
  notify_b: boolean
  send_time: string
  max_per_grade: number
  actionable_only: boolean
  min_review_priority_score: number
}

export interface CandidateNotificationPreview {
  enabled: boolean
  suppressed_reason: string
  settings: CandidateNotificationSettings
  summary: CandidateSummary
}

export interface CandidateNotificationSendInput {
  confirm: boolean
  force: boolean
}

export interface CandidateNotificationSendResult {
  preview: CandidateNotificationPreview
  batch: NotificationBatch
}

export interface CandidateHealth {
  batch_id: string
  status: 'ok' | 'degraded' | 'missing' | string
  total_candidates: number
  missing_financials: number
  missing_insiders: number
  insider_data_status: 'available' | 'missing' | string
  insider_lineage_status?: 'source_version' | 'coverage_snapshot' | 'partial_coverage_snapshot' | 'missing' | string
  candidates_with_insider_records: number
  insider_record_coverage_pct: number
  candidates_with_insider_coverage?: number
  insider_coverage_pct?: number
  insider_coverage_partial?: number
  insider_coverage_unavailable?: number
  insider_coverage_no_filings?: number
  qualified_insider_candidates: number
  no_qualified_insider_candidates: number
  candidates_with_recent_filings: number
  recent_filing_coverage_pct: number
  price_effective_date: string
  current_price_candidates: number
  fallback_price_candidates: number
  stale_price_candidates: number
  missing_price_candidates: number
  missing_market_cap: number
  active_risk_events: number
  pending_financial_recalculations?: number
  ready_candidates?: number
  research_only_candidates?: number
  blocked_candidates?: number
  open_data_quality_incidents?: number
	technical_history_retry_pending?: number
	technical_history_retry_due?: number
	technical_history_retry_deferred?: number
  issues: string[]
}

export interface DiscoveryWorkflowResult {
  status: 'published' | 'market_failed' | string
  batch_id: string
  security_batch_id?: string
  market_batch_id?: string
  summary: CandidateSummary
  health: CandidateHealth
  technical_history_warmup?: TechnicalHistoryWarmupResult
}

// DiscoverySyncRun is a lightweight lifecycle record. Unlike a published
// batch, it is created before the SEC phase starts so the page can show an
// active or failed run even if no market batch is produced.
export interface DiscoverySyncRun {
  id: number
  kind: 'full' | 'incremental' | 'market' | string
  status: 'running' | 'published' | 'failed' | string
  phase: 'security_universe' | 'market_prescreen' | 'technical_history' | 'completed' | 'failed' | string
  started_at: string
  completed_at?: string | null
  security_batch_id?: string
  market_batch_id?: string
  error_message?: string
  created_at: string
  updated_at: string
}

export interface DiscoverySyncStep {
  id: number
  run_id: number
  sequence: number
  phase: string
  status: 'running' | 'completed' | 'failed' | 'warning' | 'skipped' | string
  message: string
  record_count: number
  total_count: number
  started_at: string
  completed_at?: string | null
  created_at: string
  updated_at: string
}

export interface DiscoverySyncRunPage {
  page: number
  page_size: number
  total: number
  items: DiscoverySyncRun[]
}

export interface DiscoveryStorageHealth {
  database_path: string
  database_bytes: number
  cache_path: string
  cache_bytes: number
  cache_files: number
  status: 'ok' | 'warning' | 'error' | string
  issues: string[]
}

export interface DiscoveryCacheCleanupPreview {
  retention_days: number
  cutoff: string
  file_count: number
  bytes: number
}

export interface TechnicalHistoryWarmupResult {
  status: 'completed' | 'skipped' | 'warning' | string
  result: TechnicalHistoryBackfillResult
  error_message?: string
}

export interface DiscoveryBatch {
  batch_id: string
  kind: string
  status: string
  effective_date: string
  source_versions_json: string
  content_sha256: string
  record_count: number
  universe_source_version: string
  price_source_version: string
  share_source_version: string
  started_at: string
  completed_at?: string | null
  error_message: string
  provider_summary?: BatchProviderSummary | null
  candidate_count: number
}

export interface BatchProviderSummary {
  provider: string
  status: string
  expected_count: number
  record_count: number
  coverage_pct: number
  timely: boolean
  source_version: string
  error_message: string
  price_source_counts: Record<string, number>
  provider_attempts: ProviderAttempt[]
  fallback_used: boolean
}

export interface ProviderAttempt {
  provider: string
  status: 'success' | 'partial' | 'empty' | 'failed' | string
  source_version?: string
  expected: number
  records: number
  remaining: number
  coverage_pct: number
  elapsed_ms: number
  error_message?: string
}

export interface ProviderRun {
  id: number
  batch_id: string
  provider: string
  status: string
  source_version: string
  sha256: string
  effective_date: string
  record_count: number
  expected_count: number
  coverage_pct: number
  validation_error_pct: number
  timely: boolean
  gold_provider: string
  gold_source_url: string
  gold_sha256: string
  gold_rows: number
  gold_error_pct: number
  error_message: string
  provider_attempts: ProviderAttempt[]
  fallback_used: boolean
  created_at: string
}

export interface ProviderHealth {
  provider: string
  status: string
  qualified_trading_days: number
  failure_streak: number
  last_trade_date: string
  window_json: string
  gold_evidence_ready: boolean
  gold_sha256: string
  updated_at: string
}

export interface ProviderHealthPage {
  items: ProviderHealth[]
}

export interface CalendarCoverage {
  year: number
  complete: boolean
}

export interface ProviderObservabilityItem {
  provider: string
  configured: boolean
  configured_credential: boolean
  token_count: number
  local_request_budget: number
  budget_scope: string
  latest_source_record_count: number
	recent_attempt_count: number
	recent_usable_count: number
	recent_complete_count: number
	usable_rate_pct: number
	complete_rate_pct: number
	last_attempt_at?: string | null
	last_usable_at?: string | null
	freshness_status: string
	freshness_trading_days?: number | null
  health?: ProviderHealth | null
  latest_attempt?: ProviderAttempt | null
}

export interface ProviderObservability {
  generated_at: string
  price_provider_chain: string
  calendar_version: string
  calendar_years: CalendarCoverage[]
  latest_run?: ProviderRun | null
  chain_health?: ProviderHealth | null
  latest_price_source_counts: Record<string, number>
  providers: ProviderObservabilityItem[]
  budget_notice: string
}

export interface CandidateReport {
	status: string
	available: boolean
	message?: string
  date: string
  batch: {
    batch_id: string
    kind: string
    status: string
    effective_date: string
    record_count: number
    started_at: string
    completed_at?: string | null
    error_message: string
  }
  summary: CandidateSummary
  health: CandidateHealth
	snapshot_id?: number
	schema_version?: string
	content_sha256?: string
  generated_at: string
}

export interface CandidateEffectivenessWindow {
  horizon_days: number
  sample_count: number
  pending_count: number
  benchmark_sample_count: number
  distinct_signal_dates: number
  verification_status: 'unverified' | 'validating' | 'validated' | string
  average_return_pct?: number | null
  win_rate_pct?: number | null
  max_drawdown_pct?: number | null
  benchmark_return_pct?: number | null
  excess_return_pct?: number | null
  median_return_pct?: number | null
  p25_return_pct?: number | null
  p75_return_pct?: number | null
  net_average_return_pct?: number | null
  confidence_low_pct?: number | null
  confidence_high_pct?: number | null
}

export interface CandidateEffectivenessCohort {
  grade: string
  candidate_count: number
  windows: CandidateEffectivenessWindow[]
}

export interface CandidateEffectivenessReport {
  generated_at: string
  status: 'unverified' | 'validating' | 'validated' | string
  status_detail: string
  scoring_version: string
  minimum_sample_count: number
  benchmark_ticker: string
  benchmark_available: boolean
  benchmark_history_status: 'ready' | 'missing' | 'insufficient' | 'stale' | string
  benchmark_history_sample_days: number
  benchmark_history_required_days: number
  benchmark_latest_trade_date: string
  distinct_signal_dates: number
  minimum_distinct_signal_dates: number
  validation_horizon_days: number
  validation_sample_count: number
  validation_benchmark_count: number
  validation_signal_dates: number
  remaining_sample_count: number
  remaining_benchmark_count: number
  remaining_signal_dates: number
  cohort_source: 'signal_events' | 'legacy_first_entry' | string
  outcome_tracking_status: 'not_started' | 'tracking' | 'current' | string
  tracked_outcome_count: number
  mature_outcome_count: number
  pending_outcome_count: number
  benchmark_missing_outcome_count: number
  outcome_last_evaluated_at?: string | null
  assumed_round_trip_cost_pct: number
  cohorts: CandidateEffectivenessCohort[]
  segments: CandidateEffectivenessSegment[]
}

export interface CandidateEffectivenessSegment {
  dimension: string
  bucket: string
  candidate_count: number
  window_20: CandidateEffectivenessWindow
}

export interface CandidateEffectivenessReplayResult {
  scoring_version: string
  confirm: boolean
  batch_count: number
  eligible_batches: number
  signal_count: number
  inserted_count: number
  skipped: Record<string, number>
}

export interface TechnicalHistoryRetryState {
  ticker: string
  batch_id: string
  status: 'backoff' | 'deferred' | 'manual_review' | 'resolved' | string
  reason: string
  failure_count: number
  sample_days: number
  required_days: number
  latest_trade_date: string
  last_attempt_at: string
  next_retry_at?: string | null
}

export interface TechnicalHistoryRecoveryQueue { items: TechnicalHistoryRetryState[] }

export interface TradePlanSimulation {
  id: number
  ticker: string
  rule_version: string
  signal_date: string
  entry_date?: string | null
  entry_trigger: string
  entry_price_source: string
  entry_price_usd: number
  stop_loss_usd: number
  take_profit_usd: number
  initial_risk_pct: number
  status: string
  exit_date?: string | null
  exit_price_usd: number
  exit_reason: string
  last_mark_price_usd: number
  gross_return_pct: number
  execution_cost_pct: number
  return_pct: number
  r_multiple: number
  max_drawdown_pct: number
  holding_days: number
  created_at: string
  updated_at: string
}

export interface TradePlanSimulationReport {
  generated_at: string
  rule_version: string
  execution_convention: string
  total_count: number
  closed_count: number
  open_count: number
  win_rate_pct?: number | null
  average_return_pct?: number | null
  average_r_multiple?: number | null
  max_drawdown_pct?: number | null
  status_counts: Record<string, number>
  items: TradePlanSimulation[]
}

export interface TradePlanSimulationRebuildResult extends TradePlanSimulationReport {
  created_count: number
  updated_count: number
  skipped_count: number
}

export interface SystemConfig {
  id: number
  config_key: string
  config_value: string
  value_type: string
  category: string
  encrypted: boolean
}

export interface TaskConfig {
  id: number
  task_name: string
  cron_expr: string
  enabled: boolean
  last_run_at?: string | null
  next_run_at?: string | null
  running: boolean
  running_since?: string | null
  last_status: string
  last_error_message: string
  consecutive_failures: number
}

export interface TaskExecution {
  id: number
  task_name: string
  trigger: 'scheduled' | 'manual' | string
  status: 'running' | 'success' | 'partial' | 'skipped' | 'failed' | 'interrupted' | string
  started_at: string
  finished_at?: string | null
  duration_ms: number
  summary: string
  error_message: string
}

export interface OperationLog {
  id: number
  operated_at: string
  operator: string
  action: string
  object_type: string
  object_id: string
  before_data?: string
  after_data?: string
}

export interface NotificationLog {
  id: number
  filing_id: string
  channel: string
  target: string
  status: string
  retry_count: number
  error_message?: string
  sent_at?: string | null
  created_at: string
}

export interface NotificationBatch {
  id: number
  sync_run_id: number
  source: string
  trigger: string
  channel: string
  target: string
  status: string
  item_count: number
  sent_count: number
  suppressed_count: number
  failed_count: number
  retry_count: number
  suppression_summary?: string
  message_text?: string
  error_message?: string
  sent_at?: string | null
  next_retry_at?: string | null
  last_attempt_at?: string | null
  created_at: string
}

export interface NotificationBatchItem {
  id: number
  batch_id: number
  entity_kind: string
  filing_id: string
  ticker?: string
  cik?: string
  company_name: string
  filing_type: string
  title: string
  filing_url: string
  event_at: string
  status: string
  reason: string
}

export interface SyncRun {
  id: number
  started_at: string
  finished_at?: string | null
  status: string
  trigger: string
  targets_checked: number
  new_filings: number
  failed_targets: number
  deferred_targets: number
  error_message?: string
  warning_message?: string
  created_at: string
  updated_at: string
}

export interface SyncRunDetail {
  id: number
  sync_run_id: number
  target_id: number
  ticker: string
  status: string
  new_filings: number
  started_at: string
  finished_at?: string | null
  duration_ms: number
  error_message?: string
  warning_message?: string
  failure_kind?: string
  retryable: boolean
  attempt_count: number
  next_retry_at?: string | null
  created_at: string
  updated_at: string
}

export interface CleanupPreview {
  retention_days: number
  cutoff: string
  delete_count: number
  oldest_pulled_at?: string | null
  newest_pulled_at?: string | null
}

export interface SystemHealthIssue {
  level: string
  message: string
}

export interface SQLiteBackupHealth {
  directory: string
  complete_pairs: number
  incomplete_pairs: number
  total_bytes: number
  latest_pair_bytes: number
  latest_completed?: string | null
}

export interface SQLiteBackupVerification {
	 directory: string
	 files: Record<string, string>
	 verified_at: string
}

export interface SQLiteRecoveryReadiness {
  status: 'ready' | 'unavailable' | 'failed'
  checked_at: string
  backup: SQLiteBackupHealth
  verification?: SQLiteBackupVerification | null
  reason?: string
}

export interface SQLiteCompactionRun {
  id: number
  status: string
  started_at: string
  completed_at?: string | null
  duration_ms: number
  main_before_bytes: number
  main_after_bytes: number
  discovery_before_bytes: number
  discovery_after_bytes: number
  error_message?: string
}

export interface SQLiteCompactionDatabaseResult {
  name: string
  path: string
  before_bytes: number
  after_bytes: number
  reclaimed_bytes: number
}

export interface SQLiteCompactionResult {
  run_id: number
  status: string
  started_at: string
  completed_at: string
  duration_ms: number
  databases: SQLiteCompactionDatabaseResult[]
  reclaimed_bytes: number
  error_message?: string
}

export interface RecoveryDrill {
  id: number
  status: string
  backup_timestamp?: string | null
  started_at: string
  completed_at?: string | null
  duration_ms: number
  error_message?: string
}

export interface LifecycleCleanupPreview {
  retention_days: number
  cutoff: string
  sync_runs: number
  sync_run_details: number
	 task_executions: number
  notification_batches: number
  notification_batch_items: number
  operational_alert_deliveries: number
	 recovery_drills: number
  lifecycle_cleanup_runs: number
  discovery_sync_runs: number
  discovery_sync_steps: number
  superseded_market_repairs: number
  market_repair_universe_rows: number
  market_repair_score_rows: number
  total: number
  deleted_at?: string
}

export interface StorageHealth {
  path: string
  used_bytes: number
  total_bytes: number
  used_pct: number
}

export interface DataSourceHealth {
  source: string
  kind: 'sec' | 'market' | string
  status: 'ok' | 'info' | 'warning' | 'critical' | 'unknown' | string
  last_checked_at?: string | null
  failure_streak: number
  coverage_pct?: number | null
  detail?: string
  error_message?: string
  recommended_action?: 'scheduler' | 'configs' | 'discovery_logs' | string
}

export interface SystemHealth {
  status: string
  issues: SystemHealthIssue[]
  target_total: number
  enabled_targets: number
  filing_total: number
  notification_failures: number
  telegram_enabled: boolean
  sec_user_agent: string
  database_type: string
  database_path: string
  database_size_bytes: number
  latest_sync?: SyncRun
  backup?: SQLiteBackupHealth
	 recovery_drill?: RecoveryDrill
  storage?: StorageHealth
  data_sources?: DataSourceHealth[]
}

export interface OperationalIssue {
  key: string
  category: string
  severity: 'warning' | 'critical' | string
  title: string
  detail: string
  action?: 'scheduler' | 'sync-runs' | 'discovery-logs' | 'notification-logs' | string
  observed_at: string
}

export interface OperationalTaskStatus {
  task_name: string
  enabled: boolean
  running: boolean
  last_status: string
  last_run_at?: string | null
  next_run_at?: string | null
  running_since?: string | null
  consecutive_failures: number
  expected_within_mins: number
}

export interface OperationalReport {
  generated_at: string
  status: 'ok' | 'warning' | 'critical' | string
  issues: OperationalIssue[]
  tasks: OperationalTaskStatus[]
  retryable_targets: number
  deferred_targets: number
  company_profile_retry_due: number
  market_price_recovery: number
  low_coverage_providers: number
	 slow_sec_targets: number
	 slow_discovery_steps: number
  provider_warnings: number
	open_data_quality_incidents: number
	technical_history_pending: number
	technical_history_retry_due: number
	technical_history_deferred: number
  failed_notification_batches: number
  dead_letter_batches: number
  summary: string
}

export interface OperationalAlertResult {
  report: OperationalReport
  sent: boolean
  suppressed: boolean
  reason?: string
}

export interface MacroObservation {
  id: number
  release_id: number
  indicator_code: string
  indicator_name: string
  frequency: string
  unit: string
  actual_value?: number | null
  previous_value?: number | null
	forecast_value?: number | null
  previous_revised: boolean
  source_field: string
  source_url: string
  provider_updated_at?: string | null
  fetched_at: string
}

export interface MacroRelease {
  id: number
  provider: string
  category: string
	canonical_event_key?: string
  title: string
  reference_period: string
  release_stage: string
  status: 'scheduled' | 'published' | string
  scheduled_at?: string | null
  published_at?: string | null
  source_url: string
  fetched_at: string
  last_error?: string
	market_importance?: number
  observations: MacroObservation[]
	related_sources: MacroReleaseSource[]
}

export interface MacroReleaseSource {
  provider: string
  category: string
  title: string
  status: 'scheduled' | 'published' | string
  scheduled_at?: string | null
  published_at?: string | null
  source_url: string
  official: boolean
}

export interface MarketTrendPoint {
  date: string
  close: number
}

export interface MarketTrendSeries {
  symbol: string
  label: string
  trade_date: string
  open: number
  high: number
  low: number
  close: number
  volume: number
  change_1d_pct?: number | null
  change_5d_pct?: number | null
  change_20d_pct?: number | null
  history: MarketTrendPoint[]
}

export interface MarketTrendResponse {
  source: string
  last_fetched_at?: string | null
  market: MarketTrendSeries[]
  sectors: MarketTrendSeries[]
	temperature?: MarketTemperature | null
}

export interface MarketTemperaturePoint {
  date: string
  temperature: number
  valuation: number
  sentiment: number
}

export interface MarketTemperature {
  market: string
  trade_date: string
  temperature: number
  valuation: number
  sentiment: number
  description?: string
  history: MarketTemperaturePoint[]
}

export interface MarketTrendRefreshResult {
  symbols_requested: number
  symbols_updated: number
  bars_saved: number
	temperature_saved: number
  warnings: string[]
}

export interface USFuturesResponse {
  source: string
  last_fetched_at?: string | null
  futures: MarketTrendSeries[]
}

export interface USFuturesRefreshResult {
  symbols_requested: number
  symbols_updated: number
  bars_saved: number
  warnings: string[]
}

export interface AIAnalysisEvidence {
  fact: string
  inference: string
  impact: string
  source_paths: string[]
}

export interface AIAnalysisStructuredResult {
  schema_version: string
  stance: 'focus' | 'watch' | 'avoid' | 'insufficient_evidence' | string
  conclusion: string
  evidence: AIAnalysisEvidence[]
  counter_evidence: AIAnalysisEvidence[]
  invalidation_conditions: string[]
  catalysts: string[]
  data_gaps: string[]
  risk_notes: string[]
  evidence_sufficiency: 'high' | 'medium' | 'low' | string
}
