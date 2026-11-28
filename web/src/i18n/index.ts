/**
 * 极简 i18n：只做「查表 + 占位符替换」。
 *
 * 界面只有中文一套文案，不再维护第二份翻译：每加一个设置项就同步两种语言，
 * 是纯维护成本。translate 保留成函数而不是直接导出字典，是为了让调用点
 * 统一走 {name} 占位符，避免字符串拼接。
 */
import { zh } from './zh'

/** 文案键。zh 是唯一基准。 */
export type MessageKey = keyof typeof zh

/** 参数替换：{name} 形式，避免字符串拼接带来的语序问题。 */
export function translate(key: string, params?: Record<string, string | number>): string {
  const template = (zh as Record<string, string>)[key] ?? key
  if (!params) return template
  return template.replace(/\{(\w+)\}/g, (match: string, name: string) => {
    const value = params[name]
    return value === undefined ? match : String(value)
  })
}

/** t 是组件里用的翻译函数。 */
export const t = translate

/**
 * 把秒数渲染成「N 秒」或「M 分 N 秒」。
 *
 * 放在文案层而不是工具层：换算可以共用，但「几秒怎么说」是文案的事。
 */
export function durationText(total: number): string {
  if (!Number.isFinite(total) || total <= 0) return '—'
  if (total < 60) return translate('time.seconds', { value: Math.round(total) })
  const minutes = Math.floor(total / 60)
  return translate('time.minutesSeconds', {
    minutes,
    seconds: Math.round(total - minutes * 60),
  })
}

/**
 * 把毫秒渲染成「N 毫秒」或「N 秒」。
 *
 * 十秒以内保留一位小数：1500 毫秒说成「2 秒」会显得慢了一截，而失败时
 * 这个数恰恰是用户拿来判断「是刚起来就挂，还是跑到一半才挂」的依据。
 */
export function durationMsText(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return '—'
  if (ms < 1000) return translate('time.milliseconds', { value: Math.round(ms) })
  if (ms < 10_000) return translate('time.seconds', { value: Number((ms / 1000).toFixed(1)) })
  return durationText(ms / 1000)
}

/** 阶段名翻成人话；认不出的阶段原样返回，别让界面露出 i18n 键。 */
export function phaseText(phase: string): string {
  if (phase === '') return ''
  const key = `task.phase.${phase}`
  const translated = translate(key)
  return translated === key ? phase : translated
}
