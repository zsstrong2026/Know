<template>
  <!-- 添加已有文档到文件夹：搜索 + 多选 + 批量移动（参考 AFFiNE 的选择弹窗） -->
  <t-dialog
    v-model:visible="visible"
    :header="t('workbench.folderPicker.title', { folder: folderName })"
    width="600px"
    :footer="false"
    :close-on-overlay-click="!saving"
    :close-on-esc-keydown="!saving"
    @close="onClose"
  >
    <div class="adf-body">
      <div class="adf-toolbar">
        <t-input
          v-model="keyword"
          class="adf-search"
          :placeholder="t('workbench.folderPicker.searchPlaceholder')"
          clearable
        >
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <span class="adf-count">
          {{ t('workbench.folderPicker.available', { count: candidates.length }) }}
        </span>
      </div>

      <div class="adf-list-head">
        <t-checkbox
          :checked="allVisibleSelected"
          :indeterminate="someVisibleSelected"
          :disabled="candidates.length === 0 || loading"
          @change="toggleAll"
        >
          {{ t('workbench.folderPicker.selectAll') }}
        </t-checkbox>
        <span class="adf-selected">{{ t('workbench.folderPicker.selected', { count: selectedIds.size }) }}</span>
      </div>

      <div v-if="loading" class="adf-loading">
        <t-loading size="small" />
      </div>

      <div v-else-if="candidates.length === 0" class="adf-empty">
        <t-icon name="file" size="28px" />
        <p>{{ keyword ? t('workbench.folderPicker.emptySearch') : t('workbench.folderPicker.empty') }}</p>
        <span v-if="!keyword">{{ t('workbench.folderPicker.emptyHint') }}</span>
      </div>

      <div v-else class="adf-list">
        <div
          v-for="item in candidates"
          :key="item.id"
          class="adf-row"
          :class="{ 'is-checked': selectedIds.has(item.id) }"
          @click="onRowClick(item.id, $event)"
        >
          <t-checkbox
            :checked="selectedIds.has(item.id)"
            @click.stop
            @change="setChecked(item.id, $event)"
          />
          <t-icon name="image" size="16px" class="adf-icon" />
          <span class="adf-name" :title="item.title">{{ item.title }}</span>
          <span class="adf-folder" :title="folderLabel(item)">{{ folderLabel(item) }}</span>
        </div>
      </div>

      <div v-if="nextCursor && !loading" class="adf-more">
        <t-button variant="text" size="small" :loading="loadingMore" @click="loadMore">
          {{ t('workbench.folderPicker.loadMore') }}
        </t-button>
      </div>

      <div class="adf-footer">
        <span class="adf-selected">{{ t('workbench.folderPicker.selected', { count: selectedIds.size }) }}</span>
        <span class="spacer" />
        <t-button variant="outline" :disabled="saving" @click="onClose">
          {{ t('workbench.folderPicker.cancel') }}
        </t-button>
        <t-button
          theme="primary"
          :loading="saving"
          :disabled="selectedIds.size === 0 || loading"
          @click="onConfirm"
        >
          {{ t('workbench.folderPicker.confirm') }}
        </t-button>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 添加已有文档到文件夹。
 *
 * 候选列表按“更新时间”分页拉取，只包含文档/画布（数据库不进文件夹），
 * 并排除已经位于目标文件夹的条目；搜索走服务端 q，避免只过滤当前页导致找不到老文档。
 * 选择状态跨搜索保留，确认时逐条调用 moveItemToFolder。
 */
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { listWorkbenchItems } from '@/api/workbench'
import { useWorkbenchStore } from '@/stores/workbench'
import type { WorkbenchItem } from '@/types/workbench'

const props = defineProps<{
  modelValue: boolean
  folderId: string
  folderName: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  added: []
}>()

const { t } = useI18n()
const store = useWorkbenchStore()

const visible = computed({
  get: () => props.modelValue,
  set: (value: boolean) => emit('update:modelValue', value),
})

const candidates = ref<WorkbenchItem[]>([])
const selectedIds = ref(new Set<string>())
const keyword = ref('')
const nextCursor = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const saving = ref(false)

/** 请求序号：搜索与翻页同时发生时，只接受最后一次请求的结果。 */
let fetchToken = 0
let searchTimer: ReturnType<typeof setTimeout> | undefined

const folderNames = computed(
  () => new Map(store.folders.map((folder) => [folder.id, folder.name])),
)

const allVisibleSelected = computed(
  () => candidates.value.length > 0
    && candidates.value.every((item) => selectedIds.value.has(item.id)),
)
const someVisibleSelected = computed(
  () => !allVisibleSelected.value
    && candidates.value.some((item) => selectedIds.value.has(item.id)),
)

function folderLabel(item: WorkbenchItem): string {
  if (!item.folder_id) return t('workbench.folderPicker.unfiled')
  return folderNames.value.get(item.folder_id) ?? t('workbench.folderPicker.unfiled')
}

function setChecked(id: string, checked: boolean) {
  const next = new Set(selectedIds.value)
  if (checked) next.add(id)
  else next.delete(id)
  selectedIds.value = next
}

/** 整行可点选；复选框自身已有 change 处理，避免同一次点击翻转两次。 */
function onRowClick(id: string, event: MouseEvent) {
  if ((event.target as HTMLElement | null)?.closest('.t-checkbox')) return
  setChecked(id, !selectedIds.value.has(id))
}

function toggleAll(checked: boolean) {
  const next = new Set(selectedIds.value)
  for (const item of candidates.value) {
    if (checked) next.add(item.id)
    else next.delete(item.id)
  }
  selectedIds.value = next
}

/**
 * 拉取一页候选：排除数据库条目与已在该文件夹中的条目。
 *
 * 整页被过滤干净但仍有下一页时继续往后找（最多 3 轮），
 * 避免“列表里全是数据库”时误显示空态。返回下一页游标。
 */
async function fetchCandidatePage(
  cursor: string,
): Promise<{ items: WorkbenchItem[]; cursor: string }> {
  const collected: WorkbenchItem[] = []
  let next = cursor
  for (let round = 0; round < 4; round += 1) {
    const page = await listWorkbenchItems({
      limit: 100,
      cursor: next || undefined,
      q: keyword.value.trim() || undefined,
    })
    const rows = (page.items ?? []).filter(
      (item) => item.kind !== 'database' && item.folder_id !== props.folderId,
    )
    collected.push(...rows)
    next = page.next_cursor ?? ''
    if (!next || rows.length > 0) break
  }
  return { items: collected, cursor: next }
}

/** 拉取首屏或搜索结果；resetSelection=true 时清空已选。 */
async function loadCandidates(resetSelection: boolean) {
  const token = ++fetchToken
  loading.value = true
  nextCursor.value = ''
  try {
    const page = await fetchCandidatePage('')
    if (token !== fetchToken) return
    candidates.value = page.items
    nextCursor.value = page.cursor
    if (resetSelection) selectedIds.value = new Set()
  } catch {
    if (token === fetchToken) {
      candidates.value = []
      nextCursor.value = ''
      MessagePlugin.error(t('workbench.folderPicker.failed'))
    }
  } finally {
    if (token === fetchToken) loading.value = false
  }
}

async function loadMore() {
  if (!nextCursor.value || loading.value || loadingMore.value) return
  const token = fetchToken
  loadingMore.value = true
  try {
    const page = await fetchCandidatePage(nextCursor.value)
    if (token !== fetchToken) return
    const seen = new Set(candidates.value.map((item) => item.id))
    const appended = page.items.filter((item) => !seen.has(item.id))
    candidates.value = [...candidates.value, ...appended]
    nextCursor.value = page.cursor
  } catch {
    if (token === fetchToken) MessagePlugin.error(t('workbench.folderPicker.failed'))
  } finally {
    if (token === fetchToken) loadingMore.value = false
  }
}

function onClose() {
  if (saving.value) return
  fetchToken += 1
  visible.value = false
}

/** 确认：批量移动到目标文件夹；部分失败时给出汇总提示。 */
async function onConfirm() {
  if (selectedIds.value.size === 0 || saving.value) return
  saving.value = true
  try {
    const ids = [...selectedIds.value]
    const results = await Promise.allSettled(
      ids.map((id) => store.moveItemToFolder(id, props.folderId)),
    )
    const moved = results.filter((result) => result.status === 'fulfilled').length
    const failed = results.length - moved
    if (moved > 0) {
      MessagePlugin.success(t('workbench.folderPicker.success', { count: moved }))
    }
    if (failed > 0) {
      MessagePlugin.error(t('workbench.folderPicker.partialFailed'))
    }
    emit('added')
    visible.value = false
  } finally {
    saving.value = false
  }
}

// 打开时重置条件与选择；关闭时取消仍在等待的搜索
watch(
  () => props.modelValue,
  (open) => {
    if (!open) {
      clearTimeout(searchTimer)
      fetchToken += 1
      loading.value = false
      loadingMore.value = false
      saving.value = false
      return
    }
    keyword.value = ''
    selectedIds.value = new Set()
    void loadCandidates(true)
  },
)

// 目标文件夹变化时重载（正常流程不会在打开期间切换）
watch(
  () => props.folderId,
  () => {
    if (!visible.value) return
    selectedIds.value = new Set()
    void loadCandidates(true)
  },
)

// 搜索防抖：服务端 q，可继续翻页；输入相同关键词不重复请求
watch(keyword, () => {
  if (!visible.value) return
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    if (!visible.value) return
    void loadCandidates(false)
  }, 300)
})

onUnmounted(() => clearTimeout(searchTimer))
</script>

<style lang="less" scoped>
.adf-body {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
  min-height: 0;
}

.adf-toolbar {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
}

.adf-search {
  flex: 1;
  min-width: 0;
}

.adf-count {
  flex: none;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.adf-list-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 2px var(--app-space-2);
  border-bottom: 1px solid var(--td-component-stroke);
}

.adf-selected {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.adf-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 340px;
  margin: 0 -4px;
  padding: 2px 4px;
  overflow-y: auto;
  scrollbar-width: thin;
}

.adf-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 44px;
  padding: 6px 8px;
  border-radius: var(--app-radius-md);
  color: var(--td-text-color-primary);
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-checked {
    background: var(--td-brand-color-1);
  }
}

.adf-icon {
  flex: none;
  color: var(--td-text-color-secondary);
}

.adf-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: var(--app-text-md);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.adf-folder {
  flex: none;
  max-width: 140px;
  padding: 2px var(--app-space-2);
  overflow: hidden;
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-2xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.adf-loading {
  display: grid;
  place-items: center;
  padding: var(--app-space-10) 0;
}

.adf-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: var(--app-space-8) 0;
  color: var(--td-text-color-placeholder);

  p {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-md);
  }

  span {
    font-size: var(--app-text-xs);
  }
}

.adf-more {
  display: flex;
  justify-content: center;
}

.adf-footer {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding-top: var(--app-space-3);
  border-top: 1px solid var(--td-component-stroke);

  .spacer {
    flex: 1;
  }
}
</style>
