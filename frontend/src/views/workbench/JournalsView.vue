<template>
  <!-- Journals：主区编辑器 + 右侧月历面板（§9.13） -->
  <section class="journals">
    <div class="journals-main">
      <header class="j-head">
        <t-button variant="text" shape="square" :aria-label="$t('workbench.journals.back')" @click="goBack">
          <t-icon name="chevron-left" size="16px" />
        </t-button>
        <div class="j-crumb">
          <span>{{ $t('workbench.nav.title') }}</span>
          <span class="sep">/</span>
          <span>{{ $t('workbench.nav.journals') }}</span>
        </div>
        <span class="j-title">{{ currentEntry?.title ?? dateLabel(selectedDate) }}</span>
        <span v-if="currentEntry" class="j-save-state" :class="`is-${saveState}`">{{ saveLabel }}</span>
        <span class="spacer" />
        <!-- 智能关联：与条目页画布同一套入口，发现结果只显示角标，绝不自动改画布 -->
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
        <!-- 识别事项：抽取正文待办，预览确认后同步到「事项」库（AI 非必需） -->
        <t-button v-if="currentEntry && ready" variant="text" size="small" @click="openCollect">
          {{ $t('workbench.collect.entry') }}
        </t-button>
        <!-- 一键排版：AI 按语义分段 + 标题层级 / 首行缩进 / 行距；排版前的快照写在文档里，可一键回退 -->
        <t-button
          v-if="currentEntry && ready"
          variant="text"
          size="small"
          :disabled="formatting"
          @click="onFormatDoc"
        >
          {{ formatting ? $t('workbench.editor.formatDoc.running') : $t('workbench.editor.formatDoc.apply') }}
        </t-button>
        <t-button
          v-if="currentEntry && ready && formatActive"
          variant="text"
          size="small"
          :disabled="formatting"
          @click="onRevertFormat"
        >
          {{ $t('workbench.editor.formatDoc.revert') }}
        </t-button>
        <!-- 日历 / AI 开关：默认开启右侧面板，取消选中即收起并恢复整宽编辑区 -->
        <t-button
          variant="text"
          size="small"
          class="side-drawer-entry"
          :class="{ 'is-open': sideOpen }"
          :aria-pressed="sideOpen"
          :aria-label="$t('workbench.journals.panelSwitch')"
          @click="toggleSideDrawer"
        >
          <template #icon><t-icon name="calendar" size="14px" /></template>
          {{ $t('workbench.journals.panelEntry') }}
        </t-button>
        <div class="mode-switch" role="group" :aria-label="$t('workbench.journals.modeSwitch')">
          <button
            v-for="option in modeOptions"
            :key="option.value"
            type="button"
            :class="{ 'is-active': mode === option.value }"
            :aria-pressed="mode === option.value"
            :disabled="!currentEntry"
            @click="switchMode(option.value)"
          >
            <t-icon :name="option.icon" size="13px" />
            {{ $t(option.labelKey) }}
          </button>
        </div>
        <ArchiveBadge
          v-if="currentEntry"
          :status="currentEntry.archive_status"
          :archived-version="currentEntry.archived_version"
          @action="onArchiveAction"
        />
      </header>

      <JournalDateStrip
        :selected-date="selectedDate"
        :entries="store.journals"
        @select="selectDate"
        @step="stepWeek"
      />

      <div v-if="loadError" class="j-error">
        <t-icon name="error-circle" size="18px" />
        <span>{{ $t('workbench.editor.loadFailed') }}</span>
        <t-button size="small" variant="outline" @click="reloadDay">{{ $t('workbench.journals.retry') }}</t-button>
      </div>

      <div v-else-if="!currentEntry" class="j-empty">
        <div class="je-icon"><t-icon name="calendar" size="24px" /></div>
        <p class="je-date">{{ dateLabel(selectedDate) }}</p>
        <h2>{{ dayEntries.length ? $t('workbench.journals.pickOne', { count: dayEntries.length }) : $t('workbench.journals.emptyTitle') }}</h2>
        <p class="je-hint">{{ dayEntries.length ? $t('workbench.journals.pickHint') : $t('workbench.journals.emptyHint') }}</p>
        <t-button
          v-for="entry in dayEntries"
          :key="entry.id"
          class="je-choice"
          variant="outline"
          block
          @click="openEntry(entry.id)"
        >
          <template #icon><t-icon name="file" size="14px" /></template>
          {{ entry.title }}
        </t-button>
        <t-button v-if="dayEntries.length === 0" theme="primary" @click="createJournal">
          <template #icon><t-icon name="add" /></template>
          {{ $t('workbench.journals.new') }}
        </t-button>
      </div>

      <div v-else class="j-editor has-format-bar" :class="{ 'is-typeset': formatActive }">
        <div
          ref="hostRef"
          class="j-mount"
          :class="[
            { 'is-hidden': !ready },
            mode === 'page' ? 'affine-page-viewport' : 'affine-edgeless-viewport',
          ]"
        />
        <div v-if="!ready" class="j-loading">{{ $t('workbench.editor.loading') }}</div>

        <!--
          关联卡片文字层：画布上只建了空 frame，文字在这里画。
          定位基准与画布视口一致，必须放在 .j-editor 内、盖住 .j-mount。
        -->
        <RelationCardLayer
          v-if="showRelationLayer"
          :cards="relationState?.cards ?? []"
          :rects="relationRects"
          :view="relationView"
          @open="openRelatedItem"
        />

        <!--
          空白页起始区（参考 AFFiNE 空白文档）：
          信息 → 开始（以 AI / 模板 / 无界）→ 双向链接，整体贴在编辑区底部。
          只在空白文档的文档形态出现，用户开始输入或套用模板后自动消失。
        -->
        <div v-if="showBlankStart" class="j-blank">
          <div class="jb-inner jb-body">
            <section class="jb-info">
              <button
                class="jb-info-head"
                type="button"
                :aria-expanded="infoOpen"
                :aria-label="infoOpen ? t('workbench.journals.blank.collapse') : t('workbench.journals.blank.expand')"
                @click="infoOpen = !infoOpen"
              >
                <span>{{ t('workbench.journals.blank.info') }}</span>
                <t-icon :name="infoOpen ? 'chevron-up' : 'chevron-down'" size="12px" />
              </button>
              <div v-if="infoOpen" class="jb-info-body">
                <span>{{ t('workbench.journals.blank.updated', { time: updatedLabel }) }}</span>
                <span class="jb-dot">·</span>
                <span>{{ t('workbench.journals.blank.words', { count: wordCount }) }}</span>
                <span class="jb-dot">·</span>
                <span class="jb-private"><t-icon name="lock-on" size="10px" />{{ t('workbench.journals.blank.private') }}</span>
              </div>
            </section>

            <div class="jb-start">
              <span class="jb-start-label">{{ t('workbench.journals.blank.start') }}</span>
              <button v-if="canUseAI" class="jb-chip" type="button" @click="openAiPanel">
                {{ t('workbench.journals.blank.withAi') }}
                <t-icon name="star" size="12px" />
              </button>
              <button v-if="templateEnabled" class="jb-chip" type="button" @click="openTemplatePicker">
                {{ t('workbench.journals.blank.template') }}
                <t-icon name="bulb" size="12px" />
              </button>
              <button class="jb-chip" type="button" @click="switchMode('edgeless')">
                {{ t('workbench.journals.blank.edgeless') }}
                <t-icon name="artboard" size="12px" />
              </button>
            </div>

            <div class="jb-links">
              <span>{{ t('workbench.journals.blank.backlinks') }}</span>
              <button class="jb-show" type="button" @click="backlinksOpen = true">
                {{ t('workbench.journals.blank.show') }}
              </button>
            </div>
          </div>
        </div>

        <!-- Page AI：与条目页共用同一条流式管线，开合由右侧面板的标签控制 -->
        <PageAILayer
          v-if="ready && canUseAI && currentEntry"
          ref="aiLayer"
          :adapter="aiAdapter"
          :item-id="currentItemId"
          :editable="true"
          :agent-id="aiPrefs.defaultAgentId"
          :confirm-insert="aiPrefs.confirmInsert"
          :initial-open="false"
          :show-launcher="false"
          :panel-target="aiHostRef"
          @panel-change="onAiPanelChange"
        />

        <!-- 模板选择器：只在空白页由「模板」模块打开；关闭只是取消套用，不动当天条目 -->
        <TemplatePicker
          v-if="pickerOpen"
          @apply="onTemplateApply"
          @blank="closeTemplatePicker"
          @cancel="closeTemplatePicker"
        />
      </div>
    </div>

    <!--
      右侧抽屉：日历 / AI 共用一个可收起的面板。
      展开时作为正常栏位参与布局，顶栏与星期条随之压缩，不遮挡正文与底部信息；
      收起时宽度归零、移出布局（保留 DOM，AI 面板挂载点不会丢）。
    -->
    <aside class="j-side" :class="{ 'is-open': sideOpen }" :aria-hidden="!sideOpen">
      <div class="j-side-head">
        <div class="j-side-tabs" role="tablist" :aria-label="t('workbench.journals.panelSwitch')">
          <button
            class="j-side-tab"
            type="button"
            role="tab"
            :aria-selected="sideTab === 'calendar'"
            :class="{ 'is-active': sideTab === 'calendar' }"
            @click="selectSide('calendar')"
          >
            <t-icon name="calendar" size="14px" />
            {{ t('workbench.journals.tabCalendar') }}
          </button>
          <button
            class="j-side-tab"
            type="button"
            role="tab"
            :aria-selected="sideTab === 'ai'"
            :class="{ 'is-active': sideTab === 'ai' }"
            :disabled="!canUseAI || !currentEntry"
            @click="selectSide('ai')"
          >
            <t-icon name="star" size="14px" />
            {{ t('workbench.journals.tabAi') }}
          </button>
        </div>
        <button
          class="j-side-close"
          type="button"
          :aria-label="t('workbench.journals.closePanel')"
          :title="t('workbench.journals.closePanel')"
          @click="closeSideDrawer"
        >
          <t-icon name="close" size="15px" />
        </button>
      </div>
      <div class="j-side-body">
        <JournalMonthPanel
          v-if="sideTab === 'calendar'"
          :month="store.journalsMonth"
          :selected-date="selectedDate"
          :selected-item-id="currentItemId"
          :entries="store.journals"
          :truncated="store.journalsTruncated"
          @select="selectDate"
          @month-step="stepMonth"
          @open="openEntry"
          @create="createJournal"
          @move="onMoveEntry"
          @delete="onDeleteEntry"
        />
        <!-- AI 面板的 Teleport 容器：始终在 DOM 中，切换时只控制显隐 -->
        <div v-show="sideTab === 'ai'" ref="aiHostRef" class="j-ai-host" />
      </div>
    </aside>

    <!-- 反向链接：空白页「展示」入口 -->
    <BacklinksDialog v-model:visible="backlinksOpen" :item-id="currentItemId" />

    <!-- 画布文档卡片详情：点击卡片标题栏打开，只读预览全文（与条目页一致） -->
    <NoteDetailDrawer
      v-if="currentEntry"
      :open="noteDrawerOpen"
      :title="noteDetailTitle"
      :markdown="noteDetailMarkdown"
      :word-count="wordCount"
      :updated-label="updatedLabel"
      @close="noteDrawerOpen = false"
    />

    <!-- 智能关联（P3）：召回结果预览勾选 → 写入锚点 / 引用卡片 / 连线 -->
    <CanvasRelationsDialog
      v-if="currentEntry"
      v-model:visible="relationsOpen"
      :item-id="currentItemId"
      :agent-id="aiPrefs.defaultAgentId"
      :applied-item-ids="appliedRelationIds"
      :ignored-item-ids="relationState?.ignoredItemIds ?? []"
      :apply="applyRelationsFromAI"
      @applied="onRelationsApplied"
      @ignored="onRelationsIgnored"
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
 * Journals 页面（路线 1）。
 *
 * 三段状态：selectedDate（日期选择）、currentItemId（同日多篇区分）、
 * journalsMonth（月历浏览月，单独翻页不切正文）。
 * 日期与月份只传 YYYY-MM-DD / YYYY-MM 字符串，不做时区换算。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import ArchiveBadge from './components/ArchiveBadge.vue'
import JournalDateStrip from './components/JournalDateStrip.vue'
import JournalMonthPanel from './components/JournalMonthPanel.vue'
import PageAILayer from './components/PageAILayer.vue'
import TemplatePicker from './components/TemplatePicker.vue'
import BacklinksDialog from './components/BacklinksDialog.vue'
import NoteDetailDrawer from './components/NoteDetailDrawer.vue'
import CanvasRelationsDialog from './components/CanvasRelationsDialog.vue'
import RelationCardLayer from './components/RelationCardLayer.vue'
import CollectDialog from './components/CollectDialog.vue'
import {
  createArchive,
  getWorkbenchItem,
  refreshArchive,
  requestItemCandidates,
  requestWorkbenchRelations,
  updateWorkbenchItem,
  workbenchErrorCode,
  WORKBENCH_ERROR,
  type WorkbenchRelation,
} from '@/api/workbench'
import { useWorkbenchStore } from '@/stores/workbench'
import { applyDocFormat, collectAiSegmentTargets, readDocFormatState, revertDocFormat, type DocFormatState } from '@/workbench/editor/doc-format'
import { requestAISegments } from '@/workbench/ai/format-doc-ai'
import { useEditorSession } from '@/workbench/editor/use-editor-session'
import { WB_NOTE_DETAIL_EVENT, noteCardTitle } from '@/workbench/editor/note-events'
import { createPageAIAdapter, type PageAIAdapterOptions } from '@/workbench/ai/editor-adapter'
import { useWorkbenchPrefs } from '@/workbench/use-workbench-prefs'
import { type ArchiveBadgeAction } from '@/workbench/archive-status'
import { groupByDate } from '@/workbench/journals/month-cells'
import { addDays, currentMonth, shiftMonth, todayISO } from '@/workbench/journals/date-only'
import { dedupeCandidates, type ItemCandidate, type ItemCandidateOrigin } from '@/workbench/ai/item-candidates'
import type { RelationCardState, RelationRect, RelationsState } from '@/workbench/editor/canvas-relations'
import type { CanvasViewState, EditorMode } from '@/workbench/editor/blocksuite'
import type { PreparedTemplateSnapshot } from '@/workbench/templates/apply'
import type { TemplateCardItem } from '@/workbench/templates/picker'
import type { WorkbenchItem } from '@/types/workbench'

const props = defineProps<{ date?: string }>()

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useWorkbenchStore()

const selectedDate = ref(props.date || todayISO())
const currentItemId = ref(String(route.query.itemId ?? ''))
const currentEntry = ref<WorkbenchItem | null>(null)
const mode = ref<EditorMode>('page')
const loadError = ref(false)
const hostRef = ref<HTMLElement | null>(null)

const session = useEditorSession({
  itemId: currentItemId,
  host: hostRef,
  item: currentEntry,
  mode,
  onConflict: () => openConflictDialog(),
})
const { ready, baseVersion, saveState } = session

// ---- 一键排版 / 回退 ----
// 与条目页同一套实现：状态存在 Y.Doc 里，视图只负责决定按钮与阅读版式。
const formatState = shallowRef<DocFormatState | null>(null)
const formatActive = computed(() => formatState.value?.active === true)
/** 排版在飞（可能包含 AI 语义分段），按钮据此显示进度并防重复点击 */
const formatting = ref(false)

function refreshFormatState() {
  const handle = session.editor.value
  formatState.value = handle?.doc ? readDocFormatState(handle.doc.spaceDoc) : null
}

/** 切换 Journal / 重载都会重建编辑器，就绪后重新读一次排版状态。 */
watch(ready, (value) => {
  if (value) refreshFormatState()
})

/**
 * 先用 AI 按语义分段，再把结果交给排版。
 *
 * 任何失败（AI 关闭、超时、回复对不上原文）都返回 undefined，
 * 由 applyDocFormat 回落到规则分段——排版本身不能被 AI 卡住。
 */
async function requestSegmentOverrides(store: any): Promise<Record<string, string[]> | undefined> {
  if (!canUseAI.value) return undefined
  const targets = collectAiSegmentTargets(store?.root)
  if (targets.length === 0) return undefined

  const controller = new AbortController()
  const timer = window.setTimeout(() => controller.abort(), 30000)
  try {
    const segments = await requestAISegments(
      targets.map((target) => target.text),
      {
        itemId: currentItemId.value,
        agentId: aiPrefs.value.defaultAgentId || undefined,
        signal: controller.signal,
      },
    )
    if (!segments) return undefined
    const overrides: Record<string, string[]> = {}
    segments.forEach((chunks, index) => {
      if (chunks?.length) overrides[targets[index].id] = chunks
    })
    return Object.keys(overrides).length > 0 ? overrides : undefined
  } catch {
    // 超时/网络/模型错误：静默回落规则分段，只在结果里体现
    return undefined
  } finally {
    window.clearTimeout(timer)
  }
}

async function onFormatDoc() {
  const handle = session.editor.value
  if (!handle?.store || formatting.value) return
  formatting.value = true
  try {
    const segments = await requestSegmentOverrides(handle.store)
    // AI 等待期间可能切换了 Journal/重建了编辑器：旧实例上不能再写
    if (!ready.value || session.editor.value !== handle) return
    const outcome = applyDocFormat(
      handle.store,
      handle.doc?.spaceDoc,
      segments ? { segments } : undefined,
    )
    refreshFormatState()
    if (outcome.reason === 'readonly') {
      MessagePlugin.warning(t('workbench.editor.formatDoc.readonly'))
      return
    }
    if (outcome.changed === 0) {
      MessagePlugin.info(t('workbench.editor.formatDoc.nothing'))
      return
    }
    MessagePlugin.success(t('workbench.editor.formatDoc.applied', { count: outcome.changed }))
  } finally {
    formatting.value = false
  }
}

function onRevertFormat() {
  const handle = session.editor.value
  if (!handle?.store) return
  const outcome = revertDocFormat(handle.store, handle.doc?.spaceDoc)
  refreshFormatState()
  if (outcome.reason === 'readonly') {
    MessagePlugin.warning(t('workbench.editor.formatDoc.readonly'))
    return
  }
  if (outcome.reason === 'no-state') return
  MessagePlugin.success(t('workbench.editor.formatDoc.reverted'))
}

// ---- 空白页起始区与右侧面板（参考 AFFiNE 空白文档） ----
const { aiPrefs } = useWorkbenchPrefs()
const templateEnabled = computed(() => store.features.templates)
/** AI 同时受个人偏好与发布门禁控制 */
const canUseAI = computed(() => aiPrefs.value.enabled && store.features.ai)

/** 右侧面板当前标签：日历 / AI */
const sideTab = ref<'calendar' | 'ai'>('calendar')
/** 右侧抽屉是否展开：默认展开日历，收起后把宽度还给编辑区 */
const sideOpen = ref(true)
const aiLayer = ref<InstanceType<typeof PageAILayer> | null>(null)
/** AI 面板的 Teleport 容器：始终在 DOM 中，保证切换时不会丢挂载点 */
const aiHostRef = ref<HTMLElement | null>(null)

const pickerOpen = ref(false)
const infoOpen = ref(false)
const backlinksOpen = ref(false)

// ---- 画布文档卡片详情（右侧抽屉） ----
const noteDrawerOpen = ref(false)
/** 打开时快照一次正文投影，避免抽屉开着时内容变化导致预览跳动 */
const noteDetailMarkdown = ref('')
const noteDetailTitle = computed(() => currentEntry.value?.title ?? '')

/** 画布卡片标题栏点击 → 打开只读详情抽屉（由 blocksuite 的卡片配置派发事件）。 */
function openNoteDetail() {
  noteDetailMarkdown.value = session.markdown()
  noteDrawerOpen.value = true
}

/** 画布卡片标题栏与日记标题保持一致（两份数据源，改标题后立即同步）。 */
watch(
  noteDetailTitle,
  (value) => {
    noteCardTitle.value = value
  },
  { immediate: true },
)

// ---- 智能关联（P3）：与条目页画布完全同一套逻辑 ----
// 三份状态各自独立：落盘卡片与忽略历史（Y.Doc）、frame 实时位置、视图矩阵。
const relationsOpen = ref(false)
const relationState = shallowRef<RelationsState | null>(null)
const relationRects = shallowRef<Map<string, RelationRect>>(new Map())
const relationView = shallowRef<CanvasViewState | null>(null)
/** 自动扫描到的待处理条数：只做角标提示，绝不自动写画布 */
const relationBadge = ref(0)
/** 当前编辑器的内容/视口订阅取消函数；编辑器重建时重绑 */
let stopRelationWatch: (() => void) | null = null

/** 只在无限画布、有 AI 时出现入口（日记条目不会是数据库） */
const canShowRelations = computed(
  () => mode.value === 'edgeless' && canUseAI.value && ready.value,
)

/** 覆盖层只在画布形态渲染：Page 形态没有坐标可用。 */
const showRelationLayer = computed(
  () => mode.value === 'edgeless' && (relationState.value?.cards.length ?? 0) > 0,
)

/** 已落盘卡片对应的条目 id：召回时排除，避免推荐重复。 */
const appliedRelationIds = computed(
  () => relationState.value?.cards.map((card: RelationCardState) => card.itemId) ?? [],
)

/** 重读落盘状态与 frame 位置，并清理已被用户删掉 frame 的失效记录。 */
function refreshRelationState() {
  const handle = session.editor.value
  if (!handle) return
  const state = handle.readRelations()
  const rects = handle.readRelationRects(state?.cards ?? [])
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

/** 一次性修复早期占位锚点布局：每个编辑器实例最多跑一次。 */
let relationsMigrated = false

function migrateLegacyRelations() {
  const handle = session.editor.value
  const state = relationState.value
  if (!handle || !state || relationsMigrated) return
  if (!state.anchorFrameId || !state.cards.length) return
  relationsMigrated = true
  const outcome = handle.applyRelations({ prevAnchorId: state.anchorFrameId, existing: state.cards, relations: [] })
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

/** 点开关联卡片：跳到目标条目，并把「要定位的那句原文」带在 query 上。 */
function openRelatedItem(card: RelationCardState) {
  if (!card.itemId) return
  const query = card.targetQuote ? { locate: card.targetQuote } : {}
  void router.push({ name: 'workbenchItem', params: { itemId: card.itemId }, query })
}

/** 打开画布后延迟自动发现一次（5s），只亮角标、不落盘。 */
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
      item_id: currentItemId.value,
      agent_id: aiPrefs.value.defaultAgentId || undefined,
      exclude_item_ids: exclude,
    })
    relationBadge.value = result.relations.filter((relation) => relation.strength === 'strong').length
  } catch {
    relationBadge.value = 0
  }
}

// ---- 识别事项：AI 识别（不可用时降级为正文待办规则路径） ----
const collectOpen = ref(false)
const collectCandidates = ref<ItemCandidate[]>([])
const collectLoading = ref(false)
const collectOrigin = ref<ItemCandidateOrigin>('ai')

async function openCollect() {
  collectOrigin.value = canUseAI.value ? 'ai' : 'todo'
  collectCandidates.value = []
  collectOpen.value = true
  const markdown = session.markdown()
  if (canUseAI.value && markdown.trim()) {
    collectLoading.value = true
    try {
      const result = await requestItemCandidates({
        item_id: currentItemId.value,
        markdown,
        agent_id: aiPrefs.value.defaultAgentId || undefined,
        source_title: noteDetailTitle.value,
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

async function onCollectConfirm(picked: ItemCandidate[]) {
  if (picked.length === 0) return
  collectOpen.value = false
  try {
    await session.flush()
    const things = await store.ensureThingsItem()
    store.setPendingCollect({
      itemId: things.id,
      candidates: picked,
      origin: collectOrigin.value,
      sourceTitle: noteDetailTitle.value,
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
 * 当前编辑区是否真的为空。
 *
 * 不能只看 snapshot_version：空白文档只要保存过一次版本号就 > 0，
 * 但正文和标题仍然是空的，此时仍应显示起始区。
 */
function isEditorBlank(): boolean {
  const editorStore = session.editor.value?.store
  if (!editorStore) return false
  const title = editorStore.root?.props?.title?.toString?.() ?? ''
  if (title.trim()) return false
  return session.markdown().trim() === ''
}

/** saveState / ready / 切换条目都会带来内容变化，作为空白判定的重算信号 */
const blankDoc = computed(() => {
  void saveState.value
  void ready.value
  void currentItemId.value
  void mode.value
  return ready.value && Boolean(currentEntry.value) && isEditorBlank()
})

/** 只有「空白 + 文档形态 + 未在选模板」才显示起始区 */
const showBlankStart = computed(() => blankDoc.value && mode.value === 'page' && !pickerOpen.value)

/** 信息区的更新时间：优先本次落盘时间，其次条目 updated_at */
const updatedLabel = computed(() => {
  const stamp =
    session.savedAt.value ??
    (currentEntry.value?.updated_at ? Date.parse(currentEntry.value.updated_at) : Number.NaN)
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
  const day = sameDay
    ? ''
    : `${t('workbench.journals.dateLabel', { month: at.getMonth() + 1, day: at.getDate() })} `
  return `${day}${hh}:${mm}`
})

const wordCount = computed(() => {
  // 正文变化只通过保存状态体现：显式读取作为依赖，避免首次渲染后不再刷新。
  void saveState.value
  void ready.value
  return session.markdownCount()
})

const aiAdapterOptions: PageAIAdapterOptions = {
  handle: () => session.editor.value,
  itemId: () => currentItemId.value,
  host: () => hostRef.value,
}
const aiAdapter = () => (canUseAI.value ? createPageAIAdapter(aiAdapterOptions) : null)

/** 右侧标签切换：AI 面板停靠到抽屉，切换时同步开合 */
function selectSide(tab: 'calendar' | 'ai') {
  if (tab === 'ai' && (!canUseAI.value || !currentEntry.value)) return
  sideOpen.value = true
  if (sideTab.value === tab) {
    if (tab === 'ai') void nextTick(() => aiLayer.value?.openPanel())
    return
  }
  sideTab.value = tab
  if (tab === 'ai') {
    void nextTick(() => aiLayer.value?.openPanel())
    return
  }
  aiLayer.value?.closePanel()
}

/** 顶栏按钮：开合右侧抽屉；打开时沿用上次标签 */
function toggleSideDrawer() {
  if (sideOpen.value) {
    closeSideDrawer()
    return
  }
  sideOpen.value = true
  if (sideTab.value === 'ai' && canUseAI.value && currentEntry.value) {
    void nextTick(() => aiLayer.value?.openPanel())
    return
  }
  sideTab.value = 'calendar'
  aiLayer.value?.closePanel()
}

/** 收起抽屉：同时关掉 AI 面板，避免下次打开时状态残留 */
function closeSideDrawer() {
  sideOpen.value = false
  if (sideTab.value === 'ai') sideTab.value = 'calendar'
  aiLayer.value?.closePanel()
}

/** 空白页「以 AI」：直接打开右侧抽屉的 AI 标签 */
function openAiPanel() {
  if (!canUseAI.value || !currentEntry.value) return
  sideOpen.value = true
  sideTab.value = 'ai'
  void nextTick(() => aiLayer.value?.openPanel())
}

/**
 * AI 面板开合与右侧标签同步。
 *
 * 面板也可能被编辑区内的浮动工具条 / 问答命令打开，此时标签要跟着切到 AI，
 * 否则日历和 AI 会同时占住右侧；关闭时再回到日历。
 */
function onAiPanelChange(open: boolean) {
  if (open) {
    // 浮动工具条 / 问答命令打开 AI 时，抽屉也要跟着展开
    sideOpen.value = true
    sideTab.value = 'ai'
    return
  }
  if (sideTab.value === 'ai') sideTab.value = 'calendar'
}

/**
 * 标签先于编辑器就绪时（点击「AI」时还在挂载），
 * 等 PageAILayer 真正渲染出来再打开侧栏，避免标签亮着但面板没开。
 * 抽屉收起时不主动打开 AI，关闭动作由 closeSideDrawer 统一处理。
 */
watch(
  [sideTab, sideOpen, ready, currentItemId],
  () => {
    if (!sideOpen.value || sideTab.value !== 'ai') return
    void nextTick(() => aiLayer.value?.openPanel())
  },
  { flush: 'post' },
)

/** 空白页「模板」：只对仍为空的文档开放 */
function openTemplatePicker() {
  if (!templateEnabled.value || !showBlankStart.value) return
  pickerOpen.value = true
}

function closeTemplatePicker() {
  pickerOpen.value = false
}

/**
 * 套用模板：与条目页同一条路径（替换资产块 → 用快照建 session）。
 * 模板套用后文档不再为空，起始区立即收起。
 */
async function onTemplateApply(snapshot: PreparedTemplateSnapshot, card: TemplateCardItem) {
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
  if (currentEntry.value) {
    currentEntry.value = {
      ...currentEntry.value,
      snapshot_version: Math.max(1, currentEntry.value.snapshot_version),
    }
  }
  const name = card.nameKey ? t(card.nameKey) : card.name
  MessagePlugin.success(t('workbench.templates.applied', { name }))
}

/** 保存状态文案与条目页保持同一套（§9.7）：草稿暂存不等于归档。 */
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

const modeOptions = [
  { value: 'page' as const, icon: 'file', labelKey: 'workbench.journals.mode.page' },
  { value: 'edgeless' as const, icon: 'artboard', labelKey: 'workbench.journals.mode.edgeless' },
] as const

const grouped = computed(() => groupByDate(store.journals))
const dayEntries = computed(() => grouped.value.get(selectedDate.value) ?? [])

function dateLabel(date: string): string {
  return t('workbench.journals.dateLabel', {
    month: Number(date.slice(5, 7)),
    day: Number(date.slice(8, 10)),
  })
}

/** 拉当月月历；失败只提示一次，面板保持上一次数据。 */
async function loadMonth(month: string) {
  try {
    await store.fetchJournals(month)
  } catch {
    MessagePlugin.error(t('workbench.list.loadFailed'))
  }
}

/**
 * 切换选中日（§9.13.1）。
 *
 * 先收敛草稿：保存失败或冲突就留在原日期，避免把未落盘的改动带丢。
 * 再用 replace 更新路由，避免在浏览器历史里堆出一串日期。
 */
async function selectDate(date: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return
  if (date === selectedDate.value && currentItemId.value) return
  if (!(await convergeDraft())) return
  selectedDate.value = date
  if (!date.startsWith(store.journalsMonth)) {
    await loadMonth(date.slice(0, 7))
  }
  await resolveDay(date)
}

/**
 * 收敛当前草稿：flush 后仍处于冲突/失败则中止导航。
 *
 * 只有可靠落盘的改动才允许切换日期或条目（§9.7、§9.13.1）。
 */
async function convergeDraft(): Promise<boolean> {
  if (!currentItemId.value) return true
  try {
    await session.flush()
  } catch {
    MessagePlugin.warning(t('workbench.journals.flushFailed'))
    return false
  }
  if (!session.canLeave()) {
    MessagePlugin.warning(t('workbench.journals.flushFailed'))
    return false
  }
  return true
}

/** 选中日确定后决定打开哪一篇：0 条空态、1 条直开、多条不擅自选（§9.13.4）。 */
async function resolveDay(date: string) {
  const rows = groupByDate(store.journals).get(date) ?? []
  const keep = rows.find((row) => row.id === currentItemId.value)
  // ?itemId= 恢复时正文可能还没挂载：命中条目但无 currentEntry 仍需加载一次。
  if (keep && currentEntry.value) return
  if (keep) {
    await openEntry(keep.id)
    return
  }
  if (rows.length === 1) {
    await openEntry(rows[0].id)
    return
  }
  currentItemId.value = ''
  currentEntry.value = null
  session.destroy()
  if (sideTab.value === 'ai') sideTab.value = 'calendar'
  await syncQuery()
}

async function openEntry(id: string) {
  if (id === currentItemId.value && currentEntry.value) return
  if (!(await convergeDraft())) return
  const entry = store.journals.find((row) => row.id === id)
  if (entry) selectedDate.value = entry.journal_date
  currentItemId.value = id
  loadError.value = false
  try {
    const item = await getWorkbenchItem(id)
    // 详情接口不填归档状态：月历批量字段才是权威口径（§8.3），这里合并进来，
    // 避免已归档的 Journal 顶栏误显示“未归档”。
    currentEntry.value = entry
      ? {
          ...item,
          archive_status: entry.archive_status,
          archived_version: entry.archived_version,
        }
      : item
    mode.value = ((entry?.current_mode ?? item.current_mode) ?? 'page') as EditorMode
    session.destroy()
    await nextTick()
    await session.mount()
  } catch (error) {
    loadError.value = workbenchErrorCode(error) !== WORKBENCH_ERROR.notFound
    currentEntry.value = null
  }
  await syncQuery()
}

/** 形态切换只重建视图层，不动 Y.Doc，也不产生新版本（§7.10）。 */
async function switchMode(next: EditorMode) {
  if (mode.value === next || !currentEntry.value) return
  mode.value = next
  session.editor.value?.setMode(next)
  try {
    const updated = await updateWorkbenchItem(currentItemId.value, { current_mode: next })
    currentEntry.value = updated
    const entry = store.journals.find((row) => row.id === updated.id)
    if (entry) entry.current_mode = next
  } catch {
    // 形态写回失败只影响下次打开的默认形态，不阻断当前编辑。
  }
}

async function createJournal() {
  try {
    const item = await store.createJournal(selectedDate.value)
    await loadMonth(selectedDate.value.slice(0, 7))
    await openEntry(item.id)
  } catch {
    MessagePlugin.error(t('workbench.journals.createFailed'))
  }
}

async function stepWeek(delta: number) {
  await selectDate(addDays(selectedDate.value, delta))
}

async function stepMonth(delta: number) {
  // 只翻月历，不切选中日与正文
  await loadMonth(shiftMonth(store.journalsMonth, delta))
}

/**
 * 拖拽改期：乐观更新 → 失败真回滚并 toast（§9.13.5）。
 * 只写 workbench_items.journal_date，不碰 Y.Doc 与归档。
 */
async function onMoveEntry(payload: { id: string; date: string }) {
  const entry = store.journals.find((row) => row.id === payload.id)
  if (!entry || entry.journal_date === payload.date) return
  const previous = entry.journal_date
  entry.journal_date = payload.date
  try {
    await store.setJournalDate(payload.id, payload.date)
    if (payload.id === currentItemId.value) selectedDate.value = payload.date
    // 拖到别的月份时当前月数据已过期，重拉一次保持月格与列表一致
    if (payload.date.slice(0, 7) !== store.journalsMonth) {
      await loadMonth(store.journalsMonth)
    }
  } catch {
    entry.journal_date = previous
    MessagePlugin.error(t('workbench.journals.moveFailed'))
  }
}

/**
 * 日历列表删除：移入回收站（不删已归档知识）。
 *
 * 删除当前打开的 Journal 前先 flush，避免保存队列在删除后又把内容写回；
 * 删除后从本地月历移除并重拉当月，若删的是当前条目则按当天剩余条目重新收敛。
 */
function onDeleteEntry(id: string) {
  const entry = store.journals.find((row) => row.id === id)
  if (!entry) return
  const dialog = DialogPlugin.confirm({
    header: t('workbench.journals.deleteTitle'),
    body: t('workbench.journals.deleteBody', { title: entry.title }),
    theme: 'warning',
    confirmBtn: t('workbench.journals.deleteConfirm'),
    onConfirm: async () => {
      const isCurrent = currentItemId.value === id
      if (isCurrent) {
        // 删除前尽量把草稿落盘；失败也不阻塞删除（条目即将进入回收站）
        try {
          await session.flush()
        } catch {
          // 忽略：删除后正文不再需要保存
        }
      }
      try {
        await store.trashItem(id)
        // 月历与当天列表都读 store.journals，先本地移除避免闪一下旧数据
        store.journals = store.journals.filter((row) => row.id !== id)
        if (isCurrent) {
          currentItemId.value = ''
          currentEntry.value = null
          session.destroy()
          await resolveDay(selectedDate.value)
        }
        void loadMonth(store.journalsMonth).catch(() => undefined)
        MessagePlugin.success(t('workbench.journals.deleted'))
        dialog.hide()
      } catch {
        MessagePlugin.error(t('workbench.journals.deleteFailed'))
      }
    },
  })
}

async function onArchiveAction(action: ArchiveBadgeAction) {
  if (!currentEntry.value || action === 'none' || action === 'progress') return
  const markdown = session.markdown()
  if (!markdown.trim()) {
    MessagePlugin.warning(t('workbench.archive.status.none'))
    return
  }
  const payload = {
    source_type: 'item' as const,
    source_id: currentItemId.value,
    base_version: baseVersion.value,
    markdown,
  }
  try {
    if (action === 'create' || action === 'recreate') await createArchive(payload)
    else await refreshArchive(payload)
    // 归档后乐观刷新当天条目，再重拉当前月一次（§9.13.2）
    await loadMonth(store.journalsMonth)
    const rows = groupByDate(store.journals).get(selectedDate.value) ?? []
    const refreshed = rows.find((row) => row.id === currentItemId.value)
    if (refreshed && currentEntry.value) {
      currentEntry.value = {
        ...currentEntry.value,
        archive_status: refreshed.archive_status,
        archived_version: refreshed.archived_version,
      }
    }
    MessagePlugin.success(t('workbench.archive.action.create'))
  } catch (error) {
    if (workbenchErrorCode(error) === WORKBENCH_ERROR.archiveStale) {
      MessagePlugin.warning(t('workbench.archive.status.stale'))
      return
    }
    MessagePlugin.error(t('workbench.editor.saveError'))
  }
}

function openConflictDialog() {
  const dialog = DialogPlugin({
    header: t('workbench.editor.saveDraftConflict'),
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
  })
}

async function reloadDay() {
  loadError.value = false
  await loadMonth(store.journalsMonth)
  if (currentItemId.value) await openEntry(currentItemId.value)
}

/** 路由同步：稳定 ID 写进 ?itemId=，刷新后能恢复同一条（§9.13.1）。 */
async function syncQuery() {
  const query: Record<string, string> = {}
  if (currentItemId.value) query.itemId = currentItemId.value
  await router.replace({
    name: 'workbenchJournalDay',
    params: { date: selectedDate.value },
    query,
  })
}

function goBack() {
  void router.push({ name: 'workbenchRecent' })
}

watch(
  () => props.date,
  (next) => {
    if (!next || next === selectedDate.value) return
    void selectDate(next)
  },
)

/**
 * 关联覆盖层的三路订阅（与条目页一致）。
 *
 * 编辑器重建（切换条目/版本恢复/冲突重载/切模板）后旧 handle 已失效，
 * 必须重新绑定，否则覆盖层停在旧视图上。
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

    refreshRelationState()
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

/** 切回无限画布时补一次刷新：Page 形态下 frame 位置可能已被改过。 */
watch(
  () => mode.value,
  (next) => {
    if (next !== 'edgeless') {
      relationRects.value = new Map()
      return
    }
    refreshRelationState()
    scheduleRelationScan()
  },
  { flush: 'post' },
)

onMounted(() => {
  window.addEventListener(WB_NOTE_DETAIL_EVENT, openNoteDetail)
})

onBeforeUnmount(() => {
  window.removeEventListener(WB_NOTE_DETAIL_EVENT, openNoteDetail)
  if (relationTimer) {
    clearTimeout(relationTimer)
    relationTimer = null
  }
  stopRelationWatch?.()
  stopRelationWatch = null
  noteCardTitle.value = ''
  void session.flush()
  session.destroy()
})

// 首屏：先拉当前月，再按路由选中的日期收敛（月与日各一次请求）。
void (async () => {
  const month = selectedDate.value.slice(0, 7) || currentMonth()
  await loadMonth(month)
  await resolveDay(selectedDate.value)
})()
</script>

<style lang="less" scoped>
.journals {
  flex: 1;
  min-height: 0;
  display: flex;
  position: relative;
  background: var(--td-bg-color-page);
}

.journals-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
}

.j-head {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-1) var(--app-space-4);
  border-bottom: 1px solid var(--td-component-stroke);
}

.j-crumb {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);

  .sep {
    opacity: 0.6;
  }
}

.j-title {
  max-width: 230px;
  overflow: hidden;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.j-save-state {
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

.mode-switch {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xs);

  button {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 2px var(--app-space-2);
    border-radius: var(--app-radius-xs);
    font-size: var(--app-text-2xs);
    color: var(--td-text-color-secondary);

    &.is-active {
      background: var(--td-brand-color-1);
      color: var(--td-brand-color);
    }

    &:disabled {
      opacity: 0.5;
    }
  }
}

.j-editor {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

/* 常驻格式工具条占位高度：编辑器、空白页起始区、AI 条都据此下移 */
.j-editor.has-format-bar {
  --workbench-format-bar-height: 52px;
}

/* 预留的顶部带子画成真实顶栏，工具条不再像浮在正文上的气泡 */
.j-editor.has-format-bar::before {
  content: '';
  position: absolute;
  inset: 0 0 auto 0;
  height: var(--workbench-format-bar-height);
  background: var(--td-bg-color-container);
  border-bottom: 1px solid var(--td-component-stroke);
  pointer-events: none;
}

/* 一键排版后的阅读版式：与条目页同一组变量，回退时移除类名即复原 */
.j-editor.is-typeset {
  --affine-font-h-1: 1.6em;
  --affine-font-h-2: 1.35em;
  --affine-font-h-3: 1.15em;
  --affine-font-h-4: 1.05em;
  --affine-font-h-5: 1em;
  --affine-font-h-6: 0.95em;
  --affine-line-height: 1.75;
}

.j-mount {
  position: absolute;
  inset: 0;
  top: var(--workbench-format-bar-height, 0px);
  overflow: hidden;

  &.is-hidden {
    visibility: hidden;
  }
}

/* 与条目页一致：只有文档页面模式由挂载点滚动，无限画布保持 hidden */
.j-mount.affine-page-viewport {
  overflow-x: hidden;
  overflow-y: auto;
}

/* 顶栏「日历 / AI」开关：默认选中（浅绿底 + 品牌色），取消选中恢复普通文字按钮 */
.side-drawer-entry {
  border-radius: var(--app-radius-xs);

  &.is-open {
    background: var(--td-brand-color-1);
    color: var(--td-brand-color);
  }
}

/* ===== 右侧栏：顶部切换标签 + 关闭按钮 + 日历 / AI 面板 ===== */
.j-side {
  flex: none;
  width: 340px;
  max-width: 92vw;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-left: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  transition: width var(--app-motion-base) ease;

  /* 收起：宽度归零并移出布局，编辑区 / 顶栏 / 星期条重新铺满（保留 DOM 以免丢 AI 挂载点） */
  &:not(.is-open) {
    width: 0;
    border-left-width: 0;
    visibility: hidden;
    transition: width var(--app-motion-base) ease, visibility 0s linear var(--app-motion-base);
  }
}

.j-side-head {
  flex: none;
  min-width: 340px;
  display: flex;
  align-items: center;
  border-bottom: 1px solid var(--td-component-stroke);
}

.j-side-tabs {
  flex: 1;
  min-width: 0;
  display: flex;
  gap: 4px;
  padding: var(--app-space-2);
}

.j-side-close {
  flex: none;
  width: 28px;
  height: 28px;
  margin-right: var(--app-space-2);
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-secondary);

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.j-side-tab {
  flex: 1;
  height: 30px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  border-radius: var(--app-radius-sm);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &.is-active {
    background: var(--td-brand-color-1);
    color: var(--td-brand-color);
  }

  &:disabled {
    cursor: default;
    opacity: 0.45;
  }
}

.j-side-body {
  flex: 1;
  min-width: 340px;
  min-height: 0;
  display: flex;
}

/* AI 面板由 PageAILayer Teleport 进来，铺满右侧栏剩余高度 */
.j-ai-host {
  flex: 1;
  min-height: 0;
  display: flex;
}

/* ===== 空白页起始区（信息 / 开始 / 双向链接，贴在编辑区底部） ===== */
.j-blank {
  position: absolute;
  /* 顶部让出常驻格式工具条，起始区不再被压住 */
  inset: var(--workbench-format-bar-height, 0px) 0 0 0;
  z-index: 4;
  display: flex;
  flex-direction: column;
  pointer-events: none;
}

.jb-inner {
  width: min(var(--affine-editor-width, 720px), calc(100% - 48px));
  margin: 0 auto;
  padding: 0 24px;
  pointer-events: none;
}

.jb-body {
  /* 信息 / 开始 / 双向链接整体贴底，顶部留给正文书写 */
  margin-top: auto;
  margin-bottom: 24px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.jb-info {
  border-bottom: 1px solid var(--td-component-stroke);
  padding-bottom: 4px;
}

.jb-info-head {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 2px 0;
  font-size: var(--app-text-sm);
  font-weight: 600;
  color: var(--td-text-color-primary);
  cursor: pointer;
  pointer-events: auto;
}

.jb-info-body {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 0 4px;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.jb-private {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.jb-dot {
  color: var(--td-component-stroke);
}

.jb-start {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 0;
  border-bottom: 1px solid var(--td-component-stroke);
}

.jb-start-label {
  margin-right: 4px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
}

.jb-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 26px;
  padding: 0 10px;
  border-radius: var(--td-radius-pill, 999px);
  background: var(--td-bg-color-secondarycontainer);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-primary);
  cursor: pointer;
  pointer-events: auto;

  :deep(.t-icon) {
    color: var(--td-brand-color);
  }

  &:hover {
    background: var(--td-bg-color-secondarycontainer-hover);
  }
}

.jb-links {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
  border-bottom: 1px solid var(--td-component-stroke);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
}

.jb-show {
  height: 24px;
  padding: 0 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-pill, 999px);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  pointer-events: auto;

  &:hover {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
  }
}

.j-loading {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}

.j-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-3);
  padding: var(--app-space-6);
  text-align: center;
}

.je-icon {
  width: 54px;
  height: 54px;
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-lg);
  background: var(--td-brand-color-1);
  color: var(--td-brand-color);
}

.je-date {
  margin: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.j-empty h2 {
  margin: 0;
  font-size: var(--app-text-xl);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.je-hint {
  margin: 0 0 var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.je-choice {
  max-width: 320px;
}

.j-error {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-3);
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
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

/**
 * 无限画布里的文档卡片样式（与条目页画布保持一致）。
 *
 * 只命中页面级 note（blocksuite.ts 打的 wb-doc-note 标记），
 * 画布上新建的便签继续沿用各自的笔记样式。
 */
.j-editor :deep(affine-edgeless-note.wb-doc-note:not(:has(.edgeless-note-container[data-editing='true'])) edgeless-note-background) {
  box-shadow:
    0 0 0 1px color-mix(in srgb, var(--td-text-color-primary) 11%, transparent),
    0 1px 2px rgba(16, 24, 40, 0.05),
    0 16px 36px -20px rgba(16, 24, 40, 0.3) !important;
  transition: box-shadow 0.18s ease;
}

.j-editor :deep(affine-edgeless-note.wb-doc-note:hover:not(:has(.edgeless-note-container[data-editing='true'])) edgeless-note-background) {
  box-shadow:
    0 0 0 1px color-mix(in srgb, var(--td-text-color-primary) 22%, transparent),
    0 2px 4px rgba(16, 24, 40, 0.07),
    0 22px 44px -22px rgba(16, 24, 40, 0.38) !important;
}

/* 卡片标题栏：图标 + 标题 + 详情入口；必须抬到 note 遮罩之上才可点击 */
.j-editor :deep(affine-edgeless-note.wb-doc-note edgeless-page-block-title) {
  position: relative;
  z-index: 2;
  display: block;
  margin: 0 0 12px;
  padding: 0 0 10px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.j-editor :deep(affine-edgeless-note.wb-doc-note .wb-note-head) {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  user-select: none;
}

.j-editor :deep(affine-edgeless-note.wb-doc-note .wb-note-head-icon) {
  display: inline-flex;
  flex: none;
  color: var(--td-text-color-placeholder);
}

.j-editor :deep(affine-edgeless-note.wb-doc-note .wb-note-head-title) {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: var(--app-text-sm);
  font-weight: 600;
  color: var(--td-text-color-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.j-editor :deep(affine-edgeless-note.wb-doc-note .wb-note-head-detail) {
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
</style>
