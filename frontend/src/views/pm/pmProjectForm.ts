// ===== PM 项目表单纯逻辑（组件层无测试框架，校验抽到此处单测）=====

export const PROJECT_NAME_MAX_LENGTH = 255;

export interface ProjectNameMessages {
  empty: string;
  tooLong: string;
}

// 默认中文文案仅用于单测；组件调用时传入 i18n 文案
const DEFAULT_MESSAGES: ProjectNameMessages = {
  empty: '项目名称不能为空',
  tooLong: '项目名称不能超过 255 个字符',
};

// 返回错误文案，空串表示通过；名称按去首尾空格后的长度校验
export function validateProjectName(
  name: string,
  messages: ProjectNameMessages = DEFAULT_MESSAGES,
): string {
  const trimmed = (name || '').trim();
  if (!trimmed) return messages.empty;
  if (trimmed.length > PROJECT_NAME_MAX_LENGTH) return messages.tooLong;
  return '';
}

// 提交给后端的名称统一去首尾空格
export function normalizeProjectName(name: string): string {
  return (name || '').trim();
}
