<template>
  <!-- 结果预览卡片（§9.15.3）：默认先预览，用户确认才写入 -->
  <div class="pai-result" aria-live="polite">
    <header class="pr-head">
      <span class="pr-ico"><t-icon name="star" size="12px" /></span>
      <div class="pr-copy">
        <b>{{ title }}<span class="pr-note">{{ t('workbench.pageAi.preview.title') }}</span></b>
        <span class="pr-meta">{{ t('workbench.pageAi.preview.meta') }}</span>
      </div>
      <button class="pr-close" type="button" :aria-label="t('workbench.pageAi.preview.cancel')" @click="emit('cancel')">
        <t-icon name="close" size="12px" />
      </button>
    </header>

    <div class="pr-body" :class="{ 'is-streaming': streaming }">
      <span v-if="!text && streaming">{{ t('workbench.pageAi.state.generating') }}</span>
      <template v-else>{{ text }}</template>
    </div>

    <footer class="pr-foot">
      <template v-if="mode !== 'aside'">
        <t-button size="small" theme="primary" :disabled="streaming || !text" @click="emit('apply', 'replace')">
          {{ t('workbench.pageAi.preview.replace') }}
        </t-button>
        <t-button size="small" variant="outline" :disabled="streaming || !text" @click="emit('apply', 'below')">
          {{ t('workbench.pageAi.preview.insertBelow') }}
        </t-button>
      </template>
      <span v-else class="pr-explained">{{ t('workbench.pageAi.preview.explainOnly') }}</span>
      <span class="spacer" />
      <t-button size="small" variant="text" :disabled="!text" @click="copy">
        {{ copied ? t('workbench.pageAi.preview.copyDone') : t('workbench.pageAi.preview.copy') }}
      </t-button>
      <t-button size="small" variant="text" :disabled="streaming" @click="emit('retry')">
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
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PageAIWriteMode } from '@/workbench/ai/page-actions'

const props = defineProps<{
  /** 已生成的正文（流式期间持续增长） */
  text: string
  /** 写入方式：aside 不提供替换/插入按钮 */
  mode: PageAIWriteMode
  /** 动作显示名，例如「修正语法」 */
  title: string
  /** 是否仍在流式生成 */
  streaming?: boolean
}>()

const emit = defineEmits<{
  apply: [mode: 'replace' | 'below']
  retry: []
  cancel: []
}>()

const { t } = useI18n()
const copied = ref(false)

const streaming = computed(() => Boolean(props.streaming))

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
  width: 320px;
  max-height: 46vh;
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
}

.pr-foot {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  padding: var(--app-space-2);
  border-top: 1px solid var(--td-component-stroke);
}

.pr-explained {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.spacer {
  flex: 1;
}
</style>
