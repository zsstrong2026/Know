<template>
  <!-- 列表行：对齐 AFFiNE 文档列表——无边框、悬停浅底、左侧图标 + 标题/副标题、右侧时间与操作 -->
  <article
    class="item-row"
    :class="{ 'is-trashed': trashed, 'is-checked': selected }"
    :data-item-id="item.id"
    tabindex="0"
    @click="emit('open', item)"
    @keydown.enter.prevent="emit('open', item)"
  >
    <input
      v-if="selectable"
      class="row-check"
      type="checkbox"
      :checked="selected"
      :aria-label="item.title"
      @click.stop
      @change="emit('open', item)"
    />

    <span class="row-icon" aria-hidden="true">
      <template v-if="item.icon">{{ item.icon }}</template>
      <t-icon v-else :name="item.kind === 'database' ? 'table' : 'image'" size="15px" />
    </span>

    <div class="row-main">
      <div class="row-title" :title="item.title">{{ item.title }}</div>
      <div class="row-sub">
        <span class="row-caption">{{ caption }}</span>
        <!-- 列表里只显示有信息量的归档状态；未归档不占位，避免每行都挂一个灰徽标 -->
        <span v-if="item.archive_status && item.archive_status !== 'none'" class="row-badge">
          <ArchiveBadge
            :status="item.archive_status"
            :archived-version="item.archived_version"
            :readonly="trashed"
            @action="emit('archive', item, $event)"
          />
        </span>
      </div>
    </div>

    <span class="row-time" :title="absoluteTime">{{ updatedLabel }}</span>

    <button
      class="row-star"
      :class="{ 'is-on': item.is_favorite }"
      type="button"
      :aria-label="$t(item.is_favorite ? 'workbench.item.unfavorite' : 'workbench.item.favorite')"
      :aria-pressed="item.is_favorite"
      @click.stop="emit('favorite', item)"
    >
      <t-icon name="star" size="15px" />
    </button>

    <t-dropdown
      :options="menuOptions"
      trigger="click"
      placement="bottom-right"
      attach="body"
      @click="onMenu"
    >
      <button class="row-more" type="button" :aria-label="$t('workbench.item.more')" @click.stop>
        <t-icon name="more" size="15px" />
      </button>
    </t-dropdown>
  </article>
</template>

<script setup lang="ts">
/**
 * 工作台条目列表行（AFFiNE 风格）。
 *
 * 与卡片组件同口径：自身不发请求，交互全部上抛给列表页；
 * 菜单动作只上抛 value，具体做什么由列表页统一派发。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ArchiveBadge from './ArchiveBadge.vue'
import { itemCaption, formatRelativeTime } from '@/workbench/item-display'
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
    trashed?: boolean
    /** 批量归档选择模式下在行首显示勾选框 */
    selectable?: boolean
    selected?: boolean
  }>(),
  { trashed: false, selectable: false, selected: false },
)

const emit = defineEmits<{
  open: [item: WorkbenchItem]
  favorite: [item: WorkbenchItem]
  archive: [item: WorkbenchItem, action: ArchiveBadgeAction]
  menu: [item: WorkbenchItem, action: WorkbenchItemMenuAction]
}>()

const { t } = useI18n()

const caption = computed(() => itemCaption(props.item))
const updatedLabel = computed(() => formatRelativeTime(props.item.updated_at))
/** 悬停提示用绝对时间，相对时间只负责扫读 */
const absoluteTime = computed(() => {
  const time = new Date(props.item.updated_at)
  return Number.isFinite(time.getTime()) ? time.toLocaleString() : ''
})

const menuOptions = computed(() => buildItemMenuOptions(props.item, props.trashed, t))

function onMenu(data: { value?: string | number }) {
  const action = resolveItemMenuAction(data.value)
  if (action) emit('menu', props.item, action)
}
</script>

<style lang="less" scoped>
.item-row {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-2);
  min-width: 0;
  padding: 6px var(--app-space-2) 6px 10px;
  border-radius: var(--app-radius-md);
  cursor: pointer;
  transition: background var(--app-motion-fast);

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: -2px;
  }

  &.is-checked {
    background: var(--td-brand-color-1);
  }

  &.is-trashed {
    cursor: default;
  }
}

.row-check {
  flex: none;
  width: 15px;
  height: 15px;
  margin: 4px 0 0;
  accent-color: var(--td-brand-color);
  cursor: pointer;
}

/* 图标区固定 20px：有无 emoji 都不让标题左右跳动 */
.row-icon {
  flex: none;
  width: 20px;
  height: 20px;
  display: grid;
  place-items: center;
  font-size: 14px;
  line-height: 1;
  color: var(--td-text-color-placeholder);
}

.row-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.row-title {
  font-size: var(--app-text-base);
  font-weight: 500;
  line-height: 20px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-sub {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  min-width: 0;
  min-height: 17px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.row-caption {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 徽标与「归档/重试」链接包成一体：ArchiveBadge 内部用 margin-left:auto 右推动作，
   不包一层就会被推到整行的最右端，和徽标分家 */
.row-badge {
  display: inline-flex;
  align-items: center;
  flex: none;
}

.row-time {
  flex: none;
  font-size: var(--app-text-sm);
  line-height: 20px;
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
  /* 与标题首行对齐，不和副标题挤在一起 */
  margin-left: var(--app-space-2);
}

/* 星标与「…」常驻显示但保持低对比，收藏后变实心色 */
.row-star,
.row-more {
  flex: none;
  width: 26px;
  height: 26px;
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-placeholder);
  transition:
    color var(--app-motion-fast),
    background var(--app-motion-fast);

  &:hover {
    background: var(--td-bg-color-container-active);
    color: var(--td-text-color-primary);
  }
}

.row-star {
  margin-top: -3px;

  &.is-on {
    color: var(--td-warning-color);
  }
}

.row-more {
  margin-top: -3px;
}
</style>
