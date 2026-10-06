/**
 * 登录页的脚本。
 *
 * 独立于主应用：未登录时不该把整个前端加载进来，那会让登录页的首屏多出
 * 几十上百 KB 的解析开销，而这正是局域网用户最先看到的一屏。
 *
 * 这一页只在**从局域网访问、且已经设过密码**时才会出现：本机访问免鉴权
 * （见服务端 localExempt），没设密码时根本不需要登录。
 */
import './styles/tokens.css'
import './styles/themes.css'
import './styles/base.css'
import './styles/login.css'

const form = document.getElementById('login-form') as HTMLFormElement | null
const field = document.querySelector('.login-field') as HTMLElement | null
const input = document.getElementById('login-token') as HTMLInputElement | null
const label = document.getElementById('login-label')
const reveal = document.getElementById('login-reveal')
const error = document.getElementById('login-error')
const submit = document.getElementById('login-submit') as HTMLButtonElement | null

const note = document.getElementById('login-note')
const noteMain = document.getElementById('login-note-main')
const noteSub = document.getElementById('login-note-sub')
const notePath = document.getElementById('login-note-path')

function showError(message: string): void {
  if (error) error.textContent = message
}

/** 「显示 / 隐藏」切换。长 Token 看不见没法确认抄对了没有。 */
reveal?.addEventListener('click', () => {
  if (!input || !reveal) return
  const hidden = input.type === 'password'
  input.type = hidden ? 'text' : 'password'
  reveal.textContent = hidden ? '隐藏' : '显示'
  reveal.setAttribute('aria-label', hidden ? '隐藏密码' : '显示密码')
  input.focus()
})

interface LoginInfo {
  lan?: boolean
  token_path?: string
  password_set?: boolean
  legacy_token?: boolean
}

/**
 * 按服务端的实际情况填提示与字段标签。
 *
 * 这一段必须由服务端说了算：
 *   - 存的是用户自己设的密码，还是升级前那版自动生成的 Token——两者的文案
 *     完全不同，后者得告诉他去哪儿找；
 *   - 配置文件在哪个目录取决于用户的数据目录，静态页面猜不出来。
 *
 * 拿不到就不显示提示块：一块写着「未知」的提示比没有提示更让人心慌。
 */
async function fillNote(): Promise<void> {
  let info: LoginInfo = {}
  try {
    const response = await fetch('/auth/login-info', { credentials: 'same-origin' })
    if (!response.ok) return
    info = (await response.json()) as LoginInfo
  } catch {
    return
  }

  // 旧版 Token 是一长串十六进制，等宽字体才好逐段核对。
  if (info.legacy_token) {
    if (label) label.textContent = '访问 Token'
    if (input) input.placeholder = '粘贴访问 Token'
    field?.classList.add('mono')
  }

  if (!note || !noteMain || !noteSub || !notePath) return

  if (info.legacy_token) {
    noteMain.textContent = '这台面板用的是升级前自动生成的访问 Token。'
    noteSub.textContent = info.token_path
      ? '它打印在运行面板那台机器的控制台上，也存在这个文件里：'
      : '它打印在运行面板那台机器的控制台上。想换成自己记得住的密码，就在那台机器上打开面板，到设置页改。'
  } else if (info.lan) {
    noteMain.textContent = '面板已开放局域网访问，输入访问密码才能进入。'
    noteSub.textContent = '密码是在运行面板那台机器的设置页里设的。'
  } else {
    // 正常情况下走不到这里（本机访问免鉴权），留一句兜底。
    noteMain.textContent = '面板需要访问密码才能进入。'
    noteSub.textContent = ''
  }

  if (info.token_path && info.legacy_token) {
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
    showError('请输入访问密码')
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
