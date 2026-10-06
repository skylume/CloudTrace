package config

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// 访问密码的存储格式。
//
// 落盘的是加盐哈希，不是明文：配置文件会被用户拷来拷去、贴进 issue、丢进网盘，
// 而面板密码可能与他别处在用的密码重合。哈希用 PBKDF2-SHA256，迭代次数写进
// 字符串里——将来调高迭代次数时，老密码仍能按它自己的次数校验通过。
const (
	passwordScheme     = "pbkdf2-sha256"
	passwordIterations = 210_000
	passwordSaltBytes  = 16
	passwordKeyBytes   = 32
)

// ErrEmptyPassword 表示密码为空。
var ErrEmptyPassword = errors.New("访问密码不能为空")

// HashPassword 把明文密码转成可落盘的字符串。
//
// 格式：`pbkdf2-sha256$<迭代次数>$<盐 base64>$<哈希 base64>`。
func HashPassword(password string) (string, error) {
	if strings.TrimSpace(password) == "" {
		return "", ErrEmptyPassword
	}

	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成盐失败：%w", err)
	}

	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordKeyBytes)
	if err != nil {
		return "", fmt.Errorf("计算密码哈希失败：%w", err)
	}

	return strings.Join([]string{
		passwordScheme,
		strconv.Itoa(passwordIterations),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	}, "$"), nil
}

// IsHashedPassword 判断存的是不是哈希。
//
// 认不出的值一律当明文处理：旧版本写进配置的是一串随机 Token，用户升级之后
// 它还得能用——为了一次格式升级把所有人挡在门外，比留着一条兼容分支糟得多。
func IsHashedPassword(stored string) bool {
	return strings.HasPrefix(stored, passwordScheme+"$")
}

// VerifyPassword 校验明文密码是否与存下的值匹配。
//
// 两条路径都用**恒定时间**比较：时序侧信道能一个字符一个字符地把密码试出来，
// 而这里比较的是密码，不是别的什么可以随便泄露的东西。
func VerifyPassword(stored, password string) bool {
	if stored == "" || password == "" {
		return false
	}
	if !IsHashedPassword(stored) {
		// 旧版明文 Token。
		return subtle.ConstantTimeCompare([]byte(stored), []byte(password)) == 1
	}

	parts := strings.Split(stored, "$")
	if len(parts) != 4 {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}

	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(want, got) == 1
}

// PasswordHint 返回配置里那个密码的形态，供界面显示。
//
// 界面不该把哈希原样摆出来（既没意义又容易被误当成密码本身），也不该假装
// 知道密码是什么。它只需要回答「设过没有、是不是老格式」。
func PasswordHint(stored string) string {
	switch {
	case stored == "":
		return ""
	case IsHashedPassword(stored):
		return "hashed"
	default:
		return "legacy"
	}
}
