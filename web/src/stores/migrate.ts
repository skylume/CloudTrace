/**
 * 旧版数据迁移。
 *
 * 迁移**不阻断启动**：它只是扫描页上的一条横幅，用户点「导入」才会真的动数据。
 * 悄悄搬东西比不搬更糟——用户会发现自己的旧配置在不知情的时候变了，而那时他
 * 已经记不清原来是什么样。
 *
 * 所以这里存两份状态：`status` 是后端说「发现了什么」，`dismissed` 是「用户这次
 * 不想搬」。两者都满足才显示横幅。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { CMD } from '@/api/protocol'
import { sendCommand } from '@/api/client'
import type { MigrateStatus } from '@/api/types'

export const useMigrateStore = defineStore('migrate', () => {
  const status = ref<MigrateStatus | null>(null)
  /** 用户点了「忽略」。只作用于本次会话：重启之后还会再问一次。 */
  const dismissed = ref(false)
  /** 正在迁移，用于禁用按钮避免重复点击。 */
  const running = ref(false)
  /**
   * 本次会话里刚跑过一次迁移。
   *
   * 结果横幅只在这个时候显示：后端的最近一次结果在内存里，刷新页面就会重新
   * 下发，不加这个标记的话用户每次刷新都会看到一条「已导入」。
   */
  const justRan = ref(false)

  /**
   * 是否该显示横幅。
   *
   * 已经迁移过的旧数据不再提示：用户可能因为别的原因留着旧目录（比如还没删
   * 旧版本），每次都问一遍很烦。
   */
  const shouldOffer = computed(
    () => status.value?.found === true && !status.value.migrated && !dismissed.value,
  )

  /** 刚跑完那次迁移的结果，供界面显示。 */
  const justRanReport = computed(() => (justRan.value && !dismissed.value ? status.value?.report : undefined))

  function apply(next: MigrateStatus): void {
    status.value = next
    if (next.report) {
      // 迁移已经跑过，等待态解除。
      running.value = false
    }
  }

  function refresh(): void {
    sendCommand(CMD.migrateStatus)
  }

  function run(): void {
    if (running.value) return
    running.value = true
    justRan.value = true
    if (!sendCommand(CMD.migrateRun)) running.value = false
  }

  /** 迁移失败时也要解除等待，否则按钮会一直转下去。 */
  function fail(): void {
    running.value = false
  }

  function dismiss(): void {
    dismissed.value = true
  }

  return {
    status,
    dismissed,
    running,
    justRanReport,
    shouldOffer,
    apply,
    refresh,
    run,
    fail,
    dismiss,
  }
})
