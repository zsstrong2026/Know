<template>
  <SopPlaceholder
    :title="$t('sop.approvals.title')"
    :description="$t('sop.approvals.desc')"
    :empty-text="$t('sop.approvals.empty')"
  />
</template>

<script setup lang="ts">
import SopPlaceholder from './components/SopPlaceholder.vue';
</script>
