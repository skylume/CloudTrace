/**
 * 运行日志。
 *
 * 记录的是**客户端实际收到的事件**：阶段变化、错误、ASN 库状态、任务结束。
 * 后端目前没有独立的日志流（事件表里没有 log 事件），所以这里是「界面这一侧
 * 看到了什么」，不是服务端的完整日志——这一点在面板标题里如实说明，免得
 * 用户拿着它去排查服务端问题。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface LogLine {
  /** 毫秒时间戳，渲染时格式化成时分秒。 */
  at: number
  level: 'info' | 'warn' | 'bad'
  text: string
}

/** 保留的行数上限。日志是给人扫一眼的，不是归档。 */
const MAX_LINES = 200

export const useLogStore = defineStore('log', () => {
  const lines = ref<LogLine[]>([])

  function push(text: string, level: LogLine['level'] = 'info'): void {
    const next = [...lines.value, { at: Date.now(), level, text }]
    // 超出上限丢最旧的：滚动区里翻几百行以前的记录没有意义。
    lines.value = next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next
  }

  function clear(): void {
    lines.value = []
  }

  return { lines, push, clear }
})

/** 把时间戳格式化成 HH:MM:SS，日志里不需要日期。 */
export function formatLogTime(at: number): string {
  const date = new Date(at)
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}
