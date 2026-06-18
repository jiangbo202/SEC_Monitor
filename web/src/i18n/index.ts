import { defineStore } from 'pinia'

export type Locale = 'zh-CN' | 'en-US'

const LOCALE_KEY = 'sec-monitor-locale'

const messages = {
  'zh-CN': {
    app: {
      title: 'SEC Monitor',
      topbar: 'SEC 公告监控控制台',
      language: '语言'
    },
    nav: {
      monitor: '监控',
      dashboard: '总览',
      targets: '监控标的',
      filings: 'SEC 公告',
      automation: '自动化',
      syncRuns: '同步历史',
      scheduler: '调度任务',
      settings: '通知与配置',
      telegram: 'Telegram',
      configs: '系统配置',
      logs: '日志',
      auditLogs: '审计日志',
      notificationLogs: '通知日志'
    },
    common: {
      actions: '操作',
      add: '新增',
      cancel: '取消',
      close: '关闭',
      company: '公司',
      companyName: '公司名称',
      delete: '删除',
      disabled: '已停用',
      duration: '耗时',
      edit: '编辑',
      enabled: '已启用',
      error: '错误',
      filingDate: 'Filing Date',
      filings: '公告',
      history: '历史',
      link: '链接',
      manage: '管理',
      more: '更多',
      newCount: '新增',
      open: '打开',
      process: '处理',
      query: '查询',
      refresh: '刷新',
      refreshData: '刷新数据',
      refreshPanel: '刷新面板',
      save: '保存',
      search: '搜索',
      status: '状态',
      sync: '同步',
      syncTime: '同步时间',
      target: '标的',
      time: '时间',
      title: '标题',
      type: '类型',
      update: '更新',
      view: '查看',
      viewAll: '查看全部',
      details: '查看详情'
    },
    status: {
      success: '成功',
      failed: '失败',
      running: '运行中',
      pending: '等待中',
      enabled: '已启用',
      disabled: '已停用',
      unknown: '未知'
    },
    pages: {
      dashboard: {
        title: '总览',
        subtitle: 'SEC 监控状态、最近同步和最新公告',
        refreshFilings: '刷新公告',
        latestFilings: '最新 SEC 公告',
        syncStatus: '同步状态',
        targetHealth: '标的健康',
        enabledTargets: '启用标的',
        syncSuccess: '同步成功',
        syncFailed: '同步失败',
        activeTargets: '活跃标的',
        failedTargets: '失败标的',
        recentNotifications: '最近通知',
        notificationRate: '成功率 {rate}%',
        newFilings: '{count} 条新增公告',
        syncSummary: '检查 {targets} 个标的，失败 {failed} 个',
        startedAt: '开始：{time}',
        finishedAt: '结束：{time}',
        noSyncRuns: '暂无同步记录',
        noActiveTargets: '暂无活跃标的',
        noFailedTargets: '暂无失败标的',
        countSuffix: '{count} 条'
      },
      targets: {
        title: '监控标的',
        add: '新增标的',
        edit: '编辑标的',
        detail: '标的详情',
        empty: '暂无标的，点击右上角新增',
        lookup: '带出信息',
        enableShort: '开',
        disableShort: '关',
        syncTarget: '同步该标的',
        syncStatus: '同步状态',
        lastSync: '上次同步',
        syncError: '同步错误',
        recentNew: '最近新增',
        fetchPolicy: '拉取策略',
        recentSync: '最近同步',
        recentFilings: '最近公告',
        noSyncRuns: '暂无同步记录',
        noFilings: '暂无公告，尝试同步该标的'
      },
      filings: {
        title: 'SEC 公告',
        empty: '暂无公告，可刷新数据或调整筛选条件'
      },
      syncRuns: {
        title: '同步历史',
        empty: '暂无同步历史'
      },
      scheduler: {
        title: '调度任务',
        empty: '暂无调度任务'
      },
      configs: {
        title: '系统配置',
        save: '保存配置'
      },
      auditLogs: {
        title: '审计日志',
        empty: '暂无审计日志'
      },
      notificationLogs: {
        title: '通知日志',
        empty: '暂无通知日志'
      },
      telegram: {
        title: 'Telegram'
      }
    }
  },
  'en-US': {
    app: {
      title: 'SEC Monitor',
      topbar: 'SEC Filing Monitoring Console',
      language: 'Language'
    },
    nav: {
      monitor: 'Monitor',
      dashboard: 'Dashboard',
      targets: 'Watch Targets',
      filings: 'SEC Filings',
      automation: 'Automation',
      syncRuns: 'Sync History',
      scheduler: 'Scheduler',
      settings: 'Notifications & Settings',
      telegram: 'Telegram',
      configs: 'System Settings',
      logs: 'Logs',
      auditLogs: 'Audit Logs',
      notificationLogs: 'Notification Logs'
    },
    common: {
      actions: 'Actions',
      add: 'Add',
      cancel: 'Cancel',
      close: 'Close',
      company: 'Company',
      companyName: 'Company Name',
      delete: 'Delete',
      disabled: 'Disabled',
      duration: 'Duration',
      edit: 'Edit',
      enabled: 'Enabled',
      error: 'Error',
      filingDate: 'Filing Date',
      filings: 'Filings',
      history: 'History',
      link: 'Link',
      manage: 'Manage',
      more: 'More',
      newCount: 'New',
      open: 'Open',
      process: 'Process',
      query: 'Search',
      refresh: 'Refresh',
      refreshData: 'Refresh Data',
      refreshPanel: 'Refresh Panel',
      save: 'Save',
      search: 'Search',
      status: 'Status',
      sync: 'Sync',
      syncTime: 'Synced At',
      target: 'Target',
      time: 'Time',
      title: 'Title',
      type: 'Type',
      update: 'Updated',
      view: 'View',
      viewAll: 'View All',
      details: 'Details'
    },
    status: {
      success: 'Success',
      failed: 'Failed',
      running: 'Running',
      pending: 'Pending',
      enabled: 'Enabled',
      disabled: 'Disabled',
      unknown: 'Unknown'
    },
    pages: {
      dashboard: {
        title: 'Dashboard',
        subtitle: 'SEC monitor status, recent syncs, and latest filings',
        refreshFilings: 'Refresh Filings',
        latestFilings: 'Latest SEC Filings',
        syncStatus: 'Sync Status',
        targetHealth: 'Target Health',
        enabledTargets: 'Enabled Targets',
        syncSuccess: 'Sync Success',
        syncFailed: 'Sync Failed',
        activeTargets: 'Active Targets',
        failedTargets: 'Failed Targets',
        recentNotifications: 'Recent Notifications',
        notificationRate: 'Success Rate {rate}%',
        newFilings: '{count} New Filings',
        syncSummary: 'Checked {targets} targets, {failed} failed',
        startedAt: 'Started: {time}',
        finishedAt: 'Finished: {time}',
        noSyncRuns: 'No sync history',
        noActiveTargets: 'No active targets',
        noFailedTargets: 'No failed targets',
        countSuffix: '{count}'
      },
      targets: {
        title: 'Watch Targets',
        add: 'Add Target',
        edit: 'Edit Target',
        detail: 'Target Details',
        empty: 'No targets yet. Add one from the top right.',
        lookup: 'Lookup',
        enableShort: 'On',
        disableShort: 'Off',
        syncTarget: 'Sync Target',
        syncStatus: 'Sync Status',
        lastSync: 'Last Sync',
        syncError: 'Sync Error',
        recentNew: 'Recent New',
        fetchPolicy: 'Fetch Policy',
        recentSync: 'Recent Syncs',
        recentFilings: 'Recent Filings',
        noSyncRuns: 'No sync history',
        noFilings: 'No filings yet. Try syncing this target.'
      },
      filings: {
        title: 'SEC Filings',
        empty: 'No filings. Refresh data or adjust filters.'
      },
      syncRuns: {
        title: 'Sync History',
        empty: 'No sync history'
      },
      scheduler: {
        title: 'Scheduler',
        empty: 'No scheduled jobs'
      },
      configs: {
        title: 'System Settings',
        save: 'Save Settings'
      },
      auditLogs: {
        title: 'Audit Logs',
        empty: 'No audit logs'
      },
      notificationLogs: {
        title: 'Notification Logs',
        empty: 'No notification logs'
      },
      telegram: {
        title: 'Telegram'
      }
    }
  }
} as const

type MessageTree = Record<string, unknown>

function normalizeLocale(value: string | null): Locale {
  return value === 'en-US' ? 'en-US' : 'zh-CN'
}

function resolveMessage(tree: MessageTree, key: string): string {
  const value = key.split('.').reduce<unknown>((current, part) => {
    if (!current || typeof current === 'string') return undefined
    return (current as MessageTree)[part]
  }, tree)
  return typeof value === 'string' ? value : key
}

export const useI18nStore = defineStore('i18n', {
  state: () => ({
    locale: normalizeLocale(localStorage.getItem(LOCALE_KEY))
  }),
  actions: {
    setLocale(locale: Locale) {
      this.locale = locale
      localStorage.setItem(LOCALE_KEY, locale)
      document.documentElement.lang = locale
    }
  }
})

export function useI18n() {
  const store = useI18nStore()
  const t = (key: string, params: Record<string, string | number> = {}) => {
    let text = resolveMessage(messages[store.locale], key)
    Object.entries(params).forEach(([name, value]) => {
      text = text.replace(new RegExp(`\\{${name}\\}`, 'g'), String(value))
    })
    return text
  }
  return { store, t }
}
