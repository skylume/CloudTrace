/**
 * 旧档位库的提示与导入。
 *
 * 这里守两件事：不该提示的时候别烦人（已迁移、已忽略、压根没有），该提示的
 * 时候别漏掉——漏掉一次，用户的旧参数就再也没人提起了。
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const sendCommand = vi.fn((..._args: unknown[]) => true)

vi.mock('@/api/client', () => ({
  sendCommand: (...args: unknown[]) => sendCommand(...args),
}))

import type { LegacyPresetsStatus } from '@/api/types'

const { useLegacyPresetsStore } = await import('./legacyPresets')

function status(overrides: Partial<LegacyPresetsStatus> = {}): LegacyPresetsStatus {
  return {
    found: true,
    migrated: false,
    presets: [{ id: 'p1', name: '我的档位', values: { 'scan.workers': 321 } }],
    ...overrides,
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  sendCommand.mockClear()
})

describe('是否提示旧档位', () => {
  it('发现档位就提示', () => {
    const store = useLegacyPresetsStore()
    store.apply(status())
    expect(store.shouldOffer).toBe(true)
  })

  it('没有档位时不提示', () => {
    const store = useLegacyPresetsStore()
    store.apply(status({ presets: [] }))
    expect(store.shouldOffer).toBe(false)
  })

  it('已经导入过就不再提示', () => {
    const store = useLegacyPresetsStore()
    store.apply(status({ migrated: true }))
    expect(store.shouldOffer).toBe(false)
  })

  it('忽略之后本次会话不再提示', () => {
    const store = useLegacyPresetsStore()
    store.apply(status())
    store.dismiss()
    expect(store.shouldOffer).toBe(false)
  })
})

describe('导入', () => {
  it('一次导入全部档位', () => {
    const store = useLegacyPresetsStore()
    store.apply(status())
    store.run()
    expect(sendCommand).toHaveBeenCalledWith('presets/legacy/import', { ids: [] })
    expect(store.running).toBe(true)
  })

  it('重复点击只发一次命令', () => {
    const store = useLegacyPresetsStore()
    store.apply(status())
    store.run()
    store.run()
    expect(sendCommand).toHaveBeenCalledTimes(1)
  })

  it('回执到了就解除等待并给出结果', () => {
    const store = useLegacyPresetsStore()
    store.apply(status())
    store.run()
    store.apply(status({ imported: ['我的档位'] }))
    expect(store.running).toBe(false)
    expect(store.justRanResult?.imported).toEqual(['我的档位'])
  })

  it('失败时也要解除等待', () => {
    const store = useLegacyPresetsStore()
    store.apply(status())
    store.run()
    store.fail()
    expect(store.running).toBe(false)
  })

  it('没跑过导入时不显示结果横幅', () => {
    const store = useLegacyPresetsStore()
    store.apply(status({ imported: ['我的档位'] }))
    expect(store.justRanResult).toBeNull()
  })
})
