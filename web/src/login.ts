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

const note = document.getElementById('login-note')
const noteMain = document.getElementById('login-note-main')
const noteSub = document.getElementById('login-note-sub')
const notePath = document.getElementById('login-note-path')

function showError(message: string): void {
  if (error) error.textContent = message
}

/**
 * 填上「Token 去哪儿找」。
 *
 * 这一段必须由服务端说了算：只绑回环时面板并没有开放给局域网，而配置文件
 * 在哪个目录取决于用户的数据目录——两者都不是静态页面能猜的。
 *
 * 拿不到就不显示提示块：一块写着「未知」的提示比没有提示更让人心慌，而
 * 输入框与按钮本身已经够用了。
 */
async function fillNote(): Promise<void> {
  if (!note || !noteMain || !noteSub || !notePath) return

  let info: { lan?: boolean; token_path?: string } = {}
  try {
    const response = await fetch('/auth/login-info', { credentials: 'same-origin' })
    if (!response.ok) return
    info = (await response.json()) as typeof info
  } catch {
    return
  }

  noteMain.textContent = info.lan
    ? '面板已开放局域网访问，需要访问 Token 才能进入。'
    : '面板需要访问 Token 才能进入。'
  noteSub.textContent = info.token_path
    ? 'Token 是启动时自动生成的，控制台打印过，也存在这个文件里：'
    : 'Token 是启动时自动生成的，在运行面板那台机器的控制台打印过。'

  if (info.token_path) {
    notePath.textContent = info.token_path
    notePath.hidden = false
  }
  note.hidden = false
}

void fillNote()

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
