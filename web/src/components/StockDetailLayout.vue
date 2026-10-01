<template>
  <div ref="layout" class="stock-detail-layout">
    <header class="stock-detail-identity">
      <div><strong>{{ ticker }}</strong><span>{{ companyName || '公司名称未提供' }}</span></div>
      <el-tag size="small" type="info" effect="plain">{{ contextLabel }}</el-tag>
      <slot name="actions" />
    </header>
    <nav class="stock-detail-navigation" aria-label="标的详情分区">
      <button v-for="section in stockDetailSections" :id="`${id}-${section.key}-control`" :key="section.key"
        type="button" :aria-pressed="active === section.key" :aria-controls="`${id}-panel`"
        @click="select(section.key)">{{ section.label }}</button>
    </nav>
    <section :id="`${id}-panel`" class="stock-detail-panel" :aria-labelledby="`${id}-${active}-control`">
      <p class="stock-detail-description">{{ currentSection.description }}</p>
      <p class="stock-detail-description" role="status">{{ active === 'research' ? 'AI 仅在手动生成时更新，不会随页面打开自动调用。' : '此处展示本地快照；后台按各任务的开关、预算和周期轮转，不保证所有股票每天更新。刷新按钮会查询外部数据源，重新打开详情只读取本地记录。' }} 数据空白不等于零；无覆盖不会因反复刷新而补齐。页面保持打开时不会自动重载。</p>
      <!-- Only mount the selected group: hidden charts and long audit tables do
           not render until requested. Page state and fetched records stay local. -->
      <div :key="active" class="stock-detail-content">
        <slot :name="active"><el-empty :image-size="44" description="此入口暂无该分区数据，可进入研究工作台查看其他本地证据。" /></slot>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import { stockDetailSections, type StockDetailSection } from '@/utils/stockDetailSections'

const props = defineProps<{ ticker: string; companyName?: string; contextLabel: string; opened: boolean }>()
const emit = defineEmits<{ change: [section: StockDetailSection] }>()
const id = `stock-detail-${useId()}`
const layout = ref<HTMLElement | null>(null)
const active = ref<StockDetailSection>('overview')
const currentSection = computed(() => stockDetailSections.find(section => section.key === active.value)!)
async function select(section: StockDetailSection) {
  active.value = section
  emit('change', section)
  await nextTick()
  layout.value?.closest('.el-drawer__body')?.scrollTo({ top: 0 })
}
watch(() => [props.ticker, props.opened], () => { active.value = 'overview' })
</script>

<style scoped>
.stock-detail-layout { min-width: 0; }
.stock-detail-identity { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; margin-bottom: 12px; }
.stock-detail-identity > div { flex: 1 1 240px; min-width: 0; display: flex; flex-direction: column; gap: 5px; }
.stock-detail-identity strong { font-size: 20px; }
.stock-detail-identity span { color: var(--el-text-color-secondary); overflow-wrap: anywhere; }
.stock-detail-navigation { position: sticky; top: -20px; z-index: 5; display: grid; grid-template-columns: repeat(8, minmax(0, 1fr)); gap: 4px; padding: 8px 0; background: var(--el-bg-color); border-bottom: 1px solid var(--el-border-color-lighter); }
.stock-detail-navigation button { min-height: 36px; padding: 6px 4px; border: 1px solid transparent; border-radius: 4px; color: var(--el-text-color-regular); background: var(--el-fill-color-light); font: inherit; font-size: 13px; cursor: pointer; }
.stock-detail-navigation button[aria-pressed="true"] { background: var(--el-color-primary-light-9); border-color: var(--el-color-primary-light-5); color: var(--el-color-primary); font-weight: 600; }
.stock-detail-navigation button:focus-visible { outline: 2px solid var(--el-color-primary); outline-offset: 2px; }
.stock-detail-description { color: var(--el-text-color-secondary); font-size: 12px; line-height: 1.6; margin: 12px 0; }
.stock-detail-content { display: flex; flex-direction: column; gap: 16px; min-width: 0; }
.stock-detail-content :deep(.panel-header), .stock-detail-content :deep(.card-header-actions) { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 10px; }
.stock-detail-content :deep(.el-space) { flex-wrap: wrap; max-width: 100%; }
.stock-detail-content :deep(.el-space__item) { max-width: 100%; }
.stock-detail-content :deep(.el-select) { max-width: 100%; }
.stock-detail-content :deep(.el-descriptions__table) { table-layout: fixed; overflow-wrap: anywhere; }
@media (max-width: 640px) {
  .stock-detail-navigation { grid-template-columns: repeat(4, minmax(0, 1fr)); }
  .stock-detail-content :deep(.el-card__header), .stock-detail-content :deep(.el-card__body) { padding: 12px; }
}
</style>
