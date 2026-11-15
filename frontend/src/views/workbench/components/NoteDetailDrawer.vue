<template>
  <!-- 画布文档卡片的详情抽屉：只读预览，点击卡片标题栏打开 -->
  <aside v-if="open" class="note-drawer" role="dialog" :aria-label="t('workbench.noteDetail.title')">
    <div class="drawer-head">
      <t-icon name="file" size="12px" />
      <span>{{ t('workbench.noteDetail.title') }}</span>
      <span class="spacer" />
      <button
        class="drawer-close"
        type="button"
        :aria-label="t('workbench.noteDetail.close')"
        @click="$emit('close')"
      >
        <t-icon name="close" size="13px" />
      </button>
    </div>

    <div class="drawer-body">
      <div class="drawer-title">{{ title || t('workbench.item.untitledDocument') }}</div>
      <div class="drawer-meta">
        <span v-if="updatedLabel">{{ t('workbench.noteDetail.updated', { time: updatedLabel }) }}</span>
        <span>{{ t('workbench.noteDetail.words', { count: wordCount }) }}</span>
      </div>
      <AiMarkdown v-if="markdown.trim()" class="drawer-content" :content="markdown" />
      <p v-else class="drawer-empty">{{ t('workbench.noteDetail.empty') }}</p>
    </div>
  </aside>
</template>

<script setup lang="ts">
/**
 * 文档详情抽屉。
 *
 * 卡片本身是固定尺寸的预览，长文会被裁切；这里用同一份 Markdown 投影做只读全文，
 * 让用户不进入画布编辑也能看完内容。数据由条目页传入，组件不读 store。
 */
import { useI18n } from 'vue-i18n'
import AiMarkdown from './AiMarkdown.vue'

defineProps<{
  open: boolean
  /** 文档标题（与条目页标题一致） */
  title: string
  /** 与归档同源的 Markdown 投影 */
  markdown: string
  wordCount: number
  updatedLabel: string
}>()

defineEmits<{ close: [] }>()

const { t } = useI18n()
</script>

<style lang="less" scoped>
/* 与行详情抽屉同一套版式：右侧 352px、左边线 + 轻投影、无遮罩。 */
.note-drawer {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: 2000;
  display: flex;
  flex-direction: column;
  width: 352px;
  max-width: 92vw;
  background: var(--td-bg-color-container);
  border-left: 1px solid var(--td-component-stroke);
  box-shadow: -8px 0 24px rgba(0, 0, 0, 0.05);
}

.drawer-head {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.drawer-head .spacer {
  flex: 1;
}

.drawer-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: 0;
  border-radius: 4px;
  background: none;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.drawer-body {
  flex: 1;
  min-height: 0;
  padding: 14px 16px;
  overflow: auto;
}

.drawer-title {
  margin-bottom: 8px;
  color: var(--td-text-color-primary);
  font-size: 16px;
  font-weight: 600;
  line-height: 1.35;
}

.drawer-meta {
  display: flex;
  gap: 10px;
  margin-bottom: 14px;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.drawer-content {
  border-top: 1px solid var(--td-component-stroke);
  padding-top: 12px;
}

.drawer-empty {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}
</style>
