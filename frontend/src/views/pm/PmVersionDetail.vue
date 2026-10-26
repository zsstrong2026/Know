<template>
  <div class="pm-version-detail">
    <div class="vd-scroll">
      <!-- 面包屑：返回列表 -->
      <nav class="vd-crumb">
        <t-button variant="text" size="small" @click="backToList">
          ← {{ $t('pm.version.detail.back') }}
        </t-button>
        <span v-if="version" class="vd-crumb__sep">/</span>
        <span v-if="version" class="vd-crumb__current">{{ headline }}</span>
      </nav>

      <div v-if="loading && !version" class="vd-state"><t-loading /></div>
      <div v-else-if="!version" class="vd-state vd-empty">
        <t-icon name="error-circle" size="40px" />
        <p>{{ $t('pm.version.detail.loadFailed') }}</p>
        <t-button variant="outline" @click="reload">{{ $t('pm.version.detail.retry') }}</t-button>
      </div>

      <template v-else>
        <!-- 版本头：版本号 + 名称 + 状态 + 操作组 -->
        <header class="vd-head">
          <span class="vd-head__no">{{ versionDisplayNo(version) || $t('pm.version.unfinalized') }}</span>
          <span class="vd-head__name">{{ version.name }}</span>
          <t-tag :theme="statusMeta(version.status).theme" variant="light" size="small">
            {{ $t(statusMeta(version.status).labelKey) }}
          </t-tag>
          <div class="vd-head__ops">
            <t-button v-if="showGenerate" variant="outline" size="small" @click="openDialog('generate')">
              {{ $t('pm.version.actions.generate') }}
            </t-button>
            <t-button v-if="canReview(version)" variant="outline" size="small" @click="openDialog('review')">
              {{ $t('pm.version.actions.review') }}
            </t-button>
            <t-button v-if="canRegenerate(version)" variant="outline" size="small" @click="openDialog('regenerate')">
              {{ $t('pm.version.actions.regenerate') }}
            </t-button>
            <t-button v-if="canFinalize(version)" variant="outline" size="small" @click="openDialog('finalize')">
              {{ $t('pm.version.actions.finalize') }}
            </t-button>
            <t-button v-if="canDevelop(version)" variant="outline" size="small" @click="changeStatus('developing')">
              {{ $t('pm.version.actions.develop') }}
            </t-button>
            <t-button v-if="canLaunch(version)" variant="outline" size="small" @click="changeStatus('launched')">
              {{ $t('pm.version.actions.launch') }}
            </t-button>
            <t-button v-if="canShelve(version)" variant="outline" size="small" @click="openDialog('shelve')">
              {{ $t('pm.version.actions.shelve') }}
            </t-button>
            <t-button v-if="canRestore(version)" variant="outline" size="small" @click="changeStatus('planning')">
              {{ $t('pm.version.actions.restore') }}
            </t-button>
          </div>
        </header>

        <!-- 需求集合变动提示：变动已同步需求池，提示重新生成产物 -->
        <div v-if="version.requirements_changed && canGenerate(version)" class="vd-banner">
          <span class="vd-banner__title">{{ $t('pm.version.detail.banner.title') }}</span>
          <span class="vd-banner__text">{{ $t('pm.version.detail.banner.text') }}</span>
          <t-button
            variant="text"
            size="small"
            class="vd-banner__action"
            @click="openDialog(canRegenerate(version) ? 'regenerate' : 'generate')"
          >
            {{ $t('pm.version.detail.banner.action') }}
          </t-button>
        </div>

        <div class="vd-grid">
          <!-- 左：版本信息 -->
          <section class="vd-card">
            <header class="vd-card__head">{{ $t('pm.version.detail.info.title') }}</header>
            <div class="vd-card__body">
              <div v-for="row in infoRows" :key="row.key" class="vd-kv">
                <span class="vd-kv__k">{{ row.label }}</span>
                <span class="vd-kv__v">{{ row.value }}</span>
              </div>
              <div class="vd-kv">
                <span class="vd-kv__k">{{ $t('pm.version.detail.info.releaseNote') }}</span>
                <span class="vd-kv__v">{{ version.release_note || '—' }}</span>
              </div>
              <div class="vd-kv vd-kv--top">
                <span class="vd-kv__k">{{ $t('pm.version.detail.info.outline') }}</span>
                <span class="vd-kv__v">
                  <div v-if="version.update_outline" class="vd-outline">{{ version.update_outline }}</div>
                  <span v-else>—</span>
                </span>
              </div>
            </div>
          </section>

          <!-- 中：需求集合 -->
          <section class="vd-card">
            <header class="vd-card__head">
              <span>{{ $t('pm.version.detail.requirements.title') }}</span>
              <span class="vd-card__right">
                {{ $t('pm.version.detail.requirements.count', { count: requirementCount }) }}
              </span>
            </header>
            <div class="vd-card__body">
              <div v-if="requirementLoading && !requirements.length" class="vd-muted">
                <t-loading size="small" />
              </div>
              <p v-else-if="!requirements.length" class="vd-muted">
                {{ $t('pm.version.detail.requirements.empty') }}
              </p>
              <template v-else>
                <div v-for="item in requirements" :key="item.id" class="vd-req">
                  <t-tag :theme="priorityTheme(item.priority)" variant="light" size="small">
                    {{ item.priority }}
                  </t-tag>
                  <span class="vd-req__title" @click="openRequirement(item)">{{ item.title }}</span>
                  <t-tag :theme="requirementStatusMeta(item.status).theme" variant="light" size="small">
                    {{ $t(requirementStatusMeta(item.status).labelKey) }}
                  </t-tag>
                  <!-- 仅规划中/评审中可编辑需求集合，定稿后锁定 -->
                  <template v-if="canEditRequirementSet(version)">
                    <t-button variant="text" size="small" @click="openRequirementDialog('remove', item)">
                      {{ $t('pm.version.detail.requirements.remove') }}
                    </t-button>
                    <t-button variant="text" size="small" @click="openRequirementDialog('shelve', item)">
                      {{ $t('pm.version.detail.requirements.shelve') }}
                    </t-button>
                    <t-button variant="text" size="small" @click="openRequirementDialog('change', item)">
                      {{ $t('pm.version.detail.requirements.change') }}
                    </t-button>
                  </template>
                </div>
              </template>
              <p class="vd-muted vd-muted--foot">{{ $t('pm.version.detail.requirements.hint') }}</p>
            </div>
          </section>

          <!-- 右：产物（PRD / 原型 / 功能清单） -->
          <section class="vd-card">
            <header class="vd-card__head">
              <span>{{ $t('pm.version.detail.products.title') }}</span>
              <span v-if="version.generated_at" class="vd-card__right">
                {{ $t('pm.version.detail.products.generatedAt', { time: generatedAt }) }}
              </span>
            </header>
            <div class="vd-card__body">
              <!-- PRD -->
              <div class="vd-prod">
                <t-tag theme="primary" variant="light" size="small">
                  {{ $t('pm.version.detail.products.prd') }}
                </t-tag>
                <div class="vd-prod__main">
                  <span class="vd-prod__name">{{ prdFileName }}</span>
                  <span class="vd-prod__path">
                    {{ $t('pm.version.detail.products.prdPath', { name: version.name }) }}
                  </span>
                </div>
                <div v-if="version.prd_knowledge_id" class="vd-prod__ops">
                  <t-button variant="text" size="small" @click="openKnowledge(version.prd_knowledge_id)">
                    {{ $t('pm.version.detail.products.view') }}
                  </t-button>
                  <t-button variant="text" size="small" @click="downloadArtifact(version.prd_knowledge_id, prdFileName)">
                    {{ $t('pm.version.detail.products.download') }}
                  </t-button>
                </div>
                <span v-else class="vd-muted">{{ $t('pm.version.detail.products.notGenerated') }}</span>
              </div>

              <!-- 原型 -->
              <div class="vd-prod">
                <t-tag theme="primary" variant="light" size="small">
                  {{ $t('pm.version.detail.products.prototype') }}
                </t-tag>
                <div class="vd-prod__main">
                  <span class="vd-prod__name">{{ prototypeFileName }}</span>
                  <span class="vd-prod__path">
                    {{ $t('pm.version.detail.products.prototypePath', { name: version.name }) }}
                  </span>
                </div>
                <div v-if="version.prototype_knowledge_id" class="vd-prod__ops">
                  <t-button variant="text" size="small" @click="previewArtifact(version.prototype_knowledge_id)">
                    {{ $t('pm.version.detail.products.preview') }}
                  </t-button>
                  <t-button
                    variant="text"
                    size="small"
                    @click="downloadArtifact(version.prototype_knowledge_id, prototypeFileName)"
                  >
                    {{ $t('pm.version.detail.products.download') }}
                  </t-button>
                </div>
                <span v-else class="vd-muted">{{ $t('pm.version.detail.products.notGenerated') }}</span>
              </div>

              <!-- 功能清单 -->
              <div class="vd-prod">
                <t-tag theme="success" variant="light" size="small">
                  {{ $t('pm.version.detail.products.features') }}
                </t-tag>
                <div class="vd-prod__main">
                  <span class="vd-prod__name">
                    {{ $t('pm.version.detail.products.synced', { count: version.artifact_feature_count || 0 }) }}
                  </span>
                  <span class="vd-prod__path">{{ $t('pm.version.detail.products.featuresPath') }}</span>
                </div>
                <div class="vd-prod__ops">
                  <t-button variant="text" size="small" @click="openFeatures">
                    {{ $t('pm.version.detail.products.view') }}
                  </t-button>
                </div>
              </div>
            </div>
          </section>
        </div>

        <!-- 评审记录 -->
        <section class="vd-card vd-review">
          <header class="vd-card__head">
            <span>{{ $t('pm.version.detail.review.title') }}</span>
            <span class="vd-card__right">
              {{ $t('pm.version.detail.review.summary', { rounds: rounds.length, pending: pendingCount }) }}
            </span>
          </header>
          <div class="vd-card__body">
            <p v-if="!rounds.length" class="vd-muted">{{ $t('pm.version.detail.review.empty') }}</p>
            <div v-for="round in rounds" :key="round.round" class="vd-round">
              <div class="vd-round__head">
                <b>{{ $t('pm.version.detail.review.round', { round: round.round }) }}</b>
                <t-tag
                  v-if="round.conclusion"
                  :theme="conclusionMeta(round.conclusion).theme"
                  variant="light"
                  size="small"
                >
                  {{ $t(conclusionMeta(round.conclusion).labelKey) }}
                </t-tag>
                <span class="vd-round__meta">
                  {{ $t('pm.version.detail.review.meta', {
                    date: formatPmDate(round.reviewed_at),
                    reviewers: round.reviewers || '—',
                  }) }}
                </span>
              </div>

              <div v-for="(opinion, index) in round.opinions" :key="opinion.index" class="vd-opinion">
                <div class="vd-opinion__top">
                  <span class="vd-opinion__text">
                    {{ $t('pm.version.detail.review.opinion', { index: index + 1, text: opinion.text }) }}
                  </span>
                  <t-tag :theme="opinion.status === 'resolved' ? 'success' : 'warning'" variant="light" size="small">
                    {{ $t(`pm.version.detail.review.${opinion.status}`) }}
                  </t-tag>
                  <t-button variant="text" size="small" @click="openOpinion(round.round, opinion)">
                    {{ $t('pm.version.detail.review.editStatus') }}
                  </t-button>
                </div>
                <div class="vd-opinion__sub">
                  {{ $t('pm.version.detail.review.note', { note: opinion.note || '—' }) }}
                </div>
              </div>

              <div class="vd-round__meta vd-round__change">
                {{ $t('pm.version.detail.review.changeNote', { note: round.changeNote || '—' }) }}
              </div>
            </div>
          </div>
        </section>

        <p class="vd-note">{{ $t('pm.version.detail.note') }}</p>
      </template>
    </div>

    <!-- 评审意见状态修改 -->
    <t-dialog
      v-model:visible="opinionVisible"
      :header="$t('pm.version.detail.opinionDialog.title')"
      width="520px"
      placement="center"
      :footer="false"
      @close="opinionVisible = false"
    >
      <t-form label-align="top">
        <t-form-item :label="$t('pm.version.detail.opinionDialog.status')">
          <t-radio-group v-model="opinionForm.status" variant="default-filled">
            <t-radio-button value="pending">{{ $t('pm.version.detail.review.pending') }}</t-radio-button>
            <t-radio-button value="resolved">{{ $t('pm.version.detail.review.resolved') }}</t-radio-button>
          </t-radio-group>
        </t-form-item>
        <t-form-item :label="$t('pm.version.detail.opinionDialog.note')">
          <t-textarea
            v-model="opinionForm.note"
            :autosize="{ minRows: 6, maxRows: 6 }"
            :maxlength="500"
            :placeholder="$t('pm.version.detail.opinionDialog.notePlaceholder')"
          />
        </t-form-item>
      </t-form>
      <div class="vd-foot">
        <t-button variant="outline" @click="opinionVisible = false">
          {{ $t('pm.version.dialogs.cancel') }}
        </t-button>
        <t-button theme="primary" :loading="opinionSaving" @click="submitOpinion">
          {{ $t('pm.version.detail.opinionDialog.submit') }}
        </t-button>
      </div>
    </t-dialog>

    <!-- 需求详情抽屉 -->
    <PmRequirementDetail
      v-model:visible="requirementVisible"
      :requirement="activeRequirement"
      @changed="reload"
    />

    <!-- 版本级操作弹窗与需求集合操作弹窗 -->
    <PmVersionDialogs ref="dialogsRef" @changed="reload" />
    <PmVersionRequirementDialogs ref="reqDialogsRef" @changed="reload" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  getPmVersion,
  listPmVersionRequirements,
  updatePmVersion,
  updatePmVersionReviewOpinion,
  type PmRequirement,
  type PmVersion,
  type PmVersionStatus,
} from '@/api/pm';
import { downKnowledgeDetails, previewKnowledgeFile } from '@/api/knowledge-base';
import { usePmProjectsStore } from '@/stores/pmProjects';
import { REQUIREMENT_STATUS_MAP, VERSION_CONCLUSION_MAP, VERSION_STATUS_MAP } from './pmStatus';
import { formatPmTime, priorityTheme } from './pmRequirementState';
import {
  canDevelop,
  canEditRequirementSet,
  canFinalize,
  canGenerate,
  canLaunch,
  canRegenerate,
  canRestore,
  canReview,
  canShelve,
  countPendingOpinions,
  formatPmDate,
  readReviewRounds,
  versionDisplayNo,
  type VersionReviewOpinion,
} from './pmVersionState';
import PmVersionDialogs from './PmVersionDialogs.vue';
import PmVersionRequirementDialogs from './PmVersionRequirementDialogs.vue';
import PmRequirementDetail from './PmRequirementDetail.vue';

const route = useRoute();
const router = useRouter();
const { t } = useI18n();

// 项目知识库用于跳转产物文档
const projectStore = usePmProjectsStore();
const { projects } = storeToRefs(projectStore);

const projectId = computed(() => String(route.params.projectId || ''));
const versionId = computed(() => String(route.params.versionId || ''));

const loading = ref(false);
const version = ref<PmVersion | null>(null);
const requirements = ref<PmRequirement[]>([]);
const requirementLoading = ref(false);

const dialogsRef = ref<InstanceType<typeof PmVersionDialogs> | null>(null);
const reqDialogsRef = ref<InstanceType<typeof PmVersionRequirementDialogs> | null>(null);

const requirementVisible = ref(false);
const activeRequirement = ref<PmRequirement | null>(null);

// ===== 展示派生 =====

const headline = computed(() => {
  if (!version.value) return '';
  return `${versionDisplayNo(version.value) || t('pm.version.unfinalized')} ${version.value.name}`;
});

const infoRows = computed(() => {
  const current = version.value;
  if (!current) return [];
  return [
    { key: 'plan', label: t('pm.version.detail.info.planDate'), value: formatPmDate(current.plan_date) },
    {
      key: 'planLaunch',
      label: t('pm.version.detail.info.planLaunchDate'),
      value: formatPmDate(current.plan_launch_date),
    },
    { key: 'finalize', label: t('pm.version.detail.info.finalizeDate'), value: formatPmDate(current.finalize_date) },
    { key: 'launch', label: t('pm.version.detail.info.launchDate'), value: formatPmDate(current.launch_date) },
  ];
});

const requirementCount = computed(() => requirements.value.length || version.value?.requirement_count || 0);
const generatedAt = computed(() => formatPmTime(version.value?.generated_at));

// 未定稿版本沿用「未定稿」占位生成文件名，与原型保持一致
const fileLabel = computed(() => (version.value
  ? versionDisplayNo(version.value) || t('pm.version.unfinalized')
  : ''));
const prdFileName = computed(() => `PRD-${fileLabel.value}.md`);
const prototypeFileName = computed(() => `原型-${fileLabel.value}.html`);

const rounds = computed(() => readReviewRounds(version.value?.review_opinions));
const pendingCount = computed(() => countPendingOpinions(rounds.value));

// 首次生成：可生成但还没有任何产物轮次
const showGenerate = computed(() => !!version.value && canGenerate(version.value) && !canRegenerate(version.value));

function statusMeta(status: string) {
  return VERSION_STATUS_MAP[status] ?? VERSION_STATUS_MAP.planning;
}

function requirementStatusMeta(status: string) {
  return REQUIREMENT_STATUS_MAP[status] ?? REQUIREMENT_STATUS_MAP.pending_analysis;
}

function conclusionMeta(conclusion: string) {
  return VERSION_CONCLUSION_MAP[conclusion] ?? VERSION_CONCLUSION_MAP.reject;
}

// ===== 数据加载 =====

async function loadDetail() {
  if (!versionId.value) return;
  loading.value = true;
  try {
    version.value = await getPmVersion(versionId.value);
  } catch {
    version.value = null;
    MessagePlugin.error(t('pm.version.detail.loadFailed'));
  } finally {
    loading.value = false;
  }
}

async function loadRequirements() {
  if (!versionId.value) return;
  requirementLoading.value = true;
  try {
    requirements.value = await listPmVersionRequirements(versionId.value);
  } catch {
    requirements.value = [];
  } finally {
    requirementLoading.value = false;
  }
}

async function reload() {
  await Promise.all([loadDetail(), loadRequirements()]);
}

onMounted(() => void reload());

// 详情页内切换版本时组件复用，先清空旧数据再加载
watch(versionId, () => {
  version.value = null;
  requirements.value = [];
  void reload();
});

// ===== 导航与状态流转 =====

function backToList() {
  void router.push({ name: 'pmVersions', params: { projectId: projectId.value } });
}

function openDialog(action: 'review' | 'finalize' | 'generate' | 'regenerate' | 'shelve') {
  if (!version.value) return;
  dialogsRef.value?.open(action, version.value);
}

function openRequirementDialog(action: 'remove' | 'shelve' | 'change', item: PmRequirement) {
  if (!version.value) return;
  reqDialogsRef.value?.open(action, version.value, item);
}

function openRequirement(item: PmRequirement) {
  activeRequirement.value = item;
  requirementVisible.value = true;
}

function openFeatures() {
  void router.push({ name: 'pmFeatures', params: { projectId: projectId.value } });
}

// 启动开发 / 上线 / 恢复规划：直接变更状态
async function changeStatus(status: PmVersionStatus) {
  if (!version.value) return;
  try {
    await updatePmVersion(version.value.id, { status });
    MessagePlugin.success(t('pm.version.actionSuccess'));
    await reload();
  } catch {
    MessagePlugin.error(t('pm.version.actionFailed'));
  }
}

// ===== 产物访问 =====

const kbId = computed(() => projects.value.find((item) => item.id === projectId.value)?.kb_id || '');

function openKnowledge(knowledgeId: string) {
  if (!kbId.value) {
    MessagePlugin.warning(t('pm.version.detail.products.kbMissing'));
    return;
  }
  void router.push({
    name: 'knowledgeBaseDetail',
    params: { kbId: kbId.value },
    query: { knowledge_id: knowledgeId },
  });
}

async function downloadArtifact(knowledgeId: string, fileName: string) {
  try {
    const blob = await downKnowledgeDetails(knowledgeId);
    const objectUrl = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.style.display = 'none';
    link.href = objectUrl;
    link.download = fileName;
    document.body.appendChild(link);
    link.click();
    nextTick(() => {
      link.remove();
      URL.revokeObjectURL(objectUrl);
    });
  } catch {
    MessagePlugin.error(t('pm.version.detail.products.downloadFailed'));
  }
}

async function previewArtifact(knowledgeId: string) {
  try {
    const blob = await previewKnowledgeFile(knowledgeId);
    const objectUrl = URL.createObjectURL(blob);
    window.open(objectUrl, '_blank', 'noopener');
    // 延迟释放，避免新窗口尚未完成加载
    window.setTimeout(() => URL.revokeObjectURL(objectUrl), 60_000);
  } catch {
    MessagePlugin.error(t('pm.version.detail.products.previewFailed'));
  }
}

// ===== 评审意见状态 =====

const opinionVisible = ref(false);
const opinionSaving = ref(false);
const opinionRound = ref(0);
const opinionForm = reactive<{ status: 'pending' | 'resolved'; note: string }>({
  status: 'pending',
  note: '',
});

function openOpinion(round: number, opinion: VersionReviewOpinion) {
  opinionRound.value = round;
  opinionIndex.value = opinion.index;
  opinionForm.status = opinion.status;
  opinionForm.note = opinion.note;
  opinionVisible.value = true;
}

const opinionIndex = ref(0);

async function submitOpinion() {
  if (!version.value || opinionSaving.value) return;
  opinionSaving.value = true;
  try {
    version.value = await updatePmVersionReviewOpinion(version.value.id, {
      round: opinionRound.value,
      index: opinionIndex.value,
      status: opinionForm.status,
      note: opinionForm.note.trim(),
    });
    MessagePlugin.success(t('pm.version.detail.opinionDialog.success'));
    opinionVisible.value = false;
  } catch {
    MessagePlugin.error(t('pm.version.detail.opinionDialog.failed'));
  } finally {
    opinionSaving.value = false;
  }
}
</script>

<style lang="less" scoped>
.pm-version-detail {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.vd-scroll {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: var(--app-space-6);
}

/* ---------- 面包屑 ---------- */
.vd-crumb {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  margin-bottom: var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.vd-crumb__current {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ---------- 状态 ---------- */
.vd-state {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--app-space-10) 0;
}

.vd-empty {
  flex-direction: column;
  gap: var(--app-space-2);
  color: var(--td-text-color-placeholder);

  p {
    margin: 0;
  }
}

/* ---------- 版本头 ---------- */
.vd-head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
  margin-bottom: var(--app-space-3);
}

.vd-head__no {
  font-size: var(--app-text-2xl);
  font-weight: 700;
  color: var(--td-text-color-primary);
}

.vd-head__name {
  font-size: var(--app-text-2xl);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.vd-head__ops {
  margin-left: auto;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-1);
}

/* ---------- 需求变动提示 ---------- */
.vd-banner {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-3);
  margin-bottom: var(--app-space-3);
  border: 1px solid #f0c674;
  border-radius: var(--app-radius-md);
  background: #fffbea;
  font-size: var(--app-text-sm);
}

.vd-banner__title {
  font-weight: 700;
  color: #ea9010;
}

.vd-banner__text {
  color: var(--td-text-color-secondary);
}

.vd-banner__action {
  margin-left: auto;
}

/* ---------- 三栏 ---------- */
.vd-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.1fr) minmax(0, 1fr);
  gap: var(--app-space-3);
  align-items: start;
}

@media (max-width: 1280px) {
  .vd-grid {
    grid-template-columns: 1fr;
  }
}

.vd-card {
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  overflow: hidden;
}

.vd-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--app-space-2);
  padding: var(--app-space-3) var(--app-space-4);
  border-bottom: 1px solid var(--td-component-stroke);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.vd-card__right {
  font-size: var(--app-text-xs);
  font-weight: 400;
  color: var(--td-text-color-placeholder);
}

.vd-card__body {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2);
  padding: var(--app-space-3) var(--app-space-4);
}

/* ---------- 版本信息 ---------- */
.vd-kv {
  display: flex;
  gap: var(--app-space-3);
  font-size: var(--app-text-sm);
  line-height: 1.7;

  &--top {
    align-items: flex-start;
  }
}

.vd-kv__k {
  flex: 0 0 84px;
  color: var(--td-text-color-secondary);
}

.vd-kv__v {
  flex: 1;
  min-width: 0;
  color: var(--td-text-color-primary);
  word-break: break-word;
}

.vd-outline {
  white-space: pre-wrap;
  padding: var(--app-space-2) var(--app-space-3);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-page);
  line-height: 1.7;
}

/* ---------- 需求集合 ---------- */
.vd-req {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-1) var(--app-space-2);
  font-size: var(--app-text-sm);
}

.vd-req__title {
  flex: 1;
  min-width: 120px;
  cursor: pointer;
  color: var(--td-text-color-primary);
  word-break: break-word;

  &:hover {
    color: var(--td-brand-color);
  }
}

.vd-muted {
  margin: 0;
  font-size: var(--app-text-xs);
  line-height: 1.7;
  color: var(--td-text-color-placeholder);

  &--foot {
    margin-top: var(--app-space-1);
    padding-top: var(--app-space-2);
    border-top: 1px dashed var(--td-component-stroke);
  }
}

/* ---------- 产物 ---------- */
.vd-prod {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-2);
  font-size: var(--app-text-sm);
}

.vd-prod__main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.vd-prod__name {
  font-weight: 600;
  color: var(--td-text-color-primary);
  word-break: break-all;
}

.vd-prod__path {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.vd-prod__ops {
  display: flex;
  align-items: center;
  gap: 2px;
}

/* ---------- 评审记录 ---------- */
.vd-review {
  margin-top: var(--app-space-3);
}

.vd-round {
  padding: var(--app-space-2) 0;
  border-bottom: 1px dashed var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.vd-round__head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
  font-size: var(--app-text-sm);
}

.vd-round__meta {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.vd-round__change {
  margin-top: var(--app-space-1);
}

.vd-opinion {
  margin-top: var(--app-space-2);
  padding-left: var(--app-space-3);

  &__top {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--app-space-2);
    font-size: var(--app-text-sm);
  }

  &__text {
    flex: 1;
    min-width: 160px;
    color: var(--td-text-color-primary);
  }

  &__sub {
    margin-top: 2px;
    font-size: var(--app-text-xs);
    color: var(--td-text-color-secondary);
  }
}

.vd-note {
  margin: var(--app-space-3) 0 0;
  font-size: var(--app-text-xs);
  line-height: 1.8;
  color: var(--td-text-color-placeholder);
}

.vd-foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--app-space-2);
  margin-top: var(--app-space-4);
}
</style>
