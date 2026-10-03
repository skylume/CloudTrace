/**
 * 来源文本解析。
 *
 * 这里的每一条都对应一种用户真的会粘进来的写法。解析错了不会报错，只会让
 * 用户以为「我粘的东西没生效」——所以宁可多测几种形态。
 */
import { describe, expect, it } from 'vitest'

import { parseSourceText, sourceEntryCount } from './sourceText'

describe('parseSourceText', () => {
  it('认出网段、单点、域名与注释', () => {
    const preview = parseSourceText(
      ['104.16.0.0/12', '1.1.1.1', '1.1.1.1:2053', 'cf.example.com', '# 注释', '// 另一种注释'].join('\n'),
    )
    expect(preview.cidrs).toBe(1)
    expect(preview.single).toBe(2)
    expect(preview.hosts).toBe(1)
    expect(preview.comments).toBe(2)
    expect(preview.invalid).toEqual([])
  })

  it('IPv6 与其带端口的写法都算单点', () => {
    const preview = parseSourceText(['2606:4700::1', '[2606:4700::1]:8443'].join('\n'))
    expect(preview.single).toBe(2)
    expect(preview.invalid).toEqual([])
  })

  it('数字区间算单点', () => {
    expect(parseSourceText('1.1.1.1-1.1.1.10').single).toBe(1)
    expect(parseSourceText('1.1.1.1 - 10').single).toBe(1)
  })

  it('URL 与带路径的域名都算远端形态', () => {
    const preview = parseSourceText(['https://example.com/list.txt', 'example.com/ips-v4.txt'].join('\n'))
    expect(preview.hosts).toBe(2)
    expect(preview.invalid).toEqual([])
  })

  it('空行忽略，不算注释也不算无效', () => {
    const preview = parseSourceText('\n\n  \n1.1.1.1\n\n')
    expect(preview.single).toBe(1)
    expect(preview.comments).toBe(0)
    expect(preview.invalid).toEqual([])
  })

  it('认不出来的行原样返回，便于提示用户', () => {
    const preview = parseSourceText(['这不是地址', '1.1.1.1'].join('\n'))
    expect(preview.invalid).toEqual(['这不是地址'])
    expect(preview.single).toBe(1)
  })

  it('IPv4 段超过 255 不算地址', () => {
    expect(parseSourceText('999.1.1.1').invalid).toEqual(['999.1.1.1'])
  })

  it('前缀长度不是数字时不算网段', () => {
    expect(parseSourceText('1.1.1.0/abc').invalid).toEqual(['1.1.1.0/abc'])
  })

  it('空文本什么也不报', () => {
    const preview = parseSourceText('')
    expect(sourceEntryCount(preview)).toBe(0)
    expect(preview.invalid).toEqual([])
  })

  it('条目总数不含注释与无效行', () => {
    const preview = parseSourceText(['# 说明', '1.1.1.0/24', '2.2.2.2', '坏行'].join('\n'))
    expect(sourceEntryCount(preview)).toBe(2)
  })
})
