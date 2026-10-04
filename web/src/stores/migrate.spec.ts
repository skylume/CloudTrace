/**
 * 旧版数据迁移的状态。
 *
 * 这里守的是「什么时候该提示」：多提示一次只是烦，少提示一次用户就永远不知道
 * 自己的旧配置还能搬过来。
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// 声明成接收任意参数：下面的 mock 工厂要把它透传出去，
// 零参数的 vi.fn 会让展开实参在类型上过不去。
const sendCommand = vi.fn((..._args: unknown[]) => true)

vi.mock('@/api/client', () => ({
  sendCommand: (...args: unknown[]) => sendCommand(...args),
}))

import type { MigrateStatus } from '@/api/types'

const { useMigrateStore } = await import('./migrate')

function status(overrides: Partial<MigrateStatus> = {}): MigrateStatus {
  return { found: true, settings: true, histories: 2, migrated: false, ...overrides }
}

beforeEach(() => {
  setActivePinia(createPinia())
  sendCommand.mockClear()
})

describe('是否提示导入', () => {
  it('检测到旧数据就提示', () => {
    const store = useMigrateStore()
    store.apply(status())
    expect(store.shouldOffer).toBe(true)
  })

  it('没检测到旧数据时不提示', () => {
    const store = useMigrateStore()
    store.apply(status({ found: false }))
    expect(store.shouldOffer).toBe(false)
  })

  /**
   * 已经迁移过的旧数据不再提示。
   *
   * 用户可能因为别的原因留着旧目录（比如还没删旧版本），每次都问一遍很烦。
   */
  it('已经迁移过就不再提示', () => {
    const store = useMigrateStore()
    store.apply(status({ migrated: true }))
    expect(store.shouldOffer).toBe(false)
  })

  it('忽略之后本次会话不再提示', () => {
    const store = useMigrateStore()
    store.apply(status())
    store.dismiss()
    expect(store.shouldOffer).toBe(false)
  })

  it('状态还没到时不提示', () => {
    expect(useMigrateStore().shouldOffer).toBe(false)
  })
})

describe('结果横幅', () => {
  it('刚跑完时给出结果', () => {
    const store = useMigrateStore()
    store.run()
    store.apply(status({ migrated: true, report: { settings: true, imported: 2, failed: 0 } }))

    expect(store.justRanReport).toBeDefined()
    expect(store.justRanReport?.imported).toBe(2)
  })

  /**
   * 结果只在刚跑完那次显示。
   *
   * 后端的「最近一次结果」在内存里，刷新页面会重新下发——不加这个限制的话，
   * 用户每次刷新都会看到一条「已导入」。
   */
  it('没跑过时不显示结果', () => {
    const store = useMigrateStore()
    store.apply(status({ migrated: true, report: { settings: true, imported: 2, failed: 0 } }))
    expect(store.justRanReport).toBeUndefined()
  })

  it('关掉之后不再显示', () => {
    const store = useMigrateStore()
    store.run()
    store.apply(status({ migrated: true, report: { settings: true, imported: 2, failed: 0 } }))
    store.dismiss()
    expect(store.justRanReport).toBeUndefined()
  })
})

describe('等待态', () => {
  it('跑的时候进入等待态，收到结果后解除', () => {
    const store = useMigrateStore()
    store.run()
    expect(store.running).toBe(true)

    store.apply(status({ migrated: true, report: { settings: true, imported: 1, failed: 0 } }))
    expect(store.running).toBe(false)
  })

  it('重复点击不会发第二次', () => {
    const store = useMigrateStore()
    store.run()
    store.run()
    expect(store.running).toBe(true)
  })

  /** 失败也要解除等待，否则按钮会一直转下去。 */
  it('失败时解除等待', () => {
    const store = useMigrateStore()
    store.run()
    store.fail()
    expect(store.running).toBe(false)
  })
})
