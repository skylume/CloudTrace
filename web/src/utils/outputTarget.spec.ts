import { describe, expect, it, vi, afterEach } from 'vitest'

import { api } from '@/api/rest'
import { canShareFile, pickOutputTarget } from './outputTarget'

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('输出目标', () => {
  it('服务端地址交给浏览器下载，前端不碰本地路径', () => {
    const download = vi.spyOn(api, 'download').mockImplementation(() => {})
    const file = { name: 'result.csv', url: '/api/download/abc' }
    expect(pickOutputTarget(file).id).toBe('download')
    pickOutputTarget(file).save(file)
    expect(download).toHaveBeenCalledWith('/api/download/abc', 'result.csv')
  })

  it('没有分享能力时，blob 也走下载', () => {
    vi.stubGlobal('navigator', {})
    const blob = new Blob(['{}'], { type: 'application/json' })
    expect(canShareFile({ name: 'a.json', blob })).toBe(false)
    expect(pickOutputTarget({ name: 'a.json', blob }).id).toBe('download')
  })

  it('能分享文件时改走系统分享', () => {
    const share = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { canShare: () => true, share })
    const blob = new Blob(['{}'], { type: 'application/json' })
    const file = { name: 'a.json', blob }
    expect(pickOutputTarget(file).id).toBe('share')
    pickOutputTarget(file).save(file)
    expect(share).toHaveBeenCalledTimes(1)
    const sent = (share.mock.calls[0]![0] as { files: File[] }).files[0]!
    expect(sent.name).toBe('a.json')
  })
})
