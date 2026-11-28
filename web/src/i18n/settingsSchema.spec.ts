/**
 * 设置项声明与文案的一致性。
 *
 * 这里守的是「声明、文案两张表走散」：路径声明在一处、中英文案在另一处，三者
 * 靠人肉保持一致迟早会出问题。曾经英文侧把 `export.include_unreached` 写成
 * `include_unreachable`——多一个字母，界面上那一项就永远显示成裸的键名。
 *
 * 也顺便守住「改了就看不到」这类问题：声明了配置里没有的键时，改动会被静默
 * 丢掉，界面像保存成功了，下次进设置页又变回去。那份清单由后端对着同一张表
 * 核对。
 */
import { describe, expect, it } from 'vitest'

import {
  SETTING_GROUPS,
  buildPatchMany,
  fieldMatches,
  fieldPatch,
  fieldValue,
  fieldVisible,
  highlightParts,
  settingsText,
  type SettingField,
} from './settingsSchema'
import { PARAM_PATHS } from './params'

const allFields = SETTING_GROUPS.flatMap((group) => group.fields)

function fieldOf(path: string): SettingField {
  const found = allFields.find((field) => field.path === path)
  if (found === undefined) throw new Error(`没有这一项：${path}`)
  return found
}

const declared = SETTING_GROUPS.flatMap((group) => group.fields.map((field) => field.path))

describe('设置项声明', () => {
  it('每一项都有文案', () => {
    for (const path of declared) {
      expect(settingsText[path], `缺文案：${path}`).toBeDefined()
    }
  })

  it('文案里没有已经不存在的设置项', () => {
    const known = new Set(declared)
    for (const path of Object.keys(settingsText)) {
      expect(known.has(path), `孤儿文案：${path}`).toBe(true)
    }
  })

  it('路径不重复：重复声明只有第一个会生效', () => {
    const seen = new Set<string>()
    for (const path of declared) {
      expect(seen.has(path), `路径重复：${path}`).toBe(false)
      seen.add(path)
    }
  })

  /**
   * 每组至少有一个常用项。
   *
   * 简单模式只显示标了 primary 的项，一组里一个都没有的话整组会消失——那一组
   * 的设置就再也点不到了，而界面上看不出少了什么。
   */
  it('每组至少有一个常用项', () => {
    for (const group of SETTING_GROUPS) {
      expect(
        group.fields.some((field) => field.primary),
        `${group.id} 组没有常用项，简单模式下整组会消失`,
      ).toBe(true)
    }
  })

  /**
   * 分组成员必须覆盖组内每个字段的配置前缀。
   *
   * 「恢复本组默认」按 members 发重置键，漏掉一个前缀，那一项就会在用户点了
   * 恢复之后依然保持旧值——界面上看不出原因，只会觉得这个按钮时灵时不灵。
   */
  it('分组成员覆盖组内所有字段前缀', () => {
    for (const group of SETTING_GROUPS) {
      const members = group.members ?? [group.id]
      for (const field of group.fields) {
        const prefix = field.path.split('.')[0] ?? ''
        expect(
          members.includes(prefix),
          `${group.id} 组的 ${field.path} 不在 members 里，恢复本组会漏掉它`,
        ).toBe(true)
      }
    }
  })

  it('分组 id 也不重复', () => {
    const ids = SETTING_GROUPS.map((group) => group.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('数值项的取值范围本身要成立', () => {
    for (const group of SETTING_GROUPS) {
      for (const field of group.fields) {
        if (field.min === undefined || field.max === undefined) continue
        expect(field.min, `${field.path} 的下限大于上限`).toBeLessThanOrEqual(field.max)
      }
    }
  })

  /**
   * 显示条件必须指向真实存在的设置项。
   *
   * 路径拼错时那一项永远不显示，而界面上只是「少了一项」，没有任何报错。
   */
  /**
   * 扫描页能改的参数，设置页必须也声明得到。
   *
   * 两个页面编辑的是同一批配置。设置页少声明一个，用户在扫描页调完会以为
   * 设置页里也有这一项；更糟的是「恢复默认」碰不到它，那一项就永远回不到出厂值。
   * 合成项（一个控件写多个键）要把背后的键一并算作已声明，否则这条守卫会把
   * 正常的合并项判成缺失。
   */
  it('扫描页能改的参数都在设置页声明里', () => {
    const known = new Set(declared)
    for (const field of allFields) {
      for (const patch of Object.values(field.writes ?? {})) {
        for (const key of Object.keys(patch)) known.add(key)
      }
      for (const key of Object.values(field.toggles ?? {})) known.add(key)
    }
    for (const [name, path] of Object.entries(PARAM_PATHS)) {
      expect(known.has(path), `扫描页的 ${name} 写 ${path}，设置页没有声明它`).toBe(true)
    }
  })

  it('显示条件引用的都是已声明的项', () => {
    const known = new Set(declared)
    for (const field of allFields) {
      if (field.showWhen === undefined) continue
      const conditions = Array.isArray(field.showWhen) ? field.showWhen : [field.showWhen]
      for (const condition of conditions) {
        expect(known.has(condition.path), `${field.path} 的条件引用了不存在的 ${condition.path}`).toBe(
          true,
        )
        expect(condition.in.length, `${field.path} 的条件没有取值`).toBeGreaterThan(0)
      }
    }
  })
})

describe('按取值决定是否显示', () => {
  it('互斥项只显示生效的那一个', () => {
    const count = fieldOf('history.keep_count')
    const days = fieldOf('history.keep_days')
    expect(fieldVisible(count, { history: { keep_mode: 'days' } })).toBe(false)
    expect(fieldVisible(days, { history: { keep_mode: 'days' } })).toBe(true)
    expect(fieldVisible(count, { history: { keep_mode: 'count' } })).toBe(true)
    expect(fieldVisible(days, { history: { keep_mode: 'count' } })).toBe(false)
  })

  it('多项条件要同时满足', () => {
    const field = fieldOf('geo.asn_update_interval_days')
    expect(fieldVisible(field, { geo: { asn_source: 'iptoasn', asn_auto_update: false } })).toBe(false)
    expect(fieldVisible(field, { geo: { asn_source: 'off', asn_auto_update: true } })).toBe(false)
    expect(fieldVisible(field, { geo: { asn_source: 'iptoasn', asn_auto_update: true } })).toBe(true)
  })

  it('配置还没读到时条件项不显示，无条件的照常显示', () => {
    expect(fieldVisible(fieldOf('geo.asn_db_path'), {})).toBe(false)
    expect(fieldVisible(fieldOf('ui.theme'), {})).toBe(true)
  })
})

describe('常用区与生效时机', () => {
  /**
   * 常用区固定 20 项。
   *
   * 历史保留的「按次数 / 按天数」两项互斥，两种取值下都得凑够 20——否则切到
   * 按天数时常用区少一项，用户得回高级区找那个数字。
   */
  it('两种保留方式下常用区都是 20 项', () => {
    for (const mode of ['count', 'days']) {
      const values = { history: { keep_mode: mode } }
      const primary = allFields.filter((field) => field.primary && fieldVisible(field, values))
      expect(primary.length, `${mode} 模式下的常用项数量`).toBe(20)
    }
  })

  /**
   * 合成项的档位要与下拉里的选项一一对上。
   *
   * 下拉里列出一个档位、writes 里却没有它，选中时什么都不会写，界面像保存成功
   * 了；反过来 writes 里多一个，那个档位永远选不到。两种都不会报错。
   */
  it('合成项的档位与选项一一对应', () => {
    for (const field of allFields) {
      const options = field.writes ?? field.toggles
      if (options === undefined) continue
      const keys = Object.keys(options)
      expect(new Set(keys).size, `${field.path} 的选项重复`).toBe(keys.length)
      if (field.kind === 'enum') {
        expect([...keys].sort(), `${field.path} 的档位与选项不一致`).toEqual(
          [...(field.options ?? [])].sort(),
        )
      }
    }
  })

  it('多选值由背后几个开关反推', () => {
    const field = fieldOf('notify.channels')
    expect(fieldValue(field, { notify: { web: true, tray: false, sound: true } })).toEqual([
      'web',
      'sound',
    ])
    expect(fieldPatch(field, ['sound'])).toEqual({
      'notify.web': false,
      'notify.tray': false,
      'notify.sound': true,
    })
  })

  it('三态项由两个开关反推，凑不出档位时显示未选', () => {
    const field = fieldOf('scan.node_check')
    expect(fieldValue(field, { scan: { verify_nodes: true, usability_check: true } })).toBe('precheck')
    expect(fieldValue(field, { scan: { verify_nodes: true, usability_check: false } })).toBe('detail')
    expect(fieldValue(field, { scan: { verify_nodes: false, usability_check: false } })).toBe('off')
    // 手工改过配置文件、两个开关凑不出任何一档：显示未选，不猜一个最近的。
    expect(fieldValue(field, { scan: { verify_nodes: false, usability_check: true } })).toBe('')
    expect(fieldPatch(field, 'detail')).toEqual({
      'scan.verify_nodes': true,
      'scan.usability_check': false,
    })
  })

  it('多个键合成一份 patch，同前缀不互相覆盖', () => {
    expect(buildPatchMany({ 'scan.verify_nodes': true, 'scan.usability_check': true })).toEqual({
      scan: { verify_nodes: true, usability_check: true },
    })
  })

  /**
   * 保存即写盘、但要重启才生效的项，界面上得说清楚。
   *
   * 这份清单与 internal/server/settings.go 的 restartRequired 是同一批。
   */
  it('需要重启的项都带了标记', () => {
    const restart = allFields
      .filter((field) => field.restart)
      .map((field) => field.path)
      .sort()
    expect(restart).toEqual([
      'advanced.log_level',
      'data.dir',
      'data.portable',
      'server.autostart',
      'server.bind',
      'server.port',
    ])
  })
})

describe('设置搜索', () => {
  it('空查询命中全部', () => {
    expect(allFields.every((field) => fieldMatches(field, '  '))).toBe(true)
  })

  it('按名称命中', () => {
    const hits = allFields.filter((field) => fieldMatches(field, '延迟'))
    expect(hits.map((field) => field.path)).toContain('scan.latency_threshold')
  })

  it('按配置路径命中，且只命中这一项', () => {
    const hits = allFields.filter((field) => fieldMatches(field, 'speed.target_qualified'))
    expect(hits.map((field) => field.path)).toEqual(['speed.target_qualified'])
  })

  /**
   * 按说明命中：用户记得的是「那个会让扫描变慢的开关」，不是它的名字。
   */
  it('按说明命中', () => {
    const hint = settingsText['scan.timeout_ms']?.hint ?? ''
    expect(hint.length).toBeGreaterThan(1)
    const hits = allFields.filter((field) => fieldMatches(field, hint.slice(0, 2)))
    expect(hits.map((field) => field.path)).toContain('scan.timeout_ms')
  })

  it('命中片段被切出来，未命中的整段保留', () => {
    expect(highlightParts('延迟上限', '延迟')).toEqual([
      { text: '延迟', hit: true },
      { text: '上限', hit: false },
    ])
    expect(highlightParts('延迟上限', '抖动')).toEqual([{ text: '延迟上限', hit: false }])
  })
})
