/**
 * 离开 workspace 的数据预览（§9.10）。
 *
 * 预览失败不阻断退出：调用方拿不到数据时退化为通用确认文案。
 * 两处退出入口（组织列表、租户信息）共用这一个 composable，避免各写一套。
 */
import { ref } from 'vue'
import { getLeavePreview } from '@/api/workbench'
import type { WorkbenchLeavePreview } from '@/types/workbench'

export function useLeavePreview() {
  const preview = ref<WorkbenchLeavePreview | null>(null)
  const loading = ref(false)

  async function load(): Promise<WorkbenchLeavePreview | null> {
    loading.value = true
    try {
      preview.value = await getLeavePreview()
      return preview.value
    } catch {
      // 预览是附加信息，失败时由调用方给通用确认，不能因此阻止退出。
      preview.value = null
      return null
    } finally {
      loading.value = false
    }
  }

  return { preview, loading, load }
}
