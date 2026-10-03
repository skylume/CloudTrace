/**
 * 配色的对比度实测。
 *
 * 设计文档把「任何文字不低于 4.5:1」列为验收项，而这件事**不能靠肉眼判断**：
 * 旧项目曾把次级文字定成 2.9:1，在深色底上看起来「还行」，实际读起来很累。
 *
 * 所以这里直接解析样式表、按 WCAG 公式算真实比值。配色改了、对比度掉了，
 * 用例会立刻报出来——这比在文档里写一句「注意对比度」有用得多。
 */
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

const CSS = readFileSync(join(process.cwd(), 'src/styles/themes.css'), 'utf8')

type RGB = [number, number, number]

/** 从样式表里取出一个选择器块里的所有变量。 */
function varsIn(selector: string): Record<string, string> {
  const index = CSS.indexOf(selector)
  if (index < 0) throw new Error(`样式表里找不到 ${selector}`)
  const open = CSS.indexOf('{', index)
  const close = CSS.indexOf('}', open)
  const body = CSS.slice(open + 1, close)

  const out: Record<string, string> = {}
  for (const line of body.split('\n')) {
    const match = /^\s*(--[a-z-]+):\s*([^;]+);/.exec(line)
    if (match?.[1] && match[2]) out[match[1]] = match[2].trim()
  }
  return out
}

/** 解析 #rgb / #rrggbb / rgb(r g b) / rgb(r g b / a)。 */
function parseColor(value: string): { rgb: RGB; alpha: number } {
  const hex = /^#([0-9a-f]{6})$/i.exec(value)
  if (hex?.[1]) {
    const n = parseInt(hex[1], 16)
    return { rgb: [(n >> 16) & 255, (n >> 8) & 255, n & 255], alpha: 1 }
  }

  const rgb = /^rgb\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*(?:\/\s*([\d.]+)%)?\s*\)$/.exec(value)
  if (rgb) {
    return {
      rgb: [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])],
      alpha: rgb[4] === undefined ? 1 : Number(rgb[4]) / 100,
    }
  }

  throw new Error(`认不出的颜色写法：${value}`)
}

/** 把带透明度的颜色叠在底色上——屏幕上看到的是叠完的结果，不是原色。 */
function composite(fg: { rgb: RGB; alpha: number }, bg: RGB): RGB {
  if (fg.alpha >= 1) return fg.rgb
  return fg.rgb.map((channel, i) => channel * fg.alpha + (bg[i] ?? 0) * (1 - fg.alpha)) as RGB
}

/** WCAG 相对亮度。 */
function luminance([r, g, b]: RGB): number {
  const channel = (value: number) => {
    const v = value / 255
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
}

/** 对比度比值。 */
function contrast(fg: { rgb: RGB; alpha: number }, bg: RGB): number {
  const f = luminance(composite(fg, bg))
  const b = luminance(bg)
  const [light, dark] = f > b ? [f, b] : [b, f]
  return (light + 0.05) / (dark + 0.05)
}

/** 四套主题：基础两套 + 高对比度覆盖后的两套。 */
function themeVars(theme: 'light' | 'dark', high: boolean): Record<string, string> {
  const base = varsIn(`:root[data-theme='${theme}']`)
  if (!high) return base
  return { ...base, ...varsIn(`:root[data-contrast='high'][data-theme='${theme}']`) }
}

const THEMES = [
  { name: '浅色', theme: 'light' as const, high: false },
  { name: '深色', theme: 'dark' as const, high: false },
  { name: '浅色 + 高对比度', theme: 'light' as const, high: true },
  { name: '深色 + 高对比度', theme: 'dark' as const, high: true },
]

/** 需要在每种底色上检查的文字色。阈值按设计文档：正文 7:1，其余 4.5:1。 */
const TEXT_ON: { token: string; min: number }[] = [
  { token: '--color-text', min: 7 },
  { token: '--color-text-muted', min: 4.5 },
  { token: '--color-text-subtle', min: 4.5 },
  { token: '--color-ok', min: 4.5 },
  { token: '--color-warn', min: 4.5 },
  { token: '--color-bad', min: 4.5 },
  { token: '--latency-fast', min: 4.5 },
  { token: '--latency-mid', min: 4.5 },
  { token: '--latency-slow', min: 4.5 },
  { token: '--latency-dead', min: 4.5 },
]

/** 文字会出现在这三种底色上。 */
const SURFACES = ['--color-bg', '--color-surface', '--color-surface-sunken']

describe.each(THEMES)('$name 的对比度', ({ theme, high }) => {
  const vars = themeVars(theme, high)

  it.each(SURFACES)('文字在 %s 上不低于阈值', (surface) => {
    const raw = vars[surface]
    expect(raw, `缺少 ${surface}`).toBeTruthy()
    const bg = parseColor(raw!).rgb

    const failures: string[] = []
    for (const { token, min } of TEXT_ON) {
      const value = vars[token]
      if (!value) continue
      const ratio = contrast(parseColor(value), bg)
      if (ratio < min) {
        failures.push(`${token} 在 ${surface} 上只有 ${ratio.toFixed(2)}:1（要求 ≥ ${min}）`)
      }
    }
    expect(failures).toEqual([])
  })

  it('主色上的文字清晰可读', () => {
    const onPrimary = vars['--color-on-primary']
    const primary = vars['--color-primary']
    expect(onPrimary).toBeTruthy()
    expect(primary).toBeTruthy()

    const ratio = contrast(parseColor(onPrimary!), parseColor(primary!).rgb)
    expect(ratio, `主按钮上的文字只有 ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(4.5)
  })

  it('浅底徽章上的文字清晰可读', () => {
    // 语义色是「淡底 + 深字」，淡底要叠在卡片底色上才是实际看到的颜色。
    const surface = parseColor(vars['--color-surface']!).rgb
    const pairs: [string, string][] = [
      ['--color-ok', '--color-ok-bg'],
      ['--color-warn', '--color-warn-bg'],
      ['--color-bad', '--color-bad-bg'],
      ['--color-primary-text', '--color-primary-soft'],
    ]

    const failures: string[] = []
    for (const [fgToken, bgToken] of pairs) {
      const fg = vars[fgToken]
      const bg = vars[bgToken]
      if (!fg || !bg) continue
      const composited = composite(parseColor(bg), surface)
      const ratio = contrast(parseColor(fg), composited)
      if (ratio < 4.5) {
        failures.push(`${fgToken} 在 ${bgToken} 上只有 ${ratio.toFixed(2)}:1`)
      }
    }
    expect(failures).toEqual([])
  })
})

describe('样式表结构', () => {
  it('四套主题都有定义', () => {
    for (const selector of [
      ":root[data-theme='light']",
      ":root[data-theme='dark']",
      ":root[data-contrast='high'][data-theme='light']",
      ":root[data-contrast='high'][data-theme='dark']",
    ]) {
      expect(() => varsIn(selector)).not.toThrow()
    }
  })

  it('高对比度只覆盖颜色，不复制整套几何变量', () => {
    const high = varsIn(":root[data-contrast='high'][data-theme='light']")
    const geometry = Object.keys(high).filter((key) => key.startsWith('--radius') || key.startsWith('--space'))
    expect(geometry).toEqual([])
  })
})
