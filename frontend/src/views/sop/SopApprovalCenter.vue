<template>
  <div class="sop-approvals">
    <header class="sop-approvals-head">
      <div>
        <h2 class="sop-approvals-title">
          {{ $t('sop.appr.title') }}
          <span v-if="entries.length" class="sop-badge">{{ entries.length }}</span>
        </h2>
        <p class="sop-approvals-desc">{{ $t('sop.appr.desc') }}</p>
      </div>
      <t-button size="small" variant="outline" :loading="refreshing" @click="refresh">⟳</t-button>
    </header>

    <div v-if="!entries.length && !loading" class="sop-approvals-empty">{{ $t('sop.appr.empty') }}</div>

    <ul class="sop-approvals-list">
      <li v-for="entry in sortedEntries" :key="`${entry.kind}:${entry.ref_id}`" class="sop-approval">
        <div class="sop-approval-head">
          <span class="sop-kind" :class="`is-${entry.kind}`">{{ kindLabel(entry.kind) }}</span>
          <b class="sop-approval-title">{{ entry.title }}</b>
          <span class="sop-muted">{{ waitingText(entry.waiting_since) }}</span>
        </div>
        <div class="sop-approval-meta">
          <span v-if="entry.project_name">{{ $t('sop.appr.project', { name: entry.project_name }) }}</span>
          <span v-if="entry.item_title">{{ $t('sop.appr.item', { title: entry.item_title }) }}</span>
        </div>
        <p v-if="entry.reason" class="sop-approval-reason">{{ entry.reason }}</p>

        <!-- SOP 关口：按 actions 显示行内操作 -->
        <div v-if="entry.kind === 'sop'" class="sop-approval-actions">
          <template v-if="entry.actions.includes('approve')">
            <t-input
              v-model="comments[entry.ref_id]"
              class="sop-comment"
              :placeholder="$t('sop.appr.commentPlaceholder')"
            />
            <t-button
              size="small"
              theme="primary"
              :loading="busy === entryKey(entry)"
              @click="approve(entry)"
            >
              {{ $t('sop.appr.approve') }}
            </t-button>
          </template>
          <t-button
            v-if="entry.actions.includes('rework')"
            size="small"
            variant="outline"
            :loading="busy === entryKey(entry)"
            @click="rework(entry)"
          >
            {{ $t('sop.appr.rework') }}
          </t-button>
          <template v-if="entry.actions.includes('reject')">
            <t-select
              v-model="rejectTargets[entry.ref_id]"
              class="sop-reject"
              :options="rejectOptions(entry)"
              :placeholder="$t('sop.appr.rejectTo')"
            />
            <t-button
              size="small"
              theme="danger"
              variant="outline"
              :loading="busy === entryKey(entry)"
              :disabled="!rejectTargets[entry.ref_id]"
              @click="reject(entry)"
            >
              {{ $t('sop.appr.reject') }}
            </t-button>
          </template>
          <t-button
            v-if="entry.actions.includes('terminate')"
            size="small"
            variant="text"
            theme="danger"
            :loading="busy === entryKey(entry)"
            @click="terminate(entry)"
          >
            {{ $t('sop.appr.terminate') }}
          </t-button>
          <t-button size="small" variant="text" @click="openRun(entry)">
            {{ $t('sop.appr.openRun') }}
          </t-button>
        </div>

        <!-- 智能体追问：行内答复 -->
        <div v-else-if="entry.kind === 'agent_prompt'" class="sop-approval-actions">
          <t-input
            v-model="answers[entry.ref_id]"
            class="sop-comment"
            :placeholder="$t('sop.appr.answerPlaceholder')"
          />
          <t-button
            size="small"
            theme="primary"
            :loading="busy === entryKey(entry)"
            :disabled="!answers[entry.ref_id]"
            @click="answerPrompt(entry)"
          >
            {{ $t('sop.appr.answer') }}
          </t-button>
          <t-button size="small" variant="text" @click="openSession(entry)">
            {{ $t('sop.appr.openSession') }}
          </t-button>
        </div>

        <!-- 自动化审批：复用现有自动化页，不改动该模块 -->
        <div v-else class="sop-approval-actions">
          <t-button size="small" variant="outline" @click="openAutomation">
            {{ $t('sop.appr.openAutomation') }}
          </t-button>
        </div>
      </li>
    </ul>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  answerNode,
  approveNode,
  rejectNode,
  reworkNode,
  terminateNode,
  type ApprovalEntry,
} from '@/api/sop';
import { useSopStore } from '@/stores/sop';

const router = useRouter();
const { t } = useI18n();
const store = useSopStore();
const { approvals: entries } = storeToRefs(store);

const loading = ref(false);
const refreshing = ref(false);
const busy = ref('');
const comments = reactive<Record<string, string>>({});
const rejectTargets = reactive<Record<string, string>>({});
const answers = reactive<Record<string, string>>({});
let stopPolling: (() => void) | null = null;

const sortedEntries = computed(() =>
  [...entries.value].sort(
    (a, b) => new Date(a.waiting_since).getTime() - new Date(b.waiting_since).getTime(),
  ),
);

function kindLabel(kind: string): string {
  if (kind === 'agent_prompt') return t('sop.appr.kindAgent');
  if (kind === 'automation') return t('sop.appr.kindAutomation');
  return t('sop.appr.kindSop');
}

function waitingText(since: string): string {
  const ms = Date.now() - new Date(since).getTime();
  if (!Number.isFinite(ms) || ms < 0) return '—';
  const minutes = Math.floor(ms / 60000);
  if (minutes < 60) return t('sop.appr.waitMinutes', { n: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return t('sop.appr.waitHours', { n: hours });
  return t('sop.appr.waitDays', { n: Math.floor(hours / 24) });
}

function entryKey(entry: ApprovalEntry): string {
  return `${entry.kind}:${entry.ref_id}`;
}

/** 打回目标由运行详情里的上游节点决定：这里先给「按节点模板默认」提示，不伪造候选。 */
function rejectOptions(entry: ApprovalEntry): { label: string; value: string }[] {
  return [{ label: t('sop.appr.rejectDefault'), value: entry.node_id || 'default' }];
}

async function refresh() {
  refreshing.value = true;
  try {
    await store.loadApprovals();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.appr.loadFailed'));
  } finally {
    refreshing.value = false;
  }
}

async function runAction(
  entry: ApprovalEntry,
  action: () => Promise<void>,
  successKey: string,
) {
  busy.value = entryKey(entry);
  try {
    await action();
    MessagePlugin.success(t(successKey));
    await store.loadApprovals();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.appr.actionFailed'));
  } finally {
    busy.value = '';
  }
}

function approve(entry: ApprovalEntry) {
  if (!entry.run_id || !entry.node_id) return;
  return runAction(
    entry,
    () => approveNode(entry.run_id!, entry.node_id!, comments[entry.ref_id] || ''),
    'sop.appr.approved',
  );
}

function rework(entry: ApprovalEntry) {
  if (!entry.run_id || !entry.node_id) return;
  return runAction(
    entry,
    () => reworkNode(entry.run_id!, entry.node_id!, comments[entry.ref_id] || ''),
    'sop.appr.reworked',
  );
}

function reject(entry: ApprovalEntry) {
  if (!entry.run_id || !entry.node_id) return;
  const target = rejectTargets[entry.ref_id];
  // 选择「按节点默认」时传空串，由后端按 node.reject_to 处理
  const rejectTo = target === 'default' ? '' : target;
  return runAction(
    entry,
    () => rejectNode(entry.run_id!, entry.node_id!, comments[entry.ref_id] || '', rejectTo),
    'sop.appr.rejected',
  );
}

function terminate(entry: ApprovalEntry) {
  if (!entry.run_id || !entry.node_id) return;
  return runAction(
    entry,
    () => terminateNode(entry.run_id!, entry.node_id!, comments[entry.ref_id] || ''),
    'sop.appr.terminated',
  );
}

async function answerPrompt(entry: ApprovalEntry) {
  if (!entry.run_id || !entry.node_id) return;
  const text = answers[entry.ref_id];
  if (!text) return;
  await runAction(
    entry,
    () => answerNode(entry.run_id!, entry.node_id!, text),
    'sop.appr.answered',
  );
  answers[entry.ref_id] = '';
}

function openRun(entry: ApprovalEntry) {
  if (!entry.run_id) return;
  void router.push({ name: 'sopRunDetail', params: { id: entry.run_id } });
}

function openSession(entry: ApprovalEntry) {
  if (!entry.session_id) return;
  void router.push({ name: 'chat', params: { chatid: entry.session_id } });
}

/** 自动化审批复用现有自动化模块，本工作台不改动其页面。 */
function openAutomation() {
  void router.push({ name: 'automationList' });
}

onMounted(async () => {
  loading.value = true;
  await refresh();
  loading.value = false;
  stopPolling = store.startPolling(() => void store.loadApprovals().catch(() => undefined), 10_000);
});

onUnmounted(() => stopPolling?.());
</script>

<style lang="less" scoped src="./sopApprovalCenter.less"></style>
