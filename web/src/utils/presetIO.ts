/**
 * 档位的导入与导出。
 *
 * 导出的是**自定义档位**，不含内置三档：内置档位由后端代码定义，跟着文件跑到
 * 别人机器上只会变成一份过期的拷贝，而那份拷贝还会顶着内置的名字。
 *
 * 导入只做结构检查，值的合法性交给后端：前端再判一遍范围，两边迟早会分叉，
 * 而分叉的表现是「这里过了、保存时被拒」。
 */
import type { Preset } from '@/api/types'

/** 导出文件的形状。带版本号是为了以后改结构时能认出旧文件。 */
interface PresetFile {
  version: number
  presets: Preset[]
}

const FILE_VERSION = 1

/** 把自定义档位序列化成一份可分享的文本。 */
export function exportPresets(presets: Preset[]): string {
  const file: PresetFile = {
    version: FILE_VERSION,
    presets: presets.filter((item) => !item.builtin),
  }
  return JSON.stringify(file, null, 2)
}

/** 认不出来的东西一律拒绝，并说明是哪一条。 */
function toPreset(raw: unknown, index: number): Preset {
  if (raw === null || typeof raw !== 'object') throw new Error(`第 ${index + 1} 项不是对象`)
  const item = raw as Record<string, unknown>

  const name = typeof item.name === 'string' ? item.name.trim() : ''
  if (name === '') throw new Error(`第 ${index + 1} 项没有名称`)

  const values = item.values
  if (values === null || typeof values !== 'object' || Array.isArray(values)) {
    throw new Error(`第 ${index + 1} 项没有参数值`)
  }
  if (Object.keys(values).length === 0) throw new Error(`第 ${index + 1} 项的参数值是空的`)

  return {
    // 标识原样保留，但导入方会用 uniquePresetID 换一个新标识：文件是从别人那
    // 儿来的，直接沿用会把本机同标识的档位顶掉。
    id: typeof item.id === 'string' ? item.id : '',
    name,
    note: typeof item.note === 'string' ? item.note : '',
    builtin: false,
    icon: typeof item.icon === 'string' ? item.icon : '',
    color: typeof item.color === 'string' ? item.color : '',
    order: typeof item.order === 'number' ? item.order : 0,
    values: values as Record<string, unknown>,
  }
}

/**
 * 解析一份导入文件。
 *
 * 同时接受两种形状：带 version 的导出文件，以及一个裸的档位数组——后者是用户
 * 手写或从别处复制来的常见形态，为它单独报错没有意义。
 */
export function parsePresets(text: string): Preset[] {
  let raw: unknown
  try {
    raw = JSON.parse(text)
  } catch {
    throw new Error('不是合法的 JSON')
  }

  const list = Array.isArray(raw)
    ? raw
    : raw !== null && typeof raw === 'object' && Array.isArray((raw as PresetFile).presets)
      ? (raw as PresetFile).presets
      : null

  if (list === null) throw new Error('文件里没有档位列表')
  if (list.length === 0) throw new Error('档位列表是空的')
  return list.map(toPreset)
}

/**
 * 生成一个不会与已有档位撞车的标识。
 *
 * 导入的档位沿用原标识时，第二次导入同一份文件会覆盖掉第一次的结果；换一个
 * 新标识则每次导入都是一份新档位，符合「导入 = 拿进来用」的预期。
 */
export function uniquePresetID(base: string, taken: Set<string>): string {
  const seed = base.trim() === '' ? `imported-${Date.now().toString(36)}` : base
  if (!taken.has(seed)) return seed
  for (let n = 2; n < 1000; n += 1) {
    const candidate = `${seed}-${n}`
    if (!taken.has(candidate)) return candidate
  }
  return `${seed}-${Date.now().toString(36)}`
}
