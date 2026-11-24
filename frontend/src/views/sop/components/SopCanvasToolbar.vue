<template>
  <div class="sop-toolbar">
    <button type="button" :disabled="!canUndo" :title="$t('sop.designer.undo')" @click="emit('undo')">↩</button>
    <button type="button" :disabled="!canRedo" :title="$t('sop.designer.redo')" @click="emit('redo')">↪</button>
    <span class="sop-sep" />
    <button type="button" :title="$t('sop.designer.zoomOut')" @click="emit('zoom-out')">−</button>
    <span class="sop-zoom">{{ Math.round(zoom * 100) }}%</span>
    <button type="button" :title="$t('sop.designer.zoomIn')" @click="emit('zoom-in')">＋</button>
    <span class="sop-sep" />
    <button type="button" :title="$t('sop.designer.fit')" @click="emit('fit')">⛶</button>
    <button type="button" :title="$t('sop.designer.autoLayout')" @click="emit('auto-layout')">⿻</button>
    <button
      type="button"
      :class="{ on: minimap }"
      :title="$t('sop.designer.minimap')"
      @click="emit('toggle-minimap')"
    >
      ▣
    </button>
    <span class="sop-legend">
      <i class="is-success" /> {{ $t('sop.designer.legendSuccess') }}
      <i class="is-reject" /> {{ $t('sop.designer.legendReject') }}
    </span>
  </div>
</template>

<script setup lang="ts">
defineProps<{ canUndo: boolean; canRedo: boolean; zoom: number; minimap: boolean }>();

const emit = defineEmits<{
  undo: [];
  redo: [];
  'zoom-in': [];
  'zoom-out': [];
  fit: [];
  'auto-layout': [];
  'toggle-minimap': [];
}>();
</script>

<style lang="less" scoped src="./sopCanvasToolbar.less"></style>
