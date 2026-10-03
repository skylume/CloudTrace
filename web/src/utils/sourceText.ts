/**
 * 来源文本解析：把用户粘进来的内容分类计数。
 *
 * 纯前端解析，不发任何请求——用户边打字边看到「识别到 N 个网段」，才能
 * 确认自己粘对了。等后端回话会有明显延迟，那种「输入时毫无反应」正是让
 * 人反复检查的原因。
 *
 * 支持的形态与后端一致：CIDR / 单 IP / `IP:port` / `[v6]:port` / `a-b` 区间 /
 * 域名 / `http(s)://` 地址 / `#` 注释。
 */

export interface SourcePreview {
  /** 网段（CIDR）。 */
  cidrs: number
  /** 单点：单个 IP、带端口的 IP、以及 `a-b` 区间。 */
  single: number
  /** 域名与 URL。 */
  hosts: number
  /** 注释行。 */
  comments: number
  /** 认不出的行，原样给出便于提示用户。 */
  invalid: string[]
}

const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/
const IPV6 = /^[0-9a-f:]+$/i
/** 域名：至少两段，末段是字母开头的顶级域。 */
const HOST = /^(?=.{1,253}$)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$/i
/** 数字区间：1.1.1.1-1.1.1.10 或 1.1.1.1-10。 */
const RANGE = /^\S+\s*-\s*\S+$/

/** 判断一个不带端口的字符串是不是 IP。 */
function isIP(text: string): boolean {
  if (IPV4.test(text)) {
    return text.split('.').every((part) => Number(part) <= 255)
  }
  // IPv6 至少有 2 个冒号，否则会和 `IP:port` 混淆。
  return text.includes(':') && text.split(':').length >= 3 && IPV6.test(text)
}

/** 拆掉 `[v6]:port` 或 `host:port` 里的端口部分。 */
function stripPort(text: string): string {
  if (text.startsWith('[')) {
    const end = text.indexOf(']')
    return end > 0 ? text.slice(1, end) : text
  }
  // IPv6 自身带冒号，只有「最后一个冒号后面全是数字」才算端口。
  const index = text.lastIndexOf(':')
  if (index > 0 && /^\d+$/.test(text.slice(index + 1)) && !text.slice(0, index).includes(':')) {
    return text.slice(0, index)
  }
  return text
}

/**
 * 解析一段来源文本。
 *
 * 空行直接跳过，不计数也不报错——用户粘贴时带上空行是常事。
 */
export function parseSourceText(text: string): SourcePreview {
  const preview: SourcePreview = { cidrs: 0, single: 0, hosts: 0, comments: 0, invalid: [] }

  for (const raw of text.split('\n')) {
    const line = raw.trim()
    if (line === '') continue
    if (line.startsWith('#') || line.startsWith('//')) {
      preview.comments += 1
      continue
    }

    // URL 一律算作「远端来源」的形态之一，用户可能把订阅地址粘到这里。
    if (line.includes('://')) {
      preview.hosts += 1
      continue
    }

    const slash = line.indexOf('/')
    if (slash > 0) {
      const address = line.slice(0, slash)
      const prefix = line.slice(slash + 1)
      if (isIP(address) && /^\d{1,3}$/.test(prefix)) {
        preview.cidrs += 1
        continue
      }
      // 带路径的裸域名（example.com/list.txt）也按远端来源处理。
      if (HOST.test(address)) {
        preview.hosts += 1
        continue
      }
    }

    const bare = stripPort(line)
    if (isIP(bare)) {
      preview.single += 1
      continue
    }

    if (RANGE.test(line)) {
      preview.single += 1
      continue
    }

    if (HOST.test(line)) {
      preview.hosts += 1
      continue
    }

    preview.invalid.push(line)
  }

  return preview
}

/** 来源文本里一共有多少条可用条目。 */
export function sourceEntryCount(preview: SourcePreview): number {
  return preview.cidrs + preview.single + preview.hosts
}
