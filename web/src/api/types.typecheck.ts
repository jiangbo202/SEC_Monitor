import type {
  CandidateDetail,
  CandidateHealth,
  CandidateNotificationPreview,
  CandidateNotificationSendInput,
  CandidateNotificationSendResult,
  CandidateReport,
  CandidateScore,
  CandidateSummary,
  DiscoveryWorkflowResult,
} from './types'

// Compile-time contract guard for discovery candidate summary API payloads.
const candidate: CandidateScore = {
  id: 1,
  batch_id: 'batch',
  security_id: 1,
  ticker: 'ACME',
  market_cap_usd: 240_000_000,
  grade: 'A',
  eligible_a: true,
  eligible_b: true,
  total_score: 88,
  revenue_growth_score: 30,
  cash_runway_score: 20,
  insider_score: 20,
  gross_margin_score: 8,
  dilution_risk_score: 10,
  sector_score: 8,
  revenue_growth_pct: 55,
  cash_runway_months: 18,
  recent_qualified_insider: true,
  active_blocks_a: false,
  active_blocks_b: false,
  reason_code: 'qualified',
  scoring_version: 'v1',
  created_at: '2026-06-29T00:00:00Z',
}

export const candidateSummaryTypecheck: CandidateSummary = {
  batch_id: 'batch',
  total_a: 1,
  total_b: 0,
  items_a: [candidate],
  items_b: [],
  message: '小盘股研究候选摘要',
}

export const candidateNotificationPreviewTypecheck: CandidateNotificationPreview = {
  enabled: true,
  suppressed_reason: '',
  settings: {
    enabled: true,
    notify_a: true,
    notify_b: false,
    send_time: '09:30',
    max_per_grade: 5,
  },
  summary: candidateSummaryTypecheck,
}

export const candidateNotificationSendResultTypecheck: CandidateNotificationSendResult = {
  preview: candidateNotificationPreviewTypecheck,
  batch: {
    id: 1,
    sync_run_id: 0,
    source: 'candidate',
    trigger: 'manual',
    channel: 'telegram',
    target: 'chat',
    status: 'sent',
    item_count: 1,
    sent_count: 1,
    suppressed_count: 0,
    failed_count: 0,
    retry_count: 0,
    created_at: '2026-06-30T00:00:00Z',
  },
}

export const candidateNotificationSendInputTypecheck: CandidateNotificationSendInput = {
  confirm: true,
  force: false,
}

export const candidateDetailTypecheck: CandidateDetail = {
  batch_id: 'batch',
  security: { id: 1, cik: '0001', company_name: 'Acme', sic: 1000, catalog_status: 'published' },
  score: candidate,
  insiders: [],
  capital_risks: [],
  sector: { sic: 1000, category: '未分类赛道', score: 8, label: '优秀赛道', rationale: '基于 SIC 1000。' },
  data_quality: { financial: 'valid' },
  evidence: [{ field: 'total_score', value: '88', source: 'candidate_score_snapshots' }],
  recent_filings: [],
}

export const candidateHealthTypecheck: CandidateHealth = {
  batch_id: 'batch',
  status: 'ok',
  total_candidates: 1,
  missing_financials: 0,
  missing_insiders: 0,
  missing_market_cap: 0,
  active_risk_events: 0,
  issues: [],
}

export const discoveryWorkflowTypecheck: DiscoveryWorkflowResult = {
  status: 'published',
  batch_id: 'batch',
  summary: candidateSummaryTypecheck,
  health: candidateHealthTypecheck,
}

export const candidateReportTypecheck: CandidateReport = {
  date: '2026-06-30',
  batch: {
    batch_id: 'batch',
    kind: 'market-prescreen',
    status: 'published',
    effective_date: '2026-06-30',
    record_count: 1,
    started_at: '2026-06-30T00:00:00Z',
    error_message: '',
  },
  summary: candidateSummaryTypecheck,
  health: candidateHealthTypecheck,
  generated_at: '2026-06-30T00:00:00Z',
}
