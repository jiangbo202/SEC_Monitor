<template>
  <div class="markdown-content" v-html="rendered" />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import DOMPurify from 'dompurify'
import { marked } from 'marked'

const props = defineProps<{ content?: string }>()

const rendered = computed(() => {
  const markdown = String(props.content || '')
  const html = String(marked.parse(markdown, { async: false, gfm: true, breaks: true }))
  return DOMPurify.sanitize(html, { USE_PROFILES: { html: true } })
})
</script>

<style scoped>
.markdown-content { color: var(--el-text-color-primary); line-height: 1.75; overflow-wrap: anywhere; }
.markdown-content :deep(h1), .markdown-content :deep(h2), .markdown-content :deep(h3), .markdown-content :deep(h4) { line-height: 1.35; margin: 1.25em 0 .65em; }
.markdown-content :deep(h1) { font-size: 22px; }.markdown-content :deep(h2) { font-size: 19px; }.markdown-content :deep(h3) { font-size: 16px; }
.markdown-content :deep(p) { margin: .65em 0; }.markdown-content :deep(ul), .markdown-content :deep(ol) { margin: .65em 0; padding-left: 1.5em; }.markdown-content :deep(li + li) { margin-top: .25em; }
.markdown-content :deep(blockquote) { margin: .8em 0; padding: .45em .9em; border-left: 4px solid var(--el-color-primary-light-5); color: var(--el-text-color-regular); background: var(--el-fill-color-light); }
.markdown-content :deep(hr) { border: 0; border-top: 1px solid var(--el-border-color); margin: 1.2em 0; }.markdown-content :deep(code) { padding: .12em .35em; border-radius: 4px; background: var(--el-fill-color); font-family: var(--el-font-family-mono); font-size: .9em; }
.markdown-content :deep(pre) { overflow: auto; padding: 12px; border-radius: 6px; background: var(--el-fill-color-dark); }.markdown-content :deep(pre code) { padding: 0; background: transparent; }
.markdown-content :deep(table) { display: block; width: max-content; max-width: 100%; overflow: auto; border-collapse: collapse; margin: 1em 0; }.markdown-content :deep(th), .markdown-content :deep(td) { padding: 8px 10px; border: 1px solid var(--el-border-color-lighter); text-align: left; }.markdown-content :deep(th) { background: var(--el-fill-color-light); }
.markdown-content :deep(a) { color: var(--el-color-primary); }
</style>
