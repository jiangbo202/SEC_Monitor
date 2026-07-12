export interface ApiResponse<T> {
  code: number
  message: string
  data: T
}

export interface PageResult<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface WatchTarget {
  id: number
  ticker: string
  company_name: string
  cik: string
  target_type: string
  group?: string
  status: string
  last_sync_at?: string | null
  last_sync_status?: string
  last_sync_error?: string
  last_new_filings?: number
  created_at: string
  updated_at: string
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
  cik?: string
  company_name?: string
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

export interface IPORadarHealth {
  pending_listing: number
  missing_market_mapping: number
  stale_lifecycle_checks: number
  unsupported_offering_events: number
  failed_notification_batches: number
  due_retry_batches: number
  dead_letter_batches: number
  latest_sync?: IPORadarHealthSync | null
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
  created_at: string
  price_close_usd?: number
  price_volume?: number
  price_trade_date?: string | null
  price_currency?: string
  price_quality_status?: string
  price_source?: string
  quality_tier?: string
  quality_tags?: string[]
  quality_adjusted_score?: number
  review_priority_score?: number
  review_priority_reasons?: ReviewPriorityReason[]
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
}

export interface CandidateMarketQuality {
  sample_days: number
  average_dollar_volume_usd: number
  volatility_pct: number
  momentum_pct: number
  max_drawdown_pct: number
  status: string
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
  thesis: string
  risk_notes: string
  invalidation: string
  next_review_at?: string | null
  source_batch_id: string
  latest_score?: CandidateScore
  created_at: string
  updated_at: string
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
  catalog_status: string
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

export interface CandidateDetail {
  batch_id: string
  security: DiscoverySecurity
  score: CandidateScore
  financial?: DiscoveryFinancialMetric
  insiders: DiscoveryInsiderTransaction[]
  capital_risks: DiscoveryCapitalRisk[]
  sector: SectorExplanation
  data_quality: Record<string, string>
  evidence: DiscoveryEvidence[]
  recent_filings: RecentSECFiling[]
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
  qualified_insider_candidates: number
  no_qualified_insider_candidates: number
  missing_market_cap: number
  active_risk_events: number
  issues: string[]
}

export interface DiscoveryWorkflowResult {
  status: 'published' | 'market_failed' | string
  batch_id: string
  security_batch_id?: string
  market_batch_id?: string
  summary: CandidateSummary
  health: CandidateHealth
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

export interface CandidateReport {
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
  generated_at: string
}

export interface CandidateEffectivenessWindow {
  horizon_days: number
  sample_count: number
  average_return_pct?: number | null
  win_rate_pct?: number | null
  max_drawdown_pct?: number | null
  benchmark_return_pct?: number | null
  excess_return_pct?: number | null
}

export interface CandidateEffectivenessCohort {
  grade: string
  candidate_count: number
  windows: CandidateEffectivenessWindow[]
}

export interface CandidateEffectivenessReport {
  generated_at: string
  benchmark_ticker: string
  benchmark_available: boolean
  cohorts: CandidateEffectivenessCohort[]
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
}
