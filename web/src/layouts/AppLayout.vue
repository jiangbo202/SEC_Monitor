<template>
  <el-container class="app-shell">
    <el-aside width="240px" class="sidebar">
      <div class="brand">
        <el-icon><Monitor /></el-icon>
        <span>SEC Monitor</span>
      </div>
      <el-menu router :default-active="$route.path">
        <div class="nav-section-label">监控</div>
        <el-menu-item index="/"><el-icon><DataBoard /></el-icon><span>总览</span></el-menu-item>
        <el-menu-item index="/targets"><el-icon><Aim /></el-icon><span>监控标的</span></el-menu-item>
        <el-menu-item index="/filings"><el-icon><Document /></el-icon><span>SEC 公告</span></el-menu-item>

        <div class="nav-section-label">自动化</div>
        <el-menu-item index="/sync-runs"><el-icon><DataAnalysis /></el-icon><span>同步历史</span></el-menu-item>
        <el-menu-item index="/scheduler"><el-icon><Timer /></el-icon><span>调度任务</span></el-menu-item>

        <div class="nav-section-label">通知与配置</div>
        <el-menu-item index="/telegram"><el-icon><Bell /></el-icon><span>Telegram</span></el-menu-item>
        <el-menu-item index="/configs"><el-icon><Setting /></el-icon><span>系统配置</span></el-menu-item>

        <div class="nav-section-label">日志</div>
        <el-menu-item index="/audit-logs"><el-icon><Tickets /></el-icon><span>审计日志</span></el-menu-item>
        <el-menu-item index="/notification-logs"><el-icon><Notification /></el-icon><span>通知日志</span></el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="topbar">SEC Filing Monitoring Console</el-header>
      <el-main>
        <RouterView />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { Aim, Bell, DataAnalysis, DataBoard, Document, Monitor, Notification, Setting, Tickets, Timer } from '@element-plus/icons-vue'
</script>
