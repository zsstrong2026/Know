<template>
  <!-- 条目编辑页：顶栏（标题 / 保存状态 / 归档）+ BlockSuite 宿主 -->
  <section class="item-page">
    <!--
      事项（kind='database'）不渲染顶栏：它是全局单例，把各文档里抽取的事项
      全量汇总在一处，也可以直接在列表里手动加行。因此「标题 / 重命名 / 归档 /
      版本历史」这套条目级元数据在事项上没有意义，页面直接从视图工具条开始。
      原来「…」菜单里的导入资料 / CSV / AI 生成迁到工具条右侧（dbMenuActions）。
    -->
    <header v-if="showItemHead" class="item-head">
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
      <!-- 收集事项：抽取正文待办，预览确认后同步到「事项」库（AI 非必需） -->
      <t-button v-if="!isDatabase && ready && !loadError" variant="text" size="small" @click="openCollect">
        {{ $t('workbench.collect.entry') }}
      </t-button>
      <!-- 形态切换：只有画布条目出现，数据库条目的“页面”就是数据视图（§10.14.5） -->
      <ModeSwitch
        v-if="canSwitchMode"
        :mode="session.mode.value"
        :disabled="pickerOpen || !ready"
        @change="onModeChange"
      />
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

    <!-- CSV 导入的隐藏文件选择器：菜单项只负责触发 click，读文件在 change 回调里完成 -->
    <input
      ref="importInputRef"
      type="file"
      accept=".csv,text/csv"
      style="display: none"
      @change="onImportFileChange"
    />

    <div v-if="loadError" class="item-error">
      <t-icon name="error-circle" size="20px" />
      <span>{{ loadError === 'notFound' ? $t('workbench.editor.notFound') : $t('workbench.editor.loadFailed') }}</span>
      <t-button size="small" variant="outline" @click="goBack">
        {{ $t('workbench.editor.back') }}
      </t-button>
    </div>

    <!-- 文档信息：更新时间 / 字数 / 可见性；数据全部来自已加载内容，不额外请求。
         数据库条目的“页面”就是数据视图（§10.7.2），没有正文，因此不显示这行文档信息。 -->
    <div v-if="!loadError && !isDatabase && editorPrefs.showDocInfo" class="doc-info">
      <span>{{ $t('workbench.editor.docInfo.updated', { time: updatedLabel }) }}</span>
      <span class="doc-dot">·</span>
      <span>{{ $t('workbench.editor.docInfo.words', { count: wordCount }) }}</span>
      <span class="doc-dot">·</span>
      <span class="doc-visibility">
        <t-icon name="lock-on" size="11px" />
        {{ $t('workbench.editor.docInfo.private') }}
      </span>
    </div>

    <!-- 数据库工具条（P3-1）：视图 / 筛选 / 排序 / 搜索 / CSV / 新建行。
         只在数据库条目出现，且所有动作都转调上游 data-view，不复制数据库状态。 -->
    <DatabaseToolbar
      v-if="isDatabase && !loadError && ready"
      :controller="dbController"
      :state="dbState"
      :actions="dbMenuActions"
      :save-label="saveLabel"
      :save-tone="saveState"
      :saved-at-label="savedAtLabel"
      :archive-status="archiveStatus"
      :archived-version="archivedVersion"
      :archive-busy="archiving"
      @action="onToolbarAction"
      @archive="onArchiveAction"
    />

    <!-- 行详情抽屉：自研只读版，数据来自同一个 dataSource（原型 .db-drawer） -->
    <DatabaseRowDrawer
      v-if="isDatabase"
      :open="rowDrawerOpen"
      :detail="rowDetail"
      @close="closeRowDrawer"
    />

    <div
      v-if="!loadError"
      class="editor-host"
      :class="{
        'is-ready': ready,
        'is-ai-open': aiPanelOpen,
        'is-db-focus': dbFocused || isDatabase,
      }"
    >
      <!-- BlockSuite 挂载点：由 editor-session 注入宿主元素 -->
      <div ref="hostRef" class="editor-mount" />
      <div v-if="!ready && !pickerOpen" class="editor-loading">{{ $t('workbench.editor.loading') }}</div>
      <!-- 独立数据库视图的返回入口：只改 query，不重新加载文档（§10.7.2）。
           只有画布内数据库块的“全屏”才需要返回画布；数据库条目本身没有画布。 -->
      <button
        v-if="dbFocused && !isDatabase"
        class="db-focus-back"
        type="button"
        @click="leaveDatabaseFocus"
      >
        <t-icon name="chevron-left" size="13px" />
        {{ $t('workbench.database.focus.backToCanvas') }}
      </button>
      <!-- 模板选择器：打开期间不挂载编辑器（§9.16.2 规则 1） -->
      <TemplatePicker v-if="pickerOpen" @apply="onTemplateApply" @blank="onTemplateBlank" />
      <!-- Page AI：工具条/菜单/预览/侧栏；只读与回收站不出现（§9.15.1）。
           数据库条目的“页面”是数据视图，没有正文，不挂文档级 AI（数据库有自己的 AI 入口）。 -->
      <PageAILayer
        v-if="ready && canUseAI && !pickerOpen && !isDatabase"
        ref="aiLayer"
        :adapter="aiAdapter"
        :item-id="itemId"
        :editable="true"
        :agent-id="aiPrefs.defaultAgentId"
        :confirm-insert="aiPrefs.confirmInsert"
        @panel-change="(open: boolean) => (aiPanelOpen = open)"
      />
    </div>

    <div v-if="aiDisabled && ready && !isDatabase" class="ai-off-hint">
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

    <!-- 数据库 AI（P3-5）：生成列/行补丁，预览确认后一次性写入 -->
    <DatabaseAIDialog
      v-if="isDatabase && canUseAI"
      v-model:visible="databaseAIOpen"
      :item-id="itemId"
      :controller="dbController"
      :agent-id="aiPrefs.defaultAgentId"
    />

    <!-- 导入资料（P4-1）：加入工作知识库或作为本条目附件。
         数据库条目的“页面”是数据视图，没有正文可插附件，因此只提供加入知识库 -->
    <ImportMaterialDialog
      v-model:visible="importOpen"
      :item-id="isDatabase ? '' : itemId"
      :upload-asset="onUploadAssetForImport"
      @attach="onImportedAttachments"
    />

    <!-- 收集事项：规则抽取文档待办 → 预览勾选 → 确认后同步到「事项」库 -->
    <CollectDialog v-model:visible="collectOpen" :todos="collectTodos" @confirm="onCollectConfirm" />
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
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import ArchiveBadge from './components/ArchiveBadge.vue'
import VersionHistoryDialog from './components/VersionHistoryDialog.vue'
import PageAILayer from './components/PageAILayer.vue'
import TemplatePicker from './components/TemplatePicker.vue'
import SaveTemplateDialog from './components/SaveTemplateDialog.vue'
import ModeSwitch from './components/ModeSwitch.vue'
import DatabaseToolbar from './components/DatabaseToolbar.vue'
import DatabaseRowDrawer from './components/DatabaseRowDrawer.vue'
import DatabaseAIDialog from './components/DatabaseAIDialog.vue'
import ImportMaterialDialog from './components/ImportMaterialDialog.vue'
import CollectDialog from './components/CollectDialog.vue'
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
import type { CollectedTodo } from '@/workbench/editor/collect-todos'
import type { DatabaseToolbarController } from '@/workbench/editor/database-session'
import type { DatabaseRowDetail } from '@/workbench/editor/database-session'
import type { DatabaseToolbarState } from '@/workbench/editor/database-state'
import { EMPTY_DATABASE_STATE } from '@/workbench/editor/database-state'
import { installCalendarEdgeFlip } from '@/workbench/editor/database-calendar'
import { installDatabaseTextLocale } from '@/workbench/editor/database-text-locale'
import { installDatabaseCellDecorator } from '@/workbench/editor/database-cell-decorator'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()

const itemId = computed(() => String(route.params.itemId ?? ''))
const item = shallowRef<WorkbenchItem | null>(null)
const title = ref('')
const loadError = ref<'' | 'notFound' | 'failed'>('')
const archiveStatus = ref<ArchiveStatus>('none')
const archivedVersion = ref(0)
/** 归档请求在飞：自动同步与手动点击共用一把锁，避免同一份正文提交两次。 */
const archiving = ref(false)

const hostRef = ref<HTMLElement | null>(null)
const versionDialog = ref<{ open: () => void; setRestoring: (version: number) => void } | null>(null)

// ---- 数据库工具条（P3-1） ----
// 控制器由动态 import 创建：静态引入会把整个 BlockSuite 模块图拉进条目路由的
// 首屏 chunk，违背「只在真正打开内容时才加载编辑器」的约定。
const dbController = shallowRef<DatabaseToolbarController | null>(null)
const dbState = shallowRef<DatabaseToolbarState>(EMPTY_DATABASE_STATE)
/** CSV 导入的隐藏文件输入；菜单触发 click 后由 change 事件消费 */
const importInputRef = ref<HTMLInputElement | null>(null)
/** 画布导出包（P2-4）在飞：菜单项置灰，避免重复打包 */
const exporting = ref(false)
/** 数据库 AI 对话框（P3-5）：只在数据库条目 + AI 开关打开时可用 */
const databaseAIOpen = ref(false)
/** 导入资料对话框（P4-1） */
const importOpen = ref(false)
/** 收集事项：预览弹窗与本次抽取结果（规则抽取，不依赖 AI） */
const collectOpen = ref(false)
const collectTodos = ref<CollectedTodo[]>([])
const isDatabase = computed(() => item.value?.kind === 'database')
/** 跨月拖拽的清理函数：挂在编辑器宿主上，销毁时解除。 */
let stopEdgeFlip: (() => void) | null = null
/** 上游英文文案本地化的清理函数（P3-1）。 */
let stopTextLocale: (() => void) | null = null
/** 单元格装饰层（状态色 / 优先级圆点 / 头像 / 逾期 / 空值）的清理函数。 */
let stopCellDecorator: (() => void) | null = null
/** 行详情入口（接管行内「查看详情」按钮）的清理函数。 */
let stopRowDetailTrigger: (() => void) | null = null

/** 行详情抽屉的开关与数据。 */
const rowDrawerOpen = ref(false)
const rowDetail = shallowRef<DatabaseRowDetail | null>(null)
/** 代际计数：动态 import 期间若编辑器被重建，用它丢弃过期的一次初始化。 */
let dbGeneration = 0

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

/** 只有空文档才允许套模板；已保存过内容的不显示任何模板入口。数据库条目没有正文，不参与。 */
const canUseTemplate = computed(
  () =>
    templateEnabled.value &&
    !loadError.value &&
    item.value?.kind === 'canvas' &&
    snapshotVersion.value === 0 &&
    !pickerOpen.value,
)

/** v1 只做 Page 模板：无限画布与数据库条目不提供「另存为模板」（§9.16.3）。 */
const canSaveAsTemplate = computed(
  () =>
    templateEnabled.value &&
    item.value?.kind === 'canvas' &&
    (item.value?.current_mode ?? 'page') === 'page',
)

/**
 * 双形态开关（§10.14.5）：需要同时满足三个条件。
 *
 *   - 条目是画布：数据库条目的“页面”就是数据视图，章节文档不走这个编辑器；
 *   - 发布门禁 dual_mode 打开：关闭时顶栏不出现切换（F6 降级路径）；
 *   - 文档已就绪且没在选模板：避免“在无限画布里选文档模板”的歧义。
 */
const dualModeEnabled = computed(() => workbenchStore.features.dual_mode)
const canSwitchMode = computed(
  () => dualModeEnabled.value && item.value?.kind === 'canvas' && !loadError.value,
)

/**
 * 切换形态：视觉先响应，再防抖写回 current_mode。
 *
 * 写回失败不回滚视觉形态（形态是本地偏好，下次进入按服务端值兜底），
 * 只轻提示“偏好未同步”，避免用户以为编辑器坏了（§10.14.5）。
 */
function onModeChange(next: 'page' | 'edgeless') {
  if (!session.setMode(next)) return
  void updateWorkbenchItem(itemId.value, { current_mode: next })
    .then((updated) => {
      item.value = updated
    })
    .catch(() => {
      MessagePlugin.warning(t('workbench.modeSwitch.syncFailed'))
    })
}

/**
 * 独立数据库视图（§10.7.2）。
 *
 * 只改 `?focus=database`：复用同一个 CanvasHandle 与同一份 Y.Doc，
 * 不做第二份 store、不走第二条保存链路。返回画布也只改 query，不重新加载文档。
 */
const dbFocused = computed(() => route.query.focus === 'database')

/**
 * 顶栏是否渲染。
 *
 * 事项页不渲染顶栏（见模板顶部注释）。`?focus=database` 是进入事项的固定入口，
 * 条目还在加载、拿不到 kind 时先按它把顶栏收起，避免「先闪一行标题再塌掉」。
 * 画布内数据库块的全屏也是这个 query，但它加载完是 kind='canvas'，顶栏会正常出现。
 */
const showItemHead = computed(() => !isDatabase.value && !(dbFocused.value && !item.value))

function leaveDatabaseFocus() {
  const query = { ...route.query }
  delete query.focus
  void router.replace({ query })
}

/**
 * 收集事项：按规则抽正文待办做预览。
 * 不走 AI —— AI 只是可选增强，关闭时同样可用。
 */
async function openCollect() {
  const { collectTodos: extractTodos } = await import('@/workbench/editor/collect-todos')
  collectTodos.value = extractTodos(session.editor.value?.store)
  collectOpen.value = true
}

/**
 * 确认收集：先落盘当前文档，再把结果暂存后跳到唯一的「事项」条目应用。
 * 只暂存 { title, done }：状态选项值随建库语言变化，由事项页按真实列配置解析。
 */
async function onCollectConfirm(picked: CollectedTodo[]) {
  if (picked.length === 0) return
  collectOpen.value = false
  try {
    await session.flush()
    const things = await workbenchStore.ensureThingsItem()
    workbenchStore.setPendingCollect({ itemId: things.id, todos: picked })
    await router.push({
      name: 'workbenchItem',
      params: { itemId: things.id },
      query: { focus: 'database' },
    })
  } catch {
    MessagePlugin.error(t('workbench.collect.failed'))
  }
}

/**
 * 顶栏「…」菜单。
 *
 * 顶栏只在非数据库条目出现，因此这里不再有数据库分支：CSV / AI / 导入资料
 * 对事项来说都在工具条右侧的「⋯」里（见 dbMenuActions）。
 */
const moreOptions = computed(() => [
  // 导入资料（P4-1）：加入工作知识库 / 作为本条目附件两条路径
  { content: t('workbench.import.entry'), value: 'import-material' },
  // 画布导出包（P2-4）：快照 + 资产 + manifest；导出中置灰并换文案
  {
    content: exporting.value ? t('workbench.export.running') : t('workbench.export.entry'),
    value: 'export-package',
    disabled: exporting.value || !ready.value || Boolean(loadError.value),
  },
  {
    content: t('workbench.templates.save.entry'),
    value: 'save-template',
    disabled: snapshotVersion.value === 0 || !canSaveAsTemplate.value,
  },
])

/**
 * 事项页没有顶栏，原「…」菜单的三类入口迁到工具条右侧。
 *
 * 只列数据库条目真正用得上的动作：导入资料（进工作知识库）、CSV 双向、
 * 用 AI 生成列/行。归档与版本历史属于条目级元数据，事项不提供（见模板注释）。
 */
const dbMenuActions = computed<Array<{ value: string; label: string; disabled?: boolean }>>(() => [
  { value: 'import-material', label: t('workbench.import.entry') },
  { value: 'export-csv', label: t('workbench.database.toolbar.exportCsv') },
  { value: 'import-csv', label: t('workbench.database.toolbar.importCsv') },
  {
    value: 'database-ai',
    label: t('workbench.database.ai.entry'),
    disabled: aiDisabled.value || !dbState.value.ready,
  },
])

/** 工具条菜单的动作复用同一个分派函数，避免两处行为漂移。 */
function onToolbarAction(value: string) {
  onMore({ value })
}

const session = useEditorSession({
  itemId,
  host: hostRef,
  item,
  onConflict: () => openConflictDialog(),
})
const { saveState, baseVersion, savedAt } = session
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

/**
 * 最近一次保存的时间（工具条右侧显示「已保存 · 16:03」）。
 *
 * 优先用队列里真正落盘的时间；页面刚打开还没保存过时退回条目的 updated_at，
 * 否则会显示成「已保存」却没有时间。跨天时补上日期，避免昨天的 16:03 被当今天。
 */
const savedAtLabel = computed(() => {
  const stamp = savedAt.value ?? (item.value?.updated_at ? Date.parse(item.value.updated_at) : Number.NaN)
  if (!Number.isFinite(stamp)) return ''
  const at = new Date(stamp)
  if (Number.isNaN(at.getTime())) return ''
  const hh = String(at.getHours()).padStart(2, '0')
  const mm = String(at.getMinutes()).padStart(2, '0')
  const now = new Date()
  const sameDay =
    at.getFullYear() === now.getFullYear() &&
    at.getMonth() === now.getMonth() &&
    at.getDate() === now.getDate()
  return sameDay ? `${hh}:${mm}` : `${at.getMonth() + 1}-${at.getDate()} ${hh}:${mm}`
})

/**
 * 冲突三选项：用我的版本覆盖 / 重载服务端版本 / 稍后处理。
 *
 * 关闭按钮（✕、遮罩、Esc）走 onClose，只收起弹窗、保留本地改动，
 * **不能**复用 onCancel —— 那等于用户什么都没选就丢掉了自己的编辑。
 */
function openConflictDialog() {
  const dialog = DialogPlugin({
    header: t('workbench.editor.conflictTitle'),
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
    onClose: () => dialog.hide(),
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
  // 占位标题是后端建条目时写入的默认值，各语言都要视为占位（§9.16.5）。
  const placeholders = [
    t('workbench.editor.untitled'),
    t('workbench.item.untitledDocument'),
    t('workbench.item.untitledDatabase'),
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
  const value = String(data.value ?? '')
  if (value === 'import-material') {
    importOpen.value = true
    return
  }
  if (value === 'save-template') {
    void openSaveTemplateDialog()
    return
  }
  if (value === 'export-csv') exportDatabaseCsv()
  else if (value === 'import-csv') importInputRef.value?.click()
  else if (value === 'database-ai') databaseAIOpen.value = true
  else if (value === 'export-package') void exportCanvasPackage()
}

/**
 * 导出画布包（P2-4）。
 *
 * 包里是 `content.ydoc`（快照字节）+ `content.md`（投影正文）+ `assets/`（图片与附件）
 * + `manifest.json`（条目、块数、资产清单、schema 版本）。
 * 全程只读：不 flush、不产生版本，资产取不到只记 missing 不让整包失败。
 */
async function exportCanvasPackage() {
  if (exporting.value) return
  const handle = session.editor.value
  const current = item.value
  if (!handle || !current) return
  exporting.value = true
  try {
    // 动态 import：导出逻辑与 jszip 不该进首屏 chunk
    const { buildCanvasPackage, downloadBlob } = await import('@/workbench/export/export-canvas')
    const result = await buildCanvasPackage(
      {
        id: current.id,
        title: current.title || t('workbench.item.untitled'),
        kind: current.kind,
        mode: current.current_mode ?? session.mode.value,
        snapshotVersion: baseVersion.value,
      },
      {
        root: handle.store?.root,
        snapshot: handle.getSnapshot(),
        markdown: session.markdown(),
        resolveAsset: (assetId: string) => session.resolveAsset(assetId),
        schemaVersion: session.schemaVersion.value,
      },
    )
    downloadBlob(result.blob, result.fileName)
    MessagePlugin.success(t('workbench.export.done', { blocks: result.blocks, assets: result.assetCount }))
    if (result.missingCount > 0) {
      MessagePlugin.warning(t('workbench.export.missing', { count: result.missingCount }))
    }
  } catch {
    MessagePlugin.error(t('workbench.export.failed'))
  } finally {
    exporting.value = false
  }
}

/** 导入资料 → 作为附件：复用会话的资产上传（与粘贴/拖拽同一条链路）。 */
async function onUploadAssetForImport(file: File): Promise<string> {
  return session.uploadAsset(file)
}

/**
 * 附件上传完成：插入 attachment 块。
 *
 * 编辑器可能因刷新/切形态被重建，因此这里重新取当前实例的插入入口，
 * 而不是在打开对话框时缓存；失败时只提示，附件本身已落库可重新导入。
 */
function onImportedAttachments(files: Array<{ assetId: string; name: string; size: number; type: string }>) {
  const inserted = session.editor.value?.insertAttachments(files) ?? 0
  if (inserted === 0) {
    MessagePlugin.warning(t('workbench.import.insertFailed'))
    return
  }
  // 插入是本地 Y.Doc 变更，保存队列由 onChange 自动接管，不需要手动 flush。
}

/** CSV 导出：与工具条按钮同源，都走控制器的 columns/rows 读取。 */
function exportDatabaseCsv() {
  const result = dbController.value?.exportCsv()
  if (!result) return
  const blob = new Blob([result.content], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = result.filename
  anchor.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

/** CSV 导入：读文件文本交给控制器逐行写回，结果用消息提示。 */
async function onImportFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !dbController.value) return
  try {
    const written = dbController.value.importCsv(await file.text())
    if (written > 0) MessagePlugin.success(t('workbench.database.toolbar.importDone', { count: written }))
    else MessagePlugin.warning(t('workbench.database.toolbar.importEmpty'))
  } catch {
    MessagePlugin.error(t('workbench.database.toolbar.importFailed'))
  }
}

/**
 * 内容落盘后，已归档的快照立即过期。
 *
 * 与服务端口径一致（归档行的 source_version < 条目的 snapshot_version 即 stale），
 * 本地先切徽标可以让「事项 → 知识库」的关系一眼可见：改完这片表格，
 * 徽标就从「已归档」变成「归档已过期」，点一下就是重新同步。
 * 事项还会在此基础上排一次自动同步（见 scheduleAutoSync）。
 */
watch(saveState, (state) => {
  if (state !== 'saved') return
  if (archiveStatus.value === 'active') archiveStatus.value = 'stale'
  scheduleAutoSync()
})

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
  await submitArchive(action)
}

/**
 * 提交归档 / 更新归档。
 *
 * @param options.silent 自动同步用：成功不弹提示（徽标从「过期」回到「已归档」就是反馈），
 *                       失败仍然提示，不能让用户以为已经同步成功了。
 */
async function submitArchive(action: ArchiveBadgeAction, options: { silent?: boolean } = {}) {
  if (action === 'none' || action === 'progress') return
  if (archiving.value) return
  // 事项的归档正文就是当前表格的投影（Markdown 表格，§6.3「数据库按当前视图生成」）。
  const markdown = session.markdown()
  if (!markdown.trim()) {
    if (!options.silent) MessagePlugin.warning(t('workbench.archive.status.none'))
    return
  }
  archiving.value = true
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
    if (!options.silent) {
      MessagePlugin.success(
        t(action === 'update' || action === 'retry' ? 'workbench.archive.action.update' : 'workbench.archive.action.create'),
      )
    }
  } catch (error) {
    if (workbenchErrorCode(error) === WORKBENCH_ERROR.archiveStale) {
      MessagePlugin.warning(t('workbench.archive.status.stale'))
      return
    }
    MessagePlugin.error(t('workbench.editor.saveError'))
  } finally {
    archiving.value = false
  }
}

/**
 * 自动同步（仅事项）。
 *
 * 事项是一张会被反复微调的表，手动点「更新归档」在真实使用里必然忘记，
 * 于是「知识库里那份」很快就不是最新的。这里在**每次落盘之后**排一个
 * `AUTO_SYNC_DELAY_MS` 的静默定时器：
 *   - 期间又有新保存 → 重置计时，只在真正停下来之后同步一次；
 *   - 只有「已经同步过」的表才自动跟随（status 从 active 被本地切成 stale），
 *     首次归档仍是显式动作 —— 不替用户决定往知识库写东西。
 * 不按每次保存立即同步，是因为一次 refresh 会触发解析 + 切分 + 重建索引。
 */
const AUTO_SYNC_DELAY_MS = 30_000
let autoSyncTimer: ReturnType<typeof setTimeout> | null = null

function scheduleAutoSync() {
  if (!isDatabase.value) return
  if (autoSyncTimer) clearTimeout(autoSyncTimer)
  autoSyncTimer = setTimeout(() => {
    autoSyncTimer = null
    void runAutoSync()
  }, AUTO_SYNC_DELAY_MS)
}

async function runAutoSync() {
  // 定时器等的这段时间里可能又开始编辑了：只有「已保存 + 确实过期」才同步，
  // 否则会把半截内容写进知识库。
  if (saveState.value !== 'saved') return
  if (archiveStatus.value !== 'stale') return
  await submitArchive('update', { silent: true })
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
  // 数据库条目没有正文可套模板，带 template 参数也按普通数据库打开。
  if (templateEnabled.value && route.query.template === '1' && item.value?.kind !== 'database') {
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
    return
  }
  // 数据库条目的默认打开方式就是聚焦视图（它的“页面”本来就只有数据视图）。
  // 放在挂载之后：编辑器已经按同一份 Y.Doc 渲染完，改 query 不会触发重挂载。
  if (item.value?.kind === 'database' && route.query.focus !== 'database') {
    void router.replace({ query: { ...route.query, focus: 'database' } })
  }
})

/**
 * 编辑器实例变化时重建数据库工具条（P3-1）。
 *
 * 必须跟着 `session.editor` 走而不是只在 onMounted 里建一次：
 * 版本恢复、冲突重载、模板套用都会销毁并重建编辑器，旧控制器指向的 handle
 * 已经失效（写回去的单元格不会进入保存链路）。
 */
watch(
  () => session.editor.value,
  (handle) => {
    teardownDatabaseTools()
    if (!handle || !hostRef.value || !isDatabase.value) return
    void setupDatabaseTools(handle)
  },
  { flush: 'post' },
)

/**
 * 建数据库工具条控制器。
 *
 * 依赖编辑器已经挂载完成：控制器要从宿主 DOM 里反查上游的 DataViewRootUILogic。
 * 同时绑一次日历跨月拖拽（P3-3）——只对数据库条目生效，画布条目不受影响。
 */
async function setupDatabaseTools(handle: NonNullable<typeof session.editor.value>) {
  const host = hostRef.value
  if (!host) return
  const generation = ++dbGeneration
  const { DatabaseToolbarController } = await import('@/workbench/editor/database-session')
  // 动态 import 期间可能又切换了编辑器：直接放弃这一次，避免写回过期实例。
  if (generation !== dbGeneration) return
  try {
    const controller = new DatabaseToolbarController(handle, host)
    // 老条目里是上游默认的英文视图名（Table View…）：按当前语言就地改名，
    // 用户自己改过的名字不在表里，保持原样。
    // 另外把上一版写入的本地化名（表格 / 看板 / 日历）也一并升级成新文案，
    // 否则日历 Tab 会停在旧名字上。
    controller.localizeViewNames({
      'Table View': t('workbench.database.views.table'),
      'Kanban View': t('workbench.database.views.kanban'),
      'Calendar View': t('workbench.database.views.calendar'),
      表格: t('workbench.database.views.table'),
      看板: t('workbench.database.views.kanban'),
      日历: t('workbench.database.views.calendar'),
    })
    controller.onUpdate(() => {
      // 每次重算都换一个新对象，shallowRef 才会触发 Vue 更新。
      dbState.value = { ...controller.state }
      // 数据视图晚于控制器就绪：状态一变就尝试消费「收集事项」暂存。
      void applyPendingCollect(controller)
    })
    dbState.value = { ...controller.state }
    dbController.value = controller
    // 首次 derive 可能早于 onUpdate 注册：补一次，避免错过已经就绪的时机。
    void applyPendingCollect(controller)
  } catch {
    // 数据库块缺失（历史条目/模板异常）时不显示工具条，不影响编辑与保存。
    dbController.value = null
  }
  stopEdgeFlip = installCalendarEdgeFlip(host)
  // 上游硬编码英文文案（New Record / Search... / 日历空态）在这里就地本地化。
  // 与工具条同生命周期：编辑器重建（版本恢复/冲突重载）后重新安装。
  stopTextLocale = installDatabaseTextLocale(host)
  // 单元格装饰：状态文字色、优先级圆点、负责人头像、逾期标红、空值占位。
  stopCellDecorator = installDatabaseCellDecorator(host)
  // 行内「查看详情」改走自研抽屉（原型 .db-drawer）。
  stopRowDetailTrigger = installRowDetailTrigger(host)
}

/**
 * 消费「收集事项」暂存（规则路径，不经过 AI）。
 *
 * 状态列名与选项值都从真实列配置读取：建库时写入的是当时语言的文案，
 * 硬编码会在切换界面语言后写错值。任一步不成立就把暂存放回，
 * 等下一次状态更新（`onUpdate`）再试，不丢弃用户已确认的待办。
 */
async function applyPendingCollect(controller: DatabaseToolbarController) {
  // 数据视图还没渲染出来时先不动暂存：此时 applyAiPatch 必然失败。
  if (!controller.state.ready) return
  const pending = workbenchStore.takePendingCollect()
  if (!pending) return
  // 暂存目标不是当前条目（例如用户先打开了别的文档）：放回，留给事项页消费。
  if (pending.itemId !== itemId.value) {
    workbenchStore.setPendingCollect(pending)
    return
  }
  const [{ getDatabaseModel, WORKBENCH_DB_COLUMNS }, { buildCollectPatch }] = await Promise.all([
    import('@/workbench/editor/database-seed'),
    import('@/workbench/editor/collect-todos'),
  ])
  const model = getDatabaseModel(session.editor.value?.store)
  // 列声明在信号里：老条目的 `columns` 可能是普通数组，两种都兼容。
  const columns = (model?.props?.columns$.value ?? model?.props?.columns ?? []) as any[]
  const statusColumn = columns.find((column) => column.id === WORKBENCH_DB_COLUMNS.status)
  const options = (statusColumn?.data?.options ?? []) as Array<{ id: string; value: string }>
  const todo = options.find((option) => option.id === 'wb-opt-todo')?.value
  const done = options.find((option) => option.id === 'wb-opt-done')?.value
  if (!statusColumn?.name || !todo || !done) {
    workbenchStore.setPendingCollect(pending)
    return
  }
  const patch = buildCollectPatch(pending.todos, { columnName: statusColumn.name, todo, done })
  const outcome = controller.applyAiPatch(patch, false)
  if (!outcome) {
    // 未绑定 / 只读 / 写入被拒：放回，避免静默丢掉这批待办。
    workbenchStore.setPendingCollect(pending)
    return
  }
  MessagePlugin.success(t('workbench.collect.applied', { count: outcome.rows }))
}

/**
 * 行详情入口：接管行内第一个操作按钮（查看详情）。
 *
 * 上游那个按钮会打开它自己的字段编辑器弹层，这里在捕获阶段拦下并换成
 * 自研抽屉；第二个按钮（行菜单）保持上游行为。行/单元格编辑不受影响。
 */
function installRowDetailTrigger(host: HTMLElement): () => void {
  const onClick = (event: MouseEvent) => {
    const target = event.target as HTMLElement | null
    const op = target?.closest?.('.row-op')
    if (!op) return
    const row = op.closest('.affine-database-block-row')
    if (!row) return
    if (Array.from(row.querySelectorAll('.row-op'))[0] !== op) return
    const rowId = row.querySelector('[data-row-id]')?.getAttribute('data-row-id') ?? ''
    if (!rowId) return
    event.preventDefault()
    event.stopPropagation()
    openRowDrawer(rowId)
  }
  host.addEventListener('click', onClick, true)
  return () => host.removeEventListener('click', onClick, true)
}

function openRowDrawer(rowId: string) {
  const detail = dbController.value?.rowDetail(rowId)
  if (!detail) return
  rowDetail.value = detail
  rowDrawerOpen.value = true
}

function closeRowDrawer() {
  rowDrawerOpen.value = false
  rowDetail.value = null
}

function teardownDatabaseTools() {
  dbGeneration += 1
  stopEdgeFlip?.()
  stopEdgeFlip = null
  stopTextLocale?.()
  stopTextLocale = null
  stopCellDecorator?.()
  stopCellDecorator = null
  stopRowDetailTrigger?.()
  stopRowDetailTrigger = null
  closeRowDrawer()
  dbController.value?.destroy()
  dbController.value = null
  dbState.value = EMPTY_DATABASE_STATE
}

onBeforeUnmount(() => {
  // 待触发的自动同步直接丢弃：离开页面时 flush 与归档提交会互相抢 base_version，
  // 一旦 409 就变成「离开时报错」。下次进来徽标仍是「归档已过期」，手动点一下即可。
  if (autoSyncTimer) {
    clearTimeout(autoSyncTimer)
    autoSyncTimer = null
  }
  teardownDatabaseTools()
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

/**
 * 独立数据库视图（§10.7.2）：只做视图层聚焦，不改 store、不换保存链路。
 *
 * 数据库条目的“页面”就是数据视图，默认就按聚焦视图渲染（见模板上的 is-db-focus），
 * 因此这里要把上游「文档页」的痕迹全部收掉，否则会看起来像“文档里嵌了一个表格”：
 *   - page 根容器默认 720px 居中 + 左右 24px 留白 → 表格只有半屏宽，列一多就横向滚动；
 *   - 数据库块自带 8px 内边距、圆角与白色卡片底、负外边距 → 呈现为文档内联块；
 *   - note 容器是流式块，需要保持块级铺满。
 * 读的是 CSS 变量 --affine-editor-width（page-root-block 与 doc-title 都消费它）。
 */
.editor-host.is-db-focus .editor-mount {
  --affine-editor-width: 100%;
  /* 页面级左右留白交给工作台统一给，避免表格比顶栏/工具条再右移 24px */
  --affine-editor-side-padding: 0;

  :deep(.affine-page-root),
  :deep(.affine-page-root-block-container) {
    max-width: none;
    padding-bottom: 0;
  }

  /* 视图边距由工作台统一给（与顶栏 / 工具条 16px 对齐），页面自身不再叠加留白 */
  :deep(.affine-page-viewport) {
    padding-inline: var(--app-space-4);
  }

  /* 去掉数据库块的卡片外观：变回铺满的视图容器，与原型「标题 → 视图 Tab → 表格」一致 */
  :deep(affine-database) {
    display: block;
    width: 100%;
    margin: 0;
    padding: 0;
    border-radius: 0;
    background: transparent;
  }

  /**
   * 隐藏 note 里与数据库同级的空段落。
   *
   * 空白文档默认带一个空段落，数据库块之后上游也会补一个可输入的空行；
   * 数据库条目的“页面”只有数据视图，这两行空白会让页面看起来仍是文档。
   * 只作用于 note 的直接子块，数据库内部的行（在 affine-database 里）不受影响。
   */
  :deep(.affine-note-block-container > .affine-block-children-container > affine-paragraph) {
    display: none;
  }
}

.db-focus-back {
  position: absolute;
  top: var(--app-space-2);
  left: var(--app-space-2);
  z-index: 5;
  display: inline-flex;
  align-items: center;
  gap: 2px;
  height: 24px;
  padding: 0 var(--app-space-2);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-1);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    color: var(--td-text-color-primary);
  }
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
