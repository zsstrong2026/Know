<template>
  <!-- 条目编辑页：顶栏（标题 / 保存状态 / 归档）+ BlockSuite 宿主 -->
  <section class="item-page">
    <header class="item-head">
      <t-button variant="text" shape="square" :aria-label="$t('workbench.editor.back')" @click="goBack">
        <t-icon name="chevron-left" size="18px" />
      </t-button>
      <!-- 「添加图标」只在无图标且偏好开启时出现；点击不写数据，只是入口（§9.14.2） -->
      <button
        v-if="editorPrefs.showAddIcon && !item?.icon"
        class="add-icon-btn"
        type="button"
        :aria-label="$t('workbench.editor.addIcon')"
        @click="openIconPicker"
      >
        <t-icon name="image" size="14px" />
      </button>
      <span v-else-if="item?.icon" class="item-icon">{{ item.icon }}</span>
      <input
        class="title-input"
        :value="title"
        :placeholder="$t('workbench.editor.untitled')"
        :aria-label="$t('workbench.item.rename')"
        @change="onTitleChange"
        @blur="onTitleChange"
      />
      <span class="save-state" :class="`is-${saveState}`">{{ saveLabel }}</span>
      <span class="spacer" />
      <!-- 空文档才可套模板；已编辑/已保存后消失，避免覆盖用户内容（§9.16.2） -->
      <t-button v-if="canUseTemplate" variant="text" size="small" @click="openTemplatePicker">
        {{ $t('workbench.templates.useTemplate') }}
      </t-button>
      <t-dropdown :options="moreOptions" trigger="click" placement="bottom-right" @click="onMore">
        <t-button variant="text" shape="square" :aria-label="$t('workbench.item.more')">
          <t-icon name="ellipsis" size="16px" />
        </t-button>
      </t-dropdown>
      <t-button variant="text" size="small" @click="openVersions">
        {{ $t('workbench.version.title') }}
      </t-button>
      <ArchiveBadge
        :status="archiveStatus"
        :archived-version="archivedVersion"
        @action="onArchiveAction"
      />
    </header>

    <div v-if="loadError" class="item-error">
      <t-icon name="error-circle" size="20px" />
      <span>{{ loadError === 'notFound' ? $t('workbench.editor.notFound') : $t('workbench.editor.loadFailed') }}</span>
      <t-button size="small" variant="outline" @click="goBack">
        {{ $t('workbench.editor.back') }}
      </t-button>
    </div>

    <!-- 文档信息：更新时间 / 字数 / 可见性；数据全部来自已加载内容，不额外请求 -->
    <div v-if="!loadError && editorPrefs.showDocInfo" class="doc-info">
      <span>{{ $t('workbench.editor.docInfo.updated', { time: updatedLabel }) }}</span>
      <span class="doc-dot">·</span>
      <span>{{ $t('workbench.editor.docInfo.words', { count: wordCount }) }}</span>
      <span class="doc-dot">·</span>
      <span class="doc-visibility">
        <t-icon name="lock-on" size="11px" />
        {{ $t('workbench.editor.docInfo.private') }}
      </span>
    </div>

    <div v-if="!loadError" class="editor-host" :class="{ 'is-ready': ready, 'is-ai-open': aiPanelOpen }">
      <!-- BlockSuite 挂载点：由 editor-session 注入宿主元素 -->
      <div ref="hostRef" class="editor-mount" />
      <div v-if="!ready && !pickerOpen" class="editor-loading">{{ $t('workbench.editor.loading') }}</div>
      <!-- 模板选择器：打开期间不挂载编辑器（§9.16.2 规则 1） -->
      <TemplatePicker v-if="pickerOpen" @apply="onTemplateApply" @blank="onTemplateBlank" />
      <!-- Page AI：工具条/菜单/预览/侧栏；只读与回收站不出现（§9.15.1） -->
      <PageAILayer
        v-if="ready && canUseAI && !pickerOpen"
        ref="aiLayer"
        :adapter="aiAdapter"
        :item-id="itemId"
        :editable="true"
        :agent-id="aiPrefs.defaultAgentId"
        :confirm-insert="aiPrefs.confirmInsert"
        @panel-change="(open: boolean) => (aiPanelOpen = open)"
      />
    </div>

    <div v-if="aiDisabled && ready" class="ai-off-hint">
      <span>{{ $t('workbench.pageAi.state.disabled') }}</span>
      <t-button size="small" variant="outline" @click="goAISettings">
        {{ $t('workbench.pageAi.state.goSettings') }}
      </t-button>
    </div>

    <VersionHistoryDialog
      ref="versionDialog"
      :item-id="itemId"
      :current-version="baseVersion"
      @restore="restoreVersion"
    />

    <SaveTemplateDialog
      v-model:visible="saveDialogOpen"
      :existing-count="templateCount"
      :default-name="title || $t('workbench.editor.untitled')"
      :saving="savingTemplate"
      @submit="onSaveTemplate"
    />
  </section>
</template>

<script setup lang="ts">
/**
 * 条目编辑页（唯一加载 BlockSuite 的路由）。
 *
 * 关键约束：
 *   - Y.Doc 只由 useEditorSession 读写，store 不持有编辑器实例；
 *   - 保存走 save-queue，冲突时弹三选项，绝不静默覆盖；
 *   - 归档需要 Markdown，本页从投影结果生成并提交。
 */
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import ArchiveBadge from './components/ArchiveBadge.vue'
import VersionHistoryDialog from './components/VersionHistoryDialog.vue'
import PageAILayer from './components/PageAILayer.vue'
import TemplatePicker from './components/TemplatePicker.vue'
import SaveTemplateDialog from './components/SaveTemplateDialog.vue'
import {
  createArchive,
  createWorkbenchTemplate,
  getArchiveState,
  getWorkbenchItem,
  listWorkbenchTemplates,
  refreshArchive,
  restoreWorkbenchVersion,
  updateWorkbenchItem,
  workbenchErrorCode,
  WORKBENCH_ERROR,
} from '@/api/workbench'
import { useEditorSession } from '@/workbench/editor/use-editor-session'
import { createPageAIAdapter, type PageAIAdapterOptions } from '@/workbench/ai/editor-adapter'
import { useWorkbenchPrefs } from '@/workbench/use-workbench-prefs'
import { useWorkbenchStore } from '@/stores/workbench'
import { type ArchiveBadgeAction } from '@/workbench/archive-status'
import { shouldAdoptTemplateTitle, type PreparedTemplateSnapshot } from '@/workbench/templates/apply'
import type { TemplateCardItem } from '@/workbench/templates/picker'
import type { ArchiveStatus, WorkbenchItem } from '@/types/workbench'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()

const itemId = computed(() => String(route.params.itemId ?? ''))
const item = shallowRef<WorkbenchItem | null>(null)
const title = ref('')
const loadError = ref<'' | 'notFound' | 'failed'>('')
const archiveStatus = ref<ArchiveStatus>('none')
const archivedVersion = ref(0)

const hostRef = ref<HTMLElement | null>(null)
const versionDialog = ref<{ open: () => void; setRestoring: (version: number) => void } | null>(null)

// ---- 模板（§9.16） ----
/**
 * 发布门禁状态来自 bootstrap（§9.1）：bootstrap 未返回时模板视为关闭，
 * 避免开关关闭的部署先闪一下入口再消失。
 * 不解构 store：Pinia 解构会 unwrap computed，之后不能再取 .value。
 */
const workbenchStore = useWorkbenchStore()
const templateEnabled = computed(() => workbenchStore.features.templates)
const aiFeatureEnabled = computed(() => workbenchStore.features.ai)

/** 选择器是否打开；打开期间不挂载编辑器 */
const pickerOpen = ref(false)
/** AI 侧栏是否展开：由 PageAILayer 上报，用于收窄编辑区而不是覆盖它 */
const aiPanelOpen = ref(false)
/** 服务端快照版本；>0 表示文档已有内容，模板入口全部关闭 */
const snapshotVersion = ref(0)
const saveDialogOpen = ref(false)
const savingTemplate = ref(false)
const templateCount = ref(0)

/** 只有空文档才允许套模板；已保存过内容的不显示任何模板入口。 */
const canUseTemplate = computed(
  () =>
    templateEnabled.value &&
    !loadError.value &&
    snapshotVersion.value === 0 &&
    !pickerOpen.value,
)

/** v1 只做 Page 模板：无限画布形态下不提供「另存为模板」（§9.16.3）。 */
const canSaveAsTemplate = computed(
  () => templateEnabled.value && (item.value?.current_mode ?? 'page') === 'page',
)

const moreOptions = computed(() => [
  {
    content: t('workbench.templates.save.entry'),
    value: 'save-template',
    disabled: snapshotVersion.value === 0 || !canSaveAsTemplate.value,
  },
])

const session = useEditorSession({
  itemId,
  host: hostRef,
  item,
  onConflict: () => openConflictDialog(),
})
const { saveState, baseVersion } = session
// setup 里直接解构 ref 后，模板中的 ready 需要单独暴露（模板不会自动 unwrap 嵌套对象字段）。
const ready = session.ready

// ---- Page AI 偏好与适配器（§9.14.5） ----
// 偏好来自共享单例：设置页改完立即生效，不用刷新（§9.14.6）。
const { aiPrefs, editorPrefs } = useWorkbenchPrefs()

// ---- 文档信息（§9.14.2）：数据来自已加载正文，不额外请求 ----
const updatedLabel = computed(() => {
  const raw = item.value?.updated_at ?? ''
  if (!raw) return ''
  const at = new Date(raw)
  if (Number.isNaN(at.getTime())) return ''
  return `${at.getMonth() + 1}月${at.getDate()}日 ${String(at.getHours()).padStart(2, '0')}:${String(
    at.getMinutes(),
  ).padStart(2, '0')}`
})

/** 字数：按非空白字符计，与归档正文同源（投影后的 Markdown）。 */
const wordCount = computed(() => {
  // 投影整篇文档不便宜，只在内容可能变化时重算：就绪与保存状态变化即触发。
  void ready.value
  void saveState.value
  return session.markdownCount()
})
/**
 * AI 关闭时只隐藏入口，不影响编辑、归档与保存（§9.14.6）。
 * 同时受发布门禁约束：`workbench.ai.enabled` 关闭时整个 Page AI 入口不出现（§9.1）。
 */
const aiDisabled = computed(() => !aiPrefs.value.enabled || !aiFeatureEnabled.value)
const canUseAI = computed(() => aiPrefs.value.enabled && aiFeatureEnabled.value)

const aiAdapterOptions: PageAIAdapterOptions = {
  handle: () => session.editor.value,
  itemId: () => itemId.value,
  host: () => hostRef.value,
}
const aiAdapter = () => (aiDisabled.value ? null : createPageAIAdapter(aiAdapterOptions))

/** 「去设置」跳到工作台设置的工作台 AI 标签页（§9.14.1）。 */
function goAISettings() {
  void router.push({ name: 'workbenchSettings', query: { tab: 'ai' } })
}

const saveLabel = computed(() => {
  switch (saveState.value) {
    case 'pending':
      return t('workbench.editor.savePending')
    case 'saving':
      return t('workbench.editor.saveSaving')
    case 'error':
    case 'conflict':
      return t('workbench.editor.saveError')
    default:
      return t('workbench.editor.saveSaved')
  }
})

/** 冲突三选项：用我的版本覆盖 / 重载服务端版本 / 稍后手动处理。 */
function openConflictDialog() {
  const dialog = DialogPlugin({
    header: t('workbench.editor.saveError'),
    body: t('workbench.editor.saveError'),
    confirmBtn: t('workbench.editor.saveSaved'),
    cancelBtn: t('workbench.editor.loadFailed'),
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

async function onTitleChange(event: Event) {
  const next = (event.target as HTMLInputElement).value.trim()
  if (!next || next === item.value?.title) return
  try {
    const updated = await updateWorkbenchItem(itemId.value, { title: next })
    item.value = updated
    title.value = updated.title
  } catch {
    MessagePlugin.error(t('workbench.item.renameFailed'))
  }
}

/**
 * 「添加图标」入口。
 *
 * P1 用浏览器内置输入收一个 emoji，写进条目的 icon 字段；
 * 偏好只决定入口显隐，本身不写任何图标数据（§9.14.2）。
 */
async function openIconPicker() {
  const raw = window.prompt(t('workbench.editor.addIconHint'), item.value?.icon ?? '')
  if (raw === null) return
  const icon = raw.trim()
  if (icon === (item.value?.icon ?? '')) return
  try {
    const updated = await updateWorkbenchItem(itemId.value, { icon })
    item.value = updated
  } catch {
    MessagePlugin.error(t('workbench.item.renameFailed'))
  }
}

// ---- 模板流程（§9.16.2） ----

function openTemplatePicker() {
  if (!canUseTemplate.value) return
  pickerOpen.value = true
}

/**
 * 套用模板。
 *
 * 顺序固定：替换资产块 → 用快照建 session（base_version=0）→ 改标题 → 移除 ?template。
 * 若目标文档在这期间被别人写入了内容，mountFromTemplate 会抛错：重载并提示，
 * **不 force**，绝不覆盖用户内容。
 */
async function onTemplateApply(snapshot: PreparedTemplateSnapshot, card: TemplateCardItem) {
  // 动态 import：静态 import blocksuite 会让整个 BlockSuite 模块图在组件解析阶段
  // 就必须加载完，把「打开文档」的首屏时间拖到十几秒。只有真正套用模板时才需要它。
  const { replaceAssetBlocks } = await import('@/workbench/editor/blocksuite')
  const { bytes } = replaceAssetBlocks(snapshot.bytes)
  try {
    await session.mountFromTemplate({
      snapshot: bytes,
      editorSchemaVersion: snapshot.schemaVersion,
    })
  } catch (error) {
    pickerOpen.value = false
    if (String(error).includes('template-target-not-empty')) {
      MessagePlugin.warning(t('workbench.templates.targetNotEmpty'))
      await session.reload()
      return
    }
    MessagePlugin.error(t('workbench.templates.applyFailed'))
    return
  }

  pickerOpen.value = false
  snapshotVersion.value = Math.max(snapshotVersion.value, 1)
  stripTemplateQuery()

  // 标题只在占位值时采用模板名；用户已改过名就保留。
  // 「未命名画布」是普通新建画布的后端默认标题，同样视为占位。
  const placeholders = [
    t('workbench.editor.untitled'),
    '未命名文档',
    '未命名',
    '未命名画布',
    '未命名数据库',
  ]
  const templateTitle = card.nameKey ? t(card.nameKey) : card.name
  if (shouldAdoptTemplateTitle(title.value, placeholders) && snapshot.name) {
    await applyTemplateTitle(card.nameKey ? templateTitle : snapshot.name)
  }
  MessagePlugin.success(t('workbench.templates.applied', { name: templateTitle }))
}

/** 标题写入失败不回滚内容，只给非阻断提示（§9.16.5）。 */
async function applyTemplateTitle(nextTitle: string) {
  try {
    const updated = await updateWorkbenchItem(itemId.value, { title: nextTitle })
    item.value = updated
    title.value = updated.title
  } catch {
    MessagePlugin.warning(t('workbench.item.renameFailed'))
  }
}

/** 关闭选择器 = 从空白文档开始；延迟首次保存，保留「使用模板」（§9.16.2）。 */
async function onTemplateBlank() {
  pickerOpen.value = false
  stripTemplateQuery()
  try {
    await session.mount({ deferBlankSave: true })
  } catch {
    loadError.value = 'failed'
  }
}

/** 移除 ?template=1，之后刷新按普通文档打开。 */
function stripTemplateQuery() {
  if (!('template' in route.query)) return
  const query = { ...route.query }
  delete query.template
  void router.replace({ query })
}

/** 打开「另存为模板」：先 flush 成功才允许保存，避免存下半截内容。 */
async function openSaveTemplateDialog() {
  if (!canSaveAsTemplate.value || snapshotVersion.value === 0) return
  try {
    await session.flush()
  } catch {
    MessagePlugin.error(t('workbench.templates.save.flushFailed'))
    return
  }
  try {
    const list = await listWorkbenchTemplates()
    templateCount.value = list.length
  } catch {
    templateCount.value = 0
  }
  saveDialogOpen.value = true
}

async function onSaveTemplate(payload: { name: string; description: string }) {
  savingTemplate.value = true
  try {
    await createWorkbenchTemplate({
      source_item_id: itemId.value,
      name: payload.name,
      description: payload.description,
    })
    saveDialogOpen.value = false
    templateCount.value += 1
    MessagePlugin.success(t('workbench.templates.save.saved'))
  } catch {
    MessagePlugin.error(t('workbench.templates.save.failed'))
  } finally {
    savingTemplate.value = false
  }
}

function onMore(data: { value?: string | number }) {
  if (String(data.value ?? '') === 'save-template') void openSaveTemplateDialog()
}

async function refreshArchiveState() {
  try {
    const state = await getArchiveState('item', itemId.value)
    archiveStatus.value = state.status
    archivedVersion.value = state.archived_version ?? 0
  } catch (error) {
    // 2400 表示条目不存在/越权，不是“归档状态未知”：直接按未归档展示，
    // 不再提示，避免新建条目首次打开就弹一个“画布加载失败”。
    if (workbenchErrorCode(error) === WORKBENCH_ERROR.notFound) {
      archiveStatus.value = 'none'
      archivedVersion.value = 0
    }
    // 其他错误不影响编辑，保持上一次的徽标。
  }
}

/**
 * 归档动作。
 *
 * 正文由 projection 从当前 Y.Doc 投影成 Markdown 后提交；
 * 不使用服务端已存的 text_content，避免把上一次自动保存的旧正文归档进去。
 */
async function onArchiveAction(action: ArchiveBadgeAction) {
  if (action === 'none' || action === 'progress') return
  const markdown = session.markdown()
  if (!markdown.trim()) {
    MessagePlugin.warning(t('workbench.archive.status.none'))
    return
  }
  try {
    const payload = {
      source_type: 'item' as const,
      source_id: itemId.value,
      base_version: baseVersion.value,
      markdown,
    }
    if (action === 'create' || action === 'recreate') {
      await createArchive(payload)
    } else {
      await refreshArchive(payload)
    }
    await refreshArchiveState()
    MessagePlugin.success(t('workbench.archive.action.create'))
  } catch (error) {
    if (workbenchErrorCode(error) === WORKBENCH_ERROR.archiveStale) {
      MessagePlugin.warning(t('workbench.archive.status.stale'))
      return
    }
    MessagePlugin.error(t('workbench.editor.saveError'))
  }
}

function goBack() {
  void router.push({ name: 'workbenchRecent' })
}

function openVersions() {
  versionDialog.value?.open()
}

/**
 * 恢复到历史版本。
 *
 * 服务端会先把当前状态写成一条检查点，再以新版本号写回历史快照，
 * 因此“恢复”本身也可再恢复回来；这里只需要重建编辑器并推进 base_version。
 */
async function restoreVersion(version: number) {
  versionDialog.value?.setRestoring(version)
  try {
    const result = await restoreWorkbenchVersion(itemId.value, version, baseVersion.value)
    // 丢弃内存里的旧快照：重建编辑器一定重新拉 content，保证与刚写入的版本一致。
    await session.reload()
    if (item.value) item.value = { ...item.value, snapshot_version: result.snapshot_version }
    MessagePlugin.success(t('workbench.version.restored'))
  } catch (error) {
    if (workbenchErrorCode(error) === WORKBENCH_ERROR.versionConflict) {
      MessagePlugin.warning(t('workbench.version.conflict'))
      return
    }
    MessagePlugin.error(t('workbench.version.restoreFailed'))
  }
}

onMounted(async () => {
  // 归档状态与条目元数据互不依赖：并行发起，避免把它夹在两次 await 之间
  // 白等一个往返。refreshArchiveState 自己吞掉错误，也不会抛，这里直接丢弃句柄。
  void refreshArchiveState()
  try {
    const loaded = await getWorkbenchItem(itemId.value)
    item.value = loaded
    title.value = loaded.title
    snapshotVersion.value = loaded.snapshot_version
  } catch (error) {
    loadError.value = workbenchErrorCode(error) === WORKBENCH_ERROR.notFound ? 'notFound' : 'failed'
    return
  }

  // 模板入口：只有 ?template=1 且文档仍为空时才显示选择器（§9.16.2）。
  // 开关关闭时直接当作普通文档打开，不查也不弹。
  if (templateEnabled.value && route.query.template === '1') {
    if (snapshotVersion.value > 0) {
      // 另一个标签页已经写入内容：关闭入口，按普通文档打开。
      stripTemplateQuery()
      MessagePlugin.info(t('workbench.templates.entryClosed'))
    } else {
      pickerOpen.value = true
      return
    }
  }

  try {
    await session.mount()
  } catch {
    loadError.value = 'failed'
  }
})

onBeforeUnmount(() => {
  void session.flush()
  session.destroy()
})
</script>

<style lang="less" scoped>
.item-page {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
}

.item-head {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-4);
  border-bottom: 1px solid var(--td-component-stroke);
}

.title-input {
  min-width: 0;
  width: 320px;
  border: 1px solid transparent;
  border-radius: var(--app-radius-xs);
  padding: var(--app-space-1) var(--app-space-2);
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
  background: transparent;

  &:hover {
    border-color: var(--td-component-stroke);
  }

  &:focus {
    outline: none;
    border-color: var(--td-brand-color);
  }
}

.add-icon-btn {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  flex: none;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-placeholder);

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.item-icon {
  width: 24px;
  height: 24px;
  flex: none;
  display: grid;
  place-items: center;
  font-size: var(--app-text-lg);
  line-height: 1;
}

.doc-info {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  padding: 0 var(--app-space-4) var(--app-space-2);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.doc-dot {
  color: var(--td-component-stroke);
}

.doc-visibility {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.save-state {
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

.item-error {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-3);
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}

.editor-host {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

/**
 * AI 侧栏展开时：编辑区让出右侧面板宽度，而不是被侧栏覆盖。
 * 宽度由 --workbench-ai-panel-width 统一控制，与 .pai-panel 保持同值。
 */
.editor-host {
  --workbench-ai-panel-width: 340px;
}

.editor-host.is-ai-open .editor-mount {
  right: var(--workbench-ai-panel-width);
}

.editor-mount {
  position: absolute;
  inset: 0;
  overflow: hidden;
  transition: right var(--app-motion-base) ease;
}

.editor-loading {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-container);
}

.ai-off-hint {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-4);
  border-top: 1px solid var(--td-component-stroke);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}
</style>
