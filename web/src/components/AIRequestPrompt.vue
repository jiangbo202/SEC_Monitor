<template>
  <el-collapse class="ai-request-prompt">
    <el-collapse-item name="prompt">
      <template #title>
        <span>发送给 AI 的提示词</span>
        <small>系统指令与本次事实研究包（已过滤评分与空值）</small>
      </template>
      <template v-if="systemPrompt || userPrompt">
        <section v-if="systemPrompt" class="prompt-section">
          <h4>系统指令</h4>
          <pre>{{ systemPrompt }}</pre>
        </section>
        <section v-if="userPrompt" class="prompt-section">
          <h4>用户提示词</h4>
          <pre>{{ userPrompt }}</pre>
        </section>
      </template>
      <el-alert v-else type="info" :closable="false" title="该历史记录生成于提示词留存功能上线前，无法回溯实际发送内容。" />
    </el-collapse-item>
  </el-collapse>
</template>

<script setup lang="ts">
defineProps<{ systemPrompt?: string; userPrompt?: string }>()
</script>

<style scoped>
.ai-request-prompt { margin-top: 16px; }
.ai-request-prompt :deep(.el-collapse-item__header) { font-weight: 600; }
.ai-request-prompt small { margin-left: 10px; color: var(--el-text-color-secondary); font-weight: normal; }
.prompt-section + .prompt-section { margin-top: 12px; }
.prompt-section h4 { margin: 0 0 6px; font-size: 13px; color: var(--el-text-color-regular); }
.prompt-section pre { max-height: 360px; margin: 0; overflow: auto; padding: 12px; border-radius: 6px; white-space: pre-wrap; overflow-wrap: anywhere; background: var(--el-fill-color-light); color: var(--el-text-color-primary); font: 12px/1.65 var(--el-font-family-mono); }
</style>
