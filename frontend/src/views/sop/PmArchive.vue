<template>
  <SopPlaceholder
    :title="$t('sop.archive.title')"
    :description="$t('sop.archive.desc')"
    :empty-text="$t('sop.archive.empty')"
  />
</template>

<script setup lang="ts">
import SopPlaceholder from './components/SopPlaceholder.vue';
</script>
