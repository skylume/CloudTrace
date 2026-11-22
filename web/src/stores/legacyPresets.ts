/**
 * 已下线的「档位」留下的自定义档位。
 *
 * 档位不再是一等公民之后，旧 presets.json 里的自定义档位既不能悄悄丢掉（那是
 * 用户自己调过的参数），也不该自动写进配置（那会在他不知情时改掉默认值）。
 * 所以只提示：导入或导出都由用户点。
 *
 * 与旧版数据迁移一样，`dismissed` 只作用于本次会话：重启之后再问一次，直到
 * 用户真的处理掉。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { CMD } from '@/api/protocol'
import { sendCommand } from '@/api/client'
import type { LegacyPresetsStatus } from '@/api/types'
import { pickOutputTarget } from '@/utils/outputTarget'

export const useLegacyPresetsStore = defineStore('legacyPresets', () => {
  const status = ref<LegacyPresetsStatus | null>(null)
  /** 用户点了「忽略」。只作用于本次会话：重启之后还会再问一次。 */
  const dismissed = ref(false)
  /** 正在导入，用于禁用按钮避免重复点击。 */
  const running = ref(false)
  /** 本次会话里刚导入过：结果横幅只在这时显示，刷新页面不重复报「已导入」。 */
  const justRan = ref(false)

  const shouldOffer = computed(
    () =>
      status.value?.found === true &&
      !status.value.migrated &&
      (status.value.presets?.length ?? 0) > 0 &&
      !dismissed.value,
  )

  /** 刚跑完那次导入的结果，供界面显示。 */
  const justRanResult = computed(() => (justRan.value ? status.value : null))

  function apply(next: LegacyPresetsStatus): void {
    status.value = next
    if (next.imported?.length || next.skipped?.length) running.value = false
  }

  function refresh(): void {
    sendCommand(CMD.legacyPresets)
  }

  function run(): void {
    if (running.value) return
    running.value = true
    justRan.value = true
    // 空 ids = 全部导入：横幅上只有一个动作，没必要再让用户挑一遍。
    if (!sendCommand(CMD.legacyPresetsImport, { ids: [] })) running.value = false
  }

  /** 导入失败时也要解除等待，否则按钮会一直转下去。 */
  function fail(): void {
    running.value = false
  }

  function dismiss(): void {
    dismissed.value = true
  }

  /**
   * exportFile 把旧档位原样存成 JSON。
   *
   * 导出的是 `{default, presets}` 而不是当前配置：用户要的是「我原来调了什么」，
   * 翻译成新参数会丢掉新版已经不认识的项。
   */
  function exportFile(): void {
    const s = status.value
    if (!s?.presets?.length) return
    const raw = JSON.stringify({ default: s.default ?? '', presets: s.presets }, null, 2)
    const blob = new Blob([raw], { type: 'application/json' })
    pickOutputTarget({ name: 'cloudtrace-presets-legacy.json', blob }).save({
      name: 'cloudtrace-presets-legacy.json',
      blob,
    })
  }

  return {
    status,
    dismissed,
    running,
    shouldOffer,
    justRanResult,
    apply,
    refresh,
    run,
    fail,
    dismiss,
    exportFile,
  }
})
