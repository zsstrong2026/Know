<template>
  <div class="sop-board">
    <!-- 统计带：总额 + 五态格（可点击筛选，再点取消） -->
    <div class="sop-band">
      <div class="sop-band-left">
        <div class="sop-band-ico">{{ (store.currentProject?.code || '··').slice(0, 2) }}</div>
        <div class="sop-band-main">
          <div class="sop-band-label">{{ $t('sop.items.currentProject') }}</div>
          <div class="sop-band-name">{{ store.currentProject?.name || $t('sop.project.none') }}</div>
          <div class="sop-band-desc">{{ store.currentProject?.description || $t('sop.settings.noDesc') }}</div>
        </div>
      </div>

      <div class="sop-band-stats">
        <button
          type="button"
          class="sop-pstat"
          :class="{ on: !filter.exec }"
          :title="$t('sop.items.statAllHint')"
          @click="pickExec('')"
        >
          <span class="n">{{ stats.total }}</span>
          <span class="l">{{ $t('sop.items.statTotal') }}</span>
        </button>
        <button
          v-for="state in EXEC_STATES"
          :key="state"
          type="button"
          class="sop-pstat"
          :class="[`is-${state}`, { on: filter.exec === state }]"
          :title="$t('sop.items.statCellHint', { label: EXEC_STATE_LABELS[state] })"
          @click="pickExec(state)"
        >
          <span class="n">{{ stats[state] }}</span>
          <span class="l">{{ EXEC_STATE_LABELS[state] }}</span>
        </button>
        <div class="sop-pstat sop-pstat--meta">
          <span class="n n--sm">{{ kbName }}</span>
          <span class="l">{{ $t('sop.settings.statKb') }}</span>
          <span class="sub">{{ $t('sop.items.defaultSop', { name: defaultTemplateName }) }}</span>
        </div>
      </div>

      <div class="sop-band-actions">
        <t-button size="small" variant="outline" @click="briefPanelVisible = true">
          {{ $t('sop.split.panel') }}
        </t-button>
        <t-button size="small" variant="outline" @click="openSettings">
          ⚙ {{ $t('sop.settings.projectTitle') }}
        </t-button>
        <t-button size="small" variant="outline" :loading="refreshing" @click="refreshAll">
          ⟳
        </t-button>
        <t-button size="small" theme="primary" @click="createVisible = true">
          ＋ {{ $t('sop.items.newItem') }}
        </t-button>
      </div>
    </div>

    <!-- 检索区：条件写入 store.itemFilter，轮询刷新不会重挂载这些控件 -->
    <div class="sop-filter-row">
      <input
        v-model="search"
        class="sop-search"
        type="text"
        :placeholder="$t('sop.items.searchPlaceholder')"
        @input="onSearchInput"
      />
      <t-select
        :model-value="filter.status || ''"
        class="sop-flt"
        :options="statusOptions"
        @update:model-value="(v: any) => setFilter('status', v)"
      />
      <t-select
        :model-value="filter.owner || ''"
        class="sop-flt"
        :options="ownerOptions"
        @update:model-value="(v: any) => setFilter('owner', v)"
      />
      <SopExecFilter
        :model-value="(filter.exec as ExecState) || ''"
        :counts="stats"
        :total="stats.total"
        @update:model-value="pickExec"
      />
      <t-select
        :model-value="filter.source || ''"
        class="sop-flt"
        :options="sourceOptions"
        @update:model-value="(v: any) => setFilter('source', v)"
      />
      <t-select
        :model-value="filter.kind || ''"
        class="sop-flt"
        :options="kindOptions"
        @update:model-value="(v: any) => setFilter('kind', v)"
      />
      <div class="sop-seg">
        <button type="button" :class="{ on: viewMode === 'list' }" @click="viewMode = 'list'">
          {{ $t('sop.items.viewList') }}
        </button>
        <button type="button" :class="{ on: viewMode === 'kanban' }" @click="viewMode = 'kanban'">
          {{ $t('sop.items.viewKanban') }}
        </button>
      </div>
    </div>

    <div v-if="selected.size" class="sop-bulk">
      <b>{{ $t('sop.items.selected', { n: selected.size }) }}</b>
      <t-button size="small" theme="primary" :loading="bulkBusy" @click="bulkLaunch">
        {{ $t('sop.items.bulkLaunch') }}
      </t-button>
      <t-button size="small" :loading="bulkBusy" @click="bulkCloseItems">
        {{ $t('sop.items.bulkClose') }}
      </t-button>
      <t-button size="small" variant="text" @click="clearSelection">
        {{ $t('sop.items.bulkCancel') }}
      </t-button>
    </div>

    <!-- 看板视图：与列表共享 store.items（同一筛选项），拖拽改状态 -->
    <div v-if="viewMode === 'kanban'" class="sop-board-cols">
      <div
        v-for="column in KANBAN_COLUMNS"
        :key="column"
        class="sop-bcol"
        :class="{ 'is-over': dragOver === column }"
        @dragover.prevent="onDragOver(column)"
        @dragleave="onDragLeave(column)"
        @drop.prevent="onDrop(column)"
      >
        <div class="sop-bcol-head">
          <i :style="{ background: ITEM_STATUS_COLORS[column] }" />
          {{ ITEM_STATUS_LABELS[column] }}
          <span class="cnt">{{ columnItems(column).length }}</span>
        </div>
        <div class="sop-bcol-body">
          <div
            v-for="item in columnItems(column)"
            :key="item.id"
            class="sop-icard"
            :class="{ dragging: draggingId === item.id }"
            draggable="true"
            @dragstart="onDragStart(item)"
            @dragend="onDragEnd"
            @click="openItem(item)"
          >
            <div class="sop-icard-title">{{ item.title }}</div>
            <div class="sop-icard-meta">
              <span class="mono">{{ item.no || '' }}</span>
              <span class="sop-prio" :class="`is-${item.priority || 'P2'}`">{{ item.priority || 'P2' }}</span>
              <span class="sop-kind">{{ KIND_LABELS[item.kind] || item.kind }}</span>
              <span class="sop-src">{{ SOURCE_LABELS[item.source] || item.source }}</span>
            </div>
            <div class="sop-icard-foot">
              <span>{{ item.is_big ? EXEC_STATE_LABELS[item.child_exec || 'none'] : item.current_node || '—' }}</span>
              <span class="mono">
                {{ item.is_big ? childProgressText(item.child_done || 0, item.child_total || 0) : progressText(item.progress_done, item.progress_total) }}
              </span>
            </div>
          </div>
          <div v-if="!columnItems(column).length" class="sop-bcol-empty">
            {{ $t('sop.items.kanbanDropHere') }}
          </div>
        </div>
      </div>
    </div>

    <div v-else class="sop-table-wrap">
      <table class="sop-table">
        <thead>
          <tr>
            <th class="col-check">
              <input type="checkbox" :checked="allSelected" @change="toggleAll" />
            </th>
            <th class="col-no">{{ $t('sop.items.colNo') }}</th>
            <th>{{ $t('sop.items.colTitle') }}</th>
            <th class="col-status">{{ $t('sop.items.colStatus') }}</th>
            <th class="col-source">{{ $t('sop.items.colSource') }}</th>
            <th class="col-owner">{{ $t('sop.items.colOwner') }}</th>
            <th class="col-prio">{{ $t('sop.items.colPriority') }}</th>
            <th class="col-tpl">{{ $t('sop.items.colTemplate') }}</th>
            <th class="col-stage">{{ $t('sop.items.colStage') }}</th>
            <th class="col-progress">{{ $t('sop.items.colProgress') }}</th>
            <th class="col-actions">{{ $t('sop.items.colActions') }}</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="row in visibleRows" :key="row.item.id + (row.child ? ':child' : '')">
          <tr class="sop-row" :class="{ 'sop-row--child': row.child }" @click="openItem(row.item)">
            <td class="col-check" @click.stop>
              <input
                type="checkbox"
                :checked="selected.has(row.item.id)"
                @change="toggleSelect(row.item.id, ($event.target as HTMLInputElement).checked)"
              />
            </td>
            <td class="col-no">{{ row.item.no || '—' }}</td>
            <td>
              <button
                v-if="row.item.is_big && !row.child"
                type="button"
                class="sop-expander"
                :title="expandedParents.has(row.item.id) ? $t('sop.split.collapse') : $t('sop.split.expand')"
                @click.stop="toggleParent(row.item.id)"
              >
                {{ expandedParents.has(row.item.id) ? '▾' : '▸' }}
              </button>
              <span class="sop-title">{{ row.item.title }}</span>
              <span v-if="row.child" class="sop-tag sop-tag--sm">{{ $t('sop.split.childBadge') }}</span>
              <span class="sop-kind">{{ KIND_LABELS[row.item.kind] || row.item.kind }}</span>
            </td>
            <td class="col-status">
              <span class="sop-status">
                <i :style="{ background: ITEM_STATUS_COLORS[row.item.status] || '#c3c8cf' }" />
                {{ ITEM_STATUS_LABELS[row.item.status] || row.item.status }}
              </span>
            </td>
            <td class="col-source">
              <span class="sop-src">{{ SOURCE_LABELS[row.item.source] || row.item.source }}</span>
            </td>
            <td class="col-owner">{{ row.item.owner || '—' }}</td>
            <td class="col-prio">
              <span class="sop-prio" :class="`is-${row.item.priority || 'P2'}`">{{ row.item.priority || 'P2' }}</span>
            </td>
            <td class="col-tpl">
              <span v-if="row.item.is_big">—</span>
              <span v-else-if="templateName(row.item.template_id)">{{ templateName(row.item.template_id) }}</span>
              <span v-else class="sop-muted">{{ $t('sop.items.templateUnbound') }}</span>
            </td>
            <td class="col-stage">
              <span v-if="row.item.is_big" class="sop-child-exec">
                <i
                  v-if="row.item.child_exec"
                  :style="{ background: EXEC_STATE_COLORS[row.item.child_exec] }"
                />
                {{ row.item.child_total ? EXEC_STATE_LABELS[row.item.child_exec || 'none'] : $t('sop.split.emptyBig') }}
              </span>
              <span v-else>{{ row.item.current_node || '—' }}</span>
            </td>
            <td class="col-progress">
              <span v-if="row.item.is_big">{{ childProgressText(row.item.child_done || 0, row.item.child_total || 0) }}</span>
              <span v-else>{{ progressText(row.item.progress_done, row.item.progress_total) }}</span>
            </td>
            <td class="col-actions" @click.stop>
              <t-tooltip v-if="row.item.is_big" :content="$t('sop.split.bigParentCannotLaunch')">
                <t-button size="small" variant="outline" @click="openItem(row.item)">
                  {{ $t('sop.split.addChild') }}
                </t-button>
              </t-tooltip>
              <t-button
                v-else-if="row.item.exec_state === 'intervene'"
                size="small"
                theme="danger"
                variant="outline"
                @click="openLatestRun(row.item)"
              >
                {{ $t('sop.items.actionHandleFailure') }}
              </t-button>
              <t-button
                v-else-if="row.item.exec_state === 'running' || row.item.exec_state === 'pr'"
                size="small"
                variant="outline"
                @click="openLatestRun(row.item)"
              >
                {{ $t('sop.items.actionViewRun') }}
              </t-button>
              <t-button
                v-else
                size="small"
                theme="primary"
                :loading="launchingId === row.item.id"
                @click="launchItem(row.item)"
              >
                {{ $t('sop.items.actionLaunch') }}
              </t-button>
            </td>
          </tr>
          </template>
          <tr v-if="!store.items.length">
            <td colspan="11" class="sop-empty-row">
              <div>{{ $t('sop.items.emptyRow') }}</div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="sop-count">{{ $t('sop.items.count', { n: store.items.length }) }}</div>

    <SopItemDrawer
      :item-id="drawerItemId"
      @close="drawerItemId = null"
      @changed="onDrawerChanged"
      @open="(id: string) => (drawerItemId = id)"
    />

    <SopItemCreateModal
      v-model:visible="createVisible"
      :project-id="store.currentProjectId"
      @created="onItemCreated"
    />

    <SopProjectBriefPanel
      v-if="store.currentProjectId"
      :visible="briefPanelVisible"
      :project-id="store.currentProjectId"
      @close="briefPanelVisible = false"
      @changed="refreshAll"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  bulkClose,
  bulkLaunchItems,
  createRun,
  listRuns,
  setItemStatus,
  type ItemStatus,
  type SopItemWithDerived,
} from '@/api/sop';
import {
  EXEC_STATES,
  EXEC_STATE_LABELS,
  ITEM_STATUS_COLORS,
  ITEM_STATUS_LABELS,
  KANBAN_COLUMNS,
  KIND_LABELS,
  SOURCE_LABELS,
  canTransitItem,
  childProgressText,
  progressText,
} from '@/api/sop/helpers';
import type { ExecState } from '@/api/sop';
import { useSopStore } from '@/stores/sop';
import SopExecFilter from './components/SopExecFilter.vue';
import SopItemDrawer from './SopItemDrawer.vue';
import SopItemCreateModal from './SopItemCreateModal.vue';
import SopProjectBriefPanel from './components/SopProjectBriefPanel.vue';

const router = useRouter();
const { t } = useI18n();
const store = useSopStore();
const { itemStats: stats } = storeToRefs(store);

const search = ref(String(store.itemFilter.q || ''));
const selected = ref(new Set<string>());
// 每条事项最近一次运行的 id（用于「查看运行」按钮）
const latestRuns = ref<Record<string, string>>({});
const viewMode = ref<'list' | 'kanban'>('list');
const drawerItemId = ref<string | null>(null);
const createVisible = ref(false);
const draggingId = ref('');
const dragOver = ref<ItemStatus | ''>('');
const launchingId = ref('');
const bulkBusy = ref(false);
const refreshing = ref(false);
const briefPanelVisible = ref(false);
// 展开的大项（折叠状态只影响列表展示，不影响筛选与统计）
const expandedParents = ref(new Set<string>());
let stopPolling: (() => void) | null = null;
let latestRunsTick: (() => void) | null = null;
let searchTimer: ReturnType<typeof setTimeout> | null = null;

const filter = computed(() => store.itemFilter);

const statusOptions = computed(() => [
  { label: t('sop.items.allStatus'), value: '' },
  ...(Object.keys(ITEM_STATUS_LABELS) as (keyof typeof ITEM_STATUS_LABELS)[]).map((status) => ({
    label: ITEM_STATUS_LABELS[status],
    value: status,
  })),
]);

const uniqueOwners = computed(() => {
  const owners = new Set<string>();
  for (const item of store.items) if (item.owner) owners.add(item.owner);
  return [...owners];
});

const ownerOptions = computed(() => [
  { label: t('sop.items.allOwner'), value: '' },
  ...uniqueOwners.value.map((owner) => ({ label: owner, value: owner })),
]);

const sourceOptions = computed(() => [
  { label: t('sop.items.allSource'), value: '' },
  ...Object.entries(SOURCE_LABELS).map(([value, label]) => ({ label, value })),
]);

const kindOptions = computed(() => [
  { label: t('sop.items.allKind'), value: '' },
  ...Object.entries(KIND_LABELS).map(([value, label]) => ({ label, value })),
]);

const kbName = computed(() => {
  const project = store.currentProject;
  if (!project?.kb_id) return t('sop.settings.kbUnbound');
  return project.kb_id;
});

const defaultTemplateName = computed(() => {
  const id = store.currentProject?.default_template_id;
  if (!id) return t('sop.items.templateUnbound');
  return store.templates.find((tpl) => tpl.id === id)?.name || id;
});

const allSelected = computed(
  () => store.items.length > 0 && store.items.every((item) => selected.value.has(item.id)),
);

/** 大项执行情况色点（与统计带同一套颜色）。 */
const EXEC_STATE_COLORS: Record<string, string> = {
  none: '#c3c8cf',
  running: '#0052d9',
  intervene: '#d54941',
  pr: '#ed7b2f',
  closed: '#2ba471',
};

type BoardRow = { item: SopItemWithDerived; child: boolean };

/**
 * 列表行：大项与其（展开的）子项。
 *
 * 子项可能被当前筛选条件挡在外面（例如 exec=intervene 时只命中某些子项），
 * 这里只用 store.items（已是筛选后的结果）配对 parent_id，不额外请求。
 */
const visibleRows = computed<BoardRow[]>(() => {
  const rows: BoardRow[] = [];
  const byParent = new Map<string, SopItemWithDerived[]>();
  for (const item of store.items) {
    if (!item.parent_id) continue;
    const list = byParent.get(item.parent_id) || [];
    list.push(item);
    byParent.set(item.parent_id, list);
  }
  for (const item of store.items) {
    if (item.parent_id) {
      // 父项不在当前结果里（被筛选掉）时，子项按顶层行展示，否则会出现「孤儿缩进」
      if (!store.items.some((p) => p.id === item.parent_id)) rows.push({ item, child: false });
      continue;
    }
    rows.push({ item, child: false });
    if (item.is_big && expandedParents.value.has(item.id)) {
      for (const kid of byParent.get(item.id) || []) rows.push({ item: kid, child: true });
    }
  }
  return rows;
});

function toggleParent(id: string) {
  const next = new Set(expandedParents.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  expandedParents.value = next;
}

function columnItems(column: ItemStatus): SopItemWithDerived[] {
  return store.items.filter((item) => item.status === column);
}

function onDragStart(item: SopItemWithDerived) {
  draggingId.value = item.id;
}

function onDragEnd() {
  draggingId.value = '';
  dragOver.value = '';
}

function onDragOver(column: ItemStatus) {
  if (draggingId.value) dragOver.value = column;
}

function onDragLeave(column: ItemStatus) {
  if (dragOver.value === column) dragOver.value = '';
}

/**
 * 拖拽改状态：先本地判定状态机（非法直接提示，不动数据），
 * 合法则乐观更新 → setItemStatus → 失败回滚并透出后端错误。
 */
async function onDrop(column: ItemStatus) {
  const id = draggingId.value;
  onDragEnd();
  if (!id) return;
  const item = store.items.find((it) => it.id === id);
  if (!item || item.status === column) return;

  if (!canTransitItem(item.status, column)) {
    MessagePlugin.warning(t('sop.items.kanbanIllegal', {
      from: ITEM_STATUS_LABELS[item.status] || item.status,
      to: ITEM_STATUS_LABELS[column],
    }));
    return;
  }

  const previous = item.status;
  item.status = column;
  try {
    await setItemStatus(item.id, column);
    MessagePlugin.success(t('sop.items.kanbanMoved', {
      title: item.title,
      to: ITEM_STATUS_LABELS[column],
    }));
    await Promise.all([store.loadItems(), store.loadStats()]);
  } catch (error: any) {
    item.status = previous;
    MessagePlugin.error(error?.message || t('sop.items.kanbanMoveFailed'));
  }
}

function templateName(id: string): string {
  if (!id) return '';
  return store.templates.find((tpl) => tpl.id === id)?.name || '';
}

/** 统计格与下拉共用：选中即筛选，再次点同一值取消（原型口径）。 */
function pickExec(value: ExecState | '') {
  const next = value && value !== store.itemFilter.exec ? value : undefined;
  store.itemFilter.exec = next;
  void reloadItems();
}

function setFilter(key: 'status' | 'owner' | 'source' | 'kind', value: unknown) {
  const next = String(value ?? '');
  store.itemFilter[key] = next || undefined;
  void reloadItems();
}

function onSearchInput() {
  // 300ms 防抖：输入过程中不请求，避免每次按键打断表格
  if (searchTimer) clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    store.itemFilter.q = search.value.trim() || undefined;
    void reloadItems();
  }, 300);
}

async function loadLatestRuns() {
  try {
    const runs = await listRuns({ project_id: store.currentProjectId });
    const map: Record<string, string> = {};
    // listRuns 已按时间倒序：每个事项第一次出现即最近一次
    for (const run of runs) {
      if (run.item_id && !map[run.item_id]) map[run.item_id] = run.id;
    }
    latestRuns.value = map;
  } catch {
    latestRuns.value = {};
  }
}

async function reloadItems() {
  await store.loadItems().catch(() => undefined);
}

async function refreshAll() {
  refreshing.value = true;
  try {
    await Promise.all([store.loadItems(), store.loadStats(), store.loadTemplates(), loadLatestRuns()]);
  } finally {
    refreshing.value = false;
  }
}

function toggleSelect(id: string, checked: boolean) {
  if (checked) selected.value.add(id);
  else selected.value.delete(id);
  // Set 的变更不触发响应式，需要换一个实例
  selected.value = new Set(selected.value);
}

/** 全选：大项自动展开成其子项（大项本身不能触发，勾了也没用）。 */
function toggleAll(event: Event) {
  const checked = (event.target as HTMLInputElement).checked;
  if (!checked) {
    selected.value = new Set<string>();
    return;
  }
  const next = new Set<string>();
  let expanded = 0;
  for (const item of store.items) {
    if (item.is_big) {
      expanded += 1;
      continue;
    }
    next.add(item.id);
  }
  selected.value = next;
  if (expanded) MessagePlugin.info(t('sop.split.expandedParents', { n: expanded }));
}

function clearSelection() {
  selected.value = new Set<string>();
}

function resolveTemplateId(item: SopItemWithDerived): string {
  return item.template_id || store.currentProject?.default_template_id || '';
}

async function launchItem(item: SopItemWithDerived) {
  const templateId = resolveTemplateId(item);
  if (!templateId) {
    MessagePlugin.warning(t('sop.items.noTemplate'));
    return;
  }
  launchingId.value = item.id;
  try {
    await createRun({ item_id: item.id, template_id: templateId });
    MessagePlugin.success(t('sop.items.launchStarted', { no: item.no || item.title }));
    await refreshAll();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.items.launchFailed'));
  } finally {
    launchingId.value = '';
  }
}

/**
 * 批量触发：走后端 bulk-launch 一次请求完成。
 * 父项由后端展开为子项，运行中/待介入与无模板的事项进 skipped，
 * 前端只负责按结果给出三组提示并刷新。
 */
async function bulkLaunch() {
  const ids = [...selected.value];
  if (!ids.length) return;
  bulkBusy.value = true;
  try {
    const result = await bulkLaunchItems(ids);
    if (result.started.length) {
      MessagePlugin.success(t('sop.items.bulkStarted', { n: result.started.length }));
    }
    if (result.skipped.length) {
      MessagePlugin.info(t('sop.items.bulkSkipped', { n: result.skipped.length }));
    }
    if (result.failed.length) {
      MessagePlugin.warning(t('sop.items.bulkFailed', { n: result.failed.length }));
    }
    clearSelection();
    await refreshAll();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.items.launchFailed'));
  } finally {
    bulkBusy.value = false;
  }
}

async function bulkCloseItems() {
  const ids = [...selected.value];
  if (!ids.length) return;
  bulkBusy.value = true;
  try {
    const result = await bulkClose(ids);
    if (result.skipped.length) {
      MessagePlugin.info(t('sop.items.bulkSkipped', { n: result.skipped.length }));
    }
    if (result.updated.length) {
      MessagePlugin.success(t('sop.items.bulkClosed', { n: result.updated.length }));
    }
    clearSelection();
    await refreshAll();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.items.bulkCloseFailed'));
  } finally {
    bulkBusy.value = false;
  }
}

async function openLatestRun(item: SopItemWithDerived) {
  // 抽屉在 T42；先跳到最近一次运行详情，信息量最大且不改数据
  const runId = latestRuns.value[item.id] || item.last_run_id;
  if (runId) {
    await router.push({ name: 'sopRunDetail', params: { id: runId } });
    return;
  }
  MessagePlugin.info(t('sop.items.noRunYet'));
}

function openItem(item: SopItemWithDerived) {
  drawerItemId.value = item.id;
}

async function onDrawerChanged() {
  await refreshAll();
}

async function onItemCreated() {
  // 关窗由父级负责：子组件在过渡动画中改 visible 会让 TDesign 卡在 enter/leave 类
  createVisible.value = false;
  await refreshAll();
}

function openSettings() {
  void router.push({ name: 'sopProjectSettings' });
}

watch(
  () => store.currentProjectId,
  async () => {
    clearSelection();
    search.value = '';
    store.itemFilter = {};
    await refreshAll();
  },
);

onMounted(async () => {
  await refreshAll();
  // 自动刷新只替换表格行与统计；过滤控件在 store 里，不会重挂载，焦点得以保留
  stopPolling = store.startPolling(() => {
    void store.loadItems().catch(() => undefined);
    void store.loadStats().catch(() => undefined);
  }, 5000);
  latestRunsTick = store.startPolling(() => void loadLatestRuns(), 15000);
});

onUnmounted(() => {
  stopPolling?.();
  latestRunsTick?.();
  if (searchTimer) clearTimeout(searchTimer);
});
</script>

<style lang="less" scoped src="./sopItemBoard.less"></style>
