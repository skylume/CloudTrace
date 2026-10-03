/**
 * 全局快捷键。
 *
 * 两条容易做错的规矩：
 *   1. **在输入框里打字时不能触发**。用户在一个来源框里按 Ctrl+F 想找文字，
 *      结果被当成「表格搜索」，这类误触会让人不敢用快捷键。
 *   2. **必须能卸载**。全局监听器漏掉清理，切页时会一层层叠上去，同一次按键
 *      被执行多次——这种 bug 在开发时完全看不出来。
 */
import { onBeforeUnmount, onMounted, type Ref } from 'vue'

export interface Hotkey {
  /** 主键，小写；字母键写字母，功能键写 'enter' / 'escape' / 'arrowdown'。 */
  key: string
  /** 是否需要 Ctrl（macOS 上同时接受 Command）。 */
  ctrl?: boolean
  shift?: boolean
  handler: () => void
  /**
   * 允许在输入框、文本域、下拉里触发。
   *
   * 只有 Esc 这类「退出」语义的键才该打开它——其余键在输入框里都有原本的
   * 用途。
   */
  allowInInput?: boolean
}

/** 判断事件目标是不是可编辑元素。 */
function isEditable(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT'
}

/**
 * 注册一组快捷键。
 *
 * 同一时刻只应有一处调用它（应用根组件）。多处注册会让同一次按键被处理多次。
 */
export function useHotkeys(
  hotkeys: Hotkey[] | Ref<Hotkey[]>,
  options: { enabled?: Ref<boolean> } = {},
): void {
  function resolve(): Hotkey[] {
    return Array.isArray(hotkeys) ? hotkeys : hotkeys.value
  }

  function onKeydown(event: KeyboardEvent): void {
    if (options.enabled && !options.enabled.value) return

    const editable = isEditable(event.target)
    const key = event.key.toLowerCase()
    // macOS 上 Command 也当 Ctrl 用：两套都认，用户不必记两遍。
    const ctrl = event.ctrlKey || event.metaKey

    for (const hotkey of resolve()) {
      if (hotkey.key !== key) continue
      if (Boolean(hotkey.ctrl) !== ctrl) continue
      if (Boolean(hotkey.shift) !== event.shiftKey) continue
      if (editable && !hotkey.allowInInput) continue

      event.preventDefault()
      hotkey.handler()
      return
    }
  }

  onMounted(() => window.addEventListener('keydown', onKeydown))
  onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
}
