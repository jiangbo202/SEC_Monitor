<template>
  <el-container class="app-shell">
    <el-aside width="216px" class="sidebar">
      <div class="brand">
        <el-icon><Monitor /></el-icon>
        <span>{{ t('app.title') }}</span>
      </div>
      <el-menu router :default-active="$route.path">
        <div class="nav-section-label">{{ t('nav.monitor') }}</div>
        <el-menu-item index="/"><el-icon><DataBoard /></el-icon><span>{{ t('nav.dashboard') }}</span></el-menu-item>
        <el-menu-item index="/strategy-pool"><el-icon><Compass /></el-icon><span>{{ t('nav.strategyPool') }}</span></el-menu-item>
		<el-menu-item index="/ticker-workspace"><el-icon><Search /></el-icon><span>{{ t('nav.tickerWorkspace') }}</span></el-menu-item>
		<el-menu-item index="/ticker-evaluation"><el-icon><MagicStick /></el-icon><span>{{ t('nav.tickerEvaluation') }}</span></el-menu-item>
		<el-menu-item index="/option-research"><el-icon><DataAnalysis /></el-icon><span>{{ t('nav.optionResearch') }}</span></el-menu-item>
        <el-menu-item index="/ai-analyses"><el-icon><MagicStick /></el-icon><span>{{ t('nav.aiAnalyses') }}</span></el-menu-item>
        <el-menu-item index="/targets"><el-icon><Aim /></el-icon><span>{{ t('nav.targets') }}</span></el-menu-item>

        <div class="nav-section-label">{{ t('nav.filingResearch') }}</div>
        <el-menu-item index="/filings"><el-icon><Document /></el-icon><span>{{ t('nav.filings') }}</span></el-menu-item>
        <el-menu-item index="/event-radar"><el-icon><Warning /></el-icon><span>{{ t('nav.eventRadar') }}</span></el-menu-item>
        <el-menu-item index="/insider-trading"><el-icon><UserFilled /></el-icon><span>{{ t('nav.insiderTrading') }}</span></el-menu-item>
        <el-menu-item index="/ipo-radar"><el-icon><TrendCharts /></el-icon><span>{{ t('nav.ipoRadar') }}</span></el-menu-item>

        <div class="nav-section-label">{{ t('nav.smallCapResearch') }}</div>
        <el-menu-item index="/discovery-candidates"><el-icon><Coin /></el-icon><span>{{ t('nav.discoveryCandidates') }}</span></el-menu-item>
        <el-menu-item index="/discovery-logs"><el-icon><DocumentCopy /></el-icon><span>{{ t('nav.discoveryLogs') }}</span></el-menu-item>

        <div class="nav-section-label">{{ t('nav.macroResearch') }}</div>
        <el-menu-item index="/market-trend"><el-icon><DataLine /></el-icon><span>{{ t('nav.marketTrend') }}</span></el-menu-item>
        <el-menu-item index="/sector-breadth"><el-icon><Histogram /></el-icon><span>{{ t('nav.sectorBreadth') }}</span></el-menu-item>
        <el-menu-item index="/us-futures"><el-icon><Odometer /></el-icon><span>{{ t('nav.usFutures') }}</span></el-menu-item>
        <el-menu-item index="/institutional-holdings"><el-icon><Briefcase /></el-icon><span>{{ t('nav.institutionalHoldings') }}</span></el-menu-item>
        <el-menu-item index="/macro-calendar"><el-icon><Calendar /></el-icon><span>{{ t('nav.macroCalendar') }}</span></el-menu-item>

        <div class="nav-section-label">{{ t('nav.automation') }}</div>
        <el-menu-item index="/sync-runs"><el-icon><Collection /></el-icon><span>{{ t('nav.syncRuns') }}</span></el-menu-item>
        <el-menu-item index="/scheduler"><el-icon><Timer /></el-icon><span>{{ t('nav.scheduler') }}</span></el-menu-item>
        <el-menu-item index="/system-health"><el-icon><FirstAidKit /></el-icon><span>{{ t('nav.systemHealth') }}</span></el-menu-item>
        <el-menu-item index="/notification-logs"><el-icon><Notification /></el-icon><span>{{ t('nav.notificationLogs') }}</span></el-menu-item>
        <el-menu-item index="/audit-logs"><el-icon><Tickets /></el-icon><span>{{ t('nav.auditLogs') }}</span></el-menu-item>
        <el-menu-item index="/configs"><el-icon><Setting /></el-icon><span>{{ t('nav.configs') }}</span></el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header height="48px" class="topbar">
        <span>{{ t('app.topbar') }}</span>
        <div class="topbar-actions">
          <el-badge :value="unreadCount" :hidden="unreadCount === 0" :max="99" class="in-app-bell">
            <el-button circle :aria-label="'站内消息'" @click="openInbox"><el-icon><Bell /></el-icon></el-button>
          </el-badge>
        <div class="language-switch" :aria-label="t('app.language')">
          <el-button size="small" :type="store.locale === 'zh-CN' ? 'primary' : 'default'" @click="store.setLocale('zh-CN')">中文</el-button>
          <el-button size="small" :type="store.locale === 'en-US' ? 'primary' : 'default'" @click="store.setLocale('en-US')">EN</el-button>
        </div>
        </div>
      </el-header>
      <el-main>
        <RouterView />
      </el-main>
    </el-container>
  </el-container>
  <el-drawer v-model="inboxOpen" title="站内消息" direction="rtl" size="440px" :with-header="false" @open="loadInbox">
    <div class="inbox-header">
      <div>
        <strong>站内消息</strong>
        <span>{{ unreadCount > 0 ? `${unreadCount} 条未读` : '全部已读' }}</span>
      </div>
      <div>
        <el-button link @click="showUnreadOnly = !showUnreadOnly; loadInbox()">{{ showUnreadOnly ? '查看全部' : '仅看未读' }}</el-button>
        <el-button link type="primary" :disabled="unreadCount === 0" :loading="markingAllRead" @click="markAllInboxRead">一键全读</el-button>
        <el-button link :loading="inboxLoading" @click="loadInbox">刷新</el-button>
      </div>
    </div>
    <el-empty v-if="!inboxLoading && inboxItems.length === 0" description="暂无站内消息" />
    <div v-else v-loading="inboxLoading" class="inbox-list">
      <button v-for="item in inboxItems" :key="item.id" class="inbox-item" :class="[{ unread: !item.read_at }, `severity-${item.severity}`]" @click="openMessage(item)">
        <div class="inbox-item-head">
          <el-tag size="small" :type="sourceTagType(item.source)" effect="plain">{{ sourceLabel(item.source) }}</el-tag>
          <time :title="'通知生成时间'">{{ formatMessageTime(item.created_at) }}</time>
        </div>
        <strong>{{ item.ticker ? `${item.ticker}｜` : '' }}{{ item.title }}</strong>
        <p v-if="item.body">{{ item.body }}</p>
      </button>
    </div>
  </el-drawer>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Aim, Bell, Briefcase, Calendar, Coin, Collection, Compass, DataAnalysis, DataBoard, DataLine, Document, DocumentCopy, FirstAidKit, Histogram, MagicStick, Monitor, Notification, Odometer, Search, Setting, Tickets, Timer, TrendCharts, UserFilled, Warning } from '@element-plus/icons-vue'
import { apiClient } from '@/api/client'
import type { ApiResponse, InAppNotification, PageResult } from '@/api/types'
import { useI18n } from '@/i18n'

const { store, t } = useI18n()
const router = useRouter()
const inboxOpen = ref(false)
const inboxLoading = ref(false)
const markingAllRead = ref(false)
const showUnreadOnly = ref(false)
const unreadCount = ref(0)
const inboxItems = ref<InAppNotification[]>([])
let unreadTimer: number | undefined

async function loadUnreadCount() {
  const res = await apiClient.get<ApiResponse<{ unread_count: number }>>('/in-app-notifications/unread-count')
  unreadCount.value = res.data.data.unread_count || 0
}

async function loadInbox() {
  inboxLoading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<InAppNotification>>>('/in-app-notifications', { params: { page: 1, page_size: 50, unread_only: showUnreadOnly.value } })
    inboxItems.value = res.data.data.items
    await loadUnreadCount()
  } finally {
    inboxLoading.value = false
  }
}

async function openInbox() {
  inboxOpen.value = true
  await loadInbox()
}

async function markAllInboxRead() {
  if (unreadCount.value === 0) return
  markingAllRead.value = true
  try {
    await apiClient.patch('/in-app-notifications/read-all')
    const readAt = new Date().toISOString()
    for (const item of inboxItems.value) {
      if (!item.read_at) item.read_at = readAt
    }
    unreadCount.value = 0
    if (showUnreadOnly.value) inboxItems.value = []
  } finally {
    markingAllRead.value = false
  }
}

async function openMessage(item: InAppNotification) {
  if (!item.read_at) {
    await apiClient.patch(`/in-app-notifications/${item.id}/read`)
    item.read_at = new Date().toISOString()
    unreadCount.value = Math.max(0, unreadCount.value - 1)
  }
  const target = item.link || (item.ticker ? `/ticker-workspace?ticker=${encodeURIComponent(item.ticker)}` : '')
  if (target) {
    inboxOpen.value = false
    await router.push(target)
  }
}

function sourceLabel(source: string) {
  return ({ earnings_preview: '财报预告', earnings_preview_watch_target: '监控标的 · 财报预告', earnings_preview_candidate: '小盘候选 · 财报预告', earnings_release: '财报发布', earnings_release_watch_target: '监控标的 · 财报发布', earnings_release_candidate: '小盘候选 · 财报发布', technical_signal: '技术信号', technical_signal_watch_target: '监控标的 · 技术信号', technical_signal_candidate: '小盘候选 · 技术信号', major_event: '重大事件', major_event_watch_target: '监控标的 · 重大事件', insider_trading: '内幕交易', insider_trading_watch_target: '监控标的 · 内幕交易', ipo_progress: '关注 IPO 进展', ai_analysis: 'AI 研判' } as Record<string, string>)[source] || source
}

function sourceTagType(source: string) {
  return ({ earnings_preview: 'info', earnings_preview_watch_target: 'info', earnings_preview_candidate: 'info', earnings_release: 'success', earnings_release_watch_target: 'success', earnings_release_candidate: 'success', technical_signal: 'warning', technical_signal_watch_target: 'warning', technical_signal_candidate: 'warning', major_event: 'danger', major_event_watch_target: 'danger', insider_trading: 'warning', insider_trading_watch_target: 'warning', ipo_progress: 'primary', ai_analysis: 'primary' } as Record<string, 'info' | 'primary' | 'success' | 'warning' | 'danger'>)[source] || 'info'
}

function formatMessageTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

onMounted(() => {
  void loadUnreadCount()
  unreadTimer = window.setInterval(() => void loadUnreadCount(), 60_000)
})

onBeforeUnmount(() => {
  if (unreadTimer) window.clearInterval(unreadTimer)
})
</script>
