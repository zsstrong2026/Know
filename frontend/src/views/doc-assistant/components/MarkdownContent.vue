<template>
  <div class="doc-assistant-markdown">
    <div v-if="html" ref="contentEl" class="doc-assistant-markdown__body" v-html="html" @click="onContentClick"></div>
    <div v-else class="doc-assistant-markdown__empty">{{ t('docAssistant.detail.noContent') }}</div>
  </div>
</template>

<script setup lang="ts">
// 轻量 Markdown 渲染：marked + KaTeX + highlight.js + DOMPurify 安全输出。
// 渲染逻辑与 components/document-preview.vue 的 renderMarkdown 保持一致。
import { ref, watch, onBeforeUnmount, nextTick } from 'vue'
import { marked } from 'marked'
import markedKatex from 'marked-katex-extension'
import 'katex/dist/katex.min.css'
import hljs from 'highlight.js'
import 'highlight.js/styles/github.css'
import { sanitizeHTML, safeMarkdownToHTML, isValidImageURL, createSafeImage } from '@/utils/security'
import { openMermaidFullscreen } from '@/utils/mermaidViewer'
import { renderMermaidToSvg } from '@/utils/mermaidShared'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ content: string }>()

const { t } = useI18n()
const html = ref('')
const contentEl = ref<HTMLElement | null>(null)
let mermaidObserver: MutationObserver | null = null

const preprocessMathDelimiters = (rawText: string): string => {
  if (!rawText || typeof rawText !== 'string') return ''
  return rawText
    .replace(/\\\[([\s\S]*?)\\\]/g, '$$$$$1$$$$')
    .replace(/\\\(([\s\S]*?)\\\)/g, '$$$1$$')
}

const render = async (markdown: string) => {
  if (!markdown || typeof markdown !== 'string') {
    html.value = ''
    return
  }
  marked.use({ breaks: true, gfm: true })
  marked.use(markedKatex({ throwOnError: false, nonStandard: true }))
  const renderer = new marked.Renderer()
  renderer.code = ({ text, lang }: { text: string; lang?: string | null }) => {
    const code = text ?? ''
    let highlighted = ''
    if (lang && hljs.getLanguage(lang)) {
      try {
        highlighted = hljs.highlight(code, { language: lang }).value
      } catch {
        highlighted = hljs.highlightAuto(code).value
      }
    } else {
      highlighted = hljs.highlightAuto(code).value
    }
    return `<pre><code class="hljs">${highlighted}</code></pre>`
  }
  renderer.image = ({ href, title, text }: { href: string; title?: string | null; text?: string }) => {
    if (!isValidImageURL(href)) return ''
    return createSafeImage(href, text || '', title || '')
  }
  const mathSafeText = preprocessMathDelimiters(markdown)
  const safeText = safeMarkdownToHTML(mathSafeText)
  const rawHtml = marked.parse(safeText, { renderer, breaks: true, gfm: true, async: false }) as string
  html.value = sanitizeHTML(rawHtml)
  await nextTick()
  hydrateMermaid()
}

// Mermaid：把 ```mermaid 代码块替换为渲染后的 SVG（失败时保留源码文本）
const hydrateMermaid = async () => {
  const root = contentEl.value
  if (!root) return
  const blocks = Array.from(root.querySelectorAll('pre > code.language-mermaid'))
  for (const codeEl of blocks) {
    const pre = codeEl.parentElement
    if (!pre || pre.dataset.mermaidDone) continue
    pre.dataset.mermaidDone = 'true'
    const source = (codeEl.textContent || '').trim()
    if (!source) continue
    try {
      const svg = await renderMermaidToSvg(source)
      const holder = document.createElement('div')
      holder.innerHTML = svg || ''
      const svgEl = holder.querySelector('svg')
      if (svgEl) {
        svgEl.style.maxWidth = '100%'
        svgEl.style.height = 'auto'
      }
      pre.replaceWith(holder)
    } catch {
      // 渲染失败：保留原始代码块，用户可自行查看
    }
  }
}

// 点击图片/内联 SVG 全屏查看 Mermaid
const onContentClick = (e: Event) => {
  const target = e.target as HTMLElement
  const svg = target?.closest?.('svg')
  if (svg && (svg.closest('.mermaid') || svg.getAttribute('data-mermaid'))) {
    openMermaidFullscreen(svg.outerHTML)
  }
}

watch(
  () => props.content,
  (val) => {
    void render(val || '')
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  mermaidObserver?.disconnect()
  mermaidObserver = null
})
</script>

<style lang="less" scoped>
.doc-assistant-markdown {
  width: 100%;

  &__body {
    word-break: break-word;

    :deep(h1),
    :deep(h2),
    :deep(h3),
    :deep(h4) {
      margin: 16px 0 8px;
      line-height: 1.4;
      color: var(--td-text-color-primary);
    }

    :deep(h1) {
      font-size: 20px;
      padding-bottom: 8px;
      border-bottom: 1px solid var(--td-component-stroke);
    }

    :deep(h2) {
      font-size: 17px;
    }

    :deep(h3) {
      font-size: 15px;
    }

    :deep(p) {
      margin: 8px 0;
      line-height: 1.8;
      color: var(--td-text-color-primary);
    }

    :deep(ul),
    :deep(ol) {
      margin: 8px 0;
      padding-left: 22px;
      line-height: 1.8;
    }

    :deep(blockquote) {
      margin: 8px 0;
      padding: 4px 12px;
      border-left: 3px solid var(--td-brand-color);
      background: var(--td-bg-color-container-hover);
      color: var(--td-text-color-secondary);
      border-radius: 0 4px 4px 0;
    }

    :deep(table) {
      border-collapse: collapse;
      margin: 12px 0;
      max-width: 100%;
      display: block;
      overflow-x: auto;

      th,
      td {
        border: 1px solid var(--td-component-border);
        padding: 6px 12px;
        font-size: 13px;
      }

      th {
        background: var(--td-bg-color-secondarycontainer);
        font-weight: 600;
      }
    }

    :deep(pre) {
      background: var(--td-bg-color-secondarycontainer);
      border-radius: 6px;
      padding: 12px;
      overflow-x: auto;
      margin: 10px 0;

      code {
        font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
        font-size: 13px;
        background: transparent;
        padding: 0;
      }
    }

    :deep(code) {
      background: var(--td-bg-color-secondarycontainer);
      border-radius: 3px;
      padding: 2px 4px;
      font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
      font-size: 13px;
    }

    :deep(img) {
      max-width: 100%;
      border-radius: 4px;
    }

    :deep(hr) {
      border: none;
      border-top: 1px solid var(--td-component-stroke);
      margin: 16px 0;
    }

    :deep(a) {
      color: var(--td-brand-color);
      text-decoration: none;

      &:hover {
        text-decoration: underline;
      }
    }
  }

  &__empty {
    color: var(--td-text-color-placeholder);
    padding: 24px 0;
    text-align: center;
    font-size: 13px;
  }
}
</style>
