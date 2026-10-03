/**
 * 结果集：增量合并扫描与测速的结果，并提供筛选、排序与选择。
 *
 * 合并的键是 `ip:port`：测速结果是同一批节点的更完整版本，按地址覆盖而不是
 * 追加，否则表格里会出现两行同一个节点。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { UNREACHABLE, type IPRecord, type Summary } from '@/api/types'

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

/** 排序维度与后端的取值一致，方向取该维度的自然方向。 */
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

export const useResultsStore = defineStore('results', () => {
  /** 用 Map 保存：合并与查找都是按键进行的，数组每次都要遍历一遍。 */
  const records = ref(new Map<string, IPRecord>())
  /** 触发视图更新的版本号。Map 的原地修改不会被响应式系统看到。 */
  const version = ref(0)

  const selected = ref(new Set<string>())
  const regionFilter = ref<string[]>([])
  const maxLatency = ref<number | null>(null)
  const sortKey = ref<SortKey>('loss')
  const sortDesc = ref(false)
  const groupBy = ref<GroupBy>('none')
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

  function addChunk(chunk: IPRecord[]): void {
    if (chunk.length === 0) return
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
    records.value = new Map()
    selected.value = new Set()
    regionFilter.value = []
    maxLatency.value = null
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

    return sortRecords(list, sortKey.value, sortDesc.value)
  })

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

  function selectAllVisible(): void {
    selected.value = new Set(visible.value.map(recordKey))
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
    sortKey,
    sortDesc,
    addChunk,
    replaceAll,
    clear,
    toggleSelect,
    selectAllVisible,
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
