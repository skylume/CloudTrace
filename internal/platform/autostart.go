package platform

// Autostart 管理「开机自启」这一项。
//
// 做成一个结构体而不是包级函数：注册表是全局状态，用例碰它就会真的把自启
// 打开——而开发者多半不希望在跑测试时被悄悄加一条开机启动。结构体让用例
// 换一个临时键，跑完删掉，不碰用户真正的自启项。
type Autostart struct {
	// KeyPath 是自启项所在的注册表路径；为空表示当前用户的 Run 键。
	//
	// 只写当前用户：写机器级的那个需要管理员权限，而这个程序是便携的，
	// 不该在启动时要求提权。
	KeyPath string
	// Value 是值名。换名字等于换一个自启项，旧的不会自动消失。
	Value string
}

// defaultRunKey 是当前用户的自启项位置。
const defaultRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// defaultAutostartValue 是自启项的值名。
const defaultAutostartValue = "CloudTrace"

// normalized 补齐空字段，让调用方可以只写 `Autostart{}`。
func (a Autostart) normalized() Autostart {
	if a.KeyPath == "" {
		a.KeyPath = defaultRunKey
	}
	if a.Value == "" {
		a.Value = defaultAutostartValue
	}
	return a
}

// Set 打开或关闭开机自启。
//
// exePath 为空时取当前可执行文件。路径一律加引号写进注册表：不带引号时，
// 含空格的路径会被系统拆成「程序 + 参数」两段，自启静默失效。
func (a Autostart) Set(exePath string, enabled bool) error {
	return a.normalized().set(exePath, enabled)
}

// Enabled 查询自启项当前是否指向这个可执行文件。
//
// 不只看「值存不存在」：程序被挪到别处之后，旧路径还留在注册表里，值存在但
// 指向的东西已经没了。那种情况应当算「没开」，否则界面会一直显示已启用。
func (a Autostart) Enabled(exePath string) (bool, error) {
	return a.normalized().enabled(exePath)
}

// SyncAutostart 把开机自启对齐到期望状态，指向当前可执行文件。
//
// 启动时调用一次：用户可能直接删了安装目录、或者把 exe 挪到了别处，注册表里
// 那条旧记录既不会自己消失，也不会再起作用。对齐之后配置说了算。
func SyncAutostart(want bool) error {
	return Autostart{}.Sync("", want)
}

// Sync 把自启项对齐到期望状态。
func (a Autostart) Sync(exePath string, want bool) error {
	if !want {
		return a.Set(exePath, false)
	}
	on, err := a.Enabled(exePath)
	if err != nil {
		return err
	}
	if on {
		return nil
	}
	return a.Set(exePath, true)
}
