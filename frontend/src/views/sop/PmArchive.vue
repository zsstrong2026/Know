<template>
  <div class="sop-archive">
    <header class="sop-archive-head">
      <div>
        <h2 class="sop-archive-title">{{ $t('sop.arch.title') }}</h2>
        <p class="sop-archive-desc">{{ $t('sop.arch.desc') }}</p>
      </div>
      <t-button size="small" variant="outline" :loading="refreshing" @click="refresh">⟳</t-button>
    </header>

    <div class="sop-archive-banner">{{ $t('sop.arch.banner') }}</div>

    <div class="sop-archive-summary">
      <div class="sop-stat"><span class="n">{{ summary.projects }}</span><span class="l">{{ $t('sop.arch.statProjects') }}</span></div>
      <div class="sop-stat"><span class="n">{{ summary.items }}</span><span class="l">{{ $t('sop.arch.statItems') }}</span></div>
      <div class="sop-stat"><span class="n">{{ summary.runs }}</span><span class="l">{{ $t('sop.arch.statRuns') }}</span></div>
      <div class="sop-stat"><span class="n">{{ summary.attachments }}</span><span class="l">{{ $t('sop.arch.statAttachments') }}</span></div>
    </div>

    <div class="sop-archive-tabs">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        type="button"
        class="sop-archive-tab"
        :class="{ on: activeTab === tab.key }"
        @click="activeTab = tab.key"
      >
        {{ tab.label }}
      </button>
    </div>

    <!-- 版本 -->
    <section v-if="activeTab === 'versions'" class="sop-archive-pane">
      <table class="sop-table">
        <thead>
          <tr>
            <th>{{ $t('sop.arch.colVersion') }}</th>
            <th>{{ $t('sop.arch.colStatus') }}</th>
            <th>{{ $t('sop.arch.colRound') }}</th>
            <th>{{ $t('sop.arch.colConclusion') }}</th>
            <th>{{ $t('sop.arch.colLaunch') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="version in versions" :key="version.id">
            <td><b>{{ version.version_no }}</b> {{ version.name }}</td>
            <td><span class="sop-tag">{{ version.status }}</span></td>
            <td>{{ version.review_round }}</td>
            <td>{{ version.review_conclusion || '—' }}</td>
            <td class="mono">{{ version.launch_date || '—' }}</td>
          </tr>
          <tr v-if="!versions.length"><td colspan="5" class="sop-empty">{{ $t('sop.arch.empty') }}</td></tr>
        </tbody>
      </table>
    </section>

    <!-- 功能树（只读展平） -->
    <section v-else-if="activeTab === 'features'" class="sop-archive-pane">
      <ul class="sop-tree">
        <li v-for="node in flatFeatures" :key="node.id" :style="{ paddingLeft: `${node.depth * 18}px` }">
          <span class="sop-tree-dot" />
          {{ node.name }}
          <span v-if="node.latest_version_no" class="sop-tag">{{ node.latest_version_no }}</span>
        </li>
        <li v-if="!flatFeatures.length" class="sop-empty">{{ $t('sop.arch.empty') }}</li>
      </ul>
    </section>

    <!-- 资料 -->
    <section v-else-if="activeTab === 'materials'" class="sop-archive-pane">
      <table class="sop-table">
        <thead>
          <tr>
            <th>{{ $t('sop.arch.colTitle') }}</th>
            <th>{{ $t('sop.arch.colType') }}</th>
            <th>{{ $t('sop.arch.colStatus') }}</th>
            <th>{{ $t('sop.arch.colUpdated') }}</th>
            <th>{{ $t('sop.arch.colActions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="material in materials" :key="material.id">
            <td>{{ material.title }}</td>
            <td>{{ material.material_type }}</td>
            <td><span class="sop-tag">{{ material.status }}</span></td>
            <td class="mono">{{ formatTime(material.updated_at) }}</td>
            <td>
              <t-button v-if="material.knowledge_id" size="small" variant="text" @click="previewMaterial(material)">
                {{ $t('sop.arch.openDoc') }}
              </t-button>
              <span v-else class="sop-muted">{{ $t('sop.arch.noDoc') }}</span>
            </td>
          </tr>
          <tr v-if="!materials.length"><td colspan="5" class="sop-empty">{{ $t('sop.arch.empty') }}</td></tr>
        </tbody>
      </table>
    </section>

    <!-- 批次历史 -->
    <section v-else class="sop-archive-pane">
      <table class="sop-table">
        <thead>
          <tr>
            <th>{{ $t('sop.arch.colBatch') }}</th>
            <th>{{ $t('sop.arch.colType') }}</th>
            <th>{{ $t('sop.arch.colStatus') }}</th>
            <th>{{ $t('sop.arch.colCreated') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="batch in batches" :key="batch.id">
            <td>{{ batch.batch_name }}</td>
            <td>{{ batch.plan_type }}</td>
            <td><span class="sop-tag">{{ batch.status }}</span></td>
            <td class="mono">{{ formatTime(batch.created_at) }}</td>
          </tr>
          <tr v-if="!batches.length"><td colspan="4" class="sop-empty">{{ $t('sop.arch.empty') }}</td></tr>
        </tbody>
      </table>
    </section>

    <SopDocPreview
      v-model:visible="docVisible"
      :artifact="docArtifact"
      :knowledge-allowed="false"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  listPmFeatures,
  listPmMaterials,
  listPmVersions,
  listPlanBatches,
  listPmProjects,
  type PmFeatureNode,
  type PmMaterial,
  type PmPlanBatch,
  type PmVersion,
} from '@/api/pm';
import { pmArchiveSummary, type PMArchiveSummary, type SopArtifact } from '@/api/sop';
import SopDocPreview from './components/SopDocPreview.vue';

const { t } = useI18n();
const router = useRouter();

const summary = ref<PMArchiveSummary>({ projects: 0, items: 0, runs: 0, attachments: 0 });
const versions = ref<PmVersion[]>([]);
const features = ref<PmFeatureNode[]>([]);
const materials = ref<PmMaterial[]>([]);
const batches = ref<PmPlanBatch[]>([]);
const activeTab = ref<'versions' | 'features' | 'materials' | 'batches'>('versions');
const refreshing = ref(false);
const docVisible = ref(false);
const docArtifact = ref<SopArtifact | null>(null);

const tabs = computed(() => [
  { key: 'versions' as const, label: t('sop.arch.tabVersions') },
  { key: 'features' as const, label: t('sop.arch.tabFeatures') },
  { key: 'materials' as const, label: t('sop.arch.tabMaterials') },
  { key: 'batches' as const, label: t('sop.arch.tabBatches') },
]);

const flatFeatures = computed(() => {
  const out: { id: string; name: string; depth: number; latest_version_no?: string }[] = [];
  const walk = (nodes: PmFeatureNode[], depth: number) => {
    for (const node of nodes) {
      // 功能清单的三段字段拼成一行（子系统可选）
      const name = [node.subsystem, node.module_name, node.feature_name, node.operation_name]
        .filter(Boolean)
        .join(' · ');
      out.push({ id: node.id, name, depth, latest_version_no: node.latest_version_no });
      if (node.children?.length) walk(node.children, depth + 1);
    }
  };
  walk(features.value, 0);
  return out;
});

function formatTime(value: string): string {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString();
}

async function refresh() {
  refreshing.value = true;
  try {
    summary.value = await pmArchiveSummary().catch(() => summary.value);
    // 归档按「迁移过来的 PM 项目」展示：优先选带迁移数据的项目，其次第一个
    const projects = await listPmProjects().catch(() => []);
    if (!projects.length) {
      versions.value = [];
      features.value = [];
      materials.value = [];
      batches.value = [];
      return;
    }
    const projectId = projects[0].id;
    const [v, f, m, b] = await Promise.all([
      listPmVersions(projectId).catch(() => []),
      listPmFeatures(projectId).catch(() => []),
      listPmMaterials(projectId).catch(() => []),
      listPlanBatches(projectId).catch(() => []),
    ]);
    versions.value = v;
    features.value = f as PmFeatureNode[];
    materials.value = m;
    batches.value = b;
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.arch.loadFailed'));
  } finally {
    refreshing.value = false;
  }
}

/**
 * 资料预览：PM 资料本质是知识库文档，没有运行产物文件端点。
 * 直接定位到该资料对应的知识库（带 knowledge 定位参数由知识库页处理）。
 */
function previewMaterial(material: PmMaterial) {
  void docVisible;
  void docArtifact;
  void router.push({
    name: 'knowledgeBaseDetail',
    params: { kbId: material.knowledge_id || '' },
  });
}

onMounted(refresh);
</script>

<style lang="less" scoped src="./sopArchive.less"></style>
