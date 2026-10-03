/**
 * 测速相关的临时通知。
 *
 * 只放「需要用户做决定」的信息：被限速熔断了，接着要降低并发还是换个源。
 * 一般性的错误提示走全局提示条，不进这里——两者混在一起会让这个提示失去分量。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface BreakerNotice {
  message: string
  /** 当前配置的测速并发，「降低并发」以它为起点。 */
  concurrency: number
  /** 当前的测速源模式。 */
  urlMode: string
}

export const useSpeedStore = defineStore('speed', () => {
  const breaker = ref<BreakerNotice | null>(null)

  function applyBreaker(notice: BreakerNotice): void {
    breaker.value = notice
  }

  function dismissBreaker(): void {
    breaker.value = null
  }

  /**
   * 建议的并发值。
   *
   * 砍半而不是给一个固定数字：用户原本设成 4 或 16 时，「降到 4」这种建议
   * 要么没变、要么跨度太离谱。砍半在两种情况下都合理，且下限保底为 1。
   */
  function suggestedConcurrency(): number {
    const current = breaker.value?.concurrency ?? 0
    return Math.max(1, Math.floor(current / 2))
  }

  return { breaker, applyBreaker, dismissBreaker, suggestedConcurrency }
})
