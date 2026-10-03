<template>
  <section class="provider-history" :aria-label="title">
    <header class="provider-heading">
      <h3>{{ title }}</h3>
      <div class="provider-actions"><slot name="actions" /></div>
    </header>
    <div class="provider-content"><slot /></div>
  </section>
</template>
<script setup lang="ts">
defineProps<{ title: string }>()
</script>
<style scoped>
.provider-history { min-width: 0; padding: 20px; border: 1px solid var(--el-border-color-light); border-radius: 8px; background: var(--el-bg-color); }
.provider-heading { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 12px; padding-bottom: 14px; border-bottom: 1px solid var(--el-border-color-lighter); }
.provider-heading h3 { margin: 0; font-size: 16px; color: var(--el-text-color-primary); }
.provider-actions { display: flex; max-width: 100%; }
.provider-content { min-width: 0; }
@media (max-width: 600px) {
  .provider-history { padding: 14px; }
  .provider-actions { width: 100%; }
  .provider-actions :deep(.el-button) { max-width: 100%; height: auto; min-height: 32px; white-space: normal; }
}
</style>
