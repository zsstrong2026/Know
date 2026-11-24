<template>
  <SopPlaceholder
    :title="$t('sop.templates.title')"
    :description="$t('sop.templates.desc')"
    :empty-text="$t('sop.templates.empty')"
  />
</template>

<script setup lang="ts">
import SopPlaceholder from './components/SopPlaceholder.vue';
</script>
