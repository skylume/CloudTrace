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

import { SETTING_GROUPS, settingsText } from './settingsSchema'

const declared = SETTING_GROUPS.flatMap((group) => group.fields.map((field) => field.path))

describe('设置项声明', () => {
  it('每一项在中英文里都有文案', () => {
    for (const path of declared) {
      expect(settingsText.zh[path], `中文缺文案：${path}`).toBeDefined()
      expect(settingsText.en[path], `英文缺文案：${path}`).toBeDefined()
    }
  })

  it('文案里没有已经不存在的设置项', () => {
    const known = new Set(declared)
    for (const locale of ['zh', 'en'] as const) {
      for (const path of Object.keys(settingsText[locale])) {
        expect(known.has(path), `${locale} 里有孤儿文案：${path}`).toBe(true)
      }
    }
  })

  it('路径不重复：重复声明只有第一个会生效', () => {
    const seen = new Set<string>()
    for (const path of declared) {
      expect(seen.has(path), `路径重复：${path}`).toBe(false)
      seen.add(path)
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
})
