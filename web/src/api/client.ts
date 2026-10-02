/**
 * WebSocket 单例与命令发送。
 *
 * 组件不直接持有连接：命令一律经由这里发出，便于统一处理「还没连上」
 * 与「发送失败」，也让组件测试不必造一个假的 WebSocket。
 */
import { EVT } from './protocol'
import { WSClient } from './ws'

let instance: WSClient | null = null
/** 连接不可用时的回调，由接线层注入（通常是弹一条提示）。 */
let onSendFailed: ((type: string) => void) | null = null

export function wsClient(): WSClient {
  if (!instance) instance = new WSClient()
  return instance
}

export function setSendFailureHandler(handler: (type: string) => void): void {
  onSendFailed = handler
}

/** sendCommand 发一条命令；未连接时返回 false 并回调提示。 */
export function sendCommand(type: string, data?: unknown): boolean {
  const ok = wsClient().send(type, data)
  if (!ok) onSendFailed?.(type)
  return ok
}

/** 订阅事件的便捷入口，返回取消订阅函数。 */
export function onEvent(type: string, handler: (data: unknown) => void): () => void {
  return wsClient().on(type, (data) => handler(data))
}

export { EVT }
