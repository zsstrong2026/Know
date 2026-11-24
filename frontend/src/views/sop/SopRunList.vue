<template>
  <SopPlaceholder
    :title="$t('sop.runs.title')"
    :description="$t('sop.runs.desc')"
    :empty-text="$t('sop.runs.empty')"
  />
</template>

<script setup lang="ts">
import SopPlaceholder from './components/SopPlaceholder.vue';
</script>
