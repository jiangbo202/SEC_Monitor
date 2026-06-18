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
