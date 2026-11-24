<template>
  <SopPlaceholder
    :title="$t('sop.tasks.title')"
    :description="$t('sop.tasks.desc')"
    :empty-text="$t('sop.tasks.empty')"
  />
</template>

<script setup lang="ts">
import SopPlaceholder from './components/SopPlaceholder.vue';
</script>
