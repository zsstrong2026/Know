<template>
  <div class="sop-tpls">
    <header class="sop-tpls-head">
      <div>
        <h2 class="sop-tpls-title">{{ $t('sop.tpl.title') }}</h2>
        <p class="sop-tpls-desc">{{ $t('sop.tpl.desc') }}</p>
      </div>
      <t-button theme="primary" :loading="creating" @click="createNew">
        ＋ {{ $t('sop.tpl.create') }}
      </t-button>
    </header>

    <div v-if="!loading && !templates.length" class="sop-tpls-empty">
      {{ $t('sop.tpl.empty') }}
    </div>

    <div class="sop-tpls-grid">
      <article v-for="tpl in templates" :key="tpl.id" class="sop-tpl-card">
        <div class="sop-tpl-card-head">
          <h3>{{ tpl.name }}</h3>
          <span class="sop-tag" :class="scopeClass(tpl)">{{ scopeLabel(tpl) }}</span>
        </div>
        <p class="sop-tpl-card-desc">{{ tpl.description || '—' }}</p>
        <div class="sop-tpl-meta">
          <span>{{ $t('sop.tpl.nodes', { n: tpl.definition?.nodes?.length || 0 }) }}</span>
          <span class="sop-tag" :class="tpl.status === 'published' ? 'sop-tag--brand' : 'sop-tag--gray'">
            {{ statusLabel(tpl.status) }}
          </span>
          <span v-if="tpl.updated_at">{{ formatTime(tpl.updated_at) }}</span>
        </div>
        <div class="sop-tpl-actions">
          <t-button size="small" variant="outline" @click="openDesigner(tpl)">
            {{ tpl.is_builtin ? $t('sop.tpl.view') : $t('sop.tpl.design') }}
          </t-button>
          <t-button
            v-if="!tpl.is_builtin"
            size="small"
            theme="primary"
            :loading="publishing === tpl.id"
            @click="publish(tpl)"
          >
            {{ $t('sop.tpl.publish') }}
          </t-button>
          <t-button v-if="tpl.is_builtin" size="small" theme="primary" variant="outline" @click="copyBuiltin(tpl)">
            {{ $t('sop.tpl.copy') }}
          </t-button>
          <t-button size="small" variant="text" @click="openRevisions(tpl)">
            {{ $t('sop.tpl.revisions') }}
          </t-button>
          <t-button v-if="!tpl.is_builtin" size="small" variant="text" theme="danger" @click="askDelete(tpl)">
            {{ $t('common.delete') }}
          </t-button>
        </div>
      </article>
    </div>

    <!-- 版本历史 -->
    <t-drawer
      :visible="revisionsOpen"
      size="480px"
      placement="right"
      :header="$t('sop.tpl.revisionsTitle', { name: revisionTemplateName })"
      :footer="false"
      @close="revisionsOpen = false"
    >
      <div v-if="!revisions.length" class="sop-tpls-empty">{{ $t('sop.tpl.noRevisions') }}</div>
      <ul v-else class="sop-rev-list">
        <li v-for="rev in revisions" :key="rev.id" class="sop-rev-item">
          <div class="sop-rev-head">
            <b>v{{ rev.revision }}</b>
            <span class="sop-muted">{{ formatTime(rev.created_at) }}</span>
          </div>
          <div class="sop-rev-meta">
            {{ $t('sop.tpl.nodes', { n: rev.definition?.nodes?.length || 0 }) }}
          </div>
        </li>
      </ul>
    </t-drawer>

    <!-- 发布校验失败：逐条白话列出（后端 ValidateDefinition 的原因原文） -->
    <t-dialog
      :visible="errorsOpen"
      width="560px"
      placement="center"
      :header="$t('sop.tpl.publishBlocked')"
      :confirm-btn="$t('common.close')"
      :cancel-btn="null"
      @close="errorsOpen = false"
    >
      <p class="sop-hint">{{ $t('sop.tpl.publishBlockedHint') }}</p>
      <ul class="sop-err-list">
        <li v-for="(reason, index) in publishErrors" :key="index">{{ reason }}</li>
      </ul>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  copyTemplate,
  createTemplate,
  deleteTemplate,
  listRevisions,
  listTemplates,
  publishTemplate,
  type SopTemplate,
  type SopTemplateRevision,
} from '@/api/sop';
import { useSopStore } from '@/stores/sop';
import { useConfirmDelete } from '@/components/settings/useConfirmDelete';

const router = useRouter();
const { t } = useI18n();
const confirmDanger = useConfirmDelete();
const store = useSopStore();
const { currentProjectId } = storeToRefs(store);

const templates = ref<SopTemplate[]>([]);
const loading = ref(false);
const creating = ref(false);
const publishing = ref('');
const revisionsOpen = ref(false);
const revisions = ref<SopTemplateRevision[]>([]);
const revisionTemplateName = ref('');
const errorsOpen = ref(false);
const publishErrors = ref<string[]>([]);

function scopeLabel(tpl: SopTemplate): string {
  if (tpl.is_builtin) return t('sop.tpl.scopeBuiltin');
  return tpl.project_id ? t('sop.tpl.scopeProject') : t('sop.tpl.scopeGlobal');
}

function scopeClass(tpl: SopTemplate): string {
  if (tpl.is_builtin) return 'sop-tag--gray';
  return tpl.project_id ? 'sop-tag--brand' : 'sop-tag--gray';
}

function statusLabel(status: string): string {
  return status === 'published' ? t('sop.tpl.statusPublished') : t('sop.tpl.statusDraft');
}

function formatTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

async function load() {
  loading.value = true;
  try {
    templates.value = await listTemplates(currentProjectId.value || undefined);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.tpl.loadFailed'));
  } finally {
    loading.value = false;
  }
}

function openDesigner(tpl: SopTemplate) {
  void router.push({ name: 'sopDesigner', params: { id: tpl.id } });
}

async function createNew() {
  creating.value = true;
  try {
    const tpl = await createTemplate({
      project_id: currentProjectId.value || undefined,
      name: t('sop.tpl.newName'),
      description: '',
      definition: { nodes: [], edges: [], input_schema: [], on_success_item_status: '' },
    });
    await load();
    openDesigner(tpl);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.tpl.createFailed'));
  } finally {
    creating.value = false;
  }
}

/**
 * 发布：后端没有独立 validate 端点，Publish 会先跑同一套校验，
 * 失败时按行返回白话原因——这里直接把这些原因原样列出来。
 */
async function publish(tpl: SopTemplate) {
  publishing.value = tpl.id;
  publishErrors.value = [];
  try {
    await publishTemplate(tpl.id);
    MessagePlugin.success(t('sop.tpl.published', { name: tpl.name }));
    await load();
  } catch (error: any) {
    const message = String(error?.message || '');
    const details = error?.details;
    const list = Array.isArray(details) ? details.map((d: any) => String(d?.message || d)) : [];
    publishErrors.value = list.length ? list : [message || t('sop.tpl.publishFailed')];
    errorsOpen.value = true;
  } finally {
    publishing.value = '';
  }
}

async function copyBuiltin(tpl: SopTemplate) {
  try {
    const copy = await copyTemplate(tpl.id, t('sop.tpl.copyName', { name: tpl.name }));
    await load();
    MessagePlugin.success(t('sop.tpl.copied', { name: copy.name }));
    openDesigner(copy);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.tpl.copyFailed'));
  }
}

async function openRevisions(tpl: SopTemplate) {
  revisionTemplateName.value = tpl.name;
  revisionsOpen.value = true;
  try {
    revisions.value = await listRevisions(tpl.id);
  } catch {
    revisions.value = [];
  }
}

function askDelete(tpl: SopTemplate) {
  confirmDanger({
    title: t('sop.tpl.deleteTitle'),
    body: t('sop.tpl.deleteBody', { name: tpl.name }),
    confirmText: t('common.delete'),
    onConfirm: async () => {
      try {
        await deleteTemplate(tpl.id);
        MessagePlugin.success(t('sop.tpl.deleted'));
        await load();
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('sop.tpl.deleteFailed'));
      }
    },
  });
}

watch(() => currentProjectId.value, () => void load());
onMounted(load);
</script>

<style lang="less" scoped src="./sopTemplateList.less"></style>
