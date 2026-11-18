<template>
  <t-drawer :visible="visible" :header="detail ? $t('meeting.artifactDetail') : $t('meeting.artifacts')"
    size="46%" :footer="false" :z-index="2600" attach="body" @close="closeDrawer">
    <!-- 列表态：产物卡片 + 生成入口 -->
    <div v-if="!detail" class="drawer-body">
      <div class="drawer-toolbar">
        <t-button size="small" theme="primary" :disabled="isArchivedRoom" @click="showGenerate = true">
          <template #icon><t-icon name="add" /></template>
          {{ $t('meeting.generateArtifact') }}
        </t-button>
      </div>
      <t-loading :loading="loading" show-overlay>
        <div v-if="!loading && artifacts.length === 0" class="empty">
          {{ $t('meeting.artifactsEmptyNew') }}
        </div>
        <div v-else class="artifact-cards">
          <div v-for="a in artifacts" :key="a.id" class="artifact-card" @click="openDetail(a)">
            <div class="ac-main">
              <div class="ac-info">
                <div class="ac-title">
                  <t-tag size="small" variant="light" class="kind-tag">{{ kindLabel(a) }}</t-tag>
                  <span class="ac-name">{{ cardTitle(a) }}</span>
                </div>
                <div class="ac-meta">
                  <t-tag v-if="isSolution(a)" size="small" variant="light">v{{ a.version }}</t-tag>
                  <t-tag v-if="isCurrent(a)" size="small" theme="success" variant="light">
                    {{ $t('meeting.currentSolution') }}
                  </t-tag>
                  <t-tag size="small" :theme="statusTheme(a)" variant="light">{{ statusLabel(a) }}</t-tag>
                  <span class="ac-time">{{ formatArtifactDateTime(a.created_at) }}</span>
                </div>
                <div v-if="a.status === 'failed' && a.error" class="ac-error">{{ a.error }}</div>
              </div>
              <div v-if="a.status === 'generating'" class="ac-loading"><t-loading size="small" /></div>
            </div>
            <div class="ac-actions" @click.stop>
              <template v-if="isSolution(a) && a.status === 'ready'">
                <t-button v-if="!isCurrent(a)" size="small" theme="primary" variant="text"
                  @click="rollback(a)">{{ $t('meeting.rollbackTo') }}</t-button>
                <t-button size="small" theme="default" variant="text" @click="onExport(a)">
                  {{ $t('meeting.exportMd') }}
                </t-button>
              </template>
              <template v-else-if="a.status === 'ready'">
                <t-button v-if="a.file_name" size="small" theme="primary" variant="text"
                  @click="onDownload(a)">{{ $t('meeting.download') }}</t-button>
                <!-- 文本形态（Markdown 报告/纪要/待办，无文件）提供本地导出，历史产物也能落盘 -->
                <t-button v-else size="small" theme="primary" variant="text" @click="onExport(a)">
                  {{ $t('meeting.exportMd') }}
                </t-button>
                <t-button size="small" theme="default" variant="text" @click="onRegenerate(a)">
                  {{ $t('meeting.regenerate') }}
                </t-button>
              </template>
              <t-button v-else-if="a.status === 'failed'" size="small" theme="default" variant="text"
                @click="onRegenerate(a)">{{ $t('meeting.regenerate') }}</t-button>
            </div>
          </div>
        </div>
      </t-loading>
    </div>

    <!-- 详情态：正文/文件信息 + 动作 -->
    <div v-else class="drawer-body">
      <div class="detail-head">
        <t-button size="small" variant="text" @click="detail = null">
          <template #icon><t-icon name="arrow-left" /></template>
          {{ $t('meeting.backToList') }}
        </t-button>
      </div>
      <t-loading :loading="detailLoading" show-overlay>
        <div v-if="detail" class="detail-body">
          <div class="detail-meta">
            <span v-if="isSolution(detail)">v{{ detail.version }} · {{ $t('meeting.solution') }}</span>
            <span v-else>{{ kindLabel(detail) }}</span>
            <span class="ac-time">{{ formatArtifactDateTime(detail.created_at) }}</span>
          </div>
          <div v-if="detail.file_name" class="detail-file">
            <span class="file-name">{{ detail.file_name }}</span>
            <span class="file-size">{{ formatSize(detail.file_size) }}</span>
            <t-button v-if="detail.status === 'ready'" size="small" theme="primary" variant="text"
              @click="onDownload(detail)">{{ $t('meeting.download') }}</t-button>
          </div>
          <div v-if="detail.status === 'ready' && !detail.file_name" class="detail-file">
            <t-button size="small" theme="primary" variant="text" @click="onExport(detail)">
              {{ $t('meeting.exportMd') }}
            </t-button>
          </div>
          <div v-if="detail.status === 'failed' && detail.error" class="ac-error">{{ detail.error }}</div>
          <div class="detail-content markdown-content" v-html="renderContent(detail.content)" />
        </div>
      </t-loading>
    </div>

    <ArtifactGenerateDialog v-model:visible="showGenerate" @requested="showGenerate = false" />
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import {
  getArtifactDetail, exportArtifact, artifactDownloadUrl,
  type MeetingArtifact,
} from '@/api/meeting';
import { useMeetingStore } from '@/stores/meeting';
import ArtifactGenerateDialog from './components/ArtifactGenerateDialog.vue';
import { createChatMarkdownRenderer, renderChatMarkdown } from '@/utils/chatMarkdownRenderer';
import { formatArtifactDateTime } from '@/utils/sessionArtifacts';
import { safeMarkdownToHTML, sanitizeMarkdownHTML } from '@/utils/security';

const props = defineProps<{ visible: boolean; roomId: string; currentSolutionMessageId: string }>();
const emit = defineEmits<{ 'update:visible': [boolean] }>();

const { t } = useI18n();
const store = useMeetingStore();

const loading = ref(false);
const detailLoading = ref(false);
const detail = ref<MeetingArtifact | null>(null); // null = 列表态；非空 = 详情态
const showGenerate = ref(false);

// 列表数据统一由 store 持有（SSE 事件就地更新，抽屉重开不再丢生成中的卡片）
const artifacts = computed(() => store.artifacts);
const isArchivedRoom = computed(() => store.currentRoom?.status === 'archived');

// Markdown 渲染器与详情缓存
const markdownRenderer = createChatMarkdownRenderer();
const renderCache = new Map<string, { content: string; html: string }>();

watch(() => props.visible, (v) => {
  if (v) {
    detail.value = null;
    void refresh();
  }
});

async function refresh() {
  loading.value = true;
  try {
    await store.fetchArtifacts(props.roomId);
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
  } finally {
    loading.value = false;
  }
}

function closeDrawer() {
  emit('update:visible', false);
  detail.value = null;
}

// 是否方案类产物：历史 solution 行（kind）与并入产物通道后的 solution 形态（kind_type）同等对待
function isSolution(a: MeetingArtifact): boolean {
  return a.kind === 'solution' || a.kind_type === 'solution';
}

// 当前方案指针指向该产物的消息时视为“当前版本”
function isCurrent(a: MeetingArtifact): boolean {
  return Boolean(props.currentSolutionMessageId) && a.solution_message_id === props.currentSolutionMessageId;
}

// 卡片标题：方案类用「方案 vN」；其余用 title/file_name
function cardTitle(a: MeetingArtifact): string {
  if (isSolution(a)) return `${t('meeting.solution')} v${a.version}`;
  return a.title || a.file_name || kindLabel(a);
}

// 形态标签：方案 → 「方案」；其余按 9 形态文案
function kindLabel(a: MeetingArtifact): string {
  if (isSolution(a)) return t('meeting.kindSolution');
  switch (a.kind_type) {
    case 'report_markdown': return t('meeting.kindReportMarkdown');
    case 'minutes': return t('meeting.kindMinutes');
    case 'todo_list': return t('meeting.kindTodoList');
    case 'docx': return t('meeting.kindDocx');
    case 'xlsx': return t('meeting.kindXlsx');
    case 'pptx': return t('meeting.kindPptx');
    case 'html_report': return t('meeting.kindHtmlReport');
    case 'chart_image': return t('meeting.kindChartImage');
    default: return t('meeting.kindReportMarkdown');
  }
}

function statusTheme(a: MeetingArtifact): 'warning' | 'success' | 'danger' {
  if (a.status === 'generating') return 'warning';
  if (a.status === 'failed') return 'danger';
  return 'success';
}

function statusLabel(a: MeetingArtifact): string {
  if (a.status === 'generating') return t('meeting.artifactGenerating');
  if (a.status === 'failed') return t('meeting.artifactFailed');
  return t('meeting.artifactReady');
}

// 字节数 → 可读大小
function formatSize(bytes: number): string {
  if (!bytes) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function renderContent(content: string): string {
  const raw = content || '';
  if (!raw) return '';
  const key = `detail-${raw.length}`;
  const hit = renderCache.get(key);
  if (hit && hit.content === raw) return hit.html;
  const html = renderChatMarkdown(raw, {
    renderer: markdownRenderer,
    escapeMarkdown: safeMarkdownToHTML,
    sanitizeHtml: sanitizeMarkdownHTML,
    streaming: false,
  });
  renderCache.set(key, { content: raw, html });
  return html;
}

async function openDetail(a: MeetingArtifact) {
  detailLoading.value = true;
  try {
    detail.value = await getArtifactDetail(props.roomId, a.id);
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
  } finally {
    detailLoading.value = false;
  }
}

// 回滚 solution 版本：确认后走 store（HTTP 成功即重拉房间与消息），再刷新抽屉列表
function rollback(a: MeetingArtifact) {
  const confirm = DialogPlugin.confirm({
    header: t('meeting.rollback'),
    body: t('meeting.rollbackConfirm', { v: a.version }),
    confirmBtn: { content: t('meeting.rollbackTo'), theme: 'danger' },
    cancelBtn: { content: t('meeting.cancel') },
    onConfirm: async () => {
      try {
        await store.rollback(a.version);
        confirm.destroy();
        MessagePlugin.success(t('meeting.rollbackDone'));
        await refresh();
      } catch (e) {
        confirm.destroy();
        MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
      }
    },
  });
}

// 导出为本地 .md：solution 取方案正文，generated 取生成时的 agent 回答（后端按产物类型分流）
async function onExport(a: MeetingArtifact) {
  try {
    const blob = await exportArtifact(props.roomId, a.id);
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = isSolution(a) ? `meeting-solution-v${a.version}.md` : `${cardTitle(a)}.md`;
    link.click();
    URL.revokeObjectURL(url);
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
  }
}

// 下载 generated 产物：服务端 302 至会话下载端点，getDown 跟随重定向（保留鉴权头）
async function onDownload(a: MeetingArtifact) {
  try {
    const { getDown } = await import('../../utils/request');
    const blob = await getDown(artifactDownloadUrl(props.roomId, a.id));
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = a.file_name || 'artifact';
    link.click();
    URL.revokeObjectURL(url);
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
  }
}

// 重新生成：确认后走 store（原行重置为 generating，SSE 推终态）
function onRegenerate(a: MeetingArtifact) {
  const confirm = DialogPlugin.confirm({
    header: t('meeting.regenerate'),
    body: t('meeting.regenerateConfirm'),
    confirmBtn: { content: t('meeting.regenerate'), theme: 'primary' },
    cancelBtn: { content: t('meeting.cancel') },
    onConfirm: async () => {
      try {
        await store.regenerateArtifact(a.id);
        confirm.destroy();
        detail.value = null; // 回列表态看 loading 卡片
      } catch (e) {
        confirm.destroy();
        const msg = (e as Error)?.message || '';
        if (msg.includes('409') || msg.includes('正在生成') || msg.includes('Conflict')) {
          MessagePlugin.warning(t('meeting.artifactBusy'));
        } else {
          MessagePlugin.error(msg || t('meeting.loadFailed'));
        }
      }
    },
  });
}
</script>

<style lang="less" scoped>
.drawer-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: var(--app-space-2, 8px);
}

.drawer-body {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.empty {
  padding: 40px 0;
  text-align: center;
  color: var(--td-text-color-placeholder, #999);
}

.artifact-cards {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2, 8px);
}

.artifact-card {
  padding: var(--app-space-3, 12px);
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 6px;
  cursor: pointer;
  transition: border-color 0.15s ease;

  &:hover {
    border-color: var(--td-brand-color);
  }
}

.ac-main {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-2, 8px);
}

.ac-info {
  flex: 1;
  min-width: 0;
}

.ac-title {
  display: flex;
  align-items: center;
  gap: var(--app-space-1, 4px);
  min-width: 0;

  .ac-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: 500;
  }
}

.ac-meta {
  display: flex;
  align-items: center;
  gap: var(--app-space-1, 4px);
  flex-wrap: wrap;
  margin-top: 4px;
}

.ac-time {
  font-size: 12px;
  color: var(--td-text-color-placeholder, #999);
}

.ac-error {
  margin-top: 4px;
  font-size: 12px;
  color: var(--td-danger-color);
  word-break: break-word;
}

.ac-loading {
  flex-shrink: 0;
}

.ac-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0;
  margin-top: 4px;
}

.detail-head {
  margin-bottom: var(--app-space-2, 8px);
}

.detail-meta {
  display: flex;
  align-items: center;
  gap: var(--app-space-2, 8px);
  margin-bottom: var(--app-space-2, 8px);
  font-size: 13px;
  color: var(--td-text-color-secondary, #666);
}

.detail-file {
  display: flex;
  align-items: center;
  gap: var(--app-space-2, 8px);
  padding: var(--app-space-2, 8px) var(--app-space-3, 12px);
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 6px;
  margin-bottom: var(--app-space-3, 12px);
  font-size: 13px;

  .file-name {
    font-weight: 500;
    word-break: break-all;
  }

  .file-size {
    color: var(--td-text-color-placeholder, #999);
  }
}

.detail-content {
  font-size: 13px;
  line-height: 1.6;
  word-break: break-word;
}
</style>
