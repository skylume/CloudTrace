/**
 * 登录页的脚本。
 *
 * 独立于主应用：未登录时不该把整个前端加载进来，那会让登录页的首屏多出
 * 几十上百 KB 的解析开销，而这正是局域网用户最先看到的一屏。
 */
import './styles/tokens.css'
import './styles/themes.css'
import './styles/base.css'
import './styles/login.css'

const form = document.getElementById('login-form') as HTMLFormElement | null
const input = document.getElementById('login-token') as HTMLInputElement | null
const error = document.getElementById('login-error')
const submit = document.getElementById('login-submit') as HTMLButtonElement | null

function showError(message: string): void {
  if (error) error.textContent = message
}

form?.addEventListener('submit', (event) => {
  event.preventDefault()
  showError('')
  const token = input?.value.trim() ?? ''
  if (!token) {
    showError('请输入访问 Token')
    input?.focus()
    return
  }

  if (submit) submit.disabled = true
  void fetch('/auth/login', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  })
    .then(async (response) => {
      if (response.ok) {
        location.href = '/'
        return
      }
      // 后端出错时回的是 {code, msg}；解析不出来就退回状态码。
      const body = (await response.json().catch(() => ({}))) as { msg?: string }
      showError(body.msg ?? `登录失败（${response.status}）`)
    })
    .catch(() => showError('无法连接后端，请确认程序仍在运行'))
    .finally(() => {
      if (submit) submit.disabled = false
    })
})
