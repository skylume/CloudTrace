/**
 * 结果集：增量合并扫描与测速的结果，并提供筛选、排序与选择。
 *
 * 合并的键是 `ip:port`：测速结果是同一批节点的更完整版本，按地址覆盖而不是
 * 追加，否则表格里会出现两行同一个节点。
 */
import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

import { CMD } from '@/api/protocol'
import { sendCommand } from '@/api/client'
import { api } from '@/api/rest'
import { UNREACHABLE, type ExportResult, type FieldDef, type IPRecord, type Summary } from '@/api/types'
import { readPath } from '@/i18n/settingsSchema'
import { useSettingsStore } from '@/stores/settings'
import { useUIStore } from '@/stores/ui'
import { pickOutputTarget } from '@/utils/outputTarget'

export type GroupBy = 'none' | 'colo' | 'region' | 'asn'

/** 一个分组的汇总：父行给的是决策信息，不是明细。 */
export interface RecordGroup {
  key: string
  label: string
  records: IPRecord[]
  count: number
  minLatency: number
  avgLatency: number
  bestSpeed: number
}

export type SortKey = 'latency' | 'latency_avg' | 'loss' | 'jitter' | 'score' | 'speed_mbps' | 'region'

const NATURAL_DESC: Record<SortKey, boolean> = {
  latency: false,
  latency_avg: false,
  loss: false,
  jitter: false,
  score: true,
  speed_mbps: true,
  region: false,
}

export function naturalDesc(key: SortKey): boolean {
  return NATURAL_DESC[key]
}

/** recordKey 是结果集里的唯一键。 */
export function recordKey(record: IPRecord): string {
  return `${record.ip}:${record.port}`
}

/** reachable 判断这条记录是否探测成功过。 */
export function reachable(record: IPRecord): boolean {
  return record.recv > 0 && record.latency !== UNREACHABLE
}

/**
 * mergeRecord 用新记录覆盖旧记录。
 *
 * 归属信息（地区、ASN）以「谁有值用谁」为准：测速结果里往往没有地区——
 * 测速只测速度，不会再去取一次 trace——直接用新记录会把扫描阶段拿到的
 * 地区抹掉。
 */
export function mergeRecord(old: IPRecord, incoming: IPRecord): IPRecord {
  return {
    ...old,
    ...incoming,
    colo: incoming.colo || old.colo,
    loc: incoming.loc || old.loc,
    region_name: incoming.region_name || old.region_name,
    asn: incoming.asn || old.asn,
    as_org: incoming.as_org || old.as_org,
    geo_warn: incoming.geo_warn || old.geo_warn,
    trace: incoming.trace ?? old.trace,
  }
}

/**
 * 视图记忆：筛选、排序与分组。
 *
 * 搜索词不记——它是「我现在找这一条」，不是「我平时怎么看这张表」；把搜索词
 * 一起存下来，下次打开结果页会是一张莫名其妙空着的表。
 */
const VIEW_KEY = 'cloudtrace.resultView'

interface StoredView {
  regionFilter: string[]
  maxLatency: number | null
  maxLoss: number | null
  qualifiedOnly: boolean
  sortKey: SortKey
  sortDesc: boolean
  groupBy: GroupBy
}

const SORT_KEYS: SortKey[] = ['latency', 'latency_avg', 'loss', 'jitter', 'score', 'speed_mbps', 'region']
const GROUP_KEYS: GroupBy[] = ['none', 'colo', 'region', 'asn']

/** loadView 只收认得出的值：坏掉的一项回默认，而不是让整张表消失。 */
function loadView(): Partial<StoredView> {
  try {
    const raw = localStorage.getItem(VIEW_KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const out: Partial<StoredView> = {}
    if (Array.isArray(parsed.regionFilter)) {
      out.regionFilter = parsed.regionFilter.filter((item): item is string => typeof item === 'string')
    }
    const optionalNumber = (value: unknown): number | null | undefined => {
      if (value === null) return null
      return typeof value === 'number' && Number.isFinite(value) ? value : undefined
    }
    const maxLatency = optionalNumber(parsed.maxLatency)
    if (maxLatency !== undefined) out.maxLatency = maxLatency
    const maxLoss = optionalNumber(parsed.maxLoss)
    if (maxLoss !== undefined) out.maxLoss = maxLoss
    if (typeof parsed.qualifiedOnly === 'boolean') out.qualifiedOnly = parsed.qualifiedOnly
    if (SORT_KEYS.includes(parsed.sortKey as SortKey)) out.sortKey = parsed.sortKey as SortKey
    if (typeof parsed.sortDesc === 'boolean') out.sortDesc = parsed.sortDesc
    if (GROUP_KEYS.includes(parsed.groupBy as GroupBy)) out.groupBy = parsed.groupBy as GroupBy
    return out
  } catch {
    return {}
  }
}

function saveView(view: StoredView): void {
  try {
    localStorage.setItem(VIEW_KEY, JSON.stringify(view))
  } catch {
    /* 隐私模式下写不进去，忽略即可 */
  }
}

export const useResultsStore = defineStore('results', () => {
  /** 每页行数来自界面配置，所以这里要读另一个 store。 */
  const uiStore = useUIStore()

  /** 用 Map 保存：合并与查找都是按键进行的，数组每次都要遍历一遍。 */
  const records = ref(new Map<string, IPRecord>())
  /** 触发视图更新的版本号。Map 的原地修改不会被响应式系统看到。 */
  const version = ref(0)

  const storedView = loadView()

  const selected = ref(new Set<string>())
  const regionFilter = ref<string[]>(storedView.regionFilter ?? [])
  const maxLatency = ref<number | null>(storedView.maxLatency ?? null)
  /**
   * 丢包上限，与记录里的 loss 同单位（0..1 小数）；null 表示不限。
   *
   * 不做成配置项：丢包是「这一批结果里我想排除哪些」的临时判断，写进配置会
   * 让下次扫描的候选集也跟着变，那是另一件事。
   */
  const maxLoss = ref<number | null>(storedView.maxLoss ?? null)
  /**
   * 只看测速达标的节点。
   *
   * 合格线取自配置里的 `speed.min_speed`，不在前端写死：用户调过合格线之后，
   * 这里筛出来的必须与后端「合格数」的口径一致，否则同一个数字在两处对不上。
   */
  const qualifiedOnly = ref(storedView.qualifiedOnly ?? false)
  const sortKey = ref<SortKey>(storedView.sortKey ?? 'loss')
  const sortDesc = ref(storedView.sortDesc ?? false)
  const groupBy = ref<GroupBy>(storedView.groupBy ?? 'none')
  /**
   * 当前这批结果的来源历史 ID；为空表示「最新一份」。
   *
   * 导出必须知道这个：结果页可以显示从历史加载的那一份，此时「导出最新」会
   * 导出一份完全不同的数据，而用户看不出区别。
   */
  const sourceId = ref('')
  /** 关键词：匹配地址、地区、运营商。Ctrl+F 聚焦到它的输入框。 */
  const keyword = ref('')

  function touch(): void {
    version.value += 1
  }

  /**
   * nearest 是零结果时后端补发的「最接近阈值」那一批。
   *
   * 与结果集分开存：它不是结果，只是判断条件是否太严的依据，混进表格会
   * 让「扫出 0 条」变成「扫出 20 条不达标的」。
   */
  const nearest = ref<{ threshold: number; items: IPRecord[] } | null>(null)

  function applyNearest(payload: { threshold: number; items: IPRecord[] } | null): void {
    nearest.value = payload && payload.items.length > 0 ? payload : null
  }

  function addChunk(chunk: IPRecord[]): void {
    if (chunk.length === 0) return
    // 有真结果进来，回看那批就过期了。
    nearest.value = null
    const next = records.value
    for (const record of chunk) {
      const key = recordKey(record)
      const old = next.get(key)
      next.set(key, old ? mergeRecord(old, record) : record)
    }
    touch()
  }

  function replaceAll(list: IPRecord[], fromHistoryID = ''): void {
    sourceId.value = fromHistoryID
    const next = new Map<string, IPRecord>()
    for (const record of list) next.set(recordKey(record), record)
    records.value = next
    selected.value = new Set()
    touch()
  }

  function clear(): void {
    keyword.value = ''
    sourceId.value = ''
    nearest.value = null
    records.value = new Map()
    selected.value = new Set()
    regionFilter.value = []
    maxLatency.value = null
    touch()
  }

  /**
   * 清空当前结果，但保留筛选条件。
   *
   * 与 `clear()` 的区别：那个是「换一批数据」时的整体重置，连筛选一起清掉；
   * 这个是用户主动丢掉眼前这批，而筛选是他的意图，不该跟着一起没。
   *
   * 快照留下来供撤销——结果只活在内存里，清掉就真的没了，而清空又是个
   * 一眼看不出后果的动作（表格没了、统计卡归零），值得给一次后悔的机会。
   */
  const cleared = ref<{ records: Map<string, IPRecord>; sourceId: string } | null>(null)

  /** snapshot 取当前结果的一份快照，供会话恢复用。 */
  function snapshot(): { records: IPRecord[]; sourceId: string } {
    return { records: all.value, sourceId: sourceId.value }
  }

  function clearRecords(): number {
    const count = records.value.size
    if (count === 0) return 0
    cleared.value = { records: records.value, sourceId: sourceId.value }
    records.value = new Map()
    selected.value = new Set()
    touch()
    return count
  }

  function restoreCleared(): void {
    const snapshot = cleared.value
    if (!snapshot) return
    records.value = snapshot.records
    sourceId.value = snapshot.sourceId
    cleared.value = null
    touch()
  }

  const all = computed<IPRecord[]>(() => {
    void version.value
    return [...records.value.values()]
  })

  const total = computed(() => all.value.length)

  /** 地区分布，按数量倒序。地区芯片用它。 */
  const regionCounts = computed(() => {
    const counts = new Map<string, number>()
    for (const record of all.value) {
      const code = (record.colo || record.region_name || '').toUpperCase()
      if (!code) continue
      counts.set(code, (counts.get(code) ?? 0) + 1)
    }
    return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
  })

  /** visible 是表格实际渲染的那一批：筛选 + 排序。 */
  const visible = computed<IPRecord[]>(() => {
    let list = all.value

    if (regionFilter.value.length > 0) {
      const wanted = new Set(regionFilter.value.map((code) => code.toUpperCase()))
      list = list.filter((record) => {
        const code = (record.colo || record.region_name || '').toUpperCase()
        // 地区未知的保守保留：误杀比多显示一行代价大得多。
        return code === '' || wanted.has(code)
      })
    }

    const text = keyword.value.trim().toLowerCase()
    if (text !== '') {
      list = list.filter((record) => {
        const haystack = [record.ip, record.region_name, record.colo, record.as_org, String(record.asn ?? '')]
          .join(' ')
          .toLowerCase()
        return haystack.includes(text)
      })
    }

    if (maxLatency.value !== null) {
      const limit = maxLatency.value
      list = list.filter((record) => !reachable(record) || record.latency <= limit)
    }

    if (maxLoss.value !== null) {
      const limit = maxLoss.value
      // 不可达的记录不按丢包筛掉：它已经由「仅显示达标」与延迟上限负责，
      // 这里再筛一遍只会让「丢包 100%」这个解释消失。
      list = list.filter((record) => !reachable(record) || record.loss <= limit)
    }

    if (qualifiedOnly.value) {
      const floor = minSpeed.value
      // 0 表示「没测出来」，不算达标——与后端的合格判定同一条规则。
      list = list.filter((record) => record.speed_mbps > 0 && record.speed_mbps >= floor)
    }

    return sortRecords(list, sortKey.value, sortDesc.value)
  })

  /** 合格线（MB/s）。配置还没到之前按 0 处理，此时「仅显示达标」等价于「只看测过的」。 */
  const minSpeed = computed(() => {
    const values = useSettingsStore().values
    if (!values) return 0
    const value = readPath(values as unknown as Record<string, unknown>, 'speed.min_speed')
    return typeof value === 'number' && value > 0 ? value : 0
  })

  // ---- 分页 ----
  //
  // 扫描的典型结果是几十到几百条，一次全渲染出来会明显卡。分页放在这里而不是
  // 组件里：`visible` 是本 store 算出来的，页码必须和它同源——放在组件里就要
  // 把整份列表传进去再切，等于把排序结果白算一遍。
  const pageSize = computed(() => uiStore.pageSize)

  /** page 是当前页码，从 1 开始。 */
  const page = ref(1)

  /** pageCount 至少是 1：没有结果时也该显示「第 1 / 1 页」，而不是第 0 页。 */
  const pageCount = computed(() => Math.max(1, Math.ceil(visible.value.length / pageSize.value)))

  /**
   * currentPage 是把页码夹进合法范围之后的结果。
   *
   * 不直接改 page：筛选条件一变，visible 立刻变短，而 page 可能还停在很后面。
   * 在计算里夹住，界面就不会出现「第 7 / 2 页」这种自相矛盾的状态。
   */
  const currentPage = computed(() => Math.min(Math.max(1, page.value), pageCount.value))

  /** paged 是表格与卡片列表实际渲染的那一页。 */
  const paged = computed(() => {
    const size = pageSize.value
    const start = (currentPage.value - 1) * size
    return visible.value.slice(start, start + size)
  })

  function setPage(next: number): void {
    page.value = Math.min(Math.max(1, next), pageCount.value)
  }

  /**
   * 筛选、排序、关键词一变就回到第一页。
   *
   * 不回的话，用户停在第 7 页改一下筛选，看到的就是一张空表——而结果其实有，
   * 只是都在前几页。
   */
  watch([regionFilter, maxLatency, maxLoss, keyword, sortKey, sortDesc, groupBy, pageSize], () => {
    page.value = 1
  })

  // 视图一变就记下来。放 watch 而不是每个 setter 里：这些状态有十来个入口，
  // 漏一个就会出现「记得住筛选、记不住排序」的半吊子行为。
  watch(
    [regionFilter, maxLatency, maxLoss, qualifiedOnly, sortKey, sortDesc, groupBy],
    () => {
      saveView({
        regionFilter: regionFilter.value,
        maxLatency: maxLatency.value,
        maxLoss: maxLoss.value,
        qualifiedOnly: qualifiedOnly.value,
        sortKey: sortKey.value,
        sortDesc: sortDesc.value,
        groupBy: groupBy.value,
      })
    },
    { deep: true },
  )

  /**
   * 还原默认视图：筛选、排序、分组、列与列宽一起回到出厂状态。
   *
   * 有记忆就必须有出口——用户把视图调乱之后，没有任何按钮能回去，只能清
   * 浏览器数据，那是把「记住」做成了陷阱。
   */
  function resetView(): void {
    regionFilter.value = []
    maxLatency.value = null
    maxLoss.value = null
    qualifiedOnly.value = false
    sortKey.value = 'loss'
    sortDesc.value = false
    groupBy.value = 'none'
    keyword.value = ''
    try {
      localStorage.removeItem(VIEW_KEY)
    } catch {
      /* 同上 */
    }
    const fields = useFieldsStore()
    fields.applyPreset(DEFAULT_PRESET)
    fields.resetWidths()
  }

  /**
   * stats 从结果集现算。
   *
   * 不直接用后端摘要：扫描过程中结果是一条条推过来的，用现算的统计才能跟着
   * 实时变化；后端摘要是任务跑完才算得出来的。
   */
  const stats = computed<Summary>(() => summarize(all.value))

  /**
   * 分组结果。
   *
   * 组按最低延迟升序排——父行回答的是「这个数据中心值不值得测」，而延迟是
   * 那个问题的答案。组内保持当前排序，切分组不改变用户已经选好的顺序。
   */
  const groups = computed<RecordGroup[]>(() => {
    if (groupBy.value === 'none') return []

    const buckets = new Map<string, IPRecord[]>()
    for (const record of visible.value) {
      const { key } = groupKeyOf(record, groupBy.value)
      const list = buckets.get(key)
      if (list) list.push(record)
      else buckets.set(key, [record])
    }

    const out: RecordGroup[] = []
    for (const [key, list] of buckets) {
      out.push({ key, label: groupKeyOf(list[0]!, groupBy.value).label, records: list, ...summarizeGroup(list) })
    }
    return out.sort((a, b) => a.minLatency - b.minLatency || a.key.localeCompare(b.key))
  })

  function toggleSelect(key: string): void {
    const next = new Set(selected.value)
    if (next.has(key)) next.delete(key)
    else next.add(key)
    selected.value = next
  }

  /**
   * 表头那个复选框选的是**本页**，不是全部筛选结果。
   *
   * 分页之后「全选」如果指全部，用户会选中一堆他看不见的行——接着点「导出」
   * 或「测速」，拿到的东西和他以为选中的完全不是一回事。
   */
  function selectAllOnPage(): void {
    selected.value = new Set(paged.value.map(recordKey))
  }

  function clearSelection(): void {
    selected.value = new Set()
  }

  const selectedRecords = computed<IPRecord[]>(() => {
    void version.value
    const out: IPRecord[] = []
    for (const key of selected.value) {
      const record = records.value.get(key)
      if (record) out.push(record)
    }
    return out
  })

  return {
    all,
    visible,
    paged,
    snapshot,
    clearRecords,
    restoreCleared,
    page,
    pageCount,
    currentPage,
    setPage,
    groups,
    groupBy,
    keyword,
    sourceId,
    total,
    stats,
    regionCounts,
    selected,
    selectedRecords,
    regionFilter,
    maxLatency,
    maxLoss,
    qualifiedOnly,
    sortKey,
    sortDesc,
    resetView,
    addChunk,
    nearest,
    applyNearest,
    replaceAll,
    clear,
    toggleSelect,
    selectAllOnPage,
    clearSelection,
  }
})

/**
 * sortRecords 按维度排序。
 *
 * 两条与后端一致的规则：没有该项数据的记录**恒排最后**（不可达的延迟是
 * 哨兵值 -1、没测过速的速度是 0，升序时都会冒充「最快」）；主维度相同的
 * 按延迟、再按地址兜底，保证同一份数据每次排序结果一致。
 */
export function sortRecords(list: IPRecord[], key: SortKey, desc: boolean): IPRecord[] {
  const out = [...list]
  out.sort((a, b) => {
    const aMissing = missingValue(a, key)
    const bMissing = missingValue(b, key)
    if (aMissing !== bMissing) return aMissing ? 1 : -1
    if (aMissing && bMissing) return compareKey(a, b)

    const diff = compareValue(a, b, key)
    if (diff !== 0) return desc ? -diff : diff
    return compareKey(a, b)
  })
  return out
}

function missingValue(record: IPRecord, key: SortKey): boolean {
  switch (key) {
    case 'speed_mbps':
      return record.speed_mbps <= 0
    case 'score':
      return record.score === 0
    case 'region':
      return regionOf(record) === ''
    default:
      return !reachable(record)
  }
}

function compareValue(a: IPRecord, b: IPRecord, key: SortKey): number {
  switch (key) {
    case 'latency':
      return a.latency - b.latency
    case 'latency_avg':
      return a.latency_avg - b.latency_avg
    case 'loss':
      return a.loss - b.loss
    case 'jitter':
      return a.jitter - b.jitter
    case 'score':
      return a.score - b.score
    case 'speed_mbps':
      return a.speed_mbps - b.speed_mbps
    case 'region':
      return regionOf(a).localeCompare(regionOf(b))
    default:
      return 0
  }
}

/** regionOf 与后端一致：优先用数据中心代码，它是 ASCII，顺序稳定且能聚在一起。 */
function regionOf(record: IPRecord): string {
  const colo = (record.colo || '').trim().toUpperCase()
  if (colo) return colo
  return (record.region_name || '').trim()
}

function compareKey(a: IPRecord, b: IPRecord): number {
  if (reachable(a) && reachable(b) && a.latency !== b.latency) return a.latency - b.latency
  const byIP = compareIP(a.ip, b.ip)
  if (byIP !== 0) return byIP
  return a.port - b.port
}

/** compareIP 按数值序比较，避免 1.1.1.10 排在 1.1.1.2 前面。 */
function compareIP(a: string, b: string): number {
  const na = toNumber(a)
  const nb = toNumber(b)
  if (na !== null && nb !== null) return na - nb
  return a.localeCompare(b)
}

function toNumber(ip: string): number | null {
  const parts = ip.split('.')
  if (parts.length !== 4) return null
  let value = 0
  for (const part of parts) {
    const n = Number(part)
    if (!Number.isInteger(n) || n < 0 || n > 255) return null
    value = value * 256 + n
  }
  return value
}

/** 取一条记录的分组键与显示名。 */
function groupKeyOf(record: IPRecord, by: GroupBy): { key: string; label: string } {
  switch (by) {
    case 'colo':
      return { key: (record.colo || 'unknown').toUpperCase(), label: record.region_name || record.colo || '—' }
    case 'region':
      return { key: record.region_name || record.colo || 'unknown', label: record.region_name || record.colo || '—' }
    default:
      return { key: String(record.asn || 0), label: record.as_org || (record.asn ? `AS${record.asn}` : '—') }
  }
}

/** 组内汇总。口径与整体统计一致：不可达与未测速不参与平均。 */
function summarizeGroup(list: IPRecord[]): Omit<RecordGroup, 'key' | 'label' | 'records'> {
  let minLatency = UNREACHABLE
  let latencySum = 0
  let latencyCount = 0
  let bestSpeed = 0

  for (const record of list) {
    if (reachable(record)) {
      if (latencyCount === 0 || record.latency < minLatency) minLatency = record.latency
      latencySum += record.latency_avg
      latencyCount += 1
    }
    if (record.speed_mbps > bestSpeed) bestSpeed = record.speed_mbps
  }

  return {
    count: list.length,
    minLatency,
    avgLatency: latencyCount > 0 ? latencySum / latencyCount : UNREACHABLE,
    bestSpeed,
  }
}

/** summarize 与后端 Summary 的口径一致：不可达与未测速都不参与平均。 */
export function summarize(list: IPRecord[]): Summary {
  const regionDist: Record<string, number> = {}
  const asnDist: Record<string, number> = {}
  let minLatency = 0
  let latencySum = 0
  let latencyCount = 0
  let bestSpeed = 0
  let speedSum = 0
  let speedCount = 0
  let qualified = 0

  for (const record of list) {
    if (record.colo) regionDist[record.colo] = (regionDist[record.colo] ?? 0) + 1
    if (record.as_org) asnDist[record.as_org] = (asnDist[record.as_org] ?? 0) + 1

    if (reachable(record)) {
      if (latencyCount === 0 || record.latency < minLatency) minLatency = record.latency
      latencySum += record.latency_avg
      latencyCount += 1
    }
    if (record.speed_mbps > 0) {
      if (record.speed_mbps > bestSpeed) bestSpeed = record.speed_mbps
      speedSum += record.speed_mbps
      speedCount += 1
      qualified += 1
    }
  }

  return {
    funnel: { generated: list.length, latency_ok: latencyCount, region_ok: latencyCount, usable: latencyCount },
    region_dist: regionDist,
    asn_dist: asnDist,
    min_latency: latencyCount > 0 ? minLatency : UNREACHABLE,
    avg_latency: latencyCount > 0 ? latencySum / latencyCount : 0,
    best_speed: bestSpeed,
    avg_speed: speedCount > 0 ? speedSum / speedCount : 0,
    qualified,
    total: list.length,
  }
}

/** 列预设：三档，从「只想挑一个能用就走」到「排查线路问题」。 */
export type ColumnPresetId = 'brief' | 'standard' | 'full'

export interface ColumnPreset {
  id: ColumnPresetId
  /** 预设名走 i18n，这里只存键。 */
  labelKey: string
  keys: string[]
}

/**
 * 三档列预设。
 *
 * `sent` / `recv` / `loc` / `trace` 不进预设：前三个是过程量，`trace` 体量大，
 * 它们放进行内展开更合适——列宽是稀缺资源，展开是免费的。
 */
export const COLUMN_PRESETS: ColumnPreset[] = [
  {
    id: 'brief',
    labelKey: 'columns.brief',
    keys: ['ip', 'region_name', 'latency', 'loss', 'speed_mbps', 'score'],
  },
  {
    id: 'standard',
    labelKey: 'columns.standard',
    keys: ['ip', 'region_name', 'colo', 'latency', 'latency_avg', 'jitter', 'loss', 'use_tls', 'speed_mbps', 'score'],
  },
  {
    id: 'full',
    labelKey: 'columns.full',
    keys: [
      'ip',
      'region_name',
      'colo',
      'latency',
      'latency_avg',
      'latency_max',
      'jitter',
      'loss',
      'use_tls',
      'speed_mbps',
      'score',
      'asn',
      'as_org',
      'geo_warn',
    ],
  },
]

/** 列设置里可以勾选的键：预设里出现过的都算，其余靠「全部字段」补齐。 */
export const DEFAULT_PRESET: ColumnPresetId = 'standard'

/** 列宽的本地存储键。与界面偏好分开存：它是表格自己的事。 */
const WIDTHS_KEY = 'cloudtrace.columnWidths'

/** 可见列与预设的本地存储键。 */
const COLUMNS_KEY = 'cloudtrace.columns'

/** 列宽下限。再窄就看不出这一列是什么了。 */
const MIN_COLUMN_WIDTH = 56

/**
 * 读回用户拖过的列宽。
 *
 * 只接受认得出的数字：这份记忆只是省一次拖动，里面混进一个坏值就让整张表
 * 排版错乱的话，代价远大于收益。
 */
function loadWidths(): Record<string, number> {
  try {
    const raw = localStorage.getItem(WIDTHS_KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const out: Record<string, number> = {}
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value === 'number' && Number.isFinite(value) && value >= MIN_COLUMN_WIDTH) {
        out[key] = value
      }
    }
    return out
  } catch {
    return {}
  }
}

/** 读回用户选过的列与档位。 */
function loadColumns(): { presetId: ColumnPresetId | 'custom'; visibleKeys: string[] } {
  const fallback = { presetId: DEFAULT_PRESET as ColumnPresetId | 'custom', visibleKeys: presetKeys(DEFAULT_PRESET) }
  try {
    const raw = localStorage.getItem(COLUMNS_KEY)
    if (!raw) return fallback
    const parsed = JSON.parse(raw) as { presetId?: unknown; visibleKeys?: unknown }
    const keys = Array.isArray(parsed.visibleKeys)
      ? parsed.visibleKeys.filter((item): item is string => typeof item === 'string')
      : []
    // 一列都不剩说明这份记忆坏了：空表格比「回到默认」更难解释。
    if (keys.length === 0) return fallback
    const preset = parsed.presetId
    return {
      presetId: preset === 'brief' || preset === 'standard' || preset === 'full' || preset === 'custom' ? preset : 'custom',
      visibleKeys: keys,
    }
  } catch {
    return fallback
  }
}

function saveColumns(presetId: ColumnPresetId | 'custom', visibleKeys: string[]): void {
  try {
    localStorage.setItem(COLUMNS_KEY, JSON.stringify({ presetId, visibleKeys }))
  } catch {
    /* 隐私模式下写不进去，忽略即可 */
  }
}

function presetKeys(id: ColumnPresetId): string[] {
  return COLUMN_PRESETS.find((preset) => preset.id === id)?.keys ?? []
}

export const useFieldsStore = defineStore('fields', () => {
  const fields = ref<FieldDef[]>([])
  const loading = ref(false)
  const error = ref('')

  const stored = loadColumns()

  /** 当前生效的可见列（按显示顺序）。 */
  const visibleKeys = ref<string[]>(stored.visibleKeys)
  const presetId = ref<ColumnPresetId | 'custom'>(stored.presetId)

  /**
   * 用户拖出来的列宽，按字段键存。
   *
   * 没记录过的列返回空字符串，交给浏览器按内容撑开——给十九个字段各写一个
   * 拍脑袋的默认值，只会让「默认就不好看」变成常态。
   */
  const widths = ref<Record<string, number>>(loadWidths())

  function widthOf(key: string): string {
    const px = widths.value[key]
    return px ? `${px}px` : ''
  }

  function setWidth(key: string, px: number): void {
    widths.value = { ...widths.value, [key]: Math.max(MIN_COLUMN_WIDTH, Math.round(px)) }
    try {
      localStorage.setItem(WIDTHS_KEY, JSON.stringify(widths.value))
    } catch {
      /* 隐私模式下写不进去，忽略即可 */
    }
  }

  /** 清掉全部自定义列宽，回到「按内容撑开」。 */
  function resetWidths(): void {
    widths.value = {}
    try {
      localStorage.removeItem(WIDTHS_KEY)
    } catch {
      /* 同上 */
    }
  }

  /** 按 key 快速取字段定义，渲染时用。 */
  const byKey = computed(() => {
    const map = new Map<string, FieldDef>()
    for (const field of fields.value) map.set(field.key, field)
    return map
  })

  /** 可见列对应的字段定义。后端没下发某个键时跳过，不让表格崩掉。 */
  const columns = computed(() =>
    visibleKeys.value.map((key) => byKey.value.get(key)).filter((field): field is FieldDef => Boolean(field)),
  )

  async function load(): Promise<void> {
    if (fields.value.length > 0 || loading.value) return
    loading.value = true
    try {
      const data = await api.exportFields()
      fields.value = data.fields
      error.value = ''
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
    } finally {
      loading.value = false
    }
  }

  function applyPreset(id: ColumnPresetId): void {
    presetId.value = id
    visibleKeys.value = presetKeys(id)
    saveColumns(presetId.value, visibleKeys.value)
  }

  /** 手动改列：与任何预设都不一致时记为「自定义」。 */
  function setVisibleKeys(keys: string[]): void {
    visibleKeys.value = keys
    const matched = COLUMN_PRESETS.find(
      (preset) => preset.keys.length === keys.length && preset.keys.every((key) => keys.includes(key)),
    )
    presetId.value = matched ? matched.id : 'custom'
    saveColumns(presetId.value, keys)
  }

  function toggleKey(key: string): void {
    const next = visibleKeys.value.includes(key)
      ? visibleKeys.value.filter((item) => item !== key)
      : [...visibleKeys.value, key]
    // 一列都不剩时不给通过：空表格比缺一列更难理解。
    if (next.length === 0) return
    setVisibleKeys(next)
  }

  /**
   * 把某一列在可见顺序里前后挪一格。
   *
   * 用「上移 / 下移」而不是拖拽：列数最多十几个，两个按钮就能到任何位置，
   * 而且键盘也能操作——拖拽排序在键盘上等于没有。
   */
  function moveKey(key: string, delta: number): void {
    const keys = [...visibleKeys.value]
    const from = keys.indexOf(key)
    if (from < 0) return
    const to = from + delta
    if (to < 0 || to >= keys.length) return
    keys.splice(to, 0, ...keys.splice(from, 1))
    setVisibleKeys(keys)
  }

  return {
    fields,
    loading,
    error,
    byKey,
    columns,
    visibleKeys,
    presetId,
    widths,
    widthOf,
    setWidth,
    resetWidths,
    load,
    applyPreset,
    setVisibleKeys,
    toggleKey,
    moveKey,
  }
})

export interface BreakerNotice {
  message: string
  /** 当前配置的测速并发，「降低并发」以它为起点。 */
  concurrency: number
  /** 当前的测速源模式。 */
  urlMode: string
}

export interface SourceDecision {
  url: string
  /** 原因标识，与后端 speed.Reason* 常量一致。 */
  code: string
  detail?: string
  mode: string
}

export const useSpeedStore = defineStore('speed', () => {
  const breaker = ref<BreakerNotice | null>(null)
  /** 最近一次自动选源的结果与理由。 */
  const source = ref<SourceDecision | null>(null)

  function applyBreaker(notice: BreakerNotice): void {
    breaker.value = notice
  }

  function dismissBreaker(): void {
    breaker.value = null
  }

  function applySource(decision: SourceDecision): void {
    source.value = decision
  }

  /**
   * 建议的并发值。
   *
   * 砍半而不是给一个固定数字：用户原本设成 4 或 16 时，「降到 4」这种建议
   * 要么没变、要么跨度太离谱。砍半在两种情况下都合理，且下限保底为 1。
   */
  function suggestedConcurrency(): number {
    const current = breaker.value?.concurrency ?? 0
    return Math.max(1, Math.floor(current / 2))
  }

  return { breaker, source, applyBreaker, dismissBreaker, applySource, suggestedConcurrency }
})

export const useExportStore = defineStore('export', () => {
  /** 正在等待后端生成，用于禁用按钮避免重复点击。 */
  const pending = ref(false)

  /**
   * 发起一次导出。
   *
   * fields 为空表示用后端的默认字段集——前端不猜，字段清单本来就是后端给的。
   */
  function request(options: { fields?: string[]; format?: string; id?: string; type?: string }): boolean {
    pending.value = true
    const ok = sendCommand(CMD.export, {
      // 留空表示「最新一份」，优先测速结果——这正是结果页展示的东西。
      type: options.type ?? '',
      // 指定某一份历史时按 ID 导出，此时 type 不参与。
      id: options.id ?? '',
      fields: options.fields ?? [],
      format: options.format ?? '',
    })
    if (!ok) pending.value = false
    return ok
  }

  /**
   * 收到后端返回的下载地址后立刻触发下载。
   *
   * 用 a 标签而不是 fetch：后端已经给了文件名与 Content-Disposition，走浏览器
   * 原生下载能保留这些信息，也不会把文件内容读进内存。
   */
  function applyResult(result: ExportResult): void {
    pending.value = false
    if (!result.url) return
    // 具体怎么交给用户由输出目标决定：桌面是下载，Android 壳可以是系统分享。
    const file = { name: result.name, url: result.url }
    pickOutputTarget(file).save(file)
  }

  /** 后端报错时也要解除等待，否则按钮会一直转下去。 */
  function fail(): void {
    pending.value = false
  }

  return { pending, request, applyResult, fail }
})

