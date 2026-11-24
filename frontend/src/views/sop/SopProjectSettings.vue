<template>
  <SopPlaceholder
    :title="$t('sop.settings.projectTitle')"
    :description="$t('sop.settings.projectDesc')"
    :empty-text="$t('sop.settings.projectEmpty')"
  />
</template>

<script setup lang="ts">
import SopPlaceholder from './components/SopPlaceholder.vue';
</script>
