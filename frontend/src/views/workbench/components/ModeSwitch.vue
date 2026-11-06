<template>
  <!--
    形态切换（§10.14.5）：只在画布条目出现，数据库与章节文档不渲染。
    初值 item.current_mode ?? 'edgeless'；点击只 emit，写库由父级防抖处理。
  -->
  <div class="mode-switch" role="group" :aria-label="t('workbench.modeSwitch.label')">
    <button
      v-for="option in MODE_OPTIONS"
      :key="option.value"
      type="button"
      :class="{ 'is-active': mode === option.value }"
      :aria-pressed="mode === option.value"
      :disabled="disabled"
      :title="t(option.labelKey)"
      @click="emit('change', option.value)"
    >
      <t-icon :name="option.icon" size="13px" />
      {{ t(option.labelKey) }}
    </button>
  </div>
</template>

<script setup lang="ts">
/**
 * 工作台条目的「文档 / 无限画布」切换。
 *
 * 只有 `kind === 'canvas'` 才有意义：数据库条目的“页面”就是数据视图，
 * 章节文档不经过这个编辑器（§10.14.5）。
 */
import { useI18n } from 'vue-i18n'
import type { EditorMode } from '@/workbench/editor/blocksuite'

defineProps<{
  mode: EditorMode
  /** 模板选择器等场景下禁用，避免“在无限画布里选文档模板”的歧义 */
  disabled?: boolean
}>()

const emit = defineEmits<{ change: [mode: EditorMode] }>()

const { t } = useI18n()

const MODE_OPTIONS = [
  { value: 'page' as const, icon: 'file', labelKey: 'workbench.modeSwitch.page' },
  { value: 'edgeless' as const, icon: 'artboard', labelKey: 'workbench.modeSwitch.edgeless' },
] as const
</script>

<style lang="less" scoped>
.mode-switch {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);

  button {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    height: 22px;
    padding: 0 var(--app-space-2);
    border-radius: var(--app-radius-xs);
    font-size: var(--app-text-xs);
    color: var(--td-text-color-secondary);
    cursor: pointer;

    &:hover:not(:disabled) {
      color: var(--td-text-color-primary);
    }

    &.is-active {
      background: var(--td-bg-color-container);
      color: var(--td-text-color-primary);
      box-shadow: var(--td-shadow-1);
    }

    &:disabled {
      cursor: default;
      opacity: 0.55;
    }
  }
}
</style>
