/**
 * 上一次扫描的请求参数。
 *
 * 零结果时结果页要能「放宽一点重跑」，而重跑必须用同一份来源、端口与模式——
 * 拿当前设置重拼一份，会悄悄换掉用户刚才用的东西，跑出来的结果也就没法比较。
 *
 * 放 sessionStorage 而不是配置：这是本次会话的现场，刷新后重开一个标签页
 * 也不该假装自己知道上次跑了什么。
 */
const KEY = 'cloudtrace.last-scan-request'

/** rememberScanRequest 记下这次发出的请求。 */
export function rememberScanRequest(payload: Record<string, unknown>): void {
  try {
    sessionStorage.setItem(KEY, JSON.stringify(payload))
  } catch {
    // 存不下（隐私模式、配额满）只是少了重跑入口，不影响扫描本身。
  }
}

/** recallScanRequest 取回上一次的请求；没有或已损坏时返回 null。 */
export function recallScanRequest(): Record<string, unknown> | null {
  try {
    const raw = sessionStorage.getItem(KEY)
    if (!raw) return null
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null
    return parsed as Record<string, unknown>
  } catch {
    return null
  }
}
