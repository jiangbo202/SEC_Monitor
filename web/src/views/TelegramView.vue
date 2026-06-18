<template>
  <section class="page">
    <div class="page-header">
      <h1>{{ t('pages.telegram.title') }}</h1>
      <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
    </div>
    <el-card shadow="never" class="form-card">
      <el-form :model="form" label-width="120px">
        <el-form-item label="Bot Token">
          <el-input v-model="form.bot_token" show-password :placeholder="t('pages.telegram.tokenPlaceholder')" />
        </el-form-item>
        <el-form-item label="Chat ID"><el-input v-model="form.chat_id" /></el-form-item>
        <el-form-item :label="t('pages.telegram.enableNotification')"><el-switch v-model="form.enabled" /></el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="saving" @click="save">{{ t('common.save') }}</el-button>
          <el-button :loading="testing" @click="testSend">{{ t('pages.telegram.testSend') }}</el-button>
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
    ElMessage.success(t('messages.telegramSaved'))
    await load()
  } finally {
    saving.value = false
  }
}

async function testSend() {
  testing.value = true
  try {
    await apiClient.post('/telegram/test')
    ElMessage.success(t('messages.telegramTestSent'))
  } catch (error: any) {
    ElMessage.error(error?.response?.data?.message || t('messages.telegramTestFailed'))
  } finally {
    testing.value = false
  }
}

onMounted(load)
</script>
