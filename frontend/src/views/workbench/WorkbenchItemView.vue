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
      <!-- 图标入口：无图标且偏好开启时显示「添加」，已有图标时点击可更换；点击只开面板不写数据（§9.14.2） -->
      <t-popup
        v-if="editorPrefs.showAddIcon || item?.icon"
        v-model="iconPickerOpen"
        trigger="click"
        placement="bottom-left"
      >
        <button
          v-if="editorPrefs.showAddIcon && !item?.icon"
          class="add-icon-btn"
          type="button"
          :aria-label="$t('workbench.editor.addIcon')"
        >
          <t-icon name="image" size="14px" />
        </button>
        <button
          v-else-if="item?.icon"
          class="item-icon"
          type="button"
          :aria-label="$t('workbench.editor.addIcon')"
        >
          {{ item.icon }}
        </button>
        <template #content>
          <div class="icon-picker" @click.stop>
            <p class="icon-picker-title">{{ $t('workbench.editor.addIconHint') }}</p>
            <div class="icon-picker-grid">
              <button
                v-for="emoji in ICON_EMOJI_OPTIONS"
                :key="emoji"
                type="button"
                class="icon-picker-option"
                :class="{ 'is-selected': item?.icon === emoji }"
                @click="saveItemIcon(emoji)"
              >
                {{ emoji }}
              </button>
            </div>
            <button v-if="item?.icon" type="button" class="icon-picker-clear" @click="saveItemIcon('')">
              {{ $t('workbench.editor.iconClear') }}
            </button>
          </div>
        </template>
      </t-popup>
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
      <!-- 智能关联：发现结果只显示角标，绝不自动改画布（§P3） -->
      <t-button
        v-if="canShowRelations"
        variant="text"
        size="small"
        class="relation-entry"
        @click="relationsOpen = true"
      >
        <t-icon name="link" size="14px" />
        <span>{{ $t('workbench.relation.entry') }}</span>
        <span v-if="relationBadge > 0" class="relation-badge">{{ relationBadge }}</span>
      </t-button>
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
      @new-row="onNewRow"
    />

    <!-- 行详情抽屉：自研只读版，数据来自同一个 dataSource（原型 .db-drawer） -->
    <DatabaseRowDrawer
      v-if="isDatabase"
      :open="rowDrawerOpen"
      :detail="rowDetail"
      :readonly="dbState.readonly"
      @close="closeRowDrawer"
      @delete="onDeleteRow"
    />

    <!-- 画布文档卡片详情：点击卡片标题栏打开，只读预览全文 -->
    <NoteDetailDrawer
      v-if="!isDatabase"
      :open="noteDrawerOpen"
      :title="noteDetailTitle"
      :markdown="noteDetailMarkdown"
      :word-count="wordCount"
      :updated-label="updatedLabel"
      @close="noteDrawerOpen = false"
    />

    <div
      v-if="!loadError"
      class="editor-host"
      :class="{
        'is-ready': ready,
        'is-ai-open': aiPanelOpen,
        'is-db-focus': dbFocused || isDatabase,
        'has-format-bar': !isDatabase,
      }"
    >
      <!-- BlockSuite 挂载点：由 editor-session 注入宿主元素 -->
      <div ref="hostRef" class="editor-mount" />
      <!--
        关联卡片文字层：画布上只有空 frame，文字在这里画。
        必须放在 .editor-host 内并被 .editor-mount 覆盖：定位基准与画布视口一致。
      -->
      <RelationCardLayer
        v-if="showRelationLayer"
        :cards="relationState?.cards ?? []"
        :rects="relationRects"
        :view="relationView"
        @open="openRelatedItem"
      />
      <!-- 事项表格分页：默认每页 20 条；非当前页的行在视图层隐藏 -->
      <div
        v-if="isDatabase && ready && dbState.currentViewMode === 'table' && dbRowTotal > DB_PAGE_SIZE"
        class="db-pager"
      >
        <t-pagination
          v-model="dbPage"
          :total="dbRowTotal"
          :page-size="DB_PAGE_SIZE"
          size="small"
          :show-page-size="false"
          @change="onPageChange"
        />
      </div>
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

    <!-- 标签（P4-2）：勾选即生效，标签只对本人可见 -->
    <ItemTagDialog v-model:visible="tagsOpen" :item-id="itemId" />

    <!-- 反向链接（P4-2）：页内提及 -->
    <BacklinksDialog v-model:visible="backlinksOpen" :item-id="itemId" />

    <!-- 画布 AI（P2-6）：自然语言 → 分组/平移补丁，预览确认后一次性写入 -->
    <CanvasAIDialog
      v-if="canUseAI && !isDatabase"
      v-model:visible="canvasAIOpen"
      :item-id="itemId"
      :agent-id="aiPrefs.defaultAgentId"
      :snapshot="readCanvasSnapshotForAI"
      :apply="applyCanvasPlanFromAI"
      @applied="onCanvasAIApplied"
    />

    <!-- 智能关联（P3）：召回结果预览勾选 → 写入锚点 / 引用卡片 / 连线 -->
    <CanvasRelationsDialog
      v-if="!isDatabase"
      v-model:visible="relationsOpen"
      :item-id="itemId"
      :agent-id="aiPrefs.defaultAgentId"
      :applied-item-ids="appliedRelationIds"
      :ignored-item-ids="relationState?.ignoredItemIds ?? []"
      :apply="applyRelationsFromAI"
      @applied="onRelationsApplied"
      @ignored="onRelationsIgnored"
    />

    <!-- 导入资料（P4-1）：加入工作知识库或作为本条目附件。
         数据库条目的“页面”是数据视图，没有正文可插附件，因此只提供加入知识库 -->
    <ImportMaterialDialog
      v-model:visible="importOpen"
      :item-id="isDatabase ? '' : itemId"
      :upload-asset="onUploadAssetForImport"
      @attach="onImportedAttachments"
    />

    <!-- 识别事项：AI 识别（不可用时降级为正文待办规则路径）→ 预览勾选 → 确认后同步到「事项」库 -->
    <CollectDialog
      v-model:visible="collectOpen"
      :candidates="collectCandidates"
      :loading="collectLoading"
      :origin="collectOrigin"
      @confirm="onCollectConfirm"
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
import { computed, h, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin, NotifyPlugin } from 'tdesign-vue-next'
import ArchiveBadge from './components/ArchiveBadge.vue'
import VersionHistoryDialog from './components/VersionHistoryDialog.vue'
import PageAILayer from './components/PageAILayer.vue'
import TemplatePicker from './components/TemplatePicker.vue'
import SaveTemplateDialog from './components/SaveTemplateDialog.vue'
import ModeSwitch from './components/ModeSwitch.vue'
import DatabaseToolbar from './components/DatabaseToolbar.vue'
import DatabaseRowDrawer from './components/DatabaseRowDrawer.vue'
import NoteDetailDrawer from './components/NoteDetailDrawer.vue'
import DatabaseAIDialog from './components/DatabaseAIDialog.vue'
import CanvasAIDialog from './components/CanvasAIDialog.vue'
import CanvasRelationsDialog from './components/CanvasRelationsDialog.vue'
import RelationCardLayer from './components/RelationCardLayer.vue'
import ItemTagDialog from './components/ItemTagDialog.vue'
import BacklinksDialog from './components/BacklinksDialog.vue'
import ImportMaterialDialog from './components/ImportMaterialDialog.vue'
import CollectDialog from './components/CollectDialog.vue'
import {
  createArchive,
  createWorkbenchTemplate,
  getArchiveState,
  getWorkbenchItem,
  listWorkbenchTemplates,
  refreshArchive,
  requestItemCandidates,
  requestWorkbenchRelations,
  restoreWorkbenchVersion,
  saveWorkbenchThumbnail,
  updateWorkbenchItem,
  workbenchErrorCode,
  WORKBENCH_ERROR,
} from '@/api/workbench'
import { useEditorSession } from '@/workbench/editor/use-editor-session'
import { captureWorkbenchThumbnail } from '@/workbench/thumbnail'
import { WB_NOTE_DETAIL_EVENT, noteCardTitle } from '@/workbench/editor/note-events'
import { createPageAIAdapter, type PageAIAdapterOptions } from '@/workbench/ai/editor-adapter'
import { useWorkbenchPrefs } from '@/workbench/use-workbench-prefs'
import { useWorkbenchStore } from '@/stores/workbench'
import { type ArchiveBadgeAction } from '@/workbench/archive-status'
import { shouldAdoptTemplateTitle, type PreparedTemplateSnapshot } from '@/workbench/templates/apply'
import type { TemplateCardItem } from '@/workbench/templates/picker'
import type { ArchiveStatus, WorkbenchItem } from '@/types/workbench'
import type { ItemCandidate, ItemCandidateOrigin } from '@/workbench/ai/item-candidates'
import { buildItemPatch, dedupeCandidates } from '@/workbench/ai/item-candidates'
import type { DatabaseToolbarController } from '@/workbench/editor/database-session'
import type { DatabaseRowDetail } from '@/workbench/editor/database-session'
import type { DatabaseToolbarState } from '@/workbench/editor/database-state'
import { EMPTY_DATABASE_STATE } from '@/workbench/editor/database-state'
import { installCalendarEdgeFlip } from '@/workbench/editor/database-calendar'
import { installDatabaseTextLocale } from '@/workbench/editor/database-text-locale'
import { installDatabaseCellDecorator } from '@/workbench/editor/database-cell-decorator'
import { applyCanvasPlan, readCanvasSnapshot } from '@/workbench/editor/canvas-ai'
import {
  type RelationCardState,
  type RelationRect,
  type RelationsState,
} from '@/workbench/editor/canvas-relations'
import type { CanvasViewState } from '@/workbench/editor/blocksuite'
import type { CanvasPlan } from '@/workbench/ai/canvas-patch'
import type { WorkbenchRelation } from '@/api/workbench'
import type { DocExportFormat } from '@/workbench/export/export-doc'

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

// ---- 事项表格分页 ----
// Blocksuite 表格没有分页能力，这里只在视图层切页：按视图行顺序（已排序/筛选）
// 给非当前页的行加 `wb-page-hidden`，不改数据、不改上游视图配置。
const DB_PAGE_SIZE = 20
const dbPage = ref(1)
const dbRowTotal = ref(0)

function applyRowPagination() {
  const host = hostRef.value
  const controller = dbController.value
  if (!host || !controller) return
  const rows = Array.from(host.querySelectorAll('.affine-database-block-row'))
  if (!rows.length) return
  // 看板 / 日历不分页：把可能残留的隐藏标记清掉。
  if (dbState.value.currentViewMode !== 'table') {
    rows.forEach((row) => row.classList.remove('wb-page-hidden'))
    return
  }
  const ids = controller.viewRowIds()
  dbRowTotal.value = ids.length
  const pageCount = Math.max(1, Math.ceil(ids.length / DB_PAGE_SIZE))
  if (dbPage.value > pageCount) dbPage.value = pageCount
  const start = (dbPage.value - 1) * DB_PAGE_SIZE
  const visible = new Set(ids.slice(start, start + DB_PAGE_SIZE))
  rows.forEach((row) => {
    const id = row.querySelector('[data-row-id]')?.getAttribute('data-row-id') ?? ''
    row.classList.toggle('wb-page-hidden', !visible.has(id))
  })
}

/** 表格行会随数据 / 筛选 / 排序反复重建，用一帧节流避免每次都全量遍历。 */
let pageApplyScheduled = false
function scheduleApplyRowPagination() {
  if (pageApplyScheduled) return
  pageApplyScheduled = true
  requestAnimationFrame(() => {
    pageApplyScheduled = false
    applyRowPagination()
  })
}

/** 翻页后只保证视图 Tab 栏仍在视口内 —— 直接滚到首行会把顶部 Tab 顶出屏幕。 */
function onPageChange() {
  applyRowPagination()
  hostRef.value
    ?.querySelector('data-view-header-views')
    ?.scrollIntoView({ block: 'nearest' })
}

/** 新建行插在第一行：切回第 1 页，等行渲染后再套用分页。 */
function onNewRow() {
  dbPage.value = 1
  requestAnimationFrame(() => applyRowPagination())
}

watch(dbState, () => scheduleApplyRowPagination(), { flush: 'post' })
watch(dbPage, () => applyRowPagination())

// 行节点由 BlockSuite 自己重建，watch 不到：用 MutationObserver 兜住每一次渲染。
let pageObserver: MutationObserver | null = null
watch(
  hostRef,
  (host) => {
    pageObserver?.disconnect()
    pageObserver = null
    if (!host) return
    pageObserver = new MutationObserver(() => scheduleApplyRowPagination())
    pageObserver.observe(host, { childList: true, subtree: true })
    applyRowPagination()
  },
  { immediate: true },
)

/** CSV 导入的隐藏文件输入；菜单触发 click 后由 change 事件消费 */
const importInputRef = ref<HTMLInputElement | null>(null)
/** 画布导出包（P2-4）在飞：菜单项置灰，避免重复打包 */
const exporting = ref(false)
/** 数据库 AI 对话框（P3-5）：只在数据库条目 + AI 开关打开时可用 */
const databaseAIOpen = ref(false)
/** 导入资料对话框（P4-1） */
const importOpen = ref(false)
/** 识别事项：预览弹窗、候选列表与加载态 */
const collectOpen = ref(false)
const collectCandidates = ref<ItemCandidate[]>([])
const collectLoading = ref(false)
const collectOrigin = ref<ItemCandidateOrigin>('ai')
const isDatabase = computed(() => item.value?.kind === 'database')
/** 跨月拖拽的清理函数：挂在编辑器宿主上，销毁时解除。 */
let stopEdgeFlip: (() => void) | null = null
/** 上游英文文案本地化的清理函数（P3-1）。 */
let stopTextLocale: (() => void) | null = null
/** 单元格装饰层（状态色 / 优先级圆点 / 头像 / 逾期 / 空值）的清理函数。 */
let stopCellDecorator: (() => void) | null = null
/** 行详情入口（接管行内「查看详情」按钮）的清理函数。 */
let stopRowDetailTrigger: (() => void) | null = null
/** 上游行详情抽屉（center-peek）删除入口的清理函数。 */
let stopPeekDelete: (() => void) | null = null

/** 行详情抽屉的开关与数据。 */
const rowDrawerOpen = ref(false)
const rowDetail = shallowRef<DatabaseRowDetail | null>(null)
/** 代际计数：动态 import 期间若编辑器被重建，用它丢弃过期的一次初始化。 */
let dbGeneration = 0

// ---- 画布文档卡片详情（右侧抽屉） ----
const noteDrawerOpen = ref(false)
/** 打开时快照一次正文投影，避免抽屉开着时内容变化导致预览跳动 */
const noteDetailMarkdown = ref('')
const noteDetailTitle = computed(() => title.value || item.value?.title || '')

/** 画布卡片标题栏点击 → 打开只读详情抽屉（由 blocksuite 的卡片配置派发事件）。 */
function openNoteDetail() {
  if (isDatabase.value) return
  noteDetailMarkdown.value = session.markdown()
  noteDrawerOpen.value = true
}

/** 画布卡片标题栏与条目页标题保持一致（两份数据源，改标题后立即同步）。 */
watch(title, (value) => {
  noteCardTitle.value = value
}, { immediate: true })

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
 * 识别事项：优先走 AI，AI 不可用或失败时降级到正文待办的规则路径。
 *
 * 两条路径都产出 `ItemCandidate`，确认后的写入链路完全一致；
 * AI 只是「理解得更好」，不是完成这件事的前提。
 */
async function openCollect() {
  collectOrigin.value = canUseAI.value ? 'ai' : 'todo'
  collectCandidates.value = []
  collectOpen.value = true
  const markdown = session.markdown()
  if (canUseAI.value && markdown.trim()) {
    collectLoading.value = true
    try {
      const result = await requestItemCandidates({
        item_id: itemId.value,
        markdown,
        agent_id: aiPrefs.value.defaultAgentId || undefined,
        source_title: title.value,
        limit: 30,
      })
      const { kept } = dedupeCandidates(result.items ?? [])
      collectCandidates.value = kept
      collectOrigin.value = 'ai'
      if (!kept.length) MessagePlugin.info(t('workbench.collect.aiNotFound'))
      return
    } catch {
      // AI 失败不阻断：继续走规则路径，用户仍能把正文待办收集进事项。
      MessagePlugin.warning(t('workbench.collect.aiFailed'))
    } finally {
      collectLoading.value = false
    }
  }
  collectOrigin.value = 'todo'
  const { collectTodos: extractTodos } = await import('@/workbench/editor/collect-todos')
  collectCandidates.value = extractTodos(session.editor.value?.store).map((todo) => ({
    title: todo.title,
    done: todo.done,
  }))
}

/**
 * 确认识别结果：先落盘当前文档，再把候选暂存后跳到唯一的「事项」条目应用。
 *
 * 只暂存候选本身：状态与来源文案随建库语言变化，由事项页按真实列配置解析。
 */
async function onCollectConfirm(picked: ItemCandidate[]) {
  if (picked.length === 0) return
  collectOpen.value = false
  try {
    await session.flush()
    const things = await workbenchStore.ensureThingsItem()
    workbenchStore.setPendingCollect({
      itemId: things.id,
      candidates: picked,
      origin: collectOrigin.value,
      sourceTitle: title.value,
    })
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
  // 标签（P4-2）：canvas 与事项共用同一个对话框
  { content: t('workbench.tags.title'), value: 'tags' },
  // 反向链接（P4-2）：谁在正文里提到过本条目
  { content: t('workbench.backlinks.title'), value: 'backlinks' },
  // 画布 AI（P2-6）：只在无限画布形态出现——Page 形态没有 surface 可整理
  {
    content: t('workbench.canvasAi.entry'),
    value: 'canvas-ai',
    disabled: !canUseAI.value || session.mode.value !== 'edgeless' || !ready.value,
  },
  // 画布导出包（P2-4）：快照 + 资产 + manifest；导出中置灰并换文案
  {
    content: exporting.value ? t('workbench.export.running') : t('workbench.export.entry'),
    value: 'export-package',
    disabled: exporting.value || !ready.value || Boolean(loadError.value),
  },
  // 文档导出：正文投影出 Markdown / HTML / Word，只带走可读内容（不含画布快照与资产）
  {
    content: t('workbench.export.docEntry'),
    value: 'export-doc',
    disabled: exporting.value || !ready.value || Boolean(loadError.value),
    children: (['markdown', 'html', 'docx'] as const).map((format) => ({
      content: t(`workbench.export.format.${format}`),
      value: `export-doc-${format}`,
    })),
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
  { value: 'tags', label: t('workbench.tags.title') },
  { value: 'backlinks', label: t('workbench.backlinks.title') },
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

// ---- 列表封面缩略图：就绪/保存后延迟截取当前视口，失败静默回退骨架 ----
let thumbnailTimer: number | null = null
let thumbnailBusy = false

function scheduleThumbnail(force = false) {
  if (thumbnailTimer !== null) window.clearTimeout(thumbnailTimer)
  // 等渲染稳定再截：刚保存时 BlockSuite 还在收尾光标/浮层。
  thumbnailTimer = window.setTimeout(() => {
    thumbnailTimer = null
    void captureAndSaveThumbnail(force)
  }, 1500)
}

async function captureAndSaveThumbnail(force: boolean) {
  const current = item.value
  if (!current || current.kind === 'database' || !ready.value || loadError.value || thumbnailBusy) {
    return
  }
  if (document.visibilityState !== 'visible') return
  const version = baseVersion.value
  if (version <= 0) return
  // 内容没变且已有同版本封面就不重拍；切换形态时 force 覆盖同一版本。
  if (!force && (current.thumbnail_version ?? 0) >= version) return
  // 空白文档截图只有底色，没有信息量，继续用骨架封面。
  if (wordCount.value === 0) return

  thumbnailBusy = true
  try {
    const blob = await captureWorkbenchThumbnail(hostRef.value, session.mode.value)
    if (!blob) return
    const saved = await saveWorkbenchThumbnail(current.id, blob, version)
    if (item.value?.id === current.id) {
      item.value.thumbnail_version = saved.thumbnail_version
    }
  } catch {
    // 封面是展示增强，失败不打扰用户，卡片自动保留骨架封面。
  } finally {
    thumbnailBusy = false
  }
}

// ready 覆盖“打开旧文档补拍”；savedAt 覆盖“编辑后内容变化”。
watch([ready, savedAt], ([isReady]) => {
  if (isReady) scheduleThumbnail()
})

// 形态切换不改内容版本，但画面完全不同，需要强制重拍。
watch(
  () => session.mode.value,
  () => {
    if (ready.value) scheduleThumbnail(true)
  },
)
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

/**
 * 保存状态文案（§9.7）：自动保存只写回源条目，属于「草稿暂存」，
 * 与「已归档」是两条链路。因此这里统一用「草稿已暂存」系列文案，
 * 不出现「已保存 / 已归档」，避免用户以为内容已经进了知识库。
 */
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

/**
 * 最近一次保存的时间（工具条右侧显示「草稿已暂存 · 16:03」）。
 *
 * 优先用队列里真正落盘的时间；页面刚打开还没保存过时退回条目的 updated_at，
 * 否则会显示成「草稿已暂存」却没有时间。跨天时补上日期，避免昨天的 16:03 被当今天。
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
 * 「图标」入口。
 *
 * 用 emoji 面板选择并写入条目的 icon 字段（空串即清除）；
 * 偏好只决定入口显隐，本身不写任何图标数据（§9.14.2）。
 */
const ICON_EMOJI_OPTIONS = [
  '📄', '📝', '📌', '📚', '💡', '🎯', '🚀', '⭐',
  '✅', '🔥', '📊', '📈', '🗂️', '📁', '🔖', '🧠',
  '💼', '🛠️', '🎨', '🌱', '🔍', '🧩', '🌍', '❤️',
]
const iconPickerOpen = ref(false)

async function saveItemIcon(icon: string) {
  if (icon === (item.value?.icon ?? '')) {
    iconPickerOpen.value = false
    return
  }
  try {
    const updated = await updateWorkbenchItem(itemId.value, { icon })
    item.value = updated
    iconPickerOpen.value = false
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
  else if (value.startsWith('export-doc-')) void exportDocument(value.slice('export-doc-'.length) as DocExportFormat)
  else if (value === 'canvas-ai') canvasAIOpen.value = true
  else if (value === 'tags') tagsOpen.value = true
  else if (value === 'backlinks') backlinksOpen.value = true
}

/** 画布 AI 对话框（P2-6） */
const canvasAIOpen = ref(false)
/** 标签对话框（P4-2） */
const tagsOpen = ref(false)
/** 反向链接对话框（P4-2） */
const backlinksOpen = ref(false)

/**
 * 画布 AI 的两个回调：读快照、应用计划。
 *
 * 放在视图里而不是对话框里，是为了让写入始终经过 session 持有的同一个 store，
 * 走同一条脏标记与保存链路（对话框只负责问模型与预览）。
 */
function readCanvasSnapshotForAI() {
  const handle = session.editor.value
  if (!handle) return null
  return readCanvasSnapshot(handle.store)
}

function applyCanvasPlanFromAI(plan: CanvasPlan, surfaceId: string) {
  const handle = session.editor.value
  if (!handle) return { frames: 0, moved: 0, failed: plan.ops.length }
  return applyCanvasPlan(handle.store, plan, surfaceId)
}

async function onCanvasAIApplied(outcome: { frames: number; moved: number; failed: number }) {
  MessagePlugin.success(t('workbench.canvasAi.done', { frames: outcome.frames, moves: outcome.moved }))
  if (outcome.failed > 0) {
    MessagePlugin.warning(t('workbench.canvasAi.skipped', { count: outcome.failed }))
  }
}

// ---- 智能关联（P3） ----
// 三份状态各自独立：
//   relationState —— 落盘的卡片与忽略历史（存 Y.Doc，跟文档一起同步）；
//   relationRects —— 画布上 frame 的实时位置（每次内容变更重读）；
//   relationView  —— 视图矩阵（每次平移/缩放重读）。
// 分开是因为触发源不同：拖动卡片只改第一份，平移画布只改第三份。
const relationsOpen = ref(false)
const relationState = shallowRef<RelationsState | null>(null)
const relationRects = shallowRef<Map<string, RelationRect>>(new Map())
const relationView = shallowRef<CanvasViewState | null>(null)
/** 自动扫描到的待处理条数：只做角标提示，绝不自动写画布 */
const relationBadge = ref(0)
/** 当前编辑器的内容/视口订阅取消函数；编辑器重建时重绑 */
let stopRelationWatch: (() => void) | null = null

/** 只在无限画布、非数据库、有 AI 时出现入口。 */
const canShowRelations = computed(
  () => !isDatabase.value && session.mode.value === 'edgeless' && canUseAI.value && ready.value,
)

/** 覆盖层只在画布形态渲染：Page 形态没有坐标可用。 */
const showRelationLayer = computed(
  () => session.mode.value === 'edgeless' && (relationState.value?.cards.length ?? 0) > 0,
)

/** 已落盘卡片对应的条目 id：召回时排除，避免推荐重复。 */
const appliedRelationIds = computed(
  () => relationState.value?.cards.map((card: RelationCardState) => card.itemId) ?? [],
)

/**
 * 重读落盘状态与 frame 位置。
 *
 * 顺手做一次失效清理：用户直接删掉卡片 frame 后，状态里那条记录要跟着消失，
 * 否则它既不会被渲染，又会一直被算进「已应用」里、挡住之后的召回。
 * 只在真的少了卡片时才写回，避免每次内容变更都触发一次保存。
 */
function refreshRelationState() {
  const handle = session.editor.value
  if (!handle) return
  const state = handle.readRelations()
  const rects = handle.readRelationRects(state?.cards ?? [])
  // 只在确实少了卡片时写回：否则每次内容变更都会触发一次不必要的保存
  if (state && rects.size < state.cards.length) {
    const next = { ...state, cards: state.cards.filter((card) => rects.has(card.frameId)) }
    handle.saveRelations(next)
    relationState.value = next
    relationRects.value = rects
    return
  }
  relationState.value = state
  relationRects.value = rects
}

/**
 * 一次性修复老布局：早期版本用「占位 frame」当锚点，卡片按视口定位，
 * 结果框和卡片压在文档卡片上。打开文档时自动重建一次，用户不需要重新勾选。
 *
 * 只在锚点 id 与当前文档卡片不一致时触发，且每个编辑器实例最多跑一次；
 * 重建用的是状态里已存的全部关联字段，不丢信息。
 */
let relationsMigrated = false

function migrateLegacyRelations() {
  const handle = session.editor.value
  const state = relationState.value
  if (!handle || !state || relationsMigrated) return
  if (!state.anchorFrameId || !state.cards.length) return
  relationsMigrated = true
  const outcome = handle.applyRelations({ prevAnchorId: state.anchorFrameId, existing: state.cards, relations: [] })
  // 锚点 id 没变说明本来就是新布局，不用写回
  if (!outcome.anchorId || outcome.anchorId === state.anchorFrameId) return
  if (outcome.created !== state.cards.length) return
  handle.saveRelations({
    version: 1,
    anchorFrameId: outcome.anchorId,
    anchor: outcome.anchor,
    cards: outcome.cards,
    ignoredItemIds: state.ignoredItemIds,
    computedAt: Date.now(),
  })
  refreshRelationState()
}

/** 写入勾选的关联，并同步落盘状态。 */
function applyRelationsFromAI(relations: WorkbenchRelation[]): number {
  const handle = session.editor.value
  if (!handle) return 0
  const previous = relationState.value
  const outcome = handle.applyRelations({
    // 老状态里的锚点可能指向已废弃的占位 frame：交给写入层识别并迁移
    prevAnchorId: previous?.anchorFrameId,
    existing: previous?.cards ?? [],
    relations: relations.map((relation) => ({
      itemId: relation.item_id,
      title: relation.title,
      reason: relation.reason,
      strength: relation.strength === 'strong' ? 'strong' : 'weak',
      quote: relation.quote,
      targetQuote: relation.target_quote,
      label: relation.label,
      source: relation.source,
    })),
  })
  // 迁移场景可能一张新卡片都没建（只是把线改到文档卡片上），因此不能只看 created
  if (outcome.anchorId) {
    handle.saveRelations({
      version: 1,
      anchorFrameId: outcome.anchorId,
      anchor: outcome.anchor,
      cards: outcome.cards,
      ignoredItemIds: previous?.ignoredItemIds ?? [],
      computedAt: Date.now(),
    })
    refreshRelationState()
  }
  return outcome.created
}

function onRelationsApplied(count: number) {
  relationBadge.value = 0
  if (count > 0) MessagePlugin.success(t('workbench.relation.done', { count }))
  else MessagePlugin.warning(t('workbench.relation.failed'))
}

/** 忽略：只记进状态，不动画布——用户随时可以再点进来重新发现。 */
function onRelationsIgnored(itemIds: string[]) {
  const handle = session.editor.value
  const previous = relationState.value
  if (!handle || !previous || itemIds.length === 0) return
  const merged = [...new Set([...previous.ignoredItemIds, ...itemIds])]
  handle.saveRelations({ ...previous, ignoredItemIds: merged, computedAt: Date.now() })
  refreshRelationState()
}

/**
 * 点开关联卡片：跳到目标条目，并把「要定位的那句原文」带在 query 上。
 *
 * 用 `locate` 而不是新建一条跳转通道：目标页本来就会读 query，
 * 而且刷新后仍能复现定位，不需要任何全局状态。
 */
function openRelatedItem(card: RelationCardState) {
  if (!card.itemId) return
  const query = card.targetQuote ? { locate: card.targetQuote } : {}
  void router.push({ name: 'workbenchItem', params: { itemId: card.itemId }, query })
}

/**
 * 打开后延迟自动发现一次（5s 空闲）。
 *
 * 只计算角标、不落盘：用户没点确认前画布不该被动过。
 * 已有卡片或已忽略过全部候选时不再打扰。
 */
const RELATION_AUTO_DELAY_MS = 5_000
let relationTimer: ReturnType<typeof setTimeout> | null = null

function scheduleRelationScan() {
  if (relationTimer) clearTimeout(relationTimer)
  if (!canShowRelations.value || relationsOpen.value) return
  relationTimer = setTimeout(() => {
    relationTimer = null
    void scanRelationsSilently()
  }, RELATION_AUTO_DELAY_MS)
}

async function scanRelationsSilently() {
  if (!canShowRelations.value) return
  const handle = session.editor.value
  if (!handle) return
  const state = handle.readRelations()
  const exclude = [...(state?.cards.map((card) => card.itemId) ?? []), ...(state?.ignoredItemIds ?? [])]
  try {
    const result = await requestWorkbenchRelations({
      item_id: itemId.value,
      agent_id: aiPrefs.value.defaultAgentId || undefined,
      exclude_item_ids: exclude,
    })
    // 只在有待处理结果时亮角标；已经全部忽略 / 已应用完就保持安静
    relationBadge.value = result.relations.filter((relation) => relation.strength === 'strong').length
  } catch {
    relationBadge.value = 0
  }
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

/**
 * 文档导出：正文投影 → Markdown / HTML / Word。
 *
 * 与「导出画布包」互补：那边带走快照与资产用于还原画布，这里只带走可读正文，
 * 便于把文档交给外部阅读、打印或继续在 Word 里编辑。全程只读、不 flush。
 */
async function exportDocument(format: DocExportFormat) {
  if (exporting.value) return
  const current = item.value
  if (!current) return
  exporting.value = true
  try {
    // 动态 import：三种格式的构建逻辑与 jszip 不进首屏 chunk
    const { buildDocumentExport } = await import('@/workbench/export/export-doc')
    const { downloadBlob } = await import('@/utils/downloadFile')
    const result = await buildDocumentExport(format, {
      title: title.value || current.title || t('workbench.item.untitled'),
      // Word 才需要图片位置与字节；Markdown / HTML 由构建层统一降级成说明文字
      markdown: format === 'docx' ? session.markdownWithImages() : session.markdown(),
      resolveImage: format === 'docx' ? (assetId: string) => session.resolveAsset(assetId) : undefined,
    })
    downloadBlob(result.blob, result.fileName)
    MessagePlugin.success(t('workbench.export.docDone', { format: t(`workbench.export.format.${format}`) }))
  } catch {
    MessagePlugin.error(t('workbench.export.docFailed'))
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
    // 工作库文档数随归档变化：回源 bootstrap，让侧栏「工作知识库」计数保持当下值。
    void workbenchStore.fetchBootstrap().catch(() => undefined)
    if (!options.silent) {
      MessagePlugin.success(
        t(action === 'update' || action === 'retry' ? 'workbench.archive.action.update' : 'workbench.archive.action.create'),
      )
    }
    // 归档成功后异步把正文里的安排同步到事项表：不阻塞归档结果，失败只记日志。
    scheduleItemSync(markdown)
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
 * 归档后自动同步事项（用户确认的口径：直接入库，不做二次确认）。
 *
 * 三条边界：
 *   - 事项表自身归档不触发（否则会把自己的行再识别一遍）；
 *   - AI 关闭时不做（识别依赖模型，规则路径只认正文待办，归档场景收益低）；
 *   - 全程异步：归档已经成功，任何失败都只影响这次同步，不回滚归档、不弹错误。
 * 成功后给一张带「撤销」的通知卡片，撤销按本次写入的行 id 删除。
 */
function scheduleItemSync(markdown: string) {
  if (isDatabase.value || !canUseAI.value) return
  void (async () => {
    const { syncItemsAfterArchive, undoItemSync } = await import('@/workbench/item-extract')
    const outcome = await syncItemsAfterArchive({
      itemId: itemId.value,
      sourceTitle: title.value,
      markdown,
      agentId: aiPrefs.value.defaultAgentId || undefined,
    })
    if (outcome.rows <= 0) return
    NotifyPlugin.info({
      title: t('workbench.collect.title'),
      content: t('workbench.database.itemExtract.applied', { title: title.value, count: outcome.rows }),
      duration: 8000,
      closeBtn: true,
      footer: () =>
        h(
          't-button',
          {
            size: 'small',
            variant: 'text',
            onClick: async () => {
              const removed = await undoItemSync(outcome.rowIds)
              if (removed > 0) {
                MessagePlugin.success(t('workbench.database.itemExtract.undone', { count: removed }))
              }
            },
          },
          () => t('workbench.database.itemExtract.undo'),
        ),
    })
  })()
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
  window.addEventListener(WB_NOTE_DETAIL_EVENT, openNoteDetail)
  // 归档状态与条目元数据互不依赖：并行发起，避免把它夹在两次 await 之间
  // 白等一个往返。refreshArchiveState 自己吞掉错误，也不会抛，这里直接丢弃句柄。
  void refreshArchiveState()
  await loadItem()
})

/**
 * 加载当前 itemId 指向的条目并挂载编辑器。
 *
 * 抽成函数是为了支持「在同一路由记录内切换条目」：关联卡片与反向链接都是
 * 跳到另一个 itemId，Vue Router 会复用组件实例，onMounted 不会再执行。
 */
async function loadItem() {
  loadError.value = ''
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
  await applyLocateQuery()
}

/**
 * 切换条目：先落盘旧内容，再按新 itemId 重建编辑器。
 *
 * 不 flush 会丢未保存改动；不重建编辑器会继续显示上一条的内容。
 * 关联状态是「每个文档各一份」，因此一并清空，等新文档挂载后重读。
 */
watch(itemId, async (next, previous) => {
  if (!next || !previous || next === previous) return
  // 回滚场景：路由被改回原条目时 item 已经是它，不必再重建一次编辑器
  if (item.value?.id === next) return
  if (!session.canLeave()) {
    // 冲突或保存失败时不允许静默丢弃：停在原条目，提示用户先处理
    MessagePlugin.warning(t('workbench.editor.leaveBlocked'))
    void router.replace({ name: 'workbenchItem', params: { itemId: previous } })
    return
  }
  stopRelationWatch?.()
  stopRelationWatch = null
  relationState.value = null
  relationRects.value = new Map()
  relationBadge.value = 0
  await session.flush()
  await loadItem()
})

/**
 * 消费 `?locate=` 参数：打开关联文档时高亮被引用的那句原文（P4）。
 *
 * 高亮只是阅读辅助，定位不到（原文已改写）就静默跳过，不打扰用户。
 * 处理完立刻把参数去掉，否则用户再刷新会重复定位。
 */
async function applyLocateQuery() {
  const raw = route.query.locate
  const quote = typeof raw === 'string' ? raw : ''
  if (!quote) return
  const handle = session.editor.value
  if (handle) {
    // 等一帧：刚 mount 完时序上 DOM 还没排好，定位器自身也会重试几帧
    await new Promise((resolve) => requestAnimationFrame(resolve))
    handle.locateQuote(quote)
  }
  const query = { ...route.query }
  delete query.locate
  void router.replace({ query })
}

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
 * 关联覆盖层的三路订阅（P3）。
 *
 * 与数据库工具条同一个生命周期：编辑器重建（版本恢复/冲突重载/切模板）后
 * 旧 handle 已失效，必须重新绑定，否则覆盖层停在旧视图上。
 * 三路分别对应三种「位置会变」的原因：内容拖动、视口平移缩放、文档重载。
 */
watch(
  () => session.editor.value,
  (handle) => {
    stopRelationWatch?.()
    stopRelationWatch = null
    relationState.value = null
    relationRects.value = new Map()
    relationView.value = null
    relationBadge.value = 0
    // 换条目后允许对新文档再迁一次
    relationsMigrated = false
    if (!handle) return
    if (isDatabase.value) return

    refreshRelationState()
    // 老布局（锚点是占位框）在这里顺手迁到「连线从文档边框引出」
    migrateLegacyRelations()
    relationView.value = handle.getViewState()
    const stopView = handle.onViewChange((state) => {
      relationView.value = state
    })
    // 拖动 frame 改的是 xywh，不会触发视口信号，必须单独订阅内容变更
    const stopContent = handle.onContentChange(() => {
      refreshRelationState()
    })
    stopRelationWatch = () => {
      stopView()
      stopContent()
    }
    scheduleRelationScan()
  },
  { flush: 'post' },
)

/**
 * 切回无限画布时补一次刷新。
 *
 * Page 形态没有坐标，覆盖层被隐藏；切回来时 frame 位置可能在别处被改过，
 * 因此重读一次而不是沿用切走前的快照。
 */
watch(
  () => session.mode.value,
  (mode) => {
    if (mode !== 'edgeless') {
      relationRects.value = new Map()
      return
    }
    refreshRelationState()
    scheduleRelationScan()
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
      // 上一版误用了「Calendar 视图」这种中英混排名，一并纠正为新文案。
      'Calendar 视图': t('workbench.database.views.calendar'),
      'Calendar 視圖': t('workbench.database.views.calendar'),
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
  // 上游自己的行详情抽屉（行菜单 / 看板卡片 / 日历事项 / 新建行）也补上删除入口。
  stopPeekDelete = installPeekDetailDelete()
}

/**
 * 消费「识别事项」暂存（AI 路径与正文待办路径共用）。
 *
 * 列 id、列名与选项文案全部从控制器读真实配置：建库语言不同，
 * 「待开始 / 文档归档」的文案也不同，硬编码会写错值。任一步不成立就把暂存放回，
 * 等下一次状态更新（`onUpdate`）再试，不丢弃用户已确认的事项。
 */
async function applyPendingCollect(controller: DatabaseToolbarController) {
  // 数据视图还没渲染出来时先不动暂存：此时写入必然失败。
  if (!controller.state.ready) return
  const pending = workbenchStore.takePendingCollect()
  if (!pending) return
  // 暂存目标不是当前条目（例如用户先打开了别的文档）：放回，留给事项页消费。
  if (pending.itemId !== itemId.value) {
    workbenchStore.setPendingCollect(pending)
    return
  }
  const schema = controller.itemWriteSchema(pending.sourceTitle)
  if (!schema) {
    workbenchStore.setPendingCollect(pending)
    return
  }
  // 来源列：手动识别 / 归档自动抽取 / 正文待办，分别落到对应选项。
  schema.sourceLabel =
    pending.origin === 'todo'
      ? controller.sourceLabel('manual')
      : pending.origin === 'archive'
        ? controller.sourceLabel('archive')
        : controller.sourceLabel('ai')

  // 与表里已有行去重：同标题 + 同截止日视为同一件事，避免重复点一次就多一份。
  const { kept } = dedupeCandidates(pending.candidates, controller.existingFingerprints())
  if (!kept.length) {
    MessagePlugin.info(t('workbench.collect.aiNotFound'))
    return
  }
  const patch = buildItemPatch(kept, schema)
  const outcome = controller.applyAiPatch(patch, false)
  if (!outcome) {
    // 未绑定 / 只读 / 写入被拒：放回，避免静默丢掉这批事项。
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

/**
 * 自研抽屉里删除当前行：删除成功后关掉抽屉。
 */
function onDeleteRow() {
  const detail = rowDetail.value
  if (!detail) return
  confirmDeleteRow(detail.rowId, closeRowDrawer)
}

/**
 * 删除事项的二次确认（自研抽屉与上游详情抽屉共用）。
 *
 * 删除不可逆，因此不直接执行；成功后调用 `onDeleted`，由各入口关闭自己的面板。
 */
function confirmDeleteRow(rowId: string, onDeleted: () => void) {
  const controller = dbController.value
  if (!controller) return
  const title = controller.rowDetail(rowId)?.title || t('workbench.item.untitled')
  const dialog = DialogPlugin({
    header: t('workbench.database.toolbar.deleteRow'),
    body: t('workbench.database.toolbar.deleteBody', { title }),
    theme: 'warning',
    confirmBtn: t('workbench.database.toolbar.deleteRow'),
    cancelBtn: t('workbench.database.toolbar.close'),
    onConfirm: () => {
      dialog.hide()
      const removed = controller.deleteRows([rowId])
      if (!removed) {
        MessagePlugin.warning(t('workbench.database.toolbar.deleteFailed'))
        return
      }
      onDeleted()
      MessagePlugin.success(t('workbench.database.toolbar.deleteDone'))
    },
    onClose: () => dialog.hide(),
  })
}

/**
 * 给上游行详情抽屉补删除入口。
 *
 * 工作台有两套行详情：表格行内「查看详情」走自研抽屉；行菜单「展开行」、
 * 看板卡片、日历事项、新建行则走上游自己的详情抽屉（center-peek，
 * 由 editor-popover.css 改造成同样的右侧 352px 面板）。两者外观一致，
 * 删除能力也必须一致，否则用户会看到「有的抽屉能删、有的不能」。
 */
function installPeekDetailDelete(): () => void {
  const scan = () => {
    document.body.querySelectorAll('center-peek').forEach((peek) => {
      // 已注入（或上游重渲染后 footer 仍在）就跳过
      if (peek.querySelector(':scope > .wb-peek-footer')) return
      // 详情内容是 lit 渲染的，可能晚一两帧才挂上，短暂重试
      let frames = 0
      const tryInject = () => {
        if (!peek.isConnected) return
        const record = peek.querySelector('affine-data-view-record-detail') as
          | (HTMLElement & { rowId?: string; view?: { readonly$?: { value?: boolean } } })
          | null
        if (!record) {
          if (frames++ < 12) requestAnimationFrame(tryInject)
          return
        }
        // 只读时不提供删除入口
        if (record.view?.readonly$?.value) return
        const footer = document.createElement('div')
        footer.className = 'wb-peek-footer'
        const button = document.createElement('button')
        button.type = 'button'
        button.className = 'wb-peek-delete'
        button.textContent = t('workbench.database.toolbar.deleteRow')
        button.addEventListener('click', () => {
          // 面板内可上下切换行，点击时再读当前行，避免绑到已切换走的行
          const rowId = String(record.rowId ?? '')
          if (!rowId) return
          confirmDeleteRow(rowId, () => {
            // 上游没有关闭详情抽屉的公开 API，点它自己的关闭按钮等同用户关闭
            peek.querySelector<HTMLElement>('.close-modal')?.click()
          })
        })
        footer.appendChild(button)
        peek.appendChild(footer)
      }
      tryInject()
    })
  }
  // 抽屉挂在 body 上：只在新增节点是 center-peek（或其容器）时重扫，
  // 避免编辑器内部的频繁 DOM 变更也触发深查询。
  const observer = new MutationObserver((mutations) => {
    for (const mutation of mutations) {
      for (const node of mutation.addedNodes) {
        if (node.nodeType !== 1) continue
        const element = node as Element
        if (element.matches('center-peek') || element.firstElementChild?.matches('center-peek')) {
          scan()
          return
        }
      }
    }
  })
  observer.observe(document.body, { childList: true, subtree: true })
  scan()
  return () => observer.disconnect()
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
  stopPeekDelete?.()
  stopPeekDelete = null
  closeRowDrawer()
  dbController.value?.destroy()
  dbController.value = null
  dbState.value = EMPTY_DATABASE_STATE
}

onBeforeUnmount(() => {
  window.removeEventListener(WB_NOTE_DETAIL_EVENT, openNoteDetail)
  // 待触发的自动同步直接丢弃：离开页面时 flush 与归档提交会互相抢 base_version，
  // 一旦 409 就变成「离开时报错」。下次进来徽标仍是「归档已过期」，手动点一下即可。
  if (autoSyncTimer) {
    clearTimeout(autoSyncTimer)
    autoSyncTimer = null
  }
  pageObserver?.disconnect()
  pageObserver = null
  if (relationTimer) {
    clearTimeout(relationTimer)
    relationTimer = null
  }
  // 未触发的封面截图直接丢弃：组件卸载后 host 已销毁，截图没有意义。
  if (thumbnailTimer !== null) {
    clearTimeout(thumbnailTimer)
    thumbnailTimer = null
  }
  stopRelationWatch?.()
  stopRelationWatch = null
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
  padding: 0;
  border: none;
  border-radius: var(--app-radius-xs);
  background: transparent;
  font-size: var(--app-text-lg);
  line-height: 1;
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

/* emoji 选择面板：8 列固定格，选中有品牌色描边 */
.icon-picker {
  width: 264px;
  padding: var(--app-space-3);

  &-title {
    margin: 0 0 var(--app-space-2);
    font-size: var(--app-text-xs);
    color: var(--td-text-color-secondary);
  }

  &-grid {
    display: grid;
    grid-template-columns: repeat(8, 1fr);
    gap: var(--app-space-1);
  }

  &-option {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    padding: 0;
    border: none;
    border-radius: var(--app-radius-xs);
    background: transparent;
    font-size: var(--app-text-lg);
    line-height: 1;
    cursor: pointer;

    &:hover {
      background: var(--td-bg-color-container-hover);
    }

    &.is-selected {
      background: var(--td-brand-color-light);
      box-shadow: inset 0 0 0 1px var(--td-brand-color);
    }
  }

  &-clear {
    width: 100%;
    margin-top: var(--app-space-2);
    padding: var(--app-space-1);
    border: none;
    border-radius: var(--app-radius-xs);
    background: transparent;
    font-size: var(--app-text-xs);
    color: var(--td-text-color-secondary);
    cursor: pointer;

    &:hover {
      background: var(--td-bg-color-container-hover);
      color: var(--td-text-color-primary);
    }
  }
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

/* 关联入口：有角标时带一个圆点数字，没有则只是一枚普通文字按钮 */
.relation-entry {
  display: inline-flex;
  align-items: center;
  gap: 4px;

  :deep(.t-button__text) {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
}

.relation-badge {
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: var(--app-radius-pill);
  background: #0052d9;
  color: #fff;
  font-size: 10px;
  line-height: 16px;
  text-align: center;
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

/**
 * 常驻格式工具条占位高度。
 * 宿主与 AI 条都按这个变量下移，工具条才有独立的一条带子，不再压住正文首行。
 */
.editor-host.has-format-bar {
  --workbench-format-bar-height: 52px;
}

/**
 * 顶部工具条带：把预留的 52px 画成一条真实的顶栏。
 * 工具条是 shadow DOM 里的 fixed 元素、本身不占布局，没有这条底边时
 * 视觉上会像浮在正文上方；补上背景与下边线后它才像「固定在顶部的一栏」。
 */
.editor-host.has-format-bar::before {
  content: '';
  position: absolute;
  inset: 0 0 auto 0;
  height: var(--workbench-format-bar-height);
  background: var(--td-bg-color-container);
  border-bottom: 1px solid var(--td-component-stroke);
  pointer-events: none;
}

/**
 * 无限画布里的文档卡片边框（方案 B · 卡片浮层）。
 *
 * 只命中页面级 note（blocksuite.ts 打的 wb-doc-note 标记），画布上新建的
 * 便签继续沿用各自的笔记样式。edgeless-note-background 的 box-shadow 由
 * note 块属性内联写入，这里用 !important 覆盖成统一的 1px 描边 + 轻投影；
 * 描边颜色取文字主色做低透明度混合，深色主题自动翻成白色描边。
 * 编辑态交还上游（背景外扩 + activeShadow），避免丢掉编辑反馈。
 */
.editor-host :deep(affine-edgeless-note.wb-doc-note:not(:has(.edgeless-note-container[data-editing='true'])) edgeless-note-background) {
  box-shadow:
    0 0 0 1px color-mix(in srgb, var(--td-text-color-primary) 11%, transparent),
    0 1px 2px rgba(16, 24, 40, 0.05),
    0 16px 36px -20px rgba(16, 24, 40, 0.3) !important;
  transition: box-shadow 0.18s ease;
}

/* 悬停：描边加深一级，投影略微抬起 */
.editor-host :deep(affine-edgeless-note.wb-doc-note:hover:not(:has(.edgeless-note-container[data-editing='true'])) edgeless-note-background) {
  box-shadow:
    0 0 0 1px color-mix(in srgb, var(--td-text-color-primary) 22%, transparent),
    0 2px 4px rgba(16, 24, 40, 0.07),
    0 22px 44px -22px rgba(16, 24, 40, 0.38) !important;
}

/**
 * 画布文档卡片标题栏：图标 + 标题 + 详情入口（参考卡片式笔记版式）。
 *
 * 标题栏必须抬到 note 遮罩之上，否则点击会被遮罩拦截，
 * 既进不了详情，也拦不住画布的选择/编辑链路。
 */
.editor-host :deep(affine-edgeless-note.wb-doc-note edgeless-page-block-title) {
  position: relative;
  z-index: 2;
  display: block;
  margin: 0 0 12px;
  padding: 0 0 10px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.editor-host :deep(affine-edgeless-note.wb-doc-note .wb-note-head) {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  user-select: none;
}

.editor-host :deep(affine-edgeless-note.wb-doc-note .wb-note-head-icon) {
  display: inline-flex;
  flex: none;
  color: var(--td-text-color-placeholder);
}

.editor-host :deep(affine-edgeless-note.wb-doc-note .wb-note-head-title) {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: var(--app-text-sm);
  font-weight: 600;
  color: var(--td-text-color-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.editor-host :deep(affine-edgeless-note.wb-doc-note .wb-note-head-detail) {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-placeholder);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-brand-color);
  }
}

.editor-host.is-ai-open .editor-mount {
  right: var(--workbench-ai-panel-width);
}

/* 覆盖层与挂载点共用同一套边界，坐标基准才对得上 */
.editor-host.is-ai-open .wrel-layer {
  right: var(--workbench-ai-panel-width);
}

.editor-mount {
  position: absolute;
  inset: 0;
  /* 常驻格式工具条占位：编辑器整体下移，正文不再被工具条压住 */
  top: var(--workbench-format-bar-height, 0px);
  overflow: hidden;
  transition: right var(--app-motion-base) ease;
}

/**
 * 文档页面模式才由挂载点承担纵向滚动。
 *
 * 挂载点同时是 BlockSuite 的 .affine-page-viewport / .affine-edgeless-viewport：
 * 页面模式需要滚动条，无限画布模式必须保持 hidden（画布自己处理平移缩放），
 * 所以这里按上游加的类名区分，不能把 overflow 全局改成 auto。
 */
.editor-mount.affine-page-viewport {
  overflow-x: hidden;
  overflow-y: auto;
}

/* 事项表格分页：浮在表格底部居中，不往 BlockSuite 内部插节点（会被 lit 重渲染清掉）。 */
.db-pager {
  position: absolute;
  left: 50%;
  bottom: 12px;
  transform: translateX(-50%);
  z-index: 6;
  padding: 4px 10px;
  border-radius: 10px;
  background: var(--td-bg-color-container);
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.08);
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

  /* 给底部悬浮的分页条留出空间，避免遮住最后一行 */
  :deep(.affine-database-table-container) {
    padding-bottom: 52px;
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
