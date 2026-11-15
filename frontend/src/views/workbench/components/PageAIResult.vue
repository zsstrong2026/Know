<template>
  <!-- 结果预览卡片（§9.15.3）：默认先预览，用户确认才写入 -->
  <div class="pai-result" aria-live="polite">
    <header class="pr-head">
      <span class="pr-ico"><t-icon name="star" size="12px" /></span>
      <div class="pr-copy">
        <b>{{ title }}<span class="pr-note">{{ t('workbench.pageAi.preview.title') }}</span></b>
        <span class="pr-meta">{{ metaText }}</span>
      </div>
      <button class="pr-close" type="button" :aria-label="t('workbench.pageAi.preview.cancel')" @click="emit('cancel')">
        <t-icon name="close" size="12px" />
      </button>
    </header>

    <!-- 候选切换：只有「换一种」补过候选后才出现 -->
    <div v-if="hasAlternatives" class="pr-tabs" role="tablist">
      <button
        v-for="(candidate, index) in candidates"
        :key="candidate.id"
        type="button"
        role="tab"
        class="pr-tab"
        :class="{ 'is-active': index === activeIndex }"
        :aria-selected="index === activeIndex"
        @click="emit('select-candidate', index)"
      >
        {{ t('workbench.pageAi.preview.candidate', { index: index + 1 }) }}
        <span v-if="candidate.streaming" class="pr-tab-dot" />
      </button>
    </div>

    <div class="pr-body" :class="{ 'is-streaming': streaming }">
      <span v-if="!text && streaming">{{ t('workbench.pageAi.state.generating') }}</span>

      <!-- 纠错：逐条问题卡，可单独取消 -->
      <template v-else-if="isProofread">
        <p v-if="!issues.length" class="pr-empty">{{ t('workbench.pageAi.preview.noIssues') }}</p>
        <ul v-else class="pr-issues">
          <li v-for="(issue, index) in issues" :key="`${issue.original}-${index}`" class="pr-issue">
            <label class="pr-issue-head">
              <input
                type="checkbox"
                :checked="!rejectedIssues.has(index)"
                @change="emit('toggle-issue', index)"
              />
              <span class="pr-issue-idx">{{ index + 1 }}</span>
            </label>
            <div class="pr-issue-body">
              <p class="pr-issue-line">
                <span class="pr-del">{{ issue.original }}</span>
                <t-icon name="arrow-right" size="12px" />
                <span class="pr-ins">{{ issue.suggestion }}</span>
              </p>
              <p v-if="issue.reason" class="pr-issue-reason">{{ issue.reason }}</p>
            </div>
          </li>
        </ul>
      </template>

      <!-- 其余动作：有原文时做字级 diff，否则按 Markdown 渲染 -->
      <template v-else-if="diffSegments.length">
        <p v-for="(line, index) in diffLines" :key="index" class="pr-diff-line">
          <template v-for="(segment, segIndex) in line" :key="segIndex">
            <span v-if="segment.op === 'delete'" class="pr-del">{{ segment.text }}</span>
            <span v-else-if="segment.op === 'insert'" class="pr-ins">{{ segment.text }}</span>
            <span v-else>{{ segment.text }}</span>
          </template>
        </p>
      </template>

      <!-- 纯文本预排版会把 ## / ** 原样显示，这里统一按 Markdown 渲染 -->
      <AiMarkdown v-else :content="text" class="pr-markdown" />
    </div>

    <footer class="pr-foot">
      <template v-if="mode !== 'aside'">
        <t-button size="small" theme="primary" :disabled="!canApply" @click="emit('apply', 'replace')">
          {{ t('workbench.pageAi.preview.replace') }}
        </t-button>
        <t-button size="small" variant="outline" :disabled="!canApply" @click="emit('apply', 'below')">
          {{ t('workbench.pageAi.preview.insertBelow') }}
        </t-button>
      </template>
      <span v-else class="pr-explained">{{ t('workbench.pageAi.preview.explainOnly') }}</span>
      <span class="spacer" />
      <t-button size="small" variant="text" :disabled="!text" @click="copy">
        {{ copied ? t('workbench.pageAi.preview.copyDone') : t('workbench.pageAi.preview.copy') }}
      </t-button>
      <t-button
        v-if="!isProofread && retryable !== false"
        size="small"
        variant="text"
        :disabled="streaming"
        @click="emit('alternatives')"
      >
        {{ t('workbench.pageAi.preview.alternative') }}
      </t-button>
      <t-button
        v-if="retryable !== false"
        size="small"
        variant="text"
        :disabled="streaming"
        @click="emit('retry')"
      >
        {{ t('workbench.pageAi.preview.retry') }}
      </t-button>
      <t-button size="small" variant="text" @click="emit('cancel')">
        {{ t('workbench.pageAi.preview.cancel') }}
      </t-button>
    </footer>
  </div>
</template>

<script setup lang="ts">
/**
 * 结果预览卡片。
 *
 * 只负责展示与按钮分发：是否允许写入、写入前如何校验选区指纹，
 * 全部由使用方（PageAI 主控 composable）决定。
 *
 * 三种呈现：
 *   - 纠错：问题列表，逐条勾选后一次性写入；
 *   - 改写类：有原文时按字级 diff 展示增删，否则纯文本；
 *   - 解释类（aside）：只读展示，不提供写入按钮。
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PageAIWriteMode } from '@/workbench/ai/page-actions'
import type { PageAICandidate } from '@/workbench/ai/use-page-ai'
import type { ProofreadIssue } from '@/workbench/ai/inline-ai-types'
import { diffSegments as computeDiffSegments, stripMarkdownForPreview, type DiffSegment } from '@/workbench/ai/text-diff'
import AiMarkdown from './AiMarkdown.vue'

const props = defineProps<{
  /** 已生成的正文（流式期间持续增长） */
  text: string
  /** 写入方式：aside 不提供替换/插入按钮 */
  mode: PageAIWriteMode
  /** 动作显示名，例如「修正语法」 */
  title: string
  /** 是否仍在流式生成 */
  streaming?: boolean
  /** 全部候选；多候选时渲染 Tab */
  candidates?: PageAICandidate[]
  activeIndex?: number
  hasAlternatives?: boolean
  /** 能否重试/换一种；侧栏问答接管的预览没有原请求 */
  retryable?: boolean
  /** 纠错结果的问题列表 */
  issues?: ProofreadIssue[]
  /** 被用户取消勾选的问题下标 */
  rejectedIssues?: Set<number>
  /** 是否为纠错动作 */
  isProofread?: boolean
  /** 发起请求时的原文；有值时对结果做字级 diff */
  originalText?: string
}>()

const emit = defineEmits<{
  apply: [mode: 'replace' | 'below']
  retry: []
  cancel: []
  alternatives: []
  'select-candidate': [index: number]
  'toggle-issue': [index: number]
}>()

const { t } = useI18n()
const copied = ref(false)

const streaming = computed(() => Boolean(props.streaming))
const issues = computed(() => props.issues ?? [])
const rejectedIssues = computed(() => props.rejectedIssues ?? new Set<number>())
const candidates = computed(() => props.candidates ?? [])
const activeIndex = computed(() => props.activeIndex ?? 0)
const isProofread = computed(() => Boolean(props.isProofread))

/** 元信息：流式时提示生成中，纠错时给问题条数。 */
const metaText = computed(() => {
  if (streaming.value) return t('workbench.pageAi.state.generating')
  if (isProofread.value) {
    return t('workbench.pageAi.preview.issueCount', { count: issues.value.length })
  }
  return t('workbench.pageAi.preview.meta')
})

/** diff 对照用文本：去掉 Markdown 标记，避免预览里冒出 `**`、`##` 源码。 */
const previewText = computed(() => stripMarkdownForPreview(props.text))

/** diff 只在「替换」且不是纠错/解释时启用：插入下方与全文任务没有对照意义。 */
const diffSegments = computed<DiffSegment[]>(() => {
  if (isProofread.value || props.mode !== 'replace') return []
  if (!props.originalText || !props.text) return []
  return computeDiffSegments(props.originalText, previewText.value)
})

/** 段内按换行拆行，避免整段挤成一行看不出结构。 */
const diffLines = computed<DiffSegment[][]>(() => {
  const lines: DiffSegment[][] = []
  let current: DiffSegment[] = []
  for (const segment of diffSegments.value) {
    const parts = segment.text.split('\n')
    parts.forEach((part, index) => {
      if (index > 0) {
        if (current.length) lines.push(current)
        current = []
      }
      if (part) current.push({ op: segment.op, text: part })
    })
  }
  if (current.length) lines.push(current)
  return lines
})
/** 能否写入：纠错要求至少勾选一条，其余要求有文本。 */
const canApply = computed(() => {
  if (streaming.value) return false
  if (isProofread.value) return issues.value.length > rejectedIssues.value.size
  return Boolean(props.text)
})

/** 复制为纯文本：不走 innerHTML，也不带任何 Markdown 渲染（§9.15.5 输出安全）。 */
async function copy() {
  if (!props.text) return
  try {
    await navigator.clipboard.writeText(props.text)
    copied.value = true
    window.setTimeout(() => {
      copied.value = false
    }, 1500)
  } catch {
    // 剪贴板不可用（非安全上下文）时静默失败，不弹错误打断写作。
  }
}
</script>

<style lang="less" scoped>
.pai-result {
  width: 340px;
  max-height: 52vh;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);
  overflow: hidden;
}

.pr-head {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-2);
  padding: var(--app-space-2);
  border-bottom: 1px solid var(--td-component-stroke);
}

.pr-ico {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border-radius: var(--app-radius-xs);
  background: var(--td-brand-color-1);
  color: var(--td-brand-color);
}

.pr-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;

  b {
    font-size: var(--app-text-sm);
    font-weight: 600;
    color: var(--td-text-color-primary);
  }
}

.pr-note {
  margin-left: var(--app-space-1);
  font-size: var(--app-text-2xs);
  font-weight: 400;
  color: var(--td-text-color-placeholder);
}

.pr-meta {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.pr-close {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-placeholder);

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.pr-tabs {
  display: flex;
  gap: 2px;
  padding: var(--app-space-1) var(--app-space-2) 0;
  border-bottom: 1px solid var(--td-component-stroke);
  overflow-x: auto;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }
}

.pr-tab {
  position: relative;
  padding: 3px var(--app-space-2);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-secondary);
  border-radius: var(--app-radius-xs) var(--app-radius-xs) 0 0;

  &.is-active {
    color: var(--td-brand-color);
    background: var(--td-brand-color-1);
  }
}

.pr-tab-dot {
  display: inline-block;
  width: 4px;
  height: 4px;
  margin-left: 2px;
  border-radius: 50%;
  background: var(--td-brand-color);
  animation: pr-pulse 1s ease-in-out infinite;
}

@keyframes pr-pulse {
  50% {
    opacity: 0.3;
  }
}

.pr-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--app-space-2);
  font-size: var(--app-text-sm);
  line-height: 1.65;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;

  &.is-streaming {
    color: var(--td-text-color-secondary);
  }

  /* Markdown 渲染的是块级元素，不需要预排版保留空白 */
  .pr-markdown {
    white-space: normal;
  }
}

.pr-diff-line {
  margin: 0 0 2px;
}

/* 删除：淡红底 + 删除线；新增：淡绿底；两者都只影响阅读，不写入文档 */
.pr-del {
  color: var(--td-error-color);
  background: var(--td-error-color-1);
  text-decoration: line-through;
  border-radius: 2px;
}

.pr-ins {
  color: var(--td-success-color);
  background: var(--td-success-color-1);
  border-radius: 2px;
}

.pr-empty {
  color: var(--td-text-color-placeholder);
}

.pr-issues {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.pr-issue {
  display: flex;
  gap: var(--app-space-1);
  padding: var(--app-space-1);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xs);
}

.pr-issue-head {
  display: flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
}

.pr-issue-idx {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.pr-issue-body {
  flex: 1;
  min-width: 0;
}

.pr-issue-line {
  display: flex;
  align-items: center;
  gap: 4px;
  margin: 0;
  font-size: var(--app-text-sm);
}

.pr-issue-reason {
  margin: 2px 0 0;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-secondary);
}

.pr-foot {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  padding: var(--app-space-2);
  border-top: 1px solid var(--td-component-stroke);
  flex-wrap: wrap;
}

.pr-explained {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.spacer {
  flex: 1;
}
</style>
