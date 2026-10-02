/**
 * WS 客户端：重连、事件分发、命令发送。
 *
 * 用一个假的 WebSocket 替身，不碰真实网络：断线重连这类行为要能稳定复现，
 * 靠真实连接是测不稳的。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { WSClient } from './ws'

type Listener = ((event: unknown) => void) | null

/** FakeSocket 记录发出的报文，并允许测试手动触发开 / 关 / 消息。 */
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

  /** 以下三个是测试用的驱动方法。 */
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

const sockets = () => FakeSocket.instances
const last = (): FakeSocket => {
  const socket = FakeSocket.instances.at(-1)
  if (!socket) throw new Error('还没有建立过连接')
  return socket
}

beforeEach(() => {
  FakeSocket.instances = []
  vi.useFakeTimers()
  vi.stubGlobal('WebSocket', FakeSocket as unknown as typeof WebSocket)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('WSClient 连接与重连', () => {
  it('连上之后状态变为 open，并通知订阅者', () => {
    const states: string[] = []
    const client = new WSClient({ url: 'ws://example/ws', onStateChange: (state) => states.push(state) })
    let opened = 0
    client.onOpen(() => {
      opened += 1
    })

    client.connect()
    expect(client.state).toBe('connecting')

    last().open()
    expect(client.state).toBe('open')
    expect(opened).toBe(1)
    expect(states).toEqual(['connecting', 'open'])
  })

  it('断线后退避重连，并逐次拉长间隔', () => {
    const client = new WSClient({ url: 'ws://example/ws' })
    client.connect()
    last().open()

    last().drop()
    expect(client.state).toBe('closed')
    expect(sockets()).toHaveLength(1)

    // 首次退避 500ms。
    vi.advanceTimersByTime(499)
    expect(sockets()).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(sockets()).toHaveLength(2)

    // 第二次退避翻倍到 1000ms。
    last().drop()
    vi.advanceTimersByTime(999)
    expect(sockets()).toHaveLength(2)
    vi.advanceTimersByTime(1)
    expect(sockets()).toHaveLength(3)
  })

  it('重连成功后重新触发 onOpen，供上层恢复状态', () => {
    const client = new WSClient({ url: 'ws://example/ws' })
    let opened = 0
    client.onOpen(() => {
      opened += 1
    })

    client.connect()
    last().open()
    last().drop()
    vi.advanceTimersByTime(500)
    last().open()

    expect(opened).toBe(2)
  })

  it('主动关闭之后不再重连', () => {
    const client = new WSClient({ url: 'ws://example/ws' })
    client.connect()
    last().open()

    client.close()
    vi.advanceTimersByTime(10_000)

    expect(sockets()).toHaveLength(1)
    expect(client.state).toBe('closed')
  })
})

describe('WSClient 事件与命令', () => {
  it('按事件名分发给订阅者，取消订阅后不再收到', () => {
    const client = new WSClient({ url: 'ws://example/ws' })
    client.connect()
    last().open()

    const received: unknown[] = []
    const off = client.on('progress', (data) => received.push(data))

    last().message({ type: 'progress', data: { done: 1 } })
    expect(received).toEqual([{ done: 1 }])

    off()
    last().message({ type: 'progress', data: { done: 2 } })
    expect(received).toHaveLength(1)
  })

  it('单个订阅者抛错不影响其它订阅者', () => {
    const client = new WSClient({ url: 'ws://example/ws' })
    client.connect()
    last().open()

    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    let secondCalled = false
    client.on('state', () => {
      throw new Error('订阅者自身出错')
    })
    client.on('state', () => {
      secondCalled = true
    })

    last().message({ type: 'state', data: {} })

    expect(secondCalled).toBe(true)
    expect(errors).toHaveBeenCalled()
    errors.mockRestore()
  })

  it('非法 JSON 报文被忽略，连接不受影响', () => {
    const client = new WSClient({ url: 'ws://example/ws' })
    client.connect()
    last().open()

    last().onmessage?.({ data: '不是 JSON' })
    expect(client.state).toBe('open')
  })

  it('未连接时发送返回 false，连上之后正常发出', () => {
    const client = new WSClient({ url: 'ws://example/ws' })
    client.connect()

    expect(client.send('ping')).toBe(false)

    last().open()
    expect(client.send('ping')).toBe(true)
    expect(last().sent).toEqual(['{"type":"ping"}'])

    expect(client.send('scan/start', { port: 443 })).toBe(true)
    expect(last().sent[1]).toBe('{"type":"scan/start","data":{"port":443}}')
  })
})
