/**
 * WebSocket 客户端：连接、心跳、自动重连、命令发送与事件订阅。
 *
 * 设计取舍：
 *   - 单连接。面板是一个用户对着一个后端，多连接只会让状态同步变复杂。
 *   - 重连用退避但封顶。断线是常态（休眠、切网），退避到十几秒就够了；
 *     继续放大只会让「我改完设置怎么没反应」变得更久。
 *   - 重连成功后由上层重新拉一次全量状态。这里只负责把「连上了」这件事
 *     播出去，不替上层决定要拉什么。
 */
import { EVT, type Envelope } from './protocol'

export type ConnectionState = 'connecting' | 'open' | 'closed'

type Handler = (data: unknown, envelope: Envelope) => void

/** 退避参数：首次重试 0.5 秒，翻倍到 10 秒封顶。 */
const RETRY_BASE_MS = 500
const RETRY_MAX_MS = 10_000

/** 心跳间隔。服务端会主动 ping，这里再发一层是为了让连接状态可观测。 */
const HEARTBEAT_MS = 30_000

export interface WSClientOptions {
  /** 连接地址；默认按当前页面的协议与主机拼出来。 */
  url?: string
  /** 重连时回调，便于界面显示「N 秒后重试」。 */
  onStateChange?: (state: ConnectionState, retryInSeconds: number) => void
}

export class WSClient {
  private socket: WebSocket | null = null
  private handlers = new Map<string, Set<Handler>>()
  private retry = 0
  private retryTimer: ReturnType<typeof setTimeout> | null = null
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null
  private closedByUs = false
  private readonly url: string
  /**
   * 连接状态变化的回调。公开可写：接线层在构造之后才拿到 store，
   * 用 setter 会多一个只有一行的中间层。
   */
  onStateChange?: WSClientOptions['onStateChange']

  state: ConnectionState = 'closed'

  constructor(options: WSClientOptions = {}) {
    this.url = options.url ?? defaultURL()
    this.onStateChange = options.onStateChange
  }

  /** connect 建立连接；重复调用是安全的。 */
  connect(): void {
    if (this.socket && (this.socket.readyState === WebSocket.OPEN || this.socket.readyState === WebSocket.CONNECTING)) {
      return
    }
    this.closedByUs = false
    this.setState('connecting', 0)

    const socket = new WebSocket(this.url)
    this.socket = socket

    socket.onopen = () => {
      this.retry = 0
      this.setState('open', 0)
      this.startHeartbeat()
      // 重连之后的第一件事是拿全量状态：断线期间发生的事这里补不回来。
      this.emit('__open__', undefined, { type: '__open__' })
    }

    socket.onmessage = (event) => {
      let envelope: Envelope
      try {
        envelope = JSON.parse(String(event.data)) as Envelope
      } catch {
        // 后端不会发非 JSON 报文；真出现了也只当噪音丢掉，不必打断连接。
        return
      }
      this.emit(envelope.type, envelope.data, envelope)
    }

    socket.onclose = () => {
      this.stopHeartbeat()
      this.socket = null
      if (this.closedByUs) {
        this.setState('closed', 0)
        return
      }
      this.scheduleReconnect()
    }

    socket.onerror = () => {
      // onerror 之后一定会跟一个 onclose，重连逻辑只写在 onclose 里，
      // 两处都写会让重试次数翻倍。
      socket.close()
    }
  }

  /** close 主动断开，不再重连。 */
  close(): void {
    this.closedByUs = true
    this.clearRetry()
    this.stopHeartbeat()
    this.socket?.close()
    this.socket = null
    this.setState('closed', 0)
  }

  /** send 发一条命令。未连接时返回 false，由调用方决定要不要提示。 */
  send(type: string, data?: unknown): boolean {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) return false
    this.socket.send(JSON.stringify(data === undefined ? { type } : { type, data }))
    return true
  }

  /** on 订阅一个事件，返回取消订阅的函数。 */
  on(type: string, handler: Handler): () => void {
    let set = this.handlers.get(type)
    if (!set) {
      set = new Set()
      this.handlers.set(type, set)
    }
    set.add(handler)
    return () => set?.delete(handler)
  }

  /** onOpen 订阅「连接就绪」——首次连接与每次重连都会触发。 */
  onOpen(handler: () => void): () => void {
    return this.on('__open__', () => handler())
  }

  private emit(type: string, data: unknown, envelope: Envelope): void {
    const set = this.handlers.get(type)
    if (!set) return
    for (const handler of set) {
      // 单个订阅者抛错不该拖垮整条分发链，否则一个组件的 bug 会让别的组件
      // 也收不到事件，排查时会误以为后端没发。
      try {
        handler(data, envelope)
      } catch (err) {
        console.error(`[ws] 处理 ${type} 事件出错`, err)
      }
    }
  }

  private scheduleReconnect(): void {
    this.retry += 1
    const delay = Math.min(RETRY_BASE_MS * 2 ** (this.retry - 1), RETRY_MAX_MS)
    this.setState('closed', Math.round(delay / 1000))
    this.retryTimer = setTimeout(() => this.connect(), delay)
  }

  private clearRetry(): void {
    if (this.retryTimer !== null) {
      clearTimeout(this.retryTimer)
      this.retryTimer = null
    }
    this.retry = 0
  }

  private startHeartbeat(): void {
    this.stopHeartbeat()
    this.heartbeatTimer = setInterval(() => {
      this.send('ping')
    }, HEARTBEAT_MS)
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer !== null) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
  }

  private setState(state: ConnectionState, retryInSeconds: number): void {
    this.state = state
    this.onStateChange?.(state, retryInSeconds)
  }
}

/** defaultURL 按当前页面拼出 WS 地址，生产与开发同源。 */
function defaultURL(): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/ws`
}

/** 订阅全部事件的辅助函数名，供需要打日志或调试时使用。 */
export { EVT }
