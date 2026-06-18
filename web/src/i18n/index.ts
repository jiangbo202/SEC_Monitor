import { defineStore } from 'pinia'
import { apiClient } from '@/api/client'
import type { ApiResponse, SystemConfig } from '@/api/types'

export type Locale = 'zh-CN' | 'en-US'

const LOCALE_KEY = 'sec-monitor-locale'
const DEFAULT_LOCALE_CONFIG_KEY = 'ui.default_locale'

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
      confirm: '确认',
      delete: '删除',
      disabled: '已停用',
      duration: '耗时',
      edit: '编辑',
      enabled: '已启用',
      error: '错误',
      filingDate: 'Filing Date',
      filings: '公告',
      finishTime: '结束时间',
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
      retry: '重试',
      retryCount: '重试',
      save: '保存',
      search: '搜索',
      source: '来源',
      startTime: '开始时间',
      status: '状态',
      sync: '同步',
      syncTime: '同步时间',
      target: '标的',
      task: '任务',
      time: '时间',
      title: '标题',
      type: '类型',
      update: '更新',
      user: '用户',
      view: '查看',
      viewAll: '查看全部',
      details: '查看详情'
    },
    status: {
      success: '成功',
      failed: '失败',
      running: '运行中',
      pending: '等待中',
      partial: '部分成功',
      enabled: '已启用',
      disabled: '已停用',
      unknown: '未知',
      unnotified: '未通知'
    },
    messages: {
      saved: '已保存',
      configSaved: '配置已保存',
      newFilingsAdded: '新增 {count} 条公告',
      deletedFilings: '已删除 {count} 条公告',
      syncDone: '同步完成，新增 {count} 条',
      retryDone: '{ticker} 重试完成，新增 {count} 条',
      retryAllDone: '已重试 {targets} 个失败标的，新增 {count} 条',
      lookupDone: '已带出公司名称和 CIK',
      lookupFailed: '未能自动带出信息，请检查 Ticker 或手动填写',
      taskSaved: '调度配置已保存',
      taskTriggered: '任务已触发',
      telegramSaved: 'Telegram 配置已保存',
      telegramTestSent: '测试消息已发送',
      telegramTestFailed: '测试发送失败，请检查 Bot Token 和 Chat ID',
      confirmDeleteTarget: '删除 {ticker}?',
      confirmDeleteTitle: '确认删除',
      confirmCleanup: '确认删除 {count} 条过期公告？',
      cleanupTitle: '执行数据清理',
      offerSync: '是否现在同步 {ticker} 的 SEC 公告？',
      targetSavedTitle: '新增标的已保存',
      syncNow: '立即同步',
      later: '稍后'
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
        countSuffix: '{count} 条',
        noSyncErrorDetail: '同步失败，暂无错误详情',
        failedTargetsAlertTitle: '{count} 个标的同步失败',
        failedTargetsAlertDescription: '进入监控标的页查看错误，或对失败标的单独重试同步。',
        noSyncAlertTitle: '还没有同步记录',
        noSyncAlertDescription: '新增标的后可以手动刷新公告或等待调度执行。',
        staleSyncAlertTitle: '最近同步已超过 {hours} 小时',
        staleSyncAlertDescription: '建议检查调度任务是否启用，或手动刷新公告。',
        schedulerDisabledTitle: '调度任务未启用',
        schedulerDisabledDescription: '当前不会自动周期拉取 SEC 公告。',
        telegramDisabledTitle: 'Telegram 通知未启用',
        telegramDisabledDescription: '新公告会入库，但不会主动推送提醒。',
        healthyTitle: '系统运行正常',
        healthyDescription: '同步、调度和通知配置当前没有明显异常。'
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
        noFilings: '暂无公告，尝试同步该标的',
        policyEveryUnlimited: '每次不限制时间',
        policyEveryDays: '每次最近 {days} 天',
        policyInitialFull: '首次完整历史',
        policyInitialDays: '首次最近 {days} 天',
        policyMaxUnlimited: '不限制',
        policyMaxCount: '{count} 条',
        policySummary: '{syncWindow}，{initialWindow}，最多 {max}',
        syncIssueTitle: '{ticker} 最近同步失败',
        syncIssueCik: '建议检查 Ticker 是否正确，或手动补充 CIK 后重试。',
        syncIssueTimeout: '看起来像 SEC 请求超时，可以稍后重试或降低最大拉取条数。',
        syncIssueTelegram: '公告可能已入库，但通知失败；请检查 Telegram 配置。',
        syncIssueDefault: '可先重试该标的；如果继续失败，请查看同步历史中的错误明细。'
      },
      filings: {
        title: 'SEC 公告',
        empty: '暂无公告，可刷新数据或调整筛选条件',
        quickFilter: '快捷筛选',
        start: '开始',
        end: '结束',
        notification: '通知',
        publishedAt: '发布时间',
        typeTooltip: '查看 SEC Filing 类型说明',
        typePlaceholder: '选择或搜索类型',
        typeHelpTitle: 'SEC Filing 类型说明',
        typeName: '名称',
        typeDescription: '说明',
        important: '重点',
        watch: '关注',
        normal: '普通',
        filters: {
          recent7Days: '最近 7 天',
          majorEvents: '重大事件',
          annual10K: '年报 10-K',
          quarterly10Q: '季报 10-Q',
          insiderTrading: '内幕交易',
          financingS1: '融资 S-1'
        }
      },
      syncRuns: {
        title: '同步历史',
        empty: '暂无同步历史',
        retryCurrentFailures: '重试当前失败',
        viewTarget: '查看标的',
        triggers: {
          manual: '手动',
          scheduler: '调度',
          target: '单标的'
        }
      },
      scheduler: {
        title: '调度任务',
        empty: '暂无调度任务',
        commonFrequency: '常用频率',
        runNow: '立即执行',
        lastRun: '上次运行',
        cronInvalid: 'Cron 需要 5 段：分钟 小时 日期 月份 星期',
        cronHourlyMinute: '每小时第 {minute} 分钟执行',
        cronEveryMinutes: '每 {minutes} 分钟执行',
        cronCustom: '自定义 Cron 表达式',
        presets: {
          every5: '每 5 分钟',
          every30: '每 30 分钟',
          hourly: '每小时',
          daily9: '每天 09:00'
        }
      },
      configs: {
        title: '系统配置',
        save: '保存配置',
        secPolicy: 'SEC 拉取策略',
        syncWindowDays: '每次同步窗口',
        initialFetchDays: '首次拉取天数',
        maxFetchCount: '最大拉取条数',
        fetchFullHistory: '完整历史归档',
        retentionCleanup: '数据保留与清理',
        retentionDays: '保留天数',
        storageByDay: '按天分目录存储',
        cleanupPreview: '清理预览',
        cleanupExecute: '执行清理',
        cleanupCutoff: '清理截止',
        expectedDelete: '预计删除',
        oldestSync: '最早同步',
        newestSync: '最晚同步',
        fullHistoryTitle: '完整历史归档已开启',
        fullHistoryDescription: '首次同步可能拉取大量历史公告，建议配合最大拉取条数使用。',
        unlimitedMaxTitle: '最大拉取条数未限制',
        unlimitedMaxDescription: 'SEC 返回较多历史数据时，同步耗时和本地数据量会明显增加。',
        highMaxTitle: '最大拉取条数较高',
        highMaxDescription: '新增热门标的时可能一次入库大量公告，建议确认这是预期行为。',
        unlimitedWindowTitle: '每次同步窗口未限制',
        unlimitedWindowDescription: '已有标的后续同步也可能继续处理较早公告。',
        longWindowTitle: '每次同步窗口较长',
        longWindowDescription: '周期任务会在较长时间范围内检查所有启用标的，耗时可能增加。',
        longInitialTitle: '首次拉取窗口较长',
        longInitialDescription: '新标的首次同步会覆盖超过一年的公告数据。',
        shortRetentionTitle: '保留天数较短',
        shortRetentionDescription: '清理后较早公告将无法在本地继续检索。',
        byDayTitle: '已启用按天分目录',
        byDayDescription: '适合测试和归档隔离；长期运行时请确认备份和清理策略。',
        summarySyncUnlimited: '每次不限制时间',
        summarySyncDays: '每次最近 {days} 天',
        summaryInitialFull: '首次完整历史',
        summaryInitialDays: '首次最近 {days} 天',
        summaryMaxUnlimited: '不限制条数',
        summaryMaxCount: '最多 {count} 条',
        summarySecPolicy: '{syncWindow}，{initialWindow}，{max}',
        summaryStorageByDay: '按天分目录',
        summaryContinuousDb: '连续数据库',
        summaryRetention: '公告保留 {days} 天，{storage}',
        interfaceSettings: '界面设置',
        defaultLanguage: '默认语言',
        defaultLanguageHint: '用于新浏览器或尚未手动选择语言的用户；顶部语言切换会保存为个人偏好。'
      },
      auditLogs: {
        title: '审计日志',
        empty: '暂无审计日志',
        object: '对象',
        objectId: '对象 ID',
        before: '操作前',
        after: '操作后'
      },
      notificationLogs: {
        title: '通知日志',
        empty: '暂无通知日志',
        channel: '渠道'
      },
      telegram: {
        title: 'Telegram',
        tokenPlaceholder: '输入新 Token；保留脱敏值则不更新',
        enableNotification: '启用通知',
        testSend: '测试发送'
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
      confirm: 'Confirm',
      delete: 'Delete',
      disabled: 'Disabled',
      duration: 'Duration',
      edit: 'Edit',
      enabled: 'Enabled',
      error: 'Error',
      filingDate: 'Filing Date',
      filings: 'Filings',
      finishTime: 'Finish Time',
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
      retry: 'Retry',
      retryCount: 'Retries',
      save: 'Save',
      search: 'Search',
      source: 'Source',
      startTime: 'Start Time',
      status: 'Status',
      sync: 'Sync',
      syncTime: 'Synced At',
      target: 'Target',
      task: 'Task',
      time: 'Time',
      title: 'Title',
      type: 'Type',
      update: 'Updated',
      user: 'User',
      view: 'View',
      viewAll: 'View All',
      details: 'Details'
    },
    status: {
      success: 'Success',
      failed: 'Failed',
      running: 'Running',
      pending: 'Pending',
      partial: 'Partial',
      enabled: 'Enabled',
      disabled: 'Disabled',
      unknown: 'Unknown',
      unnotified: 'Not Sent'
    },
    messages: {
      saved: 'Saved',
      configSaved: 'Settings saved',
      newFilingsAdded: '{count} new filings added',
      deletedFilings: '{count} filings deleted',
      syncDone: 'Sync finished, {count} new',
      retryDone: '{ticker} retry finished, {count} new',
      retryAllDone: 'Retried {targets} failed targets, {count} new',
      lookupDone: 'Company name and CIK filled',
      lookupFailed: 'Lookup failed. Check the ticker or fill it manually.',
      taskSaved: 'Scheduler settings saved',
      taskTriggered: 'Task triggered',
      telegramSaved: 'Telegram settings saved',
      telegramTestSent: 'Test message sent',
      telegramTestFailed: 'Test failed. Check Bot Token and Chat ID.',
      confirmDeleteTarget: 'Delete {ticker}?',
      confirmDeleteTitle: 'Confirm Delete',
      confirmCleanup: 'Delete {count} expired filings?',
      cleanupTitle: 'Run Data Cleanup',
      offerSync: 'Sync SEC filings for {ticker} now?',
      targetSavedTitle: 'Target Saved',
      syncNow: 'Sync Now',
      later: 'Later'
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
        countSuffix: '{count}',
        noSyncErrorDetail: 'Sync failed with no error details',
        failedTargetsAlertTitle: '{count} targets failed to sync',
        failedTargetsAlertDescription: 'Open Watch Targets to inspect errors, or retry failed targets individually.',
        noSyncAlertTitle: 'No sync history yet',
        noSyncAlertDescription: 'After adding targets, refresh filings manually or wait for the scheduler.',
        staleSyncAlertTitle: 'Last sync was over {hours} hours ago',
        staleSyncAlertDescription: 'Check whether the scheduler is enabled, or refresh filings manually.',
        schedulerDisabledTitle: 'Scheduler is disabled',
        schedulerDisabledDescription: 'SEC filings will not be pulled automatically.',
        telegramDisabledTitle: 'Telegram notifications are disabled',
        telegramDisabledDescription: 'New filings will be stored but not pushed proactively.',
        healthyTitle: 'System is healthy',
        healthyDescription: 'Sync, scheduler, and notification settings show no obvious issues.'
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
        noFilings: 'No filings yet. Try syncing this target.',
        policyEveryUnlimited: 'No time limit per sync',
        policyEveryDays: 'Last {days} days per sync',
        policyInitialFull: 'full history on first sync',
        policyInitialDays: 'last {days} days on first sync',
        policyMaxUnlimited: 'unlimited',
        policyMaxCount: '{count}',
        policySummary: '{syncWindow}, {initialWindow}, max {max}',
        syncIssueTitle: '{ticker} recently failed to sync',
        syncIssueCik: 'Check whether the ticker is correct, or fill in the CIK manually and retry.',
        syncIssueTimeout: 'This looks like an SEC request timeout. Retry later or lower the max fetch count.',
        syncIssueTelegram: 'Filings may be stored, but notification failed. Check Telegram settings.',
        syncIssueDefault: 'Retry this target first. If it keeps failing, inspect error details in Sync History.'
      },
      filings: {
        title: 'SEC Filings',
        empty: 'No filings. Refresh data or adjust filters.',
        quickFilter: 'Quick Filters',
        start: 'Start',
        end: 'End',
        notification: 'Notification',
        publishedAt: 'Published At',
        typeTooltip: 'View SEC filing type descriptions',
        typePlaceholder: 'Select or search type',
        typeHelpTitle: 'SEC Filing Type Guide',
        typeName: 'Name',
        typeDescription: 'Description',
        important: 'Important',
        watch: 'Watch',
        normal: 'Normal',
        filters: {
          recent7Days: 'Last 7 Days',
          majorEvents: 'Major Events',
          annual10K: 'Annual 10-K',
          quarterly10Q: 'Quarterly 10-Q',
          insiderTrading: 'Insider Trading',
          financingS1: 'Financing S-1'
        }
      },
      syncRuns: {
        title: 'Sync History',
        empty: 'No sync history',
        retryCurrentFailures: 'Retry Current Failures',
        viewTarget: 'View Target',
        triggers: {
          manual: 'Manual',
          scheduler: 'Scheduler',
          target: 'Single Target'
        }
      },
      scheduler: {
        title: 'Scheduler',
        empty: 'No scheduled jobs',
        commonFrequency: 'Common Frequency',
        runNow: 'Run Now',
        lastRun: 'Last Run',
        cronInvalid: 'Cron needs 5 fields: minute hour day month weekday',
        cronHourlyMinute: 'Run at minute {minute} every hour',
        cronEveryMinutes: 'Run every {minutes} minutes',
        cronCustom: 'Custom cron expression',
        presets: {
          every5: 'Every 5 minutes',
          every30: 'Every 30 minutes',
          hourly: 'Hourly',
          daily9: 'Daily 09:00'
        }
      },
      configs: {
        title: 'System Settings',
        save: 'Save Settings',
        secPolicy: 'SEC Fetch Policy',
        syncWindowDays: 'Sync Window',
        initialFetchDays: 'Initial Fetch Days',
        maxFetchCount: 'Max Fetch Count',
        fetchFullHistory: 'Full History Archive',
        retentionCleanup: 'Retention & Cleanup',
        retentionDays: 'Retention Days',
        storageByDay: 'Store By Day',
        cleanupPreview: 'Cleanup Preview',
        cleanupExecute: 'Run Cleanup',
        cleanupCutoff: 'Cleanup Cutoff',
        expectedDelete: 'Expected Deletes',
        oldestSync: 'Oldest Sync',
        newestSync: 'Newest Sync',
        fullHistoryTitle: 'Full history archive is enabled',
        fullHistoryDescription: 'Initial sync may pull many historical filings. Use it with max fetch count.',
        unlimitedMaxTitle: 'Max fetch count is unlimited',
        unlimitedMaxDescription: 'When SEC returns many historical filings, sync time and local data size can grow significantly.',
        highMaxTitle: 'Max fetch count is high',
        highMaxDescription: 'Adding popular targets may insert many filings at once. Confirm this is intended.',
        unlimitedWindowTitle: 'Sync window is unlimited',
        unlimitedWindowDescription: 'Existing targets may continue processing older filings on later syncs.',
        longWindowTitle: 'Sync window is long',
        longWindowDescription: 'Scheduled jobs will scan all enabled targets over a long range and may take longer.',
        longInitialTitle: 'Initial fetch window is long',
        longInitialDescription: 'New target first sync will cover more than one year of filings.',
        shortRetentionTitle: 'Retention period is short',
        shortRetentionDescription: 'After cleanup, older filings cannot be searched locally.',
        byDayTitle: 'Store by day is enabled',
        byDayDescription: 'Good for testing and archive isolation. Confirm backup and cleanup strategy for long-running use.',
        summarySyncUnlimited: 'No time limit per sync',
        summarySyncDays: 'last {days} days per sync',
        summaryInitialFull: 'full history initially',
        summaryInitialDays: 'last {days} days initially',
        summaryMaxUnlimited: 'unlimited count',
        summaryMaxCount: 'max {count}',
        summarySecPolicy: '{syncWindow}, {initialWindow}, {max}',
        summaryStorageByDay: 'store by day',
        summaryContinuousDb: 'continuous database',
        summaryRetention: 'Keep filings for {days} days, {storage}',
        interfaceSettings: 'Interface Settings',
        defaultLanguage: 'Default Language',
        defaultLanguageHint: 'Used for new browsers or users who have not manually selected a language. The top language switch is saved as a personal preference.'
      },
      auditLogs: {
        title: 'Audit Logs',
        empty: 'No audit logs',
        object: 'Object',
        objectId: 'Object ID',
        before: 'Before',
        after: 'After'
      },
      notificationLogs: {
        title: 'Notification Logs',
        empty: 'No notification logs',
        channel: 'Channel'
      },
      telegram: {
        title: 'Telegram',
        tokenPlaceholder: 'Enter a new token; keep the masked value to leave it unchanged',
        enableNotification: 'Enable Notifications',
        testSend: 'Test Send'
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
    locale: normalizeLocale(localStorage.getItem(LOCALE_KEY)),
    hasLocalPreference: localStorage.getItem(LOCALE_KEY) !== null
  }),
  actions: {
    setLocale(locale: Locale) {
      this.locale = locale
      this.hasLocalPreference = true
      localStorage.setItem(LOCALE_KEY, locale)
      document.documentElement.lang = locale
    },
    applyDefaultLocale(locale: Locale) {
      if (this.hasLocalPreference) return
      this.locale = locale
      document.documentElement.lang = locale
    },
    async loadConfiguredDefaultLocale() {
      if (this.hasLocalPreference) return
      try {
        const res = await apiClient.get<ApiResponse<SystemConfig[]>>('/system-configs', { params: { category: 'ui' } })
        const locale = res.data.data.find((item) => item.config_key === DEFAULT_LOCALE_CONFIG_KEY)?.config_value
        this.applyDefaultLocale(normalizeLocale(locale || null))
      } catch (error) {
        this.applyDefaultLocale('zh-CN')
      }
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
