<template>
  <!-- 创建会议弹窗：表单提交成功后 emit('created', room) 交由父级刷新列表并跳转 -->
  <t-dialog :visible="visible" :header="$t('meeting.create')" :width="600"
    :close-on-overlay-click="false" attach="body"
    :confirm-btn="{ content: $t('meeting.createAndEnter'), loading: submitting }"
    :cancel-btn="{ content: $t('meeting.cancel') }" @confirm="submit"
    @close="emit('update:visible', false)">
    <t-form :data="form" :label-width="120" class="create-form">
      <t-form-item :label="$t('meeting.title')" required>
        <t-input v-model="form.title" :maxlength="100" />
      </t-form-item>
      <t-form-item :label="$t('meeting.topic')">
        <t-textarea v-model="form.description" :autosize="{ minRows: 2, maxRows: 4 }"
          :placeholder="$t('meeting.topicPlaceholder')" />
      </t-form-item>
      <t-form-item :label="$t('meeting.selectAgents')" required>
        <div class="agent-picker">
          <t-select v-model="form.agentIds" multiple filterable :placeholder="$t('meeting.selectAgents')">
            <t-option v-for="a in agents" :key="a.id" :value="a.id" :label="a.name">
              <span class="opt-name">{{ a.name }}</span>
              <!-- 模型来源标签：已绑定具体模型 / 跟随系统默认 -->
              <span class="opt-model">{{ modelTag(a) }}</span>
            </t-option>
          </t-select>
          <!-- 专家异构引导：所选智能体模型同质时提示，避免讨论观点趋同 -->
          <div v-if="homogeneousHint" class="model-hint">
            {{ $t('meeting.modelHeterogeneousHint') }}
          </div>
        </div>
      </t-form-item>
      <t-form-item :label="$t('meeting.selectKBs')">
        <t-select v-model="form.knowledgeBaseIds" multiple filterable :placeholder="$t('meeting.selectKBs')">
          <t-option v-for="kb in knowledgeBases" :key="kb.id" :value="kb.id" :label="kb.name" />
        </t-select>
      </t-form-item>
      <t-form-item :label="$t('meeting.broadcastMode')">
        <t-radio-group v-model="broadcastMode">
          <t-radio value="sequential">{{ $t('meeting.modeSequential') }}</t-radio>
          <t-radio value="parallel">{{ $t('meeting.modeParallel') }}</t-radio>
        </t-radio-group>
      </t-form-item>
      <t-form-item :label="$t('meeting.routingMode')">
        <t-radio-group v-model="routingMode">
          <t-radio value="host-assign">{{ $t('meeting.routingHostAssign') }}</t-radio>
          <t-radio value="mention">{{ $t('meeting.routingMention') }}</t-radio>
          <t-radio value="free">{{ $t('meeting.routingFree') }}</t-radio>
        </t-radio-group>
        <div class="routing-hint">{{ $t('meeting.routingHint') }}</div>
      </t-form-item>
      <t-form-item :label="$t('meeting.hostEveryNRounds')">
        <t-select v-model="form.hostEveryNRounds" class="intervene-select">
          <t-option :value="0" :label="$t('meeting.hostEveryNRoundsOff')" />
          <t-option v-for="n in HOST_INTERVENE_OPTIONS" :key="n" :value="n"
            :label="$t('meeting.hostInterveneEvery', { n })" />
        </t-select>
        <div class="routing-hint">{{ $t('meeting.hostEveryNRoundsHint') }}</div>
      </t-form-item>
      <t-form-item :label="$t('meeting.consensusDetection')">
        <t-switch v-model="form.consensusDetectionEnabled" />
        <div class="routing-hint">{{ $t('meeting.consensusDetectionHint') }}</div>
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  createMeeting, isModelHomogeneous,
  type BroadcastMode, type RoutingMode, type MeetingRoom,
} from '@/api/meeting';
import { listAgents, type CustomAgent } from '@/api/agent';   // 复用既有智能体列表接口
import { listKnowledgeBases } from '@/api/knowledge-base';   // 复用既有知识库列表接口

const props = defineProps<{ visible: boolean }>();
const emit = defineEmits<{ 'update:visible': [boolean]; created: [MeetingRoom] }>();

const { t } = useI18n();
// 智能体选项：列表接口已带 config.model_id，用于模型异构引导
const agents = ref<CustomAgent[]>([]);
const knowledgeBases = ref<Array<{ id: string; name: string }>>([]);
const submitting = ref(false);
let optionsLoaded = false; // 选项数据仅需拉取一次

const form = reactive({
  title: '', description: '',
  agentIds: [] as string[], knowledgeBaseIds: [] as string[],
  broadcastMode: 'sequential' as BroadcastMode,
  routingMode: 'host-assign' as RoutingMode,
  hostEveryNRounds: 0, // 0=主持人不介入（默认），每 N 个子轮总结并重定向
  consensusDetectionEnabled: false, // 裁判 + 共识检测开关（默认关闭）
});

// 可选的主持人介入间隔（子轮数）；0 单独作为“不介入”选项
const HOST_INTERVENE_OPTIONS = [2, 3, 5, 10];

// t-radio-group 的变更值类型较宽，用带类型 setter 的 computed 收敛回具体联合类型
const broadcastMode = computed<BroadcastMode>({
  get: () => form.broadcastMode,
  set: (val) => { form.broadcastMode = val; },
});
const routingMode = computed<RoutingMode>({
  get: () => form.routingMode,
  set: (val) => { form.routingMode = val; },
});

// 已选智能体的模型标识列表（空串代表跟随系统默认模型）
const selectedModelKeys = computed(() =>
  form.agentIds.map((id) => agents.value.find((a) => a.id === id)?.config?.model_id),
);
// 模型同质（≥2 个已选且模型标识唯一）时给出异构引导提示
const homogeneousHint = computed(() => isModelHomogeneous(selectedModelKeys.value));

// 选项右侧的模型来源标签
function modelTag(a: CustomAgent): string {
  return a.config?.model_id ? t('meeting.modelBound') : t('meeting.modelDefault');
}

// 首次打开时懒加载智能体/知识库选项，避免列表页初始化即发起请求
watch(() => props.visible, (v) => {
  if (v) void loadOptions();
});

async function loadOptions() {
  if (optionsLoaded || submitting.value) return;
  try {
    // listAgents / listKnowledgeBases 均返回 { data: [...] } 信封，这里统一解包取数组
    const [agentRes, kbRes] = await Promise.all([listAgents(), listKnowledgeBases()]);
    agents.value = agentRes?.data ?? [];
    knowledgeBases.value = (kbRes?.data ?? []) as Array<{ id: string; name: string }>;
    optionsLoaded = true;
  } catch {
    MessagePlugin.error(t('meeting.loadFailed'));
  }
}

// 重置表单为初始态，供每次打开弹窗时调用
function resetForm() {
  form.title = '';
  form.description = '';
  form.agentIds = [];
  form.knowledgeBaseIds = [];
  form.broadcastMode = 'sequential';
  form.routingMode = 'host-assign';
  form.hostEveryNRounds = 0;
  form.consensusDetectionEnabled = false;
}

async function submit() {
  if (!form.title.trim()) {
    MessagePlugin.warning(t('meeting.title'));
    return;
  }
  if (!form.agentIds.length) {
    MessagePlugin.warning(t('meeting.selectAgents'));
    return;
  }
  submitting.value = true;
  try {
    const room = await createMeeting(form);
    resetForm();
    emit('update:visible', false);
    emit('created', room);
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.createFailed'));
  } finally {
    submitting.value = false;
  }
}
</script>

<style lang="less" scoped>
.create-form {
  padding-top: var(--app-space-2);
}

.agent-picker {
  width: 100%;
}

// 下拉选项中的模型来源标签：弱化展示，避免抢占名称视觉权重
.opt-model {
  margin-left: 8px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.model-hint {
  margin-top: 6px;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-warning-color);
}

.routing-hint {
  margin-top: 6px;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-placeholder);
}
</style>
