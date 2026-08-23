<template>
  <div class="page-container">
    <div class="page-header"><div><h2>期权与多空研究</h2><p>保存 Longbridge 的 Call/Put 汇总成交量与空头持仓快照，用于观察多空指标，不代表真实全市场净仓位。</p></div></div>
    <el-alert type="info" :closable="false" show-icon title="P0 为日级汇总快照：不保存完整期权链或逐合约报价；异常标签依据本地历史成交量与阈值生成，不参与基本面总分。" />
    <el-card shadow="never" class="query-card"><el-form inline @submit.prevent="load"><el-form-item label="标的"><el-input v-model="ticker" placeholder="例如 NVDA / SPY" clearable @keyup.enter="load" /></el-form-item><el-button @click="load">查询本地快照</el-button><el-button type="primary" :loading="refreshing" @click="refresh">刷新 Longbridge 数据</el-button></el-form></el-card>
    <template v-if="research">
      <el-alert v-if="research.message" type="info" :closable="false" class="message" :title="research.message" />
      <el-empty v-if="!research.latest" description="暂无快照；可点击“刷新 Longbridge 数据”，或等待已开启的候选 / 监控标的任务。" />
      <template v-else>
        <el-row :gutter="16" class="summary"><el-col :xs="24" :md="8"><el-card shadow="never"><template #header>期权成交量</template><el-descriptions :column="1"><el-descriptions-item label="Call">{{ integer(research.latest.call_volume) }}</el-descriptions-item><el-descriptions-item label="Put">{{ integer(research.latest.put_volume) }}</el-descriptions-item><el-descriptions-item label="Put / Call">{{ decimal(research.latest.put_call_volume_ratio) }}</el-descriptions-item><el-descriptions-item label="期权数据日">{{ research.latest.option_volume_as_of || research.latest.observed_date }}</el-descriptions-item></el-descriptions></el-card></el-col><el-col :xs="24" :md="8"><el-card shadow="never"><template #header>空头持仓</template><el-descriptions :column="1"><el-descriptions-item label="空头比例">{{ pct(research.latest.short_ratio_pct) }}</el-descriptions-item><el-descriptions-item label="空头股数">{{ integer(research.latest.current_shares_short) }}</el-descriptions-item><el-descriptions-item label="日均成交量">{{ integer(research.latest.avg_daily_share_volume) }}</el-descriptions-item><el-descriptions-item label="days to cover">{{ decimal(research.latest.days_to_cover) }}</el-descriptions-item></el-descriptions></el-card></el-col><el-col :xs="24" :md="8"><el-card shadow="never"><template #header>研究提示</template><el-empty v-if="!(research.latest.anomalies || []).length" description="当前无显著异常标签" :image-size="44" /><div v-else class="anomalies"><el-tag v-for="item in research.latest.anomalies" :key="item.kind" :type="item.severity === 'warning' ? 'warning' : 'info'" effect="plain">{{ item.label }}</el-tag><p v-for="item in research.latest.anomalies" :key="item.kind + '-detail'">{{ item.detail }}</p></div></el-card></el-col></el-row>
        <el-card shadow="never" class="history"><template #header><strong>历史快照</strong></template><el-table :data="research.history || []" border><el-table-column prop="observed_date" label="快照日" width="120" /><el-table-column label="Call" align="right"><template #default="{ row }">{{ integer(row.call_volume) }}</template></el-table-column><el-table-column label="Put" align="right"><template #default="{ row }">{{ integer(row.put_volume) }}</template></el-table-column><el-table-column label="Put/Call" align="right"><template #default="{ row }">{{ decimal(row.put_call_volume_ratio) }}</template></el-table-column><el-table-column label="空头比例" align="right"><template #default="{ row }">{{ pct(row.short_ratio_pct) }}</template></el-table-column><el-table-column label="days to cover" align="right"><template #default="{ row }">{{ decimal(row.days_to_cover) }}</template></el-table-column><el-table-column label="提示" min-width="220"><template #default="{ row }">{{ (row.anomalies || []).map((item: any) => item.label).join('；') || '-' }}</template></el-table-column><el-table-column label="同步时间" width="170"><template #default="{ row }">{{ formatDate(row.fetched_at) }}</template></el-table-column></el-table></el-card>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
const ticker = ref('')
const research = ref<any>(null)
const refreshing = ref(false)
function symbol() { return ticker.value.trim().toUpperCase() }
async function load() { if (!symbol()) { ElMessage.warning('请输入标的代码'); return }; try { const res = await apiClient.get(`/discovery/options/${symbol()}`); research.value = res.data.data; ticker.value = symbol() } catch (err: any) { ElMessage.error(err?.response?.data?.message || '查询期权研究失败') } }
async function refresh() { if (!symbol()) { ElMessage.warning('请输入标的代码'); return }; refreshing.value = true; try { const res = await apiClient.post(`/discovery/options/${symbol()}/refresh`, {}, { timeout: 60000 }); research.value = res.data.data.research; ElMessage.success(res.data.data.refresh?.message || '已刷新') } catch (err: any) { ElMessage.error(err?.response?.data?.message || '刷新失败，请检查 Longbridge 权限与配置') } finally { refreshing.value = false } }
function integer(value?: number) { return Number.isFinite(value) ? Number(value).toLocaleString('en-US') : '-' }
function decimal(value?: number) { return Number.isFinite(value) ? Number(value).toFixed(2) : '-' }
function pct(value?: number) { return Number.isFinite(value) ? `${Number(value).toFixed(2)}%` : '-' }
function formatDate(value?: string) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' }) : '-' }
</script>

<style scoped>
.query-card,.message,.summary,.history{margin-top:12px}.anomalies{display:flex;flex-direction:column;align-items:flex-start;gap:6px}.anomalies p{font-size:12px;color:var(--el-text-color-secondary);margin:0}
</style>
