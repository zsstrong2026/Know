<template>
  <!-- 项目配置：摘要条 + 四个 Tab（基本设置 / 知识库与简报 / 代码仓库 / 危险操作）-->
  <div class="sop-settings">
    <div class="sop-settings-inner">
      <nav class="sop-crumb">
        <button class="sop-back" type="button" @click="backToItems">← {{ $t('sop.settings.backItems') }}</button>
        <span class="sop-crumb-title">{{ $t('sop.settings.projectTitle') }}</span>
        <span class="sop-crumb-sub">{{ $t('sop.settings.projectDesc') }}</span>
      </nav>

      <div v-if="!project" class="sop-empty-card">
        <t-empty :description="$t('sop.shell.noProject')" />
      </div>

      <template v-else>
        <header class="sop-summary">
          <div class="sop-summary-ico">{{ project.code.slice(0, 2) }}</div>
          <div class="sop-summary-main">
            <div class="sop-summary-name">{{ project.name }}</div>
            <div class="sop-summary-meta">
              {{ project.code }} · {{ project.description || $t('sop.settings.noDesc') }}
            </div>
          </div>
          <div class="sop-summary-stats">
            <div><div class="n">{{ stats.total }}</div><div class="l">{{ $t('sop.settings.statItems') }}</div></div>
            <div>
              <div class="n">{{ stats.running + stats.intervene }}</div>
              <div class="l">{{ $t('sop.settings.statProgress') }}</div>
            </div>
            <div>
              <div class="n n--sm">{{ kbLabel }}</div>
              <div class="l">{{ $t('sop.settings.statKb') }}</div>
            </div>
          </div>
          <t-button theme="primary" :loading="saving" @click="saveBasic">
            {{ $t('sop.settings.save') }}
          </t-button>
        </header>

        <div class="sop-settings-tabs">
          <button
            v-for="tab in tabs"
            :key="tab.key"
            type="button"
            class="sop-settings-tab"
            :class="{ on: activeTab === tab.key }"
            @click="activeTab = tab.key"
          >
            {{ tab.label }}
          </button>
        </div>

        <section v-if="activeTab === 'basic'" class="sop-pane">
          <div class="sop-cols">
            <div class="sop-card">
              <h4>{{ $t('sop.settings.basicTitle') }}</h4>
              <p class="sop-card-sub">{{ $t('sop.settings.basicSub') }}</p>
              <label class="sop-field">
                <span>{{ $t('sop.settings.name') }}</span>
                <t-input v-model="form.name" :maxlength="200" />
              </label>
              <label class="sop-field">
                <span>{{ $t('sop.settings.code') }}</span>
                <t-input v-model="form.code" disabled class="sop-mono" />
                <small class="sop-hint">{{ $t('sop.settings.codeHint', { code: project.code }) }}</small>
              </label>
              <label class="sop-field sop-field--grow">
                <span>{{ $t('sop.settings.desc') }}</span>
                <textarea v-model="form.description" class="sop-textarea" rows="6"
                  :placeholder="$t('sop.settings.descPlaceholder')" />
                <small class="sop-hint">{{ $t('sop.settings.descHint') }}</small>
              </label>
            </div>

            <div class="sop-card">
              <h4>{{ $t('sop.settings.defaultsTitle') }}</h4>
              <p class="sop-card-sub">{{ $t('sop.settings.defaultsSub') }}</p>
              <label class="sop-field">
                <span>{{ $t('sop.settings.defaultTemplate') }}</span>
                <t-select
                  v-model="form.default_template_id"
                  :options="templateOptions"
                  :placeholder="$t('sop.settings.defaultTemplateEmpty')"
                  clearable
                />
                <small class="sop-hint">{{ $t('sop.settings.defaultTemplateHint') }}</small>
              </label>
              <label class="sop-field">
                <span>{{ $t('sop.settings.defaultExecutor') }}</span>
                <t-select v-model="form.default_executor" :options="executorOptions" />
                <small class="sop-hint">{{ $t('sop.settings.defaultExecutorHint') }}</small>
              </label>
              <label class="sop-field">
                <span>{{ $t('sop.settings.onSuccess') }}</span>
                <t-select
                  v-model="form.on_run_success_item_status"
                  :options="onSuccessOptions"
                  :placeholder="$t('sop.settings.onSuccessNone')"
                  clearable
                />
                <small class="sop-hint">{{ $t('sop.settings.onSuccessHint') }}</small>
              </label>
              <label class="sop-field">
                <span>{{ $t('sop.settings.workspace') }}</span>
                <t-input v-model="form.workspace_root" class="sop-mono" />
                <small class="sop-hint">{{ $t('sop.settings.workspaceHint') }}</small>
              </label>
            </div>
          </div>

          <div class="sop-card sop-card--wide">
            <h4>{{ $t('sop.settings.notifyTitle') }}</h4>
            <p class="sop-card-sub">{{ $t('sop.settings.notifySub') }}</p>
            <div class="sop-switchrow">
              <t-switch v-model="form.notify_on_gate_or_fail" />
              <span>{{ $t('sop.settings.notifyLabel') }}</span>
            </div>
          </div>
        </section>

        <section v-if="activeTab === 'kb'" class="sop-pane">
          <div class="sop-cols sop-cols--asym">
            <div>
              <div class="sop-card">
                <h4>
                  {{ $t('sop.kbCard.title') }}
                  <span class="sop-tag" :class="kbOwnerClass">{{ kbOwnerLabel }}</span>
                </h4>
                <p class="sop-card-sub">{{ $t('sop.kbCard.sub') }}</p>
                <div class="sop-kv"><span class="k">{{ $t('sop.settings.kbCurrent') }}</span><span class="v">{{ kbLabel }}</span></div>
                <div class="sop-kv"><span class="k">{{ $t('sop.settings.kbBrief') }}</span><span class="v">{{ briefDocLabel }}</span></div>
                <div class="sop-kv sop-kv--last">
                  <span class="k">{{ $t('sop.settings.kbDelete') }}</span>
                  <span class="v">{{ kbDeletePolicy }}</span>
                </div>
                <div class="sop-row">
                  <t-select
                    v-model="rebindKbId"
                    class="sop-grow"
                    :options="kbOptions"
                    :placeholder="$t('sop.kbCard.bindTitle')"
                  />
                  <t-button size="small" :disabled="!rebindKbId || rebindingKb" :loading="rebindingKb" @click="rebindKB">
                    {{ $t('sop.kbCard.bindConfirm') }}
                  </t-button>
                  <t-button
                    size="small"
                    theme="danger"
                    variant="outline"
                    :disabled="project.kb_owner !== 'project' || !project.kb_id"
                    :title="$t('sop.kbCard.unbindWarn')"
                    @click="askUnbindKb"
                  >
                    {{ $t('sop.kbCard.unbindDelete') }}
                  </t-button>
                </div>
                <small class="sop-hint">{{ $t('sop.settings.kbRebindHint') }}</small>
              </div>

              <div class="sop-card">
                <h4>{{ $t('sop.brief.panelTitle') }}</h4>
                <p class="sop-card-sub">{{ $t('sop.settings.briefSub', { name: project.name }) }}</p>
                <div class="sop-kv">
                  <span class="k">{{ $t('sop.settings.briefLast') }}</span>
                  <span class="v">{{ briefLastText }}</span>
                </div>
                <div class="sop-kv sop-kv--last">
                  <span class="k">{{ $t('sop.brief.preview') }}</span>
                  <span class="v">
                    <span v-if="!briefKbBound" class="sop-tag sop-tag--muted">{{ $t('sop.brief.unbound') }}</span>
                    <span v-else-if="briefStale" class="sop-tag sop-tag--warn">
                      {{ $t('sop.brief.stale', { n: briefNotesMaxSeq - briefRenderedSeq }) }}
                    </span>
                    <span v-else class="sop-tag sop-tag--brand">{{ $t('sop.brief.synced') }}</span>
                  </span>
                </div>
                <div class="sop-row sop-row--end">
                  <t-button size="small" variant="outline" @click="briefPanelVisible = true">
                    {{ $t('sop.split.panel') }}
                  </t-button>
                  <t-button
                    v-if="briefStale && briefKbBound"
                    size="small"
                    theme="primary"
                    :loading="renderingBrief"
                    @click="renderBriefNow"
                  >
                    {{ $t('sop.brief.renderNow') }}
                  </t-button>
                </div>
              </div>
            </div>

            <div class="sop-card sop-card--preview">
              <h4>{{ $t('sop.brief.preview') }}</h4>
              <p class="sop-card-sub">
                {{ project.kb_id ? $t('sop.settings.briefPreviewSub') : $t('sop.settings.briefNoKb') }}
              </p>
              <div class="sop-preview">
                <PmMarkdown v-if="briefMarkdown.trim()" :content="briefMarkdown" />
                <t-empty v-else :description="$t('sop.brief.previewEmpty')" />
              </div>
            </div>
          </div>
        </section>

        <section v-if="activeTab === 'repos'" class="sop-pane">
          <div class="sop-card">
            <h4>
              {{ $t('sop.settings.repoTitle') }}
              <span class="sop-card-note">{{ $t('sop.settings.repoCount', { n: repos.length }) }}</span>
            </h4>
            <p class="sop-card-sub">{{ $t('sop.settings.repoSub') }}</p>

            <div v-if="!repos.length" class="sop-repo-empty">
              <div class="sop-repo-empty-title">{{ $t('sop.settings.repoEmpty') }}</div>
              <ol class="sop-repo-steps">
                <li>{{ $t('sop.settings.repoStep1') }}</li>
                <li>{{ $t('sop.settings.repoStep2') }}</li>
                <li>{{ $t('sop.settings.repoStep3') }}</li>
              </ol>
              <t-button theme="primary" variant="outline" @click="openAddRepo">
                {{ $t('sop.settings.repoAddFirst') }}
              </t-button>
            </div>

            <template v-else>
              <div v-for="repo in repos" :key="repo.id" class="sop-repo-row">
                <div class="sop-repo-main">
                  <div class="sop-repo-name">
                    {{ repo.name }}
                    <span class="sop-tag sop-tag--sm">{{ repo.branch || 'main' }}</span>
                    <span v-if="repo.default" class="sop-tag sop-tag--brand sop-tag--sm">{{ $t('sop.settings.repoDefault') }}</span>
                  </div>
                  <div class="sop-repo-url">{{ repo.url }}</div>
                </div>
                <div class="sop-repo-actions">
                  <t-button v-if="!repo.default" size="small" variant="text" @click="makeDefault(repo)">
                    {{ $t('sop.settings.repoSetDefault') }}
                  </t-button>
                  <t-button size="small" variant="text" theme="danger" @click="askRemoveRepo(repo)">
                    {{ $t('sop.settings.repoRemove') }}
                  </t-button>
                </div>
              </div>
              <div class="sop-row">
                <t-button size="small" theme="primary" variant="outline" @click="openAddRepo">
                  {{ $t('sop.settings.repoAdd') }}
                </t-button>
              </div>
            </template>
          </div>
          <div class="sop-tip">
            <span>⑂</span>
            <div>{{ $t('sop.settings.repoTip') }}</div>
          </div>
        </section>
        <section v-if="activeTab === 'danger'" class="sop-pane">
          <div class="sop-card sop-card--danger">
            <h4 class="sop-danger-title">{{ $t('sop.settings.dangerTitle') }}</h4>
            <p class="sop-card-sub">{{ $t('sop.settings.dangerSub') }}</p>
            <div class="sop-kv">
              <span class="k">{{ $t('sop.settings.dangerWillDelete') }}</span>
              <span class="v">{{ $t('sop.settings.dangerWillDeleteValue', { items: stats.total, runs: runCount }) }}</span>
            </div>
            <div class="sop-kv">
              <span class="k">{{ $t('sop.settings.dangerKb') }}</span>
              <span class="v">{{ dangerKbPolicy }}</span>
            </div>
            <div class="sop-kv sop-kv--last">
              <span class="k">{{ $t('sop.settings.dangerKeep') }}</span>
              <span class="v">{{ $t('sop.settings.dangerKeepValue') }}</span>
            </div>
            <div class="sop-row sop-row--end">
              <t-tooltip :content="deleteBlockedReason" :disabled="!deleteBlockedReason">
                <t-button theme="danger" :disabled="!!deleteBlockedReason" @click="askDeleteProject">
                  {{ $t('sop.settings.dangerDelete') }}
                </t-button>
              </t-tooltip>
            </div>
          </div>
        </section>
      </template>
    </div>

    <t-dialog
      v-model:visible="repoModal.visible"
      :header="$t('sop.settings.repoAddTitle')"
      width="520px"
      placement="center"
      :confirm-btn="{ content: $t('sop.settings.repoAdd'), loading: repoModal.submitting }"
      :cancel-btn="$t('common.cancel')"
      :on-confirm="submitRepo"
    >
      <label class="sop-field">
        <span>{{ $t('sop.settings.repoUrl') }}</span>
        <t-input v-model="repoModal.url" class="sop-mono" placeholder="https://github.com/org/repo.git" />
      </label>
      <label class="sop-field">
        <span>{{ $t('sop.settings.repoBranch') }}</span>
        <t-input v-model="repoModal.branch" class="sop-mono" placeholder="main" />
      </label>
      <label class="sop-field">
        <span>{{ $t('sop.settings.repoName') }}</span>
        <t-input v-model="repoModal.name" :placeholder="$t('sop.settings.repoNameHint')" />
      </label>
      <div v-if="repoModal.error" class="sop-form-error">{{ repoModal.error }}</div>
    </t-dialog>

    <SopProjectBriefPanel
      v-if="project"
      :visible="briefPanelVisible"
      :project-id="project.id"
      @close="briefPanelVisible = false"
      @changed="loadBriefStatus"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  addRepo,
  deleteProject,
  getBrief,
  getProject,
  listRuns,
  listTemplates,
  removeRepo,
  renderBrief,
  replaceProjectKB,
  setDefaultRepo,
  updateProject,
  type SopBriefStatus,
  type SopProject,
  type SopRepo,
} from '@/api/sop';
import { briefSeqStale } from '@/api/sop/helpers';
import SopProjectBriefPanel from './components/SopProjectBriefPanel.vue';
import PmMarkdown from '@/views/pm/components/PmMarkdown.vue';
import { listKnowledgeBases } from '@/api/knowledge-base';
import { repoDisplayName, validateRepoURL, ITEM_STATUS_LABELS } from '@/api/sop/helpers';
import { useSopStore } from '@/stores/sop';
import { useConfirmDelete } from '@/components/settings/useConfirmDelete';

type SettingsTab = 'basic' | 'kb' | 'repos' | 'danger';

const router = useRouter();
const { t } = useI18n();
const confirmDanger = useConfirmDelete();
const store = useSopStore();
const { currentProject, itemStats } = storeToRefs(store);

const project = ref<SopProject | null>(null);
const repos = ref<SopRepo[]>([]);
const templates = ref<{ id: string; name: string; status: string }[]>([]);
const kbList = ref<{ id: string; name: string }[]>([]);
const runCount = ref(0);
const activeRuns = ref(0);
const rebindKbId = ref('');
const saving = ref(false);
const rebindingKb = ref(false);
const renderingBrief = ref(false);
const briefPanelVisible = ref(false);
const brief = ref<SopBriefStatus | null>(null);
const activeTab = ref<SettingsTab>('basic');
const repoModal = reactive({ visible: false, url: '', branch: 'main', name: '', error: '', submitting: false });

const form = reactive({
  name: '',
  code: '',
  description: '',
  default_template_id: '',
  default_executor: 'platform_agent',
  on_run_success_item_status: '',
  workspace_root: '',
  notify_on_gate_or_fail: true,
});

const stats = computed(() => itemStats.value);

const tabs = computed<{ key: SettingsTab; label: string }[]>(() => [
  { key: 'basic', label: t('sop.settings.tabBasic') },
  { key: 'kb', label: t('sop.settings.tabKb') },
  {
    key: 'repos',
    label: repos.value.length
      ? t('sop.settings.tabReposCount', { n: repos.value.length })
      : t('sop.settings.tabRepos'),
  },
  { key: 'danger', label: t('sop.settings.tabDanger') },
]);

const templateOptions = computed(() =>
  templates.value
    .filter((tpl) => tpl.status === 'published')
    .map((tpl) => ({ label: tpl.name, value: tpl.id })),
);

const executorOptions = computed(() => [
  { label: t('sop.settings.executorPlatform'), value: 'platform_agent' },
]);

const onSuccessOptions = computed(() =>
  ['todo', 'in_progress', 'in_review', 'done'].map((status) => ({
    label: ITEM_STATUS_LABELS[status as keyof typeof ITEM_STATUS_LABELS],
    value: status,
  })),
);

const kbOptions = computed(() => kbList.value.map((kb) => ({ label: kb.name, value: kb.id })));

const kbLabel = computed(() => {
  if (!project.value?.kb_id) return t('sop.settings.kbUnbound');
  return kbList.value.find((kb) => kb.id === project.value?.kb_id)?.name || project.value.kb_id;
});

const kbOwnerLabel = computed(() => {
  switch (project.value?.kb_owner) {
    case 'linked':
      return t('sop.settings.kbOwnerLinked');
    case 'pm':
      return t('sop.settings.kbOwnerPm');
    case 'project':
      return t('sop.settings.kbOwnerProject');
    default:
      return t('sop.settings.kbOwnerNone');
  }
});

const kbOwnerClass = computed(() => {
  if (!project.value?.kb_id) return 'sop-tag--muted';
  return project.value.kb_owner === 'project' ? 'sop-tag--brand' : 'sop-tag--gray';
});

const kbDeletePolicy = computed(() => {
  if (!project.value?.kb_id) return t('sop.settings.kbPolicyNone');
  return project.value.kb_owner === 'project'
    ? t('sop.settings.kbPolicyProject')
    : t('sop.settings.kbPolicyLinked');
});

const dangerKbPolicy = computed(() => {
  if (!project.value?.kb_id) return t('sop.settings.kbPolicyNone');
  return project.value.kb_owner === 'project'
    ? t('sop.settings.dangerKbProject', { name: kbLabel.value })
    : t('sop.settings.dangerKbLinked', { name: kbLabel.value });
});

const briefDocLabel = computed(() =>
  project.value?.kb_id ? t('sop.settings.briefRendered') : t('sop.settings.briefUnbound'),
);

const briefMarkdown = computed(() => brief.value?.markdown || '');
const briefRenderedSeq = computed(() => brief.value?.rendered_seq || 0);
const briefNotesMaxSeq = computed(() => brief.value?.notes_max_seq || 0);
const briefKbBound = computed(() => !!brief.value?.kb_bound);
const briefStale = computed(() =>
  briefSeqStale(briefRenderedSeq.value, briefNotesMaxSeq.value),
);
const briefLastText = computed(() => {
  const at = brief.value?.last_rendered_at;
  if (!at) return t('sop.settings.briefNever');
  return at;
});

const deleteBlockedReason = computed(() => {
  if (!project.value) return t('sop.settings.deleteNoProject');
  if (store.projects.length <= 1) return t('sop.settings.deleteLast');
  if (activeRuns.value > 0) return t('sop.settings.deleteBusy', { n: activeRuns.value });
  return '';
});

watch(
  () => store.currentProjectId,
  () => {
    activeTab.value = 'basic';
    void load();
  },
);

function backToItems() {
  void router.push({ name: 'sopItems' });
}

function syncForm(target: SopProject) {
  form.name = target.name;
  form.code = target.code;
  form.description = target.description;
  form.default_template_id = target.default_template_id;
  form.default_executor = target.default_executor || 'platform_agent';
  form.on_run_success_item_status = target.on_run_success_item_status;
  form.workspace_root = target.workspace_root;
  form.notify_on_gate_or_fail = target.notify_on_gate_or_fail;
}

/**
 * 打开本页时可能带着 localStorage 里的旧项目 id（项目已被删/换账号），
 * 或项目列表还没拉回来。先对齐到真实存在的项目，避免 404 变成未捕获异常。
 */
async function ensureProject(): Promise<string> {
  if (!store.projects.length) {
    await store.loadProjects().catch(() => undefined);
  }
  const ids = store.projects.map((p) => p.id);
  if (ids.includes(store.currentProjectId)) return store.currentProjectId;
  const fallback = ids[0] || '';
  if (fallback) await store.switchProject(fallback).catch(() => undefined);
  return fallback;
}

async function load() {
  const projectId = await ensureProject();
  if (!projectId) {
    project.value = null;
    return;
  }
  let detail: SopProject;
  let runs: Awaited<ReturnType<typeof listRuns>>;
  try {
    [detail, runs] = await Promise.all([
      getProject(projectId),
      listRuns({ project_id: projectId }).catch(() => []),
    ]);
  } catch {
    project.value = null;
    return;
  }
  project.value = detail;
  repos.value = detail.repos || [];
  runCount.value = runs.length;
  // 进行中/待审批的运行会阻塞删除：后端也会拒绝，前端提前禁用并说明原因
  activeRuns.value = runs.filter((run) =>
    ['queued', 'running', 'waiting', 'paused'].includes(run.status),
  ).length;
  syncForm(detail);
  // 附录数据失败不阻塞页面：下拉为空比整页报错更好用
  const [tpls, kbs] = await Promise.all([
    listTemplates(projectId).catch(() => []),
    listKnowledgeBases().catch(() => ({ data: [] as { id: string; name: string }[] })),
  ]);
  templates.value = tpls;
  const kbData = (kbs as { data?: { id: string; name: string }[] })?.data || [];
  kbList.value = kbData.map((kb) => ({ id: String(kb.id), name: kb.name }));
  await store.loadStats().catch(() => undefined);
  await loadBriefStatus();
}

/** 简报状态（正文 + 水位 + 绑库）：失败不阻塞配置页，只让卡片显示未渲染。 */
async function loadBriefStatus() {
  if (!project.value) return;
  try {
    brief.value = await getBrief(project.value.id);
  } catch {
    brief.value = null;
  }
}

/** 改绑知识库：后端把旧自建库改名「（已迁移）」并清空简报绑定后全量重渲染。 */
async function rebindKB() {
  if (!project.value || !rebindKbId.value) return;
  rebindingKb.value = true;
  try {
    project.value = await replaceProjectKB(project.value.id, rebindKbId.value);
    repos.value = project.value.repos || [];
    rebindKbId.value = '';
    MessagePlugin.success(t('sop.kbCard.bindDone'));
    await store.loadProjects().catch(() => undefined);
    await loadBriefStatus();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.kbCard.bindFailed'));
  } finally {
    rebindingKb.value = false;
  }
}

/** 卡片上的「立即重新生成」：等价于面板里的同一操作。 */
async function renderBriefNow() {
  if (!project.value) return;
  renderingBrief.value = true;
  try {
    brief.value = await renderBrief(project.value.id);
    MessagePlugin.success(t('sop.brief.rendered'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.brief.renderFailed'));
  } finally {
    renderingBrief.value = false;
  }
}

async function saveBasic() {
  if (!project.value) return;
  const name = form.name.trim();
  if (!name) {
    MessagePlugin.warning(t('sop.project.nameRequired'));
    return;
  }
  saving.value = true;
  try {
    // 描述多行折叠为单行：后端按单行存，避免列表/统计带出现换行
    const updated = await updateProject(project.value.id, {
      name,
      description: form.description.replace(/\s*\n\s*/g, ' ').trim(),
      kb_id: rebindKbId.value || undefined,
      default_template_id: form.default_template_id || undefined,
      default_executor: form.default_executor || undefined,
      on_run_success_item_status: form.on_run_success_item_status || undefined,
      workspace_root: form.workspace_root.trim() || undefined,
      notify_on_gate_or_fail: form.notify_on_gate_or_fail,
    });
    project.value = updated;
    repos.value = updated.repos || [];
    syncForm(updated);
    if (rebindKbId.value) rebindKbId.value = '';
    MessagePlugin.success(t('sop.settings.saveSuccess'));
    await store.loadProjects();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.settings.saveFailed'));
  } finally {
    saving.value = false;
  }
}

function openAddRepo() {
  repoModal.visible = true;
  repoModal.url = '';
  repoModal.branch = 'main';
  repoModal.name = '';
  repoModal.error = '';
  repoModal.submitting = false;
}

async function submitRepo() {
  if (!project.value) return;
  const url = repoModal.url.trim();
  repoModal.error = '';
  if (!validateRepoURL(url)) {
    repoModal.error = t('sop.settings.repoUrlInvalid');
    return;
  }
  repoModal.submitting = true;
  try {
    const added = await addRepo(project.value.id, {
      url,
      branch: repoModal.branch.trim() || 'main',
      name: repoModal.name.trim() || repoDisplayName(url),
    });
    repos.value = [...repos.value, added];
    await refreshProject();
    repoModal.visible = false;
    MessagePlugin.success(t('sop.settings.repoAdded'));
  } catch (error: any) {
    repoModal.error = error?.message || t('sop.settings.repoAddFailed');
  } finally {
    repoModal.submitting = false;
  }
}

async function makeDefault(repo: SopRepo) {
  if (!project.value) return;
  try {
    await setDefaultRepo(project.value.id, repo.id);
    await refreshProject();
    MessagePlugin.success(t('sop.settings.repoDefaultSet', { name: repo.name }));
  } catch {
    MessagePlugin.error(t('sop.settings.repoDefaultFailed'));
  }
}

function askRemoveRepo(repo: SopRepo) {
  if (!project.value) return;
  confirmDanger({
    title: t('sop.settings.repoRemoveTitle'),
    body: t('sop.settings.repoRemoveBody', { name: repo.name }),
    confirmText: t('sop.settings.repoRemove'),
    onConfirm: async () => {
      try {
        await removeRepo(project.value!.id, repo.id);
        await refreshProject();
        MessagePlugin.success(t('sop.settings.repoRemoved'));
      } catch {
        MessagePlugin.error(t('sop.settings.repoRemoveFailed'));
      }
    },
  });
}

async function refreshProject() {
  if (!project.value) return;
  const detail = await getProject(project.value.id);
  project.value = detail;
  repos.value = detail.repos || [];
  await store.loadProjects().catch(() => undefined);
}

function askDeleteProject() {
  if (!project.value) return;
  confirmDanger({
    title: t('sop.settings.dangerConfirmTitle', { name: project.value.name }),
    body: t('sop.settings.dangerConfirmBody', {
      items: stats.value.total,
      runs: runCount.value,
    }),
    confirmText: t('sop.settings.dangerDelete'),
    onConfirm: async () => {
      try {
        await deleteProject(project.value!.id);
        MessagePlugin.success(t('sop.settings.dangerDeleted'));
        await store.loadProjects();
        await router.push({ name: 'sopItems' });
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('sop.settings.dangerDeleteFailed'));
      }
    },
  });
}

function askUnbindKb() {
  confirmDanger({
    title: t('sop.settings.kbUnbindTitle'),
    body: t('sop.settings.kbUnbindBody'),
    confirmText: t('sop.settings.kbUnbind'),
    onConfirm: async () => {
      if (!project.value) return;
      try {
        const updated = await updateProject(project.value.id, { kb_id: '' });
        project.value = updated;
        MessagePlugin.success(t('sop.settings.kbUnboundDone'));
        await store.loadProjects().catch(() => undefined);
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('sop.settings.saveFailed'));
      }
    },
  });
}

onMounted(load);
</script>
<style lang="less" scoped src="./sopProjectSettings.less"></style>
