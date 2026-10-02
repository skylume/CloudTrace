/**
 * 离线守卫：前端不得引用任何远程资源。
 *
 * 全部资源由构建打包进产物、随二进制一起分发：少一轮 DNS 与 TLS 握手，
 * 首屏更快；也不受对方站点可用性、限流、被墙的影响。字体用系统字体栈，
 * 图标用内联 SVG，因此连字体文件都不需要下载。
 *
 * 这条约束靠用例守住，而不是靠自觉：一个从文档里复制来的 CDN 链接就能
 * 悄悄把它破坏掉，而它带来的延迟只有在用户那边才感觉得到。
 */
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

import { describe, expect, it } from 'vitest'

/**
 * 用工作目录而不是 import.meta.url 定位项目根：后者在 Vite 里会被改写成
 * 带 /@fs/ 前缀的内部地址，跨平台拼接路径很容易出错。用例由 npm 脚本在
 * web/ 下运行，工作目录就是这里。
 */
const ROOT = process.cwd()

/** 会被扫描的文件：入口页、静态目录、源码。测试文件自身不参与。 */
function collectFiles(): string[] {
  const out: string[] = []
  const walk = (dir: string): void => {
    for (const name of readdirSync(dir)) {
      if (name === 'node_modules' || name === 'dist' || name === '.npm-cache') continue
      const full = join(dir, name)
      if (statSync(full).isDirectory()) {
        walk(full)
        continue
      }
      if (/\.(vue|ts|css|html|svg)$/.test(name) && !name.endsWith('.spec.ts')) {
        out.push(full)
      }
    }
  }
  walk(join(ROOT, 'src'))
  walk(join(ROOT, 'public'))
  out.push(join(ROOT, 'index.html'))
  return out
}

/** 远程资源的引用方式。只匹配「会去加载远程内容」的写法，普通的文本链接不算。 */
const FORBIDDEN: { name: string; pattern: RegExp }[] = [
  { name: '远程样式表', pattern: /<link[^>]+href\s*=\s*["']https?:\/\//i },
  {
    name: '远程脚本 / 图片 / 字体文件',
    pattern: /(?:src|href)\s*=\s*["']https?:\/\/[^"']+\.(?:js|mjs|css|woff2?|ttf|otf|png|jpe?g|svg|gif|ico)/i,
  },
  { name: '远程 @import', pattern: /@import[^;]*https?:\/\//i },
  { name: '远程 url()', pattern: /url\(\s*["']?https?:\/\//i },
  { name: '远程 ES 模块', pattern: /(?:from|import)\s*\(?\s*["']https?:\/\//i },
  { name: '远程接口调用', pattern: /fetch\s*\(\s*["']https?:\/\//i },
]

describe('前端不引用远程资源', () => {
  const files = collectFiles()

  it('至少扫到了入口页与源码', () => {
    expect(existsSync(join(ROOT, 'index.html'))).toBe(true)
    expect(files.length).toBeGreaterThan(5)
    expect(files.some((file) => file.endsWith('index.html'))).toBe(true)
  })

  it.each(FORBIDDEN)('没有 $name', ({ pattern }) => {
    const offenders: string[] = []
    for (const file of files) {
      const text = readFileSync(file, 'utf8')
      text.split('\n').forEach((line, index) => {
        if (pattern.test(line)) {
          offenders.push(`${relative(ROOT, file)}:${index + 1}  ${line.trim()}`)
        }
      })
    }
    expect(offenders).toEqual([])
  })
})

describe('字体不依赖外部文件', () => {
  it('只用系统字体栈', () => {
    const tokens = readFileSync(join(ROOT, 'src/styles/tokens.css'), 'utf8')
    expect(tokens).toMatch(/--font-sans:/)
    expect(tokens).not.toMatch(/@font-face/)
    expect(tokens).not.toMatch(/\.woff/)
  })
})
