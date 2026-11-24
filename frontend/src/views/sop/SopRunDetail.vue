<template>
  <SopPlaceholder
    :title="$t('sop.runDetail.title')"
    :description="$t('sop.runDetail.desc')"
    :empty-text="$t('sop.runDetail.empty')"
  />
</template>

<script setup lang="ts">
import SopPlaceholder from './components/SopPlaceholder.vue';
</script>
