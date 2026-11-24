<template>
  <div class="sop-run-detail">
    <header class="sop-run-head">
      <div class="sop-run-title">
        <button type="button" class="sop-back" @click="backToList">← {{ $t('sop.runDetailPage.back') }}</button>
        <b class="mono">{{ runDetail?.run.id ? shortId(runDetail.run.id) : '—' }}</b>
        <span v-if="runDetail" class="sop-run-status" :class="`is-${runDetail.run.status}`">
          {{ runStatusLabel(runDetail.run.status) }}
        </span>
      </div>
      <div class="sop-run-actions">
        <t-button v-if="canPause" size="small" variant="outline" :loading="busy" @click="doPause">
          {{ $t('sop.runDetailPage.pause') }}
        </t-button>
        <t-button v-if="canResume" size="small" variant="outline" :loading="busy" @click="doResume">
          {{ $t('sop.runDetailPage.resume') }}
        </t-button>
        <t-button v-if="canCancel" size="small" variant="outline" theme="danger" :loading="busy" @click="doCancel">
          {{ $t('sop.runDetailPage.cancel') }}
        </t-button>
        <t-button v-if="canRetry" size="small" theme="primary" :loading="busy" @click="doRetry">
          {{ $t('sop.runsList.retry') }}
        </t-button>
      </div>
    </header>

    <div v-if="!runDetail" class="sop-empty">{{ $t('sop.runDetailPage.notFound') }}</div>

    <template v-else>
      <!-- 只读画布：节点按状态着色，SSE 实时更新 -->
      <div class="sop-run-canvas">
        <VueFlow
          :nodes="flowNodes"
          :edges="flowEdges"
          :min-zoom="0.3"
          :max-zoom="1.4"
          :zoom-on-scroll="true"
          :pan-on-drag="[1, 2]"
          :nodes-draggable="false"
          :nodes-connectable="false"
          :fit-view-on-init="true"
          @node-click="onNodeClick"
        >
          <template #node-sop="{ data }">
            <div class="sop-ro-node" :class="`is-${data.state}`">
              <div class="sop-ro-head">
                <span class="sop-ro-type">{{ data.typeLabel }}</span>
                <b>{{ data.name }}</b>
              </div>
              <div class="sop-ro-state">{{ data.stateLabel }}</div>
            </div>
          </template>
        </VueFlow>
      </div>

      <div class="sop-run-body">
        <section class="sop-run-stream">
          <header class="sop-stream-head">
            <b>{{ $t('sop.runDetailPage.live') }}</b>
            <span class="sop-live" :class="{ on: streaming }">
              <i /> {{ streaming ? $t('sop.runDetailPage.streaming') : $t('sop.runDetailPage.streamIdle') }}
            </span>
            <button type="button" class="sop-link" @click="reconnect">{{ $t('sop.runDetailPage.reconnect') }}</button>
          </header>
          <div ref="streamEl" class="sop-stream">
            <div v-for="(line, index) in streamLines" :key="index" class="sop-stream-line" :class="`is-${line.kind}`">
              <span class="sop-stream-node">{{ line.node || '·' }}</span>
              <span>{{ line.text }}</span>
            </div>
            <div v-if="!streamLines.length" class="sop-muted">{{ $t('sop.runDetailPage.noEvents') }}</div>
          </div>
        </section>

        <!-- 节点抽屉：选中节点后显示任务书 / 日志 / 产物 / 操作 -->
        <aside v-if="selected" class="sop-node-panel">
          <header class="sop-node-head">
            <b>{{ selected.node.name || selected.node.id }}</b>
            <span class="sop-run-status" :class="`is-${selected.nr?.status || 'pending'}`">
              {{ nodeStatusLabel(selected.nr?.status || 'pending') }}
            </span>
          </header>

          <div class="sop-node-tabs">
            <button
              v-for="tab in nodeTabs"
              :key="tab.key"
              type="button"
              class="sop-node-tab"
              :class="{ on: nodeTab === tab.key }"
              @click="nodeTab = tab.key"
            >
              {{ tab.label }}
            </button>
          </div>

          <div v-if="nodeTab === 'taskbook'" class="sop-node-body">
            <h5>{{ $t('sop.runDetailPage.tbRole') }}</h5>
            <p>{{ executorSummary(selected.node) }}</p>
            <h5>{{ $t('sop.runDetailPage.tbItem') }}</h5>
            <p>{{ itemTitle }}</p>
            <h5>{{ $t('sop.runDetailPage.tbUpstream') }}</h5>
            <p>{{ upstreamTitles(selected.node) || '—' }}</p>
            <h5>{{ $t('sop.runDetailPage.tbProject') }}</h5>
            <p>{{ projectContextText }}</p>
            <h5>{{ $t('sop.runDetailPage.tbRework') }}</h5>
            <p>{{ selected.nr?.feedback || '—' }}</p>
            <h5>{{ $t('sop.runDetailPage.tbHuman') }}</h5>
            <ul class="sop-human-list">
              <li v-for="(entry, index) in humanContextFor(selected.node.id)" :key="index">
                <span class="sop-muted">{{ formatTime(entry.at) }} · {{ entry.source }}</span> {{ entry.text }}
              </li>
              <li v-if="!humanContextFor(selected.node.id).length" class="sop-muted">—</li>
            </ul>
            <h5>{{ $t('sop.runDetailPage.tbOutputs') }}</h5>
            <p>{{ taskPromptOf(selected.node) || '—' }}</p>
          </div>

          <div v-else-if="nodeTab === 'events'" class="sop-node-body">
            <div v-for="(line, index) in selectedLines" :key="index" class="sop-stream-line" :class="`is-${line.kind}`">
              <span>{{ line.text }}</span>
            </div>
            <div v-if="!selectedLines.length" class="sop-muted">{{ $t('sop.runDetailPage.noEvents') }}</div>
            <t-button size="small" variant="text" :loading="logsLoading" @click="loadLogs">
              {{ $t('sop.runDetailPage.loadLogs') }}
            </t-button>
          </div>

          <div v-else-if="nodeTab === 'artifacts'" class="sop-node-body">
            <div v-for="art in selectedArtifacts" :key="art.id" class="sop-artifact-row">
              <span class="mono">{{ art.path }}</span>
              <span class="sop-muted">{{ formatBytes(art.size) }} · {{ shortSha(art.sha256) }}</span>
              <t-button size="small" variant="text" @click="openArtifact(art)">
                {{ $t('sop.runDetailPage.open') }}
              </t-button>
            </div>
            <div v-if="!selectedArtifacts.length" class="sop-muted">{{ $t('sop.drawer.noArtifacts') }}</div>
          </div>

          <div v-else class="sop-node-body">
            <!-- 节点级操作：按状态显示 -->
            <template v-if="selected.nr?.status === 'waiting_approval'">
              <label class="sop-field">
                <span>{{ $t('sop.runDetailPage.comment') }}</span>
                <textarea v-model="comment" class="sop-textarea" rows="2" />
              </label>
              <div class="sop-row">
                <t-button size="small" theme="primary" :loading="busy" @click="approve">{{ $t('sop.runDetailPage.approve') }}</t-button>
                <t-button size="small" variant="outline" :loading="busy" @click="rework">{{ $t('sop.runDetailPage.rework') }}</t-button>
                <t-button size="small" theme="danger" variant="outline" :loading="busy" @click="terminate">{{ $t('sop.runDetailPage.terminate') }}</t-button>
              </div>
              <label class="sop-field">
                <span>{{ $t('sop.runDetailPage.rejectTo') }}</span>
                <t-select v-model="rejectTo" :options="upstreamOptions" />
              </label>
              <t-button size="small" theme="danger" :loading="busy" @click="reject">{{ $t('sop.runDetailPage.reject') }}</t-button>
            </template>

            <template v-else-if="selected.nr?.status === 'pending_input'">
              <label class="sop-field">
                <span>{{ $t('sop.runDetailPage.answer') }}</span>
                <textarea v-model="answerText" class="sop-textarea" rows="3" />
              </label>
              <t-button size="small" theme="primary" :loading="busy" @click="answerPrompt">
                {{ $t('sop.runDetailPage.answerSubmit') }}
              </t-button>
            </template>

            <template v-else>
              <p class="sop-muted">{{ $t('sop.runDetailPage.noNodeAction') }}</p>
            </template>

            <label class="sop-field">
              <span>{{ $t('sop.runDetailPage.humanContext') }}</span>
              <textarea v-model="humanText" class="sop-textarea" rows="2" />
            </label>
            <t-button size="small" variant="outline" :loading="busy" :disabled="!humanText.trim()" @click="addContext">
              {{ $t('sop.runDetailPage.humanContextAdd') }}
            </t-button>
          </div>
        </aside>
      </div>

      <!-- run 级信息栏 -->
      <section class="sop-run-meta">
        <div class="sop-card">
          <h4>{{ $t('sop.runDetailPage.usage') }}</h4>
          <div class="sop-kv"><span class="k">Input</span><span class="v">{{ usage.input_tokens }}</span></div>
          <div class="sop-kv"><span class="k">Output</span><span class="v">{{ usage.output_tokens }}</span></div>
          <div class="sop-kv"><span class="k">Cost</span><span class="v">${{ usage.cost_usd.toFixed(4) }}</span></div>
          <div class="sop-kv sop-kv--last"><span class="k">{{ $t('sop.runDetailPage.duration') }}</span><span class="v">{{ durationText }}</span></div>
        </div>
        <div class="sop-card">
          <h4>{{ $t('sop.runDetailPage.context') }}</h4>
          <div class="sop-kv"><span class="k">{{ $t('sop.runsList.colTrigger') }}</span><span class="v">{{ triggerSourceLabel(runDetail.run.trigger_kind) }}</span></div>
          <div class="sop-kv"><span class="k">{{ $t('sop.runDetailPage.item') }}</span><span class="v">{{ itemTitle }}</span></div>
          <div class="sop-kv"><span class="k">{{ $t('sop.runDetailPage.workspace') }}</span><span class="v mono">{{ runDetail.run.workspace_dir || '—' }}</span></div>
          <div class="sop-kv sop-kv--last"><span class="k">{{ $t('sop.runDetailPage.branch') }}</span><span class="v mono">sop/{{ shortId(runDetail.run.id) }}</span></div>
        </div>
        <div class="sop-card">
          <h4>{{ $t('sop.runDetailPage.humanTimeline') }}</h4>
          <ul class="sop-human-list">
            <li v-for="(entry, index) in runDetail.run.human_context || []" :key="index">
              <span class="sop-muted">{{ formatTime(entry.at) }} · {{ entry.source }}</span> {{ entry.text }}
            </li>
            <li v-if="!(runDetail.run.human_context || []).length" class="sop-muted">—</li>
          </ul>
        </div>
        <div class="sop-card">
          <h4>{{ $t('sop.runDetailPage.input') }}</h4>
          <pre class="sop-pre">{{ JSON.stringify(runDetail.run.input || {}, null, 2) }}</pre>
        </div>
      </section>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import { VueFlow, type Edge, type Node } from '@vue-flow/core';
import '@vue-flow/core/dist/style.css';
import '@vue-flow/core/dist/theme-default.css';
import {
  addHumanContext,
  answerNode,
  approveNode,
  cancelRun,
  getNodeLogs,
  getRun,
  listArtifacts,
  pauseRun,
  rejectNode,
  resumeRun,
  retryRun,
  reworkNode,
  terminateNode,
  type SopArtifact,
  type SopRunDetail,
  type SopNode,
  type SopNodeRun,
} from '@/api/sop';
import {
  NODE_STATUS_LABELS,
  RUN_STATUS_LABELS,
  formatBytes,
  shortSha,
  triggerSourceLabel,
} from '@/api/sop/helpers';
import { streamGet, type SopStreamEvent } from '@/api/sop/sse';
import { useSopStore } from '@/stores/sop';
import { SOP_BASE } from '@/api/sop';

interface StreamLine {
  node?: string;
  kind: string;
  text: string;
}

const route = useRoute();
const router = useRouter();
const { t } = useI18n();
const store = useSopStore();
const { templates } = storeToRefs(store);

const runDetail = ref<SopRunDetail | null>(null);
const selectedNodeId = ref('');
const streamLines = ref<StreamLine[]>([]);
const logs = ref<string>('');
const logsLoading = ref(false);
const streaming = ref(false);
const busy = ref(false);
const comment = ref('');
const rejectTo = ref('');
const answerText = ref('');
const humanText = ref('');
const streamEl = ref<HTMLElement | null>(null);
const nodeTab = ref('taskbook');

let controller: AbortController | null = null;
let retryTimer: ReturnType<typeof setTimeout> | null = null;
let attempts = 0;

const nodeTabs = computed(() => [
  { key: 'taskbook', label: t('sop.runDetailPage.tabTaskbook') },
  { key: 'events', label: t('sop.runDetailPage.tabEvents') },
  { key: 'artifacts', label: t('sop.runDetailPage.tabArtifacts') },
  { key: 'actions', label: t('sop.runDetailPage.tabActions') },
]);

const usage = computed(() => runDetail.value?.run.usage || { input_tokens: 0, output_tokens: 0, cost_usd: 0 });

const durationText = computed(() => {
  const ms = runDetail.value?.run.execution_duration_ms || 0;
  if (!ms) return '—';
  return `${Math.round(ms / 1000)}s`;
});

const projectContextText = computed(() => {
  const text = runDetail.value?.project_context || '';
  return text || t('sop.runDetailPage.noProjectContext');
});

const itemTitle = computed(() => {
  const itemId = runDetail.value?.run.item_id;
  if (!itemId) return '—';
  const item = store.items.find((entry) => entry.id === itemId);
  return item ? `${item.no || ''} ${item.title}`.trim() : itemId;
});

const canPause = computed(() => runDetail.value?.run.status === 'running');
const canResume = computed(() => runDetail.value?.run.status === 'paused');
const canCancel = computed(() =>
  ['queued', 'running', 'waiting', 'paused'].includes(runDetail.value?.run.status || ''),
);
const canRetry = computed(() =>
  ['failed', 'cancelled'].includes(runDetail.value?.run.status || ''),
);

/** 画布节点：三种运行状态映射到颜色，节点名来自运行快照。 */
const flowNodes = computed<Node[]>(() => {
  const snapshot = runDetail.value?.run.workflow_snapshot;
  if (!snapshot) return [];
  return (snapshot.nodes || []).map((node) => {
    const nr = findNodeRun(node.id);
    return {
      id: node.id,
      type: 'sop',
      position: node.position || { x: 0, y: 0 },
      data: {
        name: node.name || node.id,
        typeLabel: node.type,
        state: nr?.status || 'pending',
        stateLabel: nodeStatusLabel(nr?.status || 'pending'),
      },
    };
  });
});

const flowEdges = computed<Edge[]>(() => {
  const snapshot = runDetail.value?.run.workflow_snapshot;
  if (!snapshot) return [];
  return (snapshot.edges || []).map((edge) => ({
    id: edge.id || `${edge.source}->${edge.target}`,
    source: edge.source,
    target: edge.target,
    animated: false,
  }));
});

const selected = computed(() => {
  const snapshot = runDetail.value?.run.workflow_snapshot;
  if (!snapshot) return null;
  const node = (snapshot.nodes || []).find((item) => item.id === selectedNodeId.value);
  if (!node) return null;
  return { node, nr: findNodeRun(node.id) };
});

const selectedLines = computed(() =>
  streamLines.value.filter((line) => !line.node || line.node === selectedNodeId.value),
);

const selectedArtifacts = computed(() =>
  (runDetail.value?.artifacts || []).filter((art) => !selectedNodeId.value || art.node_id === selectedNodeId.value),
);

const upstreamOptions = computed(() => {
  const snapshot = runDetail.value?.run.workflow_snapshot;
  const current = selectedNodeId.value;
  if (!snapshot || !current) return [];
  const upstream = new Set<string>();
  for (const edge of snapshot.edges || []) {
    if (edge.target === current) upstream.add(edge.source);
  }
  const queue = [...upstream];
  while (queue.length) {
    const id = queue.shift() as string;
    for (const edge of snapshot.edges || []) {
      if (edge.target === id && !upstream.has(edge.source)) {
        upstream.add(edge.source);
        queue.push(edge.source);
      }
    }
  }
  return (snapshot.nodes || [])
    .filter((node) => upstream.has(node.id))
    .map((node) => ({ value: node.id, label: node.name || node.id }));
});

function runStatusLabel(status: string): string {
  return (RUN_STATUS_LABELS as Record<string, string>)[status] || status;
}

function nodeStatusLabel(status: string): string {
  return (NODE_STATUS_LABELS as Record<string, string>)[status] || status;
}

function shortId(id: string): string {
  return id ? id.slice(0, 8) : '—';
}

function formatTime(value?: string | null): string {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function findNodeRun(nodeId: string): SopNodeRun | undefined {
  return (runDetail.value?.node_runs || []).find((nr) => nr.node_id === nodeId);
}

function executorSummary(node: SopNode): string {
  const executor = node.executor || { kind: 'platform_agent' };
  if (executor.kind === 'cli') return `${t('sop.designer.execCli')} · ${executor.cli_kind || ''}`;
  return t('sop.designer.execAgent');
}

function taskPromptOf(node: SopNode): string {
  return node.task_prompt || '';
}

function upstreamTitles(node: SopNode): string {
  const snapshot = runDetail.value?.run.workflow_snapshot;
  if (!snapshot) return '';
  const refs = new Set(node.task_refs || []);
  for (const edge of snapshot.edges || []) {
    if (edge.target === node.id) refs.add(edge.source);
  }
  return [...refs]
    .map((id) => (snapshot.nodes || []).find((item) => item.id === id)?.name || id)
    .join('、');
}

function humanContextFor(nodeId: string) {
  return (runDetail.value?.run.human_context || []).filter((entry) => !entry.node_id || entry.node_id === nodeId);
}

function onNodeClick(event: { node: Node }) {
  selectedNodeId.value = event.node.id;
  nodeTab.value = 'taskbook';
}

function appendLine(line: StreamLine) {
  streamLines.value.push(line);
  if (streamLines.value.length > 500) streamLines.value.splice(0, streamLines.value.length - 500);
}

async function scrollStream() {
  await Promise.resolve();
  if (streamEl.value) streamEl.value.scrollTop = streamEl.value.scrollHeight;
}

/** SSE 直播：node_status 直接更新节点运行状态（颜色随之变化），文本追加到对应节点。 */
function onStreamEvent(event: SopStreamEvent) {
  if (event.type === 'node_status') {
    const payload = event.payload as SopNodeRun | undefined;
    if (payload?.node_id && runDetail.value) {
      const list = runDetail.value.node_runs || [];
      const index = list.findIndex((nr) => nr.node_id === payload.node_id);
      if (index >= 0) list[index] = { ...list[index], ...payload };
      else list.push(payload);
      runDetail.value = { ...runDetail.value, node_runs: [...list] };
    }
    return;
  }
  if (event.type === 'text_delta' && event.text) {
    appendLine({ node: currentNodeId(), kind: 'text', text: event.text });
    void scrollStream();
    return;
  }
  if (event.type === 'tool_call') {
    appendLine({ node: currentNodeId(), kind: 'tool', text: `→ ${event.tool_name || ''}` });
    return;
  }
  if (event.type === 'tool_result') {
    appendLine({ node: currentNodeId(), kind: 'tool', text: `← ${event.tool_name || ''}` });
    return;
  }
  if (event.type === 'error') {
    appendLine({ kind: 'error', text: String(event.text || 'error') });
    return;
  }
  if (event.type === 'result') {
    appendLine({ kind: 'result', text: t('sop.runDetailPage.resultFrame') });
  }
}

/** 当前正在执行的节点（用于把流事件归到抽屉里）。 */
function currentNodeId(): string {
  const running = (runDetail.value?.node_runs || []).find((nr) => nr.status === 'running');
  return running?.node_id || selectedNodeId.value;
}

function connect() {
  const runId = runDetail.value?.run.id;
  if (!runId) return;
  controller?.abort();
  controller = new AbortController();
  streaming.value = true;
  attempts = 0;
  void (async () => {
    try {
      await streamGet(
        `${SOP_BASE}/runs/${runId}/events`,
        onStreamEvent,
        controller?.signal,
      );
      streaming.value = false;
    } catch {
      streaming.value = false;
      // 断线退避重连：1s / 2s / 4s，最多 3 次
      if (attempts >= 3) return;
      const delay = 1000 * 2 ** attempts;
      attempts += 1;
      retryTimer = setTimeout(connect, delay);
    }
  })();
}

function reconnect() {
  attempts = 0;
  connect();
}

async function load() {
  const id = String(route.params.id || '');
  if (!id) return;
  try {
    runDetail.value = await getRun(id);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runDetailPage.loadFailed'));
    return;
  }
  if (!selectedNodeId.value) {
    const first = (runDetail.value.run.workflow_snapshot.nodes || [])[0];
    if (first) selectedNodeId.value = first.id;
  }
  connect();
}

async function reloadRun() {
  const id = runDetail.value?.run.id;
  if (!id) return;
  runDetail.value = await getRun(id).catch(() => runDetail.value);
}

async function doPause() {
  if (!runDetail.value) return;
  busy.value = true;
  try {
    await pauseRun(runDetail.value.run.id);
    MessagePlugin.success(t('sop.runDetailPage.paused'));
    await reloadRun();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runDetailPage.opFailed'));
  } finally {
    busy.value = false;
  }
}

async function doResume() {
  if (!runDetail.value) return;
  busy.value = true;
  try {
    await resumeRun(runDetail.value.run.id);
    MessagePlugin.success(t('sop.runDetailPage.resumed'));
    await reloadRun();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runDetailPage.opFailed'));
  } finally {
    busy.value = false;
  }
}

async function doCancel() {
  if (!runDetail.value) return;
  busy.value = true;
  try {
    await cancelRun(runDetail.value.run.id);
    MessagePlugin.success(t('sop.runDetailPage.cancelled'));
    await reloadRun();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runDetailPage.opFailed'));
  } finally {
    busy.value = false;
  }
}

async function doRetry() {
  if (!runDetail.value) return;
  busy.value = true;
  try {
    await retryRun(runDetail.value.run.id);
    MessagePlugin.success(t('sop.runsList.retried'));
    await reloadRun();
    connect();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runsList.retryFailed'));
  } finally {
    busy.value = false;
  }
}

async function approve() {
  if (!runDetail.value || !selected.value) return;
  busy.value = true;
  try {
    await approveNode(runDetail.value.run.id, selected.value.node.id, comment.value);
    MessagePlugin.success(t('sop.runDetailPage.approved'));
    comment.value = '';
    await reloadRun();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runDetailPage.opFailed'));
  } finally {
    busy.value = false;
  }
}

async function reject() {
  if (!runDetail.value || !selected.value) return;
  if (!rejectTo.value) {
    MessagePlugin.warning(t('sop.runDetailPage.pickRejectTarget'));
    return;
  }
  busy.value = true;
  try {
    await rejectNode(runDetail.value.run.id, selected.value.node.id, comment.value, rejectTo.value);
    MessagePlugin.success(t('sop.runDetailPage.rejected'));
    comment.value = '';
    await reloadRun();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runDetailPage.opFailed'));
  } finally {
    busy.value = false;
  }
}

async function rework() {
  if (!runDetail.value || !selected.value) return;
  busy.value = true;
  try {
    await reworkNode(runDetail.value.run.id, selected.value.node.id, comment.value);
    MessagePlugin.success(t('sop.runDetailPage.reworked'));
    comment.value = '';
    await reloadRun();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runDetailPage.opFailed'));
  } finally {
    busy.value = false;
  }
}

async function terminate() {
  if (!runDetail.value || !selected.value) return;
  busy.value = true;
  try {
    await terminateNode(runDetail.value.run.id, selected.value.node.id, comment.value);
    MessagePlugin.success(t('sop.runDetailPage.terminated'));
    comment.value = '';
    await reloadRun();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runDetailPage.opFailed'));
  } finally {
    busy.value = false;
  }
}

async function answerPrompt() {
  if (!runDetail.value || !selected.value) return;
  if (!answerText.value.trim()) return;
  busy.value = true;
  try {
    await answerNode(runDetail.value.run.id, selected.value.node.id, answerText.value.trim());
    MessagePlugin.success(t('sop.runDetailPage.answered'));
    answerText.value = '';
    await reloadRun();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runDetailPage.opFailed'));
  } finally {
    busy.value = false;
  }
}

async function addContext() {
  if (!runDetail.value) return;
  const text = humanText.value.trim();
  if (!text) return;
  busy.value = true;
  try {
    await addHumanContext(runDetail.value.run.id, text, selectedNodeId.value);
    MessagePlugin.success(t('sop.runDetailPage.contextAdded'));
    humanText.value = '';
    await reloadRun();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runDetailPage.opFailed'));
  } finally {
    busy.value = false;
  }
}

async function loadLogs() {
  if (!runDetail.value || !selected.value) return;
  logsLoading.value = true;
  try {
    logs.value = await getNodeLogs(runDetail.value.run.id, selected.value.node.id);
    const lines = logs.value.split('\n').filter(Boolean).slice(-200);
    streamLines.value = [
      ...streamLines.value.filter((line) => line.node !== selected.value!.node.id),
      ...lines.map((line) => ({ node: selected.value!.node.id, kind: 'log', text: line })),
    ];
  } catch {
    MessagePlugin.info(t('sop.runDetailPage.noLogs'));
  } finally {
    logsLoading.value = false;
  }
}

async function openArtifact(artifact: SopArtifact) {
  // 大弹窗预览属 T50；先开文件地址（下载/内联由后端 media type 决定）
  window.open(`${SOP_BASE}/runs/${artifact.run_id}/artifacts/${artifact.id}/file`, '_blank');
}

function backToList() {
  void router.push({ name: 'sopRuns' });
}

watch(
  () => route.params.id,
  () => {
    selectedNodeId.value = '';
    streamLines.value = [];
    void load();
  },
);

onMounted(async () => {
  if (!templates.value.length) await store.loadTemplates().catch(() => undefined);
  await load();
});

onUnmounted(() => {
  controller?.abort();
  if (retryTimer) clearTimeout(retryTimer);
});

void listArtifacts;
void selectedLines;
</script>

<style lang="less" scoped src="./sopRunDetail.less"></style>
