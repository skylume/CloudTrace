/**
 * 极简 i18n：够用即可，不引第三方。
 *
 * 只需要「查表 + 占位符替换 + 回退」三件事，为此拉一个库进来不划算。
 * 文案从第一天就走这里，避免后期从各处硬编码的中文里往回捞。
 *
 * translate 读的是 locale 这个 ref，因此在模板里调用它会自动建立依赖：
 * 切语言时用到它的组件会重渲染，不需要额外的响应式包装。
 */
import { ref } from 'vue'

import { en } from './en'
import { zh } from './zh'

export type Locale = 'zh' | 'en'

/** 文案键。用 zh 的键集合当基准，en 少一个键在类型层面就会报错。 */
export type MessageKey = keyof typeof zh

const dictionaries: Record<Locale, Record<string, string>> = { zh, en }

const locale = ref<Locale>('zh')

/** 参数替换：{name} 形式，避免字符串拼接带来的语序问题。 */
export function translate(key: string, params?: Record<string, string | number>): string {
  const dict = dictionaries[locale.value]
  // 回退顺序：当前语言 → 中文 → 键本身。键本身至少能让问题暴露出来，
  // 而不是显示成空白。
  const template = dict[key] ?? dictionaries.zh[key] ?? key
  if (!params) return template
  return template.replace(/\{(\w+)\}/g, (match, name: string) => {
    const value = params[name]
    return value === undefined ? match : String(value)
  })
}

/** t 是组件里用的翻译函数。 */
export const t = translate

export function setLocale(next: Locale): void {
  locale.value = next
  document.documentElement.lang = next === 'zh' ? 'zh-CN' : 'en'
}

export function currentLocale(): Locale {
  return locale.value
}

/**
 * 把秒数渲染成「N 秒」或「M 分 N 秒」。
 *
 * 放在文案层而不是工具层：换算可以共用，但「几秒怎么说」是文案的事。
 * 两个地方各拼一遍的话，改一次措辞就得记得改两处。
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
