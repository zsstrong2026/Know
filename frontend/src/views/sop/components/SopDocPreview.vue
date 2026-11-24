<template>
  <t-dialog
    :visible="visible"
    width="min(1120px, 94vw)"
    placement="center"
    :header="null"
    :footer="false"
    :close-on-overlay-click="true"
    class="sop-doc-preview-dialog"
    @close="close"
  >
    <div class="sop-doc">
      <header class="sop-doc-head">
        <div v-if="artifact" class="sop-doc-title">
          <b>{{ artifact.name || basename }}</b>
          <span class="mono">{{ artifact.path }}</span>
          <span class="sop-muted">{{ formatBytes(artifact.size) }} · {{ shortSha(artifact.sha256) }}</span>
          <span v-if="ingested" class="sop-tag sop-tag--brand">{{ $t('sop.doc.ingested') }}</span>
        </div>
        <div class="sop-doc-actions">
          <div v-if="switchable" class="sop-seg">
            <button type="button" :class="{ on: view === 'preview' }" @click="view = 'preview'">
              {{ $t('sop.doc.preview') }}
            </button>
            <button type="button" :class="{ on: view === 'source' }" @click="view = 'source'">
              {{ $t('sop.doc.source') }}
            </button>
          </div>
          <t-button size="small" variant="outline" :loading="loading" @click="copySource">
            {{ $t('sop.doc.copy') }}
          </t-button>
          <t-button size="small" variant="outline" :loading="loading" @click="download">
            {{ $t('common.download') }}
          </t-button>
          <t-tooltip :content="ingestTooltip" :disabled="!ingestTooltip">
            <t-button
              size="small"
              theme="primary"
              :disabled="!!ingested || !knowledgeAllowed"
              :loading="ingesting"
              @click="ingest"
            >
              {{ ingested ? $t('sop.doc.ingestedDone') : $t('sop.doc.ingest') }}
            </t-button>
          </t-tooltip>
          <button type="button" class="sop-doc-close" :aria-label="$t('common.close')" @click="close">×</button>
        </div>
      </header>

      <div ref="bodyEl" class="sop-doc-body">
        <div v-if="loading" class="sop-doc-loading">{{ $t('common.loading') }}</div>
        <div v-else-if="error" class="sop-doc-error">{{ error }}</div>

        <template v-else-if="kind === 'binary'">
          <div class="sop-doc-binary">
            <p>{{ $t('sop.doc.binary') }}</p>
            <t-button theme="primary" variant="outline" @click="download">{{ $t('common.download') }}</t-button>
          </div>
        </template>

        <template v-else-if="view === 'source'">
          <pre class="sop-doc-source" :class="{ 'is-selected': copiedFallback }">{{ sourceText }}</pre>
        </template>

        <template v-else-if="kind === 'markdown'">
          <article class="sop-doc-markdown">
            <PmMarkdown :content="markdownBody" />
          </article>
        </template>

        <template v-else-if="kind === 'json'">
          <pre class="sop-doc-source">{{ prettyJson }}</pre>
        </template>

        <template v-else-if="kind === 'diff'">
          <pre class="sop-doc-source sop-doc-diff"><code
            v-for="(line, index) in diffLines"
            :key="index"
            :class="diffLineClass(line)"
          >{{ line }}
</code></pre>
        </template>

        <template v-else-if="kind === 'html'">
          <iframe
            class="sop-doc-iframe"
            sandbox=""
            :src="htmlBlobUrl"
            :title="artifact?.name || basename"
          />
        </template>

        <template v-else>
          <pre class="sop-doc-source">{{ sourceText }}</pre>
        </template>
      </div>

      <footer v-if="copiedFallback" class="sop-doc-hint">{{ $t('sop.doc.copyFallback') }}</footer>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  artifactFileURL,
  ingestArtifact,
  type SopArtifact,
} from '@/api/sop';
import { diffLineClass, formatBytes, previewKind, shortSha, stripFrontmatter } from '@/api/sop/helpers';
import { buildAuthHeaders } from '@/utils/request';
import { getApiBaseUrl } from '@/utils/api-base';
import PmMarkdown from '@/views/pm/components/PmMarkdown.vue';

const props = defineProps<{
  visible: boolean;
  artifact: SopArtifact | null;
  /** 归档里没有运行 id 的资料：传空则禁用入库按钮。 */
  runId?: string;
  knowledgeAllowed?: boolean;
}>();

const emit = defineEmits<{ 'update:visible': [boolean]; ingested: [SopArtifact] }>();

const { t } = useI18n();

const view = ref<'preview' | 'source'>('preview');
const loading = ref(false);
const ingesting = ref(false);
const error = ref('');
const sourceText = ref('');
const copiedFallback = ref(false);
const htmlBlobUrl = ref('');
const bodyEl = ref<HTMLElement | null>(null);

const kind = computed(() => previewKind(props.artifact?.path || ''));

const switchable = computed(() => kind.value !== 'binary');

const basename = computed(() => {
  const path = props.artifact?.path || '';
  return path.slice(path.lastIndexOf('/') + 1) || path;
});

const ingested = computed(() => !!props.artifact?.knowledge_id);

const knowledgeAllowed = computed(() => props.knowledgeAllowed !== false && !!props.runId);

const ingestTooltip = computed(() => {
  if (!props.runId) return t('sop.doc.ingestNoRun');
  if (props.knowledgeAllowed === false) return t('sop.doc.ingestNoKb');
  return '';
});

const markdownBody = computed(() => stripFrontmatter(sourceText.value));

const prettyJson = computed(() => {
  try {
    return JSON.stringify(JSON.parse(sourceText.value || '{}'), null, 2);
  } catch {
    return sourceText.value;
  }
});

const diffLines = computed(() => (sourceText.value || '').split('\n'));

function authUrl(url: string): string {
  const base = getApiBaseUrl();
  return url.startsWith('/') ? `${base}${url}` : url;
}

/** 产物文件必须带鉴权头：用 fetch 取 blob，再按类型分派渲染。 */
async function loadFile() {
  const artifact = props.artifact;
  if (!artifact) return;
  loading.value = true;
  error.value = '';
  sourceText.value = '';
  copiedFallback.value = false;
  if (htmlBlobUrl.value) {
    URL.revokeObjectURL(htmlBlobUrl.value);
    htmlBlobUrl.value = '';
  }
  try {
    const resp = await fetch(authUrl(artifactFileURL(artifact.run_id, artifact.id)), {
      headers: buildAuthHeaders(),
    });
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    if (kind.value === 'binary') {
      // 二进制不需要读正文，下载按钮走单独路径
      return;
    }
    const blob = await resp.blob();
    if (kind.value === 'html') {
      htmlBlobUrl.value = URL.createObjectURL(blob);
      return;
    }
    sourceText.value = await blob.text();
  } catch (err: any) {
    error.value = err?.message || t('sop.doc.loadFailed');
  } finally {
    loading.value = false;
  }
}

async function copySource() {
  const text = sourceText.value;
  try {
    await navigator.clipboard.writeText(text);
    MessagePlugin.success(t('common.copied'));
    return;
  } catch {
    // 剪贴板被禁用：切源码视图并全选，提示用户手动复制
    view.value = 'source';
    copiedFallback.value = true;
    await Promise.resolve();
    const pre = bodyEl.value?.querySelector('.sop-doc-source');
    if (pre) {
      const range = document.createRange();
      range.selectNodeContents(pre);
      const selection = window.getSelection();
      selection?.removeAllRanges();
      selection?.addRange(range);
    }
    MessagePlugin.warning(t('sop.doc.copyFallback'));
  }
}

async function download() {
  const artifact = props.artifact;
  if (!artifact) return;
  try {
    const resp = await fetch(authUrl(artifactFileURL(artifact.run_id, artifact.id, true)), {
      headers: buildAuthHeaders(),
    });
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    const blob = await resp.blob();
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = basename.value;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('sop.doc.downloadFailed'));
  }
}

/** 入库对象永远是 .md 源文档：成功后按钮置灰并回写 knowledge_id。 */
async function ingest() {
  const artifact = props.artifact;
  if (!artifact || !props.runId) return;
  ingesting.value = true;
  try {
    const updated = await ingestArtifact(props.runId, artifact.id);
    emit('ingested', updated);
    MessagePlugin.success(t('sop.doc.ingestDone'));
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('sop.doc.ingestFailed'));
  } finally {
    ingesting.value = false;
  }
}

function close() {
  emit('update:visible', false);
}

watch(
  () => [props.visible, props.artifact?.id],
  ([visible]) => {
    if (!visible) return;
    view.value = 'preview';
    if (props.artifact) void loadFile();
  },
);

onBeforeUnmount(() => {
  if (htmlBlobUrl.value) URL.revokeObjectURL(htmlBlobUrl.value);
});
</script>

<style lang="less" scoped src="./sopDocPreview.less"></style>
