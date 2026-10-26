import { ref } from 'vue';
import { defineStore } from 'pinia';
import { listPmProjects, type PmProject } from '@/api/pm';

// 项目列表被侧边栏与项目列表页共用，集中到 store 保证新建/归档后两侧同步
export const usePmProjectsStore = defineStore('pmProjectsStore', () => {
  const projects = ref<PmProject[]>([]);
  const loading = ref(false);

  async function fetchProjects() {
    loading.value = true;
    try {
      projects.value = await listPmProjects();
    } finally {
      loading.value = false;
    }
  }

  // 本地增量插入/替换，避免每次操作都整表重拉
  function upsert(project: PmProject) {
    const index = projects.value.findIndex((item) => item.id === project.id);
    if (index >= 0) projects.value.splice(index, 1, project);
    else projects.value.unshift(project);
  }

  // 删除成功后同步本地列表，避免重新拉取
  function remove(id: string) {
    projects.value = projects.value.filter((item) => item.id !== id);
  }

  return { projects, loading, fetchProjects, upsert, remove };
});
