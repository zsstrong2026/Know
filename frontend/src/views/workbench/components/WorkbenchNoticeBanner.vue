<template>
  <!-- 工作台通知条：用户级、tenantless，挂在 WorkbenchLayout 与 onboarding 顶部（§9.10） -->
  <div v-if="visible.length > 0" class="notice-bar" role="status" aria-live="polite">
    <div class="nb-items">
      <div v-for="notice in visible" :key="notice.id" class="nb-item">
        <t-icon :name="iconOf(notice.kind)" size="14px" class="nb-icon" />
        <span class="nb-text">{{ textOf(notice) }}</span>
        <button class="nb-action" type="button" @click="emit('read', notice.id)">
          {{ $t('workbench.notice.gotIt') }}
        </button>
      </div>
    </div>
    <button class="nb-all" type="button" @click="emit('readAll')">
      {{ $t('workbench.notice.readAll') }}
    </button>
  </div>
</template>

<script setup lang="ts">
/**
 * 工作台站内通知条。
 *
 * 只展示 payload 里的数量与时间，不展示任何正文；
 * 点击“知道了”标记单条已读，右侧“全部已读”清空未读。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { WorkbenchNotice } from '@/types/workbench'

const props = defineProps<{
  notices: WorkbenchNotice[]
  /** 最多同时展示几条，避免通知条占满首屏 */
  limit?: number
}>()

const emit = defineEmits<{
  read: [id: string]
  readAll: []
}>()

const { t } = useI18n()

const visible = computed(() => props.notices.slice(0, props.limit ?? 2))

function iconOf(kind: WorkbenchNotice['kind']): string {
  switch (kind) {
    case 'membership_removed':
      return 'error-circle'
    case 'membership_restored':
      return 'check-circle'
    case 'cleanup_warning':
      return 'time'
    default:
      return 'info-circle'
  }
}

/** 文案只拼数量与截止日期，不拼接任何内容正文。 */
function textOf(notice: WorkbenchNotice): string {
  const payload = notice.payload ?? {}
  const counts = t('workbench.notice.counts', {
    items: payload.item_count ?? 0,
    docs: payload.doc_count ?? 0,
    knowledge: payload.knowledge_count ?? 0,
  })
  switch (notice.kind) {
    case 'membership_removed':
      return payload.purge_after
        ? t('workbench.notice.removedWithDeadline', { counts, deadline: payload.purge_after })
        : t('workbench.notice.removed', { counts })
    case 'membership_restored':
      return t('workbench.notice.restored', { counts })
    case 'cleanup_warning':
      return t('workbench.notice.cleanup', { counts })
    default:
      return t('workbench.notice.archiveFailed')
  }
}
</script>

<style lang="less" scoped>
.notice-bar {
  flex: none;
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-4);
  background: var(--td-warning-color-1);
  border-bottom: 1px solid var(--td-warning-color-3);
}

.nb-items {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--app-space-1);
}

.nb-item {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-primary);
}

.nb-icon {
  flex: none;
  color: var(--td-warning-color);
}

.nb-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nb-action,
.nb-all {
  flex: none;
  padding: 0 var(--app-space-1);
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-xs);
  color: var(--td-brand-color);

  &:hover {
    text-decoration: underline;
    text-underline-offset: 2px;
  }
}

.nb-all {
  align-self: center;
  white-space: nowrap;
}
</style>
