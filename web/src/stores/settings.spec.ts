/**
 * 设置 store 里的旧数据提示：迁移与已下线档位。
 *
 * 这里守两件事：不该提示的时候别烦人（已迁移、已忽略、压根没有），该提示的
 * 时候别漏掉——漏掉一次，用户的旧配置就再也没人提起了。
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const sendCommand = vi.fn((..._args: unknown[]) => true)

vi.mock('@/api/client', () => ({
  sendCommand: (...args: unknown[]) => sendCommand(...args),
}))

import type { LegacyPresetsStatus, MigrateStatus } from '@/api/types'

const { useSettingsStore } = await import('./settings')

function migrateStatus(overrides: Partial<MigrateStatus> = {}): MigrateStatus {
  return { found: true, settings: true, histories: 2, migrated: false, ...overrides }
}

function legacyStatus(overrides: Partial<LegacyPresetsStatus> = {}): LegacyPresetsStatus {
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

describe('旧版数据迁移', () => {
  it('检测到旧数据就提示', () => {
    const store = useSettingsStore()
    store.applyMigrate(migrateStatus())
    expect(store.migrateShouldOffer).toBe(true)
  })

  it('没检测到旧数据时不提示', () => {
    const store = useSettingsStore()
    store.applyMigrate(migrateStatus({ found: false }))
    expect(store.migrateShouldOffer).toBe(false)
  })

  it('已经迁移过就不再提示', () => {
    const store = useSettingsStore()
    store.applyMigrate(migrateStatus({ migrated: true }))
    expect(store.migrateShouldOffer).toBe(false)
  })

  it('忽略之后本次会话不再提示', () => {
    const store = useSettingsStore()
    store.applyMigrate(migrateStatus())
    store.dismissMigrate()
    expect(store.migrateShouldOffer).toBe(false)
  })

  it('状态还没到时不提示', () => {
    expect(useSettingsStore().migrateShouldOffer).toBe(false)
  })

  it('刚跑完时给出结果', () => {
    const store = useSettingsStore()
    store.runMigrate()
    store.applyMigrate(migrateStatus({ migrated: true, report: { settings: true, imported: 2, failed: 0 } }))
    expect(store.migrateReport?.imported).toBe(2)
  })

  it('没跑过时不显示结果', () => {
    const store = useSettingsStore()
    store.applyMigrate(migrateStatus({ migrated: true, report: { settings: true, imported: 2, failed: 0 } }))
    expect(store.migrateReport).toBeUndefined()
  })

  it('关掉之后不再显示结果', () => {
    const store = useSettingsStore()
    store.runMigrate()
    store.applyMigrate(migrateStatus({ migrated: true, report: { settings: true, imported: 2, failed: 0 } }))
    store.dismissMigrate()
    expect(store.migrateReport).toBeUndefined()
  })

  it('跑的时候进入等待态，收到结果后解除', () => {
    const store = useSettingsStore()
    store.runMigrate()
    expect(store.migrateRunning).toBe(true)
    store.applyMigrate(migrateStatus({ migrated: true, report: { settings: true, imported: 1, failed: 0 } }))
    expect(store.migrateRunning).toBe(false)
  })

  it('重复点击不会发第二次', () => {
    const store = useSettingsStore()
    store.runMigrate()
    store.runMigrate()
    expect(store.migrateRunning).toBe(true)
  })

  it('失败时解除等待', () => {
    const store = useSettingsStore()
    store.runMigrate()
    store.failMigrate()
    expect(store.migrateRunning).toBe(false)
  })
})

describe('旧档位', () => {
  it('发现档位就提示', () => {
    const store = useSettingsStore()
    store.applyLegacy(legacyStatus())
    expect(store.legacyShouldOffer).toBe(true)
  })

  it('没有档位时不提示', () => {
    const store = useSettingsStore()
    store.applyLegacy(legacyStatus({ presets: [] }))
    expect(store.legacyShouldOffer).toBe(false)
  })

  it('已经导入过就不再提示', () => {
    const store = useSettingsStore()
    store.applyLegacy(legacyStatus({ migrated: true }))
    expect(store.legacyShouldOffer).toBe(false)
  })

  it('忽略之后本次会话不再提示', () => {
    const store = useSettingsStore()
    store.applyLegacy(legacyStatus())
    store.dismissLegacy()
    expect(store.legacyShouldOffer).toBe(false)
  })

  it('一次导入全部档位', () => {
    const store = useSettingsStore()
    store.applyLegacy(legacyStatus())
    store.runLegacy()
    expect(sendCommand).toHaveBeenCalledWith('presets/legacy/import', { ids: [] })
    expect(store.legacyRunning).toBe(true)
  })

  it('重复点击只发一次命令', () => {
    const store = useSettingsStore()
    store.applyLegacy(legacyStatus())
    store.runLegacy()
    store.runLegacy()
    expect(sendCommand).toHaveBeenCalledTimes(1)
  })

  it('回执到了就解除等待并给出结果', () => {
    const store = useSettingsStore()
    store.applyLegacy(legacyStatus())
    store.runLegacy()
    store.applyLegacy(legacyStatus({ imported: ['我的档位'] }))
    expect(store.legacyRunning).toBe(false)
    expect(store.legacyResult?.imported).toEqual(['我的档位'])
  })

  it('失败时也要解除等待', () => {
    const store = useSettingsStore()
    store.applyLegacy(legacyStatus())
    store.runLegacy()
    store.failLegacy()
    expect(store.legacyRunning).toBe(false)
  })

  it('没跑过导入时不显示结果横幅', () => {
    const store = useSettingsStore()
    store.applyLegacy(legacyStatus({ imported: ['我的档位'] }))
    expect(store.legacyResult).toBeNull()
  })
})
