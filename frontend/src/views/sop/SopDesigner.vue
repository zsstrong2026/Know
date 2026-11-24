<template>
  <div class="sop-designer">
    <header class="sop-designer-head">
      <div class="sop-designer-title">
        <button type="button" class="sop-back" @click="backToList">← {{ $t('sop.designer.back') }}</button>
        <b>{{ template?.name || $t('sop.designer.untitled') }}</b>
        <span v-if="template?.is_builtin" class="sop-tag">{{ $t('sop.designer.builtinReadonly') }}</span>
      </div>
      <div class="sop-designer-actions">
        <div class="sop-seg">
          <button type="button" :class="{ on: sidePanel === 'inspector' }" @click="sidePanel = 'inspector'">
            {{ $t('sop.designer.panelCanvas') }}
          </button>
          <button type="button" :class="{ on: sidePanel === 'ai' }" @click="sidePanel = 'ai'">
            {{ $t('sop.ai.title') }}
          </button>
        </div>
        <t-button size="small" variant="outline" :loading="saving" :disabled="readonly" @click="saveDraft">
          {{ $t('sop.designer.saveDraft') }}
        </t-button>
        <t-button size="small" theme="primary" :loading="publishing" :disabled="readonly" @click="publish">
          {{ $t('sop.designer.publish') }}
        </t-button>
      </div>
    </header>

    <div v-if="template?.is_builtin" class="sop-banner">
      {{ $t('sop.designer.builtinBanner') }}
      <t-button size="small" theme="primary" variant="outline" @click="copyBuiltin">
        {{ $t('sop.tpl.copy') }}
      </t-button>
    </div>

    <div class="sop-designer-body">
      <aside v-if="!readonly" class="sop-palette">
        <div class="sop-palette-title">{{ $t('sop.designer.palette') }}</div>
        <button
          v-for="kind in paletteKinds"
          :key="kind.type"
          type="button"
          class="sop-palette-item"
          draggable="true"
          @dragstart="onPaletteDragStart(kind.type)"
          @click="addNode(kind.type)"
        >
          <b>{{ kind.label }}</b>
          <span>{{ kind.hint }}</span>
        </button>
      </aside>

      <div
        ref="flowWrapEl"
        class="sop-canvas"
        @dragover.prevent
        @drop="onCanvasDrop"
      >
        <VueFlow
          :nodes="flowNodes"
          :edges="flowEdges"
          :min-zoom="0.2"
          :max-zoom="1.6"
          :zoom-on-scroll="true"
          :pan-on-drag="[1, 2]"
          :nodes-draggable="!readonly"
          :nodes-connectable="false"
          :fit-view-on-init="true"
          @node-drag-stop="onNodeDragStop"
          @node-click="onNodeClick"
        >
          <template #node-sop="{ id, data }">
            <SopNodeCard
              :node="data.node"
              :readonly="readonly"
              :pending-source="pendingSource"
              :reject-target-name="data.rejectTargetName"
              @exit-click="onExitClick"
            />
          </template>
        </VueFlow>

        <SopCanvasToolbar
          :can-undo="history.canUndo"
          :can-redo="history.canRedo"
          :zoom="zoom"
          :minimap="minimap"
          @undo="undo"
          @redo="redo"
          @zoom-in="zoomBy(1.2)"
          @zoom-out="zoomBy(1 / 1.2)"
          @fit="fitView"
          @auto-layout="doAutoLayout"
          @toggle-minimap="minimap = !minimap"
        />

        <svg v-if="minimap && minimapBox" class="sop-minimap" :viewBox="minimapBox">
          <line
            v-for="(edge, index) in miniEdges"
            :key="index"
            :x1="edge.x1"
            :y1="edge.y1"
            :x2="edge.x2"
            :y2="edge.y2"
            class="sop-mini-edge"
            :class="{ reject: edge.reject }"
          />
          <rect
            v-for="rect in miniNodes"
            :key="rect.id"
            :x="rect.x"
            :y="rect.y"
            :width="rect.w"
            :height="rect.h"
            rx="6"
            class="sop-mini-node"
          />
        </svg>
      </div>

      <SopInspector
        v-if="sidePanel === 'inspector' && selectedNode"
        :key="selectedNode.id"
        :node="selectedNode"
        :def="definition"
        :readonly="readonly"
        @change="onInspectorChange"
        @ai-configure="onAiConfigure"
      />

      <SopAiPanel
        v-else-if="sidePanel === 'ai'"
        :template-id="String(route.params.id || '')"
        :def="definition"
        :readonly="readonly"
        :node-id="aiNodeId || undefined"
        :node-name="aiNodeName"
        @apply-definition="onAiApply"
        @exit-node-mode="aiNodeId = ''"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, shallowRef } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import { VueFlow, useVueFlow, type Edge, type Node } from '@vue-flow/core';
import {
  copyTemplate,
  getTemplate,
  publishTemplate,
  updateTemplate,
  type SopDefinition,
  type SopNode,
  type SopTemplate,
} from '@/api/sop';
import '@vue-flow/core/dist/style.css';
import '@vue-flow/core/dist/theme-default.css';
import SopNodeCard from './components/SopNodeCard.vue';
import SopCanvasToolbar from './components/SopCanvasToolbar.vue';
import SopInspector from './components/SopInspector.vue';
import SopAiPanel from './components/SopAiPanel.vue';
import { SnapshotHistory } from './canvas/history';
import { autoLayout } from './canvas/layout';
import { minimapTransform } from './canvas/anchors';

const route = useRoute();
const router = useRouter();
const { t } = useI18n();

const template = ref<SopTemplate | null>(null);
const definition = shallowRef<SopDefinition>({ nodes: [], edges: [], input_schema: [], on_success_item_status: '' });
const history = new SnapshotHistory<SopDefinition>(50);
const saving = ref(false);
const publishing = ref(false);
const zoom = ref(1);
const minimap = ref(false);
const pendingSource = ref<{ nodeId: string; kind: 'success' | 'reject' } | null>(null);
const flowWrapEl = ref<HTMLElement | null>(null);
const dragType = ref('');
const selectedNodeId = ref('');
const sidePanel = ref<'inspector' | 'ai'>('inspector');

const { fitView, setViewport, onViewportChange } = useVueFlow();

onViewportChange((viewport) => {
  zoom.value = viewport.zoom;
});

const readonly = computed(() => !!template.value?.is_builtin);

const selectedNode = computed(
  () => (definition.value.nodes || []).find((node) => node.id === selectedNodeId.value) || null,
);

const paletteKinds = computed(() => [
  { type: 'agent', label: t('sop.designer.typeAgent'), hint: t('sop.designer.paletteAgent') },
  { type: 'script', label: t('sop.designer.typeScript'), hint: t('sop.designer.paletteScript') },
  { type: 'approval', label: t('sop.designer.typeApproval'), hint: t('sop.designer.paletteApproval') },
  { type: 'sop', label: t('sop.designer.typeSop'), hint: t('sop.designer.paletteSop') },
]);

/** 画布节点：位置来自 definition，业务字段挂在 data.node 上（拖拽位置由事件回写）。 */
const flowNodes = computed<Node[]>(() =>
  (definition.value.nodes || []).map((node) => ({
    id: node.id,
    type: 'sop',
    position: node.position || { x: 0, y: 0 },
    data: {
      node,
      rejectTargetName: node.reject_to
        ? (definition.value.nodes || []).find((n) => n.id === node.reject_to)?.name || node.reject_to
        : '',
    },
  })),
);

const flowEdges = computed<Edge[]>(() => {
  const edges: Edge[] = [];
  for (const edge of definition.value.edges || []) {
    edges.push({
      id: edge.id || `${edge.source}->${edge.target}`,
      source: edge.source,
      target: edge.target,
      sourceHandle: 'success',
      targetHandle: 'in',
      animated: false,
    });
  }
  // 打回关系不是 edges 表里的行（后端写 node.reject_to），单独画红色虚线
  for (const node of definition.value.nodes || []) {
    if (!node.reject_to) continue;
    if (!(definition.value.nodes || []).some((n) => n.id === node.reject_to)) continue;
    edges.push({
      id: `reject:${node.id}->${node.reject_to}`,
      source: node.id,
      target: node.reject_to,
      sourceHandle: 'reject',
      targetHandle: 'in',
      style: { stroke: 'var(--td-error-color)', strokeDasharray: '4 4' },
      animated: false,
    });
  }
  return edges;
});

const minimapBox = computed(() => {
  const nodes = definition.value.nodes || [];
  if (!nodes.length) return '';
  const box = minimapTransform(nodes, 220, 150);
  return `${box.minX} ${box.minY} ${box.width} ${box.height}`;
});

const miniNodes = computed(() =>
  (definition.value.nodes || []).map((node) => ({
    id: node.id,
    x: node.position?.x || 0,
    y: node.position?.y || 0,
    w: 220,
    h: 150,
  })),
);

const miniEdges = computed(() =>
  (definition.value.edges || []).map((edge) => {
    const from = (definition.value.nodes || []).find((n) => n.id === edge.source);
    const to = (definition.value.nodes || []).find((n) => n.id === edge.target);
    return {
      x1: (from?.position?.x || 0) + 220,
      y1: (from?.position?.y || 0) + 60,
      x2: (to?.position?.x || 0) + 220,
      y2: (to?.position?.y || 0) + 60,
      reject: false,
    };
  }).concat(
    (definition.value.nodes || [])
      .filter((n) => n.reject_to)
      .map((node) => {
        const to = (definition.value.nodes || []).find((n) => n.id === node.reject_to);
        return {
          x1: (node.position?.x || 0) + 220,
          y1: (node.position?.y || 0) + 100,
          x2: (to?.position?.x || 0) + 220,
          y2: (to?.position?.y || 0) + 100,
          reject: true,
        };
      }),
  ),
);

function commit(next: SopDefinition) {
  definition.value = next;
  history.push(next);
}

function nodeName(id: string): string {
  return (definition.value.nodes || []).find((n) => n.id === id)?.name || id;
}

function makeNode(type: string, position: { x: number; y: number }): SopNode {
  // 默认名在 designer.defaultName.* 下；缺键时回退到类型标签，避免露出原始 key
  return {
    id: `n${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`,
    name: t(`sop.designer.defaultName.${type}`, t(`sop.designer.type${type[0].toUpperCase()}${type.slice(1)}`)),
    type,
    executor: { kind: 'platform_agent', kb_scope: 'project' },
    task_prompt: '',
    task_refs: [],
    extra_rules: '',
    skills: null,
    mcp: ['know-kb'],
    artifact_outputs: [],
    gate_commands: [],
    gate_extra: '',
    on_failure: 'loop',
    reject_to: '',
    max_attempts: 3,
    max_reworks: 2,
    loop_max_iterations: 3,
    carry_over: true,
    serial_group: '',
    timeout_minutes: 60,
    position,
  };
}

function addNode(type: string, position?: { x: number; y: number }) {
  if (readonly.value) return;
  const node = makeNode(type, position || { x: 80 + Math.random() * 60, y: 80 + Math.random() * 60 });
  commit({
    ...definition.value,
    nodes: [...definition.value.nodes, node],
  });
}

function onPaletteDragStart(type: string) {
  dragType.value = type;
}

function onCanvasDrop(event: DragEvent) {
  if (readonly.value || !dragType.value) return;
  const rect = flowWrapEl.value?.getBoundingClientRect();
  if (!rect) return;
  // 近似换算：屏幕偏移 / 缩放 - 平移，足够把节点落在鼠标附近
  const position = {
    x: (event.clientX - rect.left) / zoom.value,
    y: (event.clientY - rect.top) / zoom.value,
  };
  addNode(dragType.value, position);
  dragType.value = '';
}

/** 点出口 → 再点目标节点左缘：成功建边，失败写 reject_to。 */
function onExitClick(nodeId: string, kind: 'success' | 'reject') {
  if (readonly.value) return;
  if (pendingSource.value?.nodeId === nodeId && pendingSource.value.kind === kind) {
    pendingSource.value = null;
    return;
  }
  pendingSource.value = { nodeId, kind };
  MessagePlugin.info(
    kind === 'success' ? t('sop.designer.pickSuccessTarget') : t('sop.designer.pickRejectTarget'),
  );
}

function connectToTarget(targetId: string) {
  const pending = pendingSource.value;
  if (!pending || pending.nodeId === targetId) return;
  pendingSource.value = null;
  if (pending.kind === 'success') {
    const exists = (definition.value.edges || []).some(
      (edge) => edge.source === pending.nodeId && edge.target === targetId,
    );
    if (exists) return;
    commit({
      ...definition.value,
      edges: [
        ...definition.value.edges,
        { id: `${pending.nodeId}->${targetId}`, source: pending.nodeId, target: targetId, when: 'success' },
      ],
    });
    return;
  }
  commit({
    ...definition.value,
    nodes: definition.value.nodes.map((node) =>
      node.id === pending.nodeId ? { ...node, reject_to: targetId } : node,
    ),
  });
}

/** 有待连接的出口时，点目标节点即完成连线；否则选中节点交给检查器。 */
function onNodeClick(event: { node: Node }) {
  if (pendingSource.value) {
    connectToTarget(event.node.id);
    return;
  }
  selectedNodeId.value = event.node.id;
}

/** 检查器改字段：合并进定义并 push 历史（与拖拽/连线同一套撤销栈）。 */
function onInspectorChange(fields: Partial<SopNode>) {
  if (!selectedNode.value) return;
  const nodes = definition.value.nodes.map((node) =>
    node.id === selectedNode.value!.id ? { ...node, ...fields } : node,
  );
  commit({ ...definition.value, nodes });
}

const aiNodeId = ref('');

const aiNodeName = computed(
  () => (definition.value.nodes || []).find((node) => node.id === aiNodeId.value)?.name || '',
);

/** ✦ AI 配这一步：切到 AI 面板并进入 node 模式（与检查器互斥）。 */
function onAiConfigure(nodeId: string) {
  aiNodeId.value = nodeId;
  sidePanel.value = 'ai';
}

/** AI 产出草稿：只改未保存定义并入历史栈，不自动保存/发布。 */
function onAiApply(next: SopDefinition) {
  commit(next);
}

function onNodeDragStop(event: { node: Node }) {
  if (readonly.value) return;
  const next = definition.value.nodes.map((node) =>
    node.id === event.node.id ? { ...node, position: { ...event.node.position } } : node,
  );
  commit({ ...definition.value, nodes: next });
}

function undo() {
  const snap = history.undo();
  if (snap) {
    definition.value = snap;
    if (!snap.nodes.some((node) => node.id === selectedNodeId.value)) selectedNodeId.value = '';
  }
}

function redo() {
  const snap = history.redo();
  if (snap) definition.value = snap;
}

function zoomBy(factor: number) {
  const next = Math.min(1.6, Math.max(0.2, zoom.value * factor));
  void setViewport({ x: 0, y: 0, zoom: next });
  zoom.value = next;
}

function doAutoLayout() {
  try {
    commit(autoLayout(definition.value));
    void fitView();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.designer.layoutFailed'));
  }
}

async function load() {
  const id = String(route.params.id || '');
  if (!id) return;
  try {
    const tpl = await getTemplate(id);
    template.value = tpl;
    definition.value = JSON.parse(JSON.stringify(tpl.definition));
    history.reset();
    history.push(definition.value);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.tpl.loadFailed'));
  }
}

async function saveDraft() {
  if (!template.value) return;
  saving.value = true;
  try {
    const tpl = await updateTemplate(template.value.id, { definition: definition.value });
    template.value = tpl;
    MessagePlugin.success(t('sop.designer.saved'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.designer.saveFailed'));
  } finally {
    saving.value = false;
  }
}

async function publish() {
  if (!template.value) return;
  publishing.value = true;
  try {
    await updateTemplate(template.value.id, { definition: definition.value });
    await publishTemplate(template.value.id);
    MessagePlugin.success(t('sop.designer.published'));
    await load();
  } catch (error: any) {
    const details = error?.details;
    const list = Array.isArray(details) ? details.map((d: any) => String(d?.message || d)) : [];
    MessagePlugin.error(list.length ? list.join('；') : error?.message || t('sop.tpl.publishFailed'));
  } finally {
    publishing.value = false;
  }
}

async function copyBuiltin() {
  if (!template.value) return;
  try {
    const copy = await copyTemplate(template.value.id, t('sop.tpl.copyName', { name: template.value.name }));
    MessagePlugin.success(t('sop.tpl.copied', { name: copy.name }));
    await router.push({ name: 'sopDesigner', params: { id: copy.id } });
    await load();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.tpl.copyFailed'));
  }
}

function backToList() {
  void router.push({ name: 'sopTemplates' });
}

onMounted(load);
</script>

<style lang="less" scoped src="./sopDesigner.less"></style>
