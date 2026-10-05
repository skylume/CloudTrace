/**
 * 任务结束时的提醒。
 *
 * 这里守的是「开关真的说了算」：这几个开关此前一个都不生效，勾不勾没区别。
 * 另外要守住「提醒失败不影响别的」——通知权限被拒、浏览器没有音频接口，
 * 都不该让任务结束这条路径抛错。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  notifyTaskEnd,
  playBeep,
  shouldNotify,
  webPermission,
  type NotifyPrefs,
} from './notify'

const ALL_OFF: NotifyPrefs = { on_done: false, on_fail: false, web: false, sound: false }
const prefs = (over: Partial<NotifyPrefs> = {}): NotifyPrefs => ({ ...ALL_OFF, ...over })

const TEXT = { title: '任务完成', body: '扫描完成，共 34 个节点' }

/** 装一个假的 Notification，返回收到的调用记录。 */
function stubNotification(permission: NotificationPermission) {
  const sent: Array<{ title: string; body?: string }> = []
  class FakeNotification {
    static permission = permission
    static requestPermission = vi.fn(async () => permission)
    constructor(title: string, options?: { body?: string }) {
      sent.push({ title, body: options?.body })
    }
  }
  vi.stubGlobal('Notification', FakeNotification)
  return sent
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('该不该提醒', () => {
  it('完成只受 on_done 控制', () => {
    expect(shouldNotify('done', prefs({ on_done: true }))).toBe(true)
    expect(shouldNotify('done', prefs({ on_fail: true }))).toBe(false)
  })

  it('失败只受 on_fail 控制', () => {
    expect(shouldNotify('failed', prefs({ on_fail: true }))).toBe(true)
    expect(shouldNotify('failed', prefs({ on_done: true }))).toBe(false)
  })

  // 配置还没到（首屏、断线）时不该提醒：那时读到的全是默认值，
  // 按默认值弹一次提醒等于替用户做了决定。
  it('没有配置时不提醒', () => {
    expect(shouldNotify('done', null)).toBe(false)
    expect(shouldNotify('done', undefined)).toBe(false)
  })
})

describe('发提醒', () => {
  it('开关没开时一个渠道都不走', () => {
    stubNotification('granted')
    expect(notifyTaskEnd('done', prefs({ on_done: true }), TEXT)).toEqual([])
  })

  it('浏览器通知已授权时发一条', () => {
    const sent = stubNotification('granted')

    const used = notifyTaskEnd('done', prefs({ on_done: true, web: true }), TEXT)

    expect(used).toEqual(['web'])
    expect(sent).toHaveLength(1)
    const first = sent.at(0)
    expect(first?.title).toBe('任务完成')
    expect(first?.body).toBe('扫描完成，共 34 个节点')
  })

  /**
   * 没授权就静默跳过。
   *
   * 提醒是锦上添花，不该因为它弹一个错误框——那比不提醒更烦。
   */
  it('浏览器通知未授权时跳过，不报错', () => {
    const sent = stubNotification('denied')

    const used = notifyTaskEnd('done', prefs({ on_done: true, web: true }), TEXT)

    expect(used).toEqual([])
    expect(sent).toHaveLength(0)
  })

  it('完成提醒不会因为失败开关而发出', () => {
    stubNotification('granted')
    expect(notifyTaskEnd('done', prefs({ on_fail: true, web: true }), TEXT)).toEqual([])
  })

  it('失败提醒走 on_fail', () => {
    const sent = stubNotification('granted')

    const used = notifyTaskEnd('failed', prefs({ on_fail: true, web: true }), TEXT)

    expect(used).toEqual(['web'])
    expect(sent).toHaveLength(1)
  })
})

describe('提示音', () => {
  /**
   * 没有音频接口时安静地失败。
   *
   * jsdom 里就没有 AudioContext，而真实环境里也可能被策略禁掉。这条路径
   * 不能抛错——它是在任务结束的回调里跑的。
   */
  it('没有音频接口时返回 false 而不抛错', () => {
    expect(() => playBeep()).not.toThrow()
    expect(playBeep()).toBe(false)
  })

  it('提示音渠道不可用时不计入已用渠道', () => {
    stubNotification('granted')
    const used = notifyTaskEnd('done', prefs({ on_done: true, sound: true }), TEXT)
    expect(used).toEqual([])
  })
})

describe('通知权限', () => {
  it('浏览器不支持时报 unsupported', () => {
    vi.stubGlobal('Notification', undefined)
    expect(webPermission()).toBe('unsupported')
  })

  it('支持时返回当前权限', () => {
    stubNotification('granted')
    expect(webPermission()).toBe('granted')
  })
})
