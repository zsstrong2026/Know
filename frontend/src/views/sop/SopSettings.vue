<template>
  <div class="sop-cli">
    <header class="sop-cli-head">
      <div>
        <h2 class="sop-cli-title">{{ $t('sop.cli.title') }}</h2>
        <p class="sop-cli-desc">{{ $t('sop.cli.desc') }}</p>
      </div>
      <t-button size="small" variant="outline" :loading="refreshing" @click="refresh">⟳</t-button>
    </header>

    <div class="sop-notice">{{ $t('sop.cli.securityNotice') }}</div>

    <div class="sop-cli-grid">
      <article v-for="item in cards" :key="item.kind" class="sop-cli-card">
        <div class="sop-cli-card-head">
          <b>{{ item.label }}</b>
          <span class="sop-badge" :class="item.installed ? 'is-ok' : 'is-off'">
            {{ item.installed ? $t('sop.cli.installed', { version: item.version || '—' }) : $t('sop.cli.notInstalled') }}
          </span>
        </div>
        <div class="sop-cli-binary mono">{{ item.binary }}</div>

        <div class="sop-cli-cred">
          <span class="sop-tag" :class="item.credential ? 'sop-tag--brand' : 'sop-tag--gray'">
            {{ item.credential
              ? $t('sop.cli.credConfigured', { tail: item.credential.api_key_tail })
              : $t('sop.cli.credMissing') }}
          </span>
          <t-button size="small" variant="text" @click="openCredential(item)">
            {{ item.credential ? $t('common.edit') : $t('sop.cli.setCredential') }}
          </t-button>
          <t-button
            v-if="item.credential"
            size="small"
            variant="text"
            theme="danger"
            @click="askDelete(item)"
          >
            {{ $t('common.delete') }}
          </t-button>
        </div>

        <t-collapse v-if="!item.installed && item.guidance" :default-value="[]">
          <t-collapse-panel :header="$t('sop.cli.guidance')">
            <p class="sop-cli-guidance">{{ item.guidance }}</p>
          </t-collapse-panel>
        </t-collapse>
      </article>
    </div>

    <section class="sop-cli-extra">
      <div class="sop-card">
        <h4>{{ $t('sop.cli.workspaceTitle') }}</h4>
        <div class="sop-kv">
          <span class="k">{{ $t('sop.cli.workspaceRoot') }}</span>
          <span class="v mono">{{ workspaceRoot || $t('sop.cli.workspaceDefault') }}</span>
        </div>
        <p class="sop-hint">{{ $t('sop.cli.workspaceHint') }}</p>
        <t-button size="small" variant="outline" @click="openProjectSettings">
          {{ $t('sop.cli.goProjectSettings') }}
        </t-button>
      </div>

      <div class="sop-card">
        <h4>{{ $t('sop.cli.blocklistTitle') }}</h4>
        <p class="sop-hint">{{ $t('sop.cli.blocklistDesc') }}</p>
        <ul class="sop-blocklist">
          <li v-for="cmd in BLOCKED_COMMANDS" :key="cmd" class="mono">{{ cmd }}</li>
        </ul>
      </div>
    </section>

    <t-dialog
      v-if="credentialVisible"
      :visible="true"
      width="520px"
      placement="center"
      :header="$t('sop.cli.credTitle', { name: credentialTarget?.label || '' })"
      :confirm-btn="{ content: $t('common.save'), loading: saving }"
      :cancel-btn="$t('common.cancel')"
      :on-confirm="saveCredentialInput"
      @close="credentialVisible = false"
    >
      <div class="sop-cred-form">
        <label class="sop-field">
          <span>{{ $t('sop.cli.credName') }}</span>
          <t-input v-model="credentialForm.name" :maxlength="60" />
        </label>
        <label class="sop-field">
          <span>{{ $t('sop.cli.apiKey') }}</span>
          <t-input v-model="credentialForm.api_key" type="password" :placeholder="$t('sop.cli.apiKeyHint')" />
        </label>
        <label class="sop-field">
          <span>{{ $t('sop.cli.baseUrl') }}</span>
          <t-input v-model="credentialForm.base_url" placeholder="https://api.example.com" />
        </label>
        <label class="sop-field">
          <span>{{ $t('sop.cli.defaultModel') }}</span>
          <t-input v-model="credentialForm.model" />
        </label>
        <p class="sop-hint">{{ $t('sop.cli.credHint') }}</p>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  cliStatus,
  deleteCredential,
  listCredentials,
  saveCredential,
  type CLICredentialMasked,
  type CLIStatus,
} from '@/api/sop';
import { useSopStore } from '@/stores/sop';
import { useConfirmDelete } from '@/components/settings/useConfirmDelete';

const router = useRouter();
const { t } = useI18n();
const confirmDanger = useConfirmDelete();
const store = useSopStore();
const { currentProject } = storeToRefs(store);

const statuses = ref<CLIStatus[]>([]);
const credentials = ref<CLICredentialMasked[]>([]);
const refreshing = ref(false);
const saving = ref(false);
const credentialVisible = ref(false);
const credentialTarget = ref<{ kind: string; label: string } | null>(null);
const credentialForm = reactive({ name: '', api_key: '', base_url: '', model: '' });

/**
 * 危险命令黑名单：与后端脚本执行器的拒绝清单同源（执行时以服务端为准），
 * 这里只做只读展示，避免前端给人「改了这条就能绕过」的错觉。
 */
const BLOCKED_COMMANDS = [
  'rm -rf /',
  ':(){ :|:& };:',
  'mkfs.*',
  'dd if=/dev/zero of=/dev/*',
  'chmod -R 777 /',
  'shutdown / reboot',
  'curl * | sh',
];

const CLI_NAMES: Record<string, string> = {
  claude: 'Claude Code',
  opencode: 'opencode',
  'cursor-agent': 'Cursor CLI',
  agy: 'Antigravity',
  kimi: 'Kimi Code',
  pi: 'pi-coding-agent',
  dsh: 'DeepSeek CLI',
  zcode: 'zcode',
  codebuddy: 'CodeBuddy',
  codex: 'OpenAI Codex',
};

const cards = computed(() =>
  statuses.value.map((status) => ({
    ...status,
    label: CLI_NAMES[status.kind] || status.kind,
    credential: credentials.value.find((cred) => cred.cli_kind === status.kind) || null,
  })),
);

const workspaceRoot = computed(() => currentProject.value?.workspace_root || '');

async function refresh() {
  refreshing.value = true;
  try {
    const [list, creds] = await Promise.all([cliStatus(), listCredentials().catch(() => [])]);
    statuses.value = list;
    credentials.value = creds;
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.cli.loadFailed'));
  } finally {
    refreshing.value = false;
  }
}

function openCredential(item: { kind: string; label: string }) {
  credentialTarget.value = item;
  const kind = item.kind;
  const existing = credentials.value.find((cred) => cred.cli_kind === kind);
  credentialForm.name = existing?.name || '';
  credentialForm.api_key = '';
  credentialForm.base_url = existing?.base_url || '';
  credentialForm.model = existing?.default_model || '';
  credentialVisible.value = true;
}

async function saveCredentialInput() {
  if (!credentialTarget.value) return;
  saving.value = true;
  try {
    await saveCredential(credentialTarget.value.kind, {
      name: credentialForm.name.trim(),
      api_key: credentialForm.api_key.trim(),
      base_url: credentialForm.base_url.trim(),
      model: credentialForm.model.trim(),
    });
    MessagePlugin.success(t('sop.cli.credSaved'));
    credentialVisible.value = false;
    await refresh();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.cli.credSaveFailed'));
  } finally {
    saving.value = false;
  }
}

function askDelete(item: { kind: string; label: string }) {
  confirmDanger({
    title: t('sop.cli.credDeleteTitle'),
    body: t('sop.cli.credDeleteBody', { name: item.label }),
    confirmText: t('common.delete'),
    onConfirm: async () => {
      try {
        await deleteCredential(item.kind);
        MessagePlugin.success(t('sop.cli.credDeleted'));
        await refresh();
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('sop.cli.credDeleteFailed'));
      }
    },
  });
}

function openProjectSettings() {
  void router.push({ name: 'sopProjectSettings' });
}

onMounted(refresh);
</script>

<style lang="less" scoped src="./sopSettings.less"></style>
