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
        <t-button size="small" variant="outline" @click="openSettings">
          ⚙ {{ $t('sop.settings.projectTitle') }}
        </t-button>
        <t-button size="small" variant="outline" :loading="refreshing" @click="refreshAll">
          ⟳
        </t-button>
        <t-tooltip :content="$t('sop.items.newItemSoon')">
          <t-button size="small" theme="primary" disabled>＋ {{ $t('sop.items.newItem') }}</t-button>
        </t-tooltip>
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
              <span>{{ item.current_node || '—' }}</span>
              <span class="mono">{{ progressText(item.progress_done, item.progress_total) }}</span>
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
          <tr v-for="item in store.items" :key="item.id" class="sop-row" @click="openItem(item)">
            <td class="col-check" @click.stop>
              <input
                type="checkbox"
                :checked="selected.has(item.id)"
                @change="toggleSelect(item.id, ($event.target as HTMLInputElement).checked)"
              />
            </td>
            <td class="col-no">{{ item.no || '—' }}</td>
            <td>
              <span class="sop-title">{{ item.title }}</span>
              <span class="sop-kind">{{ KIND_LABELS[item.kind] || item.kind }}</span>
            </td>
            <td class="col-status">
              <span class="sop-status">
                <i :style="{ background: ITEM_STATUS_COLORS[item.status] || '#c3c8cf' }" />
                {{ ITEM_STATUS_LABELS[item.status] || item.status }}
              </span>
            </td>
            <td class="col-source">
              <span class="sop-src">{{ SOURCE_LABELS[item.source] || item.source }}</span>
            </td>
            <td class="col-owner">{{ item.owner || '—' }}</td>
            <td class="col-prio">
              <span class="sop-prio" :class="`is-${item.priority || 'P2'}`">{{ item.priority || 'P2' }}</span>
            </td>
            <td class="col-tpl">
              <span v-if="templateName(item.template_id)">{{ templateName(item.template_id) }}</span>
              <span v-else class="sop-muted">{{ $t('sop.items.templateUnbound') }}</span>
            </td>
            <td class="col-stage">{{ item.current_node || '—' }}</td>
            <td class="col-progress">{{ progressText(item.progress_done, item.progress_total) }}</td>
            <td class="col-actions" @click.stop>
              <t-button
                v-if="item.exec_state === 'intervene'"
                size="small"
                theme="danger"
                variant="outline"
                @click="openLatestRun(item)"
              >
                {{ $t('sop.items.actionHandleFailure') }}
              </t-button>
              <t-button
                v-else-if="item.exec_state === 'running' || item.exec_state === 'pr'"
                size="small"
                variant="outline"
                @click="openLatestRun(item)"
              >
                {{ $t('sop.items.actionViewRun') }}
              </t-button>
              <t-button
                v-else
                size="small"
                theme="primary"
                :loading="launchingId === item.id"
                @click="launchItem(item)"
              >
                {{ $t('sop.items.actionLaunch') }}
              </t-button>
            </td>
          </tr>
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
  progressText,
} from '@/api/sop/helpers';
import type { ExecState } from '@/api/sop';
import { useSopStore } from '@/stores/sop';
import SopExecFilter from './components/SopExecFilter.vue';
import SopItemDrawer from './SopItemDrawer.vue';

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
const draggingId = ref('');
const dragOver = ref<ItemStatus | ''>('');
const launchingId = ref('');
const bulkBusy = ref(false);
const refreshing = ref(false);
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

function toggleAll(event: Event) {
  const checked = (event.target as HTMLInputElement).checked;
  selected.value = checked ? new Set(store.items.map((item) => item.id)) : new Set<string>();
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
 * 批量触发：后端没有 bulk-launch 端点，这里逐条 createRun。
 * 正在推进/需要介入的事项跳过（与「跳过运行中」口径一致），其余失败逐条累计。
 */
async function bulkLaunch() {
  const targets = store.items.filter((item) => selected.value.has(item.id));
  const skipped = targets.filter(
    (item) => item.exec_state === 'running' || item.exec_state === 'intervene',
  );
  const runnable = targets.filter(
    (item) => item.exec_state !== 'running' && item.exec_state !== 'intervene',
  );
  if (!runnable.length) {
    MessagePlugin.info(t('sop.items.bulkSkipped', { n: skipped.length }));
    return;
  }
  bulkBusy.value = true;
  let started = 0;
  let failed = 0;
  for (const item of runnable) {
    const templateId = resolveTemplateId(item);
    if (!templateId) {
      failed += 1;
      continue;
    }
    try {
      await createRun({ item_id: item.id, template_id: templateId });
      started += 1;
    } catch {
      failed += 1;
    }
  }
  bulkBusy.value = false;
  if (started) MessagePlugin.success(t('sop.items.bulkStarted', { n: started }));
  if (skipped.length) MessagePlugin.info(t('sop.items.bulkSkipped', { n: skipped.length }));
  if (failed) MessagePlugin.warning(t('sop.items.bulkFailed', { n: failed }));
  clearSelection();
  await refreshAll();
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
