// 项目资料纯函数层：类型/状态映射、列表筛选、草稿校验。
// 不依赖浏览器 API，便于 Node 单测；组件只负责绑定与网络请求。
import type { PmMaterial, PmMaterialType } from '@/api/pm';
import type { PmStatusMeta } from './pmStatus';

// ===== 类型与状态 =====

export const MATERIAL_TYPES: PmMaterialType[] = ['manual', 'brochure', 'ppt'];

export const MATERIAL_TYPE_MAP: Record<PmMaterialType, PmStatusMeta> = {
  manual: { labelKey: 'pm.materials.type.manual', theme: 'primary' },
  brochure: { labelKey: 'pm.materials.type.brochure', theme: 'primary' },
  ppt: { labelKey: 'pm.materials.type.ppt', theme: 'primary' },
};

// 草稿/已发布两态；归档态单独以「已归档」标签展示
export const MATERIAL_STATUS_MAP: Record<string, PmStatusMeta> = {
  draft: { labelKey: 'pm.materials.status.draft', theme: 'warning' },
  published: { labelKey: 'pm.materials.status.published', theme: 'success' },
};

export const MATERIAL_ARCHIVED_META: PmStatusMeta = {
  labelKey: 'pm.materials.status.archived',
  theme: 'default',
};

// ===== 列表筛选 =====

export interface MaterialFilterState {
  keyword: string;
  type: PmMaterialType | '';
  // 对应版本 ID，空串表示全部版本
  versionId: string;
}

export function emptyMaterialFilter(): MaterialFilterState {
  return { keyword: '', type: '', versionId: '' };
}

// 关键字命中资料名称与版本号；归档记录由页面单独组装历史区
export function filterMaterials<T extends Pick<PmMaterial, 'title' | 'material_type' | 'version_id' | 'version_no'>>(
  items: T[],
  filter: MaterialFilterState,
): T[] {
  const keyword = filter.keyword.trim().toLowerCase();
  return items.filter((item) => {
    if (keyword) {
      const haystack = `${item.title} ${item.version_no ?? ''}`.toLowerCase();
      if (!haystack.includes(keyword)) return false;
    }
    if (filter.type && item.material_type !== filter.type) return false;
    if (filter.versionId && item.version_id !== filter.versionId) return false;
    return true;
  });
}

function timeValue(value: string | null | undefined): number {
  if (!value) return 0;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}

// 更新时间倒序，不修改原数组
export function sortMaterials<T extends Pick<PmMaterial, 'updated_at'>>(items: T[]): T[] {
  return [...items].sort((a, b) => timeValue(b.updated_at) - timeValue(a.updated_at));
}

// 历史版本：同项目同类型的归档记录，排除当前资料自身；列表弹窗与预览页共用同一口径
export function materialHistory<
  T extends Pick<PmMaterial, 'id' | 'material_type' | 'archived'>,
>(items: T[], current: Pick<PmMaterial, 'id' | 'material_type'>): T[] {
  return items.filter(
    (m) => m.id !== current.id && m.archived && m.material_type === current.material_type,
  );
}

// ===== 操作可用性 =====

// 草稿可生成、可发布；归档记录只读
export function canGenerate(m: Pick<PmMaterial, 'status' | 'archived'>): boolean {
  return !m.archived && m.status === 'draft';
}

export function canPublish(m: Pick<PmMaterial, 'status' | 'archived'>): boolean {
  return !m.archived && m.status === 'draft';
}

// 有新版提示且未归档时才允许更新到最新版本
export function canUpdateToLatest(
  m: Pick<PmMaterial, 'archived' | 'needs_update'>,
): boolean {
  return !m.archived && Boolean(m.needs_update);
}

// 正文非空才可导出（与后端 ErrMaterialContentEmpty 对齐）；归档记录仍可下载追溯
export function canDownload(m: Pick<PmMaterial, 'content'>): boolean {
  return Boolean(m.content?.trim());
}

// 与后端一致：仅未归档草稿可删除
export function canDelete(m: Pick<PmMaterial, 'status' | 'archived'>): boolean {
  return !m.archived && m.status === 'draft';
}

// 下载文件名：PPT 导出 pptx，手册/宣传册导出 docx；剔除文件系统非法字符
export function materialFileName(
  m: Pick<PmMaterial, 'title' | 'material_type'>,
): string {
  const base = (m.title || '').replace(/[\\/:*?"<>|\r\n]/g, '_').trim() || 'material';
  return `${base}${m.material_type === 'ppt' ? '.pptx' : '.docx'}`;
}

// ===== 新建草稿 =====

export interface MaterialDraft {
  title: string;
  material_type: PmMaterialType;
  version_id: string;
}

export function emptyMaterialDraft(): MaterialDraft {
  return { title: '', material_type: 'manual', version_id: '' };
}

// 返回 i18n 文案 key，空串表示校验通过
export function validateMaterialDraft(draft: MaterialDraft): string {
  if (!draft.title.trim()) return 'pm.materials.form.errTitle';
  if (!draft.version_id) return 'pm.materials.form.errVersion';
  return '';
}

// 列表与预览页统一展示到分钟，日期非法时占位
export function formatPmDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
