<template>
  <div class="power-panel">
    <div class="power-panel__header">
      <div>
        <h3 class="power-panel__title">{{ t('settings.parser.power.title') }}</h3>
        <p class="power-panel__desc">{{ t('settings.parser.power.description') }}</p>
      </div>
      <span
        class="power-panel__badge"
        :class="available ? 'power-panel__badge--on' : 'power-panel__badge--off'"
      >
        {{ available ? t('settings.parser.power.online') : t('settings.parser.power.offline') }}
      </span>
    </div>

    <p v-if="!available" class="power-panel__hint">
      {{ t('settings.parser.power.offlineHint') }}
      <code>python D:\知识库\Know\tools\engine_supervisor.py</code>
    </p>

    <template v-else>
      <div v-for="name in engineNames" :key="name" class="power-row">
        <div class="power-row__meta">
          <span class="power-row__name">{{ t(`settings.parser.power.${name}`) }}</span>
          <span class="power-row__state">{{ stateText(name) }}</span>
        </div>
        <t-switch
          :value="isRunning(name)"
          :disabled="!!busy[name]"
          :loading="!!busy[name]"
          @change="(val: unknown) => onToggleChange(name, val)"
        />
      </div>

      <div class="power-row power-row--config">
        <div class="power-row__meta">
          <span class="power-row__name">{{ t('settings.parser.power.idleLabel') }}</span>
          <span class="power-row__state">{{ t('settings.parser.power.idleHint') }}</span>
        </div>
        <div class="power-row__actions">
          <t-input-number
            v-model="idleMinutes"
            size="small"
            theme="normal"
            :min="0"
            :max="1440"
            :step="5"
          />
          <t-button size="small" variant="outline" :loading="savingIdle" @click="saveIdle">
            {{ t('settings.parser.power.save') }}
          </t-button>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'

import {
  getEnginePowerStatus,
  setEnginePower,
  updateEnginePowerConfig,
  type EnginePowerStatus,
} from '@/api/system'

const { t } = useI18n()

// 仅自建引擎可被宿主机控制器启停；云端引擎没有进程可控制
const engineNames = ['paddleocr_vl', 'mineru'] as const
type EngineName = (typeof engineNames)[number]

const status = ref<EnginePowerStatus | null>(null)
const busy = reactive<Record<string, boolean>>({})
// 启动需要 30~90 秒，用本地挂起状态保持开关为“开”，避免状态轮询把开关打回“关”
const pending = reactive<Record<string, number>>({})
const PENDING_TTL = 180000
const savingIdle = ref(false)
const idleMinutes = ref(30)

let timer: number | undefined

const available = computed(() => status.value?.supervisor_available === true)

function pendingActive(name: string): boolean {
  const startedAt = pending[name]
  return !!startedAt && Date.now() - startedAt < PENDING_TTL
}

function isRunning(name: string): boolean {
  return status.value?.engines?.[name]?.running === true || pendingActive(name)
}

function stateText(name: string): string {
  if (busy[name]) return t('settings.parser.power.switching')
  if (pendingActive(name) && !isRunningByStatus(name)) return t('settings.parser.power.starting')
  if (!isRunning(name)) return t('settings.parser.power.stopped')
  const idle = status.value?.engines?.[name]?.idle_sec ?? 0
  const minutes = Math.floor(idle / 60)
  return minutes > 0
    ? t('settings.parser.power.runningIdle', { minutes })
    : t('settings.parser.power.running')
}

function isRunningByStatus(name: string): boolean {
  return status.value?.engines?.[name]?.running === true
}

async function load() {
  try {
    const res = await getEnginePowerStatus()
    status.value = res?.data ?? null
    // 引擎一旦真正就绪，清掉本地挂起状态
    for (const name of engineNames) {
      if (status.value?.engines?.[name]?.running) delete pending[name]
    }
    if (status.value && !savingIdle.value) {
      idleMinutes.value = Math.round((status.value.idle_timeout_sec ?? 1800) / 60)
    }
  } catch {
    status.value = null
  }
}

/** t-switch 的 change 只给出新值，这里统一转换后调用 toggle */
function onToggleChange(name: EngineName, value: unknown) {
  void toggle(name, value === true)
}

async function toggle(name: EngineName, next: boolean) {
  busy[name] = true
  if (next) pending[name] = Date.now()
  else delete pending[name]
  try {
    const res = await setEnginePower(name, next ? 'start' : 'stop')
    if (res?.code !== 0) {
      MessagePlugin.error(res?.msg || t('settings.parser.power.actionFailed'))
    } else {
      MessagePlugin.success(
        next ? t('settings.parser.power.startRequested') : t('settings.parser.power.stopRequested'),
      )
    }
  } catch {
    MessagePlugin.error(t('settings.parser.power.actionFailed'))
  } finally {
    // 启动需要加载模型，稍后再刷新状态；此处只解除按钮占用
    setTimeout(() => {
      busy[name] = false
      void load()
    }, 1500)
  }
}

async function saveIdle() {
  savingIdle.value = true
  try {
    const res = await updateEnginePowerConfig(Math.max(0, Math.floor(idleMinutes.value)) * 60)
    if (res?.code !== 0) {
      MessagePlugin.error(res?.msg || t('settings.parser.power.actionFailed'))
    } else {
      MessagePlugin.success(t('settings.parser.power.saved'))
      await load()
    }
  } catch {
    MessagePlugin.error(t('settings.parser.power.actionFailed'))
  } finally {
    savingIdle.value = false
  }
}

onMounted(() => {
  void load()
  timer = window.setInterval(load, 5000)
})

onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
})
</script>

<style scoped>
.power-panel {
  margin-bottom: 16px;
  padding: 16px;
  border: 1px solid var(--td-component-stroke, #e7e7e7);
  border-radius: 8px;
  background: var(--td-bg-color-container, #fff);
}

.power-panel__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.power-panel__title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--td-text-color-primary, #1f2329);
}

.power-panel__desc {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--td-text-color-secondary, #8f959e);
}

.power-panel__badge {
  flex: none;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
}

.power-panel__badge--on {
  color: var(--td-success-color, #2ba471);
  background: var(--td-success-color-light, #e8f8f2);
}

.power-panel__badge--off {
  color: var(--td-text-color-secondary, #8f959e);
  background: var(--td-bg-color-secondarycontainer, #f3f3f3);
}

.power-panel__hint {
  margin: 12px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--td-text-color-secondary, #8f959e);
}

.power-panel__hint code {
  display: inline-block;
  margin-top: 4px;
  padding: 2px 6px;
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer, #f3f3f3);
}

.power-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 0;
  border-top: 1px solid var(--td-component-stroke, #e7e7e7);
}

.power-row:first-of-type {
  margin-top: 8px;
}

.power-row__meta {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.power-row__name {
  font-size: 13px;
  color: var(--td-text-color-primary, #1f2329);
}

.power-row__state {
  font-size: 12px;
  color: var(--td-text-color-secondary, #8f959e);
}

.power-row__actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.power-row--config :deep(.t-input-number) {
  width: 130px;
}
</style>
