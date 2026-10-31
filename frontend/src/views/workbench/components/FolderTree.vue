<template>
  <!-- 文件夹树：把扁平 folders 摊平成带层级的行，折叠时过滤掉子孙 -->
  <div class="folder-tree">
    <button
      v-for="row in visibleRows"
      :key="row.folder.id"
      class="ft-row"
      :class="{ 'is-active': row.folder.id === activeId }"
      :style="{ paddingLeft: `${row.depth * 12 + 8}px` }"
      type="button"
      @click="emit('select', row.folder.id)"
    >
      <t-icon
        v-if="row.hasChildren"
        :name="collapsed.has(row.folder.id) ? 'chevron-right' : 'chevron-down'"
        size="11px"
        class="ft-chev"
        @click.stop="toggle(row.folder.id)"
      />
      <span v-else class="ft-chev-placeholder" />
      <t-icon name="folder" size="13px" />
      <span class="ft-name">{{ row.folder.name }}</span>
    </button>
    <div v-if="rows.length === 0" class="ft-empty">{{ $t('workbench.nav.newFolder') }}</div>
  </div>
</template>

<script setup lang="ts">
/**
 * 文件夹树（单层组件，不递归）。
 *
 * 后端最多 5 层，扁平化后按 depth 缩进即可；折叠状态只影响展示，
 * 不改变数据，因此切换视图不会丢失折叠记忆（组件内 ref 保留）。
 */
import { computed, ref } from 'vue'
import type { WorkbenchFolder } from '@/types/workbench'

const props = withDefaults(
  defineProps<{
    folders: WorkbenchFolder[]
    /** 当前选中文件夹；来自路由参数 */
    activeId?: string
  }>(),
  { activeId: '' },
)

const emit = defineEmits<{ select: [folderId: string] }>()

const collapsed = ref(new Set<string>())

interface FolderRow {
  folder: WorkbenchFolder
  depth: number
  hasChildren: boolean
}

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

function toggle(folderId: string) {
  const next = new Set(collapsed.value)
  if (next.has(folderId)) next.delete(folderId)
  else next.add(folderId)
  collapsed.value = next
}
</script>

<style lang="less" scoped>
.folder-tree {
  display: flex;
  flex-direction: column;
}

.ft-row {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding-right: var(--app-space-2);
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  text-align: left;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &.is-active {
    background: var(--td-brand-color-1);
    color: var(--td-brand-color);
  }
}

.ft-chev,
.ft-chev-placeholder {
  width: 11px;
  flex: none;
  color: var(--td-text-color-placeholder);
}

.ft-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ft-empty {
  padding: var(--app-space-2);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}
</style>
