<template>
  <t-dialog
    v-if="visible"
    :visible="true"
    width="980px"
    placement="center"
    :header="$t('sop.create.title')"
    :confirm-btn="null"
    :cancel-btn="null"
    class="sop-create-dialog"
    @close="close"
  >
    <div class="sop-create">
      <!-- 左：表单（字段区可滚动，操作按钮固定在底部） -->
      <div class="sop-create-form">
        <div class="sop-create-fields">
          <label class="sop-field">
            <span>{{ $t('sop.create.itemTitle') }} *</span>
            <t-input v-model="form.title" :maxlength="200" :placeholder="$t('sop.create.titlePlaceholder')" />
          </label>

          <label class="sop-field">
            <span>{{ $t('sop.create.idea') }}</span>
            <textarea
              v-model="form.description"
              class="sop-textarea"
              rows="6"
              maxlength="2000"
              :placeholder="$t('sop.create.ideaPlaceholder')"
            />
            <small class="sop-counter">{{ form.description.length }}/2000</small>
          </label>

          <div class="sop-field-row">
            <label class="sop-field">
              <span>{{ $t('sop.items.colSource') }} *</span>
              <t-select v-model="form.source" :options="sourceOptions" />
            </label>
            <label class="sop-field">
              <span>{{ $t('sop.drawer.proposer') }}</span>
              <t-input v-model="form.proposer" :maxlength="200" />
            </label>
          </div>

          <div class="sop-field-row">
            <label class="sop-field">
              <span>{{ $t('sop.items.colPriority') }}</span>
              <t-radio-group class="sop-choice" v-model="form.priority" variant="default-filled">
                <t-radio-button value="P0">P0</t-radio-button>
                <t-radio-button value="P1">P1</t-radio-button>
                <t-radio-button value="P2">P2</t-radio-button>
                <t-radio-button value="P3">P3</t-radio-button>
              </t-radio-group>
            </label>
            <label class="sop-field">
              <span>{{ $t('sop.drawer.kind') }}</span>
              <t-select v-model="form.kind" :options="kindOptions" />
            </label>
          </div>

          <label class="sop-field">
            <span>{{ $t('sop.create.template') }}</span>
            <t-select v-model="form.template_id" :options="templateOptions" clearable
              :placeholder="$t('sop.items.templateUnbound')" />
          </label>

          <label v-if="bigParentOptions.length" class="sop-field">
            <span>{{ $t('sop.split.parentPicker') }}</span>
            <t-select
              v-model="form.parent_id"
              :options="bigParentOptions"
              clearable
              :placeholder="$t('sop.split.parentNone')"
            />
            <small class="sop-hint">{{ $t('sop.split.parentHint') }}</small>
          </label>

          <label class="sop-field">
            <span>{{ $t('sop.create.attachments') }}</span>
            <input type="file" multiple @change="onFilesPicked" />
            <small v-if="attachments.length" class="sop-hint">
              {{ $t('sop.create.attachmentsPending', { n: attachments.length }) }}
            </small>
          </label>
        </div>

        <div class="sop-create-actions">
          <t-button variant="outline" @click="close">{{ $t('common.cancel') }}</t-button>
          <t-button variant="outline" :loading="submitting" @click="submit('backlog')">
            {{ $t('sop.create.saveDraft') }}
          </t-button>
          <t-button theme="primary" :loading="submitting" @click="submit('todo')">
            {{ $t('sop.create.confirm') }}
          </t-button>
        </div>
      </div>

      <!-- 右：AI 拆解面板（与左栏等高，输入框固定在面板底部） -->
      <div class="sop-create-ai">
        <div class="sop-ai-head">
          <t-select v-model="agentId" :options="agentOptions" size="small"
            :placeholder="$t('sop.create.agentPlaceholder')" />
        </div>

        <div ref="scrollEl" class="sop-ai-messages">
          <div v-if="!messages.length" class="sop-ai-empty">
            <p>{{ $t('sop.create.aiEmpty') }}</p>
            <t-button size="small" theme="primary" :loading="streaming" @click="startBreakdown">
              {{ $t('sop.create.startBreakdown') }}
            </t-button>
          </div>
          <div v-for="(msg, index) in messages" :key="index" class="sop-ai-msg" :class="`is-${msg.role}`">
            <PmMarkdown v-if="msg.role === 'assistant'" :content="msg.text" />
            <p v-else>{{ msg.text }}</p>
          </div>
          <div v-if="card" class="sop-ai-card">
            <h5>{{ $t('sop.create.cardGoal') }}：{{ card.goal }}</h5>
            <ul>
              <li v-for="(sub, index) in card.sub_items" :key="index" class="sop-sub-row">
                <input
                  type="checkbox"
                  :checked="pickedSubs.has(index)"
                  @change="toggleSub(index, ($event.target as HTMLInputElement).checked)"
                />
                <div>
                  <b>{{ sub.title }}</b>
                  <span v-if="sub.executor" class="sop-tag sop-tag--gray">{{ executorLabel(sub.executor) }}</span>
                  <p v-if="sub.description">{{ sub.description }}</p>
                </div>
              </li>
            </ul>
            <p class="sop-ai-acceptance">{{ $t('sop.create.cardAcceptance') }}：{{ card.acceptance }}</p>
            <div class="sop-ai-card-actions">
              <t-button size="small" variant="outline" @click="writeToDescription">
                {{ $t('sop.create.writeToDesc') }}
              </t-button>
              <t-button
                size="small"
                theme="primary"
                :loading="submitting"
                :disabled="!pickedSubs.size"
                @click="submitWithSplit"
              >
                {{ $t('sop.split.submitCount', { n: pickedSubs.size }) }}
              </t-button>
            </div>
          </div>
        </div>

        <div class="sop-ai-footer">
          <t-button size="small" variant="text" :disabled="streaming" @click="skipToBreakdown">
            {{ $t('sop.create.skipToBreakdown') }}
          </t-button>
          <div class="sop-ai-input">
            <t-input
              v-model="followUp"
              :disabled="streaming"
              :placeholder="$t('sop.create.followUpPlaceholder')"
              @enter="sendFollowUp"
            />
            <t-button size="small" theme="primary" :disabled="!followUp.trim() || streaming" @click="sendFollowUp">
              {{ $t('sop.create.send') }}
            </t-button>
          </div>
        </div>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { storeToRefs } from 'pinia';
import {
  addAttachment,
  addClarification,
  answerClarification,
  breakdownItems,
  createItem,
  splitItem,
  type ItemStatus,
  type SopBreakdownCard,
  type SopChildItemSpec,
  type SopItem,
} from '@/api/sop';
import {
  KIND_LABELS,
  SOURCE_LABELS,
  upsertBreakdownBlock,
} from '@/api/sop/helpers';
import { useSopStore } from '@/stores/sop';
import PmMarkdown from '@/views/pm/components/PmMarkdown.vue';

const props = defineProps<{ visible: boolean; projectId: string }>();
const emit = defineEmits<{ 'update:visible': [boolean]; created: [SopItem] }>();

const { t } = useI18n();
const store = useSopStore();
const { templates } = storeToRefs(store);

interface ChatMessage {
  role: 'user' | 'assistant';
  text: string;
}

const form = reactive({
  title: '',
  description: '',
  source: 'internal',
  proposer: '',
  priority: 'P2',
  kind: 'requirement',
  template_id: '',
  parent_id: '',
});

const attachments = ref<{ id: string; name: string }[]>([]);
const pickedSubs = ref(new Set<number>());
const messages = ref<ChatMessage[]>([]);
const card = ref<SopBreakdownCard | null>(null);
const followUp = ref('');
const agentId = ref('default');
const streaming = ref(false);
const submitting = ref(false);
const scrollEl = ref<HTMLElement | null>(null);

const sourceOptions = computed(() =>
  Object.entries(SOURCE_LABELS).map(([value, label]) => ({ value, label })),
);

const kindOptions = computed(() =>
  Object.entries(KIND_LABELS).map(([value, label]) => ({ value, label })),
);

const templateOptions = computed(() =>
  templates.value
    .filter((tpl) => tpl.status === 'published')
    .map((tpl) => ({ value: tpl.id, label: tpl.name })),
);

const agentOptions = computed(() => [{ value: 'default', label: t('sop.create.agentDefault') }]);

/** 归属大项下拉：只列大项（两级上限由后端兜底）。 */
const bigParentOptions = computed(() =>
  store.items
    .filter((it) => it.is_big)
    .map((it) => ({ value: it.id, label: `${it.no} ${it.title}`.trim() })),
);

function toggleSub(index: number, checked: boolean) {
  const next = new Set(pickedSubs.value);
  if (checked) next.add(index);
  else next.delete(index);
  pickedSubs.value = next;
}

watch(
  () => props.visible,
  (open) => {
    if (!open) return;
    form.title = '';
    form.description = '';
    form.source = 'internal';
    form.proposer = '';
    form.priority = 'P2';
    form.kind = 'requirement';
    form.template_id = '';
    form.parent_id = '';
    pickedSubs.value = new Set();
    attachments.value = [];
    messages.value = [];
    card.value = null;
    followUp.value = '';
    streaming.value = false;
    submitting.value = false;
  },
);

function close() {
  emit('update:visible', false);
}

function executorLabel(executor: string): string {
  switch (executor) {
    case 'cli':
      return t('sop.create.execCli');
    case 'script':
      return t('sop.create.execScript');
    case 'approval':
      return t('sop.create.execApproval');
    case 'platform_agent':
    default:
      return t('sop.create.execAgent');
  }
}

async function scrollToBottom() {
  await nextTick();
  if (scrollEl.value) scrollEl.value.scrollTop = scrollEl.value.scrollHeight;
}

/** 拆解/追问：一次请求拿回拆解卡，正文用卡片渲染（后端不返回纯文本流）。 */
async function runBreakdown(prompt: string) {
  streaming.value = true;
  messages.value = [{ role: 'user', text: prompt }];
  await scrollToBottom();
  try {
    const result = await breakdownItems({
      project_id: props.projectId,
      title: form.title || t('sop.create.untitled'),
      description: form.description,
      instruction: prompt,
    });
    card.value = result;
    messages.value.push({ role: 'assistant', text: breakdownSummary(result) });
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.create.aiFailed'));
  } finally {
    streaming.value = false;
    await scrollToBottom();
  }
}

/** 助手气泡里的摘要：卡片主体在下方单独渲染，这里只说明产出粒度。 */
function breakdownSummary(result: SopBreakdownCard): string {
  if (!result.sub_items.length) return t('sop.create.aiEmptyResult');
  return t('sop.create.aiResult', { n: result.sub_items.length });
}

function startBreakdown() {
  return runBreakdown(t('sop.create.breakdownPrompt'));
}

function skipToBreakdown() {
  return runBreakdown(t('sop.create.skipPrompt'));
}

function sendFollowUp() {
  const text = followUp.value.trim();
  if (!text || streaming.value) return;
  followUp.value = '';
  return runBreakdown(text);
}

function writeToDescription() {
  if (!card.value) return;
  const lines = [
    `${t('sop.create.cardGoal')}：${card.value.goal}`,
    ...card.value.sub_items.map((sub) => `- ${sub.title}${sub.description ? `：${sub.description}` : ''}`),
    `${t('sop.create.cardAcceptance')}：${card.value.acceptance}`,
  ];
  // 覆盖语义在 helpers 里：重复写入只替换标记块，用户原文不动
  form.description = upsertBreakdownBlock(form.description, lines.join('\n'));
}

async function onFilesPicked(event: Event) {
  const input = event.target as HTMLInputElement;
  const files = Array.from(input.files || []);
  input.value = '';
  if (!files.length) return;
  for (const file of files) {
    try {
      // 附件必须挂在事项上：先记住文件，创建成功后再逐个上传
      attachments.value.push({ id: URL.createObjectURL(file), name: file.name });
      pendingFiles.set(URL.createObjectURL(file), file);
    } catch {
      MessagePlugin.error(t('sop.create.attachmentFailed', { name: file.name }));
    }
  }
}

const pendingFiles = new Map<string, File>();

/** 拆解卡上的「拆成子事项」：先建父项，再按勾选条目 split（split_source=ai）。 */
async function submitWithSplit() {
  if (!form.title.trim()) {
    MessagePlugin.warning(t('sop.create.titleRequired'));
    return;
  }
  if (!card.value || !pickedSubs.value.size) {
    MessagePlugin.warning(t('sop.split.empty'));
    return;
  }
  submitting.value = true;
  try {
    const parent = await createItem(props.projectId, {
      title: form.title.trim(),
      description: form.description,
      source: form.source,
      proposer: form.proposer.trim(),
      priority: form.priority,
      kind: form.kind,
      outcome: {},
      domain_data: {},
    });
    const specs: SopChildItemSpec[] = [...pickedSubs.value]
      .sort((a, b) => a - b)
      .map((index) => card.value!.sub_items[index])
      .filter(Boolean)
      .map((sub) => ({ title: sub.title, description: sub.description }));
    const result = await splitItem(parent.id, { children: specs, source: 'ai' });
    MessagePlugin.success(t('sop.split.created', { n: result.created.length }));
    pickedSubs.value = new Set();
    emit('created', parent);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.split.failed'));
  } finally {
    submitting.value = false;
  }
}

async function submit(status: ItemStatus) {
  if (!form.title.trim()) {
    MessagePlugin.warning(t('sop.create.titleRequired'));
    return;
  }
  submitting.value = true;
  try {
    const item = await createItem(props.projectId, {
      title: form.title.trim(),
      description: form.description,
      source: form.source,
      proposer: form.proposer.trim(),
      priority: form.priority,
      kind: form.kind,
      template_id: form.template_id || undefined,
      parent_id: form.parent_id || undefined,
      outcome: {},
      domain_data: {},
    });
    // 状态：saveDraft=backlog 由后端默认；确认事项需要单独迁移到 todo
    if (status === 'todo') {
      const { setItemStatus } = await import('@/api/sop');
      await setItemStatus(item.id, 'todo');
    }
    // 附件在创建后才可上传
    for (const entry of attachments.value) {
      const file = pendingFiles.get(entry.id);
      if (!file) continue;
      await addAttachment(item.id, file).catch(() => undefined);
    }
    // 追问里已经回答过的内容 seeding 成已回答澄清
    for (const message of messages.value) {
      if (message.role !== 'assistant' || !message.text.trim()) continue;
      const created = await addClarification(item.id, t('sop.create.seedQuestion'), 'ai').catch(() => null);
      if (created) {
        await answerClarification(item.id, created.id, message.text.trim()).catch(() => undefined);
      }
    }
    MessagePlugin.success(t('sop.create.created'));
    // 只通知父级：关闭由父级统一改 visible，避免 enter/leave 过渡互相打断
    emit('created', item);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.create.createFailed'));
  } finally {
    submitting.value = false;
  }
}
</script>

<style lang="less" scoped src="./sopItemCreateModal.less"></style>
