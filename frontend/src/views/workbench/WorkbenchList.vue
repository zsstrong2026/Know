<template>
  <!-- 列表页：最近 / 数据库 / 收藏 / 回收站 / 文件夹 共用一个组件，靠 scope 区分 -->
  <section class="wb-page">
    <!-- 事项是全局单例：不渲染列表，进入后直接打开唯一事项 -->
    <div v-if="scope === 'database'" class="wb-loading">
      <t-loading :loading="true" size="small" />
    </div>

    <template v-else>
    <header class="page-head">
      <div>
        <div class="page-title">{{ title }}</div>
        <div class="page-sub">{{ subtitle }}</div>
      </div>
      <span class="spacer" />
      <!-- 回收站：一键清空（仅在有内容时出现） -->
      <t-button
        v-if="scope === 'trash' && store.items.length > 0"
        theme="danger"
        variant="outline"
        :loading="emptying"
        @click="confirmEmptyTrash"
      >
        <template #icon><t-icon name="delete" /></template>
        {{ $t('workbench.list.emptyTrashAction') }}
      </t-button>
      <!-- 导入资料（P4-1）：列表页只有「加入工作知识库」一条路径 -->
      <t-button v-if="scope !== 'trash'" variant="outline" @click="importOpen = true">
        <template #icon><t-icon name="upload" /></template>
        {{ $t('workbench.import.entry') }}
      </t-button>
      <!-- 新建四选（B5）：文档 / 画布 / 数据库 / 文件夹 -->
      <CreateMenu
        v-if="scope !== 'trash'"
        class="create-menu-host"
        :folder-id="scope === 'folder' ? folderId ?? null : null"
        @created="onCreated"
        @create-folder="requestCreateFolder"
      >
        <t-button theme="primary">
          <template #icon><t-icon name="add" /></template>
          {{ $t('workbench.new.label') }}
        </t-button>
      </CreateMenu>
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
      <!-- 按标签筛选（P4-2）：紧跟搜索框，与搜索/排序同高；没标签时不占位置 -->
      <t-select
        v-if="tags.length > 0 && scope !== 'trash'"
        v-model="tagId"
        class="list-tag-filter"
        clearable
        :placeholder="$t('workbench.tags.filterPlaceholder')"
        :options="tagOptions"
        @change="reload"
      />
      <!-- 排序（B5）：切换排序必须重置分页，cursor 与排序键绑定 -->
      <t-select
        v-model="sort"
        class="list-sort"
        :aria-label="$t('workbench.list.sortLabel')"
        :options="sortOptions"
        @change="reload"
      />
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
      :trashable-count="selectedIds.size"
      @select-all-actionable="selectAllActionable"
      @archive="openBatchDialog"
      @trash="confirmBatchTrash"
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
        <!-- 空态与顶部共用同一套新建菜单（§9.16.2） -->
        <CreateMenu
          v-else-if="scope !== 'trash'"
          :folder-id="scope === 'folder' ? folderId ?? null : null"
          @created="onCreated"
          @create-folder="requestCreateFolder"
        >
          <t-button size="small" theme="primary">{{ $t('workbench.new.label') }}</t-button>
        </CreateMenu>
      </div>
    </template>

    <div v-else-if="viewMode === 'cards'" class="card-grid">
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
          @menu="onItemMenu"
        />
      </div>
    </div>

    <!-- 列表视图：AFFiNE 风格——按更新时间分组，行内无边框、悬停浅底 -->
    <div v-else class="wb-rows">
      <section v-for="group in groupedItems" :key="group.day" class="rows-group">
        <div v-if="group.label" class="group-head">
          {{ group.label }} · {{ group.items.length }}
        </div>
        <WorkbenchItemRow
          v-for="item in group.items"
          :key="item.id"
          :item="item"
          :trashed="scope === 'trash'"
          :selectable="selectMode"
          :selected="selectedIds.has(item.id)"
          @open="selectMode ? toggleCandidate(item) : open(item)"
          @favorite="toggleFavorite"
          @archive="onArchive"
          @menu="onItemMenu"
        />
      </section>
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

    <!-- 导入资料（P4-1）：列表页不需要 itemId，「作为附件」路径自动隐藏 -->
    <ImportMaterialDialog v-model:visible="importOpen" />
    </template>
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
import WorkbenchItemRow from './components/WorkbenchItemRow.vue'
import BatchArchiveBar from './components/BatchArchiveBar.vue'
import BatchArchiveDialog from './components/BatchArchiveDialog.vue'
import ImportMaterialDialog from './components/ImportMaterialDialog.vue'
import CreateMenu from './components/CreateMenu.vue'
import { listWorkbenchTags } from '@/api/workbench'
import type { WorkbenchItemSort, WorkbenchTag } from '@/types/workbench'
import { useWorkbenchStore } from '@/stores/workbench'
import { canDeleteArchive } from '@/workbench/archive-status'
import { batchOpFor, capBatchSelection, type BatchArchiveCandidate } from '@/workbench/batch-archive'
import { todayISO } from '@/workbench/journals/date-only'
import { groupItemsByDay, todayStart } from '@/workbench/item-display'
import type { WorkbenchItemMenuAction } from '@/workbench/item-menu'
import { deleteArchive } from '@/api/workbench'
import type { ArchiveBadgeAction } from '@/workbench/archive-status'
import type { WorkbenchItem, WorkbenchScope } from '@/types/workbench'

const props = defineProps<{
  scope: WorkbenchScope
  /** 文件夹视图专用；其他 scope 忽略 */
  folderId?: string
}>()

const router = useRouter()
const { t, locale } = useI18n()
const store = useWorkbenchStore()

const keyword = ref('')
const viewMode = ref<'cards' | 'list'>('cards')
/** 列表排序（B5）：默认最近更新；切换后由 reload 重置分页 */
const sort = ref<WorkbenchItemSort>('updated')
const sortOptions = computed(() => [
  { label: t('workbench.list.sortRecent'), value: 'updated' },
  { label: t('workbench.list.sortCreated'), value: 'created' },
  { label: t('workbench.list.sortTitle'), value: 'title' },
])
/** 导入资料对话框（P4-1）；列表页只走「加入工作知识库」 */
const importOpen = ref(false)
/** 一键清空回收站请求中：按钮 loading，避免重复提交 */
const emptying = ref(false)

/**
 * 列表视图分组：只在「最近更新」排序下按天分组。
 *
 * 创建时间/名称排序时条目不再按 updated_at 连续，分组会碎成很多单条，
 * 因此退化为一个无标题分组，保持行渲染逻辑不变（B5）。
 */
const groupedItems = computed(() => {
  if (sort.value !== 'updated') {
    return [{ day: Number.NaN, items: store.items, label: '' }]
  }
  return groupItemsByDay(store.items).map((group) => ({
    day: group.day,
    items: group.items,
    label: group.day === todayStart() ? t('workbench.list.groupToday') : formatDayLabel(group.day),
  }))
})

/** 日期小标题：跟随界面语言；时间非法时留空，该组不显示标题 */
function formatDayLabel(day: number): string {
  if (!Number.isFinite(day)) return ''
  return new Date(day).toLocaleDateString(locale.value, {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  })
}

/** 「…」菜单只上报动作，卡片与列表行共用这一处派发 */
function onItemMenu(item: WorkbenchItem, action: WorkbenchItemMenuAction) {
  switch (action) {
    case 'open':
      open(item)
      break
    case 'rename':
      void rename(item)
      break
    case 'move':
      void move(item)
      break
    case 'pin':
      void pinToDate(item)
      break
    case 'archive':
      void onArchive(item, 'update')
      break
    case 'deleteArchives':
      deleteArchives(item)
      break
    case 'trash':
      void trash(item)
      break
    case 'restore':
      void restore(item)
      break
    case 'purge':
      purge(item)
      break
  }
}

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

/** 标签筛选（P4-2）：选项来自当前用户的个人标签 */
const tags = ref<WorkbenchTag[]>([])
const tagId = ref('')
const tagOptions = computed(() => tags.value.map((tag) => ({ label: tag.name, value: tag.id })))

async function loadTags() {
  try {
    tags.value = await listWorkbenchTags()
  } catch {
    // 标签加载失败不影响列表本身：当作没有标签即可
    tags.value = []
  }
}

/** 列表排序（B5）：默认最近更新；切换后由 reload 重置分页 */
const query = computed(() => ({
  folderId: props.scope === 'folder' ? props.folderId : undefined,
  q: keyword.value.trim() || undefined,
  tagId: tagId.value || undefined,
  sort: sort.value,
}))

async function reload() {
  try {
    await store.fetchItems(props.scope, query.value)
  } catch {
    MessagePlugin.error(t('workbench.list.loadFailed'))
  }
}

/** 事项是全局单例：进入后直接打开唯一事项，不渲染列表 */
async function openThings() {
  try {
    const item = await store.ensureThingsItem()
    await router.replace({
      name: 'workbenchItem',
      params: { itemId: item.id },
      query: { focus: 'database' },
    })
  } catch {
    MessagePlugin.error(t('workbench.list.loadFailed'))
  }
}

/** 统一入口：事项走单例跳转，其余 scope 走列表加载 */
function load() {
  if (props.scope === 'database') {
    void openThings()
    return
  }
  void reload()
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
  load()
})

function onItemsChanged() {
  load()
}

onMounted(() => {
  void loadTags()
  load()
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

/**
 * 批量移入回收站。
 *
 * 后端只有单条软删除接口，这里逐条串行调用，单条失败不阻塞其余条目；
 * 结束时统一刷新列表与计数，并把成功/失败数量明确告诉用户。
 */
function confirmBatchTrash() {
  const targets = store.items.filter((item) => selectedIds.value.has(item.id))
  if (targets.length === 0) return
  const dialog = DialogPlugin({
    header: t('workbench.batchArchive.trashTitle'),
    body: t('workbench.batchArchive.trashConfirm', { count: targets.length }),
    theme: 'warning',
    confirmBtn: t('workbench.batchArchive.trashSelected'),
    onConfirm: async () => {
      let failed = 0
      for (const item of targets) {
        try {
          await store.trashItem(item.id, {
            deleteArchives: canDeleteArchive(item.archive_status),
          })
        } catch {
          failed += 1
        }
      }
      dialog.hide()
      await onBatchFinished()
      if (failed > 0) {
        MessagePlugin.warning(
          t('workbench.batchArchive.trashPartial', { done: targets.length - failed, failed }),
        )
      } else {
        MessagePlugin.success(t('workbench.batchArchive.trashDone', { count: targets.length }))
      }
    },
  })
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

/**
 * 新建完成（CreateMenu 内部已跳转）：只刷新一次列表与计数。
 */
function onCreated() {
  void reload()
  void store.fetchBootstrap()
}

/**
 * 菜单里的「新建文件夹」：交给侧栏文件夹树的行内输入，
 * 列表页不再重复实现一套输入框（§9.5）。
 */
function requestCreateFolder() {
  window.dispatchEvent(new CustomEvent('workbench:create-folder'))
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

/** 一键清空回收站：二次确认后由服务端永久删除全部条目。 */
function confirmEmptyTrash() {
  const dialog = DialogPlugin({
    header: t('workbench.list.emptyTrashAction'),
    body: t('workbench.list.emptyTrashConfirm'),
    theme: 'warning',
    confirmBtn: t('workbench.list.emptyTrashAction'),
    onConfirm: async () => {
      emptying.value = true
      try {
        const purged = await store.emptyTrash()
        MessagePlugin.success(t('workbench.list.emptyTrashDone', { count: purged }))
        dialog.hide()
        void store.fetchBootstrap()
      } catch {
        MessagePlugin.error(t('workbench.list.emptyTrashFailed'))
      } finally {
        emptying.value = false
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

/**
 * 工具条控件统一高度。
 *
 * 搜索框是默认尺寸（32px），两个下拉原先用 size="small"（24px），
 * 并排时高矮不齐、文字基线也错位；现在下拉都用默认尺寸，
 * 这里再钉一次高度兜住不同主题下的差异。
 */
.list-toolbar :deep(.t-select__wrap),
.list-toolbar :deep(.t-input) {
  height: 32px;
}

.list-search {
  width: 260px;
  flex: none;
}

/* 标签筛选：紧跟搜索框，宽度按「筛选标签」占位文案留余量 */
.list-tag-filter {
  width: 168px;
  flex: none;
}

/* 排序下拉：与搜索框同高，宽度按最长文案留余量，避免中/俄文被截断 */
.list-sort {
  width: 132px;
  flex: none;
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
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: var(--app-space-4);
  align-content: start;
}

/* 列表视图：白底容器 + 圆角，行本身无边框，靠悬停底色分界（对齐 AFFiNE） */
.wb-rows {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--app-space-1) var(--app-space-2) var(--app-space-3);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
}

/* 日期小标题：左缘与行内图标对齐，避免标题比行内容更“缩进” */
.group-head {
  padding: var(--app-space-3) 10px var(--app-space-1);
  font-size: var(--app-text-sm);
  line-height: 18px;
  color: var(--td-text-color-placeholder);
  user-select: none;
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
