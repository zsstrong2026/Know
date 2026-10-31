<template>
  <!-- 章节文档列表：入口在工作知识库详情页，不挂二级导航（§9.9） -->
  <section class="docs-page">
    <header class="page-head">
      <div>
        <div class="page-title">{{ $t('workbench.docs.title') }}</div>
        <div class="page-sub">{{ $t('workbench.docs.subtitle') }}</div>
      </div>
      <span class="spacer" />
      <t-button v-if="rows.length > 0" variant="text" @click="toggleSelectMode">
        {{ selectMode ? $t('workbench.batchArchive.exitSelect') : $t('workbench.batchArchive.selectMode') }}
      </t-button>
      <t-button variant="outline" @click="goWorkKb">{{ $t('workbench.docs.backToKb') }}</t-button>
      <t-button theme="primary" @click="goGenerate">
        <template #icon><t-icon name="add" /></template>
        {{ $t('workbench.docs.generate') }}
      </t-button>
    </header>

    <BatchArchiveBar
      v-if="selectMode"
      class="batch-bar-host"
      :selected-count="selectedKeys.size"
      :actionable-count="actionableSelection.length"
      :skipped-count="selectedKeys.size - actionableSelection.length"
      @select-all-actionable="selectAllActionable"
      @archive="openBatchDialog"
      @cancel="toggleSelectMode"
    />

    <div v-if="loading" class="wb-loading"><t-loading :loading="true" size="small" /></div>

    <div v-else-if="rows.length === 0" class="wb-empty">
      <div class="empty-title">{{ $t('workbench.docs.emptyTitle') }}</div>
      <div class="empty-sub">{{ $t('workbench.docs.emptyHint') }}</div>
      <div class="empty-actions">
        <t-button theme="primary" size="small" @click="goGenerate">{{ $t('workbench.docs.generate') }}</t-button>
        <t-button variant="outline" size="small" @click="goWorkKb">{{ $t('workbench.docs.backToKb') }}</t-button>
      </div>
    </div>

    <t-table
      v-else
      row-key="id"
      :data="rows"
      :columns="columns"
      :hover="true"
      size="small"
      class="docs-table"
      :row-class-name="rowClassName"
      @row-click="onRowClick"
    >
      <template #theme="{ row }">
        <label v-if="selectMode" class="row-check" @click.stop>
          <input
            type="checkbox"
            :checked="selectedKeys.has(row.taskId)"
            :aria-label="row.theme"
            @change="toggleCandidate(row)"
          />
        </label>
        <span>{{ row.theme }}</span>
      </template>
      <template #status="{ row }">
        <span class="status-text" :class="`is-${row.status}`">{{ statusLabel(row.status) }}</span>
      </template>
      <template #archive="{ row }">
        <ArchiveBadge
          :status="row.archiveStatus"
          :archived-version="row.archivedVersion"
          readonly
        />
      </template>
      <template #updatedAt="{ row }">{{ formatTime(row.updatedAt) }}</template>
      <template #op="{ row }">
        <div class="row-actions">
          <t-button variant="text" size="small" @click.stop="openDoc(row)">{{ $t('workbench.docs.open') }}</t-button>
          <t-button
            variant="text"
            size="small"
            :disabled="row.archiveStatus === 'processing'"
            @click.stop="archive(row)"
          >
            {{ archiveActionLabel(row.archiveStatus) }}
          </t-button>
        </div>
      </template>
    </t-table>

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
 * 章节文档列表（P1：复用现有任务列表，不新增后端接口）。
 *
 * 归档状态来自工作台归档（source_type='doc'），不读 doc_generation_tasks.archive_status：
 * 后者只用于旧自动归档任务，不驱动徽标（§9.9）。
 * 状态按可见行分批读取，一次最多 20 条，避免整页 N+1。
 */
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import ArchiveBadge from './components/ArchiveBadge.vue'
import BatchArchiveBar from './components/BatchArchiveBar.vue'
import BatchArchiveDialog from './components/BatchArchiveDialog.vue'
import { listDocTasks, type DocGenerationTask } from '@/api/doc-assistant'
import { getArchiveState } from '@/api/workbench'
import { archiveBadge } from '@/workbench/archive-status'
import { batchOpFor, capBatchSelection, type BatchArchiveCandidate } from '@/workbench/batch-archive'
import { useWorkbenchStore } from '@/stores/workbench'
import type { ArchiveStatus } from '@/types/workbench'

interface DocRow {
  id: string
  taskId: string
  theme: string
  status: DocGenerationTask['status']
  updatedAt: string
  archiveStatus: ArchiveStatus
  archivedVersion: number
}

const router = useRouter()
const { t } = useI18n()
const store = useWorkbenchStore()

const loading = ref(true)
const rows = ref<DocRow[]>([])

// ---- 批量归档（§9.8.1） ----
const selectMode = ref(false)
/** 用 taskId 作为选择键：章节文档的归档源就是任务 ID。 */
const selectedKeys = ref(new Set<string>())
const batchDialogOpen = ref(false)
const batchSelection = ref<BatchArchiveCandidate[]>([])
const batchDropped = ref(0)

const actionableSelection = computed(() =>
  rows.value.filter((row) => selectedKeys.value.has(row.taskId) && batchOpFor(row.archiveStatus) !== 'skip'),
)

const columns = computed(() => [
  { colKey: 'theme', title: t('workbench.docs.columns.title'), ellipsis: true },
  { colKey: 'status', title: t('workbench.docs.columns.status'), width: 120 },
  { colKey: 'updatedAt', title: t('workbench.docs.columns.updatedAt'), width: 160 },
  { colKey: 'archive', title: t('workbench.docs.columns.archive'), width: 150 },
  { colKey: 'op', title: t('workbench.docs.columns.op'), width: 160, align: 'right' as const },
])

function statusLabel(status: DocGenerationTask['status']): string {
  return t(`workbench.docs.status.${status}`)
}

function archiveActionLabel(status: ArchiveStatus): string {
  const badge = archiveBadge(status, 0)
  return badge.action === 'none' ? t('workbench.archive.action.create') : t(`workbench.archive.action.${badge.action}`)
}

function formatTime(value: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

/** 只对进入列表的前 20 行读归档状态，避免一次性打满请求。 */
async function fillArchiveStatus(list: DocRow[]) {
  const visible = list.slice(0, 20)
  await Promise.all(
    visible.map(async (row) => {
      try {
        const state = await getArchiveState('doc', row.taskId)
        row.archiveStatus = state.status
        row.archivedVersion = state.archived_version ?? 0
      } catch {
        // 没有映射时接口返回 2400；按“未归档”展示即可。
        row.archiveStatus = 'none'
        row.archivedVersion = 0
      }
    }),
  )
}

async function load() {
  loading.value = true
  try {
    const resp = await listDocTasks()
    const tasks = resp.data ?? []
    rows.value = tasks.map((task) => ({
      id: task.id,
      taskId: task.id,
      theme: task.theme,
      status: task.status,
      updatedAt: task.finished_at ?? task.created_at,
      archiveStatus: 'none' as ArchiveStatus,
      archivedVersion: 0,
    }))
    await fillArchiveStatus(rows.value)
  } catch {
    MessagePlugin.error(t('workbench.docs.loadFailed'))
    rows.value = []
  } finally {
    loading.value = false
  }
}

/** 打开文档：先取任务下的最新版本，没有版本时留在列表提示。 */
async function openDoc(row: DocRow) {
  try {
    const { listDocDocuments } = await import('@/api/doc-assistant')
    const resp = await listDocDocuments(row.taskId)
    const latest = resp.data?.[0]
    if (!latest) {
      MessagePlugin.warning(t('workbench.docs.noVersion'))
      return
    }
    await router.push({
      name: 'docAssistantDocumentDetail',
      params: { id: latest.id },
      query: { from: 'workbenchDocs' },
    })
  } catch {
    MessagePlugin.error(t('workbench.docs.loadFailed'))
  }
}

/** 归档动作：章节文档由服务端渲染 Markdown，前端只传引用。 */
async function archive(row: DocRow) {
  const action = archiveBadge(row.archiveStatus, row.archivedVersion).action
  if (action === 'none' || action === 'progress') return
  try {
    const { createArchive, refreshArchive } = await import('@/api/workbench')
    const payload = { source_type: 'doc' as const, source_id: row.taskId }
    if (action === 'create' || action === 'recreate') {
      await createArchive(payload)
    } else {
      await refreshArchive(payload)
    }
    const state = await getArchiveState('doc', row.taskId)
    row.archiveStatus = state.status
    row.archivedVersion = state.archived_version ?? 0
    MessagePlugin.success(t('workbench.archive.action.create'))
  } catch {
    MessagePlugin.error(t('workbench.editor.saveError'))
  }
}

function goGenerate() {
  void router.push({ name: 'docAssistantGenerate' })
}

function goWorkKb() {
  if (store.workKbId) {
    void router.push(`/platform/knowledge-bases/${store.workKbId}`)
    return
  }
  void router.push({ name: 'workbenchRecent' })
}

// ---- 批量归档操作 ----

function toggleSelectMode() {
  selectMode.value = !selectMode.value
  if (!selectMode.value) resetSelection()
}

function resetSelection() {
  selectedKeys.value = new Set()
  selectMode.value = false
  batchSelection.value = []
  batchDropped.value = 0
}

function toggleCandidate(row: DocRow) {
  const next = new Set(selectedKeys.value)
  if (next.has(row.taskId)) next.delete(row.taskId)
  else next.add(row.taskId)
  selectedKeys.value = next
}

/** 只全选可归档的行；active/processing 会被计入「将跳过」。 */
function selectAllActionable() {
  const next = new Set(selectedKeys.value)
  for (const row of rows.value) {
    if (batchOpFor(row.archiveStatus) !== 'skip') next.add(row.taskId)
  }
  selectedKeys.value = next
}

/** 选择模式下点整行等于勾选；非选择模式下不拦截行点击（按钮自己处理）。 */
function onRowClick(payload: { row: DocRow }) {
  if (!selectMode.value) return
  toggleCandidate(payload.row)
}

function rowClassName(payload: { row: DocRow }): string {
  return selectMode.value && selectedKeys.value.has(payload.row.taskId) ? 'is-checked' : ''
}

function openBatchDialog() {
  const candidates: BatchArchiveCandidate[] = rows.value
    .filter((row) => selectedKeys.value.has(row.taskId))
    .map((row) => ({
      key: row.taskId,
      sourceType: 'doc' as const,
      sourceId: row.taskId,
      title: row.theme,
      status: row.archiveStatus,
      // 章节文档的 markdown 由服务端渲染，版本号只作基线占位。
      baseVersion: row.archivedVersion,
    }))
  const { kept, dropped } = capBatchSelection(candidates, 20)
  batchSelection.value = kept
  batchDropped.value = dropped
  batchDialogOpen.value = true
}

/** 批量结束后重拉一次归档状态（不逐行刷新）。 */
async function onBatchFinished() {
  resetSelection()
  await fillArchiveStatus(rows.value)
}

onMounted(() => {
  void load()
})
</script>

<style lang="less" scoped>
.docs-page {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: var(--app-space-5) var(--app-space-6);
  overflow: auto;
  background: var(--td-bg-color-container);
}

.page-head {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-2);
  padding-bottom: var(--app-space-4);
  border-bottom: 1px solid var(--td-component-stroke);
}

.page-title {
  font-size: var(--app-text-xl);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.page-sub {
  margin-top: var(--app-space-1);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.spacer {
  flex: 1;
}

.docs-table {
  margin-top: var(--app-space-4);
}

.status-text {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
}

.row-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--app-space-1);
}

.row-check {
  display: inline-flex;
  align-items: center;
  margin-right: var(--app-space-1);

  input {
    width: 14px;
    height: 14px;
    accent-color: var(--td-brand-color);
  }
}

.batch-bar-host {
  margin-top: var(--app-space-3);
}

.wb-loading,
.wb-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-2);
  padding: var(--app-space-8);
}

.empty-title {
  font-size: var(--app-text-lg);
  color: var(--td-text-color-primary);
}

.empty-sub {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.empty-actions {
  display: flex;
  gap: var(--app-space-2);
  margin-top: var(--app-space-2);
}
</style>
