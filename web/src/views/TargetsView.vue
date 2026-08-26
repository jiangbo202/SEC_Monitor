<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.targets.title') }}</h1>
      <el-space>
        <el-button :loading="simulationLoading" @click="openTradePlanSimulations">交易模拟复盘</el-button>
        <el-button type="primary" @click="openCreate">{{ t('pages.targets.add') }}</el-button>
      </el-space>
    </div>
    <el-form :inline="true" :model="filters" class="toolbar">
      <el-form-item label="Ticker"><el-input v-model="filters.ticker" clearable /></el-form-item>
      <el-form-item :label="t('common.targetGroup')"><el-input v-model="filters.group" clearable style="width: 150px" /></el-form-item>
      <el-form-item :label="t('common.status')">
        <el-select fit-input-width v-model="filters.status" clearable style="width: 140px">
          <el-option :label="t('common.enabled')" value="enabled" />
          <el-option :label="t('common.disabled')" value="disabled" />
        </el-select>
      </el-form-item>
      <el-form-item>
        <el-button :type="filters.upcoming_earnings ? 'primary' : 'default'" plain @click="toggleUpcomingEarnings">
          即将财报 <el-badge :value="upcomingEarningsCount" :hidden="upcomingEarningsCount === 0" class="quick-filter-badge" />
        </el-button>
      </el-form-item>
      <el-form-item><el-button :loading="loading" @click="load">{{ t('common.query') }}</el-button></el-form-item>
    </el-form>
    <el-table class="target-list-table" :data="rows" v-loading="loading" border size="small" :empty-text="t('pages.targets.empty')">
      <el-table-column label="标的" width="210">
        <template #default="{ row }">
          <div class="target-identity">
            <el-link class="target-ticker" type="primary" @click="openDetail(row)">{{ row.ticker }}</el-link>
            <span class="target-company" :title="row.company_name">{{ row.company_name || '-' }}</span>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="group" :label="t('common.targetGroup')" width="110">
        <template #default="{ row }">
          <el-tag v-if="row.group" size="small" effect="plain">{{ row.group }}</el-tag>
          <span v-else>-</span>
        </template>
      </el-table-column>
      <el-table-column label="监控状态" width="150">
        <template #default="{ row }">
          <div class="target-status-cell">
            <el-switch
              size="small"
              :model-value="row.status === 'enabled'"
              inline-prompt
              :active-text="t('pages.targets.enableShort')"
              :inactive-text="t('pages.targets.disableShort')"
              @change="(value: boolean) => setTargetEnabled(row, value)"
            />
            <el-tooltip :disabled="!row.last_sync_error" :content="row.last_sync_error || ''" placement="top" effect="dark">
              <el-tag size="small" class="status-tag" :type="syncStatusType(row.last_sync_status)" effect="plain">{{ syncStatusLabel(row.last_sync_status) }}</el-tag>
            </el-tooltip>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="技术信号" width="200">
        <template #default="{ row }">
          <el-tooltip placement="top" effect="dark">
            <template #content>
              <template v-if="row.technical?.status === 'ready'">
                <div>价格日期：{{ row.technical.trade_date || '-' }}</div>
                <div>收盘价：{{ formatPrice(row.technical.close_usd) }}</div>
                <div>MA20：{{ formatPrice(row.technical.ma20_usd) }}（{{ formatSignedPct(row.technical.distance_to_ma20_pct) }}）</div>
                <div>前 20 日最高收盘价：{{ formatPrice(row.technical.prior_20d_high_usd) }}（{{ formatSignedPct(row.technical.distance_to_20d_high_pct) }}）</div>
                <div>距 50 / 200 日高点：{{ formatSignedPct(row.technical.distance_to_50d_high_pct) }} / {{ formatSignedPct(row.technical.distance_to_200d_high_pct) }}</div>
                <div>当日估算成交额：{{ formatNotional(row.technical.dollar_volume_usd) }}</div>
                <div>20 日均成交额：{{ formatNotional(row.technical.average_dollar_volume_20) }}（{{ formatRatio(row.technical.dollar_volume_ratio_20) }}）</div>
                <div>流动性：{{ liquidityLabel(row.technical.liquidity_status) }}</div>
                <div>全部技术信号：{{ technicalSignalsTooltip(row.technical) }}</div>
              </template>
              <span v-else>{{ targetTechnicalStatusDescription(row) }}</span>
            </template>
            <div class="target-signals">
              <el-tag v-if="row.technical?.status === 'ready'" size="small" :type="liquidityTagType(row.technical.liquidity_status)" effect="plain">流动性{{ liquidityShortLabel(row.technical.liquidity_status) }}</el-tag>
              <el-tag v-for="signal in (row.technical?.signals || []).slice(0, 1)" :key="signal.kind" class="target-primary-signal" size="small" type="success" effect="plain">{{ signal.label }}</el-tag>
              <el-tag v-if="!(row.technical?.signals || []).length" size="small" :type="row.technical?.status === 'ready' ? 'info' : 'warning'" effect="plain">
                {{ row.technical?.status === 'ready' ? '暂无突破' : targetTechnicalStatusLabel(row) }}
              </el-tag>
              <span v-if="(row.technical?.signals || []).length > 1" class="target-signal-more" aria-label="还有更多技术信号">••</span>
            </div>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="交易计划" width="110">
        <template #default="{ row }">
          <el-tooltip :content="tradeSetupSummary(row.technical)" placement="top">
            <el-tag size="small" :type="tradeSetupTagType(row.technical?.trade_setup?.status)" effect="plain">
              {{ tradeSetupLabel(row.technical?.trade_setup?.status) }}
            </el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="财报预告" width="120">
        <template #default="{ row }">
          <el-tooltip :content="earningsPreviewTooltip(earningsPreviewFor(row))" placement="top" effect="dark">
            <el-tag size="small" :type="earningsPreviewTagType(earningsPreviewFor(row))" effect="plain">
              {{ earningsPreviewLabel(earningsPreviewFor(row)) }}
            </el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="last_sync_at" :label="t('pages.targets.lastSync')" width="135">
        <template #default="{ row }"><span class="target-sync-time">{{ formatCompactDateTime(row.last_sync_at) }}</span></template>
      </el-table-column>
      <el-table-column :label="t('common.actions')" width="125" fixed="right">
        <template #default="{ row }">
          <el-button size="small" type="primary" :loading="syncingId === row.id" @click="syncTarget(row)">{{ t('common.sync') }}</el-button>
          <el-dropdown trigger="click" @command="(command: string) => handleTargetCommand(command, row)">
            <el-button size="small" :icon="MoreFilled" />
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="detail">{{ t('common.details') }}</el-dropdown-item>
                <el-dropdown-item command="edit">{{ t('common.edit') }}</el-dropdown-item>
                <el-dropdown-item command="delete" divided>{{ t('common.delete') }}</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination class="pagination" layout="total, prev, pager, next" :total="total" :page-size="pageSize" v-model:current-page="page" @current-change="load" />

    <el-dialog v-model="dialogVisible" :title="editingId ? t('pages.targets.edit') : t('pages.targets.add')" width="520px">
      <el-form :model="form" label-width="110px">
        <el-form-item label="Ticker">
          <el-input v-model="form.ticker" placeholder="TSLA" @input="invalidateFundIdentity" @blur="lookupTicker">
            <template #append>
              <el-button :loading="lookingUp" @click="lookupTicker">{{ t('pages.targets.lookup') }}</el-button>
            </template>
          </el-input>
        </el-form-item>
        <el-form-item :label="t('common.companyName')"><el-input v-model="form.company_name" /></el-form-item>
        <el-form-item label="CIK"><el-input v-model="form.cik" @input="markManualFundIdentity" /></el-form-item>
        <el-form-item :label="t('common.type')">
          <el-select fit-input-width v-model="form.target_type" @change="handleTargetTypeChange">
            <el-option label="Stock" value="stock" />
            <el-option label="ETF" value="etf" />
          </el-select>
        </el-form-item>
        <template v-if="form.target_type === 'etf'">
          <el-form-item v-if="fundCandidates.length" :label="t('pages.targets.fundCandidate')">
            <el-select fit-input-width v-model="selectedFundCandidateKey" :placeholder="t('pages.targets.fundCandidatePlaceholder')" @change="selectFundCandidate">
              <el-option v-for="candidate in fundCandidates" :key="fundCandidateKey(candidate)" :label="fundCandidateLabel(candidate)" :value="fundCandidateKey(candidate)" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('pages.targets.fundSeriesId')">
            <el-input v-model="form.fund_series_id" placeholder="S000102337" @input="markManualFundIdentity" />
          </el-form-item>
          <el-form-item :label="t('pages.targets.fundClassId')">
            <el-input v-model="form.fund_class_id" placeholder="C000272806" @input="markManualFundIdentity" />
          </el-form-item>
          <el-form-item :label="t('pages.targets.identitySource')">
            <el-input v-model="form.identity_source" readonly :placeholder="t('pages.targets.identitySourceManual')" />
          </el-form-item>
          <el-form-item label-width="0">
            <el-alert
              :title="formHasExactFundIdentity ? t('pages.targets.fundIdentityExact') : t('pages.targets.fundIdentityUnresolved')"
              :description="fundIdentityFormDescription"
              :type="formHasExactFundIdentity ? 'success' : 'warning'"
              :closable="false"
              show-icon
            />
          </el-form-item>
        </template>
        <el-form-item :label="t('common.targetGroup')">
          <el-input v-model="form.group" :placeholder="t('pages.targets.groupPlaceholder')" />
        </el-form-item>
        <el-form-item :label="t('common.status')">
          <el-select fit-input-width v-model="form.status">
            <el-option :label="t('common.enabled')" value="enabled" />
            <el-option :label="t('common.disabled')" value="disabled" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" :disabled="saveDisabled" @click="save">{{ t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="simulationVisible" title="交易计划模拟复盘" width="1120px" top="6vh">
      <el-alert
        :title="simulationReport?.execution_convention || '日线模拟结果'"
        type="info"
        :closable="false"
        show-icon
        class="summary-alert"
      />
      <div class="target-detail-actions">
        <el-button type="primary" :loading="simulationRebuilding" @click="rebuildTradePlanSimulations">重建日线模拟</el-button>
        <span v-if="simulationReport" class="muted">规则版本 {{ simulationReport.rule_version }} · 生成于 {{ formatDateTime(simulationReport.generated_at) }}</span>
      </div>
      <el-descriptions v-if="simulationReport" :column="5" border size="small" class="simulation-summary">
        <el-descriptions-item label="模拟数">{{ simulationReport.total_count }}</el-descriptions-item>
        <el-descriptions-item label="已平仓">{{ simulationReport.closed_count }}</el-descriptions-item>
        <el-descriptions-item label="进行中">{{ simulationReport.open_count }}</el-descriptions-item>
        <el-descriptions-item label="胜率">{{ formatPct(simulationReport.win_rate_pct) }}</el-descriptions-item>
        <el-descriptions-item label="平均收益">{{ formatSignedPct(simulationReport.average_return_pct) }}</el-descriptions-item>
        <el-descriptions-item label="平均 R">{{ formatRMultiple(simulationReport.average_r_multiple) }}</el-descriptions-item>
        <el-descriptions-item label="最大回撤">{{ formatSignedPct(simulationReport.max_drawdown_pct) }}</el-descriptions-item>
        <el-descriptions-item label="止损">{{ simulationReport.status_counts.stop_loss || 0 }}</el-descriptions-item>
        <el-descriptions-item label="目标退出">{{ simulationReport.status_counts.take_profit || 0 }}</el-descriptions-item>
        <el-descriptions-item label="趋势退出">{{ simulationReport.status_counts.trend_exit || 0 }}</el-descriptions-item>
      </el-descriptions>
      <el-table :data="simulationReport?.items || []" border max-height="480" empty-text="暂无可复盘的日线模拟记录">
        <el-table-column prop="ticker" label="Ticker" width="100" />
        <el-table-column label="信号 / 入场" width="190">
          <template #default="{ row }">{{ formatDate(row.signal_date) }}<br><small>{{ formatDate(row.entry_date) }} · {{ formatPrice(row.entry_price_usd) }} · {{ row.entry_price_source === 'next_open' ? '次日开盘' : '收盘回退' }}</small></template>
        </el-table-column>
        <el-table-column label="计划" min-width="190">
          <template #default="{ row }"><div>{{ row.entry_trigger || '-' }}</div><small>止损 {{ formatPrice(row.stop_loss_usd) }} · 目标 {{ formatPrice(row.take_profit_usd) }}</small></template>
        </el-table-column>
        <el-table-column label="结果" min-width="170">
          <template #default="{ row }"><el-tag :type="simulationStatusType(row.status)" effect="plain">{{ simulationStatusLabel(row.status) }}</el-tag><div><small>{{ row.exit_reason || `标记价 ${formatPrice(row.last_mark_price_usd)}` }}</small></div></template>
        </el-table-column>
        <el-table-column label="收益 / R" width="125" align="right">
          <template #default="{ row }">{{ formatSignedPct(row.return_pct) }}<br><small>毛 {{ formatSignedPct(row.gross_return_pct) }} · 成本 {{ formatPct(row.execution_cost_pct) }} · {{ formatRMultiple(row.r_multiple) }}</small></template>
        </el-table-column>
        <el-table-column label="最大回撤" width="115" align="right"><template #default="{ row }">{{ formatSignedPct(row.max_drawdown_pct) }}</template></el-table-column>
        <el-table-column label="持仓天数" width="100" align="right"><template #default="{ row }">{{ row.holding_days }}</template></el-table-column>
      </el-table>
    </el-dialog>

    <el-drawer v-model="detailVisible" :title="detailTarget ? `${detailTarget.ticker} ${t('common.details')}` : t('pages.targets.detail')" size="720px">
      <div v-if="detailTarget" class="target-detail">
        <el-alert
          v-if="detailTarget.last_sync_status === 'failed'"
          :title="syncIssueTitle(detailTarget)"
          :description="syncIssueSuggestion(detailTarget)"
          type="error"
          :closable="false"
          show-icon
        />
        <div class="target-detail-summary">
          <el-alert
            v-if="detailTarget.target_type === 'etf'"
            :title="hasStoredExactFundIdentity(detailTarget) ? t('pages.targets.fundIdentityExact') : t('pages.targets.fundIdentityLegacy')"
            :description="hasStoredExactFundIdentity(detailTarget) ? t('pages.targets.fundIdentityExactDetail') : t('pages.targets.fundIdentityLegacyDetail')"
            :type="hasStoredExactFundIdentity(detailTarget) ? 'success' : 'warning'"
            :closable="false"
            show-icon
          />
          <el-descriptions :column="2" border>
            <el-descriptions-item :label="t('common.company')">{{ detailTarget.company_name }}</el-descriptions-item>
            <el-descriptions-item label="CIK">{{ detailTarget.cik || '-' }}</el-descriptions-item>
            <el-descriptions-item :label="t('common.type')">{{ detailTarget.target_type }}</el-descriptions-item>
            <template v-if="detailTarget.target_type === 'etf'">
              <el-descriptions-item :label="t('pages.targets.fundSeriesId')">{{ detailTarget.fund_series_id || '-' }}</el-descriptions-item>
              <el-descriptions-item :label="t('pages.targets.fundClassId')">{{ detailTarget.fund_class_id || '-' }}</el-descriptions-item>
              <el-descriptions-item :label="t('pages.targets.identitySource')">{{ detailTarget.identity_source || '-' }}</el-descriptions-item>
            </template>
            <el-descriptions-item :label="t('common.targetGroup')">{{ detailTarget.group || '-' }}</el-descriptions-item>
            <el-descriptions-item :label="t('common.status')">
              <el-tag :type="detailTarget.status === 'enabled' ? 'success' : 'info'" effect="plain">{{ targetStatusLabel(detailTarget.status) }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="t('pages.targets.syncStatus')">
              <el-tag :type="syncStatusType(detailTarget.last_sync_status)" effect="plain">{{ detailTarget.last_sync_status || '-' }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="t('pages.targets.lastSync')">{{ formatDateTime(detailTarget.last_sync_at) }}</el-descriptions-item>
            <el-descriptions-item :label="t('pages.targets.recentNew')">{{ detailTarget.last_new_filings || 0 }}</el-descriptions-item>
            <el-descriptions-item :label="t('pages.targets.syncError')">{{ detailTarget.last_sync_error || '-' }}</el-descriptions-item>
            <el-descriptions-item :label="t('pages.targets.fetchPolicy')">{{ policySummary }}</el-descriptions-item>
          </el-descriptions>
          <div class="target-detail-actions">
            <el-button type="primary" :loading="syncingId === detailTarget.id" @click="syncTarget(detailTarget)">{{ t('pages.targets.syncTarget') }}</el-button>
            <el-button @click="openEdit(detailTarget)">{{ t('common.edit') }}</el-button>
          </div>
        </div>

        <div class="target-detail-section">
          <div class="panel-header target-detail-section-title">
            <span>AI 研判（手动）</span>
            <el-space><el-select fit-input-width v-model="targetAIProvider" placeholder="选择模型" size="small" style="width:210px"><el-option v-for="provider in aiProviders" :key="provider.id" :label="`${provider.name} · ${provider.model}`" :value="provider.id" /></el-select><el-select fit-input-width v-model="targetAIPromptTemplate" placeholder="选择模板" size="small" style="width:180px"><el-option v-for="template in aiPromptTemplates" :key="template.id" :label="template.name" :value="template.id" /></el-select><el-button type="primary" size="small" :disabled="!targetAIProvider || !targetAIPromptTemplate" :loading="targetAIGenerating" @click="generateTargetAI">生成研判</el-button></el-space>
          </div>
          <el-alert v-if="!aiProviders.length" type="info" :closable="false" title="尚未配置可用 AI 模型；请在系统配置 → AI 分析中添加供应商。" />
          <template v-else-if="targetAIAnalyses.length"><el-select fit-input-width v-model="targetAIAnalysisID" size="small" style="width:100%;margin-bottom:12px"><el-option v-for="item in targetAIAnalyses" :key="item.id" :label="`${item.provider_name} · ${item.model} · ${item.template_name || '历史模板'} · ${formatDateTime(item.requested_at)}`" :value="item.id" /></el-select><el-alert v-if="activeTargetAIAnalysis?.status === 'failed'" type="error" :closable="false" :title="activeTargetAIAnalysis.error_message || 'AI 调用失败'" /><template v-else><el-alert v-if="activeTargetAIAnalysis?.validation_warning" type="warning" :closable="false" show-icon title="模型输出未通过结构校验，系统已安全降级为证据不足。" style="margin-bottom:12px" /><AIRequestPrompt :system-prompt="activeTargetAIAnalysis?.system_prompt" :user-prompt="activeTargetAIAnalysis?.user_prompt" /><div style="padding:12px;background:var(--el-fill-color-light);border-radius:4px"><AIAnalysisResult :result="activeTargetAIAnalysis?.structured_result" :content="activeTargetAIAnalysis?.content" /></div></template></template>
          <el-empty v-else-if="aiProviders.length" description="尚无 AI 研判记录；仅在手动点击后生成。" :image-size="44" />
          <el-alert v-show="activeTargetAIAnalysis?.status === 'queued' || activeTargetAIAnalysis?.status === 'running'" type="warning" :closable="false" title="AI 研判正在后台处理，页面会自动刷新结果。" />
        </div>

        <div class="target-detail-section">
          <div class="panel-header target-detail-section-title">
            <span>公司概览（SEC + Longbridge）</span>
            <el-button v-if="detailTarget.target_type === 'stock'" size="small" :loading="detailCompanyProfileRefreshing" @click="refreshTargetCompanyProfile">刷新公司资料</el-button>
          </div>
          <el-descriptions v-if="detailCompanyProfile" :column="2" border size="small">
            <el-descriptions-item :label="t('common.company')">{{ detailCompanyProfile.company_name || detailTarget.company_name || '-' }}</el-descriptions-item>
            <el-descriptions-item label="交易所">{{ detailCompanyProfile.exchange || '-' }}</el-descriptions-item>
            <el-descriptions-item label="CIK">{{ detailCompanyProfile.cik || detailTarget.cik || '-' }}</el-descriptions-item>
            <el-descriptions-item label="注册州/地区">{{ detailCompanyProfile.state_of_incorporation || '-' }}</el-descriptions-item>
            <el-descriptions-item label="SEC 行业（SIC）" :span="2">
              {{ detailCompanyProfile.sic_description || (detailCompanyProfile.sic ? `SIC ${detailCompanyProfile.sic}` : '-') }}
            </el-descriptions-item>
            <el-descriptions-item label="业务概览" :span="2">{{ detailCompanyProfile.business_summary }}</el-descriptions-item>
            <el-descriptions-item v-if="detailCompanyProfile.website" label="官网"><a :href="companyProfileWebsiteURL(detailCompanyProfile.website)" target="_blank" rel="noopener">{{ detailCompanyProfile.website }}</a></el-descriptions-item>
            <el-descriptions-item v-if="detailCompanyProfile.founded" label="成立时间">{{ detailCompanyProfile.founded }}</el-descriptions-item>
            <el-descriptions-item v-if="detailCompanyProfile.listing_date" label="上市时间">{{ detailCompanyProfile.listing_date }}</el-descriptions-item>
            <el-descriptions-item v-if="detailCompanyProfile.market" label="上市市场">{{ detailCompanyProfile.market }}</el-descriptions-item>
            <el-descriptions-item v-if="detailCompanyProfile.employees" label="员工数">{{ detailCompanyProfile.employees }}</el-descriptions-item>
            <el-descriptions-item v-if="detailCompanyProfile.manager" label="管理者">{{ detailCompanyProfile.manager }}</el-descriptions-item>
            <el-descriptions-item v-if="detailCompanyProfile.year_end" label="财年截止日">{{ detailCompanyProfile.year_end }}</el-descriptions-item>
            <el-descriptions-item v-if="detailCompanyProfile.address" label="公司地址" :span="2">{{ detailCompanyProfile.address }}</el-descriptions-item>
            <el-descriptions-item label="来源" :span="2">
              {{ detailCompanyProfile.summary_source }}<span v-if="detailCompanyProfile.profile_fetched_at"> · Longbridge 更新于 {{ formatDateTime(detailCompanyProfile.profile_fetched_at) }}</span><span v-else-if="detailCompanyProfile.metadata_as_of"> · SEC 同步于 {{ formatDateTime(detailCompanyProfile.metadata_as_of) }}</span>
            </el-descriptions-item>
          </el-descriptions>
          <el-alert v-else type="info" :closable="false" show-icon title="尚未找到本地 SEC 公司元数据；将在下一次 SEC 安全宇宙同步后自动补齐。" />
        </div>

        <div v-if="detailTarget.target_type === 'stock'" class="target-detail-section">
          <div class="panel-header target-detail-section-title">
            <span>机构持仓披露（Longbridge）</span>
            <el-button size="small" :loading="detailMarketResearchRefreshing" @click="refreshTargetMarketResearch">刷新机构持仓</el-button>
          </div>
          <el-alert type="info" :closable="false" show-icon title="显示 Longbridge 已返回并保存的全部报告日快照。机构的披露频率、覆盖范围和持股比例口径由提供方决定，不能把未覆盖视为零持仓。" />
          <template v-if="detailInstitutionalHoldings && (detailInstitutionalHoldings.institutional_holders.length || detailInstitutionalHoldings.fund_holders.length)">
            <div class="analyst-rating-provenance-title">机构股东：每次披露的公司持股比例</div>
            <el-table :data="detailInstitutionalHoldings.institutional_holders" size="small" border max-height="360" empty-text="Longbridge 暂无机构股东披露">
              <el-table-column prop="holder_name" label="机构" min-width="210" show-overflow-tooltip />
              <el-table-column prop="institution_type" label="类型" min-width="100" show-overflow-tooltip />
              <el-table-column label="持股比例" width="110" align="right"><template #default="{ row }">{{ formatPct(row.percent_of_shares) }}</template></el-table-column>
              <el-table-column label="披露变动" width="120" align="right"><template #default="{ row }">{{ row.shares_changed === undefined || row.shares_changed === null ? '-' : row.shares_changed.toLocaleString(undefined, { maximumFractionDigits: 0 }) }}</template></el-table-column>
              <el-table-column prop="report_date" label="报告日" width="115" />
              <el-table-column label="本地同步" width="165"><template #default="{ row }">{{ formatDateTime(row.fetched_at) }}</template></el-table-column>
              <el-table-column label="来源" width="80"><template #default="{ row }"><el-link v-if="row.source_url" :href="row.source_url" target="_blank" type="primary">Longbridge</el-link><span v-else>-</span></template></el-table-column>
            </el-table>
            <div class="analyst-rating-provenance-title">基金 / ETF：每次披露的组合权重</div>
            <el-table :data="detailInstitutionalHoldings.fund_holders" size="small" border max-height="360" empty-text="Longbridge 暂无基金或 ETF 持仓披露">
              <el-table-column prop="fund_name" label="基金 / ETF" min-width="220" show-overflow-tooltip />
              <el-table-column prop="fund_symbol" label="代码" width="110" />
              <el-table-column label="组合权重" width="110" align="right"><template #default="{ row }">{{ formatPct(row.position_ratio) }}</template></el-table-column>
              <el-table-column prop="report_date" label="报告日" width="115" />
              <el-table-column label="本地同步" width="165"><template #default="{ row }">{{ formatDateTime(row.fetched_at) }}</template></el-table-column>
              <el-table-column label="来源" width="80"><template #default="{ row }"><el-link v-if="row.source_url" :href="row.source_url" target="_blank" type="primary">Longbridge</el-link><span v-else>-</span></template></el-table-column>
            </el-table>
            <el-alert type="info" :closable="false" style="margin-top: 12px" :title="detailInstitutionalHoldings.message" />
          </template>
          <el-alert v-else type="info" :closable="false" show-icon style="margin-top: 12px" :title="detailInstitutionalHoldings?.message || '尚未同步机构持仓披露。'" description="可手动刷新当前标的；不会执行 SEC 同步、行情全量请求或候选重算。" />
        </div>

        <div v-if="detailTarget.target_type === 'stock'" class="target-detail-section">
          <div class="panel-header target-detail-section-title">
            <span>市场一致目标价与合理价值情景</span>
            <el-button size="small" :loading="detailValuationRefreshing" @click="refreshTargetValuationResearch">刷新估值研究</el-button>
          </div>
          <el-alert type="warning" :closable="false" show-icon title="不提供 Longbridge “公允价值”结论：市场一致目标价与本地历史估值情景必须分开阅读，均不构成投资建议。" />
          <template v-if="detailFairValue?.status === 'available'">
            <el-descriptions :column="2" border size="small" style="margin-top: 12px">
              <el-descriptions-item label="参考收盘价">{{ formatFairValuePrice(detailFairValue.reference_price, detailFairValue.currency) }}<span v-if="detailFairValue.reference_price_date"> · {{ detailFairValue.reference_price_date }}</span></el-descriptions-item>
              <el-descriptions-item label="市场一致目标价（平均）">{{ formatFairValuePrice(detailFairValue.market_consensus_target, detailFairValue.currency) }}</el-descriptions-item>
              <el-descriptions-item label="目标价相对空间">{{ detailFairValue.market_consensus_upside_pct === undefined || detailFairValue.market_consensus_upside_pct === null ? '-' : formatSignedPct(detailFairValue.market_consensus_upside_pct) }}</el-descriptions-item>
              <el-descriptions-item label="市场目标价区间">{{ formatFairValuePrice(detailFairValue.market_consensus_low, detailFairValue.currency) }} - {{ formatFairValuePrice(detailFairValue.market_consensus_high, detailFairValue.currency) }}</el-descriptions-item>
              <el-descriptions-item label="机构覆盖数">{{ detailFairValue.analyst_count || '-' }}</el-descriptions-item>
              <el-descriptions-item v-if="detailFairValue.local_historical_scenario" label="本地历史倍数情景（低 / 中 / 高）" :span="2">
                {{ formatFairValuePrice(detailFairValue.local_historical_scenario.low, detailFairValue.currency) }} / {{ formatFairValuePrice(detailFairValue.local_historical_scenario.mid, detailFairValue.currency) }} / {{ formatFairValuePrice(detailFairValue.local_historical_scenario.high, detailFairValue.currency) }}（{{ detailFairValue.local_historical_scenario.metrics }} 个可用指标等权）
              </el-descriptions-item>
              <el-descriptions-item label="本地参考价来源" :span="2">{{ detailFairValue.reference_price_source || '-' }}</el-descriptions-item>
            </el-descriptions>
            <div v-if="detailFairValue.metric_scenarios.length" class="analyst-rating-provenance-title">本地计算输入与过程</div>
            <el-table v-if="detailFairValue.metric_scenarios.length" :data="detailFairValue.metric_scenarios" size="small" border>
              <el-table-column prop="metric" label="指标" width="80" />
              <el-table-column label="当前倍数" width="115" align="right"><template #default="{ row }">{{ row.current_multiple.toFixed(2) }}</template></el-table-column>
              <el-table-column label="历史倍数低 / 中 / 高" min-width="190" align="right"><template #default="{ row }">{{ row.historical_low.toFixed(2) }} / {{ row.historical_mid.toFixed(2) }} / {{ row.historical_high.toFixed(2) }}</template></el-table-column>
              <el-table-column label="推导价格低 / 中 / 高" min-width="210" align="right"><template #default="{ row }">{{ formatFairValuePrice(row.price_low, detailFairValue.currency) }} / {{ formatFairValuePrice(row.price_mid, detailFairValue.currency) }} / {{ formatFairValuePrice(row.price_high, detailFairValue.currency) }}</template></el-table-column>
            </el-table>
            <el-alert type="info" :closable="false" style="margin-top: 12px" :title="detailFairValue.methodology" :description="detailFairValue.message" />
          </template>
          <el-alert v-else type="info" :closable="false" show-icon style="margin-top:12px" :title="detailFairValue?.message || '尚缺机构目标价或可用估值倍数，无法计算本地历史估值情景。'" />
        </div>

        <div v-if="detailTarget.target_type === 'stock'" class="target-detail-section">
          <div class="panel-header target-detail-section-title">
            <span>机构与分析师共识（Longbridge）</span>
            <el-button size="small" :loading="detailAnalystRatingRefreshing" @click="refreshTargetAnalystRating">刷新分析师评级</el-button>
          </div>
          <template v-if="detailAnalystRating?.latest?.status === 'available'">
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item label="共识评级"><el-tag :type="analystRecommendationTagType(detailAnalystRating.latest.recommendation)" effect="plain">{{ analystRecommendationLabel(detailAnalystRating.latest.recommendation) }}</el-tag></el-descriptions-item>
              <el-descriptions-item label="覆盖数">{{ detailAnalystRating.latest.analyst_count }}</el-descriptions-item>
			  <el-descriptions-item label="市场一致目标价（平均）">{{ formatAnalystPrice(detailAnalystRating.latest.target_average_micros, detailAnalystRating.latest.currency) }}</el-descriptions-item>
              <el-descriptions-item label="目标价区间">{{ formatAnalystPrice(detailAnalystRating.latest.target_low_micros, detailAnalystRating.latest.currency) }} - {{ formatAnalystPrice(detailAnalystRating.latest.target_high_micros, detailAnalystRating.latest.currency) }}</el-descriptions-item>
              <el-descriptions-item label="评级分布" :span="2">强烈买入 {{ detailAnalystRating.latest.strong_buy_count }} · 买入 {{ detailAnalystRating.latest.buy_count }} · 持有 {{ detailAnalystRating.latest.hold_count }} · 跑输 {{ detailAnalystRating.latest.underperform_count }} · 卖出 {{ detailAnalystRating.latest.sell_count }}</el-descriptions-item>
              <el-descriptions-item label="来源" :span="2">Longbridge · {{ analystProviderTimeText(detailAnalystRating.latest) }}</el-descriptions-item>
            </el-descriptions>
            <div class="analyst-rating-provenance-title">结果溯源明细</div>
            <el-table :data="analystRatingProvenanceRows(detailAnalystRating.latest)" size="small" border class="analyst-rating-provenance-table">
              <el-table-column prop="result" label="分析结果" min-width="120" />
              <el-table-column prop="value" label="当前值" min-width="165" show-overflow-tooltip />
              <el-table-column prop="source" label="数据来源 / 原始聚合字段" min-width="240" show-overflow-tooltip />
              <el-table-column prop="providerUpdatedAt" label="提供方时间" width="145" show-overflow-tooltip />
              <el-table-column prop="fetchedAt" label="本地同步时间" width="165" />
              <el-table-column prop="note" label="说明" min-width="190" show-overflow-tooltip />
            </el-table>
            <div v-if="detailAnalystRating.history?.length > 1" class="analyst-rating-provenance-title">快照变更历史（仅在聚合值变化时新增）</div>
            <el-table v-if="detailAnalystRating.history?.length > 1" :data="detailAnalystRating.history.slice(0, 12)" size="small" border class="analyst-rating-history-table">
              <el-table-column label="同步时间" width="165"><template #default="{ row }">{{ formatDateTime(row.fetched_at) }}</template></el-table-column>
              <el-table-column label="评级" width="105"><template #default="{ row }">{{ analystRecommendationLabel(row.recommendation) }}</template></el-table-column>
              <el-table-column prop="analyst_count" label="覆盖数" width="80" align="right" />
              <el-table-column label="平均目标价" width="125" align="right"><template #default="{ row }">{{ formatAnalystPrice(row.target_average_micros, row.currency) }}</template></el-table-column>
              <el-table-column prop="change_summary" label="有效变化" min-width="165" show-overflow-tooltip />
            </el-table>
          </template>
          <el-alert v-else type="info" :closable="false" show-icon :title="detailAnalystRating?.message || '尚未同步分析师共识'" description="可手动刷新当前标的，不会执行 SEC 同步或行情全量请求。小盘股没有分析师覆盖属于正常情况。" />
        </div>

        <div v-if="detailTarget.target_type === 'stock'" class="target-detail-section">
          <div class="panel-header target-detail-section-title">
            <span>财报预告（Longbridge）</span>
            <el-button size="small" :loading="detailEarningsRefreshing" @click="refreshTargetEarningsPreview">刷新财报预告</el-button>
          </div>
          <template v-if="detailEarningsPreview?.preview?.status === 'scheduled'">
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item label="下次财报日">{{ formatDate(detailEarningsPreview.preview.report_at) }}（{{ daysUntilEarnings(detailEarningsPreview.preview.report_at) }}）</el-descriptions-item>
              <el-descriptions-item label="发布时段">{{ detailEarningsPreview.preview.session || '提供方未标注' }}</el-descriptions-item>
              <el-descriptions-item label="财季">{{ earningsFiscalPeriod(detailEarningsPreview.preview) }}</el-descriptions-item>
              <el-descriptions-item label="币种">{{ detailEarningsPreview.preview.currency || '-' }}</el-descriptions-item>
              <el-descriptions-item label="EPS 预期">{{ formatEarningsValue(detailEarningsPreview.preview.eps_estimate, detailEarningsPreview.preview.currency, false) }}</el-descriptions-item>
              <el-descriptions-item label="收入预期">{{ formatEarningsValue(detailEarningsPreview.preview.revenue_estimate, detailEarningsPreview.preview.currency, true) }}</el-descriptions-item>
              <el-descriptions-item label="最近实际 EPS">{{ formatEarningsValue(detailEarningsPreview.preview.eps_actual, detailEarningsPreview.preview.currency, false) }}</el-descriptions-item>
              <el-descriptions-item label="最近实际收入">{{ formatEarningsValue(detailEarningsPreview.preview.revenue_actual, detailEarningsPreview.preview.currency, true) }}</el-descriptions-item>
              <el-descriptions-item v-if="detailEarningsPreview.preview.change_summary" label="最近变化" :span="2">{{ detailEarningsPreview.preview.change_summary }}</el-descriptions-item>
              <el-descriptions-item label="来源与本地更新时间" :span="2">Longbridge 财报日历 / 财务共识 · {{ formatDateTime(detailEarningsPreview.preview.fetched_at) }}</el-descriptions-item>
              <el-descriptions-item v-if="detailEarningsPreview.preview.event_content" label="提供方说明" :span="2">{{ detailEarningsPreview.preview.event_content }}</el-descriptions-item>
            </el-descriptions>
            <el-alert type="info" :closable="false" show-icon class="target-technical-alert" title="财报日、发布时段与预期值由提供方维护，可能调整；实际披露结果仍以公司公告及 SEC 文件为准。" />
          </template>
          <el-alert v-else type="info" :closable="false" show-icon :title="detailEarningsPreview?.message || '尚未同步财报预告'" :description="detailEarningsPreview?.preview?.last_error || '可手动刷新当前标的；不会执行 SEC 同步或行情全量请求。'" />
        </div>

        <div v-if="detailTarget.target_type === 'stock'" class="target-detail-section">
          <ProfitHistoryChart :history="detailProfitHistory" />
        </div>

        <div class="target-detail-section">
          <div class="panel-header target-detail-section-title">
            <span>本地日线与成交额</span>
            <el-button size="small" :loading="detailTechnicalBackfilling" @click="backfillTargetTechnicalHistory">
              回填/刷新价格历史
            </el-button>
          </div>
          <el-alert
            v-if="detailTechnicalHistory.length === 0"
            title="尚未保存该标的的本地日线；点击右侧按钮可回填约 220 个交易日，用于展示 MA20、MA50、MA200 与每日估算成交额。"
            type="info"
            :closable="false"
            show-icon
            class="target-technical-alert"
          />
          <TechnicalPriceHistoryChart :ticker="detailTarget.ticker" :rows="detailTechnicalHistory" :technical="detailTechnicalAnalysis" />
        </div>

        <div class="target-detail-section">
          <div class="panel-header target-detail-section-title"><span>交易计划状态历史</span></div>
          <el-alert type="info" :closable="false" show-icon title="仅记录日线收盘后交易计划状态的变化，不是交易指令或实际成交记录。" />
          <el-timeline v-if="detailTradeSetupHistory.length" class="target-trade-setup-timeline">
            <el-timeline-item v-for="event in detailTradeSetupHistory" :key="event.id" :timestamp="formatDateTime(event.started_at)" :type="tradeSetupTagType(event.status)">
              <strong>{{ tradeSetupLabel(event.status) }}</strong>
              <span v-if="event.previous_status">（由 {{ tradeSetupLabel(event.previous_status) }} 变更）</span>
              <div class="target-trade-setup-detail">收盘 {{ formatPrice(event.close_usd) }} USD · 止损 {{ formatPrice(event.stop_loss_usd) }} USD · {{ event.entry_trigger || event.exit_reason || '等待触发条件' }}</div>
              <div v-if="event.reasons?.length" class="target-trade-setup-detail">{{ event.reasons.join('；') }}</div>
            </el-timeline-item>
          </el-timeline>
          <el-empty v-else :image-size="44" description="尚未记录状态变化；下次日线同步后会建立当前状态基线。" />
        </div>

        <div class="panel-header target-detail-section-title">
          <span>{{ t('pages.targets.recentSync') }}</span>
          <el-link type="primary" @click="$router.push('/sync-runs')">{{ t('common.history') }}</el-link>
        </div>
        <el-table :data="detailSyncDetails" v-loading="detailLoading" border :empty-text="t('pages.targets.noSyncRuns')">
          <el-table-column prop="status" :label="t('common.status')" width="130">
            <template #default="{ row }">
              <el-tag class="status-tag" :type="syncStatusType(row.status)" effect="plain">{{ row.status }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="new_filings" :label="t('common.newCount')" width="80" />
          <el-table-column prop="duration_ms" :label="t('common.duration')" width="100">
            <template #default="{ row }">{{ formatDuration(row.duration_ms) }}</template>
          </el-table-column>
          <el-table-column prop="started_at" :label="t('common.time')" width="180">
            <template #default="{ row }">{{ formatDateTime(row.started_at) }}</template>
          </el-table-column>
          <el-table-column prop="error_message" :label="t('common.error')" min-width="180" show-overflow-tooltip />
		  <el-table-column prop="warning_message" :label="t('pages.syncRuns.warning')" min-width="180" show-overflow-tooltip />
        </el-table>

        <div class="panel-header target-detail-section-title">
          <span>{{ t('pages.targets.recentFilings') }}</span>
          <el-link type="primary" @click="$router.push(`/filings?ticker=${encodeURIComponent(detailTarget.ticker)}`)">{{ t('common.viewAll') }}</el-link>
        </div>
        <el-table :data="detailFilings" v-loading="detailLoading" border :empty-text="t('pages.targets.noFilings')">
          <el-table-column prop="filing_type" :label="t('common.type')" width="90" />
          <el-table-column prop="filing_date" :label="t('common.filingDate')" width="130">
            <template #default="{ row }">{{ formatDate(row.filing_date) }}</template>
          </el-table-column>
          <el-table-column prop="pulled_at" :label="t('common.syncTime')" width="170">
            <template #default="{ row }">{{ formatDateTime(row.pulled_at) }}</template>
          </el-table-column>
          <el-table-column prop="title" :label="t('common.title')" min-width="200" show-overflow-tooltip />
          <el-table-column :label="t('common.link')" width="80">
            <template #default="{ row }"><el-link :href="row.filing_url" target="_blank" type="primary">{{ t('common.open') }}</el-link></template>
          </el-table-column>
        </el-table>
      </div>
    </el-drawer>
  </section>
</template>

<script setup lang="ts">
import axios from 'axios'
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { MoreFilled } from '@element-plus/icons-vue'
import { apiClient } from '@/api/client'
import AIRequestPrompt from '@/components/AIRequestPrompt.vue'
import AIAnalysisResult from '@/components/AIAnalysisResult.vue'
import type { AIAnalysisStructuredResult } from '@/api/types'
import ProfitHistoryChart from '@/components/ProfitHistoryChart.vue'
import TechnicalPriceHistoryChart from '@/components/TechnicalPriceHistoryChart.vue'
import type { AnalystRatingView, ApiResponse, CandidateFairValueEstimate, CandidateTechnicalAnalysis, CandidateTechnicalHistoryRow, CompanyProfile, EarningsPreview, EarningsPreviewRefreshResult, EarningsPreviewView, Filing, FundIdentity, PageResult, ProfitHistory, SyncRunDetail, SystemConfig, TickerInstitutionalHoldingHistory, TickerLookup, TickerTechnicalHistory, TradePlanSimulationRebuildResult, TradePlanSimulationReport, TradeSetupStatusEvent, WatchTarget } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const lookingUp = ref(false)
const syncingId = ref<number | null>(null)
const route = useRoute()
const rows = ref<WatchTarget[]>([])
const earningsPreviews = ref<Record<number, EarningsPreview>>({})
const total = ref(0)
const page = ref(1)
const pageSize = 20
const dialogVisible = ref(false)
const detailVisible = ref(false)
const detailLoading = ref(false)
const detailTarget = ref<WatchTarget | null>(null)
const detailFilings = ref<Filing[]>([])
const detailSyncDetails = ref<SyncRunDetail[]>([])
const detailProfitHistory = ref<ProfitHistory | null>(null)
const detailTechnicalHistory = ref<CandidateTechnicalHistoryRow[]>([])
const detailTechnicalAnalysis = ref<CandidateTechnicalAnalysis | null>(null)
const detailTradeSetupHistory = ref<TradeSetupStatusEvent[]>([])
const detailCompanyProfile = ref<CompanyProfile | null>(null)
const detailCompanyProfileRefreshing = ref(false)
const detailAnalystRating = ref<AnalystRatingView | null>(null)
const detailAnalystRatingRefreshing = ref(false)
const detailFairValue = ref<CandidateFairValueEstimate | null>(null)
const detailValuationRefreshing = ref(false)
const detailInstitutionalHoldings = ref<TickerInstitutionalHoldingHistory | null>(null)
const detailMarketResearchRefreshing = ref(false)
const detailEarningsPreview = ref<EarningsPreviewView | null>(null)
const detailEarningsRefreshing = ref(false)
const detailTechnicalBackfilling = ref(false)
type AIProvider = { id: string; name: string; model: string }
type AIPromptTemplate = { id: string; name: string }
type AIAnalysis = { id: number; provider_name: string; model: string; template_name?: string; content: string; status: string; error_message?: string; validation_warning?: string; system_prompt?: string; user_prompt?: string; requested_at: string; structured_result?: AIAnalysisStructuredResult }
const aiProviders = ref<AIProvider[]>([])
const aiPromptTemplates = ref<AIPromptTemplate[]>([])
const targetAIProvider = ref('')
const targetAIPromptTemplate = ref('')
const targetAIGenerating = ref(false)
const targetAIAnalyses = ref<AIAnalysis[]>([])
const targetAIAnalysisID = ref<number | null>(null)
const activeTargetAIAnalysis = computed(() => targetAIAnalyses.value.find((item) => item.id === targetAIAnalysisID.value) || targetAIAnalyses.value[0])
let targetAIPollingTimer: number | undefined
const simulationVisible = ref(false)
const simulationLoading = ref(false)
const simulationRebuilding = ref(false)
const simulationReport = ref<TradePlanSimulationReport | null>(null)
const systemConfigs = ref<SystemConfig[]>([])
const editingId = ref<number | null>(null)
const filters = reactive({ ticker: '', status: '', group: '', upcoming_earnings: false })
const form = reactive({
  ticker: '', company_name: '', cik: '', target_type: 'stock', fund_series_id: '', fund_class_id: '', identity_source: '', group: '', status: 'enabled'
})
const fundCandidates = ref<FundIdentity[]>([])
const selectedFundCandidateKey = ref('')
const resolvedFundIdentity = ref<FundIdentity | null>(null)

const formHasExactFundIdentity = computed(() => matchesResolvedFundIdentity(form))
const saveDisabled = computed(() => form.target_type === 'etf' && !formHasExactFundIdentity.value)
const fundIdentityFormDescription = computed(() => {
  if (formHasExactFundIdentity.value) {
    return t('pages.targets.fundIdentityExactForm', { source: resolvedFundIdentity.value?.source || t('pages.targets.identitySourceManual') })
  }
  if (resolvedFundIdentity.value) return t('pages.targets.fundIdentityModified')
  if (fundCandidates.value.length > 0) return t('pages.targets.fundCandidateRequired')
  return t('pages.targets.fundIdentityUnresolvedDetail')
})

const policySummary = computed(() => {
  const days = configValue('sec.initial_fetch_days', '30')
  const syncWindow = configValue('sec.sync_window_days', '30')
  const max = configValue('sec.max_fetch_count', '300')
  const full = configValue('sec.fetch_full_history', 'false') === 'true'
  const syncText = syncWindow === '0' ? t('pages.targets.policyEveryUnlimited') : t('pages.targets.policyEveryDays', { days: syncWindow })
  const initialText = full ? t('pages.targets.policyInitialFull') : t('pages.targets.policyInitialDays', { days })
  const maxText = max === '0' ? t('pages.targets.policyMaxUnlimited') : t('pages.targets.policyMaxCount', { count: max })
  return t('pages.targets.policySummary', { syncWindow: syncText, initialWindow: initialText, max: maxText })
})

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<PageResult<WatchTarget>>>('/watch-targets', { params: { ...filters, page: page.value, page_size: pageSize } })
    rows.value = res.data.data.items
    total.value = res.data.data.total
		void loadEarningsPreviews()
  } catch (error) {
    // Keep a failed quick filter from looking like it simply returned the
    // previously loaded, unfiltered table.
    rows.value = []
    total.value = 0
    const message = axios.isAxiosError(error) ? error.response?.data?.message : ''
    ElMessage.error(typeof message === 'string' && message.trim() ? message : '加载监控标的失败，请稍后重试')
  } finally {
    loading.value = false
  }
}

const upcomingEarningsCount = computed(() => Object.values(earningsPreviews.value).filter((preview) => preview.status === 'scheduled' && earningsDays(preview.report_at) !== null && (earningsDays(preview.report_at) || 0) >= 0).length)
function toggleUpcomingEarnings() { filters.upcoming_earnings = !filters.upcoming_earnings; page.value = 1; load() }

async function loadEarningsPreviews() {
  try {
    const response = await apiClient.get<ApiResponse<EarningsPreview[]>>('/watch-targets/earnings-previews')
    earningsPreviews.value = Object.fromEntries(response.data.data.map((item) => [item.target_id, item]))
  } catch {
    // Earnings previews are optional provider data; the main watch list remains
    // available when Longbridge or its local cache is temporarily unavailable.
    earningsPreviews.value = {}
  }
}

async function openTradePlanSimulations() {
	simulationLoading.value = true
	try {
		const response = await apiClient.get<ApiResponse<TradePlanSimulationReport>>('/watch-targets/trade-plan-simulations')
		simulationReport.value = response.data.data
		simulationVisible.value = true
	} finally {
		simulationLoading.value = false
	}
}

async function rebuildTradePlanSimulations() {
	simulationRebuilding.value = true
	try {
		const response = await apiClient.post<ApiResponse<TradePlanSimulationRebuildResult>>('/watch-targets/trade-plan-simulations/rebuild')
		simulationReport.value = response.data.data
		ElMessage.success(`已创建 ${response.data.data.created_count} 笔模拟，更新 ${response.data.data.updated_count} 笔`)
	} finally {
		simulationRebuilding.value = false
	}
}

function configValue(key: string, fallback: string) {
  return systemConfigs.value.find((item) => item.config_key === key)?.config_value || fallback
}

function openCreate() {
  editingId.value = null
  Object.assign(form, { ticker: '', company_name: '', cik: '', target_type: 'stock', fund_series_id: '', fund_class_id: '', identity_source: '', group: '', status: 'enabled' })
  clearFundResolution()
  dialogVisible.value = true
}

async function lookupTicker() {
  const ticker = form.ticker.trim().toUpperCase()
  if (!ticker) return
  form.ticker = ticker
  lookingUp.value = true
  try {
    const res = await apiClient.get<ApiResponse<TickerLookup>>(`/sec/tickers/${encodeURIComponent(ticker)}`, {
      params: { target_type: form.target_type }
    })
    const lookup = res.data.data
    form.company_name = lookup.company_name
    form.cik = lookup.cik
    form.target_type = lookup.target_type || form.target_type || 'stock'
    clearFundResolution()
    Object.assign(form, { fund_series_id: '', fund_class_id: '', identity_source: '' })
    if (lookup.fund_identity) {
      applyFundIdentity(lookup.fund_identity)
      ElMessage.success(t('pages.targets.fundIdentityExact'))
    } else if (lookup.fund_candidates?.length) {
      fundCandidates.value = lookup.fund_candidates
      ElMessage.warning(t('pages.targets.fundCandidateRequired'))
    } else if (form.target_type === 'etf') {
      ElMessage.warning(t('pages.targets.fundIdentityUnresolved'))
    } else {
      ElMessage.success(t('messages.lookupDone'))
    }
  } catch (error) {
    ElMessage.warning(t('messages.lookupFailed'))
  } finally {
    lookingUp.value = false
  }
}

function openEdit(row: WatchTarget) {
  editingId.value = row.id
  Object.assign(form, {
    ...row,
    fund_series_id: row.fund_series_id || '',
    fund_class_id: row.fund_class_id || '',
    identity_source: row.identity_source || ''
  })
  clearFundCandidates()
  resolvedFundIdentity.value = resolvedIdentityFromStoredTarget(row)
  dialogVisible.value = true
}

function hasCompleteFundIdentity(target: Pick<WatchTarget, 'target_type' | 'cik' | 'fund_series_id' | 'fund_class_id'> | typeof form) {
  return target.target_type === 'etf' && Boolean(target.cik?.trim()) && Boolean(target.fund_series_id?.trim()) && Boolean(target.fund_class_id?.trim())
}

function hasStoredExactFundIdentity(target: WatchTarget) {
  return hasCompleteFundIdentity(target)
}

// Pure conversion helper: a complete stored ETF tuple is the verified baseline
// for non-identity edits. It returns null for legacy Trust-level records.
function resolvedIdentityFromStoredTarget(target: WatchTarget): FundIdentity | null {
  if (!hasCompleteFundIdentity(target)) return null
  return {
    ticker: target.ticker,
    cik: target.cik,
    series_id: target.fund_series_id || '',
    class_id: target.fund_class_id || '',
    fund_name: target.company_name || target.identity_note || target.ticker,
    source: target.identity_source || 'stored_watch_target'
  }
}

function matchesResolvedFundIdentity(target: typeof form) {
  const identity = resolvedFundIdentity.value
  return Boolean(identity && hasCompleteFundIdentity(target) &&
    target.cik.trim() === identity.cik.trim() &&
    target.fund_series_id.trim() === identity.series_id.trim() &&
    target.fund_class_id.trim() === identity.class_id.trim())
}

function clearFundResolution() {
  clearFundCandidates()
  resolvedFundIdentity.value = null
}

function clearFundCandidates() {
  fundCandidates.value = []
  selectedFundCandidateKey.value = ''
}

function invalidateFundIdentity() {
  Object.assign(form, { fund_series_id: '', fund_class_id: '', identity_source: '' })
  clearFundResolution()
}

function handleTargetTypeChange(targetType: string) {
  if (targetType !== 'etf') {
    Object.assign(form, { fund_series_id: '', fund_class_id: '', identity_source: '' })
    clearFundResolution()
  }
}

function fundCandidateKey(candidate: FundIdentity) {
  return `${candidate.cik}:${candidate.series_id}:${candidate.class_id}`
}

function fundCandidateLabel(candidate: FundIdentity) {
  const name = candidate.fund_name || candidate.ticker
  return `${name} · ${candidate.cik} · ${candidate.series_id} · ${candidate.class_id}`
}

function selectFundCandidate(key: string) {
  const candidate = fundCandidates.value.find((item) => fundCandidateKey(item) === key)
  if (!candidate) return
  applyFundIdentity(candidate)
}

function applyFundIdentity(identity: FundIdentity) {
  resolvedFundIdentity.value = { ...identity }
  Object.assign(form, {
    cik: identity.cik,
    company_name: identity.fund_name || identity.ticker,
    fund_series_id: identity.series_id,
    fund_class_id: identity.class_id,
    identity_source: identity.source
  })
}

function markManualFundIdentity() {
  // A manual CIK, Series ID, or Class ID edit requires a fresh lookup or
  // candidate selection before this ETF can be saved again.
  clearFundResolution()
}

async function save() {
  saving.value = true
  let createdTarget: WatchTarget | null = null
  try {
    if (editingId.value) {
      await apiClient.put(`/watch-targets/${editingId.value}`, form)
    } else {
      const res = await apiClient.post<ApiResponse<WatchTarget>>('/watch-targets', form)
      createdTarget = res.data.data
    }
  } catch (error) {
    ElMessage.error(saveErrorMessage(error))
    return
  } finally {
    saving.value = false
  }
  dialogVisible.value = false
  ElMessage.success(t('messages.saved'))
  await load()
  if (createdTarget) {
    await offerImmediateSync(createdTarget)
  }
}

function saveErrorMessage(error: unknown) {
  if (axios.isAxiosError(error)) {
    const message = error.response?.data?.message
    if (typeof message === 'string' && message.trim()) return message
  }
  return t('messages.saveFailed')
}

async function setTargetEnabled(row: WatchTarget, enabled: boolean) {
  const previous = row.status
  row.status = enabled ? 'enabled' : 'disabled'
  try {
    await apiClient.patch(`/watch-targets/${row.id}/status`, { status: row.status })
    await load()
  } catch (error) {
    row.status = previous
    throw error
  }
}

async function handleTargetCommand(command: string, row: WatchTarget) {
  if (command === 'detail') {
    await openDetail(row)
    return
  }
  if (command === 'edit') {
    openEdit(row)
    return
  }
  if (command === 'delete') {
    await remove(row)
  }
}

async function syncTarget(row: WatchTarget) {
  syncingId.value = row.id
  try {
    const res = await apiClient.post<ApiResponse<{ new_filings: number, failed_targets: number }>>(`/watch-targets/${row.id}/sync`)
    ElMessage.success(t('messages.syncDone', { count: res.data.data.new_filings }))
    await load()
    if (detailVisible.value && detailTarget.value?.id === row.id) {
      const updated = rows.value.find((item) => item.id === row.id)
      if (updated) detailTarget.value = updated
      await loadTargetDetailData(row)
    }
  } finally {
    syncingId.value = null
  }
}

async function offerImmediateSync(target: WatchTarget) {
  try {
    await ElMessageBox.confirm(t('messages.offerSync', { ticker: target.ticker }), t('messages.targetSavedTitle'), {
      confirmButtonText: t('messages.syncNow'),
      cancelButtonText: t('messages.later'),
      type: 'info'
    })
  } catch (error) {
    // User chose to wait for scheduled sync.
    return
  }
  await syncTarget(target)
}

async function openDetail(row: WatchTarget) {
  detailTarget.value = row
  detailVisible.value = true
  await loadTargetDetailData(row)
}

async function loadTargetDetailData(row: WatchTarget) {
	detailLoading.value = true
	detailProfitHistory.value = null
	detailTechnicalHistory.value = []
	detailTechnicalAnalysis.value = null
	detailTradeSetupHistory.value = []
	detailCompanyProfile.value = null
	detailAnalystRating.value = null
	detailFairValue.value = null
	detailInstitutionalHoldings.value = null
		detailEarningsPreview.value = null
		targetAIAnalyses.value = []
	try {
    const [filings, syncDetails, configs, tradeSetupHistory] = await Promise.all([
      apiClient.get<ApiResponse<PageResult<Filing>>>('/filings', {
        params: { ticker: row.ticker, page: 1, page_size: 8, sort_by: 'pulled_at', sort_order: 'desc' }
      }),
      apiClient.get<ApiResponse<SyncRunDetail[]>>(`/watch-targets/${row.id}/sync-details`),
      apiClient.get<ApiResponse<SystemConfig[]>>('/system-configs'),
      apiClient.get<ApiResponse<TradeSetupStatusEvent[]>>(`/discovery/trade-setup-history/${encodeURIComponent(row.ticker)}`)
    ])
    detailFilings.value = filings.data.data.items
    detailSyncDetails.value = syncDetails.data.data
    systemConfigs.value = configs.data.data
		detailTradeSetupHistory.value = tradeSetupHistory.data.data || []
		try {
			const profile = await apiClient.get<ApiResponse<CompanyProfile>>(`/discovery/company-profiles/${encodeURIComponent(row.ticker)}`, { params: { cik: row.cik || undefined } })
			detailCompanyProfile.value = profile.data.data
		} catch {
			detailCompanyProfile.value = null
		}
		try {
			const technical = await apiClient.get<ApiResponse<TickerTechnicalHistory>>(`/watch-targets/${row.id}/technical-history`)
			detailTechnicalHistory.value = technical.data.data.history || []
			detailTechnicalAnalysis.value = technical.data.data.technical
		} catch {
			detailTechnicalHistory.value = []
			detailTechnicalAnalysis.value = null
		}
		if (row.target_type === 'stock') {
			try {
				const earnings = await apiClient.get<ApiResponse<EarningsPreviewView>>(`/watch-targets/${row.id}/earnings-preview`)
				detailEarningsPreview.value = earnings.data.data
			} catch {
				detailEarningsPreview.value = null
			}
			try {
				const rating = await apiClient.get<ApiResponse<AnalystRatingView>>(`/discovery/analyst-ratings/${encodeURIComponent(row.ticker)}`)
				detailAnalystRating.value = rating.data.data
			} catch {
				detailAnalystRating.value = null
			}
			try {
				const fairValue = await apiClient.get<ApiResponse<CandidateFairValueEstimate>>(`/discovery/fair-values/${encodeURIComponent(row.ticker)}`)
				detailFairValue.value = fairValue.data.data
			} catch {
				detailFairValue.value = null
			}
			try {
				const holdings = await apiClient.get<ApiResponse<TickerInstitutionalHoldingHistory>>(`/discovery/institutional-holdings/${encodeURIComponent(row.ticker)}`)
				detailInstitutionalHoldings.value = holdings.data.data
			} catch {
				detailInstitutionalHoldings.value = null
			}
			try {
				const history = await apiClient.get<ApiResponse<ProfitHistory>>(`/discovery/profit-history/${encodeURIComponent(row.ticker)}`)
				detailProfitHistory.value = history.data.data
			} catch {
				detailProfitHistory.value = null
			}
		}
		await loadTargetAIAnalyses()
  } finally {
    detailLoading.value = false
  }
}

async function loadAIProviders() {
	try {
		const [response, templateResponse] = await Promise.all([apiClient.get('/ai/providers'), apiClient.get('/ai/prompt-templates', { params: { scope: 'watch_target_detail' } })])
		aiProviders.value = response.data.data || []; aiPromptTemplates.value = templateResponse.data.data || []
		if (!targetAIProvider.value && aiProviders.value.length) targetAIProvider.value = aiProviders.value[0].id
		if (!targetAIPromptTemplate.value && aiPromptTemplates.value.length) targetAIPromptTemplate.value = aiPromptTemplates.value[0].id
	} catch { aiProviders.value = []; aiPromptTemplates.value = [] }
}

async function loadTargetAIAnalyses() {
	const ticker = detailTarget.value?.ticker
	if (!ticker) return
	try {
		const response = await apiClient.get('/ai/analyses', { params: { ticker, page: 1, page_size: 50 } })
		targetAIAnalyses.value = (response.data.data.items || []).filter((item: AIAnalysis & { scope?: string }) => item.scope === 'watch_target_detail')
		targetAIAnalysisID.value = targetAIAnalyses.value[0]?.id || null
		if (targetAIAnalyses.value.some((item) => item.status === 'queued' || item.status === 'running')) scheduleTargetAIPoll()
	} catch { targetAIAnalyses.value = [] }
}

function scheduleTargetAIPoll() {
	if (targetAIPollingTimer !== undefined) return
	targetAIPollingTimer = window.setTimeout(() => { targetAIPollingTimer = undefined; void loadTargetAIAnalyses() }, 2000)
}

async function generateTargetAI() {
	const target = detailTarget.value
	if (!target || !targetAIProvider.value || !targetAIPromptTemplate.value) return
	targetAIGenerating.value = true
	try {
		const context = { target, company_profile: detailCompanyProfile.value, analyst_rating: detailAnalystRating.value, fair_value: detailFairValue.value, institutional_holdings: detailInstitutionalHoldings.value, earnings_preview: detailEarningsPreview.value, technical_history: detailTechnicalHistory.value, trade_setup_history: detailTradeSetupHistory.value, recent_filings: detailFilings.value }
		const response = await apiClient.post('/ai/analyses', { provider_id: targetAIProvider.value, template_id: targetAIPromptTemplate.value, scope: 'watch_target_detail', ticker: target.ticker, company_name: target.company_name, target_type: target.target_type, context }, { timeout: 315000 })
		ElMessage.success('AI 研判已提交，正在后台处理')
		await loadTargetAIAnalyses()
		targetAIAnalysisID.value = response.data.data.id
	} catch (err: any) { ElMessage.error(err?.response?.data?.message || 'AI 研判请求超时或失败；请检查供应商配置、额度或适当提高模型超时后手动重试') } finally { targetAIGenerating.value = false }
}

async function refreshTargetEarningsPreview() {
  const target = detailTarget.value
  if (!target) return
  detailEarningsRefreshing.value = true
  try {
    const response = await apiClient.post<ApiResponse<EarningsPreviewRefreshResult>>(`/watch-targets/${target.id}/earnings-preview/refresh`)
    detailEarningsPreview.value = { preview: response.data.data.preview, message: response.data.data.message }
    if (response.data.data.preview) earningsPreviews.value[target.id] = response.data.data.preview
    ElMessage.success(response.data.data.message || '已更新财报预告')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '刷新财报预告失败')
  } finally {
    detailEarningsRefreshing.value = false
  }
}

async function backfillTargetTechnicalHistory() {
  const target = detailTarget.value
  if (!target) return
  detailTechnicalBackfilling.value = true
  try {
    const response = await apiClient.post<ApiResponse<{ persisted_count: number }>>(`/watch-targets/${target.id}/technical-history-backfill`, { lookback_days: 0 })
    ElMessage.success(`已保存 ${response.data.data.persisted_count} 条本地日线数据`)
    const history = await apiClient.get<ApiResponse<TickerTechnicalHistory>>(`/watch-targets/${target.id}/technical-history`)
    detailTechnicalHistory.value = history.data.data.history || []
    detailTechnicalAnalysis.value = history.data.data.technical
  } finally {
    detailTechnicalBackfilling.value = false
  }
}

async function refreshTargetCompanyProfile() {
  const target = detailTarget.value
  if (!target) return
  detailCompanyProfileRefreshing.value = true
  try {
    const response = await apiClient.post<ApiResponse<{ profile: CompanyProfile }>>(`/discovery/company-profiles/${encodeURIComponent(target.ticker)}/refresh`, null, { params: { cik: target.cik || undefined } })
    detailCompanyProfile.value = response.data.data.profile
    ElMessage.success('已更新 Longbridge 公司资料')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '刷新公司资料失败')
  } finally {
    detailCompanyProfileRefreshing.value = false
  }
}

async function refreshTargetAnalystRating() {
  const target = detailTarget.value
  if (!target) return
  detailAnalystRatingRefreshing.value = true
  try {
    const response = await apiClient.post<ApiResponse<{ rating: AnalystRatingView }>>(`/discovery/analyst-ratings/${encodeURIComponent(target.ticker)}/refresh`, null, { params: { cik: target.cik || undefined } })
    detailAnalystRating.value = response.data.data.rating
    ElMessage.success('已更新 Longbridge 分析师共识')
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '刷新分析师评级失败')
  } finally {
    detailAnalystRatingRefreshing.value = false
  }
}

async function refreshTargetValuationResearch() {
	const target = detailTarget.value
	if (!target) return
	detailValuationRefreshing.value = true
	try {
		await apiClient.post(`/discovery/valuation-research/${encodeURIComponent(target.ticker)}/refresh`, null, { params: { cik: target.cik || undefined } })
		const fairValue = await apiClient.get<ApiResponse<CandidateFairValueEstimate>>(`/discovery/fair-values/${encodeURIComponent(target.ticker)}`)
		detailFairValue.value = fairValue.data.data
		ElMessage.success('已更新 Longbridge 估值研究')
	} catch (err: any) {
		ElMessage.error(err?.response?.data?.message || '刷新估值研究失败')
	} finally {
		detailValuationRefreshing.value = false
	}
}

async function refreshTargetMarketResearch() {
	const target = detailTarget.value
	if (!target) return
	detailMarketResearchRefreshing.value = true
	try {
		await apiClient.post(`/discovery/market-research/${encodeURIComponent(target.ticker)}/refresh`, null, { params: { cik: target.cik || undefined } })
		const holdings = await apiClient.get<ApiResponse<TickerInstitutionalHoldingHistory>>(`/discovery/institutional-holdings/${encodeURIComponent(target.ticker)}`)
		detailInstitutionalHoldings.value = holdings.data.data
		ElMessage.success('已更新 Longbridge 机构持仓研究')
	} catch (err: any) {
		ElMessage.error(err?.response?.data?.message || '刷新机构持仓研究失败')
	} finally {
		detailMarketResearchRefreshing.value = false
	}
}

function analystRecommendationLabel(value?: string) {
  const labels: Record<string, string> = { strong_buy: '强烈买入', buy: '买入', hold: '持有', underperform: '跑输', sell: '卖出', strong_sell: '强烈卖出', no_opinion: '无观点', unknown: '未评级' }
  return labels[value || 'unknown'] || value || '未评级'
}

function analystRecommendationTagType(value?: string) {
  if (value === 'strong_buy' || value === 'buy') return 'success'
  if (value === 'sell' || value === 'strong_sell' || value === 'underperform') return 'danger'
  return 'info'
}

function formatAnalystPrice(micros?: number, currency?: string) {
  if (!micros) return '-'
  return `${currency || '$'}${(micros / 1_000_000).toLocaleString(undefined, { maximumFractionDigits: 2 })}`
}

function formatFairValuePrice(value?: number | null, currency?: string) {
	if (value === undefined || value === null || !Number.isFinite(value)) return '-'
	return `${currency || '$'}${value.toLocaleString(undefined, { maximumFractionDigits: 2 })}`
}

function analystRatingProvenanceRows(snapshot: AnalystRatingView['latest']) {
  if (!snapshot) return []
  const providerUpdatedAt = snapshot.provider_updated_at_text || '提供方未返回精确更新时间'
  const fetchedAt = formatDateTime(snapshot.fetched_at)
  const distribution = `强烈买入 ${snapshot.strong_buy_count} · 买入 ${snapshot.buy_count} · 持有 ${snapshot.hold_count} · 跑输 ${snapshot.underperform_count} · 卖出 ${snapshot.sell_count}`
  return [
    { result: '共识评级', value: analystRecommendationLabel(snapshot.recommendation), source: 'Longbridge InstitutionRating / Summary.Recommend', providerUpdatedAt, fetchedAt, note: '提供方汇总结论，不代表单一机构或分析师。' },
    { result: '覆盖数与分布', value: `${snapshot.analyst_count} 位覆盖；${distribution}`, source: 'Longbridge InstitutionRating / Latest.Evaluate', providerUpdatedAt, fetchedAt, note: '覆盖数及各评级档位均为提供方聚合口径。' },
    { result: '平均目标价', value: formatAnalystPrice(snapshot.target_average_micros, snapshot.currency), source: 'Longbridge InstitutionRating / Summary.Target', providerUpdatedAt, fetchedAt, note: '目标价为聚合值，非本系统估值计算。' },
    { result: '目标区间与参考价', value: `${formatAnalystPrice(snapshot.target_low_micros, snapshot.currency)} - ${formatAnalystPrice(snapshot.target_high_micros, snapshot.currency)}；参考 ${formatAnalystPrice(snapshot.reference_price_micros, snapshot.currency)}`, source: 'Longbridge InstitutionRating / Latest.Target', providerUpdatedAt, fetchedAt, note: '区间和参考收盘价以提供方返回值为准。' }
  ]
}

function analystProviderTimeText(snapshot: AnalystRatingView['latest']) {
  if (!snapshot) return '-'
  return snapshot.provider_updated_at_text || `提供方未返回；本地同步于 ${formatDateTime(snapshot.fetched_at)}`
}

function companyProfileWebsiteURL(value?: string) {
  const website = (value || '').trim()
  if (!website) return '#'
  return /^https?:\/\//i.test(website) ? website : `https://${website}`
}

async function remove(row: WatchTarget) {
  await ElMessageBox.confirm(t('messages.confirmDeleteTarget', { ticker: row.ticker }), t('messages.confirmDeleteTitle'), { type: 'warning' })
  await apiClient.delete(`/watch-targets/${row.id}`)
  await load()
}

function formatDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

function formatCompactDateTime(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
    hour12: false,
  }).format(date)
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toISOString().slice(0, 10)
}

function earningsPreviewFor(row: WatchTarget) {
  return earningsPreviews.value[row.id]
}

function earningsPreviewLabel(preview?: EarningsPreview) {
  if (!preview) return '未同步'
  if (preview.status === 'scheduled') return `${formatDate(preview.report_at)} · ${daysUntilEarnings(preview.report_at)}`
  if (preview.status === 'no_coverage') return '暂无覆盖'
  return '暂不可用'
}

function earningsPreviewTagType(preview?: EarningsPreview): 'success' | 'warning' | 'info' | 'danger' {
  if (!preview) return 'info'
  if (preview.status === 'scheduled') {
    const days = earningsDays(preview.report_at)
    return days !== null && days >= 0 && days <= 7 ? 'warning' : 'success'
  }
  return preview.status === 'unavailable' ? 'danger' : 'info'
}

function earningsPreviewTooltip(preview?: EarningsPreview) {
  if (!preview) return '尚未同步；可在详情中点击“刷新财报预告”。'
  if (preview.status === 'scheduled') {
    return `预计：${formatDate(preview.report_at)}${preview.session ? `，${preview.session}` : ''}\nEPS 预期：${formatEarningsValue(preview.eps_estimate, preview.currency, false)}\n收入预期：${formatEarningsValue(preview.revenue_estimate, preview.currency, true)}\n来源：Longbridge（本地更新 ${formatDateTime(preview.fetched_at)}）`
  }
  return preview.last_error || 'Longbridge 当前未返回该标的的未来财报日；这不表示公司不会发布财报。'
}

function earningsDays(value?: string | null) {
  if (!value) return null
  const report = new Date(value)
  if (Number.isNaN(report.getTime())) return null
  const today = new Date()
  const start = Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate())
  const end = Date.UTC(report.getUTCFullYear(), report.getUTCMonth(), report.getUTCDate())
  return Math.round((end - start) / 86400000)
}

function daysUntilEarnings(value?: string | null) {
  const days = earningsDays(value)
  if (days === null) return '日期待确认'
  if (days === 0) return '今日'
  return days > 0 ? `${days} 天后` : '已过期，待提供方更新'
}

function earningsFiscalPeriod(preview: EarningsPreview) {
  if (preview.fiscal_year && preview.fiscal_period) return `${preview.fiscal_year} ${preview.fiscal_period}`
  return preview.fiscal_period || '-'
}

function formatEarningsValue(value?: number | null, currency?: string, compact = false) {
  if (!Number.isFinite(value)) return '-'
  const prefix = currency || '$'
  const numeric = Number(value)
  if (compact && Math.abs(numeric) >= 1_000_000_000) return `${prefix}${(numeric / 1_000_000_000).toFixed(2)}B`
  if (compact && Math.abs(numeric) >= 1_000_000) return `${prefix}${(numeric / 1_000_000).toFixed(2)}M`
  return `${prefix}${numeric.toLocaleString(undefined, { maximumFractionDigits: compact ? 2 : 4 })}`
}

function formatPrice(value?: number | null) {
  return Number.isFinite(value) && Number(value) > 0 ? `$${Number(value).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}` : '-'
}

function formatSignedPct(value?: number | null) {
  return Number.isFinite(value) ? `${Number(value) >= 0 ? '+' : ''}${Number(value).toFixed(1)}%` : '-'
}

function formatPct(value?: number | null) {
	return Number.isFinite(value) ? `${Number(value).toFixed(1)}%` : '-'
}

function formatRMultiple(value?: number | null) {
	return Number.isFinite(value) ? `${Number(value).toFixed(2)}R` : '-'
}

function formatRatio(value?: number | null) {
  return Number.isFinite(value) && Number(value) > 0 ? `${Number(value).toFixed(2)}x` : '-'
}

function formatNotional(value?: number | null) {
  return Number.isFinite(value) && Number(value) > 0 ? `$${new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 2 }).format(Number(value))}` : '-'
}

function liquidityLabel(status?: string) {
  if (status === 'low') return '低流动性（20 日均额 < $1M）'
  if (status === 'limited') return '受限（20 日均额 < $5M）'
  if (status === 'normal') return '正常'
  return '历史不足'
}

function liquidityShortLabel(status?: string) {
  if (status === 'low') return '低'
  if (status === 'limited') return '受限'
  if (status === 'normal') return '正常'
  return '未知'
}

function technicalSignalsTooltip(technical?: CandidateTechnicalAnalysis | null) {
  return technical?.signals?.map((signal) => signal.label).join('、') || '暂无突破'
}

function liquidityTagType(status?: string) {
  if (status === 'low') return 'danger'
  if (status === 'limited') return 'warning'
  return 'success'
}

function tradeSetupLabel(status?: string) {
  if (status === 'entry_candidate') return '入场候选'
  if (status === 'watching') return '观察中'
  if (status === 'exit_warning') return '离场预警'
  if (status === 'invalidated') return '趋势失效'
  return '计划不可用'
}

function tradeSetupTagType(status?: string) {
  if (status === 'entry_candidate') return 'success'
  if (status === 'exit_warning') return 'warning'
  if (status === 'invalidated') return 'danger'
  return 'info'
}

function tradeSetupSummary(technical?: WatchTarget['technical']) {
  const setup = technical?.trade_setup
  const statusSince = setup?.status_since ? '；当前状态始于 ' + formatDateTime(setup.status_since) : '；状态起始时间将在下次日线同步后记录'
  if (!setup || setup.status === 'unavailable') return (setup?.reasons?.[0] || '日线历史不足') + statusSince
  const stop = formatPrice(setup.stop_loss_usd)
  if (setup.status === 'entry_candidate') return setup.entry_trigger + '；计划止损 ' + stop + '（风险 ' + formatSignedPct(setup.risk_pct) + '）' + statusSince
  if (setup.exit_reason) return setup.exit_reason + statusSince
  return (setup.reasons?.[0] || '等待触发条件') + statusSince
}

function simulationStatusLabel(status?: string) {
	if (status === 'open') return '进行中'
	if (status === 'stop_loss') return '止损退出'
	if (status === 'take_profit') return '目标退出'
	if (status === 'trend_exit') return '趋势退出'
	return status || '-'
}

function simulationStatusType(status?: string) {
	if (status === 'take_profit') return 'success'
	if (status === 'open') return 'info'
	if (status === 'trend_exit') return 'warning'
	return 'danger'
}

function targetTechnicalStatusLabel(target: WatchTarget) {
  if (target.technical?.status === 'corporate_action_review') return '待确认复权'
  if (target.technical?.status === 'data_insufficient') return '历史不足'
  if (target.technical?.status === 'missing') return '无行情数据'
  return '暂无技术数据'
}

function targetTechnicalStatusDescription(target: WatchTarget) {
  const technical = target.technical
  if (technical?.status === 'corporate_action_review') return technical.adjustment_review?.detail || '存在公司行动且价格复权状态未确认；技术信号已暂停。'
  if (technical?.status === 'data_insufficient') return `技术分析至少需要 ${technical.required_sample_days || 21} 个交易日，当前仅有 ${technical.sample_days || 0} 个。`
  return '尚未保存可用本地日线；请在详情中回填价格历史后计算技术信号。'
}

function syncStatusType(status?: string) {
  if (status === 'success') return 'success'
  if (status === 'failed') return 'danger'
  return 'info'
}

function syncStatusLabel(status?: string) {
  if (status === 'success') return t('status.success')
  if (status === 'failed') return t('status.failed')
  if (status === 'running') return t('status.running')
  return '-'
}

function targetStatusLabel(status?: string) {
  if (status === 'enabled') return t('status.enabled')
  if (status === 'disabled') return t('status.disabled')
  return status || '-'
}

function formatDuration(value: number) {
  if (!value) return '-'
  if (value < 1000) return `${value} ms`
  return `${(value / 1000).toFixed(1)} s`
}

function syncIssueTitle(target: WatchTarget) {
  return t('pages.targets.syncIssueTitle', { ticker: target.ticker })
}

function syncIssueSuggestion(target: WatchTarget) {
  const message = target.last_sync_error || ''
  if (message.toLowerCase().includes('cik')) return t('pages.targets.syncIssueCik')
  if (message.toLowerCase().includes('timeout') || message.includes('deadline')) return t('pages.targets.syncIssueTimeout')
  if (message.toLowerCase().includes('telegram')) return t('pages.targets.syncIssueTelegram')
  return message || t('pages.targets.syncIssueDefault')
}

onMounted(() => {
  const ticker = route.query.ticker
  if (typeof ticker === 'string') {
    filters.ticker = ticker
  }
  const status = route.query.status
  if (typeof status === 'string') {
    filters.status = status
  }
  load()
	void loadAIProviders()
})
onUnmounted(() => { if (targetAIPollingTimer !== undefined) window.clearTimeout(targetAIPollingTimer) })
</script>

<style scoped>
.target-list-table :deep(.el-table__cell) {
  padding: 4px 0;
}

.target-list-table :deep(.cell) {
  line-height: 18px;
}

.target-list-table :deep(.el-table__row) {
  height: 42px;
}

.target-list-table :deep(.el-tag) {
  max-width: 100%;
  height: 22px;
  padding: 0 7px;
  line-height: 20px;
}

.target-identity {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: 8px;
  white-space: nowrap;
}

.target-ticker {
  flex: 0 0 auto;
  font-weight: 700;
}

.target-company {
  min-width: 0;
  overflow: hidden;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  text-overflow: ellipsis;
}

.target-status-cell,
.target-signals {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 5px;
  white-space: nowrap;
}

.target-signals {
  overflow: hidden;
}

.target-signals :deep(.el-tag) {
  flex: 0 0 auto;
}

.target-signals :deep(.target-primary-signal) {
  min-width: 0;
  max-width: 78px;
}

.target-signals :deep(.target-primary-signal .el-tag__content) {
  overflow: hidden;
  text-overflow: ellipsis;
}

.target-signal-more {
  flex: 0 0 auto;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 1px;
}

.target-sync-time {
  color: var(--el-text-color-regular);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

@media (max-width: 900px) {
  .target-list-table :deep(.el-table__row) {
    height: 40px;
  }
}
</style>
