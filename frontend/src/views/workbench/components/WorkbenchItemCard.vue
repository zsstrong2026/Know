<template>
  <!-- 条目卡片：封面用 CSS 表达 kind，不加载真实缩略图（P3 再做预览图） -->
  <article
    class="item-card"
    :class="{ 'is-trashed': trashed }"
    :data-item-id="item.id"
    tabindex="0"
    @click="emit('open', item)"
    @keydown.enter.prevent="emit('open', item)"
  >
    <div class="card-cover" :class="`cover-${item.kind}`">
      <span class="card-kind">
        <t-icon :name="kindIcon" size="11px" />
        {{ kindLabel }}
      </span>
      <button
        class="card-star"
        :class="{ 'is-on': item.is_favorite }"
        type="button"
        :aria-label="
          $t(item.is_favorite ? 'workbench.item.unfavorite' : 'workbench.item.favorite')
        "
        :aria-pressed="item.is_favorite"
        @click.stop="emit('favorite', item)"
      >
        <t-icon name="star" size="14px" />
      </button>
    </div>

    <div class="card-body">
      <div class="card-title" :title="item.title">{{ item.title }}</div>
      <div class="card-desc">{{ describe }}</div>

      <div class="card-archive">
        <ArchiveBadge
          :status="item.archive_status"
          :archived-version="item.archived_version"
          :readonly="trashed"
          @action="emit('archive', item, $event)"
        />
      </div>

      <div class="card-foot">
        <span>{{ updatedLabel }}</span>
        <span class="ft-dot" aria-hidden="true">·</span>
        <span>v{{ item.snapshot_version }}</span>
        <span class="spacer" />
        <t-dropdown
          :options="menuOptions"
          trigger="click"
          placement="bottom-right"
          attach="body"
          @click="onMenu"
        >
          <button class="card-more" type="button" :aria-label="$t('workbench.item.open')" @click.stop>
            <t-icon name="ellipsis" size="14px" />
          </button>
        </t-dropdown>
      </div>
    </div>
  </article>
</template>

<script setup lang="ts">
/**
 * 工作台条目卡片。
 *
 * 交互全部上抛给列表页（open / favorite / archive / menu），
 * 卡片本身不请求接口，保证在列表里可以大量复用而不产生 N+1 请求。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ArchiveBadge from './ArchiveBadge.vue'
import { formatRelativeTime, itemCaption } from '@/workbench/item-display'
import {
  buildItemMenuOptions,
  resolveItemMenuAction,
  type WorkbenchItemMenuAction,
} from '@/workbench/item-menu'
import type { ArchiveBadgeAction } from '@/workbench/archive-status'
import type { WorkbenchItem } from '@/types/workbench'

const props = withDefaults(
  defineProps<{
    item: WorkbenchItem
    /** 回收站里的条目只提供恢复/永久删除，不提供归档操作 */
    trashed?: boolean
  }>(),
  { trashed: false },
)

const emit = defineEmits<{
  open: [item: WorkbenchItem]
  favorite: [item: WorkbenchItem]
  archive: [item: WorkbenchItem, action: ArchiveBadgeAction]
  /** 「…」菜单里的动作统一上抛，由列表页派发，卡片不自己 switch */
  menu: [item: WorkbenchItem, action: WorkbenchItemMenuAction]
}>()

const { t } = useI18n()

/** 角标：数据库是独立实体；文档与画布按当前查看模式区分，不当作两类条目 */
const kindIcon = computed(() => {
  if (props.item.kind === 'database') return 'table'
  return (props.item.current_mode ?? 'edgeless') === 'page' ? 'file' : 'image'
})

const kindLabel = computed(() => {
  if (props.item.kind === 'database') return t('workbench.item.database')
  return t((props.item.current_mode ?? 'edgeless') === 'page'
    ? 'workbench.item.document'
    : 'workbench.item.canvas')
})

const describe = computed(() => itemCaption(props.item, t))
const updatedLabel = computed(() => formatRelativeTime(props.item.updated_at))
const menuOptions = computed(() => buildItemMenuOptions(props.item, props.trashed, t))

function onMenu(data: { value?: string | number }) {
  const action = resolveItemMenuAction(data.value)
  if (action) emit('menu', props.item, action)
}
</script>

<style lang="less" scoped>
.item-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  overflow: hidden;
  cursor: pointer;
  transition:
    border-color var(--app-motion-fast),
    box-shadow var(--app-motion-fast);

  &:hover {
    border-color: var(--td-brand-color-3);
    box-shadow: var(--td-shadow-1);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 1px;
  }
}

.card-cover {
  position: relative;
  height: 116px;
  flex: none;
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
}

.cover-canvas {
  background-image:
    linear-gradient(var(--td-component-stroke) 1px, transparent 1px),
    linear-gradient(90deg, var(--td-component-stroke) 1px, transparent 1px);
  background-size: 16px 16px;
  background-color: var(--td-bg-color-secondarycontainer);
}

.cover-database {
  background-image:
    linear-gradient(var(--td-component-stroke) 1px, transparent 1px),
    linear-gradient(90deg, var(--td-component-stroke) 1px, transparent 1px);
  background-size: 100% 20px, 64px 100%;
  background-color: var(--td-bg-color-secondarycontainer);
}

.card-kind {
  position: absolute;
  left: var(--app-space-2);
  top: var(--app-space-2);
  display: inline-flex;
  align-items: center;
  gap: var(--app-space-1);
  height: 20px;
  padding: 0 6px;
  border-radius: var(--app-radius-xs);
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-secondary);
}

.card-star {
  position: absolute;
  right: var(--app-space-2);
  top: var(--app-space-2);
  width: 24px;
  height: 24px;
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-xs);
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-placeholder);
  opacity: 0;
  transition: opacity var(--app-motion-fast);

  &.is-on {
    opacity: 1;
    color: var(--td-warning-color);
  }
}

.item-card:hover .card-star {
  opacity: 1;
}

.card-body {
  display: flex;
  flex-direction: column;
  gap: 5px;
  flex: 1;
  padding: var(--app-space-2) var(--app-space-3) var(--app-space-3);
}

.card-title {
  font-size: var(--app-text-md);
  font-weight: 600;
  line-height: 1.35;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-desc {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-archive {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  min-height: 20px;
}

.card-foot {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  margin-top: auto;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.ft-dot {
  opacity: 0.6;
}

.spacer {
  flex: 1;
}

.card-more {
  width: 22px;
  height: 22px;
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-placeholder);

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}
</style>
