/**
 * 任务链路端到端：WS 报文 → store → 界面。
 *
 * 只测一条真实的链路，不做组件级替身：假的 WebSocket 把后端报文原样投进来，
 * 断言落在 store 与真实渲染出来的 DOM 上。这样「字段名对不上」「事件接了但没
 * 人消费」这类跨层问题才会暴露——单测各层都绿、连起来却不动的坑，只有在这里
 * 才看得见。
 *
 * 断线重连必须靠假定时器推进，绝不真等：退避间隔本身就是要验证的行为。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { IPRecord, TaskState } from '@/api/types'

type Listener = ((event: unknown) => void) | null

/** 与 ws.spec.ts 同款替身：记录发出去的报文，允许测试手动驱动开 / 关 / 消息。 */
class FakeSocket {
  static instances: FakeSocket[] = []
  static readonly OPEN = 1
  static readonly CONNECTING = 0
  static readonly CLOSED = 3

  readyState = FakeSocket.CONNECTING
  sent: string[] = []
  onopen: Listener = null
  onclose: Listener = null
  onerror: Listener = null
  onmessage: Listener = null

  constructor(readonly url: string) {
    FakeSocket.instances.push(this)
  }

  send(payload: string): void {
    this.sent.push(payload)
  }

  close(): void {
    this.readyState = FakeSocket.CLOSED
    this.onclose?.({})
  }

  open(): void {
    this.readyState = FakeSocket.OPEN
    this.onopen?.({})
  }

  message(payload: unknown): void {
    this.onmessage?.({ data: JSON.stringify(payload) })
  }

  drop(): void {
    this.readyState = FakeSocket.CLOSED
    this.onclose?.({})
  }
}

const sockets = (): FakeSocket[] => FakeSocket.instances
const last = (): FakeSocket => {
  const socket = FakeSocket.instances.at(-1)
  if (!socket) throw new Error('还没有建立过连接')
  return socket
}

/** 后端字段清单（真实来源是 GET /api/export/fields，这里给出稳定的一份）。 */
const FIELDS = [
  { key: 'ip', label: 'IP' },
  { key: 'region_name', label: '地区' },
  { key: 'colo', label: '机房' },
  { key: 'latency', label: '延迟' },
  { key: 'latency_avg', label: '平均延迟' },
  { key: 'jitter', label: '抖动' },
  { key: 'loss', label: '丢包' },
  { key: 'use_tls', label: 'TLS' },
  { key: 'speed_mbps', label: '速度' },
  { key: 'score', label: '评分' },
]

const RECORD: IPRecord = {
  ip: '1.1.1.1',
  port: 443,
  use_tls: true,
  latency: 12.3,
  latency_avg: 14.5,
  latency_max: 20,
  jitter: 1.2,
  loss: 0,
  sent: 20,
  recv: 20,
  colo: 'LAX',
  loc: 'US',
  region_name: '洛杉矶',
  speed_mbps: 0,
  score: 0,
}

function stateFrame(over: Partial<TaskState> = {}): TaskState {
  return {
    phase: 'scan',
    status: 'running',
    done: 0,
    total: 0,
    elapsed_s: 0,
    eta_s: 0,
    funnel: { generated: 0, latency_ok: 0, region_ok: 0, usable: 0 },
    started_at: 0,
    ...over,
  }
}

const sentTypes = (socket: FakeSocket): string[] =>
  socket.sent.map((payload) => (JSON.parse(payload) as { type: string }).type)

let active: { wrapper: { unmount: () => void }; ws: { close: () => void } } | null = null

/**
 * 起一套最小可用的界面：顶栏（任务状态区）+ 结果页 + 提示条。
 *
 * 不挂整个 App：那会连带扫描页、命令面板与全局快捷键，测任务链路时全是噪声。
 * 但每个用例都要 vi.resetModules()——WSClient 是单例，复用模块会让上一个用例
 * 的订阅者继续收事件，断言串场。
 */
async function boot() {
  vi.resetModules()

  const vue = await import('vue')
  const { createPinia, setActivePinia } = await import('pinia')
  const { mount } = await import('@vue/test-utils')
  const client = await import('@/api/client')
  const bridge = await import('@/stores/bridge')
  const { useTaskStore } = await import('@/stores/conn')
  const { useResultsStore } = await import('@/stores/results')
  const { useUIStore } = await import('@/stores/ui')
  const { default: TopBar } = await import('@/components/layout/TopBar.vue')
  const { default: ResultView } = await import('@/views/ResultView.vue')
  const { default: ToastStack } = await import('@/components/ui/ToastStack.vue')

  const pinia = createPinia()
  setActivePinia(pinia)

  bridge.wireEvents()
  client.wsClient().connect()
  last().open()

  const Root = vue.defineComponent({
    setup() {
      return () =>
        vue.h('div', [
          vue.h(TopBar, { title: '结果', narrow: false, collapsed: false }),
          vue.h(ResultView),
          vue.h(ToastStack),
        ])
    },
  })
  const wrapper = mount(Root, { global: { plugins: [pinia] } })

  const flush = async (): Promise<void> => {
    await vue.nextTick()
    await vi.advanceTimersByTimeAsync(0)
  }
  await flush()

  active = { wrapper, ws: client.wsClient() }

  return {
    vue,
    wrapper,
    flush,
    task: useTaskStore(),
    results: useResultsStore(),
    ui: useUIStore(),
  }
}

beforeEach(() => {
  FakeSocket.instances = []
  localStorage.clear()
  vi.useFakeTimers()
  vi.stubGlobal('WebSocket', FakeSocket as unknown as typeof WebSocket)
  // 结果页挂载时会拉字段清单；除它以外的请求都算意外，直接抛错暴露。
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/export/fields') {
        return {
          ok: true,
          json: async () => ({ fields: FIELDS, formats: [] }),
        }
      }
      throw new Error(`测试不该请求 ${url}`)
    }),
  )
})

afterEach(() => {
  active?.wrapper.unmount()
  active?.ws.close()
  active = null
  localStorage.clear()
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

// 首个用例要冷启动 20 多个模块（组件 + store），并行跑全量时 5 秒默认超时不够。
describe('任务结果端到端', { timeout: 20_000 }, () => {
  it('state 帧把阶段与进度渲染到任务状态区', async () => {
    const { wrapper, flush, task } = await boot()

    last().message({ type: 'state', data: stateFrame({ done: 120, total: 500, eta_s: 40 }) })
    await flush()

    expect(task.running).toBe(true)
    expect(task.phase).toBe('scan')
    expect(task.percent).toBe(24)
    expect(wrapper.text()).toContain('扫描')
    expect(wrapper.text()).toContain('120 / 500')
    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('24')
    // 没有任何结局事件时，不出现空的告警容器。
    expect(wrapper.find('.warn-box').exists()).toBe(false)
  })

  it('progress 帧直接驱动进度条，不必等下一次 state 帧', async () => {
    const { wrapper, flush, task } = await boot()

    last().message({ type: 'state', data: stateFrame({ done: 120, total: 500, eta_s: 40 }) })
    await flush()

    last().message({
      type: 'progress',
      data: {
        phase: 'scan',
        done: 250,
        total: 500,
        eta: 40,
        elapsed: 12.5,
        funnel: stateFrame().funnel,
      },
    })
    await flush()

    // 运行期间后端只发 progress、不重发 state：界面必须当场跟上，
    // 否则进度条整场停在 0，结束时直接跳满。
    expect(task.progress).toMatchObject({ done: 250, total: 500, eta: 40, elapsed: 12.5 })
    expect(task.liveElapsed).toBe(12.5)
    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('50')
    expect(wrapper.text()).toContain('250 / 500')

    // 任务结束后回落到快照：进度事件的残留不能盖住最终结果。
    last().message({ type: 'state', data: stateFrame({ status: 'done', done: 500, total: 500 }) })
    await flush()

    expect(task.progress).toBeNull()
    expect(task.percent).toBe(100)
    expect(task.state.done).toBe(500)
    // 结束态不再挂进度条：顶栏换成结论，没有下载时页面上不该再有任何进度条。
    expect(wrapper.find('[role="progressbar"]').exists()).toBe(false)
  })

  it('结果行事件进入表格，字段按后端键名原样映射', async () => {
    const { wrapper, flush, results } = await boot()

    last().message({ type: 'state', data: stateFrame({ done: 1, total: 1 }) })
    last().message({ type: 'scan/result', data: [RECORD] })
    await flush()

    expect(results.all).toHaveLength(1)
    expect(results.all[0]).toMatchObject({
      ip: '1.1.1.1',
      port: 443,
      latency: 12.3,
      region_name: '洛杉矶',
      colo: 'LAX',
    })

    const headers = wrapper.findAll('thead th').map((cell) => cell.text())
    expect(headers).toContain('IP')
    expect(headers).toContain('地区')
    expect(headers).toContain('机房')

    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(1)
    const rowText = rows[0]?.text() ?? ''
    expect(rowText).toContain('1.1.1.1')
    expect(rowText).toContain('洛杉矶')
    expect(rowText).toContain('LAX')
    // 平均延迟 14.5 带单位，整数不带小数点。
    expect(rowText).toContain('14.5 ms')
  })

  it('成功但带告警：列出告警并可折叠，顶栏不再重复提示', async () => {
    const { wrapper, flush, task, ui } = await boot()

    last().message({ type: 'state', data: stateFrame({ done: 0, total: 500 }) })
    last().message({ type: 'scan/result', data: [RECORD] })
    last().message({
      type: 'scan/done',
      data: {
        count: 1,
        stage: 'scan',
        duration_ms: 4200,
        warnings: ['ASN 库不可用，结果里没有运营商'],
      },
    })
    last().message({ type: 'state', data: stateFrame({ status: 'done', done: 500, total: 500 }) })
    await flush()

    expect(task.outcome).toEqual({
      status: 'done',
      stage: 'scan',
      durationMs: 4200,
      warnings: ['ASN 库不可用，结果里没有运营商'],
    })

    const warnings = wrapper.findAll('.warn-list li')
    expect(warnings).toHaveLength(1)
    expect(warnings[0]?.text()).toContain('ASN 库不可用')
    expect(wrapper.text()).toContain('1 条提醒')
    expect(wrapper.find('details.warn-box').exists()).toBe(true)

    // 成功后自动跳到结果页，顶栏的状态条就不再重复一遍告警。
    expect(ui.activeView).toBe('result')
    expect(wrapper.find('.task-outcome').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('没能跑完')
  })

  it('失败时给出阶段与耗时，主提示只弹一次', async () => {
    const { wrapper, flush, task, ui } = await boot()

    last().message({ type: 'state', data: stateFrame({ done: 120, total: 500 }) })
    last().message({
      type: 'scan/failed',
      data: { count: 120, stage: 'scan', duration_ms: 1500 },
    })
    last().message({ type: 'error', data: { code: 'E_IO', msg: '文件读写失败，请检查数据目录' } })
    last().message({
      type: 'state',
      data: stateFrame({ status: 'failed', done: 120, total: 500, error: '文件读写失败，请检查数据目录' }),
    })
    await flush()

    expect(task.outcome).toEqual({
      status: 'failed',
      stage: 'scan',
      durationMs: 1500,
      warnings: [],
    })

    // /failed 补细节、error 出主提示：只允许一条提示，不能弹两次。
    expect(ui.toasts).toHaveLength(1)
    expect(ui.toasts[0]?.kind).toBe('bad')
    expect(ui.toasts[0]?.message).toBe('文件读写失败，请检查数据目录')

    expect(wrapper.text()).toContain('扫描没能跑完')
    expect(wrapper.text()).toContain('卡在「扫描」')
    expect(wrapper.text()).toContain('1.5 秒')
    expect(wrapper.find('.warn-box').exists()).toBe(false)

    // 没有跳到结果页时，顶栏保留一条可点的结局状态。
    expect(ui.activeView).toBe('scan')
    const strip = wrapper.get('.task-outcome')
    expect(strip.text()).toContain('扫描没能跑完')
    expect(strip.text()).toContain('1.5 秒')
  })

  it('断线后按退避重连，并在新连接上重拉全量状态', async () => {
    const { wrapper, flush, task } = await boot()
    const first = last()

    expect(sentTypes(first)).toEqual(
      expect.arrayContaining([
        'settings/get',
        'migrate/status',
        'history/list',
        'geo/status',
        'source/status',
        'server/status',
        'auth/status',
      ]),
    )

    last().message({ type: 'state', data: stateFrame({ done: 10, total: 100 }) })
    await flush()
    expect(task.percent).toBe(10)

    first.drop()
    await flush()
    expect(task.connection).toBe('closed')
    expect(task.retryInSeconds).toBe(1)

    // 首次退避 500ms：差 1ms 都不该重连。
    await vi.advanceTimersByTimeAsync(499)
    expect(sockets()).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(sockets()).toHaveLength(2)
    expect(task.connection).toBe('connecting')

    last().open()
    await flush()
    expect(task.connection).toBe('open')

    // 新连接上重新拉了一遍全量状态。
    expect(sentTypes(last())).toEqual(
      expect.arrayContaining(['settings/get', 'history/list', 'geo/status']),
    )

    last().message({ type: 'state', data: stateFrame({ done: 50, total: 100 }) })
    await flush()
    expect(task.percent).toBe(50)
    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('50')
  })
})
