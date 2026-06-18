<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.telegram.title') }}</h1>
      <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
    </div>
    <el-card shadow="never" class="form-card">
      <el-form :model="form" label-width="120px">
        <el-form-item label="Bot Token">
          <el-input v-model="form.bot_token" show-password placeholder="输入新 Token；保留脱敏值则不更新" />
        </el-form-item>
        <el-form-item label="Chat ID"><el-input v-model="form.chat_id" /></el-form-item>
        <el-form-item label="启用通知"><el-switch v-model="form.enabled" /></el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="saving" @click="save">{{ t('common.save') }}</el-button>
          <el-button :loading="testing" @click="testSend">测试发送</el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient } from '@/api/client'
import type { ApiResponse, SystemConfig } from '@/api/types'
import { useI18n } from '@/i18n'

const { t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const testing = ref(false)
const form = reactive({ bot_token: '', chat_id: '', enabled: false })

async function load() {
  loading.value = true
  try {
    const res = await apiClient.get<ApiResponse<SystemConfig[]>>('/telegram/config')
    for (const cfg of res.data.data) {
      if (cfg.config_key === 'telegram.bot_token') form.bot_token = cfg.config_value
      if (cfg.config_key === 'telegram.chat_id') form.chat_id = cfg.config_value
      if (cfg.config_key === 'telegram.enabled') form.enabled = cfg.config_value === 'true'
    }
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    await apiClient.put('/telegram/config', form)
    ElMessage.success('Telegram 配置已保存')
    await load()
  } finally {
    saving.value = false
  }
}

async function testSend() {
  testing.value = true
  try {
    await apiClient.post('/telegram/test')
    ElMessage.success('测试消息已发送')
  } catch (error: any) {
    ElMessage.error(error?.response?.data?.message || '测试发送失败，请检查 Bot Token 和 Chat ID')
  } finally {
    testing.value = false
  }
}

onMounted(load)
</script>
