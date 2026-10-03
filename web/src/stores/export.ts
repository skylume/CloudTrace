/**
 * 导出。
 *
 * 流程是「发命令 → 拿回一个下载地址 → 交给浏览器下载」：文件内容不经过前端，
 * 由后端生成后从 `/api/download/{id}` 取走。这样几十万行的导出不会把内存压在
 * 浏览器里，也天然复用了后端已经写好的 CSV/JSON/TXT 三种格式。
 *
 * 导出物在服务端是有期限的（内存中转，会过期），所以拿到地址就该立刻下载，
 * 不要缓存起来等用户再点一次。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

import { sendCommand } from '@/api/client'
import { api } from '@/api/rest'
import type { ExportResult } from '@/api/types'

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
    const ok = sendCommand('export', {
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
    api.download(result.url, result.name)
  }

  /** 后端报错时也要解除等待，否则按钮会一直转下去。 */
  function fail(): void {
    pending.value = false
  }

  return { pending, request, applyResult, fail }
})
