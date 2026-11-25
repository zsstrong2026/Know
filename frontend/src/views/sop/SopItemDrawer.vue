<template>
  <t-drawer
    :visible="!!itemId"
    size="min(1080px, 94vw)"
    placement="right"
    :header="drawerTitle"
    :footer="false"
    :on-close="close"
    @close="close"
  >
    <div v-if="item" class="sop-drawer">
      <div class="sop-drawer-tabs">
        <button
          v-for="tab in tabs"
          :key="tab.key"
          type="button"
          class="sop-drawer-tab"
          :class="{ on: activeTab === tab.key }"
          @click="activeTab = tab.key"
        >
          {{ tab.label }}
          <span v-if="tab.count" class="badge">{{ tab.count }}</span>
        </button>
      </div>

      <!-- 概览 -->
      <section v-if="activeTab === 'overview'" class="sop-pane">
        <div class="sop-card">
          <h4>{{ $t('sop.drawer.latestRun') }}</h4>
          <template v-if="latestRun">
            <div class="sop-kv">
              <span class="k">{{ $t('sop.drawer.runStatus') }}</span>
              <span class="v">{{ runStatusLabel(latestRun.status) }}</span>
            </div>
            <div class="sop-kv">
              <span class="k">{{ $t('sop.drawer.runProgress') }}</span>
              <span class="v">{{ progressText(item.progress_done || 0, item.progress_total || 0) }}</span>
            </div>
            <div v-if="latestRun.status === 'failed'" class="sop-retry">
              <textarea
                v-model="retryContext"
                class="sop-textarea"
                rows="3"
                :placeholder="$t('sop.drawer.retryContextPlaceholder')"
              />
              <t-button size="small" theme="primary" :loading="busy" @click="retryLatestRun">
                {{ $t('sop.drawer.retryWithContext') }}
              </t-button>
            </div>
            <div class="sop-row">
              <t-button size="small" variant="outline" @click="openRunDetail(latestRun.id)">
                {{ $t('sop.drawer.investigate') }}
              </t-button>
              <t-button size="small" variant="outline" :loading="busy" @click="relaunch">
                {{ $t('sop.drawer.relaunch') }}
              </t-button>
            </div>
          </template>
          <p v-else class="sop-muted">{{ $t('sop.drawer.noRun') }}</p>
        </div>

        <div v-if="parentItem" class="sop-card">
          <h4>{{ $t('sop.split.parentTitle') }}</h4>
          <div class="sop-kv sop-kv--last">
            <span class="k">{{ $t('sop.split.parentLabel') }}</span>
            <span class="v">
              <button type="button" class="sop-link" @click="openItemById(parentItem.id)">
                {{ parentItem.no }} {{ parentItem.title }}
              </button>
            </span>
          </div>
        </div>

        <div v-if="item.is_big" class="sop-card">
          <h4>{{ $t('sop.split.childProgress') }}</h4>
          <div class="sop-kv">
            <span class="k">{{ $t('sop.items.colProgress') }}</span>
            <span class="v">{{ childProgressText(childStats.done, childStats.total) }}</span>
          </div>
          <div class="sop-kv sop-kv--last">
            <span class="k">{{ $t('sop.settings.statProgress') }}</span>
            <span class="v">{{ EXEC_STATE_LABELS[childStats.exec] }}</span>
          </div>
          <div class="sop-row">
            <t-button size="small" variant="outline" @click="activeTab = 'children'">
              {{ $t('sop.split.addChild') }}
            </t-button>
          </div>
        </div>

        <div class="sop-card">
          <h4>{{ $t('sop.drawer.fields') }}</h4>
          <div class="sop-kv"><span class="k">{{ $t('sop.items.colSource') }}</span><span class="v">{{ SOURCE_LABELS[item.source] || item.source }}</span></div>
          <div class="sop-kv"><span class="k">{{ $t('sop.drawer.proposer') }}</span><span class="v">{{ item.proposer || '—' }}</span></div>
          <div class="sop-kv"><span class="k">{{ $t('sop.items.colPriority') }}</span><span class="v">{{ item.priority }}</span></div>
          <div class="sop-kv"><span class="k">{{ $t('sop.drawer.kind') }}</span><span class="v">{{ KIND_LABELS[item.kind] || item.kind }}</span></div>
          <div class="sop-kv"><span class="k">{{ $t('sop.items.colTemplate') }}</span><span class="v">{{ templateName || $t('sop.items.templateUnbound') }}</span></div>
          <div class="sop-kv"><span class="k">{{ $t('sop.drawer.kb') }}</span><span class="v">{{ item.kb_id || $t('sop.settings.kbUnbound') }}</span></div>
          <div class="sop-kv"><span class="k">{{ $t('sop.drawer.repos') }}</span><span class="v">{{ repoSummary }}</span></div>
          <div class="sop-kv sop-kv--last"><span class="k">{{ $t('sop.drawer.createdAt') }}</span><span class="v">{{ formatTime(item.created_at) }}</span></div>
          <div class="sop-row">
            <t-button size="small" :loading="busy" @click="runAiAnalysis">
              ✦ {{ $t('sop.drawer.aiAnalyze') }}
            </t-button>
            <t-button size="small" variant="outline" @click="activeTab = 'clarify'">
              {{ $t('sop.drawer.clarifyEntry', { n: openClarifications }) }}
            </t-button>
            <t-button size="small" :loading="busy" @click="closeItem">
              {{ $t('sop.drawer.closeItem') }}
            </t-button>
            <t-button size="small" theme="danger" variant="outline" @click="askDelete">
              {{ $t('common.delete') }}
            </t-button>
          </div>
        </div>

        <div class="sop-card">
          <h4>{{ $t('sop.drawer.description') }}</h4>
          <PmMarkdown v-if="item.description" :content="item.description" />
          <p v-else class="sop-muted">{{ $t('sop.drawer.noDescription') }}</p>
        </div>
      </section>

      <!-- 执行进展 -->
      <section v-if="activeTab === 'progress'" class="sop-pane">
        <div v-if="!nodeRuns.length" class="sop-empty">{{ $t('sop.drawer.noProgress') }}</div>
        <ol v-else class="sop-timeline">
          <li v-for="node in nodeRuns" :key="node.id" class="sop-timeline-item">
            <div class="sop-timeline-head">
              <b>{{ nodeName(node.node_id) }}</b>
              <span class="sop-status">{{ NODE_STATUS_LABELS[node.status] || node.status }}</span>
            </div>
            <div class="sop-timeline-meta">
              <span>attempt {{ node.attempt }}</span>
              <span v-if="node.rework_count">rework {{ node.rework_count }}</span>
              <span v-if="node.gate_result">{{ $t('sop.drawer.gate') }}：{{ node.gate_result }}</span>
              <span>{{ durationText(node) }}</span>
            </div>
            <p v-if="node.error" class="sop-error">{{ node.error }}</p>
          </li>
        </ol>
      </section>

      <!-- 产物 -->
      <section v-if="activeTab === 'artifacts'" class="sop-pane">
        <div v-if="!artifacts.length" class="sop-empty">{{ $t('sop.drawer.noArtifacts') }}</div>
        <table v-else class="sop-table">
          <thead>
            <tr>
              <th>{{ $t('sop.drawer.artifactName') }}</th>
              <th>{{ $t('sop.drawer.artifactPath') }}</th>
              <th>{{ $t('sop.drawer.artifactSize') }}</th>
              <th>{{ $t('sop.drawer.artifactSha') }}</th>
              <th>{{ $t('sop.drawer.artifactIngested') }}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="art in artifacts" :key="art.id">
              <td>{{ art.name }}</td>
              <td class="mono">{{ art.path }}</td>
              <td>{{ formatBytes(art.size) }}</td>
              <td class="mono">{{ shortSha(art.sha256) }}</td>
              <td>{{ art.knowledge_id ? $t('sop.drawer.ingested') : '—' }}</td>
              <td>
                <t-button size="small" variant="text" @click="previewArtifact(art)">
                  {{ $t('sop.drawer.preview') }}
                </t-button>
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <!-- 会话 -->
      <section v-if="activeTab === 'sessions'" class="sop-pane">
        <div class="sop-empty">{{ $t('sop.drawer.sessionsSoon') }}</div>
      </section>

      <!-- 子事项（仅大项）：表格 + 触发 + 打开 + 移出 -->
      <section v-if="activeTab === 'children'" class="sop-pane">
        <div v-if="!children.length" class="sop-empty">{{ $t('sop.split.childrenEmpty') }}</div>
        <template v-else>
          <div class="sop-row">
            <t-button
              size="small"
              theme="primary"
              :loading="busy"
              @click="launchChildren"
            >
              {{ $t('sop.split.launchAll') }}
            </t-button>
          </div>
          <table class="sop-table">
            <thead>
              <tr>
                <th>{{ $t('sop.items.colNo') }}</th>
                <th>{{ $t('sop.items.colTitle') }}</th>
                <th>{{ $t('sop.items.colStatus') }}</th>
                <th>{{ $t('sop.items.colProgress') }}</th>
                <th>{{ $t('sop.items.colActions') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="kid in children" :key="kid.id">
                <td>{{ kid.no }}</td>
                <td>{{ kid.title }}</td>
                <td>{{ ITEM_STATUS_LABELS[kid.status] || kid.status }}</td>
                <td>{{ progressText(kid.progress_done || 0, kid.progress_total || 0) }}</td>
                <td>
                  <t-button size="small" variant="text" @click="openItemById(kid.id)">
                    {{ $t('sop.split.openChild') }}
                  </t-button>
                  <t-button size="small" variant="text" :loading="launchingChildId === kid.id" @click="launchChild(kid)">
                    {{ $t('sop.items.actionLaunch') }}
                  </t-button>
                  <t-button size="small" variant="text" theme="danger" @click="askDetach(kid)">
                    {{ $t('sop.split.detach') }}
                  </t-button>
                </td>
              </tr>
            </tbody>
          </table>
        </template>
      </section>

      <!-- 澄清 -->
      <section v-if="activeTab === 'clarify'" class="sop-pane">
        <div class="sop-row sop-row--end">
          <t-button size="small" variant="outline" :loading="busy" @click="generateClarificationsForItem">
            {{ $t('sop.drawer.aiClarify') }}
          </t-button>
        </div>
        <div v-if="!clarifications.length" class="sop-empty">{{ $t('sop.drawer.noClarify') }}</div>
        <div v-for="entry in clarifications" :key="entry.id" class="sop-clarify">
          <div class="sop-clarify-q">
            <span class="sop-tag" :class="entry.status === 'open' ? 'sop-tag--brand' : 'sop-tag--gray'">
              {{ entry.status === 'open' ? $t('sop.drawer.open') : $t('sop.drawer.answered') }}
            </span>
            {{ entry.question }}
          </div>
          <p v-if="entry.answer" class="sop-clarify-a">{{ entry.answer }}</p>
          <div v-else class="sop-clarify-form">
            <t-input v-model="answerDrafts[entry.id]" :placeholder="$t('sop.drawer.answerPlaceholder')" />
            <t-button size="small" theme="primary" :disabled="!answerDrafts[entry.id]" @click="submitAnswer(entry.id)">
              {{ $t('common.confirm') }}
            </t-button>
          </div>
        </div>
      </section>

      <!-- 交付 -->
      <section v-if="activeTab === 'delivery'" class="sop-pane">
        <div class="sop-card">
          <h4>{{ $t('sop.drawer.outcome') }}</h4>
          <p>{{ outcomeSummary(item.outcome) }}</p>
        </div>
        <div v-if="!artifacts.length" class="sop-empty">{{ $t('sop.drawer.noArtifacts') }}</div>
      </section>

      <!-- 验收 -->
      <section v-if="activeTab === 'accept'" class="sop-pane">
        <div class="sop-card">
          <h4>{{ $t('sop.drawer.acceptance') }}</h4>
          <label class="sop-field">
            <span>{{ $t('sop.drawer.valueAchieved') }}</span>
            <t-radio-group v-model="outcomeForm.value_achieved">
              <t-radio value="achieved">{{ $t('sop.drawer.achieved') }}</t-radio>
              <t-radio value="partial">{{ $t('sop.drawer.partial') }}</t-radio>
              <t-radio value="not_achieved">{{ $t('sop.drawer.notAchieved') }}</t-radio>
            </t-radio-group>
          </label>
          <label class="sop-field">
            <span>{{ $t('sop.drawer.summary') }}</span>
            <textarea v-model="outcomeForm.summary" class="sop-textarea" rows="4" />
          </label>
          <div class="sop-row sop-row--end">
            <t-button theme="primary" :loading="busy" @click="saveOutcome">
              {{ $t('common.save') }}
            </t-button>
          </div>
        </div>
      </section>

      <!-- 活动 -->
      <section v-if="activeTab === 'activity'" class="sop-pane">
        <div class="sop-row sop-row--end">
          <t-button size="small" variant="outline" @click="mergedView = !mergedView">
            {{ mergedView ? $t('sop.drawer.businessOnly') : $t('sop.drawer.mergedView') }}
          </t-button>
        </div>
        <ul class="sop-activities">
          <li v-for="(entry, index) in activityList" :key="index">
            <span class="sop-activity-time">{{ formatTime(activityTime(entry)) }}</span>
            <span class="sop-tag sop-tag--sm">{{ entry.kind || 'business' }}</span>
            <span>{{ entry.text || entry.action || entry.detail }}</span>
            <span v-if="entry.actor" class="sop-muted">· {{ entry.actor }}</span>
          </li>
        </ul>
      </section>

      <!-- 操作日志 -->
      <section v-if="activeTab === 'audit'" class="sop-pane">
        <ul class="sop-activities">
          <li v-for="(entry, index) in auditActivities" :key="index">
            <span class="sop-activity-time">{{ formatTime(activityTime(entry)) }}</span>
            <span>{{ entry.action || entry.detail }}</span>
            <span v-if="entry.actor" class="sop-muted">· {{ entry.actor }}</span>
          </li>
        </ul>
        <div v-if="!auditActivities.length" class="sop-empty">{{ $t('sop.drawer.noAudit') }}</div>
      </section>
      <SopDocPreview
        v-model:visible="docPreviewVisible"
        :artifact="docPreview"
        :run-id="docPreview?.run_id || latestRunId"
        :knowledge-allowed="!!projectKbId"
        @ingested="onDocIngested"
      />
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  addHumanContext,
  analyzeItem,
  answerClarification,
  createRun,
  deleteItem,
  relaunch as apiRelaunch,
  detachChild,
  listChildren,
  generateClarifications,
  getItem,
  getRun,
  listActivities,
  listArtifacts,
  listClarifications,
  setOutcome as apiSetOutcome,
  setItemStatus,
  type SopArtifact,
  type SopItem,
  type SopItemActivity,
  type SopItemWithDerived,
  type SopItemClarification,
  type SopNodeRun,
} from '@/api/sop';
import {
  EXEC_STATE_LABELS,
  ITEM_STATUS_LABELS,
  KIND_LABELS,
  NODE_STATUS_LABELS,
  RUN_STATUS_LABELS,
  SOURCE_LABELS,
  childProgressText,
  deriveParentExecState,
  formatBytes,
  mergeActivities,
  outcomeSummary,
  progressText,
  shortSha,
  type MergeableActivity,
} from '@/api/sop/helpers';
import PmMarkdown from '@/views/pm/components/PmMarkdown.vue';
import { useSopStore } from '@/stores/sop';
import { useConfirmDelete } from '@/components/settings/useConfirmDelete';
import SopDocPreview from './components/SopDocPreview.vue';

type DrawerTab =
  | 'overview'
  | 'progress'
  | 'artifacts'
  | 'sessions'
  | 'children'
  | 'clarify'
  | 'delivery'
  | 'accept'
  | 'activity'
  | 'audit';

const props = defineProps<{ itemId: string | null }>();
const emit = defineEmits<{ close: []; changed: []; open: [id: string] }>();

const router = useRouter();
const { t } = useI18n();
const confirmDanger = useConfirmDelete();
const store = useSopStore();

const item = ref<(SopItem & Partial<SopItemWithDerived>) | null>(null);
const nodeRuns = ref<SopNodeRun[]>([]);
const artifacts = ref<SopArtifact[]>([]);
const latestRunId = ref('');
const latestRunStatus = ref('');
const clarifications = ref<SopItemClarification[]>([]);
const businessActivities = ref<SopItemActivity[]>([]);
const auditActivities = ref<SopItemActivity[]>([]);
const activeTab = ref<DrawerTab>('overview');
const mergedView = ref(false);
const busy = ref(false);
const retryContext = ref('');
const answerDrafts = reactive<Record<string, string>>({});
const children = ref<SopItemWithDerived[]>([]);
const parentItem = ref<SopItem | null>(null);
const launchingChildId = ref('');
const outcomeForm = reactive({ value_achieved: '', summary: '' });

const latestRun = computed(() =>
  latestRunId.value ? { id: latestRunId.value, status: latestRunStatus.value } : null,
);

const openClarifications = computed(
  () => clarifications.value.filter((entry) => entry.status === 'open').length,
);

const templateName = computed(() => {
  const id = item.value?.template_id;
  return id || '';
});

const repoSummary = computed(() => '—');

const projectKbId = computed(() => store.currentProject?.kb_id || '');

const drawerTitle = computed(() =>
  item.value
    ? `${item.value.no || ''} ${item.value.title}`.trim()
    : t('sop.drawer.title'),
);

const childStats = computed(() => {
  const done = children.value.filter(
    (kid) => kid.status === 'done' || kid.status === 'closed',
  ).length;
  return {
    total: children.value.length,
    done,
    exec: deriveParentExecState(children.value.map((kid) => kid.exec_state)),
  };
});

const tabs = computed<{ key: DrawerTab; label: string; count?: number }[]>(() => {
  const list: { key: DrawerTab; label: string; count?: number }[] = [
    { key: 'overview', label: t('sop.drawer.tabOverview') },
    { key: 'progress', label: t('sop.drawer.tabProgress') },
    { key: 'artifacts', label: t('sop.drawer.tabArtifacts'), count: artifacts.value.length },
  ];
  // 「子事项」只对大项出现（独立事项没有子项，多一个空 tab 只会添乱）
  if (item.value?.is_big) {
    list.push({ key: 'children', label: t('sop.split.tabChildren'), count: children.value.length });
  }
  list.push(
    { key: 'sessions', label: t('sop.drawer.tabSessions') },
  { key: 'clarify', label: t('sop.drawer.tabClarify'), count: openClarifications.value },
  { key: 'delivery', label: t('sop.drawer.tabDelivery') },
  { key: 'accept', label: t('sop.drawer.tabAccept') },
    { key: 'activity', label: t('sop.drawer.tabActivity') },
    { key: 'audit', label: t('sop.drawer.tabAudit') },
  );
  return list;
});

const activityList = computed<MergeableActivity[]>(() =>
  mergedView.value
    ? mergeActivities(businessActivities.value as any, auditActivities.value as any)
    : (businessActivities.value as any),
);

function runStatusLabel(status: string): string {
  return (RUN_STATUS_LABELS as Record<string, string>)[status] || status;
}

function nodeName(nodeId: string): string {
  return nodeId || '—';
}

function durationText(node: SopNodeRun): string {
  if (!node.started_at || !node.finished_at) return '';
  const ms = new Date(node.finished_at).getTime() - new Date(node.started_at).getTime();
  if (!Number.isFinite(ms) || ms < 0) return '';
  return `${Math.round(ms / 1000)}s`;
}

function formatTime(value?: string | null): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function activityTime(entry: MergeableActivity): string {
  return String(entry.at || entry.created_at || '');
}

function close() {
  emit('close');
}

async function load() {
  const id = props.itemId;
  if (!id) return;
  const detail = await getItem(id).catch(() => null);
  if (!detail) {
    MessagePlugin.error(t('sop.drawer.loadFailed'));
    return;
  }
  item.value = detail;
  outcomeForm.value_achieved = String(detail.outcome?.value_achieved || '');
  outcomeForm.summary = String(detail.outcome?.summary || '');
  latestRunId.value = detail.last_run_id || '';
  latestRunStatus.value = '';
  nodeRuns.value = [];
  artifacts.value = [];
  if (latestRunId.value) {
    const run = await getRun(latestRunId.value).catch(() => null);
    if (run) {
      nodeRuns.value = run.node_runs || [];
      artifacts.value = run.artifacts || [];
      latestRunStatus.value = run.run.status;
    }
  }
  await loadRelations(detail);
  const [cls, biz, audit] = await Promise.all([
    listClarifications(id).catch(() => []),
    listActivities(id, 'business').catch(() => []),
    listActivities(id, 'audit').catch(() => []),
  ]);
  clarifications.value = cls;
  businessActivities.value = biz;
  auditActivities.value = audit;
}

/** 父项/子项关系：子项取父项做面包屑，大项取子项做 tab。 */
async function loadRelations(detail: SopItem) {
  children.value = [];
  parentItem.value = null;
  if (detail.is_big) {
    children.value = await listChildren(detail.id).catch(() => []);
    return;
  }
  if (detail.parent_id) {
    parentItem.value = await getItem(detail.parent_id).catch(() => null);
  }
}

/** 在同一个抽屉里切到另一条事项（父项 ↔ 子项）：由父级换 id 触发重载。 */
function openItemById(id: string) {
  emit('open', id);
}

/** 大项批量触发：逐条 createRun，模板取子项自己的或项目默认。 */
async function launchChildren() {
  if (!item.value) return;
  const templateId = store.currentProject?.default_template_id || '';
  if (!templateId) {
    MessagePlugin.warning(t('sop.items.noTemplate'));
    return;
  }
  busy.value = true;
  let ok = 0;
  let skipped = 0;
  for (const kid of children.value) {
    if (kid.exec_state === 'running' || kid.exec_state === 'intervene') {
      skipped += 1;
      continue;
    }
    try {
      await createRun({ item_id: kid.id, template_id: kid.template_id || templateId });
      ok += 1;
    } catch {
      skipped += 1;
    }
  }
  busy.value = false;
  if (ok) MessagePlugin.success(t('sop.items.bulkStarted', { n: ok }));
  if (skipped) MessagePlugin.info(t('sop.items.bulkSkipped', { n: skipped }));
  await load();
  emit('changed');
}

async function launchChild(kid: SopItemWithDerived) {
  const templateId = kid.template_id || store.currentProject?.default_template_id || '';
  if (!templateId) {
    MessagePlugin.warning(t('sop.items.noTemplate'));
    return;
  }
  launchingChildId.value = kid.id;
  try {
    await createRun({ item_id: kid.id, template_id: templateId });
    MessagePlugin.success(t('sop.items.launchStarted', { no: kid.no || kid.title }));
    await load();
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.items.launchFailed'));
  } finally {
    launchingChildId.value = '';
  }
}

function askDetach(kid: SopItemWithDerived) {
  if (!item.value) return;
  confirmDanger({
    title: t('sop.split.detach'),
    body: t('sop.split.detachConfirm'),
    confirmText: t('sop.split.detach'),
    onConfirm: async () => {
      try {
        await detachChild(item.value!.id, kid.id);
        MessagePlugin.success(t('sop.split.detached'));
        await load();
        emit('changed');
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('sop.split.detachFailed'));
      }
    },
  });
}

watch(
  () => props.itemId,
  async (id) => {
    activeTab.value = 'overview';
    mergedView.value = false;
    retryContext.value = '';
    if (id) await load();
    else item.value = null;
  },
  { immediate: true },
);

/** AI 分析：后端建会话并返回 session_id，前端跳到现有会话页看流式结论。 */
async function runAiAnalysis() {
  if (!item.value) return;
  busy.value = true;
  try {
    const sessionId = await analyzeItem(item.value.id);
    MessagePlugin.success(t('sop.drawer.aiAnalyzeStarted'));
    emit('close');
    await router.push({ name: 'chat', params: { chatid: sessionId } });
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.drawer.aiAnalyzeFailed'));
  } finally {
    busy.value = false;
  }
}

function openRunDetail(runId: string) {
  void router.push({ name: 'sopRunDetail', params: { id: runId } });
}

/** 失败重试：把补充上下文写进运行，再让引擎重试（后端 retry 即从失败节点续跑）。 */
async function retryLatestRun() {
  if (!latestRunId.value) return;
  busy.value = true;
  try {
    if (retryContext.value.trim()) {
      await addHumanContext(latestRunId.value, retryContext.value.trim());
    }
    const { retryRun } = await import('@/api/sop');
    await retryRun(latestRunId.value);
    MessagePlugin.success(t('sop.drawer.retryStarted'));
    retryContext.value = '';
    await load();
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.drawer.retryFailed'));
  } finally {
    busy.value = false;
  }
}

/** 整条重触发：新 run 冻结新快照，旧 run 保留为历史。大项由后端 400 拦截。 */
async function relaunch() {
  if (!item.value) return;
  if (!item.value.template_id) {
    MessagePlugin.warning(t('sop.items.noTemplate'));
    return;
  }
  busy.value = true;
  try {
    await apiRelaunch(item.value.id);
    MessagePlugin.success(t('sop.drawer.relaunched'));
    await load();
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.items.launchFailed'));
  } finally {
    busy.value = false;
  }
}

const docPreview = ref<SopArtifact | null>(null);
const docPreviewVisible = ref(false);

function previewArtifact(artifact: SopArtifact) {
  docPreview.value = artifact;
  docPreviewVisible.value = true;
}

function onDocIngested(updated: SopArtifact) {
  artifacts.value = artifacts.value.map((art) => (art.id === updated.id ? updated : art));
  docPreview.value = updated;
}

async function submitAnswer(clarificationId: string) {
  if (!item.value) return;
  const answer = (answerDrafts[clarificationId] || '').trim();
  if (!answer) return;
  busy.value = true;
  try {
    await answerClarification(item.value.id, clarificationId, answer);
    answerDrafts[clarificationId] = '';
    MessagePlugin.success(t('sop.drawer.answeredDone'));
    await load();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.drawer.answerFailed'));
  } finally {
    busy.value = false;
  }
}

async function generateClarificationsForItem() {
  if (!item.value) return;
  busy.value = true;
  try {
    await generateClarifications(item.value.id);
    await load();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.drawer.clarifyFailed'));
  } finally {
    busy.value = false;
  }
}

async function saveOutcome() {
  if (!item.value) return;
  busy.value = true;
  try {
    await apiSetOutcome(item.value.id, {
      value_achieved: outcomeForm.value_achieved,
      summary: outcomeForm.summary.trim(),
    });
    MessagePlugin.success(t('sop.drawer.outcomeSaved'));
    await load();
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.drawer.outcomeFailed'));
  } finally {
    busy.value = false;
  }
}

function askDelete() {
  if (!item.value) return;
  const target = item.value;
  confirmDanger({
    title: t('sop.drawer.deleteTitle'),
    body: t('sop.drawer.deleteConfirm', { title: target.title }),
    confirmText: t('common.delete'),
    onConfirm: async () => {
      try {
        await deleteItem(target.id);
        MessagePlugin.success(t('sop.drawer.deleted'));
        emit('changed');
        emit('close');
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('sop.drawer.deleteFailed'));
      }
    },
  });
}

/** 快捷关闭：状态机允许就直接 closed，不允许则透出后端白话原因。 */
async function closeItem() {
  if (!item.value) return;
  busy.value = true;
  try {
    await setItemStatus(item.value.id, 'closed');
    MessagePlugin.success(t('sop.drawer.closed'));
    await load();
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.drawer.closeFailed'));
  } finally {
    busy.value = false;
  }
}
</script>

<style lang="less" scoped src="./sopItemDrawer.less"></style>
