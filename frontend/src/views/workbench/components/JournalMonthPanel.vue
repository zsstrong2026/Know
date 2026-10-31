<template>
  <!-- 右侧 Journals 面板：月历 + 计数 + 当天列表 + 新建（§9.13.1） -->
  <aside class="jp" :aria-label="$t('workbench.journals.panelTitle')">
    <div class="jp-head">
      <span class="jp-title">{{ monthLabel }}</span>
      <span class="spacer" />
      <div class="jp-nav">
        <button type="button" :aria-label="$t('workbench.journals.prevMonth')" @click="emit('month-step', -1)">
          <t-icon name="chevron-left" size="14px" />
        </button>
        <button class="jp-today" type="button" @click="emit('select', today)">{{ $t('workbench.journals.today') }}</button>
        <button type="button" :aria-label="$t('workbench.journals.nextMonth')" @click="emit('month-step', 1)">
          <t-icon name="chevron-right" size="14px" />
        </button>
      </div>
    </div>

    <JournalMonthGrid
      :month="month"
      :selected-date="selectedDate"
      :entries="entries"
      :dragging-id="draggingId"
      @select="(date) => emit('select', date)"
      @move="(payload) => emit('move', payload)"
    />
    <JournalArchiveLegend />

    <div class="jp-counts">
      <span>{{ $t('workbench.journals.monthCount', { count: summary.total }) }}</span>
      <span>{{ $t('workbench.journals.dayCount', { count: dayEntries.length }) }}</span>
    </div>
    <div class="jp-sub" aria-live="polite">
      {{ subtitle }}
      <template v-if="truncated"> · {{ $t('workbench.journals.truncated', { count: entries.length }) }}</template>
    </div>

    <div class="jp-list">
      <button
        v-for="entry in dayEntries"
        :key="entry.id"
        class="jp-row"
        :class="{ 'is-active': entry.id === selectedItemId, 'is-dragging': entry.id === draggingId }"
        type="button"
        draggable="true"
        :aria-pressed="entry.id === selectedItemId"
        @click="emit('open', entry.id)"
        @dragstart="onDragStart(entry.id, $event)"
        @dragend="onDragEnd"
      >
        <span class="jr-date">{{ entry.journal_date.slice(5) }}</span>
        <span class="jr-body">
          <span class="jr-title">{{ entry.title }}</span>
          <span class="jr-meta">
            <i class="jr-dot" :class="`is-${entry.archive_status}`" aria-hidden="true" />
            {{ $t(`workbench.archive.status.${entry.archive_status}`) }}
            <span class="jr-mode">{{ $t(`workbench.journals.mode.${entry.current_mode}`) }}</span>
          </span>
        </span>
      </button>

      <div v-if="dayEntries.length === 0" class="jp-empty">
        {{ $t('workbench.journals.emptyDay') }}
      </div>
    </div>

    <div class="jp-foot">
      <t-button theme="primary" block @click="emit('create')">
        <template #icon><t-icon name="add" /></template>
        {{ $t('workbench.journals.new') }}
      </t-button>
      <p class="jp-note">{{ $t('workbench.journals.newHint') }}</p>
    </div>
  </aside>
</template>

<script setup lang="ts">
/**
 * Journals 右侧面板。
 *
 * 只接收已加载的月数据与当天条目，不再自行请求；
 * 拖拽只上抛 { id, date }，改期与回滚由 JournalsView 负责。
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import JournalMonthGrid from './JournalMonthGrid.vue'
import JournalArchiveLegend from './JournalArchiveLegend.vue'
import { groupByDate, monthSummary } from '@/workbench/journals/month-cells'
import { todayISO } from '@/workbench/journals/date-only'
import type { WorkbenchJournalEntry } from '@/types/workbench'

const props = defineProps<{
  month: string
  selectedDate: string
  selectedItemId: string
  entries: WorkbenchJournalEntry[]
  truncated: boolean
}>()

const emit = defineEmits<{
  select: [date: string]
  'month-step': [delta: number]
  open: [id: string]
  create: []
  move: [payload: { id: string; date: string }]
}>()

const { t, locale } = useI18n()
const today = todayISO()
const draggingId = ref('')

const monthLabel = computed(() => {
  void locale.value
  const year = props.month.slice(0, 4)
  const mon = Number(props.month.slice(5, 7))
  return t('workbench.journals.monthLabel', { year, month: mon })
})

const grouped = computed(() => groupByDate(props.entries))
const dayEntries = computed(() => grouped.value.get(props.selectedDate) ?? [])
const summary = computed(() => monthSummary(props.entries, props.month))

const subtitle = computed(() => {
  void locale.value
  return t('workbench.journals.selectedDay', {
    date: t('workbench.journals.dateLabel', {
      month: Number(props.selectedDate.slice(5, 7)),
      day: Number(props.selectedDate.slice(8, 10)),
    }),
    count: dayEntries.value.length,
  })
})

function onDragStart(id: string, event: DragEvent) {
  draggingId.value = id
  // 保留 text/plain 便于浏览器允许 drop；真实 id 走 props 传递更可靠
  event.dataTransfer?.setData('text/plain', id)
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
}

function onDragEnd() {
  draggingId.value = ''
}
</script>

<style lang="less" scoped>
.jp {
  width: 300px;
  flex: none;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--td-bg-color-container);
  border-left: 1px solid var(--td-component-stroke);
}

.jp-head {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  padding: var(--app-space-4) var(--app-space-4) var(--app-space-3);
}

.jp-title {
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.spacer {
  flex: 1;
}

.jp-nav {
  display: flex;
  align-items: center;
  gap: 2px;

  button {
    width: 22px;
    height: 22px;
    display: grid;
    place-items: center;
    border-radius: var(--app-radius-xs);
    color: var(--td-text-color-secondary);

    &:hover {
      background: var(--td-bg-color-container-hover);
    }
  }

  .jp-today {
    width: auto;
    padding: 0 var(--app-space-1);
    font-size: var(--app-text-2xs);
  }
}

.jp-counts {
  display: flex;
  justify-content: space-between;
  margin: var(--app-space-3) var(--app-space-3) var(--app-space-1);
  padding: var(--app-space-2);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.jp-sub {
  padding: var(--app-space-1) var(--app-space-4);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.jp-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--app-space-1) var(--app-space-2) var(--app-space-2);
}

.jp-row {
  width: 100%;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  margin-bottom: var(--app-space-1);
  padding: var(--app-space-2);
  border: 1px solid transparent;
  border-radius: var(--app-radius-sm);
  text-align: left;
  cursor: grab;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-active {
    border-color: var(--td-brand-color-3);
    background: var(--td-brand-color-1);
  }

  &.is-dragging {
    opacity: 0.45;
  }
}

.jr-date {
  flex: none;
  width: 32px;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
}

.jr-body {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.jr-title {
  overflow: hidden;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.jr-meta {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.jr-dot {
  width: 4px;
  height: 4px;
  flex: none;
  border-radius: var(--td-radius-circle);
}

.jr-mode {
  margin-left: auto;
}

.is-active.jr-dot { background: var(--td-success-color); }
.is-processing.jr-dot { background: var(--td-brand-color); }
.is-stale.jr-dot { background: var(--td-warning-color); }
.is-partial.jr-dot { background: var(--td-error-color); }
.is-none.jr-dot { background: var(--td-text-color-disabled); }
.is-deleted.jr-dot { background: var(--td-text-color-placeholder); opacity: 0.55; }

.jp-empty {
  padding: var(--app-space-3);
  font-size: var(--app-text-xs);
  line-height: 1.9;
  color: var(--td-text-color-placeholder);
}

.jp-foot {
  padding: var(--app-space-2) var(--app-space-3) var(--app-space-3);
  border-top: 1px solid var(--td-component-stroke);
}

.jp-note {
  margin: var(--app-space-2) 0 0;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
  text-align: center;
}
</style>
