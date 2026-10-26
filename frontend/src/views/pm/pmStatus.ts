// ===== PM 状态映射（数据库存英文枚举，UI 通过 labelKey 取 i18n 文案）=====

// TDesign Tag 主题
export type PmStatusTheme = 'default' | 'primary' | 'success' | 'warning' | 'danger';

export interface PmStatusMeta {
  labelKey: string;
  theme: PmStatusTheme;
}

// 需求状态：7 态，与 internal/types/pm.go 常量一一对应
export const REQUIREMENT_STATUS_MAP: Record<string, PmStatusMeta> = {
  pending_analysis: { labelKey: 'pm.requirement.status.pending_analysis', theme: 'default' },
  confirmed: { labelKey: 'pm.requirement.status.confirmed', theme: 'warning' },
  scheduled: { labelKey: 'pm.requirement.status.scheduled', theme: 'primary' },
  developing: { labelKey: 'pm.requirement.status.developing', theme: 'warning' },
  launched: { labelKey: 'pm.requirement.status.launched', theme: 'success' },
  rejected: { labelKey: 'pm.requirement.status.rejected', theme: 'danger' },
  closed: { labelKey: 'pm.requirement.status.closed', theme: 'default' },
};

// 看板列：只展示流转中的 5 个状态，终止态（驳回/关闭）留在列表视图
export const REQUIREMENT_BOARD_COLUMNS: Array<{ status: string; labelKey: string }> = [
  'pending_analysis',
  'confirmed',
  'scheduled',
  'developing',
  'launched',
].map((status) => ({ status, labelKey: REQUIREMENT_STATUS_MAP[status].labelKey }));

// 版本状态：6 态
export const VERSION_STATUS_MAP: Record<string, PmStatusMeta> = {
  planning: { labelKey: 'pm.version.status.planning', theme: 'default' },
  reviewing: { labelKey: 'pm.version.status.reviewing', theme: 'warning' },
  finalized: { labelKey: 'pm.version.status.finalized', theme: 'primary' },
  developing: { labelKey: 'pm.version.status.developing', theme: 'warning' },
  launched: { labelKey: 'pm.version.status.launched', theme: 'success' },
  shelved: { labelKey: 'pm.version.status.shelved', theme: 'danger' },
};

// 评审结论：通过/有条件通过均视为可定稿，不通过标红
export const VERSION_CONCLUSION_MAP: Record<string, PmStatusMeta> = {
  pass: { labelKey: 'pm.version.conclusion.pass', theme: 'success' },
  conditional: { labelKey: 'pm.version.conclusion.conditional', theme: 'success' },
  reject: { labelKey: 'pm.version.conclusion.reject', theme: 'danger' },
};
