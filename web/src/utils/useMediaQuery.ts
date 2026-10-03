/**
 * 响应式媒体查询。
 *
 * 抽出来是为了不让每个组件各写一遍 matchMedia 的监听与清理——漏掉清理会
 * 在切页时积累监听器，而这类泄漏在开发时完全看不出来。
 */
import { onBeforeUnmount, onMounted, ref, type Ref } from 'vue'

/** 断点。与设计文档的适配表一致，改动前先看那张表。 */
export const BREAKPOINTS = {
  /** 大屏桌面：双栏。 */
  wide: 1440,
  /** 标准桌面。 */
  desktop: 1024,
  /** 平板 / 小窗：侧栏折叠为图标条。 */
  tablet: 768,
  /** 手机：单列 + 底部 Tab。 */
  phone: 480,
} as const

/**
 * 监听一个媒体查询。
 *
 * 返回值在组件卸载后不再更新，监听器同时被移除。
 */
export function useMediaQuery(query: string): Ref<boolean> {
  const matches = ref(false)
  const media = typeof matchMedia === 'function' ? matchMedia(query) : null

  function sync(event: MediaQueryList | MediaQueryListEvent): void {
    matches.value = event.matches
  }

  onMounted(() => {
    if (!media) return
    sync(media)
    media.addEventListener('change', sync)
  })

  onBeforeUnmount(() => {
    media?.removeEventListener('change', sync)
  })

  return matches
}

/** 窄屏：手机与小屏平板，用底部 Tab 导航、表格降级为卡片。 */
export function useNarrow(): Ref<boolean> {
  return useMediaQuery(`(max-width: ${BREAKPOINTS.tablet}px)`)
}
