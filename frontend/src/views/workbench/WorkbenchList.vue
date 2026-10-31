<template>
  <!-- 列表页：最近 / 数据库 / 收藏 / 回收站 / 文件夹 共用一个组件，靠 scope 区分 -->
  <section class="wb-page">
    <header class="page-head">
      <div>
        <div class="page-title">{{ title }}</div>
        <div class="page-sub">{{ subtitle }}</div>
      </div>
      <span class="spacer" />
      <t-button v-if="scope !== 'trash'" variant="outline" @click="create('database')">
        <template #icon><t-icon name="table" /></template>
        {{ $t('workbench.list.newDatabase') }}
      </t-button>
      <t-button v-if="scope !== 'trash'" theme="primary" @click="create('canvas')">
        <template #icon><t-icon name="add" /></template>
        {{ $t('workbench.list.newCanvas') }}
      </t-button>
    </header>

    <div class="list-toolbar">
      <t-input
        v-model="keyword"
        class="list-search"
        :placeholder="$t('workbench.list.searchPlaceholder')"
        clearable
        @change="reload"
      >
        <template #prefix-icon><t-icon name="search" /></template>
      </t-input>
      <span class="spacer" />
      <span class="toolbar-meta">{{ $t('workbench.list.total', { count: store.items.length }) }}</span>
      <!-- 批量归档入口只在列表页；编辑器与 Journals 不做多选（§9.8.1） -->
      <t-button
        v-if="scope !== 'trash' && store.items.length > 0"
        variant="text"
        size="small"
        @click="toggleSelectMode"
      >
        {{ selectMode ? $t('workbench.batchArchive.exitSelect') : $t('workbench.batchArchive.selectMode') }}
      </t-button>
      <t-radio-group v-model="viewMode" variant="default-filled" size="small">
        <t-radio-button value="cards">{{ $t('workbench.list.cards') }}</t-radio-button>
        <t-radio-button value="list">{{ $t('workbench.list.list') }}</t-radio-button>
      </t-radio-group>
    </div>

    <BatchArchiveBar
      v-if="selectMode"
      class="batch-bar-host"
      :selected-count="selectedIds.size"
      :actionable-count="actionableSelection.length"
      :skipped-count="selectedIds.size - actionableSelection.length"
      @select-all-actionable="selectAllActionable"
      @archive="openBatchDialog"
      @cancel="toggleSelectMode"
    />

    <div v-if="store.loading" class="wb-loading">
      <t-loading :loading="true" size="small" />
    </div>

    <template v-else-if="store.items.length === 0">
      <div class="wb-empty">
        <div class="empty-title">{{ emptyTitle }}</div>
        <div class="empty-sub">{{ emptyHint }}</div>
        <t-button v-if="keyword" size="small" variant="outline" @click="clearSearch">
          {{ $t('workbench.list.clearSearch') }}
        </t-button>
        <t-button v-else-if="scope !== 'trash'" size="small" theme="primary" @click="create('canvas')">
          {{ $t('workbench.list.newCanvas') }}
        </t-button>
      </div>
    </template>

    <div v-else class="card-grid" :class="`is-${viewMode}`">
      <div
        v-for="item in store.items"
        :key="item.id"
        class="card-cell"
        :class="{ 'is-selecting': selectMode, 'is-checked': selectedIds.has(item.id) }"
      >
        <!-- 选择模式下点卡片是勾选而不是打开（§9.8.1） -->
        <label v-if="selectMode" class="card-check">
          <input
            type="checkbox"
            :checked="selectedIds.has(item.id)"
            :aria-label="item.title"
            @change="toggleCandidate(item)"
          />
        </label>
        <WorkbenchItemCard
          :item="item"
          :trashed="scope === 'trash'"
          @open="selectMode ? toggleCandidate(item) : open(item)"
          @favorite="toggleFavorite"
          @archive="onArchive"
          @rename="rename"
          @move="move"
          @pin="pinToDate"
          @trash-item="trash"
          @delete-archives="deleteArchives"
          @restore="restore"
          @purge="purge"
        />
      </div>
    </div>

    <div v-if="store.nextCursor && !store.loading" class="wb-load-more">
      <t-button variant="text" :loading="store.loadingMore" @click="loadMore">
        {{ $t('workbench.list.loadMore') }}
      </t-button>
    </div>

    <BatchArchiveDialog
      v-model="batchDialogOpen"
      :selected="batchSelection"
      :dropped="batchDropped"
      @finished="onBatchFinished"
    />
  </section>
</template>

<script setup lang="ts">
/**
 * 工作台列表页。
 *
 * scope 由路由 props 注入；列表数据统一走 store，新建/删除后局部更新，
 * 避免整表重拉造成滚动位置丢失。
 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import WorkbenchItemCard from './components/WorkbenchItemCard.vue'
import BatchArchiveBar from './components/BatchArchiveBar.vue'
import BatchArchiveDialog from './components/BatchArchiveDialog.vue'
import { useWorkbenchStore } from '@/stores/workbench'
import { canDeleteArchive } from '@/workbench/archive-status'
import { batchOpFor, capBatchSelection, type BatchArchiveCandidate } from '@/workbench/batch-archive'
import { todayISO } from '@/workbench/journals/date-only'
import { deleteArchive } from '@/api/workbench'
import type { ArchiveBadgeAction } from '@/workbench/archive-status'
import type { WorkbenchItem, WorkbenchScope } from '@/types/workbench'

const props = defineProps<{
  scope: WorkbenchScope
  /** 文件夹视图专用；其他 scope 忽略 */
  folderId?: string
}>()

const router = useRouter()
const { t } = useI18n()
const store = useWorkbenchStore()

const keyword = ref('')
const viewMode = ref<'cards' | 'list'>('cards')

// ---- 批量归档（§9.8.1） ----
const selectMode = ref(false)
const selectedIds = ref(new Set<string>())
const batchDialogOpen = ref(false)
const batchSelection = ref<BatchArchiveCandidate[]>([])
const batchDropped = ref(0)

/**
 * 选中项里可归档的那些。
 *
 * 不可归档的条目仍允许勾选，但计数里单列「将跳过」，
 * 避免用户以为自己选错了（§9.8.1）。
 */
const actionableSelection = computed(() =>
  store.items.filter((item) => selectedIds.value.has(item.id) && batchOpFor(item.archive_status) !== 'skip'),
)

const title = computed(() => {
  switch (props.scope) {
    case 'database':
      return t('workbench.nav.databases')
    case 'favorites':
      return t('workbench.nav.favorites')
    case 'trash':
      return t('workbench.nav.trash')
    case 'folder':
      return store.folders.find((row) => row.id === props.folderId)?.name
        ?? t('workbench.nav.folders')
    default:
      return t('workbench.nav.recent')
  }
})

const subtitle = computed(() =>
  props.scope === 'trash' ? t('workbench.list.emptyTrashHint') : t('workbench.list.emptyHint'),
)

const emptyTitle = computed(() => {
  if (keyword.value) return t('workbench.list.emptySearchTitle')
  if (props.scope === 'trash') return t('workbench.list.emptyTrashTitle')
  return t('workbench.list.emptyTitle')
})

const emptyHint = computed(() => {
  if (keyword.value) return t('workbench.list.emptySearchHint')
  if (props.scope === 'trash') return t('workbench.list.emptyTrashHint')
  return t('workbench.list.emptyHint')
})

const query = computed(() => ({
  folderId: props.scope === 'folder' ? props.folderId : undefined,
  q: keyword.value.trim() || undefined,
}))

async function reload() {
  try {
    await store.fetchItems(props.scope, query.value)
  } catch {
    MessagePlugin.error(t('workbench.list.loadFailed'))
  }
}

function loadMore() {
  void store.loadMore(props.scope, query.value)
}

function clearSearch() {
  keyword.value = ''
  void reload()
}

watch(() => [props.scope, props.folderId], () => {
  // 切换筛选/文件夹时清空选择，防止“看不见的选中项”被一起归档（§9.8.1）。
  resetSelection()
  void reload()
})

function onItemsChanged() {
  void reload()
}

onMounted(() => {
  void reload()
  window.addEventListener('workbench:items-changed', onItemsChanged)
  window.addEventListener('keydown', onKeydown)
})

onUnmounted(() => {
  window.removeEventListener('workbench:items-changed', onItemsChanged)
  window.removeEventListener('keydown', onKeydown)
})

// ---- 批量归档操作 ----

function toggleSelectMode() {
  selectMode.value = !selectMode.value
  if (!selectMode.value) resetSelection()
}

function resetSelection() {
  selectedIds.value = new Set()
  selectMode.value = false
  batchSelection.value = []
  batchDropped.value = 0
}

function toggleCandidate(item: WorkbenchItem) {
  const next = new Set(selectedIds.value)
  if (next.has(item.id)) next.delete(item.id)
  else next.add(item.id)
  selectedIds.value = next
}

/** 全选「当前列表里可归档的条目」，不是全选所有（§9.8.1）。 */
function selectAllActionable() {
  const next = new Set(selectedIds.value)
  for (const item of store.items) {
    if (batchOpFor(item.archive_status) !== 'skip') next.add(item.id)
  }
  selectedIds.value = next
}

/** 预检：把选中项转成候选，超过 20 条时记录被丢弃的数量。 */
function openBatchDialog() {
  const candidates: BatchArchiveCandidate[] = store.items
    .filter((item) => selectedIds.value.has(item.id))
    .map((item) => ({
      key: item.id,
      sourceType: 'item' as const,
      sourceId: item.id,
      title: item.title,
      status: item.archive_status ?? 'none',
      baseVersion: item.snapshot_version,
    }))
  const { kept, dropped } = capBatchSelection(candidates, 20)
  batchSelection.value = kept
  batchDropped.value = dropped
  batchDialogOpen.value = true
}

/** 批量结束后只刷新一次列表与 KB 计数（§9.8.1），不逐条刷新。 */
async function onBatchFinished() {
  resetSelection()
  await reload()
  void store.fetchBootstrap()
}

/** Esc 退出选择模式；Cmd/Ctrl+A 只全选可归档项。 */
function onKeydown(event: KeyboardEvent) {
  if (!selectMode.value) return
  if (event.key === 'Escape') {
    event.preventDefault()
    resetSelection()
    return
  }
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'a') {
    // 焦点在输入框里时让浏览器自己全选文本。
    const tag = (event.target as HTMLElement | null)?.tagName
    if (tag === 'INPUT' || tag === 'TEXTAREA') return
    event.preventDefault()
    selectAllActionable()
  }
}

async function create(kind: 'canvas' | 'database') {
  try {
    const item = await store.createItem(kind, undefined, props.folderId ?? null)
    await router.push({ name: 'workbenchItem', params: { itemId: item.id } })
  } catch {
    MessagePlugin.error(t('workbench.editor.loadFailed'))
  }
}

function open(item: WorkbenchItem) {
  if (props.scope === 'trash') return
  void router.push({ name: 'workbenchItem', params: { itemId: item.id } })
}

async function toggleFavorite(item: WorkbenchItem) {
  try {
    await store.toggleFavorite(item)
  } catch {
    MessagePlugin.error(t('workbench.item.favoriteFailed'))
  }
}

async function rename(item: WorkbenchItem) {
  const next = window.prompt(t('workbench.item.rename'), item.title)
  if (!next || !next.trim() || next.trim() === item.title) return
  try {
    await store.renameItem(item.id, next.trim())
  } catch {
    MessagePlugin.error(t('workbench.item.renameFailed'))
  }
}

async function move(item: WorkbenchItem) {
  // “移动到文件夹”用原生 prompt 选名称：P1 不做拖拽树，避免为一次操作引入整套选择器。
  const names = store.folders.map((folder) => folder.name)
  if (names.length === 0) {
    MessagePlugin.info(t('workbench.list.emptyTitle'))
    return
  }
  const answer = window.prompt(`${t('workbench.item.move')}\n${names.join(' / ')}`, names[0])
  if (!answer) return
  const target = store.folders.find((folder) => folder.name === answer.trim())
  if (!target) {
    MessagePlugin.warning(t('workbench.item.renameFailed'))
    return
  }
  try {
    await store.moveItemToFolder(item.id, target.id)
  } catch {
    MessagePlugin.error(t('workbench.item.renameFailed'))
  }
}

/** 钉到某天 / 改期：写 YYYY-MM-DD；留空表示取消钉。 */
async function pinToDate(item: WorkbenchItem) {
  const answer = window.prompt('YYYY-MM-DD', item.journal_date ?? todayISO())
  if (answer === null) return
  const next = answer.trim()
  if (next && !/^\d{4}-\d{2}-\d{2}$/.test(next)) {
    MessagePlugin.warning('YYYY-MM-DD')
    return
  }
  try {
    await store.pinItemToDate(item.id, next)
  } catch {
    MessagePlugin.error(t('workbench.item.renameFailed'))
  }
}

async function trash(item: WorkbenchItem) {
  try {
    await store.trashItem(item.id, { deleteArchives: canDeleteArchive(item.archive_status) })
  } catch {
    MessagePlugin.error(t('workbench.item.deleteFailed'))
  }
}

async function restore(item: WorkbenchItem) {
  try {
    await store.restoreItem(item.id)
    MessagePlugin.success(t('workbench.item.restore'))
    await reload()
  } catch {
    MessagePlugin.error(t('workbench.item.restoreFailed'))
  }
}

function purge(item: WorkbenchItem) {
  const dialog = DialogPlugin({
    header: t('workbench.item.purge'),
    body: t('workbench.item.delete') + ' · ' + item.title,
    theme: 'warning',
    confirmBtn: t('workbench.item.purge'),
    onConfirm: async () => {
      try {
        await store.removeItemPermanently(item.id, {
          deleteArchives: canDeleteArchive(item.archive_status),
        })
        dialog.hide()
      } catch {
        MessagePlugin.error(t('workbench.item.deleteFailed'))
      }
    },
  })
}

/** 删除归档：先删知识再标映射由后端保证顺序（§7.6）。 */
function deleteArchives(item: WorkbenchItem) {
  const dialog = DialogPlugin({
    header: t('workbench.archive.deleteTitle'),
    body: t('workbench.archive.deleteBody'),
    theme: 'warning',
    confirmBtn: t('workbench.archive.deleteConfirm'),
    onConfirm: async () => {
      try {
        await deleteArchive({
          source_type: 'item',
          source_id: item.id,
          delete_knowledge: true,
        })
        MessagePlugin.success(t('workbench.archive.deleteTitle'))
        dialog.hide()
        await reload()
      } catch {
        MessagePlugin.error(t('workbench.item.deleteFailed'))
      }
    },
  })
}

/**
 * 归档动作。
 *
 * 列表页没有 Y.Doc，无法投影 Markdown，所以这里只做导航：
 * 归档/更新/重试都需要正文，必须在条目编辑页发起。
 */
async function onArchive(item: WorkbenchItem, action: ArchiveBadgeAction) {
  if (action === 'none' || action === 'progress') return
  await router.push({ name: 'workbenchItem', params: { itemId: item.id } })
}
</script>

<style lang="less" scoped>
.wb-page {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: var(--app-space-5) var(--app-space-6) 0;
  overflow: hidden;
}

.page-head {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-3);
  margin-bottom: var(--app-space-4);
}

.page-title {
  font-size: var(--app-text-3xl);
  font-weight: 600;
  line-height: 1.3;
  color: var(--td-text-color-primary);
}

.page-sub {
  margin-top: 3px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.spacer {
  flex: 1;
}

.list-toolbar {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  margin-bottom: var(--app-space-4);
}

.list-search {
  width: 260px;
}

.toolbar-meta {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.card-grid {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-bottom: var(--app-space-6);
  display: grid;
  gap: var(--app-space-4);
  align-content: start;
}

.card-grid.is-cards {
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
}

.card-grid.is-list {
  grid-template-columns: minmax(0, 1fr);
}

.card-cell {
  position: relative;
  min-width: 0;
}

/* 选择模式下让卡片接受点击勾选，并给选中项一个明确边框 */
.card-cell.is-checked :deep(.item-card) {
  border-color: var(--td-brand-color);
  box-shadow: 0 0 0 1px var(--td-brand-color);
}

.card-check {
  position: absolute;
  top: var(--app-space-2);
  left: var(--app-space-2);
  z-index: 2;
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border-radius: var(--app-radius-xs);
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);

  input {
    width: 14px;
    height: 14px;
    accent-color: var(--td-brand-color);
  }
}

.batch-bar-host {
  margin-bottom: var(--app-space-3);
}

.wb-loading {
  flex: 1;
  display: grid;
  place-items: center;
}

.wb-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-2);
  text-align: center;
}

.empty-title {
  font-size: var(--app-text-xl);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.empty-sub {
  max-width: 420px;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}

.wb-load-more {
  display: grid;
  place-items: center;
  padding: var(--app-space-3) 0 var(--app-space-5);
}
</style>
