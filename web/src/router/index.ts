import { createRouter, createWebHistory } from 'vue-router'

import AppLayout from '@/layouts/AppLayout.vue'
import DashboardView from '@/views/DashboardView.vue'
import TargetsView from '@/views/TargetsView.vue'
import FilingsView from '@/views/FilingsView.vue'
import EventRadarView from '@/views/EventRadarView.vue'
import IPORadarView from '@/views/IPORadarView.vue'
import InsiderTradingView from '@/views/InsiderTradingView.vue'
import SyncRunsView from '@/views/SyncRunsView.vue'
import SchedulerView from '@/views/SchedulerView.vue'
import TelegramView from '@/views/TelegramView.vue'
import ConfigsView from '@/views/ConfigsView.vue'
import SystemHealthView from '@/views/SystemHealthView.vue'
import AuditLogsView from '@/views/AuditLogsView.vue'
import NotificationLogsView from '@/views/NotificationLogsView.vue'

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
        { path: 'event-radar', name: 'event-radar', component: EventRadarView },
        { path: 'ipo-radar', name: 'ipo-radar', component: IPORadarView },
        { path: 'insider-trading', name: 'insider-trading', component: InsiderTradingView },
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
