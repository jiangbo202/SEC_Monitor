import type { TaskConfig } from './types'
export interface APIProviderPolicy {provider:string;paused:boolean;daily_budget:number;daily_used:number;min_interval_ms:number;created_at:string;budget_date:string}
export interface APIProviderSummary {policy:APIProviderPolicy;credential_configured:boolean;authorization:string;requests:number;failures:number;rate_limited:number;pending:number;average_ms:number;last_success_at?:string;vendor_quota:number|null}
export interface APICapability {key:string;label:string;provider:string;auto_enabled:boolean;issuer_budget:number;ttl_hours:number;metric_scope:string;config_key?:string;implemented:boolean;effective_status?:string;schedules?:APICapabilitySchedule[]}
export interface APICall {id:number;provider:string;endpoint:string;ticker:string;trigger:string;status:string;error_kind?:string;elapsed_ms:number;started_at:string}
export interface APICoverageCell {ticker:string;capability:string;status:string;synced_at?:string;checked_at?:string;snapshot_at?:string;ttl_hours?:number}
export interface APITrend {provider:string;day:string;requests:number;failures:number;rate_limited:number}
export interface APIManagementOverview {generated_at:string;window_start:string;time_zone:string;providers:APIProviderSummary[];capabilities:APICapability[];trends:APITrend[];calls:APICall[];coverage:APICoverageCell[];tasks:TaskConfig[];notice:string;modules:APIModuleView[];price_route:APIPriceRoute}

export interface APIInterfaceView {key:string;label:string;endpoints:string[];purpose:string;required:boolean;depends_on:string[];enabled:boolean;last_status:string;last_called_at?:string}
export interface APIModuleView {key:string;label:string;provider:string;pages:string[];flow:string[];auto_capabilities:string[];task_names:string[];interfaces:APIInterfaceView[];note:string;enabled:boolean;revision:number;status:string;warnings:string[];source_options?:{provider:string;implemented:boolean;reason:string;interfaces:APIInterfaceView[]}[]}
export interface APIPriceSource {key:string;ready:boolean;reason:string}
export interface APIPriceRoute {configured:string;order:string[];editable:boolean;sources:APIPriceSource[];scope:string}

export interface APICapabilitySchedule {task_name:string;enabled:boolean;next_run_at?:string;last_status:string;universe_size:number;receipt_missing:number;receipt_stale:number;minimum_rounds:number;earliest_full_rotation_at?:string;freshness_feasible?:boolean;research_ttl_hours:number;note:string}
