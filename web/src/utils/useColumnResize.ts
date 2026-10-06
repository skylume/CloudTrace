/**
 * 拖动列宽。
 *
 * 抽成组合式函数而不是留在表格组件里：表格本身已经要管排序、分组、选中、
 * 展开四件事，再把「指针按下的那一刻是哪一列、从多宽开始」这类过程状态摊在
 * 里面，读的人得在两种关注点之间来回跳。
 */
import { onBeforeUnmount, ref } from 'vue'

import { useFieldsStore } from '@/stores/fields'

/** 一次拖动中的上下文。 */
interface ResizeState {
  key: string
  startX: number
  startWidth: number
}

export function useColumnResize() {
  const fields = useFieldsStore()
  const resizing = ref<ResizeState | null>(null)

  /**
   * 从表头右边缘开始拖。
   *
   * 起点取**渲染出来的实际宽度**而不是我们记过的值：没记过的列是按内容撑开
   * 的，从记录值起算会让第一次拖动跳一下。
   */
  function start(key: string, event: MouseEvent): void {
    const header = (event.currentTarget as HTMLElement).closest('th')
    if (!header) return
    resizing.value = { key, startX: event.clientX, startWidth: header.getBoundingClientRect().width }
    // 监听挂在 window 上：指针滑出表头甚至滑出表格时也要继续跟着走。
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', stop)
    // 拖动期间禁掉文本选择，否则整张表会被刷成蓝色。
    document.body.classList.add('ct-col-resizing')
  }

  function onMove(event: MouseEvent): void {
    const state = resizing.value
    if (!state) return
    fields.setWidth(state.key, state.startWidth + (event.clientX - state.startX))
  }

  function stop(): void {
    resizing.value = null
    window.removeEventListener('mousemove', onMove)
    window.removeEventListener('mouseup', stop)
    document.body.classList.remove('ct-col-resizing')
  }

  onBeforeUnmount(stop)

  return { resizing, start }
}
