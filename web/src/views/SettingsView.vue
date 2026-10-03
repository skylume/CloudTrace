<script setup lang="ts">
/**
 * 设置页：承载全部配置项而不造成压迫感。
 *
 * 三件事让它不至于压人：左侧分组导航（一次只看一组）、每项带一句「为什么要调」
 * 的说明、危险操作单独成区（不与日常设置混在一起，避免误触）。
 */
import { computed, onMounted, ref } from 'vue'

import { sendCommand } from '@/api/client'
import SettingsField from '@/components/settings/SettingsField.vue'
import Banner from '@/components/ui/Banner.vue'
import { t } from '@/i18n'
import { SETTING_GROUPS, buildPatch, groupText, readPath } from '@/i18n/settingsSchema'
import { useGeoStore } from '@/stores/geo'
import { useSettingsStore } from '@/stores/settings'
import { useUIStore } from '@/stores/ui'

const settings = useSettingsStore()
const geo = useGeoStore()
const ui = useUIStore()

const activeGroup = ref(SETTING_GROUPS[0]!.id)
const healthChecked = computed(() => settings.health !== null)
const healthIssues = computed(() => settings.health?.issues ?? [])
const confirmingReset = ref(false)

onMounted(() => {
  sendCommand('settings/get')
  sendCommand('geo/status')
})

const values = computed<Record<string, unknown>>(
  () => (settings.values ?? {}) as unknown as Record<string, unknown>,
)

const group = computed(() => SETTING_GROUPS.find((item) => item.id === activeGroup.value) ?? SETTING_GROUPS[0]!)

const groupLabel = (id: string) => groupText[ui.lang][id] ?? id

/** 改一项：只把这一项按点号路径拼成嵌套 patch，不动别的键。 */
function change(path: string, value: unknown): void {
  sendCommand('settings/update', { patch: buildPatch(path, value), origins: { [path]: 'user' } })
}

function resetField(path: string): void {
  sendCommand('settings/reset', { keys: [path] })
}

function resetGroup(): void {
  sendCommand('settings/reset', { keys: [group.value.id] })
}

/** 恢复推荐设置影响面大，先确认再发。 */
function resetAll(): void {
  sendCommand('settings/reset', { keys: [] })
  confirmingReset.value = false
}

function runHealth(): void {
  sendCommand('health/check')
}

function applyFix(issue: { fix?: { key: string; value: unknown } }): void {
  if (!issue.fix) return
  change(issue.fix.key, issue.fix.value)
}
</script>

<template>
  <div class="page">
    <Banner
      v-if="settings.restartRequired.length > 0"
      tone="warn"
      :message="t('settings.restartRequired', { keys: settings.restartRequired.join(', ') })"
    />

    <nav class="groups" aria-label="设置分组">
      <button
        v-for="item in SETTING_GROUPS"
        :key="item.id"
        type="button"
        class="group-tab"
        :class="{ on: activeGroup === item.id }"
        @click="activeGroup = item.id"
      >
        {{ groupLabel(item.id) }}
      </button>
    </nav>

    <div class="columns">
      <section class="ct-card">
        <h2 class="ct-card-title">
          <span>{{ groupLabel(group.id) }}</span>
          <span class="spacer" />
          <button type="button" class="ct-link" @click="resetGroup()">{{ t('common.reset') }}</button>
        </h2>
        <SettingsField
          v-for="field in group.fields"
          :key="field.path"
          :field="field"
          :value="readPath(values, field.path)"
          :locale="ui.lang"
          @change="change"
          @reset="resetField"
        />
      </section>

      <div class="side">
        <section class="ct-card">
          <h2 class="ct-card-title">
            <span>{{ t('health.title') }}</span>
            <span class="spacer" />
            <button type="button" class="ct-btn" @click="runHealth">{{ t('health.run') }}</button>
          </h2>
          <p v-if="healthIssues.length === 0" class="ct-subtle">
            {{ healthChecked ? t('health.noIssues') : t('health.checked', { count: 0 }) }}
          </p>
          <ul v-else class="issues">
            <li v-for="issue in healthIssues" :key="issue.key" :class="issue.level">
              <span>{{ issue.problem }}</span>
              <span class="ct-subtle">{{ issue.suggestion }}</span>
              <button v-if="issue.fixable && issue.fix" type="button" class="ct-link" @click="applyFix(issue)">
                {{ t('health.fix') }}
              </button>
            </li>
          </ul>
        </section>

        <section class="ct-card">
          <h2 class="ct-card-title">
            <span>{{ t('geo.title') }}</span>
            <span class="spacer" />
            <button type="button" class="ct-btn" @click="sendCommand('geo/update')">{{ t('geo.update') }}</button>
          </h2>
          <dl class="facts">
            <dt>{{ t('geo.source') }}</dt>
            <dd>{{ geo.status?.status.source ?? '—' }}</dd>
            <dt>{{ t('geo.records') }}</dt>
            <dd class="tnum">{{ geo.status?.status.records ?? 0 }}</dd>
            <dt>{{ t('geo.path') }}</dt>
            <dd class="ct-mono">{{ geo.status?.status.path ?? '—' }}</dd>
          </dl>
          <p v-if="geo.status?.status.error" class="ct-subtle error">{{ geo.status.status.error }}</p>
        </section>

        <section class="ct-card danger">
          <h2 class="ct-card-title">{{ t('settings.dangerZone') }}</h2>
          <p class="ct-subtle">{{ t('settings.dangerHint') }}</p>
          <div class="danger-actions">
            <button v-if="!confirmingReset" type="button" class="ct-btn" @click="confirmingReset = true">
              {{ t('settings.resetAll') }}
            </button>
            <template v-else>
              <button type="button" class="ct-btn danger-btn" @click="resetAll">{{ t('common.confirm') }}</button>
              <button type="button" class="ct-btn" @click="confirmingReset = false">{{ t('common.cancel') }}</button>
            </template>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.groups {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.group-tab {
  padding: var(--space-1) var(--space-3);
  border: 1px solid transparent;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--color-text-muted);
  font-size: var(--font-size-sm);
  cursor: pointer;
}

.group-tab:hover {
  background: var(--color-surface-hover);
}

.group-tab.on {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
  color: var(--color-primary-text);
}

.columns {
  display: grid;
  grid-template-columns: minmax(0, 1.5fr) minmax(0, 1fr);
  gap: var(--space-4);
  align-items: start;
}

.side {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.spacer {
  flex: 1;
}

.issues {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: var(--font-size-sm);
}

.issues li {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.issues li.error {
  color: var(--color-bad);
}

.issues li.warning {
  color: var(--color-warn);
}

.facts {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-1) var(--space-3);
  margin: 0;
  font-size: var(--font-size-xs);
}

.facts dt {
  color: var(--color-text-subtle);
}

.facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}

.error {
  margin-top: var(--space-2);
  color: var(--color-bad);
}

.danger {
  border-color: var(--color-bad-border);
}

.danger-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-3);
}

.danger-btn {
  border-color: var(--color-bad);
  color: var(--color-bad);
}

@media (max-width: 1024px) {
  .columns {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
