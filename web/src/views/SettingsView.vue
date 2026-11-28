<script setup lang="ts">
/**
 * 设置页：承载全部配置项而不造成压迫感。
 *
 * 三件事让它不至于压人：左侧分组导航（一次只看一组）、每项带一句「为什么要调」
 * 的说明、危险操作单独成区（不与日常设置混在一起，避免误触）。
 */
import { CMD } from '@/api/protocol'
import { computed, onMounted, ref } from 'vue'

import { sendCommand } from '@/api/client'
import { api } from '@/api/rest'
import SettingsField from '@/components/settings/SettingsField.vue'
import Banner from '@/components/ui/Banner.vue'
import { t } from '@/i18n'
import {
  SETTING_GROUPS,
  buildPatch,
  buildPatchMany,
  fieldMatches,
  fieldPatch,
  fieldValue,
  fieldVisible,
  groupText,
  readPath,
  type SettingGroup,
} from '@/i18n/settingsSchema'
import DiagPanel from '@/components/settings/DiagPanel.vue'
import ServerPanel from '@/components/settings/ServerPanel.vue'
import { useGeoStore } from '@/stores/conn'
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

/** 恢复中的状态：挡住连点，也用来禁用按钮。 */
const restoring = ref(false)
const restoreInput = ref<HTMLInputElement | null>(null)

/** 备份设置：后端直接回附件，交给浏览器原生下载。 */
function backupSettings(): void {
  api.download(api.settingsBackupUrl)
}

/** 恢复设置：按钮只负责打开文件选择，真正的导入在 change 之后。 */
function pickRestoreFile(): void {
  restoreInput.value?.click()
}

async function onRestoreFile(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  // 清空 value：否则选同一个文件再点一次不会触发 change。
  input.value = ''
  if (!file) return

  restoring.value = true
  try {
    const result = await api.restoreSettings(file)
    const parts = [t('settings.restoreDone')]
    if (result.ignored?.length) parts.push(t('settings.restoreIgnored'))
    if (result.restart_required?.length) parts.push(t('settings.restoreRestart'))
    ui.pushToast({ kind: result.ignored?.length ? 'warn' : 'ok', message: parts.join(' ') })
  } catch (err) {
    const message = err instanceof Error ? err.message : t('settings.restoreFailed')
    ui.pushToast({ kind: 'bad', message })
  } finally {
    restoring.value = false
  }
}
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
 * 搜索词。
 *
 * 有搜索词时不看常用/高级：搜一个只出现在高级区的项却搜不到，是最让人困惑
 * 的结果；命中之后也不该再要求用户先切到「显示全部」。
 */
const query = ref('')
const searching = computed(() => query.value.trim() !== '')

/** 恢复默认影响面有多大：项数按声明计，用户理解的就是「多少项」。 */
const settableCount = computed(() =>
  SETTING_GROUPS.reduce((sum, group) => sum + group.fields.length, 0),
)

/** 只有桌面版窗口里才生效的项：面板版跑在浏览器里，没有托盘可收。 */
const DESKTOP_ONLY = new Set(['ui.close_to_tray'])

/** 按路径取声明，用于把「一个控件对应多个键」的项摊开。 */
const FIELD_BY_PATH = new Map(
  SETTING_GROUPS.flatMap((group) => group.fields.map((field) => [field.path, field] as const)),
)

/**
 * 每一组在当前模式下实际会渲染的字段。
 *
 * 简单模式只留标了 primary 的项——一份八十多项的配置表一次全铺开，用户找一项
 * 得先读完七组。一组里一个常用项都没有就不显示这一组，免得点进去是空的。
 */
const groupsWithFields = computed(() =>
  SETTING_GROUPS.map((group) => ({
    ...group,
    fields: group.fields
      // 改了不会起作用的项不显示，比显示出来再注明「仅桌面版有效」诚实。
      .filter((field) => settings.desktop || !DESKTOP_ONLY.has(field.path))
      // 多选里的托盘渠道同理：面板版没有托盘，留着这一格只会让人勾了没反应。
      .map((field) =>
        settings.desktop || field.toggles === undefined
          ? field
          : {
              ...field,
              toggles: Object.fromEntries(
                Object.entries(field.toggles).filter(([option]) => option !== 'tray'),
              ),
            },
      )
      // 互斥项按当前取值只显示生效的那一个。
      .filter((field) => fieldVisible(field, values.value))
      .filter((field) =>
        searching.value
          ? fieldMatches(field, query.value)
          : ui.showAdvanced || field.primary,
      ),
  })).filter((group) => group.fields.length > 0),
)

/** 当前选中的组。切到简单模式后原选中的组可能已经不在列表里，退回第一组。 */
const activeGroupSafe = computed(() =>
  groupsWithFields.value.some((item) => item.id === activeGroup.value)
    ? activeGroup.value
    : (groupsWithFields.value[0]?.id ?? ''),
)

/** 窄屏渲染全部分组，宽屏只渲染当前选中的那一组；搜索时一律全渲染。 */
const visibleGroups = computed(() =>
  narrow.value || searching.value
    ? groupsWithFields.value
    : groupsWithFields.value.filter((item) => item.id === activeGroupSafe.value),
)

/** 搜索命中的项数，显示在输入框旁边。 */
const matchedCount = computed(() =>
  groupsWithFields.value.reduce((sum, group) => sum + group.fields.length, 0),
)

function isOpen(id: string): boolean {
  // 搜索结果散在多个组里，折叠着等于没搜到。
  if (searching.value) return true
  return !narrow.value || openGroups.value.includes(id)
}
const healthChecked = computed(() => settings.health !== null)
const healthIssues = computed(() => settings.health?.issues ?? [])

/**
 * 服务状态默认收起。
 *
 * 设置页是来改设置的；健康检查、归属地库、服务面板、诊断与危险操作都是「出了
 * 事才来找」的东西。摊在同一屏里，八十几项设置还没开始看，先被四张运维卡压住。
 */
const serviceOpen = ref(false)

/** 收起时标题右侧那一行：有事没事得一眼看出来，不能靠展开才知道。 */
const serviceSummary = computed(() => {
  const issues = healthIssues.value.length
  const health = !healthChecked.value
    ? t('settings.serviceUnchecked')
    : issues > 0
      ? t('settings.serviceIssues', { count: issues })
      : t('settings.serviceHealthy')
  const geoCount = geo.status?.status.records ?? 0
  return [
    health,
    t('settings.serviceGeo', { count: geoCount }),
    t('settings.serviceHistory', { count: history.total }),
  ].join(' · ')
})
const confirmingReset = ref(false)
const confirmingClear = ref(false)
const confirmingDir = ref(false)
const dataDirDraft = ref('')

onMounted(() => {
  sendCommand(CMD.settingsGet)
  sendCommand(CMD.geoStatus)
  // 危险区那条「清空历史」要显示条数，所以这里顺带拉一次索引。
  history.refresh()
})

const values = computed<Record<string, unknown>>(
  () => (settings.values ?? {}) as unknown as Record<string, unknown>,
)

const groupLabel = (id: string) => groupText[id] ?? id

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

/**
 * 改一项。
 *
 * 多数项只对应一个键，但合成项（一个控件管几个键）要按选项摊开：节点明细
 * 三档同时写明细与预检两个布尔，提醒方式写三个渠道开关。一次发出去而不是
 * 连发几条——中间那些半截状态会被广播回来，界面上闪一下又跳回去。
 */
function change(path: string, value: unknown): void {
  const field = FIELD_BY_PATH.get(path)
  if (field !== undefined && (field.writes !== undefined || field.toggles !== undefined)) {
    const pairs = fieldPatch(field, value)
    const origins: Record<string, string> = {}
    for (const key of Object.keys(pairs)) origins[key] = 'user'
    sendCommand(CMD.settingsUpdate, { patch: buildPatchMany(pairs), origins })
    // 勾上浏览器通知时顺手申请权限：申请需要一个用户手势，而任务结束时没有手势
    // 可借。用户之前拒绝过就不再问——反复弹窗只会让人把整个站点的通知永久关掉。
    if (field.toggles?.web !== undefined && Array.isArray(value) && value.includes('web')) {
      void requestWebPermission()
    }
    return
  }

  sendCommand(CMD.settingsUpdate, { patch: buildPatch(path, value), origins: { [path]: 'user' } })
}

/** 恢复一项默认；合成项恢复的是它背后那几个键。 */
function resetField(path: string): void {
  const field = FIELD_BY_PATH.get(path)
  if (field === undefined || (field.writes === undefined && field.toggles === undefined)) {
    sendCommand(CMD.settingsReset, { keys: [path] })
    return
  }
  const keys = Object.keys(fieldPatch(field, fieldValue(field, values.value)))
  sendCommand(CMD.settingsReset, { keys })
}

/**
 * 恢复本组默认。
 *
 * 按 members 而不是按 id 发：界面上「网络与来源」是一组，配置里却是 net 与
 * source 两个前缀，只发 id 会漏掉另一半，用户看到「恢复了但还有几项没变」。
 */
function resetGroupOf(group: SettingGroup): void {
  sendCommand(CMD.settingsReset, { keys: group.members ?? [group.id] })
}

/** 恢复推荐设置影响面大，先确认再发。 */
function resetAll(): void {
  sendCommand(CMD.settingsReset, { keys: [] })
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
  sendCommand(CMD.healthCheck)
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

    <div class="search-row">
      <input
        v-model="query"
        type="search"
        class="ct-input search"
        :placeholder="t('settings.search')"
        :aria-label="t('settings.search')"
      />
      <span v-if="searching" class="ct-subtle tnum">
        {{ t('settings.searchCount', { count: matchedCount }) }}
      </span>
      <span class="spacer" />
      <button type="button" class="ct-link" @click="backupSettings">
        {{ t('settings.backup') }}
      </button>
      <button type="button" class="ct-link" :disabled="restoring" @click="pickRestoreFile">
        {{ t('settings.restore') }}
      </button>
      <input
        ref="restoreInput"
        type="file"
        accept="application/json,.json"
        class="sr-only"
        tabindex="-1"
        @change="onRestoreFile"
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

    <nav v-if="!narrow && !searching" class="groups" :aria-label="t('settings.groups')">
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
        <p v-if="searching && matchedCount === 0" class="ct-card search-empty">
          {{ t('settings.searchEmpty', { query: query.trim() }) }}
        </p>
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
            <button type="button" class="ct-link" @click="resetGroupOf(item)">{{ t('common.reset') }}</button>
          </h2>
          <template v-if="isOpen(item.id)">
            <SettingsField
              v-for="field in item.fields"
              :key="field.path"
              :field="field"
              :value="fieldValue(field, values)"
              :values="values"
              :warning="settings.warningOf(field.path)"
              :highlight="query.trim()"
              @change="change"
              @reset="resetField"
            />
          </template>
        </section>
      </div>

    </div>

    <!--
      服务状态：健康检查、归属地库、服务面板、诊断与危险操作。
      默认收起，标题右侧留一行摘要——「有事没事」不展开就能看见。
    -->
    <section v-if="!searching" class="ct-card service">
      <h2 class="ct-card-title">
        <button
          type="button"
          class="ct-link disclosure"
          :aria-expanded="serviceOpen"
          @click="serviceOpen = !serviceOpen"
        >
          {{ serviceOpen ? '▾' : '▸' }} {{ t('settings.serviceState') }}
        </button>
        <span class="spacer" />
        <span class="ct-subtle summary">{{ serviceSummary }}</span>
      </h2>
      <div v-if="serviceOpen" class="service-body">
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
            <button type="button" class="ct-btn" @click="sendCommand(CMD.geoUpdate)">{{ t('geo.update') }}</button>
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

        <ServerPanel />

        <DiagPanel />

        <section class="danger">
          <h2 class="ct-card-title">{{ t('settings.dangerZone') }}</h2>
          <p class="ct-subtle">{{ t('settings.dangerHint') }}</p>

          <!--
            数据目录切换。这条与设置页里那个普通输入框是同一个配置项，区别在于
            这里会先把后果说清楚再让你确认——改了它，历史、缓存、ASN 库就换地方了。
          -->
          <div class="danger-row">
            <!-- 当前值在上面「服务面板」里，这里只说这是干什么的。 -->
            <div class="danger-info">
              <span>{{ t('settings.dataDir') }}</span>
              <span class="ct-subtle">{{ t('settings.dataDirSwitchHint') }}</span>
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
          <p v-if="confirmingReset" class="ct-subtle warn-text">
            {{ t('settings.resetAllConfirm', { count: settableCount }) }}
          </p>
        </section>
      </div>
    </section>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.search-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.search {
  flex: 1 1 200px;
  max-width: 320px;
}

.search-empty {
  margin: 0;
  color: var(--color-text-muted);
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
  /* 一行铺满超宽屏时，标签与输入框隔得太远，扫读反而更慢。 */
  max-width: 920px;
}

.disclosure {
  font-size: var(--font-size-md);
  font-weight: 500;
  color: var(--color-text);
}

.columns {
  display: block;
}

.summary {
  font-size: var(--font-size-xs);
}

/* 展开后四张运维卡按可用宽度两列排开；窄屏自动落成一列。 */
.service-body {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: var(--space-4);
  align-items: start;
  margin-top: var(--space-3);
}

/* 危险区跨满整行：与某张卡并排，会让人以为两者有关。 */
.service-body .danger {
  grid-column: 1 / -1;
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
  padding-top: var(--space-4);
  border-top: 1px solid var(--color-bad-border);
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

</style>
