<template>
  <SopPlaceholder :title="$t('sop.items.title')" :description="$t('sop.items.desc')">
    <template #actions>
      <!-- T40 会把入口移进统计带右端；这里先保证「唯一入口」在事项页可达 -->
      <t-button size="small" variant="outline" @click="openSettings">
        ⚙ {{ $t('sop.settings.projectTitle') }}
      </t-button>
    </template>
    <t-empty :description="$t('sop.items.empty')" />
  </SopPlaceholder>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router';
import SopPlaceholder from './components/SopPlaceholder.vue';

const router = useRouter();

function openSettings() {
  void router.push({ name: 'sopProjectSettings' });
}
</script>
