/**
 * 任务结束时的提醒。
 *
 * 这些开关的意义是「我没在看窗口的时候也能知道跑完了」：一次扫描要跑几分钟，
 * 用户多半会切去做别的事。所以它和界面上那条完成提示不是一回事——后者只在
 * 用户正看着的时候有用，而这个要在他没看的时候把他叫回来。
 *
 * 两个维度是正交的：
 *   - `on_done` / `on_fail` 决定「这次结束值不值得提醒」；
 *   - `web` / `sound` 决定「用什么提醒」。托盘那一档在 Go 侧实现（桌面壳）。
 */

/** 值得提醒的两种结束方式。 */
export type NotifyOutcome = 'done' | 'failed'

/** 只取用得到的几项，避免这个模块依赖整个配置类型。 */
export interface NotifyPrefs {
  on_done: boolean
  on_fail: boolean
  web: boolean
  sound: boolean
}

/** 提醒的文案，由调用方按当前语言拼好传进来。 */
export interface NotifyText {
  title: string
  body: string
}

/**
 * 该不该为这次结束提醒。
 *
 * 中途停止（aborted）不在此列：那是用户自己按的停止，他本来就知道停了——
 * 再提醒一次只是噪音。
 */
export function shouldNotify(
  outcome: NotifyOutcome,
  prefs: NotifyPrefs | null | undefined,
): boolean {
  if (!prefs) return false
  return outcome === 'done' ? prefs.on_done === true : prefs.on_fail === true
}

/** 浏览器通知的当前权限；浏览器不支持时返回 `unsupported`。 */
export function webPermission(): NotificationPermission | 'unsupported' {
  const api = notificationAPI()
  return api ? api.permission : 'unsupported'
}

/**
 * 申请浏览器通知权限。
 *
 * 由设置页在用户打开那个开关时调用——申请权限需要一个用户手势，而任务结束时
 * 没有手势可借。用户之前拒绝过就直接返回，不再问第二遍：反复弹窗只会让人
 * 把整个站点的通知永久关掉。
 */
export async function requestWebPermission(): Promise<NotificationPermission | 'unsupported'> {
  const current = webPermission()
  if (current === 'unsupported' || current === 'denied') return current

  const api = notificationAPI()
  if (!api) return 'unsupported'

  try {
    return await api.requestPermission()
  } catch {
    // 旧实现只支持回调形式，抛错就当作没拿到。
    return webPermission()
  }
}

/**
 * 播一声提示音。
 *
 * 用 Web Audio 合成而不是音频文件：项目约定不引任何外部资源，而一段几百毫秒
 * 的提示音做成 wav 也要占进二进制。返回是否真的播了。
 */
export function playBeep(): boolean {
  const ctx = audioContext()
  if (!ctx) return false

  // 用户手势之前浏览器会把音频挂起。扫描是点出来的，通常已经解锁过了；
  // 真被挂起时 resume 一下，不保证这次响，但下一次会。
  if (ctx.state === 'suspended') void ctx.resume()

  const now = ctx.currentTime
  const osc = ctx.createOscillator()
  const gain = ctx.createGain()

  osc.type = 'sine'
  osc.frequency.setValueAtTime(880, now)
  // 两端淡入淡出：直接开关会「啪」一声，那是波形跳变，不是提示音。
  gain.gain.setValueAtTime(0.0001, now)
  gain.gain.exponentialRampToValueAtTime(0.15, now + 0.02)
  gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.28)

  osc.connect(gain)
  gain.connect(ctx.destination)
  osc.start(now)
  osc.stop(now + 0.3)
  return true
}

/**
 * 按配置发一次任务结束提醒。
 *
 * 返回实际用到的渠道，便于调用方（和用例）确认哪一路真的走了。
 */
export function notifyTaskEnd(
  outcome: NotifyOutcome,
  prefs: NotifyPrefs | null | undefined,
  text: NotifyText,
): string[] {
  if (!shouldNotify(outcome, prefs) || !prefs) return []

  const used: string[] = []
  if (prefs.web && showWebNotification(text)) used.push('web')
  if (prefs.sound && playBeep()) used.push('sound')
  return used
}

/**
 * 发一条浏览器通知。返回是否真的发出去了。
 *
 * 没授权就静默跳过：提醒是锦上添花，不该因为它弹一个错误框——那比不提醒更烦。
 */
function showWebNotification(text: NotifyText): boolean {
  const api = notificationAPI()
  if (!api || api.permission !== 'granted') return false

  try {
    // tag 让后一条覆盖前一条：连着跑两次时，用户要看到的是最新的那一次，
    // 而不是叠在通知中心里的两条。
    new api(text.title, { body: text.body, tag: 'cloudtrace-task' })
    return true
  } catch {
    return false
  }
}

/**
 * 取浏览器的通知接口，没有就返回 null。
 *
 * 不用 `'Notification' in window` 判断：那个属性可能被定义成 `undefined`
 * （测试替身、某些加固过的浏览器都会这样），此时 `in` 为真而取用会直接抛错。
 * 取值再判类型才是稳的。
 */
function notificationAPI(): typeof Notification | null {
  if (typeof window === 'undefined') return null
  const api = (window as { Notification?: typeof Notification }).Notification
  return typeof api === 'function' ? api : null
}

/**
 * 共享的音频上下文。
 *
 * 懒创建而不是模块加载时就建：AudioContext 在某些浏览器里一开始是 suspended，
 * 建早了只会白占一个音频通道，而绝大多数会话根本不会开提示音。
 */
let audio: AudioContext | null = null

function audioContext(): AudioContext | null {
  if (audio) return audio
  if (typeof window === 'undefined') return null

  const ctor =
    window.AudioContext ??
    (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
  if (!ctor) return null

  audio = new ctor()
  return audio
}
