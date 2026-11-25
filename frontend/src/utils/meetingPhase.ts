import type { MeetingPhase } from '@/api/meeting';

// 阶段 → i18n key；未知/空串返回空，调用方自行回退显示原始值。
const PHASE_KEYS: Record<string, string> = {
  opening: 'meeting.phaseOpening',
  confirming: 'meeting.phaseConfirming',
  discussing: 'meeting.phaseDiscussing',
  converging: 'meeting.phaseConverging',
  producing: 'meeting.phaseProducing',
  archived: 'meeting.phaseArchived',
};

export function meetingPhaseLabelKey(phase: MeetingPhase | string): string {
  return PHASE_KEYS[phase] || '';
}

// 阶段 → tdesign tag 主题：讨论中主色、收敛成功、进行中类阶段警示色
export function meetingPhaseTheme(phase: string): 'default' | 'primary' | 'success' | 'warning' {
  switch (phase) {
    case 'discussing':
      return 'primary';
    case 'converging':
      return 'success';
    case 'opening':
    case 'confirming':
    case 'producing':
      return 'warning';
    default:
      return 'default';
  }
}
