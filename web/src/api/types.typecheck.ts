import type { CandidateNotificationPreview, CandidateScore, CandidateSummary } from './types'

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
