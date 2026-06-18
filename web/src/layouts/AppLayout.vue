<template>
  <el-container class="app-shell">
    <el-aside width="240px" class="sidebar">
      <div class="brand">
        <el-icon><Monitor /></el-icon>
        <span>{{ t('app.title') }}</span>
      </div>
      <el-menu router :default-active="$route.path">
        <div class="nav-section-label">{{ t('nav.monitor') }}</div>
        <el-menu-item index="/"><el-icon><DataBoard /></el-icon><span>{{ t('nav.dashboard') }}</span></el-menu-item>
        <el-menu-item index="/targets"><el-icon><Aim /></el-icon><span>{{ t('nav.targets') }}</span></el-menu-item>
        <el-menu-item index="/filings"><el-icon><Document /></el-icon><span>{{ t('nav.filings') }}</span></el-menu-item>

        <div class="nav-section-label">{{ t('nav.automation') }}</div>
        <el-menu-item index="/sync-runs"><el-icon><DataAnalysis /></el-icon><span>{{ t('nav.syncRuns') }}</span></el-menu-item>
        <el-menu-item index="/scheduler"><el-icon><Timer /></el-icon><span>{{ t('nav.scheduler') }}</span></el-menu-item>

        <div class="nav-section-label">{{ t('nav.settings') }}</div>
        <el-menu-item index="/telegram"><el-icon><Bell /></el-icon><span>Telegram</span></el-menu-item>
        <el-menu-item index="/configs"><el-icon><Setting /></el-icon><span>{{ t('nav.configs') }}</span></el-menu-item>

        <div class="nav-section-label">{{ t('nav.logs') }}</div>
        <el-menu-item index="/audit-logs"><el-icon><Tickets /></el-icon><span>{{ t('nav.auditLogs') }}</span></el-menu-item>
        <el-menu-item index="/notification-logs"><el-icon><Notification /></el-icon><span>{{ t('nav.notificationLogs') }}</span></el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="topbar">
        <span>{{ t('app.topbar') }}</span>
        <div class="language-switch" :aria-label="t('app.language')">
          <el-button size="small" :type="store.locale === 'zh-CN' ? 'primary' : 'default'" @click="store.setLocale('zh-CN')">中文</el-button>
          <el-button size="small" :type="store.locale === 'en-US' ? 'primary' : 'default'" @click="store.setLocale('en-US')">EN</el-button>
        </div>
      </el-header>
      <el-main>
        <RouterView />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { Aim, Bell, DataAnalysis, DataBoard, Document, Monitor, Notification, Setting, Tickets, Timer } from '@element-plus/icons-vue'
import { useI18n } from '@/i18n'

const { store, t } = useI18n()
</script>
