<template>
  <aside class="sop-inspector">
    <header class="sop-inspector-head">
      <span class="sop-type" :class="`is-${node.type}`">{{ typeLabel }}</span>
      <t-button size="small" variant="outline" :disabled="readonly" @click="emit('ai-configure', node.id)">
        ✦ {{ $t('sop.insp.aiConfigure') }}
      </t-button>
    </header>

    <label class="sop-field">
      <span>{{ $t('sop.insp.stepName') }}</span>
      <t-input :model-value="node.name" @update:model-value="update('name', $event)" />
    </label>

    <!-- ① 由谁执行 -->
    <section class="sop-step">
      <header class="sop-step-head">
        <i class="sop-step-no">1</i>
        <b>{{ $t('sop.insp.step1') }}</b>
      </header>

      <template v-if="node.type === 'agent'">
        <t-radio-group :model-value="node.executor.kind" variant="default-filled" :disabled="readonly" @update:model-value="onExecutorChange">
          <t-radio-button value="platform_agent">{{ $t('sop.insp.execAgent') }}</t-radio-button>
          <t-radio-button value="cli">{{ $t('sop.insp.execCli') }}</t-radio-button>
        </t-radio-group>

        <template v-if="node.executor.kind === 'cli'">
          <div class="sop-cols2">
            <label class="sop-field">
              <span>{{ $t('sop.insp.cliKind') }}</span>
              <t-select :model-value="node.executor.cli_kind || ''" :options="cliOptions" :disabled="readonly"
                @update:model-value="updateExecutor('cli_kind', $event)" />
            </label>
            <label class="sop-field">
              <span>
                {{ $t('sop.insp.model') }}
                <SopHelpTip :text="$t('sop.insp.modelTip')" />
              </span>
              <t-select :model-value="node.executor.model || ''" :options="modelOptions" :disabled="readonly"
                @update:model-value="updateExecutor('model', $event)" />
            </label>
          </div>
          <label class="sop-field">
            <span>{{ $t('sop.insp.roleOptional') }}</span>
            <t-select :model-value="node.executor.role_id || ''" :options="roleOptions" clearable :disabled="readonly"
              @update:model-value="updateExecutor('role_id', $event)" />
          </label>
          <div class="sop-cols2">
            <label class="sop-field">
              <span>
                {{ $t('sop.insp.kbScope') }}
                <SopHelpTip :text="$t('sop.insp.kbScopeTip')" />
              </span>
              <t-select :model-value="node.executor.kb_scope || 'project'" :options="kbScopeOptions" :disabled="readonly"
                @update:model-value="updateExecutor('kb_scope', $event)" />
            </label>
            <label v-if="node.executor.kb_scope === 'custom'" class="sop-field">
              <span>{{ $t('sop.insp.kbPick') }}</span>
              <t-select v-model="customKbIds" multiple :options="kbOptions" :disabled="readonly"
                @update:model-value="updateExecutor('kb_ids', $event)" />
            </label>
          </div>
          <small class="sop-hint">{{ $t('sop.insp.privateHome') }}</small>
        </template>

        <template v-else>
          <label class="sop-field">
            <span>{{ $t('sop.insp.role') }}</span>
            <t-select :model-value="node.executor.agent_id || ''" :options="roleOptions" :disabled="readonly"
              @update:model-value="updateExecutor('agent_id', $event)" />
          </label>
          <div v-if="!node.executor.agent_id" class="sop-hint">
            {{ $t('sop.insp.noRoleSkills') }}
          </div>
        </template>
      </template>

      <template v-else-if="node.type === 'script'">
        <t-collapse :default-value="[]" class="sop-collapse">
          <t-collapse-panel :header="$t('sop.insp.scriptBody')">
            <textarea
              class="sop-textarea mono"
              rows="6"
              :value="node.script || ''"
              :readonly="readonly"
              @input="update('script', ($event.target as HTMLTextAreaElement).value)"
            />
          </t-collapse-panel>
        </t-collapse>
      </template>

      <template v-else-if="node.type === 'approval'">
        <p class="sop-hint">{{ $t('sop.insp.approvalHint') }}</p>
        <div class="sop-cols2">
          <label class="sop-field">
            <span>{{ $t('sop.insp.approvalTimeout') }}</span>
            <t-input-number :value="node.approval_timeout_hours || 0" :min="0" :disabled="readonly"
              @update:model-value="update('approval_timeout_hours', Number($event) || 0)" />
          </label>
          <label class="sop-field">
            <span>{{ $t('sop.insp.approvalAction') }}</span>
            <t-select :model-value="node.approval_timeout_action || 'notify'" :options="approvalActionOptions"
              :disabled="readonly" @update:model-value="update('approval_timeout_action', $event)" />
          </label>
        </div>
      </template>

      <template v-else>
        <label class="sop-field">
          <span>{{ $t('sop.insp.childTemplate') }}</span>
          <t-select :model-value="node.child_template_id || ''" :options="templateOptions" clearable :disabled="readonly"
            @update:model-value="update('child_template_id', $event)" />
        </label>
      </template>
    </section>

    <!-- ② 做什么 -->
    <section class="sop-step">
      <header class="sop-step-head">
        <i class="sop-step-no">2</i>
        <b>{{ $t('sop.insp.step2') }}</b>
        <SopHelpTip :text="$t('sop.insp.step2Tip')" />
      </header>
      <textarea
        class="sop-textarea"
        rows="5"
        :value="node.task_prompt"
        :readonly="readonly"
        :placeholder="$t('sop.insp.taskPromptPlaceholder')"
        @input="update('task_prompt', ($event.target as HTMLTextAreaElement).value)"
      />
      <label class="sop-field">
        <span>{{ $t('sop.insp.taskRefs') }}</span>
        <SopMultiSelect
          :options="stepOptions"
          :model-value="node.task_refs || []"
          :all-label="$t('sop.insp.taskRefsAll')"
          :placeholder="$t('sop.insp.taskRefsNone')"
          @update:model-value="update('task_refs', $event || [])"
        />
      </label>
    </section>

    <!-- ③ 遵循什么规则 · 用哪些能力 -->
    <section class="sop-step">
      <header class="sop-step-head">
        <i class="sop-step-no">3</i>
        <b>{{ $t('sop.insp.step3') }}</b>
        <SopHelpTip :text="$t('sop.insp.step3Tip')" />
      </header>
      <div class="sop-rules">
        {{ $t('sop.insp.roleRulesHint') }}
      </div>
      <textarea
        class="sop-textarea"
        rows="3"
        :value="node.extra_rules"
        :readonly="readonly"
        :placeholder="$t('sop.insp.extraRulesPlaceholder')"
        @input="update('extra_rules', ($event.target as HTMLTextAreaElement).value)"
      />
      <label class="sop-field">
        <span>{{ $t('sop.insp.skills') }}</span>
        <SopMultiSelect
          :options="skillOptions"
          :model-value="node.skills"
          :all-label="$t('sop.insp.skillsAll')"
          :placeholder="$t('sop.insp.skillsNone')"
          @update:model-value="update('skills', $event)"
        />
      </label>
      <label class="sop-field">
        <span>{{ $t('sop.insp.mcp') }}</span>
        <SopMultiSelect
          :options="mcpOptions"
          :model-value="node.mcp || []"
          :all-label="$t('sop.insp.mcpAll')"
          :placeholder="$t('sop.insp.mcpNone')"
          @update:model-value="update('mcp', $event || [])"
        />
      </label>
    </section>

    <!-- ④ 产出什么 · 怎么校验 -->
    <section class="sop-step">
      <header class="sop-step-head">
        <i class="sop-step-no">4</i>
        <b>{{ $t('sop.insp.step4') }}</b>
        <SopHelpTip :text="$t('sop.insp.step4Tip')" />
      </header>

      <div class="sop-card">
        <div class="sop-card-title">{{ $t('sop.insp.reportStatus') }}</div>
        <div class="sop-report">
          <span v-for="state in reportStates" :key="state" class="sop-tag">{{ state }}</span>
        </div>
      </div>

      <div class="sop-card">
        <div class="sop-card-title">
          {{ $t('sop.insp.artifacts') }}
          <t-button size="small" variant="text" :disabled="readonly" @click="addArtifact">＋</t-button>
        </div>
        <div v-for="(artifact, index) in node.artifact_outputs || []" :key="index" class="sop-artifact">
          <t-input :model-value="artifact.name" :disabled="readonly" :placeholder="$t('sop.insp.artifactName')"
            @update:model-value="updateArtifact(index, 'name', $event)" />
          <t-input :model-value="artifact.path" :disabled="readonly" :placeholder="$t('sop.insp.artifactPath')"
            @update:model-value="updateArtifact(index, 'path', $event)" />
          <t-select :model-value="artifact.check?.type || 'exists'" :options="checkTypeOptions" :disabled="readonly"
            @update:model-value="updateCheck(index, 'type', $event)" />
          <t-input v-if="artifact.check?.type === 'contains'" :model-value="artifact.check?.keyword || ''"
            :disabled="readonly" :placeholder="$t('sop.insp.checkKeyword')"
            @update:model-value="updateCheck(index, 'keyword', $event)" />
          <t-input v-if="artifact.check?.type === 'command'" :model-value="artifact.check?.command || ''"
            :disabled="readonly" placeholder="make test"
            @update:model-value="updateCheck(index, 'command', $event)" />
          <small class="sop-hint">{{ gateCheckSummary(artifact.check) }}</small>
          <t-button size="small" variant="text" theme="danger" :disabled="readonly" @click="removeArtifact(index)">
            {{ $t('common.remove') }}
          </t-button>
        </div>
        <div v-if="!(node.artifact_outputs || []).length" class="sop-hint">{{ $t('sop.insp.noArtifacts') }}</div>
      </div>

      <t-collapse :default-value="[]">
        <t-collapse-panel :header="$t('sop.insp.extraCheck')">
          <textarea
            class="sop-textarea mono"
            rows="3"
            :value="node.gate_extra"
            :readonly="readonly"
            placeholder="&&"
            @input="update('gate_extra', ($event.target as HTMLTextAreaElement).value)"
          />
        </t-collapse-panel>
      </t-collapse>

      <div class="sop-switchrow">
        <t-switch :value="!!node.auto_ingest" :disabled="readonly" @update:model-value="update('auto_ingest', $event)" />
        <span>{{ $t('sop.insp.autoIngest') }}</span>
      </div>
    </section>

    <!-- ⑤ 失败了怎么办 -->
    <section class="sop-step">
      <header class="sop-step-head">
        <i class="sop-step-no">5</i>
        <b>{{ $t('sop.insp.step5') }}</b>
        <SopHelpTip :text="failureHelpText(node.on_failure)" />
      </header>
      <div class="sop-fail-grid">
        <button
          v-for="option in failureOptions"
          :key="option.value"
          type="button"
          class="sop-fail-card"
          :class="{ on: node.on_failure === option.value }"
          :disabled="readonly"
          @click="update('on_failure', option.value)"
        >
          <b>{{ option.icon }} {{ option.label }}</b>
          <span>{{ option.hint }}</span>
        </button>
      </div>

      <div v-if="node.on_failure === 'loop'" class="sop-cols2">
        <label class="sop-field">
          <span>{{ $t('sop.insp.maxAttempts') }}</span>
          <t-input-number :value="node.max_attempts" :min="1" :max="10" :disabled="readonly"
            @update:model-value="update('max_attempts', Number($event) || 3)" />
        </label>
      </div>

      <div v-if="node.on_failure === 'reject'" class="sop-field">
        <span>{{ $t('sop.insp.rejectTo') }}</span>
        <t-select :model-value="node.reject_to || ''" :options="candidates" :disabled="readonly"
          @update:model-value="update('reject_to', $event)" />
      </div>

      <div class="sop-cols2">
        <label class="sop-field">
          <span>{{ $t('sop.insp.maxReworks') }}</span>
          <t-input-number :value="node.max_reworks" :min="0" :max="10" :disabled="readonly"
            @update:model-value="update('max_reworks', Number($event) || 0)" />
        </label>
        <label class="sop-field">
          <span>{{ $t('sop.insp.timeout') }}</span>
          <t-input-number :value="node.timeout_minutes" :min="1" :max="1440" :disabled="readonly"
            @update:model-value="update('timeout_minutes', Number($event) || 60)" />
        </label>
      </div>

      <div class="sop-switchrow">
        <t-switch :value="!!node.carry_over" :disabled="readonly" @update:model-value="update('carry_over', $event)" />
        <span>{{ $t('sop.insp.carryOver') }}</span>
      </div>
    </section>
  </aside>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { storeToRefs } from 'pinia';
import type { SopArtifactOutput, SopDefinition, SopNode } from '@/api/sop';
import { ITEM_STATUS_LABELS, gateCheckSummary } from '@/api/sop/helpers';
import { useSopStore } from '@/stores/sop';
import SopHelpTip from './SopHelpTip.vue';
import SopMultiSelect from './SopMultiSelect.vue';
import { failureHelpText, rejectCandidates } from '../inspector/defaults';

const props = defineProps<{
  node: SopNode;
  def: SopDefinition;
  readonly: boolean;
  /** 角色/技能/MCP/知识库清单（设计器从真实 API 取，取不到时为空）。 */
  inventory?: {
    roles?: { value: string; label: string; skillCount?: number }[];
    skills?: { value: string; label: string }[];
    mcps?: { value: string; label: string }[];
    models?: { value: string; label: string }[];
    kbs?: { value: string; label: string }[];
    templates?: { value: string; label: string }[];
  };
}>();

const emit = defineEmits<{ change: [Partial<SopNode>]; 'ai-configure': [string] }>();

const { t } = useI18n();
const store = useSopStore();
const { templates } = storeToRefs(store);
const customKbIds = ref<string[]>(props.node.executor?.kb_ids || []);

const typeLabel = computed(() => {
  switch (props.node.type) {
    case 'script':
      return t('sop.designer.typeScript');
    case 'approval':
      return t('sop.designer.typeApproval');
    case 'sop':
      return t('sop.designer.typeSop');
    case 'agent':
    default:
      return t('sop.designer.typeAgent');
  }
});

const cliOptions = computed(() => [
  'opencode', 'cursor-agent', 'agy', 'kimi', 'pi', 'dsh', 'zcode', 'claude', 'codebuddy', 'codex',
].map((kind) => ({ value: kind, label: kind })));

const modelOptions = computed(() => [
  { value: '', label: t('sop.insp.modelDefault') },
  ...(props.inventory?.models || []),
]);

const roleOptions = computed(() => [
  { value: '', label: t('sop.insp.roleNone') },
  ...(props.inventory?.roles || []),
]);

const kbScopeOptions = computed(() => [
  { value: 'project', label: t('sop.insp.kbProject') },
  { value: 'custom', label: t('sop.insp.kbCustom') },
  { value: 'all', label: t('sop.insp.kbAll') },
]);

const kbOptions = computed(() => props.inventory?.kbs || []);

const skillOptions = computed(() => props.inventory?.skills || []);

const mcpOptions = computed(() => props.inventory?.mcps || [{ value: 'know-kb', label: 'know-kb' }]);

const stepOptions = computed(() =>
  (props.def.nodes || [])
    .filter((node) => node.id !== props.node.id)
    .map((node) => ({ value: node.id, label: node.name || node.id })),
);

const templateOptions = computed(() =>
  (templates.value || [])
    .filter((tpl) => tpl.status === 'published')
    .map((tpl) => ({ value: tpl.id, label: tpl.name })),
);

const reportStates = computed(() => [
  t('sop.insp.reportPass'),
  t('sop.insp.reportRework'),
  t('sop.insp.reportFail'),
  t('sop.insp.reportHuman'),
]);

const checkTypeOptions = computed(() => [
  { value: 'exists', label: t('sop.insp.checkExists') },
  { value: 'contains', label: t('sop.insp.checkContains') },
  { value: 'command', label: t('sop.insp.checkCommand') },
]);

const approvalActionOptions = computed(() => [
  { value: 'notify', label: t('sop.insp.approvalNotify') },
  { value: 'fail', label: t('sop.insp.approvalFail') },
]);

const failureOptions = computed(() => [
  { value: 'loop', icon: '⟳', label: t('sop.designer.failLoop'), hint: t('sop.insp.failLoopHint') },
  { value: 'reject', icon: '↩', label: t('sop.designer.failReject'), hint: t('sop.insp.failRejectHint') },
  { value: 'human', icon: '⏸', label: t('sop.designer.failHuman'), hint: t('sop.insp.failHumanHint') },
  { value: 'fail', icon: '✕', label: t('sop.designer.failFail'), hint: t('sop.insp.failFailHint') },
]);

const candidates = computed(() =>
  rejectCandidates(props.def, props.node.id).map((node) => ({ value: node.id, label: node.name })),
);

function update<K extends keyof SopNode>(key: K, value: SopNode[K]) {
  if (props.readonly) return;
  emit('change', { [key]: value } as Partial<SopNode>);
}

function updateExecutor(key: string, value: unknown) {
  if (props.readonly) return;
  emit('change', { executor: { ...props.node.executor, [key]: value === '' ? undefined : value } });
}

function onExecutorChange(kind: string) {
  if (props.readonly) return;
  emit('change', { executor: { ...props.node.executor, kind } });
}

function addArtifact() {
  if (props.readonly) return;
  const outputs: SopArtifactOutput[] = [
    ...(props.node.artifact_outputs || []),
    { name: '', path: '', media_type: 'text/markdown', required: true, check: { type: 'exists' } },
  ];
  update('artifact_outputs', outputs);
}

function updateArtifact(index: number, key: 'name' | 'path', value: string) {
  const outputs = (props.node.artifact_outputs || []).map((item, i) =>
    i === index ? { ...item, [key]: value } : item,
  );
  update('artifact_outputs', outputs);
}

function updateCheck(index: number, key: 'type' | 'keyword' | 'command', value: string) {
  const outputs = (props.node.artifact_outputs || []).map((item, i) =>
    i === index ? { ...item, check: { ...item.check, [key]: value } } : item,
  );
  update('artifact_outputs', outputs);
}

function removeArtifact(index: number) {
  update('artifact_outputs', (props.node.artifact_outputs || []).filter((_, i) => i !== index));
}
</script>

<style lang="less" scoped src="./sopInspector.less"></style>
