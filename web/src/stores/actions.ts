/**
 * 动作注册表。
 *
 * 命令面板与快捷键要能触发「开始扫描」，但扫描参数只有扫描页知道——它可能
 * 还没被打开过，也可能刚被改过。让面板自己拼一份参数就会与扫描页不一致。
 *
 * 于是反过来：页面把自己那套动作注册进来，面板与快捷键只负责调用。这样
 * 「怎么开始扫描」只有一处定义，快捷键和按钮走的是同一条路径。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

/** 一个可被面板或快捷键触发的动作。 */
export type ActionHandler = () => void

export const useActionStore = defineStore('actions', () => {
  const startScan = ref<ActionHandler | null>(null)
  const startSpeed = ref<ActionHandler | null>(null)

  /** 页面挂载时注册；返回注销函数，页面卸载时调用。 */
  function register(name: 'startScan' | 'startSpeed', handler: ActionHandler): () => void {
    if (name === 'startScan') startScan.value = handler
    else startSpeed.value = handler
    return () => {
      if (name === 'startScan') startScan.value = null
      else startSpeed.value = null
    }
  }

  function runStartScan(): boolean {
    if (!startScan.value) return false
    startScan.value()
    return true
  }

  function runStartSpeed(): boolean {
    if (!startSpeed.value) return false
    startSpeed.value()
    return true
  }

  return { startScan, startSpeed, register, runStartScan, runStartSpeed }
})
