<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.configs.title') }}</h1>
      <div>
        <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ t('pages.configs.save') }}</el-button>
      </div>
    </div>

    <div class="config-category-bar" aria-label="配置分类">
      <el-radio-group v-model="activeConfigSection" size="large">
        <el-radio-button v-for="item in configSections" :key="item.key" :value="item.key">
          {{ item.label }}
        </el-radio-button>
      </el-radio-group>
      <span class="config-category-hint">{{ activeConfigSectionHint }}</span>
    </div>

    <div class="config-grid">
      <div v-show="activeConfigSection === 'general'" class="config-section">
        <div class="config-section-heading">
          <div>
            <h2>基础与调度</h2>
            <p>仅影响界面默认语言和所有 Cron 表达式的解释时区。</p>
          </div>
        </div>
      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.interfaceSettings') }}</span>
            <el-tag effect="plain">{{ localeLabel(uiForm.default_locale) }}</el-tag>
          </div>
        </template>
        <el-form :model="uiForm" label-width="150px">
          <el-form-item :label="t('pages.configs.defaultLanguage')">
            <el-select v-model="uiForm.default_locale" style="width: 180px">
              <el-option label="中文" value="zh-CN" />
              <el-option label="English" value="en-US" />
            </el-select>
          </el-form-item>
        </el-form>
        <el-alert :title="t('pages.configs.defaultLanguageHint')" type="info" :closable="false" show-icon />
      </el-card>

      </div>

      <div v-show="activeConfigSection === 'notifications'" class="config-section">
        <div class="config-section-heading">
          <div>
            <h2>通知规则</h2>
            <p>统一由通知中心投递和记录；这里仅配置不同业务事件的筛选条件与发送边界。</p>
          </div>
        </div>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.notificationRules') }}</span>
          </div>
        </template>
        <el-form :model="notificationForm" label-width="150px">
          <el-form-item :label="t('pages.configs.importantOnly')">
            <el-switch v-model="notificationForm.important_only" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.notifyFilingTypes')">
            <el-input v-model="notificationForm.filing_types" :placeholder="t('pages.configs.notifyFilingTypesPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.notifyKeywords')">
            <el-input v-model="notificationForm.keywords" :placeholder="t('pages.configs.notifyKeywordsPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.quietHoursEnabled')">
            <el-switch v-model="notificationForm.quiet_hours_enabled" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.quietHoursStart')">
            <el-time-picker v-model="notificationForm.quiet_hours_start" format="HH:mm" value-format="HH:mm" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.quietHoursEnd')">
            <el-time-picker v-model="notificationForm.quiet_hours_end" format="HH:mm" value-format="HH:mm" />
          </el-form-item>
        </el-form>
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>事件通知渠道</span>
            <div class="panel-header-actions">
              <el-tag effect="plain">站内 {{ inAppNotificationEnabledCount }} / {{ notificationChannelRows.length }}</el-tag>
              <el-tag effect="plain">Telegram {{ telegramNotificationEnabledCount }} / {{ notificationChannelRows.length }}</el-tag>
            </div>
          </div>
        </template>
        <el-table :data="notificationChannelRows" class="notification-channel-table">
          <el-table-column prop="menu" label="菜单" width="150" />
          <el-table-column prop="label" label="事件" width="150" />
          <el-table-column prop="description" label="触发范围" min-width="360" />
          <el-table-column label="站内消息" width="140" align="center">
            <template #default="{ row }"><el-checkbox :model-value="notificationChannelEnabled('in_app', row.key)" @change="setNotificationChannelEnabled('in_app', row.key, $event)">启用</el-checkbox></template>
          </el-table-column>
          <el-table-column label="Telegram" width="140" align="center">
            <template #default="{ row }"><el-checkbox :model-value="notificationChannelEnabled('telegram', row.key)" @change="setNotificationChannelEnabled('telegram', row.key, $event)">启用</el-checkbox></template>
          </el-table-column>
        </el-table>
        <el-alert title="两个渠道独立控制，仅影响后续事件。关闭 Telegram 后不会发送，并会在通知日志记录“按事件频道关闭”；Telegram 仍需启用机器人，且原有范围、阈值、静默时段规则继续生效。" type="info" :closable="false" show-icon />
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.candidateNotification') }}</span>
            <el-tag effect="plain">{{ candidateNotificationSummary }}</el-tag>
          </div>
        </template>
        <el-form :model="candidateNotificationForm" label-width="150px">
          <el-form-item :label="t('pages.configs.candidateNotificationEnabled')">
            <el-switch v-model="candidateNotificationForm.enabled" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.candidateNotificationShadowMode')">
            <el-switch v-model="candidateNotificationForm.shadow_mode" />
            <span class="form-help">{{ t('pages.configs.candidateNotificationShadowModeHint') }}</span>
          </el-form-item>
          <el-form-item :label="t('pages.configs.candidateNotifyA')">
            <el-switch v-model="candidateNotificationForm.notify_a" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.candidateNotifyB')">
            <el-switch v-model="candidateNotificationForm.notify_b" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.candidateSendTime')">
            <el-time-picker v-model="candidateNotificationForm.send_time" format="HH:mm" value-format="HH:mm" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.candidateMaxPerGrade')">
            <el-input-number v-model="candidateNotificationForm.max_per_grade" :min="1" :max="20" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.candidateActionableOnly')">
            <el-switch v-model="candidateNotificationForm.actionable_only" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.candidateMinPriority')">
            <el-input-number v-model="candidateNotificationForm.min_review_priority_score" :min="0" :max="2000" :step="50" />
          </el-form-item>
        </el-form>
        <el-alert :title="t('pages.configs.candidateNotificationHint')" type="info" :closable="false" show-icon />
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>交易计划通知</span>
            <el-tag effect="plain">{{ tradeSetupNotificationForm.enabled ? '已启用' : '未启用' }}</el-tag>
          </div>
        </template>
        <el-form :model="tradeSetupNotificationForm" label-width="150px">
          <el-form-item label="启用通知">
            <el-switch v-model="tradeSetupNotificationForm.enabled" />
          </el-form-item>
          <el-form-item label="影子模式">
            <el-switch v-model="tradeSetupNotificationForm.shadow_mode" />
            <span class="form-help">仅生成预检结果与状态变化，不发送 Telegram，也不推进通知基线。</span>
          </el-form-item>
          <el-form-item label="入场候选">
            <el-switch v-model="tradeSetupNotificationForm.notify_entry" />
          </el-form-item>
          <el-form-item label="离场预警">
            <el-switch v-model="tradeSetupNotificationForm.notify_exit" />
          </el-form-item>
          <el-form-item label="趋势失效">
            <el-switch v-model="tradeSetupNotificationForm.notify_invalidated" />
          </el-form-item>
          <el-form-item label="每次通知上限">
            <el-input-number v-model="tradeSetupNotificationForm.max_per_run" :min="1" :max="50" />
          </el-form-item>
        </el-form>
        <el-alert title="仅监控已启用标的的日线状态变化。首次扫描只提示入场候选，离场类状态先建立基线；请在“调度任务”中启用 trade_setup_notification_sync 后自动执行。" type="info" :closable="false" show-icon />
      </el-card>

      </div>

      <div v-show="activeConfigSection === 'general'" class="config-section">
        <div class="config-section-heading">
          <div>
            <h2>运行时区</h2>
            <p>所有调度任务的 Cron 表达式都统一使用这里设置的时区解释。</p>
          </div>
        </div>
      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.schedulerSettings') }}</span>
            <el-tag effect="plain">{{ schedulerForm.timezone }}</el-tag>
          </div>
        </template>
        <el-form :model="schedulerForm" label-width="150px">
          <el-form-item :label="t('pages.configs.schedulerTimezone')">
            <el-select
              v-model="schedulerForm.timezone"
              filterable
              allow-create
              default-first-option
              style="width: 240px"
            >
              <el-option v-for="item in timezoneOptions" :key="item" :label="item" :value="item" />
            </el-select>
          </el-form-item>
        </el-form>
        <el-alert :title="t('pages.configs.schedulerTimezoneHint')" type="info" :closable="false" show-icon />
      </el-card>

      </div>

      <div v-show="activeConfigSection === 'data'" class="config-section">
        <div class="config-section-heading">
          <div>
            <h2>数据源与研究同步</h2>
            <p>Longbridge 为主数据源，备用行情源和请求限额收纳在高级配置中；所有结果先写入本地再由页面读取。</p>
          </div>
        </div>
      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>行情与研究数据源</span>
            <el-tag effect="plain">{{ discoveryDatasourceSummary }}</el-tag>
          </div>
        </template>
        <el-form :model="discoveryForm" label-width="280px" class="research-settings-form">
          <el-form-item :label="t('pages.configs.discoveryPriceProvider')">
            <el-select v-model="discoveryForm.price_provider" style="width: 220px">
              <el-option :label="t('pages.configs.discoveryProviderAuto')" value="" />
              <el-option label="Longbridge → Tiingo → Twelve Data → Yahoo" value="longbridge,tiingo,twelvedata,yahoo" />
              <el-option label="Longbridge" value="longbridge" />
              <el-option label="Tiingo" value="tiingo" />
              <el-option label="Tiingo → Twelve Data → Yahoo" value="tiingo,twelvedata,yahoo" />
              <el-option label="Tiingo → Yahoo" value="tiingo,yahoo" />
              <el-option label="Twelve Data" value="twelvedata" />
              <el-option label="Stooq → Tiingo → Yahoo" value="stooq,tiingo,yahoo" />
              <el-option label="Yahoo" value="yahoo" />
              <el-option label="Stooq" value="stooq" />
            </el-select>
          </el-form-item>
          <el-form-item label="备用行情源密钥">
            <el-button type="primary" link @click="openProviderSettings">配置 Tiingo / Twelve Data</el-button>
            <span class="form-help">仍作为 Longbridge 的备用行情源；仅在价格 Provider 的顺序包含对应来源时调用。</span>
          </el-form-item>
          <el-form-item :label="t('pages.configs.stooqUrls')">
            <el-input
              v-model="discoveryForm.stooq_urls"
              type="textarea"
              :rows="2"
              :placeholder="t('pages.configs.stooqUrlsPlaceholder')"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.longbridgeAppKey')">
            <el-input v-model="discoveryForm.longbridge_app_key" show-password :placeholder="t('pages.configs.longbridgeSecretPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.longbridgeAppSecret')">
            <el-input v-model="discoveryForm.longbridge_app_secret" show-password :placeholder="t('pages.configs.longbridgeSecretPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.longbridgeAccessToken')">
            <el-input v-model="discoveryForm.longbridge_access_token" show-password :placeholder="t('pages.configs.longbridgeSecretPlaceholder')" />
          </el-form-item>
          <el-form-item label="Longbridge 行情连接">
            <el-button :loading="longbridgeProbeLoading" @click="probeLongbridgeQuote">测试行情连接</el-button>
            <span class="form-help">仅请求 AAPL.US 一次；使用已保存的凭证，不触发候选同步或其他行情源。</span>
          </el-form-item>
          <el-alert
            v-if="longbridgeProbe"
            :title="longbridgeProbeTitle"
            :description="longbridgeProbeDescription"
            :type="longbridgeProbe.status === 'ok' ? 'success' : 'error'"
            :closable="false"
            show-icon
          />
          <el-form-item label="Longbridge 公司资料补充">
            <el-switch v-model="discoveryForm.longbridge_company_profile_enabled" />
            <span class="form-help">候选同步后增量补充公司简介；详情页仅读取本地缓存。</span>
          </el-form-item>
          <el-form-item label="公司资料单次预算">
            <el-input-number v-model="discoveryForm.longbridge_company_profile_request_budget" :min="0" :max="200" controls-position="right" />
            <span class="form-help">每次小盘工作流最多请求的 Longbridge 公司概览数量。</span>
          </el-form-item>
          <el-form-item label="公司资料缓存有效期（天）">
            <el-input-number v-model="discoveryForm.longbridge_company_profile_ttl_days" :min="1" :max="365" controls-position="right" />
          </el-form-item>
          <el-divider content-position="left">Longbridge 分析师共识</el-divider>
          <el-form-item label="分析师共识同步">
            <el-switch v-model="discoveryForm.longbridge_analyst_rating_enabled" />
            <span class="form-help">候选工作流结束后，按预算补充机构评级聚合共识；详情页只读取本地快照。</span>
          </el-form-item>
          <el-form-item label="分析师共识单次预算">
            <el-input-number v-model="discoveryForm.longbridge_analyst_rating_request_budget" :min="0" :max="200" controls-position="right" />
          </el-form-item>
          <el-form-item label="目标价推送阈值">
            <el-input-number v-model="discoveryForm.longbridge_analyst_rating_target_change_pct" :min="0" :max="100" :step="0.5" controls-position="right" />
            <span class="form-help">平均目标价相对上次快照变动达到该百分比时，才标记为更新。</span>
          </el-form-item>
          <el-form-item label="评级更新 Telegram 推送">
            <el-switch v-model="discoveryForm.analyst_rating_notify_enabled" />
            <span class="form-help">仅在评级、覆盖数量或目标价出现有效变化时推送；首次建立快照不推送。</span>
          </el-form-item>
          <el-divider content-position="left">Longbridge 候选研究定时任务</el-divider>
          <el-form-item label="P1 市场研究同步">
            <el-switch v-model="discoveryForm.longbridge_candidate_research_enabled" />
            <span class="form-help">独立更新 EPS 预期、异动及机构/基金持仓；不触发小盘选股或 P2 估值请求。</span>
          </el-form-item>
          <el-form-item label="P1 单次候选预算">
            <el-input-number v-model="discoveryForm.longbridge_candidate_research_request_budget" :min="0" :max="50" controls-position="right" />
          </el-form-item>
		  <el-form-item label="监控标的机构持仓研究同步">
			<el-switch v-model="discoveryForm.longbridge_watch_target_research_enabled" />
			<span class="form-help">独立更新已启用股票的机构股东、基金/ETF 持仓、EPS 预期与异动；不占用候选 P1 预算。</span>
		  </el-form-item>
		  <el-form-item label="监控标的 P1 单次预算">
			<el-input-number v-model="discoveryForm.longbridge_watch_target_research_request_budget" :min="0" :max="50" controls-position="right" />
		  </el-form-item>
          <el-form-item label="P2 估值研究同步">
            <el-switch v-model="discoveryForm.longbridge_candidate_valuation_enabled" />
            <span class="form-help">独立更新估值历史、行业分位与同业比较；接口较重，单次预算单独控制。</span>
          </el-form-item>
          <el-form-item label="P2 单次候选预算">
            <el-input-number v-model="discoveryForm.longbridge_candidate_valuation_request_budget" :min="0" :max="50" controls-position="right" />
          </el-form-item>
		  <el-form-item label="监控标的估值研究同步">
			<el-switch v-model="discoveryForm.longbridge_watch_target_valuation_enabled" />
			<span class="form-help">独立轮换已启用股票监控标的的估值历史、行业分位与同业比较；不占用候选 P2 预算。</span>
		  </el-form-item>
		  <el-form-item label="监控标的单次预算">
			<el-input-number v-model="discoveryForm.longbridge_watch_target_valuation_request_budget" :min="0" :max="50" controls-position="right" />
		  </el-form-item>
		  <el-divider content-position="left">Longbridge 期权与空头研究（P0）</el-divider>
		  <el-form-item label="期权与空头研究同步">
			<el-switch v-model="discoveryForm.longbridge_option_research_enabled" />
			<span class="form-help">独立保存 Call/Put 汇总成交量、Put/Call、空头比例与 days to cover；不改变基本面总分。</span>
		  </el-form-item>
		  <el-form-item label="候选单次预算">
			<el-input-number v-model="discoveryForm.longbridge_candidate_option_research_budget" :min="0" :max="50" controls-position="right" />
		  </el-form-item>
		  <el-form-item label="监控标的单次预算">
			<el-input-number v-model="discoveryForm.longbridge_watch_target_option_research_budget" :min="0" :max="50" controls-position="right" />
		  </el-form-item>
          <el-divider content-position="left">监控标的财报预告</el-divider>
          <el-form-item label="财报预告同步">
            <el-switch v-model="earningsPreviewForm.enabled" />
            <span class="form-help">读取 Longbridge 财报日历并保存到本地；列表和详情页不会实时请求外部接口。</span>
          </el-form-item>
          <el-form-item label="向前查询天数">
            <el-input-number v-model="earningsPreviewForm.lookahead_days" :min="7" :max="180" controls-position="right" />
            <span class="form-help">默认 90 天。范围越大，日历分页请求越多。</span>
          </el-form-item>
          <el-form-item label="日历分页上限">
            <el-input-number v-model="earningsPreviewForm.max_calendar_pages" :min="1" :max="100" controls-position="right" />
            <span class="form-help">每次同步最多读取的 Longbridge 财报日历页数。</span>
          </el-form-item>
          <el-form-item label="财报预告 Telegram 推送">
            <el-switch v-model="earningsPreviewForm.notify_enabled" />
            <span class="form-help">预告日期或预期值发生有效变化，或到达提醒日时推送；首次建立缓存不推送变更通知。</span>
          </el-form-item>
          <el-form-item label="提醒提前天数">
            <el-input v-model="earningsPreviewForm.reminder_days" placeholder="7,3,1,0" />
            <span class="form-help">以英文逗号分隔；0 表示预计财报日当天。</span>
          </el-form-item>
          <el-collapse id="provider-settings" v-model="dataAdvancedPanels" class="config-advanced-collapse">
            <el-collapse-item title="Tiingo / Twelve Data / Yahoo：备用行情源与运行参数（高级）" name="providers">
              <p class="form-help collapse-help">仅在 Longbridge 不可用、调试数据源或调整限流/缓存策略时修改。日常使用无需展开。</p>
          <el-form-item :label="t('pages.configs.tiingoApiToken')">
            <el-input
              v-model="discoveryForm.tiingo_api_token"
              show-password
              :placeholder="t('pages.configs.tiingoApiTokenPlaceholder')"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.tiingoApiTokens')">
            <el-input
              v-model="discoveryForm.tiingo_api_tokens"
              show-password
              :placeholder="t('pages.configs.tiingoApiTokensPlaceholder')"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.tiingoRequestBudget')">
            <el-input-number
              v-model="discoveryForm.tiingo_request_budget"
              :min="0"
              :step="5"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.tiingoBaseUrl')">
            <el-input v-model="discoveryForm.tiingo_base_url" placeholder="https://api.tiingo.com" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.twelveDataApiKey')">
            <el-input
              v-model="discoveryForm.twelve_data_api_key"
              show-password
              :placeholder="t('pages.configs.twelveDataApiKeyPlaceholder')"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.twelveDataRequestBudget')">
            <el-input-number
              v-model="discoveryForm.twelve_data_request_budget"
              :min="0"
              :step="50"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.twelveDataRequestIntervalMs')">
            <el-input-number
              v-model="discoveryForm.twelve_data_request_interval_ms"
              :min="1000"
              :step="500"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.twelveDataBaseUrl')">
            <el-input v-model="discoveryForm.twelve_data_base_url" placeholder="https://api.twelvedata.com" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.yahooRequestBudget')">
            <el-input-number
              v-model="discoveryForm.yahoo_request_budget"
              :min="0"
              :step="5"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.yahooBaseUrl')">
            <el-input v-model="discoveryForm.yahoo_base_url" placeholder="https://query1.finance.yahoo.com" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.minPublishCoveragePct')">
            <el-input-number
              v-model="discoveryForm.min_publish_coverage_pct"
              :min="0"
              :max="100"
              :step="5"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item :label="t('pages.configs.autoTechnicalHistoryWarmup')">
            <el-switch v-model="discoveryForm.auto_technical_history_warmup" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.discoveryTaskTimeoutMinutes')">
            <el-input-number v-model="discoveryForm.task_timeout_minutes" :min="15" :max="240" :step="15" controls-position="right" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.discoveryDownloadIdleTimeoutSeconds')">
            <el-input-number v-model="discoveryForm.download_idle_timeout_seconds" :min="30" :max="900" :step="30" controls-position="right" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.discoverySECBulkCacheTTLHours')">
            <el-input-number v-model="discoveryForm.sec_bulk_cache_ttl_hours" :min="1" :max="72" :step="1" controls-position="right" />
          </el-form-item>
          <el-form-item label="SEC 缓存保留天数">
            <el-input-number v-model="discoveryForm.cache_retention_days" :min="1" :max="90" :step="1" controls-position="right" />
          </el-form-item>
            </el-collapse-item>
          </el-collapse>
        </el-form>
        <el-alert :title="t('pages.configs.discoveryDatasourceHint')" description="过期缓存只会在同步开始或“小盘候选”页手动清理时删除；不会删除研究库中的候选、评分、公告或研究记录。" type="info" :closable="false" show-icon />
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.ipoRadar') }}</span>
          </div>
        </template>
        <el-form :model="ipoForm" label-width="150px">
          <el-form-item :label="t('pages.configs.ipoEnabled')">
            <el-switch v-model="ipoForm.enabled" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoFormTypes')">
            <el-input v-model="ipoForm.form_types" :placeholder="t('pages.configs.ipoFormTypesPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoLookbackDays')">
            <el-input-number v-model="ipoForm.lookback_days" :min="1" :max="365" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoMaxResults')">
            <el-input-number v-model="ipoForm.max_results" :min="1" :max="100" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoNotifyEnabled')">
            <el-switch v-model="ipoForm.notify_enabled" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoNotifyFormTypes')">
            <el-input v-model="ipoForm.notify_form_types" :placeholder="t('pages.configs.ipoNotifyFormTypesPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.ipoKeywords')">
            <el-input v-model="ipoForm.keywords" :placeholder="t('pages.configs.ipoKeywordsPlaceholder')" />
          </el-form-item>
          <el-divider content-position="left">Longbridge 自动上市确认</el-divider>
          <el-form-item label="启用自动确认">
            <el-switch v-model="ipoForm.longbridge_listing_verification_enabled" />
            <span class="form-help">SEC 已匹配 Ticker、但交易所缺失时，自动查询 Longbridge 市场资料；查询失败仍保留“待确认”。</span>
          </el-form-item>
          <el-form-item label="单次确认预算">
            <el-input-number v-model="ipoForm.longbridge_listing_request_budget" :min="0" :max="200" controls-position="right" />
            <span class="form-help">每次 IPO 同步最多确认的待上市公司数。</span>
          </el-form-item>
          <el-form-item label="复查间隔（小时）">
            <el-input-number v-model="ipoForm.longbridge_listing_recheck_hours" :min="1" :max="168" controls-position="right" />
          </el-form-item>
          <el-divider content-position="left">Longbridge IPO 日历</el-divider>
          <el-form-item label="启用 IPO 日历">
            <el-switch v-model="ipoForm.longbridge_calendar_enabled" />
            <span class="form-help">每次 IPO 扫描读取并缓存美股 IPO 日历；仅在公司名严格匹配 SEC 候选时补充预计上市日和标的。</span>
          </el-form-item>
          <el-form-item label="回看天数">
            <el-input-number v-model="ipoForm.longbridge_calendar_lookback_days" :min="0" :max="365" controls-position="right" />
          </el-form-item>
          <el-form-item label="前瞻天数">
            <el-input-number v-model="ipoForm.longbridge_calendar_lookahead_days" :min="0" :max="365" controls-position="right" />
          </el-form-item>
          <el-form-item label="单次最多页数">
            <el-input-number v-model="ipoForm.longbridge_calendar_max_pages" :min="1" :max="20" controls-position="right" />
          </el-form-item>
        </el-form>
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.secPolicy') }}</span>
            <el-tag effect="plain">{{ secPolicySummary }}</el-tag>
          </div>
        </template>
        <el-form :model="secForm" label-width="150px">
          <el-form-item label="SEC User-Agent">
            <el-input v-model="secForm.user_agent" placeholder="例如 SEC Monitor admin@your-domain.com" />
            <span class="form-help">用于 SEC 请求识别；保存后重启服务生效。</span>
          </el-form-item>
          <el-form-item :label="t('pages.configs.syncWindowDays')">
            <el-input-number v-model="secForm.sync_window_days" :min="0" :max="3650" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.initialFetchDays')">
            <el-input-number v-model="secForm.initial_fetch_days" :min="1" :max="3650" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.maxFetchCount')">
            <el-input-number v-model="secForm.max_fetch_count" :min="0" :max="5000" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.fetchFullHistory')">
            <el-switch v-model="secForm.fetch_full_history" />
          </el-form-item>
        </el-form>
        <div class="config-risk-list">
          <el-alert
            v-for="item in secRiskHints"
            :key="item.title"
            :title="item.title"
            :description="item.description"
            :type="item.type"
            :closable="false"
            show-icon
          />
        </div>
      </el-card>

      </div>

      <div v-show="activeConfigSection === 'maintenance'" class="config-section">
        <div class="config-section-heading">
          <div>
            <h2>存储、清理与导出</h2>
            <p>用于控制本地数据保留、清理预览和备份导出；清理动作始终需要手动确认。</p>
          </div>
        </div>
      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.retentionCleanup') }}</span>
            <el-tag effect="plain">{{ retentionPolicySummary }}</el-tag>
          </div>
        </template>
        <el-form :model="systemForm" label-width="150px">
          <el-form-item :label="t('pages.configs.retentionDays')">
            <el-input-number v-model="systemForm.data_retention_days" :min="1" :max="3650" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.storageByDay')">
            <el-switch v-model="systemForm.storage_by_day" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.backupRetentionDays')">
            <el-input-number v-model="systemForm.backup_retention_days" :min="1" :max="365" />
          </el-form-item>
          <el-form-item :label="t('pages.configs.operationHistoryRetentionDays')">
            <el-input-number v-model="systemForm.operation_history_retention_days" :min="7" :max="3650" />
            <span class="form-help">{{ t('pages.configs.operationHistoryRetentionHint') }}</span>
          </el-form-item>
          <el-form-item :label="t('pages.configs.backupDirectory')">
            <el-input v-model="systemForm.backup_dir" :placeholder="t('pages.configs.backupDirectoryPlaceholder')" />
            <span class="form-help">{{ t('pages.configs.backupDirectoryHint') }}</span>
          </el-form-item>
          <el-form-item :label="t('pages.configs.storageWarningPct')">
            <el-input-number v-model="systemForm.storage_warning_pct" :min="1" :max="100" />
          </el-form-item>
        </el-form>
        <div class="config-risk-list">
          <el-alert
            v-for="item in systemRiskHints"
            :key="item.title"
            :title="item.title"
            :description="item.description"
            :type="item.type"
            :closable="false"
            show-icon
          />
        </div>
        <div class="cleanup-actions">
          <el-button :loading="previewing" @click="loadCleanupPreview">{{ t('pages.configs.cleanupPreview') }}</el-button>
          <el-button type="danger" :disabled="!cleanupPreview || cleanupPreview.delete_count === 0" :loading="cleaning" @click="cleanup">{{ t('pages.configs.cleanupExecute') }}</el-button>
        </div>
        <el-descriptions v-if="cleanupPreview" class="cleanup-preview" :column="1" border>
          <el-descriptions-item :label="t('pages.configs.retentionDays')">{{ cleanupPreview.retention_days }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.cleanupCutoff')">{{ formatDateTime(cleanupPreview.cutoff) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.expectedDelete')">{{ cleanupPreview.delete_count }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.oldestSync')">{{ formatDateTime(cleanupPreview.oldest_pulled_at) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.newestSync')">{{ formatDateTime(cleanupPreview.newest_pulled_at) }}</el-descriptions-item>
        </el-descriptions>
        <el-divider />
        <div class="panel-header">
          <span>{{ t('pages.configs.operationHistoryCleanup') }}</span>
          <el-tag effect="plain">{{ t('pages.configs.operationHistorySafe') }}</el-tag>
        </div>
        <p class="form-help">{{ t('pages.configs.operationHistoryCleanupHint') }}</p>
        <div class="cleanup-actions">
          <el-button :loading="previewingLifecycle" @click="loadLifecycleCleanupPreview">{{ t('pages.configs.cleanupPreview') }}</el-button>
          <el-button type="danger" :disabled="!lifecycleCleanupPreview || lifecycleCleanupPreview.total === 0" :loading="cleaningLifecycle" @click="cleanupLifecycle">{{ t('pages.configs.cleanupExecute') }}</el-button>
        </div>
        <el-descriptions v-if="lifecycleCleanupPreview" class="cleanup-preview" :column="2" border>
          <el-descriptions-item :label="t('pages.configs.retentionDays')">{{ lifecycleCleanupPreview.retention_days }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.cleanupCutoff')">{{ formatDateTime(lifecycleCleanupPreview.cutoff) }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.syncRunHistory')">{{ lifecycleCleanupPreview.sync_runs }} / {{ lifecycleCleanupPreview.sync_run_details }}</el-descriptions-item>
		  <el-descriptions-item label="任务执行记录">{{ lifecycleCleanupPreview.task_executions }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.discoveryRunHistory')">{{ lifecycleCleanupPreview.discovery_sync_runs }} / {{ lifecycleCleanupPreview.discovery_sync_steps }}</el-descriptions-item>
			<el-descriptions-item :label="t('pages.configs.marketRepairSnapshots')">{{ lifecycleCleanupPreview.superseded_market_repairs }} / {{ lifecycleCleanupPreview.market_repair_universe_rows }} / {{ lifecycleCleanupPreview.market_repair_score_rows }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.operationalAlerts')">{{ lifecycleCleanupPreview.operational_alert_deliveries }}</el-descriptions-item>
			<el-descriptions-item :label="t('pages.configs.recoveryDrills')">{{ lifecycleCleanupPreview.recovery_drills }}</el-descriptions-item>
			<el-descriptions-item :label="t('pages.configs.cleanupRunHistory')">{{ lifecycleCleanupPreview.lifecycle_cleanup_runs }}</el-descriptions-item>
          <el-descriptions-item :label="t('pages.configs.expectedDelete')">{{ lifecycleCleanupPreview.total }}</el-descriptions-item>
        </el-descriptions>
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="panel-header">
            <span>{{ t('pages.configs.exportBackup') }}</span>
          </div>
        </template>
        <div class="export-actions">
          <el-button @click="download('/api/exports/filings.csv')">{{ t('pages.configs.exportFilings') }}</el-button>
          <el-button @click="download('/api/exports/ipo-companies.csv')">{{ t('pages.configs.exportIPOCompanies') }}</el-button>
          <el-button @click="download('/api/exports/ipo-filings.csv')">{{ t('pages.configs.exportIPOFilings') }}</el-button>
          <el-button @click="download('/api/exports/watch-targets.csv')">{{ t('pages.configs.exportTargets') }}</el-button>
          <el-button @click="download('/api/exports/configs.json')">{{ t('pages.configs.exportConfigs') }}</el-button>
          <el-button type="primary" @click="download('/api/exports/backup.json')">{{ t('pages.configs.exportAll') }}</el-button>
        </div>
      </el-card>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, CleanupPreview, LifecycleCleanupPreview, LongbridgeQuoteProbeResult, SystemConfig } from '@/api/types'
import { type Locale, useI18n } from '@/i18n'

const { store, t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const previewing = ref(false)
const cleaning = ref(false)
const cleanupPreview = ref<CleanupPreview | null>(null)
const previewingLifecycle = ref(false)
const cleaningLifecycle = ref(false)
const lifecycleCleanupPreview = ref<LifecycleCleanupPreview | null>(null)
const longbridgeProbeLoading = ref(false)
const longbridgeProbe = ref<LongbridgeQuoteProbeResult | null>(null)
const activeConfigSection = ref<'general' | 'notifications' | 'data' | 'maintenance'>('general')
const dataAdvancedPanels = ref<string[]>([])
const configSections = [
  { key: 'general', label: '基础与调度', hint: '界面语言与调度时区' },
  { key: 'notifications', label: '通知规则', hint: '公告、候选与交易计划的通知边界' },
  { key: 'data', label: '数据源与同步', hint: '行情、研究、IPO 与 SEC 数据策略' },
  { key: 'maintenance', label: '存储与维护', hint: '保留策略、清理预览与备份导出' }
] as const
const activeConfigSectionHint = computed(() => configSections.find((item) => item.key === activeConfigSection.value)?.hint || '')

const secForm = reactive({ user_agent: '', initial_fetch_days: 30, sync_window_days: 30, max_fetch_count: 300, fetch_full_history: false })
const systemForm = reactive({ data_retention_days: 30, storage_by_day: false, backup_retention_days: 7, operation_history_retention_days: 90, backup_dir: '', storage_warning_pct: 80 })
const uiForm = reactive<{ default_locale: Locale }>({ default_locale: 'zh-CN' })
const notificationForm = reactive({
  important_only: false,
  filing_types: '',
  keywords: '',
  quiet_hours_enabled: false,
  quiet_hours_start: '22:00',
  quiet_hours_end: '08:00'
})
const inAppNotificationForm = reactive({
  watch_target_earnings_preview_enabled: true,
  watch_target_earnings_release_enabled: true,
  watch_target_technical_signal_enabled: true,
  watch_target_major_event_enabled: true,
  watch_target_insider_trading_enabled: true,
  candidate_earnings_preview_enabled: true,
  candidate_earnings_release_enabled: true,
  candidate_technical_signal_enabled: true,
  ipo_progress_enabled: true,
})
const inAppNotificationEnabledCount = computed(() => Object.values(inAppNotificationForm).filter(Boolean).length)
const telegramNotificationForm = reactive({
  watch_target_earnings_preview_enabled: true,
  watch_target_earnings_release_enabled: true,
  watch_target_technical_signal_enabled: true,
  watch_target_major_event_enabled: true,
  watch_target_insider_trading_enabled: true,
  candidate_earnings_preview_enabled: true,
  candidate_earnings_release_enabled: true,
  candidate_technical_signal_enabled: true,
  ipo_progress_enabled: true,
})
const telegramNotificationEnabledCount = computed(() => Object.values(telegramNotificationForm).filter(Boolean).length)
const notificationChannelRows = [
  { menu: '监控标的', key: 'watch_target_earnings_preview_enabled', legacyKey: 'earnings_preview_enabled', label: '财报预告', description: '监控标的的财报日期新增、变更或进入提醒窗口' },
  { menu: '监控标的', key: 'watch_target_earnings_release_enabled', legacyKey: 'earnings_release_enabled', label: '财报已发布', description: '监控标的的 SEC 定期财报或可识别的业绩公告' },
  { menu: '监控标的', key: 'watch_target_technical_signal_enabled', legacyKey: 'technical_signal_enabled', label: '技术信号变化', description: '监控标的出现入场候选、离场预警或趋势失效' },
  { menu: '监控标的', key: 'watch_target_major_event_enabled', legacyKey: 'major_event_enabled', label: '重大事件', description: '8-K、6-K、S-1/S-3、13D 等重大公告' },
  { menu: '监控标的', key: 'watch_target_insider_trading_enabled', legacyKey: 'insider_trading_enabled', label: '内幕交易', description: 'Form 3、4、5 及修订版本' },
  { menu: '小盘候选', key: 'candidate_earnings_preview_enabled', legacyKey: 'earnings_preview_enabled', label: '财报预告', description: '小盘候选的财报日期新增、变更或进入提醒窗口' },
  { menu: '小盘候选', key: 'candidate_earnings_release_enabled', legacyKey: 'earnings_release_enabled', label: '财报已发布', description: '小盘候选对应的 SEC 定期财报或业绩公告' },
  { menu: '小盘候选', key: 'candidate_technical_signal_enabled', legacyKey: 'technical_signal_enabled', label: '技术信号变化', description: '小盘候选出现入场候选、离场预警或趋势失效' },
  { menu: 'IPO 监控', key: 'ipo_progress_enabled', legacyKey: 'ipo_progress_enabled', label: '关注 IPO 进展', description: '仅已关注 IPO 公司出现新文件或关键状态、代码、交易所、定价变化时通知' },
] as const
type NotificationChannelKey = typeof notificationChannelRows[number]['key']

function notificationChannelEnabled(channel: 'in_app' | 'telegram', key: string) {
  const normalized = key as NotificationChannelKey
  return channel === 'in_app' ? inAppNotificationForm[normalized] : telegramNotificationForm[normalized]
}

function setNotificationChannelEnabled(channel: 'in_app' | 'telegram', key: string, value: boolean | string | number) {
  const normalized = key as NotificationChannelKey
  if (channel === 'in_app') {
    inAppNotificationForm[normalized] = Boolean(value)
    return
  }
  telegramNotificationForm[normalized] = Boolean(value)
}
const candidateNotificationForm = reactive({
  enabled: false,
  shadow_mode: false,
  notify_a: false,
  notify_b: false,
  send_time: '09:30',
  max_per_grade: 5,
  actionable_only: true,
  min_review_priority_score: 0
})
const tradeSetupNotificationForm = reactive({
  enabled: false,
  shadow_mode: false,
  notify_entry: true,
  notify_exit: true,
  notify_invalidated: true,
  max_per_run: 10
})
const schedulerForm = reactive({ timezone: 'UTC' })
const timezoneOptions = ['Asia/Shanghai', 'UTC', 'America/New_York', 'America/Los_Angeles']
const discoveryForm = reactive({
  price_provider: '',
  stooq_urls: '',
  longbridge_app_key: '',
  longbridge_app_secret: '',
  longbridge_access_token: '',
  longbridge_company_profile_enabled: true,
  longbridge_company_profile_request_budget: 20,
  longbridge_company_profile_ttl_days: 30,
  longbridge_analyst_rating_enabled: true,
  longbridge_analyst_rating_request_budget: 20,
  longbridge_analyst_rating_target_change_pct: 5,
  longbridge_candidate_research_enabled: true,
  longbridge_candidate_research_request_budget: 5,
	longbridge_watch_target_research_enabled: true,
	longbridge_watch_target_research_request_budget: 5,
  longbridge_candidate_valuation_enabled: true,
  longbridge_candidate_valuation_request_budget: 3,
	longbridge_watch_target_valuation_enabled: true,
	longbridge_watch_target_valuation_request_budget: 3,
	longbridge_option_research_enabled: true,
	longbridge_candidate_option_research_budget: 5,
	longbridge_watch_target_option_research_budget: 5,
  analyst_rating_notify_enabled: false,
  tiingo_api_token: '',
  tiingo_api_tokens: '',
  tiingo_request_budget: 45,
  tiingo_base_url: 'https://api.tiingo.com',
  twelve_data_api_key: '',
  twelve_data_request_budget: 700,
  twelve_data_request_interval_ms: 8000,
  twelve_data_base_url: 'https://api.twelvedata.com',
  yahoo_request_budget: 45,
  yahoo_base_url: 'https://query1.finance.yahoo.com',
  min_publish_coverage_pct: 85,
  auto_technical_history_warmup: true,
  task_timeout_minutes: 60,
  download_idle_timeout_seconds: 90,
  sec_bulk_cache_ttl_hours: 12,
  cache_retention_days: 14
})
const earningsPreviewForm = reactive({
  enabled: true,
  lookahead_days: 90,
  max_calendar_pages: 20,
  notify_enabled: false,
  reminder_days: '7,3,1,0'
})
const ipoForm = reactive({
  enabled: true,
  form_types: 'S-1,S-1/A,F-1,F-1/A,S-1MEF',
  lookback_days: 7,
  max_results: 100,
  notify_enabled: true,
  notify_form_types: '',
  keywords: '',
  longbridge_listing_verification_enabled: true,
  longbridge_listing_request_budget: 20,
  longbridge_listing_recheck_hours: 24,
  longbridge_calendar_enabled: true,
  longbridge_calendar_lookback_days: 14,
  longbridge_calendar_lookahead_days: 30,
  longbridge_calendar_max_pages: 5
})

const secRiskHints = computed(() => {
  const hints: Array<{ title: string, description: string, type: 'warning' | 'info' }> = []
  if (secForm.fetch_full_history) {
    hints.push({ title: t('pages.configs.fullHistoryTitle'), description: t('pages.configs.fullHistoryDescription'), type: 'warning' })
  }
  if (secForm.max_fetch_count === 0) {
    hints.push({ title: t('pages.configs.unlimitedMaxTitle'), description: t('pages.configs.unlimitedMaxDescription'), type: 'warning' })
  } else if (secForm.max_fetch_count >= 1000) {
    hints.push({ title: t('pages.configs.highMaxTitle'), description: t('pages.configs.highMaxDescription'), type: 'info' })
  }
  if (secForm.sync_window_days === 0) {
    hints.push({ title: t('pages.configs.unlimitedWindowTitle'), description: t('pages.configs.unlimitedWindowDescription'), type: 'warning' })
  } else if (secForm.sync_window_days > 365) {
    hints.push({ title: t('pages.configs.longWindowTitle'), description: t('pages.configs.longWindowDescription'), type: 'info' })
  }
  if (secForm.initial_fetch_days > 365) {
    hints.push({ title: t('pages.configs.longInitialTitle'), description: t('pages.configs.longInitialDescription'), type: 'info' })
  }
  return hints
})

const secPolicySummary = computed(() => {
  const syncWindowText = secForm.sync_window_days === 0 ? t('pages.configs.summarySyncUnlimited') : t('pages.configs.summarySyncDays', { days: secForm.sync_window_days })
  const initialWindowText = secForm.fetch_full_history ? t('pages.configs.summaryInitialFull') : t('pages.configs.summaryInitialDays', { days: secForm.initial_fetch_days })
  const maxText = secForm.max_fetch_count === 0 ? t('pages.configs.summaryMaxUnlimited') : t('pages.configs.summaryMaxCount', { count: secForm.max_fetch_count })
  return t('pages.configs.summarySecPolicy', { syncWindow: syncWindowText, initialWindow: initialWindowText, max: maxText })
})

const retentionPolicySummary = computed(() => {
  const storage = systemForm.storage_by_day ? t('pages.configs.summaryStorageByDay') : t('pages.configs.summaryContinuousDb')
  return t('pages.configs.summaryRetention', { days: systemForm.data_retention_days, storage })
})

const candidateNotificationSummary = computed(() => {
  if (!candidateNotificationForm.enabled) return t('status.disabled')
  const grades = [
    candidateNotificationForm.notify_a ? 'A' : '',
    candidateNotificationForm.notify_b ? 'B' : ''
  ].filter(Boolean).join('/')
  return t('pages.configs.candidateNotificationSummary', {
    grades: grades || '-',
    time: candidateNotificationForm.send_time,
    count: candidateNotificationForm.max_per_grade
  })
})

const discoveryDatasourceSummary = computed(() => {
  if (discoveryForm.price_provider === 'longbridge,tiingo,twelvedata,yahoo') return 'Longbridge → Tiingo → Twelve Data → Yahoo'
  if (discoveryForm.price_provider === 'longbridge') return 'Longbridge'
  if (discoveryForm.price_provider === 'tiingo') return 'Tiingo'
  if (discoveryForm.price_provider === 'tiingo,twelvedata,yahoo') return 'Tiingo → Twelve Data → Yahoo'
  if (discoveryForm.price_provider === 'tiingo,yahoo') return 'Tiingo → Yahoo'
  if (discoveryForm.price_provider === 'twelvedata') return 'Twelve Data'
  if (discoveryForm.price_provider === 'stooq,tiingo,yahoo') return 'Stooq → Tiingo → Yahoo'
  if (discoveryForm.price_provider === 'yahoo') return 'Yahoo'
  if (discoveryForm.price_provider === 'stooq') return 'Stooq'
  return t('pages.configs.discoveryProviderAuto')
})

const longbridgeProbeTitle = computed(() => {
  if (!longbridgeProbe.value) return ''
  return longbridgeProbe.value.status === 'ok'
    ? `行情连接成功：${longbridgeProbe.value.symbol}，耗时 ${longbridgeProbe.value.elapsed_millis}ms`
    : `行情连接失败：${longbridgeProbe.value.error_kind || 'unknown'}`
})

const longbridgeProbeDescription = computed(() => {
  if (!longbridgeProbe.value) return ''
  const result = longbridgeProbe.value
  const quote = result.quote_received ? `返回价格 ${result.last_done || '-'}，成交量 ${result.volume?.toLocaleString() || '0'}。` : ''
  return `${result.endpoint}；${result.message || '-'}${quote ? `；${quote}` : ''}`
})

const systemRiskHints = computed(() => {
  const hints: Array<{ title: string, description: string, type: 'warning' | 'info' }> = []
  if (systemForm.data_retention_days < 14) {
    hints.push({ title: t('pages.configs.shortRetentionTitle'), description: t('pages.configs.shortRetentionDescription'), type: 'warning' })
  }
  if (systemForm.storage_by_day) {
    hints.push({ title: t('pages.configs.byDayTitle'), description: t('pages.configs.byDayDescription'), type: 'info' })
  }
  return hints
})

function configValue(configs: SystemConfig[], key: string, fallback: string) {
  return configs.find((item) => item.config_key === key)?.config_value || fallback
}

function localeValue(value: string): Locale {
  return value === 'en-US' ? 'en-US' : 'zh-CN'
}

function localeLabel(value: Locale) {
  return value === 'en-US' ? 'English' : '中文'
}

function openProviderSettings() {
  dataAdvancedPanels.value = ['providers']
  window.requestAnimationFrame(() => {
    document.getElementById('provider-settings')?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  })
}

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<SystemConfig[]>>('/system-configs')
    const configs = res.data.data
    secForm.user_agent = configValue(configs, 'sec.user_agent', '')
    secForm.initial_fetch_days = Number(configValue(configs, 'sec.initial_fetch_days', '30'))
    secForm.sync_window_days = Number(configValue(configs, 'sec.sync_window_days', '30'))
    secForm.max_fetch_count = Number(configValue(configs, 'sec.max_fetch_count', '300'))
    secForm.fetch_full_history = configValue(configs, 'sec.fetch_full_history', 'false') === 'true'
    systemForm.data_retention_days = Number(configValue(configs, 'system.data_retention_days', '30'))
    systemForm.storage_by_day = configValue(configs, 'system.storage_by_day', 'false') === 'true'
		systemForm.backup_retention_days = Number(configValue(configs, 'system.backup_retention_days', '7'))
		systemForm.operation_history_retention_days = Number(configValue(configs, 'system.operation_history_retention_days', '90'))
    systemForm.backup_dir = configValue(configs, 'system.backup_dir', '')
    systemForm.storage_warning_pct = Number(configValue(configs, 'system.storage_warning_pct', '80'))
    uiForm.default_locale = localeValue(configValue(configs, 'ui.default_locale', 'zh-CN'))
    notificationForm.important_only = configValue(configs, 'notification.important_only', 'false') === 'true'
    notificationForm.filing_types = configValue(configs, 'notification.filing_types', '')
    notificationForm.keywords = configValue(configs, 'notification.keywords', '')
    notificationForm.quiet_hours_enabled = configValue(configs, 'notification.quiet_hours_enabled', 'false') === 'true'
    notificationForm.quiet_hours_start = configValue(configs, 'notification.quiet_hours_start', '22:00')
    notificationForm.quiet_hours_end = configValue(configs, 'notification.quiet_hours_end', '08:00')
    for (const row of notificationChannelRows) {
      inAppNotificationForm[row.key] = configValue(configs, `in_app_notification.${row.key}`, configValue(configs, `in_app_notification.${row.legacyKey}`, 'true')) === 'true'
      telegramNotificationForm[row.key] = configValue(configs, `telegram_notification.${row.key}`, configValue(configs, `telegram_notification.${row.legacyKey}`, 'true')) === 'true'
    }
    candidateNotificationForm.enabled = configValue(configs, 'candidate_notification.enabled', 'false') === 'true'
    candidateNotificationForm.shadow_mode = configValue(configs, 'candidate_notification.shadow_mode', 'false') === 'true'
    candidateNotificationForm.notify_a = configValue(configs, 'candidate_notification.notify_a', 'false') === 'true'
    candidateNotificationForm.notify_b = configValue(configs, 'candidate_notification.notify_b', 'false') === 'true'
    candidateNotificationForm.send_time = configValue(configs, 'candidate_notification.send_time', '09:30')
    candidateNotificationForm.max_per_grade = Number(configValue(configs, 'candidate_notification.max_per_grade', '5'))
    candidateNotificationForm.actionable_only = configValue(configs, 'candidate_notification.actionable_only', 'true') === 'true'
    candidateNotificationForm.min_review_priority_score = Number(configValue(configs, 'candidate_notification.min_review_priority_score', '0'))
		tradeSetupNotificationForm.enabled = configValue(configs, 'trade_setup_notification.enabled', 'false') === 'true'
		tradeSetupNotificationForm.shadow_mode = configValue(configs, 'trade_setup_notification.shadow_mode', 'false') === 'true'
		tradeSetupNotificationForm.notify_entry = configValue(configs, 'trade_setup_notification.notify_entry', 'true') === 'true'
		tradeSetupNotificationForm.notify_exit = configValue(configs, 'trade_setup_notification.notify_exit', 'true') === 'true'
		tradeSetupNotificationForm.notify_invalidated = configValue(configs, 'trade_setup_notification.notify_invalidated', 'true') === 'true'
		tradeSetupNotificationForm.max_per_run = Number(configValue(configs, 'trade_setup_notification.max_per_run', '10'))
    schedulerForm.timezone = configValue(configs, 'scheduler.timezone', 'UTC')
    discoveryForm.price_provider = configValue(configs, 'discovery.price_provider', '')
    discoveryForm.stooq_urls = configValue(configs, 'discovery.stooq_urls', '')
    discoveryForm.longbridge_app_key = configValue(configs, 'discovery.longbridge_app_key', '')
    discoveryForm.longbridge_app_secret = configValue(configs, 'discovery.longbridge_app_secret', '')
    discoveryForm.longbridge_access_token = configValue(configs, 'discovery.longbridge_access_token', '')
    discoveryForm.longbridge_company_profile_enabled = configValue(configs, 'discovery.longbridge_company_profile_enabled', 'true') === 'true'
    discoveryForm.longbridge_company_profile_request_budget = Number(configValue(configs, 'discovery.longbridge_company_profile_request_budget', '20'))
    discoveryForm.longbridge_company_profile_ttl_days = Number(configValue(configs, 'discovery.longbridge_company_profile_ttl_days', '30'))
    discoveryForm.longbridge_analyst_rating_enabled = configValue(configs, 'discovery.longbridge_analyst_rating_enabled', 'true') === 'true'
    discoveryForm.longbridge_analyst_rating_request_budget = Number(configValue(configs, 'discovery.longbridge_analyst_rating_request_budget', '20'))
    discoveryForm.longbridge_analyst_rating_target_change_pct = Number(configValue(configs, 'discovery.longbridge_analyst_rating_target_change_pct', '5'))
    discoveryForm.longbridge_candidate_research_enabled = configValue(configs, 'discovery.longbridge_candidate_research_enabled', 'true') === 'true'
    discoveryForm.longbridge_candidate_research_request_budget = Number(configValue(configs, 'discovery.longbridge_candidate_research_request_budget', '5'))
		discoveryForm.longbridge_watch_target_research_enabled = configValue(configs, 'discovery.longbridge_watch_target_research_enabled', 'true') === 'true'
		discoveryForm.longbridge_watch_target_research_request_budget = Number(configValue(configs, 'discovery.longbridge_watch_target_research_request_budget', '5'))
    discoveryForm.longbridge_candidate_valuation_enabled = configValue(configs, 'discovery.longbridge_candidate_valuation_enabled', 'true') === 'true'
    discoveryForm.longbridge_candidate_valuation_request_budget = Number(configValue(configs, 'discovery.longbridge_candidate_valuation_request_budget', '3'))
		discoveryForm.longbridge_watch_target_valuation_enabled = configValue(configs, 'discovery.longbridge_watch_target_valuation_enabled', 'true') === 'true'
		discoveryForm.longbridge_watch_target_valuation_request_budget = Number(configValue(configs, 'discovery.longbridge_watch_target_valuation_request_budget', '3'))
		discoveryForm.longbridge_option_research_enabled = configValue(configs, 'discovery.longbridge_option_research_enabled', 'true') === 'true'
		discoveryForm.longbridge_candidate_option_research_budget = Number(configValue(configs, 'discovery.longbridge_candidate_option_research_budget', '5'))
		discoveryForm.longbridge_watch_target_option_research_budget = Number(configValue(configs, 'discovery.longbridge_watch_target_option_research_budget', '5'))
    discoveryForm.analyst_rating_notify_enabled = configValue(configs, 'analyst_rating.notify_enabled', 'false') === 'true'
    discoveryForm.tiingo_api_token = configValue(configs, 'discovery.tiingo_api_token', '')
    discoveryForm.tiingo_api_tokens = configValue(configs, 'discovery.tiingo_api_tokens', '')
    discoveryForm.tiingo_request_budget = Number(configValue(configs, 'discovery.tiingo_request_budget', '45'))
    discoveryForm.tiingo_base_url = configValue(configs, 'discovery.tiingo_base_url', 'https://api.tiingo.com')
    discoveryForm.twelve_data_api_key = configValue(configs, 'discovery.twelve_data_api_key', '')
    discoveryForm.twelve_data_request_budget = Number(configValue(configs, 'discovery.twelve_data_request_budget', '700'))
    discoveryForm.twelve_data_request_interval_ms = Number(configValue(configs, 'discovery.twelve_data_request_interval_ms', '8000'))
    discoveryForm.twelve_data_base_url = configValue(configs, 'discovery.twelve_data_base_url', 'https://api.twelvedata.com')
    discoveryForm.yahoo_request_budget = Number(configValue(configs, 'discovery.yahoo_request_budget', '45'))
    discoveryForm.yahoo_base_url = configValue(configs, 'discovery.yahoo_base_url', 'https://query1.finance.yahoo.com')
    discoveryForm.min_publish_coverage_pct = Number(configValue(configs, 'discovery.min_publish_coverage_pct', '85'))
    discoveryForm.auto_technical_history_warmup = configValue(configs, 'discovery.auto_technical_history_warmup', 'true') === 'true'
    discoveryForm.task_timeout_minutes = Number(configValue(configs, 'discovery.task_timeout_minutes', '60'))
    discoveryForm.download_idle_timeout_seconds = Number(configValue(configs, 'discovery.download_idle_timeout_seconds', '90'))
    discoveryForm.sec_bulk_cache_ttl_hours = Number(configValue(configs, 'discovery.sec_bulk_cache_ttl_hours', '12'))
    discoveryForm.cache_retention_days = Number(configValue(configs, 'discovery.cache_retention_days', '14'))
    earningsPreviewForm.enabled = configValue(configs, 'earnings_preview.enabled', 'true') === 'true'
    earningsPreviewForm.lookahead_days = Number(configValue(configs, 'earnings_preview.lookahead_days', '90'))
    earningsPreviewForm.max_calendar_pages = Number(configValue(configs, 'earnings_preview.max_calendar_pages', '20'))
    earningsPreviewForm.notify_enabled = configValue(configs, 'earnings_preview.notify_enabled', 'false') === 'true'
    earningsPreviewForm.reminder_days = configValue(configs, 'earnings_preview.reminder_days', '7,3,1,0')
    ipoForm.enabled = configValue(configs, 'ipo.enabled', 'true') === 'true'
    ipoForm.form_types = configValue(configs, 'ipo.form_types', 'S-1,S-1/A,F-1,F-1/A,S-1MEF')
    ipoForm.lookback_days = Number(configValue(configs, 'ipo.lookback_days', '7'))
    ipoForm.max_results = Number(configValue(configs, 'ipo.max_results', '100'))
    ipoForm.notify_enabled = configValue(configs, 'ipo.notify_enabled', 'true') === 'true'
    ipoForm.notify_form_types = configValue(configs, 'ipo.notify_form_types', '')
    ipoForm.keywords = configValue(configs, 'ipo.keywords', '')
    ipoForm.longbridge_listing_verification_enabled = configValue(configs, 'ipo.longbridge_listing_verification_enabled', 'true') === 'true'
    ipoForm.longbridge_listing_request_budget = Number(configValue(configs, 'ipo.longbridge_listing_request_budget', '20'))
    ipoForm.longbridge_listing_recheck_hours = Number(configValue(configs, 'ipo.longbridge_listing_recheck_hours', '24'))
    ipoForm.longbridge_calendar_enabled = configValue(configs, 'ipo.longbridge_calendar_enabled', 'true') === 'true'
    ipoForm.longbridge_calendar_lookback_days = Number(configValue(configs, 'ipo.longbridge_calendar_lookback_days', '14'))
    ipoForm.longbridge_calendar_lookahead_days = Number(configValue(configs, 'ipo.longbridge_calendar_lookahead_days', '30'))
    ipoForm.longbridge_calendar_max_pages = Number(configValue(configs, 'ipo.longbridge_calendar_max_pages', '5'))
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    await apiClient.put('/system-configs', [
      { key: 'sec.user_agent', value: secForm.user_agent, value_type: 'string', category: 'sec', encrypted: false },
      { key: 'sec.initial_fetch_days', value: String(secForm.initial_fetch_days), value_type: 'int', category: 'sec', encrypted: false },
      { key: 'sec.sync_window_days', value: String(secForm.sync_window_days), value_type: 'int', category: 'sec', encrypted: false },
      { key: 'sec.max_fetch_count', value: String(secForm.max_fetch_count), value_type: 'int', category: 'sec', encrypted: false },
      { key: 'sec.fetch_full_history', value: String(secForm.fetch_full_history), value_type: 'bool', category: 'sec', encrypted: false },
      { key: 'system.data_retention_days', value: String(systemForm.data_retention_days), value_type: 'int', category: 'system', encrypted: false },
      { key: 'system.storage_by_day', value: String(systemForm.storage_by_day), value_type: 'bool', category: 'system', encrypted: false },
      { key: 'system.backup_retention_days', value: String(systemForm.backup_retention_days), value_type: 'int', category: 'system', encrypted: false },
			{ key: 'system.operation_history_retention_days', value: String(systemForm.operation_history_retention_days), value_type: 'int', category: 'system', encrypted: false },
      { key: 'system.backup_dir', value: systemForm.backup_dir, value_type: 'string', category: 'system', encrypted: false },
      { key: 'system.storage_warning_pct', value: String(systemForm.storage_warning_pct), value_type: 'int', category: 'system', encrypted: false },
      { key: 'ui.default_locale', value: uiForm.default_locale, value_type: 'string', category: 'ui', encrypted: false },
      { key: 'notification.important_only', value: String(notificationForm.important_only), value_type: 'bool', category: 'notification', encrypted: false },
      { key: 'notification.filing_types', value: notificationForm.filing_types, value_type: 'string', category: 'notification', encrypted: false },
      { key: 'notification.keywords', value: notificationForm.keywords, value_type: 'string', category: 'notification', encrypted: false },
      { key: 'notification.quiet_hours_enabled', value: String(notificationForm.quiet_hours_enabled), value_type: 'bool', category: 'notification', encrypted: false },
      { key: 'notification.quiet_hours_start', value: notificationForm.quiet_hours_start, value_type: 'string', category: 'notification', encrypted: false },
      { key: 'notification.quiet_hours_end', value: notificationForm.quiet_hours_end, value_type: 'string', category: 'notification', encrypted: false },
      ...notificationChannelRows.map((row) => ({ key: `in_app_notification.${row.key}`, value: String(inAppNotificationForm[row.key]), value_type: 'bool', category: 'in_app_notification', encrypted: false })),
      ...notificationChannelRows.map((row) => ({ key: `telegram_notification.${row.key}`, value: String(telegramNotificationForm[row.key]), value_type: 'bool', category: 'telegram_notification', encrypted: false })),
      { key: 'candidate_notification.enabled', value: String(candidateNotificationForm.enabled), value_type: 'bool', category: 'candidate_notification', encrypted: false },
      { key: 'candidate_notification.shadow_mode', value: String(candidateNotificationForm.shadow_mode), value_type: 'bool', category: 'candidate_notification', encrypted: false },
      { key: 'candidate_notification.notify_a', value: String(candidateNotificationForm.notify_a), value_type: 'bool', category: 'candidate_notification', encrypted: false },
      { key: 'candidate_notification.notify_b', value: String(candidateNotificationForm.notify_b), value_type: 'bool', category: 'candidate_notification', encrypted: false },
      { key: 'candidate_notification.send_time', value: candidateNotificationForm.send_time, value_type: 'string', category: 'candidate_notification', encrypted: false },
      { key: 'candidate_notification.max_per_grade', value: String(candidateNotificationForm.max_per_grade), value_type: 'int', category: 'candidate_notification', encrypted: false },
      { key: 'candidate_notification.actionable_only', value: String(candidateNotificationForm.actionable_only), value_type: 'bool', category: 'candidate_notification', encrypted: false },
      { key: 'candidate_notification.min_review_priority_score', value: String(candidateNotificationForm.min_review_priority_score), value_type: 'int', category: 'candidate_notification', encrypted: false },
			{ key: 'trade_setup_notification.enabled', value: String(tradeSetupNotificationForm.enabled), value_type: 'bool', category: 'trade_setup_notification', encrypted: false },
			{ key: 'trade_setup_notification.shadow_mode', value: String(tradeSetupNotificationForm.shadow_mode), value_type: 'bool', category: 'trade_setup_notification', encrypted: false },
			{ key: 'trade_setup_notification.notify_entry', value: String(tradeSetupNotificationForm.notify_entry), value_type: 'bool', category: 'trade_setup_notification', encrypted: false },
			{ key: 'trade_setup_notification.notify_exit', value: String(tradeSetupNotificationForm.notify_exit), value_type: 'bool', category: 'trade_setup_notification', encrypted: false },
			{ key: 'trade_setup_notification.notify_invalidated', value: String(tradeSetupNotificationForm.notify_invalidated), value_type: 'bool', category: 'trade_setup_notification', encrypted: false },
			{ key: 'trade_setup_notification.max_per_run', value: String(tradeSetupNotificationForm.max_per_run), value_type: 'int', category: 'trade_setup_notification', encrypted: false },
      { key: 'scheduler.timezone', value: schedulerForm.timezone, value_type: 'string', category: 'scheduler', encrypted: false },
      { key: 'discovery.price_provider', value: discoveryForm.price_provider, value_type: 'string', category: 'discovery', encrypted: false },
      { key: 'discovery.stooq_urls', value: discoveryForm.stooq_urls, value_type: 'string', category: 'discovery', encrypted: false },
      { key: 'discovery.longbridge_app_key', value: discoveryForm.longbridge_app_key, value_type: 'string', category: 'discovery', encrypted: true },
      { key: 'discovery.longbridge_app_secret', value: discoveryForm.longbridge_app_secret, value_type: 'string', category: 'discovery', encrypted: true },
      { key: 'discovery.longbridge_access_token', value: discoveryForm.longbridge_access_token, value_type: 'string', category: 'discovery', encrypted: true },
      { key: 'discovery.longbridge_company_profile_enabled', value: String(discoveryForm.longbridge_company_profile_enabled), value_type: 'bool', category: 'discovery', encrypted: false },
      { key: 'discovery.longbridge_company_profile_request_budget', value: String(discoveryForm.longbridge_company_profile_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.longbridge_company_profile_ttl_days', value: String(discoveryForm.longbridge_company_profile_ttl_days), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.longbridge_analyst_rating_enabled', value: String(discoveryForm.longbridge_analyst_rating_enabled), value_type: 'bool', category: 'discovery', encrypted: false },
      { key: 'discovery.longbridge_analyst_rating_request_budget', value: String(discoveryForm.longbridge_analyst_rating_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.longbridge_analyst_rating_target_change_pct', value: String(discoveryForm.longbridge_analyst_rating_target_change_pct), value_type: 'float', category: 'discovery', encrypted: false },
      { key: 'discovery.longbridge_candidate_research_enabled', value: String(discoveryForm.longbridge_candidate_research_enabled), value_type: 'bool', category: 'discovery', encrypted: false },
      { key: 'discovery.longbridge_candidate_research_request_budget', value: String(discoveryForm.longbridge_candidate_research_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
		  { key: 'discovery.longbridge_watch_target_research_enabled', value: String(discoveryForm.longbridge_watch_target_research_enabled), value_type: 'bool', category: 'discovery', encrypted: false },
		  { key: 'discovery.longbridge_watch_target_research_request_budget', value: String(discoveryForm.longbridge_watch_target_research_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.longbridge_candidate_valuation_enabled', value: String(discoveryForm.longbridge_candidate_valuation_enabled), value_type: 'bool', category: 'discovery', encrypted: false },
      { key: 'discovery.longbridge_candidate_valuation_request_budget', value: String(discoveryForm.longbridge_candidate_valuation_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
		  { key: 'discovery.longbridge_watch_target_valuation_enabled', value: String(discoveryForm.longbridge_watch_target_valuation_enabled), value_type: 'bool', category: 'discovery', encrypted: false },
		  { key: 'discovery.longbridge_watch_target_valuation_request_budget', value: String(discoveryForm.longbridge_watch_target_valuation_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
		  { key: 'discovery.longbridge_option_research_enabled', value: String(discoveryForm.longbridge_option_research_enabled), value_type: 'bool', category: 'discovery', encrypted: false },
		  { key: 'discovery.longbridge_candidate_option_research_budget', value: String(discoveryForm.longbridge_candidate_option_research_budget), value_type: 'int', category: 'discovery', encrypted: false },
		  { key: 'discovery.longbridge_watch_target_option_research_budget', value: String(discoveryForm.longbridge_watch_target_option_research_budget), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'analyst_rating.notify_enabled', value: String(discoveryForm.analyst_rating_notify_enabled), value_type: 'bool', category: 'analyst_rating', encrypted: false },
      { key: 'discovery.tiingo_api_token', value: discoveryForm.tiingo_api_token, value_type: 'string', category: 'discovery', encrypted: true },
      { key: 'discovery.tiingo_api_tokens', value: discoveryForm.tiingo_api_tokens, value_type: 'string', category: 'discovery', encrypted: true },
      { key: 'discovery.tiingo_request_budget', value: String(discoveryForm.tiingo_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.tiingo_base_url', value: discoveryForm.tiingo_base_url, value_type: 'string', category: 'discovery', encrypted: false },
      { key: 'discovery.twelve_data_api_key', value: discoveryForm.twelve_data_api_key, value_type: 'string', category: 'discovery', encrypted: true },
      { key: 'discovery.twelve_data_request_budget', value: String(discoveryForm.twelve_data_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.twelve_data_request_interval_ms', value: String(discoveryForm.twelve_data_request_interval_ms), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.twelve_data_base_url', value: discoveryForm.twelve_data_base_url, value_type: 'string', category: 'discovery', encrypted: false },
      { key: 'discovery.yahoo_request_budget', value: String(discoveryForm.yahoo_request_budget), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.yahoo_base_url', value: discoveryForm.yahoo_base_url, value_type: 'string', category: 'discovery', encrypted: false },
      { key: 'discovery.min_publish_coverage_pct', value: String(discoveryForm.min_publish_coverage_pct), value_type: 'float', category: 'discovery', encrypted: false },
      { key: 'discovery.auto_technical_history_warmup', value: String(discoveryForm.auto_technical_history_warmup), value_type: 'bool', category: 'discovery', encrypted: false },
      { key: 'discovery.task_timeout_minutes', value: String(discoveryForm.task_timeout_minutes), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.download_idle_timeout_seconds', value: String(discoveryForm.download_idle_timeout_seconds), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.sec_bulk_cache_ttl_hours', value: String(discoveryForm.sec_bulk_cache_ttl_hours), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'discovery.cache_retention_days', value: String(discoveryForm.cache_retention_days), value_type: 'int', category: 'discovery', encrypted: false },
      { key: 'earnings_preview.enabled', value: String(earningsPreviewForm.enabled), value_type: 'bool', category: 'earnings_preview', encrypted: false },
      { key: 'earnings_preview.lookahead_days', value: String(earningsPreviewForm.lookahead_days), value_type: 'int', category: 'earnings_preview', encrypted: false },
      { key: 'earnings_preview.max_calendar_pages', value: String(earningsPreviewForm.max_calendar_pages), value_type: 'int', category: 'earnings_preview', encrypted: false },
      { key: 'earnings_preview.notify_enabled', value: String(earningsPreviewForm.notify_enabled), value_type: 'bool', category: 'earnings_preview', encrypted: false },
      { key: 'earnings_preview.reminder_days', value: earningsPreviewForm.reminder_days, value_type: 'string', category: 'earnings_preview', encrypted: false },
      { key: 'ipo.enabled', value: String(ipoForm.enabled), value_type: 'bool', category: 'ipo', encrypted: false },
      { key: 'ipo.form_types', value: ipoForm.form_types, value_type: 'string', category: 'ipo', encrypted: false },
      { key: 'ipo.lookback_days', value: String(ipoForm.lookback_days), value_type: 'int', category: 'ipo', encrypted: false },
      { key: 'ipo.max_results', value: String(ipoForm.max_results), value_type: 'int', category: 'ipo', encrypted: false },
      { key: 'ipo.notify_enabled', value: String(ipoForm.notify_enabled), value_type: 'bool', category: 'ipo', encrypted: false },
      { key: 'ipo.notify_form_types', value: ipoForm.notify_form_types, value_type: 'string', category: 'ipo', encrypted: false },
      { key: 'ipo.keywords', value: ipoForm.keywords, value_type: 'string', category: 'ipo', encrypted: false },
      { key: 'ipo.longbridge_listing_verification_enabled', value: String(ipoForm.longbridge_listing_verification_enabled), value_type: 'bool', category: 'ipo', encrypted: false },
      { key: 'ipo.longbridge_listing_request_budget', value: String(ipoForm.longbridge_listing_request_budget), value_type: 'int', category: 'ipo', encrypted: false },
      { key: 'ipo.longbridge_listing_recheck_hours', value: String(ipoForm.longbridge_listing_recheck_hours), value_type: 'int', category: 'ipo', encrypted: false },
      { key: 'ipo.longbridge_calendar_enabled', value: String(ipoForm.longbridge_calendar_enabled), value_type: 'bool', category: 'ipo', encrypted: false },
      { key: 'ipo.longbridge_calendar_lookback_days', value: String(ipoForm.longbridge_calendar_lookback_days), value_type: 'int', category: 'ipo', encrypted: false },
      { key: 'ipo.longbridge_calendar_lookahead_days', value: String(ipoForm.longbridge_calendar_lookahead_days), value_type: 'int', category: 'ipo', encrypted: false },
      { key: 'ipo.longbridge_calendar_max_pages', value: String(ipoForm.longbridge_calendar_max_pages), value_type: 'int', category: 'ipo', encrypted: false }
    ])
    store.applyDefaultLocale(uiForm.default_locale)
    ElMessage.success(t('messages.configSaved'))
    cleanupPreview.value = null
    await load()
  } finally {
    saving.value = false
  }
}

async function probeLongbridgeQuote() {
  longbridgeProbeLoading.value = true
  try {
    const res = await apiClient.post<ApiResponse<LongbridgeQuoteProbeResult>>(
      '/discovery/providers/longbridge/probe',
      null,
      { timeout: 30_000 },
    )
    longbridgeProbe.value = res.data.data
    if (res.data.data.status === 'ok') ElMessage.success('Longbridge 行情连接正常')
    else ElMessage.error(res.data.data.message || 'Longbridge 行情连接失败')
  } catch (err: any) {
    const message = err?.response?.data?.message || 'Longbridge 行情连接测试失败'
    longbridgeProbe.value = { provider: 'longbridge', endpoint: 'wss://openapi-quote.longbridge.com', symbol: 'AAPL.US', status: 'failed', error_kind: 'request', message, elapsed_millis: 0, quote_received: false }
    ElMessage.error(message)
  } finally {
    longbridgeProbeLoading.value = false
  }
}

function download(url: string) {
  window.location.href = url
}

async function loadCleanupPreview() {
  previewing.value = true
  try {
    await save()
    const res = await apiClient.get<ApiResponse<CleanupPreview>>('/filings/cleanup-preview')
    cleanupPreview.value = res.data.data
  } finally {
    previewing.value = false
  }
}

async function cleanup() {
  if (!cleanupPreview.value) return
  await ElMessageBox.confirm(t('messages.confirmCleanup', { count: cleanupPreview.value.delete_count }), t('messages.cleanupTitle'), { type: 'warning' })
  cleaning.value = true
  try {
    const res = await apiClient.post<ApiResponse<{ deleted: number }>>('/filings/cleanup')
    ElMessage.success(t('messages.deletedFilings', { count: res.data.data.deleted }))
    await loadCleanupPreview()
  } finally {
    cleaning.value = false
  }
}

async function loadLifecycleCleanupPreview() {
	previewingLifecycle.value = true
	try {
		await save()
		const res = await apiClient.get<ApiResponse<LifecycleCleanupPreview>>('/system/lifecycle-cleanup-preview')
		lifecycleCleanupPreview.value = res.data.data
	} finally {
		previewingLifecycle.value = false
	}
}

async function cleanupLifecycle() {
	if (!lifecycleCleanupPreview.value) return
	await ElMessageBox.confirm(t('messages.confirmCleanup', { count: lifecycleCleanupPreview.value.total }), t('messages.cleanupTitle'), { type: 'warning' })
	cleaningLifecycle.value = true
	try {
		const res = await apiClient.post<ApiResponse<LifecycleCleanupPreview>>('/system/lifecycle-cleanup')
		ElMessage.success(t('messages.deletedFilings', { count: res.data.data.total }))
		await loadLifecycleCleanupPreview()
	} finally {
		cleaningLifecycle.value = false
	}
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

onMounted(load)
</script>

<style scoped>
.config-category-bar {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 18px;
  padding: 14px 16px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
  background: var(--el-fill-color-blank);
}

.config-category-hint,
.config-section-heading p,
.collapse-help {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.config-section {
  display: grid;
  gap: 16px;
}

.config-section-heading h2 {
  margin: 0 0 4px;
  font-size: 18px;
}

.config-section-heading p {
  margin: 0;
}

.config-advanced-collapse {
  margin-top: 8px;
}

:deep(.research-settings-form .el-form-item__label) {
  height: auto;
  min-height: 32px;
  line-height: 22px;
  white-space: normal;
  overflow: visible;
  text-overflow: clip;
}

:deep(.research-settings-form .el-form-item__content) {
  min-height: 32px;
}

.collapse-help {
  margin: 0 0 16px;
}

@media (max-width: 900px) {
  .config-category-bar {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
