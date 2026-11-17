<template>
  <!-- 模板管理：列出「我的模板」，支持编辑正文/重命名/删除（§9.16） -->
  <section class="tpl-page">
    <header class="page-head">
      <div>
        <div class="page-title">{{ t('workbench.templates.manage.title') }}</div>
        <div class="page-sub">{{ t('workbench.templates.manage.subtitle') }}</div>
      </div>
      <span class="spacer" />
      <div class="head-actions">
        <span class="toolbar-meta">{{ t('workbench.templates.manage.quota', { left: rows.length, total: TEMPLATE_QUOTA }) }}</span>
        <t-button
          size="small"
          theme="primary"
          :disabled="quotaLeft === 0"
          @click="openCreate"
        >
          <template #icon><t-icon name="add" /></template>
          {{ t('workbench.templates.manage.create') }}
        </t-button>
      </div>
    </header>

    <div class="list-toolbar">
      <t-input
        v-model="keyword"
        class="list-search"
        :placeholder="t('workbench.templates.manage.searchPlaceholder')"
        clearable
      >
        <template #prefix-icon><t-icon name="search" /></template>
      </t-input>
      <span class="spacer" />
      <span class="toolbar-meta">{{ t('workbench.templates.manage.total', { count: filteredRows.length }) }}</span>
      <t-button variant="text" size="small" :loading="loading" @click="load">
        {{ t('workbench.templates.manage.refresh') }}
      </t-button>
    </div>

    <div v-if="loading && rows.length === 0" class="tpl-loading">
      <t-loading :loading="true" size="small" />
    </div>

    <div v-else-if="loadError" class="tpl-empty">
      <div class="empty-title">{{ t('workbench.templates.manage.loadFailed') }}</div>
      <t-button size="small" variant="outline" @click="load">
        {{ t('workbench.templates.retry') }}
      </t-button>
    </div>

    <div v-else-if="rows.length === 0" class="tpl-empty">
      <div class="empty-title">{{ t('workbench.templates.mineEmpty') }}</div>
      <div class="empty-sub">{{ t('workbench.templates.mineEmptyHint') }}</div>
      <t-button size="small" theme="primary" :disabled="quotaLeft === 0" @click="openCreate">
        <template #icon><t-icon name="add" /></template>
        {{ t('workbench.templates.manage.create') }}
      </t-button>
    </div>

    <div v-else-if="filteredRows.length === 0" class="tpl-empty">
      <div class="empty-title">{{ t('workbench.templates.noResult') }}</div>
      <t-button size="small" variant="text" @click="keyword = ''">
        {{ t('workbench.templates.clearSearch') }}
      </t-button>
    </div>

    <t-table
      v-else
      row-key="id"
      :data="filteredRows"
      :columns="columns"
      :hover="true"
      size="small"
      class="tpl-table"
    >
      <template #name="{ row }">
        <span class="cell-name" :title="row.name">{{ row.name }}</span>
      </template>
      <template #description="{ row }">
        <span class="cell-desc">{{ row.description || t('workbench.templates.manage.noDescription') }}</span>
      </template>
      <template #size="{ row }">{{ formatFileSize(row.size_bytes) || '—' }}</template>
      <template #updatedAt="{ row }">{{ formatTime(row.updated_at) }}</template>
      <template #op="{ row }">
        <div class="row-actions">
          <t-button variant="text" size="small" @click="openContent(row)">
            {{ t('workbench.templates.manage.editContent') }}
          </t-button>
          <t-button variant="text" size="small" @click="openEdit(row)">
            {{ t('workbench.templates.manage.edit') }}
          </t-button>
          <t-button variant="text" size="small" theme="danger" @click="confirmDelete(row)">
            {{ t('workbench.templates.manage.delete') }}
          </t-button>
        </div>
      </template>
    </t-table>

    <!-- 编辑元数据：只改名称与描述；正文在独立的模板编辑器里改 -->
    <t-dialog
      v-model:visible="editOpen"
      :header="t('workbench.templates.manage.editTitle')"
      :confirm-btn="t('workbench.templates.manage.save')"
      :cancel-btn="t('workbench.templates.manage.cancel')"
      :confirm-loading="saving"
      width="480px"
      @confirm="submitEdit"
      @cancel="editOpen = false"
      @close="editOpen = false"
    >
      <div class="edit-form">
        <label class="edit-field">
          <span class="edit-label">{{ t('workbench.templates.manage.name') }}</span>
          <t-input
            v-model="editName"
            :maxlength="NAME_MAX"
            :placeholder="t('workbench.templates.save.namePlaceholder')"
            size="small"
          />
        </label>
        <label class="edit-field">
          <span class="edit-label">{{ t('workbench.templates.save.description') }}</span>
          <t-textarea
            v-model="editDescription"
            :maxlength="DESC_MAX"
            :autosize="{ minRows: 2, maxRows: 4 }"
            :placeholder="t('workbench.templates.save.descriptionPlaceholder')"
          />
        </label>
      </div>
    </t-dialog>

    <!-- 新建模板：先收名称/描述，再进空白模板编辑器；首次保存才真正落库 -->
    <t-dialog
      v-model:visible="createOpen"
      :header="t('workbench.templates.manage.createTitle')"
      :confirm-btn="t('workbench.templates.manage.createConfirm')"
      :cancel-btn="t('workbench.templates.manage.cancel')"
      width="480px"
      @confirm="submitCreate"
      @cancel="createOpen = false"
      @close="createOpen = false"
    >
      <div class="edit-form">
        <label class="edit-field">
          <span class="edit-label">{{ t('workbench.templates.manage.name') }}</span>
          <t-input
            v-model="createName"
            :maxlength="NAME_MAX"
            :placeholder="t('workbench.templates.save.namePlaceholder')"
            size="small"
          />
        </label>
        <label class="edit-field">
          <span class="edit-label">{{ t('workbench.templates.save.description') }}</span>
          <t-textarea
            v-model="createDescription"
            :maxlength="DESC_MAX"
            :autosize="{ minRows: 2, maxRows: 4 }"
            :placeholder="t('workbench.templates.save.descriptionPlaceholder')"
          />
        </label>
        <p class="edit-hint">{{ t('workbench.templates.manage.createHint') }}</p>
      </div>
    </t-dialog>

    <!-- 编辑正文：全屏浮层，内部挂一份 BlockSuite，保存即整体覆盖模板快照 -->
    <TemplateContentEditor
      v-if="contentTarget"
      :template-id="contentTarget.id"
      :template-name="contentTarget.name"
      @close="contentTarget = null"
      @saved="onContentSaved"
    />

    <!-- 新建模板正文：同一浮层，mode=create 时从空白页开始 -->
    <TemplateContentEditor
      v-if="createTarget"
      mode="create"
      :template-name="createTarget.name"
      :template-description="createTarget.description"
      @close="createTarget = null"
      @saved="onCreated"
    />
  </section>
</template>

<script setup lang="ts">
/**
 * 模板管理页。
 *
 * 入口与两类编辑：
 *   - 「新建模板」直接在这里起步：收名称/描述 → 打开空白模板编辑器 → 首次保存才落库；
 *     不需要先建一份文档再去 ⋯ 里另存为模板；
 *   - 「编辑」只改名称/描述，走 PATCH /templates/:id；正文不在这个弹窗里改；
 *   - 「编辑内容」打开全屏模板编辑器，保存时整体覆盖模板快照；
 *     已用该模板创建的文档不受影响（copy-on-use）。
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import TemplateContentEditor from './components/TemplateContentEditor.vue'
import {
  deleteWorkbenchTemplate,
  listWorkbenchTemplates,
  updateWorkbenchTemplate,
} from '@/api/workbench'
import { TEMPLATE_QUOTA, templateQuotaLeft } from '@/workbench/templates/apply'
import { formatFileSize } from '@/utils/files'
import type { WorkbenchTemplateSummary } from '@/types/workbench'

const NAME_MAX = 60
const DESC_MAX = 200

const { t } = useI18n()

const rows = ref<WorkbenchTemplateSummary[]>([])
const loading = ref(false)
const loadError = ref(false)
const keyword = ref('')

const editOpen = ref(false)
const saving = ref(false)
const editingId = ref('')
const editName = ref('')
const editDescription = ref('')
/** 正在编辑正文的模板；非空时渲染全屏模板编辑器 */
const contentTarget = ref<WorkbenchTemplateSummary | null>(null)

const createOpen = ref(false)
const createName = ref('')
const createDescription = ref('')
/** 新建模板的「草稿」：名称/描述已确认，但正文还没保存落库 */
const createTarget = ref<{ name: string; description: string } | null>(null)

/** 配额已满时禁用新建，避免点进编辑器后才发现存不下。 */
const quotaLeft = computed(() => templateQuotaLeft(rows.value.length))

const columns = computed(() => [
  { colKey: 'name', title: t('workbench.templates.manage.columns.name'), ellipsis: true },
  { colKey: 'description', title: t('workbench.templates.manage.columns.description'), ellipsis: true },
  { colKey: 'size', title: t('workbench.templates.manage.columns.size'), width: 100 },
  { colKey: 'updatedAt', title: t('workbench.templates.manage.columns.updatedAt'), width: 160 },
  { colKey: 'op', title: t('workbench.templates.manage.columns.op'), width: 220, align: 'right' as const },
])

/** 搜索只按名称与描述过滤，与服务端列表口径一致（不分页）。 */
const filteredRows = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  if (!q) return rows.value
  return rows.value.filter((row) =>
    `${row.name} ${row.description ?? ''}`.toLowerCase().includes(q),
  )
})

function formatTime(value: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

async function load() {
  loading.value = true
  loadError.value = false
  try {
    rows.value = await listWorkbenchTemplates()
  } catch {
    loadError.value = true
    rows.value = []
  } finally {
    loading.value = false
  }
}

function openEdit(row: WorkbenchTemplateSummary) {
  editingId.value = row.id
  editName.value = row.name
  editDescription.value = row.description ?? ''
  editOpen.value = true
}

/** 打开新建弹窗：每次重置输入，不复用上一次的值。 */
function openCreate() {
  if (quotaLeft.value === 0) return
  createName.value = ''
  createDescription.value = ''
  createOpen.value = true
}

/** 确认名称/描述后进入空白模板编辑器；此时还没有落库，取消不会留下空模板。 */
function submitCreate() {
  const name = createName.value.trim()
  if (!name) {
    MessagePlugin.warning(t('workbench.templates.manage.nameRequired'))
    return
  }
  createTarget.value = { name, description: createDescription.value.trim() }
  createOpen.value = false
}

/** 新建保存成功：直接把新模板插到列表最前，不必重拉整个列表。 */
function onCreated(created: WorkbenchTemplateSummary) {
  rows.value = [created, ...rows.value]
  createTarget.value = null
}

/** 打开模板正文编辑器；关闭/保存后由子组件回传。 */
function openContent(row: WorkbenchTemplateSummary) {
  contentTarget.value = row
}

/** 保存正文成功后只更新列表里的元数据（名称/大小/更新时间），不重拉整个列表。 */
function onContentSaved(updated: WorkbenchTemplateSummary) {
  rows.value = rows.value.map((row) => (row.id === updated.id ? updated : row))
}

async function submitEdit() {
  const name = editName.value.trim()
  if (!name) {
    MessagePlugin.warning(t('workbench.templates.manage.nameRequired'))
    return
  }
  saving.value = true
  try {
    const updated = await updateWorkbenchTemplate(editingId.value, {
      name,
      description: editDescription.value.trim(),
    })
    rows.value = rows.value.map((row) => (row.id === updated.id ? updated : row))
    editOpen.value = false
    MessagePlugin.success(t('workbench.templates.manage.saved'))
  } catch {
    MessagePlugin.error(t('workbench.templates.manage.saveFailed'))
  } finally {
    saving.value = false
  }
}

/** 删除是硬删：模板不进回收站，已用该模板创建的文档不受影响。 */
function confirmDelete(row: WorkbenchTemplateSummary) {
  const dialog = DialogPlugin.confirm({
    header: t('workbench.templates.manage.deleteTitle'),
    body: t('workbench.templates.manage.deleteBody', { name: row.name }),
    theme: 'warning',
    confirmBtn: t('workbench.templates.manage.deleteConfirm'),
    cancelBtn: t('workbench.templates.manage.cancel'),
    onConfirm: async () => {
      try {
        await deleteWorkbenchTemplate(row.id)
        rows.value = rows.value.filter((item) => item.id !== row.id)
        MessagePlugin.success(t('workbench.templates.manage.deleted'))
        dialog.hide()
      } catch {
        MessagePlugin.error(t('workbench.templates.manage.deleteFailed'))
      }
    },
  })
}

onMounted(() => {
  void load()
})
</script>

<style lang="less" scoped>
.tpl-page {
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
  align-items: center;
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

.head-actions {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
}

.list-toolbar {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  margin: var(--app-space-4) 0;
}

.list-search {
  width: 260px;
}

.toolbar-meta {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.tpl-table {
  margin-top: var(--app-space-1);
}

.cell-name {
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.cell-desc {
  color: var(--td-text-color-secondary);
}

.row-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--app-space-1);
}

.tpl-loading,
.tpl-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-2);
  padding: var(--app-space-8);
  text-align: center;
}

.empty-title {
  font-size: var(--app-text-lg);
  color: var(--td-text-color-primary);
}

.empty-sub {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.edit-form {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
}

.edit-field {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-1);
}

.edit-label {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
}

.edit-hint {
  margin: 0;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}
</style>
