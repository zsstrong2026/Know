<template>
  <SopPlaceholder
    :title="$t('sop.settings.cliTitle')"
    :description="$t('sop.settings.cliDesc')"
    :empty-text="$t('sop.settings.cliEmpty')"
  />
</template>

<script setup lang="ts">
import SopPlaceholder from './components/SopPlaceholder.vue';
</script>
