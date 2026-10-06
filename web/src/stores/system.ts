/**
 * 后端告诉界面的一批「环境事实」。
 *
 * 合在一个 store 里是因为它们同源：都是「服务端现在是什么样」，都在连接建立
 * 后拉一次、之后基本不变。分成几个 store 只会让每个都多一遍同样的样板。
 *
 * 这里的值一律**由后端下发**，前端不自己算：
 *   - 官方段数来自随二进制固化的清单（`assets/`），前端抄一份迟早会分叉；
 *   - 可访问地址要按实际网卡枚举，前端拿不到；
 *   - 是否已设密码、是否需要登录，判定权在服务端。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

import { CMD } from '@/api/protocol'
import { sendCommand } from '@/api/client'
import type { AuthStatus, ServerStatus, SourceStatus } from '@/api/types'

export const useSystemStore = defineStore('system', () => {
  /** 内置官方网段的段数。0 表示还没拿到。 */
  const officialV4 = ref(0)
  const officialV6 = ref(0)
  const server = ref<ServerStatus | null>(null)
  const auth = ref<AuthStatus>({ password_set: false, legacy: false })
  /** 正在重启：连接马上会断，界面要给出「在重启」而不是「掉线了」。 */
  const restarting = ref(false)

  function refresh(): void {
    sendCommand(CMD.sourceStatus)
    sendCommand(CMD.serverStatus)
    sendCommand(CMD.authStatus)
  }

  function applySource(status: SourceStatus): void {
    officialV4.value = status.official_v4
    officialV6.value = status.official_v6
  }

  function applyServer(status: ServerStatus): void {
    server.value = status
  }

  function applyAuth(status: AuthStatus): void {
    auth.value = status
  }

  /**
   * 重启程序。
   *
   * 回执先到、连接后断，所以这里只负责把按钮置为「重启中」；真正的刷新由
   * 重连后的 refresh 完成。
   */
  function restart(): boolean {
    restarting.value = true
    return sendCommand(CMD.serverRestart, {})
  }

  /**
   * 设置访问密码。
   *
   * 明文只走这一趟：服务端收到就加盐哈希，之后再也拿不回来。所以界面上不做
   * 「回填已设的密码」这种事——它做不到，也不该做。
   */
  function setPassword(password: string): boolean {
    return sendCommand(CMD.authSetPassword, { password })
  }

  return {
    officialV4,
    officialV6,
    server,
    auth,
    restarting,
    refresh,
    applySource,
    applyServer,
    applyAuth,
    restart,
    setPassword,
  }
})
