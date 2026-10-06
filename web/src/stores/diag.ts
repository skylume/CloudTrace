/**
 * 网络诊断。
 *
 * 与「配置体检」分工明确：体检查的是**配置**对不对（端口占用、目录不可写），
 * 这里查的是**网络**通不通。用户说「扫不出结果」时，先跑这个就能分清是环境
 * 问题还是节点问题——两者的处理方式完全不同。
 *
 * 结果只留在内存里：诊断是一次性的快照，刷新页面后重跑一次比留着旧结果更
 * 有意义（网络状态本来就随时会变）。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

import { CMD } from '@/api/protocol'
import { sendCommand } from '@/api/client'
import { api } from '@/api/rest'
import type { DiagExportResult, DiagReport } from '@/api/types'

export const useDiagStore = defineStore('diag', () => {
  const report = ref<DiagReport | null>(null)
  /** 诊断进行中。四项检查最坏要十几秒，必须让用户看见「在跑」。 */
  const running = ref(false)
  const exporting = ref(false)

  function run(): boolean {
    running.value = true
    const ok = sendCommand(CMD.diagRun, {})
    if (!ok) running.value = false
    return ok
  }

  function applyReport(next: DiagReport): void {
    report.value = next
    running.value = false
  }

  /**
   * 导出诊断包。
   *
   * 把手上这份报告一起发过去：服务端不留诊断结果，导出的是用户此刻看到的
   * 那一份。没跑过诊断也能导，包里会写明「本次没有跑诊断」。
   */
  function exportBundle(): boolean {
    exporting.value = true
    const ok = sendCommand(CMD.diagExport, { report: report.value })
    if (!ok) exporting.value = false
    return ok
  }

  function applyExport(result: DiagExportResult): void {
    exporting.value = false
    if (result.url) api.download(result.url, result.name)
  }

  /** 后端报错时解除等待，否则按钮会一直转下去。 */
  function fail(): void {
    running.value = false
    exporting.value = false
  }

  return { report, running, exporting, run, applyReport, exportBundle, applyExport, fail }
})
