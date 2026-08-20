<template>
  <div v-if="result" class="structured-result">
    <div class="result-heading">
      <el-tag :type="stanceType" effect="dark">{{ stanceLabel }}</el-tag>
      <el-tag effect="plain">证据充分程度：{{ sufficiencyLabel }}</el-tag>
      <el-tag size="small" type="info" effect="plain">{{ result.schema_version }}</el-tag>
    </div>
    <p class="conclusion">{{ result.conclusion }}</p>

    <section v-if="result.evidence?.length">
      <h4>关键证据与推断</h4>
      <div v-for="(item, index) in result.evidence" :key="`evidence-${index}`" class="evidence-card">
        <p><strong>事实</strong>{{ item.fact }}</p>
        <p><strong>推断</strong>{{ item.inference }}</p>
        <p><strong>影响</strong>{{ item.impact }}</p>
        <div class="source-paths"><span>事实依据</span><el-tag v-for="path in item.source_paths" :key="path" size="small" effect="plain">{{ path }}</el-tag></div>
      </div>
    </section>

    <section v-if="result.counter_evidence?.length">
      <h4>反面证据</h4>
      <div v-for="(item, index) in result.counter_evidence" :key="`counter-${index}`" class="evidence-card counter">
        <p><strong>事实</strong>{{ item.fact }}</p>
        <p><strong>推断</strong>{{ item.inference }}</p>
        <p><strong>影响</strong>{{ item.impact }}</p>
        <div class="source-paths"><span>事实依据</span><el-tag v-for="path in item.source_paths" :key="path" size="small" effect="plain">{{ path }}</el-tag></div>
      </div>
    </section>

    <div class="list-grid">
      <section v-for="section in listSections" :key="section.title" v-show="section.items.length" class="list-card">
        <h4>{{ section.title }}</h4>
        <ul><li v-for="(item, index) in section.items" :key="index">{{ item }}</li></ul>
      </section>
    </div>
  </div>
  <MarkdownContent v-else :content="content || ''" />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import MarkdownContent from '@/components/MarkdownContent.vue'
import type { AIAnalysisStructuredResult } from '@/api/types'

const props = defineProps<{ result?: AIAnalysisStructuredResult | null; content?: string }>()
const stanceLabel = computed(() => ({ focus: '关注', watch: '观望', avoid: '回避', insufficient_evidence: '证据不足' } as Record<string, string>)[props.result?.stance || ''] || props.result?.stance || '-')
const stanceType = computed(() => ({ focus: 'success', watch: 'warning', avoid: 'danger', insufficient_evidence: 'info' } as Record<string, 'success' | 'warning' | 'danger' | 'info'>)[props.result?.stance || ''] || 'info')
const sufficiencyLabel = computed(() => ({ high: '高', medium: '中', low: '低' } as Record<string, string>)[props.result?.evidence_sufficiency || ''] || props.result?.evidence_sufficiency || '-')
const listSections = computed(() => [
  { title: '判断失效条件', items: props.result?.invalidation_conditions || [] },
  { title: '近期催化剂与观察点', items: props.result?.catalysts || [] },
  { title: '主要风险', items: props.result?.risk_notes || [] },
  { title: '数据缺口与下一步验证', items: props.result?.data_gaps || [] }
])
</script>

<style scoped>
.structured-result { color: var(--el-text-color-primary); }
.result-heading { align-items: center; display: flex; flex-wrap: wrap; gap: 8px; }
.conclusion { font-size: 15px; line-height: 1.75; margin: 14px 0 20px; }
h4 { margin: 18px 0 10px; }
.evidence-card { background: var(--el-fill-color-light); border-left: 3px solid var(--el-color-primary); border-radius: 4px; margin-bottom: 10px; padding: 10px 14px; }
.evidence-card.counter { border-left-color: var(--el-color-warning); }
.evidence-card p { line-height: 1.65; margin: 5px 0; }
.evidence-card strong { display: inline-block; margin-right: 10px; min-width: 36px; }
.source-paths { align-items: center; display: flex; flex-wrap: wrap; gap: 6px; margin-top: 8px; }
.source-paths > span { color: var(--el-text-color-secondary); font-size: 12px; margin-right: 4px; }
.list-grid { display: grid; gap: 12px; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); margin-top: 12px; }
.list-card { background: var(--el-fill-color-lighter); border-radius: 4px; padding: 1px 14px 8px; }
.list-card ul { line-height: 1.7; margin: 0; padding-left: 20px; }
</style>
