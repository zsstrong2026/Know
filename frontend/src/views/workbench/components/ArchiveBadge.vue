<template>
  <!-- 归档徽标：状态必须同时有文字与颜色，不能只靠色点传达（§9.8 无障碍） -->
  <span
    class="archive-chip"
    :class="`is-${badge.status}`"
    role="status"
    :aria-label="ariaLabel"
  >
    <span class="dot" aria-hidden="true" />
    <span class="chip-text">{{ $t(badge.key) }}</span>
    <span v-if="badge.version > 0" class="chip-version">· v{{ badge.version }}</span>
  </span>
  <button
    v-if="badge.action !== 'none' && !readonly"
    class="chip-action"
    type="button"
    @click.stop="emit('action', badge.action)"
  >
    {{ $t(`workbench.archive.action.${badge.action}`) }}
  </button>
</template>

<script setup lang="ts">
/**
 * 归档状态徽标 + 主操作。
 *
 * 状态与可操作性全部来自 archive-status.ts 的纯函数，
 * 组件只做渲染与事件上抛，便于在列表与卡片里复用同一套口径。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { archiveBadge, type ArchiveBadgeAction } from '@/workbench/archive-status'
import type { ArchiveStatus } from '@/types/workbench'

const props = withDefaults(
  defineProps<{
    status?: ArchiveStatus
    archivedVersion?: number
    /** 只读场景（例如回收站）不显示操作按钮 */
    readonly?: boolean
  }>(),
  { archivedVersion: 0, readonly: false },
)

const emit = defineEmits<{ action: [action: ArchiveBadgeAction] }>()

const { t } = useI18n()
const badge = computed(() => archiveBadge(props.status, props.archivedVersion))

/** 读屏文案把状态与版本拼成一句，避免只念出“已归档”。 */
const ariaLabel = computed(() => {
  const status = t(badge.value.key)
  return badge.value.version > 0
    ? `${status} · v${badge.value.version}`
    : status
})
</script>

<style lang="less" scoped>
.archive-chip {
  display: inline-flex;
  align-items: center;
  gap: var(--app-space-1);
  height: 19px;
  padding: 0 6px;
  border: 1px solid transparent;
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-2xs);
  line-height: 1;
  white-space: nowrap;
}

.dot {
  width: 4px;
  height: 4px;
  flex: none;
  border-radius: var(--td-radius-circle);
  background: currentColor;
}

.is-active {
  background: var(--td-success-color-1);
  color: var(--td-success-color);
  border-color: var(--td-success-color-3);
}

.is-processing {
  background: var(--td-brand-color-1);
  color: var(--td-brand-color);
  border-color: var(--td-brand-color-3);
}

.is-stale {
  background: var(--td-warning-color-1);
  color: var(--td-warning-color);
  border-color: var(--td-warning-color-3);
}

.is-partial {
  background: var(--td-error-color-1);
  color: var(--td-error-color);
  border-color: var(--td-error-color-3);
}

.is-none {
  background: var(--td-bg-color-component);
  color: var(--td-text-color-placeholder);
  border-color: var(--td-component-stroke);
  border-style: dashed;
}

.is-deleted,
.is-source_deleted {
  background: var(--td-bg-color-component);
  color: var(--td-text-color-placeholder);
  border-color: var(--td-component-stroke);
}

.chip-action {
  margin-left: auto;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
  text-decoration: underline;
  text-underline-offset: 2px;
  white-space: nowrap;

  &:hover {
    color: var(--td-text-color-primary);
  }
}
</style>
