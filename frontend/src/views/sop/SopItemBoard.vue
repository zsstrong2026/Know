<template>
  <SopPlaceholder
    :title="$t('sop.items.title')"
    :description="$t('sop.items.desc')"
    :empty-text="$t('sop.items.empty')"
  />
</template>

<script setup lang="ts">
import SopPlaceholder from './components/SopPlaceholder.vue';
</script>
