import { createRouter, createWebHistory } from 'vue-router'

import AppLayout from '@/layouts/AppLayout.vue'

// Research pages have grown substantially. Keep the shared layout eager but
// load each route only when it is opened, so a normal dashboard visit does not
// download tables, dialogs, and chart code for every module.
const DashboardView = () => import('@/views/DashboardView.vue')
const TargetsView = () => import('@/views/TargetsView.vue')
const FilingsView = () => import('@/views/FilingsView.vue')
const EventRadarView = () => import('@/views/EventRadarView.vue')
const DiscoveryCandidatesView = () => import('@/views/DiscoveryCandidatesView.vue')
const DiscoveryLogsView = () => import('@/views/DiscoveryLogsView.vue')
const IPORadarView = () => import('@/views/IPORadarView.vue')
const InsiderTradingView = () => import('@/views/InsiderTradingView.vue')
const SyncRunsView = () => import('@/views/SyncRunsView.vue')
const SchedulerView = () => import('@/views/SchedulerView.vue')
const TelegramView = () => import('@/views/TelegramView.vue')
const ConfigsView = () => import('@/views/ConfigsView.vue')
const SystemHealthView = () => import('@/views/SystemHealthView.vue')
const AuditLogsView = () => import('@/views/AuditLogsView.vue')
const NotificationLogsView = () => import('@/views/NotificationLogsView.vue')
const MacroCalendarView = () => import('@/views/MacroCalendarView.vue')
const MarketTrendView = () => import('@/views/MarketTrendView.vue')
const USFuturesView = () => import('@/views/USFuturesView.vue')
const SectorBreadthView = () => import('@/views/SectorBreadthView.vue')
const InstitutionalHoldingsView = () => import('@/views/InstitutionalHoldingsView.vue')
const StrategyPoolView = () => import('@/views/StrategyPoolView.vue')
const TickerEvaluationView = () => import('@/views/TickerEvaluationView.vue')
const OptionResearchView = () => import('@/views/OptionResearchView.vue')

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: AppLayout,
      children: [
        { path: '', name: 'dashboard', component: DashboardView },
        { path: 'targets', name: 'targets', component: TargetsView },
        { path: 'filings', name: 'filings', component: FilingsView },
        { path: 'discovery-candidates', name: 'discovery-candidates', component: DiscoveryCandidatesView },
        { path: 'strategy-pool', name: 'strategy-pool', component: StrategyPoolView },
		{ path: 'ticker-evaluation', name: 'ticker-evaluation', component: TickerEvaluationView },
		{ path: 'option-research', name: 'option-research', component: OptionResearchView },
        { path: 'discovery-logs', name: 'discovery-logs', component: DiscoveryLogsView },
        { path: 'macro-calendar', name: 'macro-calendar', component: MacroCalendarView },
		{ path: 'market-trend', name: 'market-trend', component: MarketTrendView },
		{ path: 'us-futures', name: 'us-futures', component: USFuturesView },
		{ path: 'sector-breadth', name: 'sector-breadth', component: SectorBreadthView },
        { path: 'institutional-holdings', name: 'institutional-holdings', component: InstitutionalHoldingsView },
        { path: 'event-radar', name: 'event-radar', component: EventRadarView },
        { path: 'insider-trading', name: 'insider-trading', component: InsiderTradingView },
        { path: 'ipo-radar', name: 'ipo-radar', component: IPORadarView },
        { path: 'sync-runs', name: 'sync-runs', component: SyncRunsView },
        { path: 'scheduler', name: 'scheduler', component: SchedulerView },
        { path: 'telegram', name: 'telegram', component: TelegramView },
        { path: 'configs', name: 'configs', component: ConfigsView },
        { path: 'system-health', name: 'system-health', component: SystemHealthView },
        { path: 'audit-logs', name: 'audit-logs', component: AuditLogsView },
        { path: 'notification-logs', name: 'notification-logs', component: NotificationLogsView }
      ]
    }
  ]
})

export default router
