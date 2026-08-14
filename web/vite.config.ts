import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url))
    }
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080'
    }
  },
  build: {
    rollupOptions: {
      output: {
        // Keep vendor code cacheable independently from application pages.
        // Routes are already lazy-loaded; these stable chunks prevent a small
        // page edit from invalidating the large Element Plus runtime as well.
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          if (id.includes('element-plus') || id.includes('@element-plus')) return 'vendor-element-plus'
          if (id.includes('/vue/') || id.includes('/@vue/') || id.includes('vue-router') || id.includes('pinia')) return 'vendor-vue'
          if (id.includes('marked') || id.includes('dompurify')) return 'vendor-markdown'
          return 'vendor-other'
        }
      }
    }
  }
})
