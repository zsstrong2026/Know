<template>
  <div class="meeting-create">
    <h2>{{ $t('meeting.create') }}</h2>
    <t-form :data="form" :label-width="120" class="create-form">
      <t-form-item :label="$t('meeting.title')" required>
        <t-input v-model="form.title" :maxlength="100" />
      </t-form-item>
      <t-form-item :label="$t('meeting.topic')">
        <t-textarea v-model="form.description" :autosize="{ minRows: 2, maxRows: 4 }"
          :placeholder="$t('meeting.topicPlaceholder')" />
      </t-form-item>
      <t-form-item :label="$t('meeting.selectAgents')" required>
        <t-select v-model="form.agentIds" multiple filterable :placeholder="$t('meeting.selectAgents')">
          <t-option v-for="a in agents" :key="a.id" :value="a.id" :label="a.name" />
        </t-select>
      </t-form-item>
      <t-form-item :label="$t('meeting.selectKBs')">
        <t-select v-model="form.knowledgeBaseIds" multiple filterable :placeholder="$t('meeting.selectKBs')">
          <t-option v-for="kb in knowledgeBases" :key="kb.id" :value="kb.id" :label="kb.name" />
        </t-select>
      </t-form-item>
      <t-form-item :label="$t('meeting.broadcastMode')">
        <t-radio-group v-model="broadcastMode">
          <t-radio value="sequential">{{ $t('meeting.modeSequential') }}</t-radio>
          <t-radio value="parallel">{{ $t('meeting.modeParallel') }}</t-radio>
        </t-radio-group>
      </t-form-item>
      <t-form-item>
        <t-button theme="primary" :loading="submitting" @click="submit">
          {{ $t('meeting.createAndEnter') }}
        </t-button>
      </t-form-item>
    </t-form>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import { createMeeting, type BroadcastMode } from '@/api/meeting';
import { listAgents } from '@/api/agent';                    // 复用既有智能体列表接口
import { listKnowledgeBases } from '@/api/knowledge-base';   // 复用既有知识库列表接口

const router = useRouter();
const { t } = useI18n();
const agents = ref<Array<{ id: string; name: string }>>([]);
const knowledgeBases = ref<Array<{ id: string; name: string }>>([]);
const submitting = ref(false);
const form = reactive({
  title: '', description: '',
  agentIds: [] as string[], knowledgeBaseIds: [] as string[],
  broadcastMode: 'sequential' as BroadcastMode,
});

// t-radio-group 的变更值类型较宽，用带类型 setter 的 computed 收敛回 BroadcastMode
const broadcastMode = computed<BroadcastMode>({
  get: () => form.broadcastMode,
  set: (val) => { form.broadcastMode = val; },
});

onMounted(async () => {
  // listAgents / listKnowledgeBases 均返回 { data: [...] } 信封，这里统一解包取数组
  try {
    const [agentRes, kbRes] = await Promise.all([listAgents(), listKnowledgeBases()]);
    agents.value = agentRes?.data ?? [];
    knowledgeBases.value = (kbRes?.data ?? []) as Array<{ id: string; name: string }>;
  } catch {
    MessagePlugin.error(t('meeting.loadFailed'));
  }
});

async function submit() {
  submitting.value = true;
  try {
    const room = await createMeeting(form);
    router.push({ name: 'meetingRoom', params: { roomId: room.id } });
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.createFailed'));
  } finally {
    submitting.value = false;
  }
}
</script>

<style scoped>
.create-form {
  max-width: 640px;
}
</style>
