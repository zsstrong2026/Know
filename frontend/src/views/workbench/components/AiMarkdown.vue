<template>
  <!-- 侧栏 AI 回答的 Markdown 渲染：只做解析 + 消毒，不加载 mermaid（侧栏用不到） -->
  <div class="ai-markdown" v-html="html" />
</template>

<script setup lang="ts">
/**
 * 紧凑版 Markdown 渲染。
 *
 * 与 PmMarkdown 的差异：不带 mermaid 水合 —— 侧栏对话里几乎不会出现图表，
 * 为它多加载一份 mermaid 不划算。安全边界一致：marked 解析后统一过 sanitizeHTML，
 * 模型输出里的 HTML 标签不会被执行。
 */
import { computed } from 'vue'
import { marked } from 'marked'
import { sanitizeHTML } from '@/utils/security'

const props = defineProps<{ content: string }>()

const html = computed(() => {
  const source = props.content ?? ''
  if (!source.trim()) return ''
  const raw = marked.parse(source, {
    breaks: true,
    gfm: true,
    async: false,
  }) as string
  return sanitizeHTML(raw)
})
</script>

<style lang="less" scoped>
.ai-markdown {
  font-size: var(--app-text-sm);
  line-height: 1.7;
  color: var(--td-text-color-primary);
  word-break: break-word;

  :deep(> :first-child) {
    margin-top: 0;
  }

  :deep(> :last-child) {
    margin-bottom: 0;
  }

  :deep(h1),
  :deep(h2),
  :deep(h3),
  :deep(h4) {
    margin: var(--app-space-3) 0 var(--app-space-1);
    font-size: var(--app-text-sm);
    font-weight: 600;
    line-height: 1.5;
  }

  :deep(h1) {
    font-size: var(--app-text-md);
  }

  :deep(p) {
    margin: var(--app-space-1) 0;
  }

  :deep(ul),
  :deep(ol) {
    margin: var(--app-space-1) 0;
    padding-left: 18px;
  }

  :deep(li) {
    margin: 2px 0;
  }

  :deep(blockquote) {
    margin: var(--app-space-2) 0;
    padding: 2px var(--app-space-2);
    border-left: 2px solid var(--td-component-stroke);
    color: var(--td-text-color-secondary);
  }

  :deep(code) {
    padding: 0 3px;
    border-radius: 3px;
    background: var(--td-bg-color-secondarycontainer);
    font-family: 'SFMono-Regular', Consolas, monospace;
    font-size: var(--app-text-2xs);
  }

  :deep(pre) {
    margin: var(--app-space-2) 0;
    padding: var(--app-space-2);
    border-radius: var(--app-radius-xs);
    background: var(--td-bg-color-secondarycontainer);
    overflow-x: auto;

    code {
      padding: 0;
      background: transparent;
    }
  }

  :deep(table) {
    width: 100%;
    margin: var(--app-space-2) 0;
    border-collapse: collapse;
    font-size: var(--app-text-2xs);
  }

  :deep(th),
  :deep(td) {
    padding: 4px 6px;
    border: 1px solid var(--td-component-border);
    text-align: left;
  }

  :deep(th) {
    background: var(--td-bg-color-secondarycontainer);
    font-weight: 600;
  }

  :deep(hr) {
    margin: var(--app-space-3) 0;
    border: 0;
    border-top: 1px solid var(--td-component-stroke);
  }
}
</style>
