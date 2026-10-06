/**
 * 「以此参数重跑」的交接点。
 *
 * 历史页与扫描页不会同时挂载——切页会把上一个销毁，所以不能用「页面注册动作、
 * 别人调用」那套（历史页拿着参数的时候扫描页已经卸载了，注册表里是空的）。
 * 改成一个待取件：历史页把参数放进来并切到扫描页，扫描页挂载时取走。
 *
 * 只传值、不传函数：这份数据要跨一次页面销毁与重建，带着闭包会让「什么时候
 * 还有效」变得难以判断。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

/** 一次重跑请求：面板参数（驼峰）+ 来源。 */
export interface RerunRequest {
  /** 面板参数，键与 i18n 映射表一致（如 `workers`）。 */
  params: Record<string, number | boolean>
  /** 自定义文本来源。官方网段与远端地址取自配置，不随记录走。 */
  customText: string
  /** 记录里带的档位名；为空表示手调参数。 */
  preset: string
}

export const useRerunStore = defineStore('rerun', () => {
  const pending = ref<RerunRequest | null>(null)

  function request(payload: RerunRequest): void {
    pending.value = payload
  }

  /** 取走待办。取过就清空：同一个请求不该被应用两次。 */
  function take(): RerunRequest | null {
    const value = pending.value
    pending.value = null
    return value
  }

  return { pending, request, take }
})
