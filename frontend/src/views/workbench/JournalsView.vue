<template>
  <!-- Journals：主区编辑器 + 右侧月历面板（§9.13） -->
  <section class="journals">
    <div class="journals-main">
      <header class="j-head">
        <t-button variant="text" shape="square" :aria-label="$t('workbench.journals.back')" @click="goBack">
          <t-icon name="chevron-left" size="16px" />
        </t-button>
        <div class="j-crumb">
          <span>{{ $t('workbench.nav.title') }}</span>
          <span class="sep">/</span>
          <span>{{ $t('workbench.nav.journals') }}</span>
        </div>
        <span class="j-title">{{ currentEntry?.title ?? dateLabel(selectedDate) }}</span>
        <span v-if="currentEntry" class="j-save-state" :class="`is-${saveState}`">{{ saveLabel }}</span>
        <span class="spacer" />
        <div class="mode-switch" role="group" :aria-label="$t('workbench.journals.modeSwitch')">
          <button
            v-for="option in modeOptions"
            :key="option.value"
            type="button"
            :class="{ 'is-active': mode === option.value }"
            :aria-pressed="mode === option.value"
            :disabled="!currentEntry"
            @click="switchMode(option.value)"
          >
            <t-icon :name="option.icon" size="13px" />
            {{ $t(option.labelKey) }}
          </button>
        </div>
        <ArchiveBadge
          v-if="currentEntry"
          :status="currentEntry.archive_status"
          :archived-version="currentEntry.archived_version"
          @action="onArchiveAction"
        />
      </header>

      <JournalDateStrip
        :selected-date="selectedDate"
        :entries="store.journals"
        @select="selectDate"
        @step="stepWeek"
      />

      <div v-if="loadError" class="j-error">
        <t-icon name="error-circle" size="18px" />
        <span>{{ $t('workbench.editor.loadFailed') }}</span>
        <t-button size="small" variant="outline" @click="reloadDay">{{ $t('workbench.journals.retry') }}</t-button>
      </div>

      <div v-else-if="!currentEntry" class="j-empty">
        <div class="je-icon"><t-icon name="calendar" size="24px" /></div>
        <p class="je-date">{{ dateLabel(selectedDate) }}</p>
        <h2>{{ dayEntries.length ? $t('workbench.journals.pickOne', { count: dayEntries.length }) : $t('workbench.journals.emptyTitle') }}</h2>
        <p class="je-hint">{{ dayEntries.length ? $t('workbench.journals.pickHint') : $t('workbench.journals.emptyHint') }}</p>
        <t-button
          v-for="entry in dayEntries"
          :key="entry.id"
          class="je-choice"
          variant="outline"
          block
          @click="openEntry(entry.id)"
        >
          <template #icon><t-icon name="file" size="14px" /></template>
          {{ entry.title }}
        </t-button>
        <t-button v-if="dayEntries.length === 0" theme="primary" @click="createJournal">
          <template #icon><t-icon name="add" /></template>
          {{ $t('workbench.journals.new') }}
        </t-button>
      </div>

      <div v-else class="j-editor">
        <div ref="hostRef" class="j-mount" :class="{ 'is-hidden': !ready }" />
        <div v-if="!ready" class="j-loading">{{ $t('workbench.editor.loading') }}</div>
      </div>
    </div>

    <JournalMonthPanel
      v-if="store.journalPanelOpen"
      :month="store.journalsMonth"
      :selected-date="selectedDate"
      :selected-item-id="currentItemId"
      :entries="store.journals"
      :truncated="store.journalsTruncated"
      @select="selectDate"
      @month-step="stepMonth"
      @open="openEntry"
      @create="createJournal"
      @move="onMoveEntry"
    />
  </section>
</template>

<script setup lang="ts">
/**
 * Journals 页面（路线 1）。
 *
 * 三段状态：selectedDate（日期选择）、currentItemId（同日多篇区分）、
 * journalsMonth（月历浏览月，单独翻页不切正文）。
 * 日期与月份只传 YYYY-MM-DD / YYYY-MM 字符串，不做时区换算。
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import ArchiveBadge from './components/ArchiveBadge.vue'
import JournalDateStrip from './components/JournalDateStrip.vue'
import JournalMonthPanel from './components/JournalMonthPanel.vue'
import {
  createArchive,
  getWorkbenchItem,
  refreshArchive,
  updateWorkbenchItem,
  workbenchErrorCode,
  WORKBENCH_ERROR,
} from '@/api/workbench'
import { useWorkbenchStore } from '@/stores/workbench'
import { useEditorSession } from '@/workbench/editor/use-editor-session'
import { type ArchiveBadgeAction } from '@/workbench/archive-status'
import { groupByDate } from '@/workbench/journals/month-cells'
import { addDays, currentMonth, shiftMonth, todayISO } from '@/workbench/journals/date-only'
import type { EditorMode } from '@/workbench/editor/blocksuite'
import type { WorkbenchItem } from '@/types/workbench'

const props = defineProps<{ date?: string }>()

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useWorkbenchStore()

const selectedDate = ref(props.date || todayISO())
const currentItemId = ref(String(route.query.itemId ?? ''))
const currentEntry = ref<WorkbenchItem | null>(null)
const mode = ref<EditorMode>('page')
const loadError = ref(false)
const hostRef = ref<HTMLElement | null>(null)

const session = useEditorSession({
  itemId: currentItemId,
  host: hostRef,
  item: currentEntry,
  mode,
  onConflict: () => openConflictDialog(),
})
const { ready, baseVersion, saveState } = session

/** 保存状态文案与条目页保持同一套（§9.7）：草稿暂存不等于归档。 */
const saveLabel = computed(() => {
  switch (saveState.value) {
    case 'pending':
      return t('workbench.editor.saveDraftPending')
    case 'saving':
      return t('workbench.editor.saveDraftSaving')
    case 'error':
      return t('workbench.editor.saveDraftError')
    case 'conflict':
      return t('workbench.editor.saveDraftConflict')
    default:
      return t('workbench.editor.saveDraftSaved')
  }
})

const modeOptions = [
  { value: 'page' as const, icon: 'file', labelKey: 'workbench.journals.mode.page' },
  { value: 'edgeless' as const, icon: 'artboard', labelKey: 'workbench.journals.mode.edgeless' },
] as const

const grouped = computed(() => groupByDate(store.journals))
const dayEntries = computed(() => grouped.value.get(selectedDate.value) ?? [])

function dateLabel(date: string): string {
  return t('workbench.journals.dateLabel', {
    month: Number(date.slice(5, 7)),
    day: Number(date.slice(8, 10)),
  })
}

/** 拉当月月历；失败只提示一次，面板保持上一次数据。 */
async function loadMonth(month: string) {
  try {
    await store.fetchJournals(month)
  } catch {
    MessagePlugin.error(t('workbench.list.loadFailed'))
  }
}

/**
 * 切换选中日（§9.13.1）。
 *
 * 先收敛草稿：保存失败或冲突就留在原日期，避免把未落盘的改动带丢。
 * 再用 replace 更新路由，避免在浏览器历史里堆出一串日期。
 */
async function selectDate(date: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return
  if (date === selectedDate.value && currentItemId.value) return
  if (!(await convergeDraft())) return
  selectedDate.value = date
  if (!date.startsWith(store.journalsMonth)) {
    await loadMonth(date.slice(0, 7))
  }
  await resolveDay(date)
}

/**
 * 收敛当前草稿：flush 后仍处于冲突/失败则中止导航。
 *
 * 只有可靠落盘的改动才允许切换日期或条目（§9.7、§9.13.1）。
 */
async function convergeDraft(): Promise<boolean> {
  if (!currentItemId.value) return true
  try {
    await session.flush()
  } catch {
    MessagePlugin.warning(t('workbench.journals.flushFailed'))
    return false
  }
  if (!session.canLeave()) {
    MessagePlugin.warning(t('workbench.journals.flushFailed'))
    return false
  }
  return true
}

/** 选中日确定后决定打开哪一篇：0 条空态、1 条直开、多条不擅自选（§9.13.4）。 */
async function resolveDay(date: string) {
  const rows = groupByDate(store.journals).get(date) ?? []
  const keep = rows.find((row) => row.id === currentItemId.value)
  // ?itemId= 恢复时正文可能还没挂载：命中条目但无 currentEntry 仍需加载一次。
  if (keep && currentEntry.value) return
  if (keep) {
    await openEntry(keep.id)
    return
  }
  if (rows.length === 1) {
    await openEntry(rows[0].id)
    return
  }
  currentItemId.value = ''
  currentEntry.value = null
  session.destroy()
  await syncQuery()
}

async function openEntry(id: string) {
  if (id === currentItemId.value && currentEntry.value) return
  if (!(await convergeDraft())) return
  const entry = store.journals.find((row) => row.id === id)
  if (entry) selectedDate.value = entry.journal_date
  currentItemId.value = id
  loadError.value = false
  try {
    const item = await getWorkbenchItem(id)
    // 详情接口不填归档状态：月历批量字段才是权威口径（§8.3），这里合并进来，
    // 避免已归档的 Journal 顶栏误显示“未归档”。
    currentEntry.value = entry
      ? {
          ...item,
          archive_status: entry.archive_status,
          archived_version: entry.archived_version,
        }
      : item
    mode.value = ((entry?.current_mode ?? item.current_mode) ?? 'page') as EditorMode
    session.destroy()
    await nextTick()
    await session.mount()
  } catch (error) {
    loadError.value = workbenchErrorCode(error) !== WORKBENCH_ERROR.notFound
    currentEntry.value = null
  }
  await syncQuery()
}

/** 形态切换只重建视图层，不动 Y.Doc，也不产生新版本（§7.10）。 */
async function switchMode(next: EditorMode) {
  if (mode.value === next || !currentEntry.value) return
  mode.value = next
  session.editor.value?.setMode(next)
  try {
    const updated = await updateWorkbenchItem(currentItemId.value, { current_mode: next })
    currentEntry.value = updated
    const entry = store.journals.find((row) => row.id === updated.id)
    if (entry) entry.current_mode = next
  } catch {
    // 形态写回失败只影响下次打开的默认形态，不阻断当前编辑。
  }
}

async function createJournal() {
  try {
    const item = await store.createJournal(selectedDate.value)
    await loadMonth(selectedDate.value.slice(0, 7))
    await openEntry(item.id)
  } catch {
    MessagePlugin.error(t('workbench.journals.createFailed'))
  }
}

async function stepWeek(delta: number) {
  await selectDate(addDays(selectedDate.value, delta))
}

async function stepMonth(delta: number) {
  // 只翻月历，不切选中日与正文
  await loadMonth(shiftMonth(store.journalsMonth, delta))
}

/**
 * 拖拽改期：乐观更新 → 失败真回滚并 toast（§9.13.5）。
 * 只写 workbench_items.journal_date，不碰 Y.Doc 与归档。
 */
async function onMoveEntry(payload: { id: string; date: string }) {
  const entry = store.journals.find((row) => row.id === payload.id)
  if (!entry || entry.journal_date === payload.date) return
  const previous = entry.journal_date
  entry.journal_date = payload.date
  try {
    await store.setJournalDate(payload.id, payload.date)
    if (payload.id === currentItemId.value) selectedDate.value = payload.date
    // 拖到别的月份时当前月数据已过期，重拉一次保持月格与列表一致
    if (payload.date.slice(0, 7) !== store.journalsMonth) {
      await loadMonth(store.journalsMonth)
    }
  } catch {
    entry.journal_date = previous
    MessagePlugin.error(t('workbench.journals.moveFailed'))
  }
}

async function onArchiveAction(action: ArchiveBadgeAction) {
  if (!currentEntry.value || action === 'none' || action === 'progress') return
  const markdown = session.markdown()
  if (!markdown.trim()) {
    MessagePlugin.warning(t('workbench.archive.status.none'))
    return
  }
  const payload = {
    source_type: 'item' as const,
    source_id: currentItemId.value,
    base_version: baseVersion.value,
    markdown,
  }
  try {
    if (action === 'create' || action === 'recreate') await createArchive(payload)
    else await refreshArchive(payload)
    // 归档后乐观刷新当天条目，再重拉当前月一次（§9.13.2）
    await loadMonth(store.journalsMonth)
    const rows = groupByDate(store.journals).get(selectedDate.value) ?? []
    const refreshed = rows.find((row) => row.id === currentItemId.value)
    if (refreshed && currentEntry.value) {
      currentEntry.value = {
        ...currentEntry.value,
        archive_status: refreshed.archive_status,
        archived_version: refreshed.archived_version,
      }
    }
    MessagePlugin.success(t('workbench.archive.action.create'))
  } catch (error) {
    if (workbenchErrorCode(error) === WORKBENCH_ERROR.archiveStale) {
      MessagePlugin.warning(t('workbench.archive.status.stale'))
      return
    }
    MessagePlugin.error(t('workbench.editor.saveError'))
  }
}

function openConflictDialog() {
  const dialog = DialogPlugin({
    header: t('workbench.editor.saveDraftConflict'),
    body: t('workbench.editor.conflictBody'),
    confirmBtn: t('workbench.editor.conflictOverwrite'),
    cancelBtn: t('workbench.editor.conflictReload'),
    onConfirm: async () => {
      await session.forceSave()
      dialog.hide()
    },
    onCancel: async () => {
      dialog.hide()
      await session.reload()
    },
  })
}

async function reloadDay() {
  loadError.value = false
  await loadMonth(store.journalsMonth)
  if (currentItemId.value) await openEntry(currentItemId.value)
}

/** 路由同步：稳定 ID 写进 ?itemId=，刷新后能恢复同一条（§9.13.1）。 */
async function syncQuery() {
  const query: Record<string, string> = {}
  if (currentItemId.value) query.itemId = currentItemId.value
  await router.replace({
    name: 'workbenchJournalDay',
    params: { date: selectedDate.value },
    query,
  })
}

function goBack() {
  void router.push({ name: 'workbenchRecent' })
}

watch(
  () => props.date,
  (next) => {
    if (!next || next === selectedDate.value) return
    void selectDate(next)
  },
)

onBeforeUnmount(() => {
  void session.flush()
  session.destroy()
})

// 首屏：先拉当前月，再按路由选中的日期收敛（月与日各一次请求）。
void (async () => {
  const month = selectedDate.value.slice(0, 7) || currentMonth()
  await loadMonth(month)
  await resolveDay(selectedDate.value)
})()
</script>

<style lang="less" scoped>
.journals {
  flex: 1;
  min-height: 0;
  display: flex;
  background: var(--td-bg-color-page);
}

.journals-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
}

.j-head {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-1) var(--app-space-4);
  border-bottom: 1px solid var(--td-component-stroke);
}

.j-crumb {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);

  .sep {
    opacity: 0.6;
  }
}

.j-title {
  max-width: 230px;
  overflow: hidden;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.j-save-state {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);

  &.is-error,
  &.is-conflict {
    color: var(--td-error-color);
  }
}

.spacer {
  flex: 1;
}

.mode-switch {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xs);

  button {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 2px var(--app-space-2);
    border-radius: var(--app-radius-xs);
    font-size: var(--app-text-2xs);
    color: var(--td-text-color-secondary);

    &.is-active {
      background: var(--td-brand-color-1);
      color: var(--td-brand-color);
    }

    &:disabled {
      opacity: 0.5;
    }
  }
}

.j-editor {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.j-mount {
  position: absolute;
  inset: 0;
  overflow: hidden;

  &.is-hidden {
    visibility: hidden;
  }
}

.j-loading {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}

.j-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-3);
  padding: var(--app-space-6);
  text-align: center;
}

.je-icon {
  width: 54px;
  height: 54px;
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-lg);
  background: var(--td-brand-color-1);
  color: var(--td-brand-color);
}

.je-date {
  margin: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.j-empty h2 {
  margin: 0;
  font-size: var(--app-text-xl);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.je-hint {
  margin: 0 0 var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.je-choice {
  max-width: 320px;
}

.j-error {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-3);
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}
</style>
