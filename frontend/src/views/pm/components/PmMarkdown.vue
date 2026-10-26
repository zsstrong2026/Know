<template>
  <!-- 轻量 Markdown 渲染：Mermaid 代码块在挂载后替换为 SVG，解析失败则保留源码 -->
  <div ref="rootEl" class="pm-markdown" v-html="html"></div>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue';
import { marked } from 'marked';
import { sanitizeHTML } from '@/utils/security';
import { renderMermaidToSvg } from '@/utils/mermaidShared';

const props = defineProps<{ content: string }>();

const rootEl = ref<HTMLElement | null>(null);
const html = ref('');

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function render(markdown: string) {
  const renderer = new marked.Renderer();
  renderer.code = ({ text, lang }: { text: string; lang?: string | null }) => {
    const cls = lang === 'mermaid' ? ' class="language-mermaid"' : '';
    return `<pre><code${cls}>${escapeHtml(text ?? '')}</code></pre>`;
  };
  const raw = marked.parse(markdown || '', {
    renderer,
    breaks: true,
    gfm: true,
    async: false,
  }) as string;
  html.value = sanitizeHTML(raw);
  void nextTick(hydrateMermaid);
}

async function hydrateMermaid() {
  const root = rootEl.value;
  if (!root) return;
  const blocks = Array.from(root.querySelectorAll<HTMLElement>('pre > code.language-mermaid'));
  for (const codeEl of blocks) {
    const pre = codeEl.parentElement;
    const source = (codeEl.textContent ?? '').trim();
    if (!pre || !source) continue;
    const svg = await renderMermaidToSvg(source);
    if (!svg) continue; // 渲染失败：保留原始代码块便于排查
    const holder = document.createElement('div');
    holder.className = 'pm-markdown__mermaid';
    holder.innerHTML = svg;
    pre.replaceWith(holder);
  }
}

watch(
  () => props.content,
  (value) => render(value),
  { immediate: true },
);
</script>

<style lang="less" scoped>
.pm-markdown {
  font-size: var(--app-text-base);
  line-height: 1.8;
  color: var(--td-text-color-primary);
  word-break: break-word;

  :deep(h1) {
    margin: 0 0 var(--app-space-4);
    padding-bottom: var(--app-space-2);
    border-bottom: 1px solid var(--td-component-stroke);
    font-size: var(--app-text-xl);
    font-weight: 600;
  }

  :deep(h2) {
    margin: var(--app-space-5) 0 var(--app-space-2);
    font-size: var(--app-text-lg);
    font-weight: 600;
  }

  :deep(h3) {
    margin: var(--app-space-4) 0 var(--app-space-2);
    font-size: var(--app-text-md);
    font-weight: 600;
  }

  :deep(p),
  :deep(ul),
  :deep(ol) {
    margin: var(--app-space-2) 0;
  }

  :deep(ul),
  :deep(ol) {
    padding-left: 22px;
  }

  :deep(table) {
    width: 100%;
    border-collapse: collapse;
    margin: var(--app-space-3) 0;
    font-size: var(--app-text-sm);
  }

  :deep(th),
  :deep(td) {
    border: 1px solid var(--td-component-border);
    padding: 6px 10px;
    text-align: left;
  }

  :deep(th) {
    background: var(--td-bg-color-secondarycontainer);
    font-weight: 600;
  }

  :deep(pre) {
    margin: var(--app-space-3) 0;
    padding: var(--app-space-3);
    border-radius: var(--app-radius-md);
    background: var(--td-bg-color-secondarycontainer);
    overflow-x: auto;
  }

  :deep(code) {
    font-family: 'SFMono-Regular', Consolas, monospace;
    font-size: var(--app-text-sm);
  }

  :deep(.pm-markdown__mermaid) {
    margin: var(--app-space-3) 0;
    padding: var(--app-space-3);
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-md);
    overflow-x: auto;

    svg {
      max-width: 100%;
      height: auto;
    }
  }
}
</style>
