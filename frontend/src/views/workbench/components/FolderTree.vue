<template>
  <!-- 文件夹树：AFFiNE 风格的行内管理；行尾菜单承载新建文档、子文件夹、重命名与删除 -->

  <div class="folder-tree">
    <template v-for="row in displayRows" :key="row.key">
      <div
        v-if="row.kind === 'folder'"
        class="ft-row"
        :class="{
          'is-active': row.folder.id === activeId,
          'is-dragging': draggingId === row.folder.id,
          'drop-before': dropTarget?.id === row.folder.id && dropTarget.zone === 'before',
          'drop-after': dropTarget?.id === row.folder.id && dropTarget.zone === 'after',
          'drop-inside': dropTarget?.id === row.folder.id && dropTarget.zone === 'inside',
        }"
        :style="{ paddingLeft: `${row.depth * 12 + 8}px` }"
        role="button"
        tabindex="0"
        :draggable="renamingId !== row.folder.id"
        @click="onRowClick(row)"
        @keydown.enter.self="emit('select', row.folder.id)"
        @keydown.space.prevent.self="toggle(row.folder.id)"
        @dragstart="onDragStart(row, $event)"
        @dragover="onDragOver(row, $event)"
        @dragleave="onDragLeave(row, $event)"
        @drop="onDrop(row, $event)"
        @dragend="resetDrag"
      >
        <button
          v-if="row.hasChildren"
          class="ft-chev"
          type="button"
          :aria-label="isExpanded(row.folder.id) ? $t('workbench.nav.collapseFolder') : $t('workbench.nav.expandFolder')"
          @click.stop="toggle(row.folder.id)"
        >
          <t-icon :name="isExpanded(row.folder.id) ? 'chevron-down' : 'chevron-right'" size="11px" />
        </button>
        <span v-else class="ft-chev-placeholder" />

        <t-icon
          :name="row.hasChildren && isExpanded(row.folder.id) ? 'folder-open' : 'folder'"
          size="13px"
          class="ft-icon"
        />

        <input
          v-if="renamingId === row.folder.id"
          ref="renameInputRef"
          v-model="renameValue"
          class="ft-input"
          :placeholder="$t('workbench.nav.folderNamePlaceholder')"
          @click.stop
          @keydown.enter.stop.prevent="commitRename(row)"
          @keydown.esc.stop.prevent="cancelRename"
          @blur="commitRename(row)"
        />
        <span v-else class="ft-name">{{ row.folder.name }}</span>

        <!-- 行尾「…」：常驻弱化显示，悬停/选中提亮，保证文件夹操作可发现 -->
        <t-dropdown
          v-if="renamingId !== row.folder.id"
          :options="folderMenuOptions"
          trigger="click"
          placement="bottom-right"
          attach="body"
          @click="onFolderMenu(row, $event)"
        >
          <button
            class="ft-more"
            type="button"
            :aria-label="$t('workbench.nav.folderActions')"
            @click.stop
          >
            <t-icon name="more" size="15px" />
          </button>
        </t-dropdown>
      </div>

      <!-- 行内新建：根级追加在末尾，子文件夹紧跟父行 -->
      <div
        v-else
        class="ft-row ft-row--create"
        :style="{ paddingLeft: `${row.depth * 12 + 8}px` }"
      >
        <span class="ft-chev-placeholder" />
        <t-icon name="folder" size="13px" class="ft-icon" />
        <input
          ref="createInputRef"
          v-model="createValue"
          class="ft-input"
          :placeholder="$t('workbench.nav.folderNamePlaceholder')"
          @keydown.enter.stop.prevent="commitCreate"
          @keydown.esc.stop.prevent="cancelCreate"
          @blur="commitCreate"
        />
      </div>
    </template>

    <button
      v-if="folders.length === 0 && creatingParentId === undefined"
      class="ft-empty"
      type="button"
      @click="startCreate(null)"
    >
      <t-icon name="add" size="12px" />
      <span>{{ $t('workbench.nav.newFolder') }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
/**
 * 文件夹树（单层组件，不递归）。
 *
 * 后端最多 5 层，扁平化后按 depth 缩进即可；折叠状态只影响展示，
 * 不改变数据，因此切换视图不会丢失折叠记忆（组件内 ref 保留）。
 *
 * 交互参考 AFFiNE：新建/重命名走行内输入，文件夹行尾菜单负责
 * 「新建文档、新建子文件夹、重命名、删除」。画布只是文档的查看模式，
 * 不再是独立的创建项。
 */
import { computed, h, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon as TIcon } from 'tdesign-vue-next'
import type { WorkbenchFolder } from '@/types/workbench'
import { isSelfOrDescendant, resolveFolderDrop, type FolderDropZone } from '@/workbench/folder-drop'

const props = withDefaults(
  defineProps<{
    folders: WorkbenchFolder[]
    /** 当前选中的文件夹；来自路由参数 */
    activeId?: string
  }>(),
  { activeId: '' },
)

const emit = defineEmits<{
  select: [folderId: string]
  'create-folder': [payload: { name: string; parentId: string | null }]
  'rename-folder': [payload: { id: string; name: string }]
  'delete-folder': [folder: WorkbenchFolder]
  'add-existing': [folder: WorkbenchFolder]
  /** 文件夹只收纳文档，数据库不参与文件夹分类 */
  'create-item': [payload: { folderId: string }]
  /** 拖拽排序 / 跨层移动：position 已按「排除自己之后」的口径算好 */
  'move-folder': [payload: { id: string; parentId: string | null; position: number }]
}>()

const { t } = useI18n()

// ---- 拖拽排序 / 跨层移动（P4-2） ----
// 落点分区：行的上 28% / 下 28% 是「排到前/后」，中间是「放进这个文件夹」。
const draggingId = ref('')
const dropTarget = ref<{ id: string; zone: FolderDropZone } | null>(null)
/** 拖拽刚结束时浏览器可能补一个 click，用一次性开关挡掉，避免误导航 */
let suppressClick = false

function onDragStart(row: { folder: WorkbenchFolder }, event: DragEvent) {
  // 重命名过程中不允许拖：输入框的选区拖拽会和这里抢事件
  if (renamingId.value === row.folder.id) {
    event.preventDefault()
    return
  }
  draggingId.value = row.folder.id
  dropTarget.value = null
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    // 部分浏览器只有 setData 之后才会真正开始拖拽
    event.dataTransfer.setData('text/plain', row.folder.id)
  }
}

function onDragOver(row: { folder: WorkbenchFolder }, event: DragEvent) {
  if (!draggingId.value || draggingId.value === row.folder.id) return
  // 拖到自己的子孙上不接受：否则会成环，服务端也会拒绝
  if (isSelfOrDescendant(props.folders, draggingId.value, row.folder.id)) {
    dropTarget.value = null
    return
  }
  event.preventDefault()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
  const ratio = rect.height > 0 ? (event.clientY - rect.top) / rect.height : 0.5
  const zone: FolderDropZone = ratio < 0.28 ? 'before' : ratio > 0.72 ? 'after' : 'inside'
  dropTarget.value = { id: row.folder.id, zone }
}

function onDragLeave(row: { folder: WorkbenchFolder }, event: DragEvent) {
  // 移到行内子元素也会触发 dragleave；真的离开这一行才清提示
  const next = event.relatedTarget as Node | null
  if (next && (event.currentTarget as HTMLElement).contains(next)) return
  if (dropTarget.value?.id === row.folder.id) dropTarget.value = null
}

function onDrop(row: { folder: WorkbenchFolder }, event: DragEvent) {
  event.preventDefault()
  const zone = dropTarget.value?.id === row.folder.id ? dropTarget.value.zone : null
  const id = draggingId.value
  resetDrag()
  if (!id || !zone) return
  const plan = resolveFolderDrop(props.folders, id, row.folder.id, zone)
  if (!plan) return
  emit('move-folder', { id, parentId: plan.parentId, position: plan.position })
}

function resetDrag() {
  const wasDragging = Boolean(draggingId.value)
  draggingId.value = ''
  dropTarget.value = null
  if (wasDragging) {
    suppressClick = true
    window.setTimeout(() => {
      suppressClick = false
    }, 0)
  }
}

/** 行点击：拖拽刚结束时忽略一次，其余照常选中 */
function onRowClick(row: { folder: WorkbenchFolder }) {
  if (suppressClick) return
  emit('select', row.folder.id)
}

const collapsed = ref(new Set<string>())
/** undefined=未在新建；null=根级新建；string=在某文件夹下新建 */
const creatingParentId = ref<string | null | undefined>(undefined)
const createValue = ref('')
const createInputRef = ref<HTMLInputElement | HTMLInputElement[] | null>(null)
const renamingId = ref('')
const renameValue = ref('')
const renameInputRef = ref<HTMLInputElement | HTMLInputElement[] | null>(null)

type FolderRow = {
  folder: WorkbenchFolder
  depth: number
  hasChildren: boolean
}

type DisplayRow =
  | (FolderRow & { kind: 'folder'; key: string })
  | { kind: 'create'; key: string; parentId: string | null; depth: number }

/** 先按 parent_id 建索引，再深度优先摊平；孤儿节点（父级已删）提到顶层。 */
const rows = computed<FolderRow[]>(() => {
  const byParent = new Map<string, WorkbenchFolder[]>()
  const ids = new Set(props.folders.map((folder) => folder.id))
  for (const folder of props.folders) {
    const parent = folder.parent_id && ids.has(folder.parent_id) ? folder.parent_id : ''
    const bucket = byParent.get(parent) ?? []
    bucket.push(folder)
    byParent.set(parent, bucket)
  }

  const out: FolderRow[] = []
  const walk = (parentID: string, depth: number) => {
    const children = (byParent.get(parentID) ?? [])
      .slice()
      .sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name))
    for (const folder of children) {
      const hasChildren = (byParent.get(folder.id)?.length ?? 0) > 0
      out.push({ folder, depth, hasChildren })
      walk(folder.id, depth + 1)
    }
  }
  walk('', 0)
  return out
})

/** 折叠某节点时，把它所有后代的行一并隐藏（深度优先顺序保证父在前）。 */
const visibleRows = computed<FolderRow[]>(() => {
  const hidden = new Set<string>()
  const out: FolderRow[] = []
  for (const row of rows.value) {
    const parentID = row.folder.parent_id ?? ''
    if (hidden.has(parentID)) {
      hidden.add(row.folder.id)
      continue
    }
    out.push(row)
    if (collapsed.value.has(row.folder.id)) hidden.add(row.folder.id)
  }
  return out
})

/** 可见文件夹行 + 正在编辑的新建行，统一交给模板渲染。 */
const displayRows = computed<DisplayRow[]>(() => {
  const out: DisplayRow[] = []
  for (const row of visibleRows.value) {
    out.push({ kind: 'folder', key: row.folder.id, ...row })
    if (creatingParentId.value === row.folder.id) {
      out.push({
        kind: 'create',
        key: `create-${row.folder.id}`,
        parentId: row.folder.id,
        depth: row.depth + 1,
      })
    }
  }
  if (creatingParentId.value === null) {
    out.push({ kind: 'create', key: 'create-root', parentId: null, depth: 0 })
  }
  return out
})

const foldersById = computed(() => new Map(props.folders.map((folder) => [folder.id, folder])))

const folderMenuOptions = computed(() => [
  {
    content: t('workbench.new.document'),
    value: 'document',
    prefixIcon: () => h(TIcon, { name: 'file' }),
  },
  {
    content: t('workbench.nav.addExistingDocs'),
    value: 'add-existing',
    prefixIcon: () => h(TIcon, { name: 'file-add' }),
    divider: true,
  },
  {
    content: t('workbench.nav.newSubfolder'),
    value: 'subfolder',
    prefixIcon: () => h(TIcon, { name: 'folder-add' }),
    divider: true,
  },
  {
    content: t('workbench.item.rename'),
    value: 'rename',
    prefixIcon: () => h(TIcon, { name: 'edit' }),
    divider: true,
  },
  {
    content: t('workbench.nav.deleteFolderTitle'),
    value: 'delete',
    theme: 'error',
    prefixIcon: () => h(TIcon, { name: 'delete' }),
  },
])

const isExpanded = (folderId: string) => !collapsed.value.has(folderId)

function toggle(folderId: string) {
  const next = new Set(collapsed.value)
  if (next.has(folderId)) next.delete(folderId)
  else next.add(folderId)
  collapsed.value = next
}

function ensureExpanded(folderId: string) {
  if (!collapsed.value.has(folderId)) return
  const next = new Set(collapsed.value)
  next.delete(folderId)
  collapsed.value = next
}

/** 路由选中某个深层文件夹时，自动展开它的全部祖先，避免选中项被折叠隐藏。 */
function expandAncestors() {
  if (!props.activeId) return
  const next = new Set(collapsed.value)
  const seen = new Set<string>()
  let current = props.activeId
  while (current && !seen.has(current)) {
    seen.add(current)
    const folder = foldersById.value.get(current)
    if (!folder?.parent_id) break
    next.delete(folder.parent_id)
    current = folder.parent_id
  }
  collapsed.value = next
}

watch(() => props.activeId, expandAncestors, { immediate: true })

function resolveInput(value: HTMLInputElement | HTMLInputElement[] | null): HTMLInputElement | null {
  return Array.isArray(value) ? value[0] ?? null : value
}

/** 供父组件（头部「+」）触发根级新建。 */
function startCreate(parentId: string | null) {
  if (parentId) ensureExpanded(parentId)
  creatingParentId.value = parentId
  createValue.value = ''
}

watch(creatingParentId, async (value) => {
  if (value === undefined) return
  await nextTick()
  const input = resolveInput(createInputRef.value)
  input?.focus()
  input?.scrollIntoView({ block: 'nearest' })
})

function commitCreate() {
  if (creatingParentId.value === undefined) return
  const parentId = creatingParentId.value
  const name = createValue.value.trim()
  creatingParentId.value = undefined
  createValue.value = ''
  if (!name) return
  emit('create-folder', { name, parentId })
}

function cancelCreate() {
  creatingParentId.value = undefined
  createValue.value = ''
}

async function startRename(row: FolderRow) {
  renamingId.value = row.folder.id
  renameValue.value = row.folder.name
  await nextTick()
  const input = resolveInput(renameInputRef.value)
  input?.focus()
  input?.select()
}

function commitRename(row: FolderRow) {
  if (renamingId.value !== row.folder.id) return
  const name = renameValue.value.trim()
  renamingId.value = ''
  renameValue.value = ''
  if (!name || name === row.folder.name) return
  emit('rename-folder', { id: row.folder.id, name })
}

function cancelRename() {
  renamingId.value = ''
  renameValue.value = ''
}

function onFolderMenu(row: FolderRow, data: { value?: string | number }) {
  const action = String(data.value ?? '')
  switch (action) {
    case 'document':
      emit('create-item', { folderId: row.folder.id })
      break
    case 'subfolder':
      startCreate(row.folder.id)
      break
    case 'add-existing':
      emit('add-existing', row.folder)
      break
    case 'rename':
      void startRename(row)
      break
    case 'delete':
      emit('delete-folder', row.folder)
      break
  }
}

defineExpose({ startCreate })
</script>

<style lang="less" scoped>
.folder-tree {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

/* 胶囊行：与 WorkbenchNav 的一级菜单保持同一套圆角与悬停语言 */
.ft-row {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 29px;
  /* 拖拽中的三种落点提示：上下是排序线，整行底色是「放进去」 */
  &.is-dragging {
    opacity: 0.45;
  }

  &.drop-before {
    box-shadow: inset 0 2px 0 var(--td-brand-color);
  }

  &.drop-after {
    box-shadow: inset 0 -2px 0 var(--td-brand-color);
  }

  &.drop-inside {
    background: var(--td-brand-color-light);
  }
  padding-right: var(--app-space-2);
  border-radius: var(--app-radius-pill);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  text-align: left;
  transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &.is-active {
    background: var(--td-brand-color-1);
    color: var(--td-brand-color-7);
    font-weight: 500;
  }
}

.ft-chev,
.ft-chev-placeholder {
  width: 15px;
  height: 15px;
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.ft-chev {
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-xs);
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.ft-icon {
  flex: none;
  color: var(--td-text-color-placeholder);
}

.ft-row.is-active .ft-icon {
  color: var(--td-brand-color-7);
}

.ft-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ft-input {
  flex: 1;
  min-width: 0;
  height: 22px;
  padding: 0 6px;
  border: 1px solid var(--td-brand-color);
  border-radius: var(--app-radius-xs);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: var(--app-text-sm);
  outline: none;
}

.ft-more {
  width: 22px;
  height: 22px;
  margin-left: auto;
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-pill);
  background: transparent;
  color: var(--td-text-color-secondary);
  /* 常驻可见但弱化，保证文件夹操作可发现；悬停/选中时提亮 */
  opacity: 0.55;
  cursor: pointer;
  transition: opacity var(--app-motion-fast) ease, background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }
}

.ft-row.is-active .ft-more {
  opacity: 0.8;
  color: var(--td-brand-color-7);
}

.ft-row:hover .ft-more,
.ft-row:focus-within .ft-more {
  opacity: 1;
  color: var(--td-text-color-primary);
}

.ft-row:hover .ft-more:hover {
  color: var(--td-brand-color);
}

.ft-row--create {
  background: transparent;
  cursor: default;

  &:hover {
    background: transparent;
  }
}

.ft-empty {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 29px;
  padding: 0 9px;
  border: 0;
  border-radius: var(--app-radius-pill);
  background: transparent;
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family);
  font-size: var(--app-text-sm);
  text-align: left;
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}
</style>
