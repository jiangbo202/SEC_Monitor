<template>
  <el-collapse class="ai-request-prompt" @change="loadPrompts">
    <el-collapse-item name="prompt">
      <template #title>
        <span>发送给 AI 的提示词</span>
        <small>系统指令与本次事实研究包（已过滤评分与空值）</small>
      </template>
      <el-alert v-if="loading" title="读取当次保存的提示词…" type="info" :closable="false" />
      <el-alert v-else-if="error" :title="error" type="error" :closable="false"><el-button link @click="loadPrompts(['prompt'])">重试</el-button></el-alert>
      <template v-else-if="systemPrompt || userPrompt || storedSystem || storedUser">
        <section v-if="systemPrompt || storedSystem" class="prompt-section">
          <h4>系统指令</h4>
          <pre>{{ systemPrompt || storedSystem }}</pre>
        </section>
        <section v-if="userPrompt || storedUser" class="prompt-section">
          <h4>用户提示词</h4>
          <pre>{{ userPrompt || storedUser }}</pre>
        </section>
      </template>
      <el-alert v-else type="info" :closable="false" title="该历史记录生成于提示词留存功能上线前，无法回溯实际发送内容。" />
    </el-collapse-item>
  </el-collapse>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { apiClient } from '@/api/client'
const props = defineProps<{ systemPrompt?: string; userPrompt?: string; analysisId?: number }>()
const loading=ref(false), error=ref(''), storedSystem=ref(''), storedUser=ref('')
let loadedId:number|undefined, generation=0, expanded=false
watch(()=>props.analysisId,()=>{generation++;loadedId=undefined;storedSystem.value='';storedUser.value='';error.value='';loading.value=false;if(expanded)void loadPrompts(['prompt'])})
async function loadPrompts(names:string|string[]) {
  expanded=names.includes('prompt')
  if(!expanded || !props.analysisId || loading.value || loadedId===props.analysisId || props.systemPrompt || props.userPrompt)return
  const id=props.analysisId, current=++generation
  loading.value=true;error.value=''
  try {const response=await apiClient.get(`/ai/analyses/${id}`,{params:{view:'prompts'}});if(current!==generation)return;storedSystem.value=response.data.data.system_prompt||'';storedUser.value=response.data.data.user_prompt||'';loadedId=id}
  catch(e:any){if(current===generation)error.value=e?.response?.data?.message||'读取提示词失败'}
  finally{if(current===generation)loading.value=false}
}
</script>

<style scoped>
.ai-request-prompt { margin-top: 16px; }
.ai-request-prompt :deep(.el-collapse-item__header) { font-weight: 600; }
.ai-request-prompt small { margin-left: 10px; color: var(--el-text-color-secondary); font-weight: normal; }
.prompt-section + .prompt-section { margin-top: 12px; }
.prompt-section h4 { margin: 0 0 6px; font-size: 13px; color: var(--el-text-color-regular); }
.prompt-section pre { max-height: 360px; margin: 0; overflow: auto; padding: 12px; border-radius: 6px; white-space: pre-wrap; overflow-wrap: anywhere; background: var(--el-fill-color-light); color: var(--el-text-color-primary); font: 12px/1.65 var(--el-font-family-mono); }
</style>
