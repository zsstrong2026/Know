<template>
  <!-- 另存为模板（§9.16.3）：只复制已落盘的快照，图片/附件不复制 -->
  <t-dialog
    :visible="visible"
    :header="t('workbench.templates.save.title')"
    :confirm-btn="confirmBtn"
    :cancel-btn="t('workbench.templates.save.cancel')"
    :confirm-loading="saving"
    width="480px"
    @confirm="onConfirm"
    @cancel="emit('update:visible', false)"
    @close="emit('update:visible', false)"
  >
    <div class="stp">
      <label class="stp-field">
        <span class="stp-label">{{ t('workbench.templates.save.name') }}</span>
        <t-input
          v-model="name"
          :maxlength="NAME_MAX"
          :placeholder="t('workbench.templates.save.namePlaceholder')"
          size="small"
        />
        <span class="stp-counter">{{ name.trim().length }} / {{ NAME_MAX }}</span>
      </label>

      <label class="stp-field">
        <span class="stp-label">{{ t('workbench.templates.save.description') }}</span>
        <t-textarea
          v-model="description"
          :maxlength="DESC_MAX"
          :autosize="{ minRows: 2, maxRows: 4 }"
          :placeholder="t('workbench.templates.save.descriptionPlaceholder')"
        />
        <span class="stp-counter">{{ description.trim().length }} / {{ DESC_MAX }}</span>
      </label>

      <p class="stp-hint">{{ t('workbench.templates.save.assetHint') }}</p>
      <p class="stp-quota">{{ t('workbench.templates.save.quota', { left: savedCount, total: TEMPLATE_QUOTA }) }}</p>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 另存为模板对话框。
 *
 * 服务端从 workbench_items 复制**已落盘**的快照，因此父组件必须在打开前
 * flush 成功；这里只收集名称与描述，不上传任何字节。
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  TEMPLATE_QUOTA,
  templateQuotaLeft,
} from '@/workbench/templates/apply'

const NAME_MAX = 60
const DESC_MAX = 200

const props = defineProps<{
  visible: boolean
  /** 已存在模板数量；用于配额预检 */
  existingCount: number
  /** 默认名称，通常是当前文档标题 */
  defaultName: string
  saving?: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  submit: [payload: { name: string; description: string }]
}>()

const { t } = useI18n()

const name = ref('')
const description = ref('')

// 每次打开都用当前标题重置，避免沿用上一次的输入。
watch(
  () => props.visible,
  (open) => {
    if (!open) return
    name.value = props.defaultName.slice(0, NAME_MAX)
    description.value = ''
  },
  { immediate: true },
)

const quotaLeft = computed(() => templateQuotaLeft(props.existingCount))
/** 文案里的 {left} 是「已保存数量」：钉死在 50 附近时用户才知道还能不能再存。 */
const savedCount = computed(() => Math.min(props.existingCount, TEMPLATE_QUOTA))

const canSubmit = computed(() => name.value.trim().length > 0 && quotaLeft.value > 0)

/** 配额已满时禁用确认按钮，避免点下去才在服务端被 400 拒绝。 */
const confirmBtn = computed(() =>
  quotaLeft.value > 0
    ? t('workbench.templates.save.confirm')
    : t('workbench.templates.save.quotaFull'),
)

function onConfirm() {
  if (!canSubmit.value || props.saving) return
  emit('submit', { name: name.value.trim(), description: description.value.trim() })
}
</script>

<style lang="less" scoped>
.stp {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
}

.stp-field {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-1);
}

.stp-label {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
}

.stp-counter {
  align-self: flex-end;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
}

.stp-hint {
  margin: 0;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.stp-quota {
  margin: 0;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-secondary);
}
</style>
