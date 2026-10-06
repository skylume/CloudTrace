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
import { useHistoryStore } from '@/stores/history'
import { useSettingsStore } from '@/stores/settings'
import { useUIStore } from '@/stores/ui'
import { requestWebPermission } from '@/utils/notify'
import { checkCombinations, type ParamWarning } from '@/utils/paramRules'
import { useNarrow } from '@/utils/useMediaQuery'

const settings = useSettingsStore()
const geo = useGeoStore()
const history = useHistoryStore()
const ui = useUIStore()

const narrow = useNarrow()
const activeGroup = ref(SETTING_GROUPS[0]!.id)
/**
 * 窄屏下哪些分组是展开的。
 *
 * 与 activeGroup 分开：宽屏一次只看一组，窄屏把所有组叠起来、各自可折叠——
 * 手机上「点芯片切组」意味着每换一组都要滚回顶部，而折叠列表不需要。
 */
const openGroups = ref<string[]>([SETTING_GROUPS[0]!.id])

function toggleGroup(id: string): void {
  openGroups.value = openGroups.value.includes(id)
    ? openGroups.value.filter((item) => item !== id)
    : [...openGroups.value, id]
}

/**
 * 每一组在当前模式下实际会渲染的字段。
 *
 * 简单模式只留标了 primary 的项——一份六十多项的配置表一次全铺开，用户找一项
 * 得先读完十一组。一组里一个常用项都没有就不显示这一组，免得点进去是空的。
 */
const groupsWithFields = computed(() =>
  SETTING_GROUPS.map((group) => ({
    ...group,
    fields: ui.showAdvanced ? group.fields : group.fields.filter((field) => field.primary),
  })).filter((group) => group.fields.length > 0),
)

/** 当前选中的组。切到简单模式后原选中的组可能已经不在列表里，退回第一组。 */
const activeGroupSafe = computed(() =>
  groupsWithFields.value.some((item) => item.id === activeGroup.value)
    ? activeGroup.value
    : (groupsWithFields.value[0]?.id ?? ''),
)

/** 窄屏渲染全部分组，宽屏只渲染当前选中的那一组。 */
const visibleGroups = computed(() =>
  narrow.value
    ? groupsWithFields.value
    : groupsWithFields.value.filter((item) => item.id === activeGroupSafe.value),
)

function isOpen(id: string): boolean {
  return !narrow.value || openGroups.value.includes(id)
}
const healthChecked = computed(() => settings.health !== null)
const healthIssues = computed(() => settings.health?.issues ?? [])
const confirmingReset = ref(false)
const confirmingClear = ref(false)
const confirmingDir = ref(false)
const dataDirDraft = ref('')

onMounted(() => {
  sendCommand('settings/get')
  sendCommand('geo/status')
  // 危险区那条「清空历史」要显示条数，所以这里顺带拉一次索引。
  history.refresh()
})

const values = computed<Record<string, unknown>>(
  () => (settings.values ?? {}) as unknown as Record<string, unknown>,
)

const groupLabel = (id: string) => groupText[ui.lang][id] ?? id

/**
 * 组合起来不合理的参数。
 *
 * 只提示，不给「一键修复」：该改哪一边取决于用户想干什么。放在页面顶部而
 * 不是塞进某一组里，是因为涉及的键往往横跨两组（并发在扫描、间隔在测速），
 * 挂在任一组下都会让人以为改另一组就好。
 */
const warnings = computed<ParamWarning[]>(() => checkCombinations(values.value))

function warningText(item: ParamWarning): string {
  return t(`paramRule.${item.rule}` as never, { a: item.values[0] ?? 0, b: item.values[1] ?? 0 })
}

/** 改一项：只把这一项按点号路径拼成嵌套 patch，不动别的键。 */
function change(path: string, value: unknown): void {
  sendCommand('settings/update', { patch: buildPatch(path, value), origins: { [path]: 'user' } })

  // 打开浏览器通知时顺手申请权限：申请需要一个用户手势，而任务结束时没有手势
  // 可借。用户之前拒绝过就不再问——反复弹窗只会让人把整个站点的通知永久关掉。
  if (path === 'notify.web' && value === true) void requestWebPermission()
}

function resetField(path: string): void {
  sendCommand('settings/reset', { keys: [path] })
}

function resetGroupOf(id: string): void {
  sendCommand('settings/reset', { keys: [id] })
}

/** 恢复推荐设置影响面大，先确认再发。 */
function resetAll(): void {
  sendCommand('settings/reset', { keys: [] })
  confirmingReset.value = false
}

/**
 * 清空历史。
 *
 * 这个操作没有撤销窗口（后端刻意不给：撤销是给「点错了一个」准备的），所以
 * 确认按钮上带条数——「清空 37 条」比「确认」更能让人停下来看一眼。
 */
function clearHistory(): void {
  history.clear()
  confirmingClear.value = false
}

/** 当前生效的数据目录。留空表示按便携模式走，这里就显示成「便携模式」。 */
const currentDataDir = computed(() => {
  const value = readPath(values.value, 'data.dir')
  return typeof value === 'string' && value !== '' ? value : t('settings.dataDirPortable')
})

function openDirSwitch(): void {
  const value = readPath(values.value, 'data.dir')
  dataDirDraft.value = typeof value === 'string' ? value : ''
  confirmingDir.value = true
}

/**
 * 切数据目录。
 *
 * 只写配置，不搬数据：搬迁要跨盘复制整个目录，中途失败会把两边的数据都搞成
 * 半截状态，那是另一个功能。所以确认文案里把「原目录不会自动搬」说清楚——
 * 用户以为搬了、实际没搬，比直接告诉他没搬糟得多。
 */
function applyDataDir(): void {
  const next = dataDirDraft.value.trim()
  if (next === '') return
  change('data.dir', next)
  confirmingDir.value = false
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

    <div v-if="warnings.length > 0" class="warnings">
      <Banner
        v-for="item in warnings"
        :key="item.rule"
        tone="warn"
        :message="warningText(item)"
        :note="t('paramRule.onlyHint')"
        :closable="false"
      />
    </div>

    <!-- 披露程度：简单模式只显示常用项，与扫描页的「详细设置」共用同一个开关。 -->
    <div class="mode-row">
      <span class="ct-subtle">{{ ui.showAdvanced ? t('settings.allHint') : t('settings.coreHint') }}</span>
      <span class="spacer" />
      <button
        type="button"
        class="ct-link"
        :aria-expanded="ui.showAdvanced"
        @click="ui.setDensity(ui.showAdvanced ? 'simple' : 'advanced')"
      >
        {{ ui.showAdvanced ? t('settings.showCore') : t('settings.showAll') }}
      </button>
    </div>

    <nav v-if="!narrow" class="groups" :aria-label="t('settings.groups')">
      <button
        v-for="item in groupsWithFields"
        :key="item.id"
        type="button"
        class="group-tab"
        :class="{ on: activeGroupSafe === item.id }"
        @click="activeGroup = item.id"
      >
        {{ groupLabel(item.id) }}
      </button>
    </nav>

    <div class="columns">
      <div class="main">
        <section v-for="item in visibleGroups" :key="item.id" class="ct-card">
          <h2 class="ct-card-title">
            <button
              v-if="narrow"
              type="button"
              class="ct-link disclosure"
              :aria-expanded="isOpen(item.id)"
              @click="toggleGroup(item.id)"
            >
              {{ isOpen(item.id) ? '▾' : '▸' }} {{ groupLabel(item.id) }}
            </button>
            <span v-else>{{ groupLabel(item.id) }}</span>
            <span class="spacer" />
            <button type="button" class="ct-link" @click="resetGroupOf(item.id)">{{ t('common.reset') }}</button>
          </h2>
          <template v-if="isOpen(item.id)">
            <SettingsField
              v-for="field in item.fields"
              :key="field.path"
              :field="field"
              :value="readPath(values, field.path)"
              :values="values"
              :warning="settings.warningOf(field.path)"
              :locale="ui.lang"
              @change="change"
              @reset="resetField"
            />
          </template>
        </section>
      </div>

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

          <!--
            数据目录切换。这条与设置页里那个普通输入框是同一个配置项，区别在于
            这里会先把后果说清楚再让你确认——改了它，历史、缓存、ASN 库就换地方了。
          -->
          <div class="danger-row">
            <div class="danger-info">
              <span>{{ t('settings.dataDir') }}</span>
              <span class="ct-mono ct-subtle">{{ currentDataDir }}</span>
            </div>
            <template v-if="confirmingDir">
              <input
                v-model="dataDirDraft"
                class="ct-input dir-input"
                type="text"
                spellcheck="false"
                :placeholder="t('settings.dataDirPlaceholder')"
              />
              <button type="button" class="ct-btn danger-btn" @click="applyDataDir">
                {{ t('common.confirm') }}
              </button>
              <button type="button" class="ct-btn" @click="confirmingDir = false">{{ t('common.cancel') }}</button>
            </template>
            <button v-else type="button" class="ct-btn" @click="openDirSwitch">
              {{ t('settings.dataDirSwitch') }}
            </button>
          </div>
          <p v-if="confirmingDir" class="ct-subtle warn-text">{{ t('settings.dataDirWarn') }}</p>

          <div class="danger-actions">
            <template v-if="confirmingReset">
              <button type="button" class="ct-btn danger-btn" @click="resetAll">{{ t('common.confirm') }}</button>
              <button type="button" class="ct-btn" @click="confirmingReset = false">{{ t('common.cancel') }}</button>
            </template>
            <template v-else-if="confirmingClear">
              <button type="button" class="ct-btn danger-btn" @click="clearHistory">
                {{ t('settings.clearHistoryConfirm', { count: history.total }) }}
              </button>
              <button type="button" class="ct-btn" @click="confirmingClear = false">{{ t('common.cancel') }}</button>
            </template>
            <template v-else>
              <button type="button" class="ct-btn" @click="confirmingReset = true">
                {{ t('settings.resetAll') }}
              </button>
              <button
                type="button"
                class="ct-btn"
                :disabled="history.total === 0"
                @click="confirmingClear = true"
              >
                {{ t('settings.clearHistory') }}
              </button>
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

.warnings {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.mode-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  font-size: var(--font-size-sm);
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

.main {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
}

.disclosure {
  font-size: var(--font-size-md);
  font-weight: 500;
  color: var(--color-text);
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

/* 危险区的每一行：左边是说明，右边是动作。 */
.danger-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: var(--space-3) 0;
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border);
}

.danger-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 200px;
  flex: 1;
  font-size: var(--font-size-sm);
}

.dir-input {
  flex: 1 1 240px;
  font-family: var(--font-mono);
}

.warn-text {
  margin: 0 0 var(--space-3);
  font-size: var(--font-size-xs);
  color: var(--color-warn);
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
