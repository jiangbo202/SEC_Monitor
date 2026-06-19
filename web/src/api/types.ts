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

export interface TickerLookup {
  ticker: string
  cik: string
  company_name: string
  target_type: string
  group?: string
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
  override_note?: string
  override_updated_at?: string | null
}

export interface IPORadarRefreshResult {
  checked: number
  new_filings: number
  notified: number
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
