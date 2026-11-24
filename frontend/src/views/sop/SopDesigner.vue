<template>
  <SopPlaceholder
    :title="$t('sop.designer.title')"
    :description="$t('sop.designer.desc')"
    :empty-text="$t('sop.designer.empty')"
  />
</template>

<script setup lang="ts">
import SopPlaceholder from './components/SopPlaceholder.vue';
</script>
