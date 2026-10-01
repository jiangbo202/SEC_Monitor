<template>
  <p class="stock-data-status" role="status">{{ label }}：{{ state.label }} · 本地同步：{{ state.synced ? new Date(state.synced).toLocaleString() : '未记录' }}<span v-if="state.asOf"> · 数据截至：{{ state.asOf }}</span></p>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { stockDataStatus } from '@/utils/stockDataStatus'
const props = defineProps<{ label: string; data?: unknown }>()
const state = computed(() => stockDataStatus(props.data))
</script>
<style scoped>
.stock-data-status { margin: 0; color: var(--el-text-color-secondary); font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }
</style>
