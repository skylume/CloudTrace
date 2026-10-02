/**
 * REST 封装。
 *
 * 只包那几条不适合走 WebSocket 的接口：一次性读取（字段清单、探活）与文件
 * 下载（走浏览器的下载流程，需要带上会话 Cookie）。
 *
 * 同源请求因此不需要 CORS 配置；开发态的跨端口由 Vite 代理解决。
 */
import type { ExportFields, HealthReport } from './types'

/** APIError 带上后端的错误码，便于界面区分「未登录」与「真的出错了」。 */
export class APIError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'APIError'
    this.status = status
    this.code = code
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
    ...init,
  })

  if (!response.ok) {
    // 后端出错时回的是 {code, msg}；解析不出来就退回状态码，
    // 至少比抛一个「undefined」强。
    let code = 'E_UNKNOWN'
    let message = `请求失败（${response.status}）`
    try {
      const body = (await response.json()) as { code?: string; msg?: string }
      if (body.code) code = body.code
      if (body.msg) message = body.msg
    } catch {
      /* 保持上面的兜底文案 */
    }
    throw new APIError(response.status, code, message)
  }

  return (await response.json()) as T
}

export interface HealthResponse {
  status: string
  version: string
  protocol_version: number
  uptime_s: number
  data_dir: string
  warnings?: string[]
}

export const api = {
  health: () => request<HealthResponse>('/api/health'),

  /** 导出字段清单。前端不硬编码任何字段名，全靠这里下发。 */
  exportFields: () => request<ExportFields>('/api/export/fields'),

  /** 服务端的探活接口与配置体检不是一回事：前者不鉴权，后者走 WS。 */
  configHealth: () => request<HealthReport>('/health'),

  /** 本地结果地址的文本形态，供脚本与「一键复制全部」使用。 */
  async latestText(): Promise<string> {
    const response = await fetch('/latest', { credentials: 'same-origin' })
    if (!response.ok) throw new APIError(response.status, 'E_NOT_FOUND', '还没有可用结果')
    return response.text()
  },

  /**
   * 触发一次导出文件下载。
   *
   * 用 a 标签而不是 fetch + blob：后端已经给了 Content-Disposition 与文件名，
   * 走浏览器原生下载能保留这些信息，也不会把文件内容读进内存。
   */
  download(url: string, filename?: string): void {
    const link = document.createElement('a')
    link.href = url
    if (filename) link.download = filename
    link.rel = 'noopener'
    document.body.appendChild(link)
    link.click()
    link.remove()
  },
}
