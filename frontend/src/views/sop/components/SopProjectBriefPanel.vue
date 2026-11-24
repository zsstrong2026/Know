<template>
  <!-- 项目黑板与简报：左侧黑板时间线（唯一写入源），右侧简报投影（只读） -->
  <t-dialog
    :visible="visible"
    width="1080px"
    placement="center"
    :header="`${$t('sop.brief.panelTitle')} · ${projectName}`"
    :footer="false"
    :on-close="close"
    @close="close"
  >
    <div class="sop-brief">
      <section class="sop-brief-left">
        <header class="sop-brief-head">
          <span class="t">{{ $t('sop.brief.panelSub') }}</span>
          <span class="n">{{ $t('sop.notes.count', { n: notes.length }) }}</span>
        </header>

        <div class="sop-brief-chips">
          <button
            v-for="chip in kindChips"
            :key="chip.value"
            type="button"
            class="sop-chip"
            :class="{ on: kindFilter === chip.value }"
            @click="kindFilter = chip.value"
          >
            {{ chip.label }}
          </button>
        </div>

        <div class="sop-brief-list">
          <div v-if="!visibleNotes.length" class="sop-brief-empty">{{ $t('sop.brief.empty') }}</div>
          <article v-for="note in visibleNotes" :key="note.id" class="sop-note">
            <div class="nh">
              <span v-if="note.pinned" class="pin" :title="$t('sop.brief.pinned')">📌</span>
              <span class="sop-tag" :class="kindTagClass(note.kind)">{{ noteKindLabel(note.kind) }}</span>
              <span class="nt">{{ note.title || firstLine(note.body) }}</span>
              <span class="nact">
                <t-button size="small" variant="text" @click="openEdit(note)">{{ $t('sop.brief.edit') }}</t-button>
                <t-button size="small" variant="text" :loading="pinningId === note.id" @click="togglePin(note)">
                  {{ note.pinned ? $t('sop.brief.unpin') : $t('sop.brief.pin') }}
                </t-button>
                <t-button size="small" variant="text" theme="danger" @click="askDelete(note)">
                  {{ $t('sop.brief.delete') }}
                </t-button>
              </span>
            </div>
            <div class="nb">{{ note.body }}</div>
            <div class="nm">
              <span class="sop-tag sop-tag--gray sop-tag--sm">{{ noteSourceLabel(note.source) }}</span>
              <span>{{ formatAt(note.created_at) }}</span>
              <span v-if="note.item_id">· {{ $t('sop.notes.itemLinked') }}</span>
              <span v-if="note.run_id">· {{ $t('sop.notes.runLinked') }}</span>
              <span>· seq {{ note.seq }}</span>
            </div>
          </article>
        </div>

        <div class="sop-note-form">
          <div class="row">
            <t-select v-model="draft.kind" class="sop-kind-select" size="small" :options="kindOptions" />
            <t-button
              size="small"
              variant="outline"
              :loading="drafting"
              :disabled="!projectId"
              @click="generateDraft"
            >
              ✦ {{ $t('sop.brief.draft') }}
            </t-button>
          </div>
          <t-input v-model="draft.title" size="small" :placeholder="$t('sop.brief.composeTitle')" :maxlength="200" />
          <t-textarea
            v-model="draft.body"
            :autosize="{ minRows: 2, maxRows: 5 }"
            :placeholder="$t('sop.brief.composePlaceholder')"
            :maxlength="2000"
          />
          <div class="row row--end">
            <t-button size="small" theme="primary" :loading="saving" @click="submitNote">
              {{ $t('sop.brief.composeSubmit') }}
            </t-button>
          </div>
        </div>
      </section>

      <section class="sop-brief-right">
        <div class="sop-brief-meta">
          <span class="doc">📄 {{ projectName }} · {{ $t('sop.brief.preview') }}</span>
          <span v-if="!kbBound" class="sop-tag sop-tag--gray">{{ $t('sop.brief.unbound') }}</span>
          <span v-else-if="stale" class="sop-tag sop-tag--brand">
            {{ $t('sop.brief.stale', { n: notesMaxSeq - renderedSeq }) }}
          </span>
          <span v-else class="sop-tag sop-tag--brand">{{ $t('sop.brief.synced') }}</span>
        </div>

        <div v-if="!kbBound" class="sop-brief-hint">{{ $t('sop.brief.unboundHint') }}</div>

        <div class="sop-brief-doc">
          <PmMarkdown v-if="markdown.trim()" :content="markdown" />
          <t-empty v-else :description="$t('sop.brief.previewEmpty')" />
        </div>

        <footer class="sop-brief-actions">
          <span class="tip">{{ lastRenderedText }}</span>
          <t-button size="small" variant="outline" :loading="rendering" @click="renderNow">
            {{ $t('sop.brief.renderNow') }}
          </t-button>
          <t-button size="small" :disabled="!kbBound" @click="openKB">{{ $t('sop.brief.openKB') }}</t-button>
        </footer>
      </section>
    </div>

    <t-dialog
      v-model:visible="edit.visible"
      :header="$t('sop.brief.editTitle')"
      width="520px"
      placement="center"
      :confirm-btn="{ content: $t('sop.brief.editSaved'), loading: edit.submitting }"
      :cancel-btn="$t('common.cancel')"
      :on-confirm="submitEdit"
    >
      <t-form label-align="top">
        <t-form-item :label="$t('sop.brief.composeKind')">
          <t-select v-model="edit.kind" :options="kindOptions" />
        </t-form-item>
        <t-form-item :label="$t('sop.brief.composeTitle')">
          <t-input v-model="edit.title" :maxlength="200" />
        </t-form-item>
        <t-form-item :label="$t('sop.brief.composePlaceholder')">
          <t-textarea v-model="edit.body" :autosize="{ minRows: 3, maxRows: 8 }" :maxlength="2000" />
        </t-form-item>
      </t-form>
    </t-dialog>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import PmMarkdown from '@/views/pm/components/PmMarkdown.vue';
import {
  createNote,
  decisionDraft,
  deleteNote,
  getBrief,
  getProject,
  listNotes,
  renderBrief,
  updateNote,
  type SopNoteInput,
  type SopNoteKind,
  type SopProject,
  type SopProjectNote,
} from '@/api/sop';
import { briefSeqStale, noteKindLabel, noteSourceLabel } from '@/api/sop/helpers';
import { useConfirmDelete } from '@/components/settings/useConfirmDelete';

const props = defineProps<{ visible: boolean; projectId: string }>();
const emit = defineEmits<{ close: []; changed: [] }>();

const { t } = useI18n();
const router = useRouter();
const confirmDanger = useConfirmDelete();

const project = ref<SopProject | null>(null);
const notes = ref<SopProjectNote[]>([]);
const markdown = ref('');
const renderedSeq = ref(0);
const notesMaxSeq = ref(0);
const kbBound = ref(false);
const lastRenderedAt = ref('');
const loading = ref(false);
const saving = ref(false);
const rendering = ref(false);
const drafting = ref(false);
const pinningId = ref('');
const kindFilter = ref<'' | SopNoteKind>('');

const draft = reactive({ kind: 'decision' as SopNoteKind, title: '', body: '' });
const edit = reactive({
  visible: false,
  submitting: false,
  id: '',
  kind: 'note' as SopNoteKind,
  title: '',
  body: '',
});

const projectName = computed(() => project.value?.name || '');
const stale = computed(() => briefSeqStale(renderedSeq.value, notesMaxSeq.value));
const lastRenderedText = computed(() =>
  lastRenderedAt.value
    ? t('sop.brief.lastRendered', { time: lastRenderedAt.value })
    : t('sop.brief.neverRendered'),
);

const kindOptions = computed(() =>
  (['decision', 'run_summary', 'risk', 'release', 'note'] as SopNoteKind[]).map((kind) => ({
    label: noteKindLabel(kind),
    value: kind,
  })),
);

const kindChips = computed(() => [
  { label: t('sop.brief.allKinds'), value: '' as const },
  ...kindOptions.value.map((opt) => ({ label: opt.label, value: opt.value })),
]);

const visibleNotes = computed(() => {
  const list = kindFilter.value ? notes.value.filter((n) => n.kind === kindFilter.value) : notes.value.slice();
  // 置顶在前、其余按 seq 倒序（后端已排好，这里兜住本地新增的行）
  return list.sort((a, b) => Number(b.pinned) - Number(a.pinned) || b.seq - a.seq);
});

function close() {
  emit('close');
}

function firstLine(text: string): string {
  const s = (text || '').trim();
  const i = s.indexOf('\n');
  return i >= 0 ? s.slice(0, i) : s;
}

function formatAt(raw: string): string {
  if (!raw) return '';
  const d = new Date(raw);
  if (Number.isNaN(d.getTime())) return raw;
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function kindTagClass(kind: string): string {
  switch (kind) {
    case 'decision':
      return 'sop-tag--brand';
    case 'risk':
      return 'sop-tag--warn';
    case 'release':
      return 'sop-tag--purple';
    default:
      return 'sop-tag--gray';
  }
}

async function load() {
  if (!props.projectId) return;
  loading.value = true;
  try {
    const [detail, list, brief] = await Promise.all([
      getProject(props.projectId),
      listNotes(props.projectId).catch(() => []),
      getBrief(props.projectId).catch(() => null),
    ]);
    project.value = detail;
    notes.value = list;
    if (brief) {
      markdown.value = brief.markdown || '';
      renderedSeq.value = brief.rendered_seq || 0;
      notesMaxSeq.value = brief.notes_max_seq || 0;
      kbBound.value = !!brief.kb_bound;
      lastRenderedAt.value = brief.last_rendered_at || '';
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.brief.renderFailed'));
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.visible, props.projectId],
  ([open]) => {
    if (!open) return;
    kindFilter.value = '';
    draft.kind = 'decision';
    draft.title = '';
    draft.body = '';
    void load();
  },
);

async function submitNote() {
  if (!props.projectId) return;
  if (!draft.title.trim() && !draft.body.trim()) {
    MessagePlugin.warning(t('sop.brief.composeRequired'));
    return;
  }
  saving.value = true;
  try {
    const input: SopNoteInput = {
      kind: draft.kind,
      title: draft.title.trim(),
      body: draft.body.trim(),
    };
    await createNote(props.projectId, input);
    MessagePlugin.success(t('sop.brief.composeSaved'));
    draft.title = '';
    draft.body = '';
    await load();
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.brief.composeFailed'));
  } finally {
    saving.value = false;
  }
}

function openEdit(note: SopProjectNote) {
  edit.id = note.id;
  edit.kind = note.kind;
  edit.title = note.title;
  edit.body = note.body;
  edit.visible = true;
  edit.submitting = false;
}

async function submitEdit() {
  if (!edit.id) return;
  edit.submitting = true;
  try {
    await updateNote(edit.id, { kind: edit.kind, title: edit.title.trim(), body: edit.body.trim() });
    MessagePlugin.success(t('sop.brief.editSaved'));
    edit.visible = false;
    await load();
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.brief.composeFailed'));
  } finally {
    edit.submitting = false;
  }
}

async function togglePin(note: SopProjectNote) {
  pinningId.value = note.id;
  try {
    await updateNote(note.id, { pinned: !note.pinned });
    await load();
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.brief.pinFailed'));
  } finally {
    pinningId.value = '';
  }
}

function askDelete(note: SopProjectNote) {
  confirmDanger({
    title: t('sop.brief.deleteConfirm'),
    body: t('sop.brief.deleteConfirmBody'),
    confirmText: t('sop.brief.delete'),
    onConfirm: async () => {
      try {
        await deleteNote(note.id);
        MessagePlugin.success(t('sop.brief.deleted'));
        await load();
        emit('changed');
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('sop.brief.deleteFailed'));
      }
    },
  });
}

/** 生成决策草稿：只填进表单，人确认后才写入黑板。 */
async function generateDraft() {
  if (!props.projectId) return;
  drafting.value = true;
  try {
    const text = await decisionDraft(props.projectId);
    if (!draft.title.trim()) hintDraftTitle(text);
    draft.body = text;
    draft.kind = 'decision';
    MessagePlugin.success(t('sop.brief.draftInserted'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.brief.draftFailed'));
  } finally {
    drafting.value = false;
  }
}

/** 草稿首行「决策：xxx」直接变成标题，正文保留全文（人可改）。 */
function hintDraftTitle(text: string) {
  const first = firstLine(text);
  const m = first.match(/^决策[：:]\s*(.+)$/);
  draft.title = (m ? m[1] : first).slice(0, 200);
}

async function renderNow() {
  if (!props.projectId) return;
  if (!kbBound.value) {
    MessagePlugin.warning(t('sop.brief.noKB'));
    return;
  }
  rendering.value = true;
  try {
    const status = await renderBrief(props.projectId);
    markdown.value = status.markdown || '';
    renderedSeq.value = status.rendered_seq || 0;
    notesMaxSeq.value = status.notes_max_seq || 0;
    kbBound.value = !!status.kb_bound;
    lastRenderedAt.value = status.last_rendered_at || '';
    MessagePlugin.success(t('sop.brief.rendered'));
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.brief.renderFailed'));
  } finally {
    rendering.value = false;
  }
}

function openKB() {
  if (!kbBound.value || !project.value?.kb_id) return;
  close();
  void router.push({ name: 'knowledgeBaseDetail', params: { kbId: project.value.kb_id } });
}
</script>

<style lang="less" scoped src="./sopProjectBriefPanel.less"></style>
