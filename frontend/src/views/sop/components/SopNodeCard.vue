<template>
  <!-- 节点卡：标题 + 徽标 + 双出口（成功/失败打回）端口 -->
  <div class="sop-node" :class="{ readonly, 'is-pending': !!pendingSource }">
    <Handle type="target" :position="Position.Left" class="sop-handle sop-handle--in" />

    <div class="sop-node-head">
      <span class="sop-node-type" :class="`is-${node.type}`">{{ typeLabel }}</span>
      <b class="sop-node-name">{{ node.name || node.id }}</b>
    </div>

    <div class="sop-node-badges">
      <span class="sop-badge">{{ executorLabel }}</span>
      <span v-if="artifactCount" class="sop-badge">{{ $t('sop.designer.artifacts', { n: artifactCount }) }}</span>
      <span class="sop-badge" :class="`is-${node.on_failure}`">{{ failureLabel }}</span>
    </div>

    <template v-if="!readonly">
      <div class="sop-node-exits" @mousedown.stop>
        <div class="sop-exit">
          <span class="sop-dot is-success" />
          <span class="sop-exit-label">{{ $t('sop.designer.exitSuccess') }}</span>
          <Handle
            id="success"
            type="source"
            :position="Position.Right"
            class="sop-handle sop-handle--success"
            @click.stop="emit('exit-click', node.id, 'success')"
          />
        </div>
        <div class="sop-exit">
          <span class="sop-dot is-reject" />
          <span class="sop-exit-label">{{ $t('sop.designer.exitReject') }}</span>
          <span v-if="rejectTargetName" class="sop-exit-target">→ {{ rejectTargetName }}</span>
          <Handle
            id="reject"
            type="source"
            :position="Position.Right"
            class="sop-handle sop-handle--reject"
            @click.stop="emit('exit-click', node.id, 'reject')"
          />
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { Handle, Position } from '@vue-flow/core';
import type { SopNode } from '@/api/sop';

const props = defineProps<{
  node: SopNode;
  readonly: boolean;
  pendingSource: { nodeId: string; kind: string } | null;
  /** 打回目标节点的显示名（由设计器解析后传入，避免卡片自己查全图）。 */
  rejectTargetName?: string;
}>();

const emit = defineEmits<{
  'exit-click': [nodeId: string, kind: 'success' | 'reject'];
  'inbound-click': [nodeId: string];
}>();

const { t } = useI18n();

const typeLabel = computed(() => {
  switch (props.node.type) {
    case 'script':
      return t('sop.designer.typeScript');
    case 'approval':
      return t('sop.designer.typeApproval');
    case 'sop':
      return t('sop.designer.typeSop');
    case 'agent':
    default:
      return t('sop.designer.typeAgent');
  }
});

const executorLabel = computed(() => {
  const executor = props.node.executor || { kind: 'platform_agent' };
  if (executor.kind === 'cli') return t('sop.designer.execCli');
  return t('sop.designer.execAgent');
});

const failureLabel = computed(() => {
  switch (props.node.on_failure) {
    case 'reject':
      return t('sop.designer.failReject');
    case 'fail':
      return t('sop.designer.failFail');
    case 'human':
      return t('sop.designer.failHuman');
    case 'loop':
    default:
      return t('sop.designer.failLoop');
  }
});

const artifactCount = computed(() => (props.node.artifact_outputs || []).length);
</script>

<style lang="less" scoped src="./sopNodeCard.less"></style>
