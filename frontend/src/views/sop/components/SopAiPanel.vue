<template>
  <aside class="sop-ai">
    <header class="sop-ai-head">
      <b>{{ $t('sop.ai.title') }}</b>
      <span v-if="nodeMode" class="sop-chip on">
        {{ $t('sop.ai.nodeChip', { name: nodeName }) }}
        <button type="button" class="sop-chip-x" @click="exitNodeMode">×</button>
      </span>
    </header>

    <div v-if="!nodeMode" class="sop-chips">
      <button
        v-for="chip in scenarioChips"
        :key="chip.key"
        type="button"
        class="sop-chip"
        :class="{ on: scenario === chip.key }"
        @click="pickScenario(chip.key)"
      >
        {{ chip.label }}
      </button>
    </div>

    <div ref="scrollEl" class="sop-ai-messages">
      <div v-for="(msg, index) in messages" :key="index" class="sop-ai-msg" :class="`is-${msg.role}`">
        <PmMarkdown v-if="msg.role === 'assistant'" :content="msg.text" />
        <p v-else>{{ msg.text }}</p>
      </div>
      <div v-if="!messages.length" class="sop-ai-empty">{{ $t('sop.ai.empty') }}</div>
    </div>

    <div v-if="cards.length" class="sop-diffs">
      <div class="sop-diffs-head">
        <b>{{ $t('sop.ai.pending', { n: cards.length }) }}</b>
        <button type="button" variant="text" @click="discardDraft">{{ $t('sop.ai.discard') }}</button>
      </div>
      <div v-for="(card, index) in cards" :key="index" class="sop-diff">
        <div class="sop-diff-head">
          <span class="sop-diff-node">{{ card.nodeName }}</span>
          <b>{{ card.field }}</b>
          <button type="button" class="sop-diff-apply" @click="applyOne(card)">
            {{ $t('sop.ai.applyOne') }}
          </button>
        </div>
        <div class="sop-diff-values">
          <span class="from">{{ card.from }}</span>
          <span class="arrow">→</span>
          <span class="to">{{ card.to }}</span>
        </div>
        <div v-if="card.reason" class="sop-diff-reason">{{ card.reason }}</div>
      </div>
      <div class="sop-diffs-actions">
        <t-button size="small" theme="primary" :disabled="readonly" @click="applyAll">
          {{ $t('sop.ai.applyAll') }}
        </t-button>
      </div>
    </div>

    <div v-if="skipped.length" class="sop-skipped">
      <b>{{ $t('sop.ai.skipped', { n: skipped.length }) }}</b>
      <ul>
        <li v-for="(item, index) in skipped" :key="index">{{ item.op }}：{{ item.reason }}</li>
      </ul>
    </div>

    <div class="sop-ai-footer">
      <t-input
        v-model="instruction"
        :disabled="streaming"
        :placeholder="$t('sop.ai.placeholder')"
        @enter="send"
      />
      <t-button size="small" theme="primary" :disabled="streaming || !instruction.trim()" @click="send">
        {{ $t('sop.ai.send') }}
      </t-button>
      <t-button v-if="nodeMode && !instruction.trim()" size="small" variant="outline" :loading="streaming" @click="generateNodeConfig">
        {{ $t('sop.ai.generateConfig') }}
      </t-button>
    </div>

    <div v-if="readonly" class="sop-ai-readonly">{{ $t('sop.ai.readonlyHint') }}</div>
  </aside>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  orchestrate,
  type SopApplyOpsResult,
  type SopDefinition,
} from '@/api/sop';
import { diffDefinitions } from '@/api/sop/helpers';
import type { DiffCard } from '@/api/sop';
import PmMarkdown from '@/views/pm/components/PmMarkdown.vue';

const props = defineProps<{
  templateId: string;
  def: SopDefinition;
  readonly: boolean;
  nodeId?: string;
  nodeName?: string;
}>();

const emit = defineEmits<{ 'apply-definition': [SopDefinition]; 'exit-node-mode': [] }>();

const { t } = useI18n();

const instruction = ref('');
const messages = ref<{ role: 'user' | 'assistant'; text: string }[]>([]);
const streaming = ref(false);
const scenario = ref('product');
// node 模式的草稿：后端只回合并后的定义，差异按逐字段比对还原
const pendingDefinition = ref<SopDefinition | null>(null);
const skipped = ref<{ op: string; reason: string }[]>([]);
const scrollEl = ref<HTMLElement | null>(null);

const nodeMode = computed(() => !!props.nodeId);

const scenarioChips = computed(() => [
  { key: 'product', label: t('sop.ai.chipProduct') },
  { key: 'release', label: t('sop.ai.chipRelease') },
  { key: 'research', label: t('sop.ai.chipResearch') },
  { key: 'custom', label: t('sop.ai.chipCustom') },
]);

const cards = computed<DiffCard[]>(() =>
  pendingDefinition.value ? diffDefinitions(props.def, pendingDefinition.value) : [],
);

async function scrollToBottom() {
  await nextTick();
  if (scrollEl.value) scrollEl.value.scrollTop = scrollEl.value.scrollHeight;
}

function pickScenario(key: string) {
  scenario.value = key;
  instruction.value = t(`sop.ai.scenarioPrompt.${key}`);
}

/**
 * AI 编排一律草稿：apply 只改**画布未保存定义**（由设计器入历史栈），
 * 不自动保存、不自动发布。
 */
function absorbResult(result: SopApplyOpsResult) {
  skipped.value = result.skipped || [];
  pendingDefinition.value = result.definition || null;
}

async function send() {
  const text = instruction.value.trim();
  if (!text || streaming.value) return;
  instruction.value = '';
  streaming.value = true;
  messages.value.push({ role: 'user', text });
  await scrollToBottom();

  try {
    const result = await orchestrate({
      template_id: props.templateId,
      mode: nodeMode.value ? 'node' : 'flow',
      instruction: text,
      node_id: props.nodeId,
      definition: props.def,
      inventory: {},
    });
    if ('applied' in result) {
      // node 模式：返回 applied/skipped + 合并后的草稿
      absorbResult(result as SopApplyOpsResult);
      messages.value.push({ role: 'assistant', text: t('sop.ai.nodeReady') });
    } else {
      // flow 模式：整份定义草稿（含 warnings）
      const draft = result as { definition: SopDefinition; warnings: string[] };
      if (draft.warnings?.length) {
        skipped.value = draft.warnings.map((reason) => ({ op: 'flow', reason }));
      }
      emit('apply-definition', draft.definition);
      messages.value.push({ role: 'assistant', text: t('sop.ai.flowApplied') });
    }
  } catch (error: any) {
    messages.value.push({ role: 'assistant', text: error?.message || t('sop.ai.failed') });
    MessagePlugin.error(error?.message || t('sop.ai.failed'));
  } finally {
    streaming.value = false;
    await scrollToBottom();
  }
}

/** 零输入「生成配置」：node 模式推断该节点的字段。 */
async function generateNodeConfig() {
  instruction.value = t('sop.ai.generateConfigPrompt');
  await send();
}

/** 单独应用一张卡：把草稿里该节点该字段搬进画布，草稿其余项保留。 */
function applyOne(card: DiffCard) {
  if (props.readonly) {
    MessagePlugin.warning(t('sop.ai.readonlyHint'));
    return;
  }
  const draft = pendingDefinition.value;
  if (!draft) return;
  const key = card.fieldKey || '';
  const next = {
    ...props.def,
    nodes: (props.def.nodes || []).map((node) => {
      if (node.id !== card.nodeId) return node;
      const source = (draft.nodes || []).find((item) => item.id === card.nodeId);
      if (!source) return node;
      if (!key) return { ...source };
      return { ...node, [key]: (source as unknown as Record<string, unknown>)[key] };
    }),
  };
  emit('apply-definition', next);
  // 草稿里把这一项回填成目标值，差异卡随之消失；其余字段继续保留
  pendingDefinition.value = {
    ...draft,
    nodes: (draft.nodes || []).map((node) =>
      node.id === card.nodeId && key ? { ...node, [key]: (next.nodes.find((item) => item.id === node.id) as any)?.[key] } : node,
    ),
  };
}

function applyAll() {
  if (props.readonly) {
    MessagePlugin.warning(t('sop.ai.readonlyHint'));
    return;
  }
  if (pendingDefinition.value) emit('apply-definition', pendingDefinition.value);
  pendingDefinition.value = null;
  skipped.value = [];
}

function discardDraft() {
  pendingDefinition.value = null;
  skipped.value = [];
}

function exitNodeMode() {
  emit('exit-node-mode');
}
</script>

<style lang="less" scoped src="./sopAiPanel.less"></style>
