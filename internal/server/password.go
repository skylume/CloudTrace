package server

import (
	"encoding/json"
	"strings"

	"cloudtrace/internal/config"
)

// 访问密码相关的命令与事件名。
const (
	cmdAuthStatus      = "auth/status"
	cmdAuthSetPassword = "auth/set-password"
	eventAuth          = "auth"
)

// 密码长度下限。
//
// 8 位而不是 12 位：这是个局域网里自用的面板，不是公网服务。要求太长只会让
// 用户改成「12345678」这种更好猜的东西，而下限真正要挡住的是「空」和「1」。
const minPasswordLen = 8

// authStatusResp 是访问密码的现状。
type authStatusResp struct {
	PasswordSet bool `json:"password_set"`
	// Legacy 表示存下来的是升级前那版自动生成的明文 Token。
	Legacy bool `json:"legacy"`
}

// authHandlers 返回访问密码相关的命令表。
//
// 这些命令**没有额外的鉴权检查**，因为 WebSocket 本身就要先过鉴权才能建立
// （见 router.go 的 /ws 分支）。在这里再查一遍等于重复一遍同样的判断。
func (s *server) authHandlers() map[string]commandHandler {
	return map[string]commandHandler{
		cmdAuthStatus:      s.handleAuthStatus,
		cmdAuthSetPassword: s.handleAuthSetPassword,
	}
}

func (s *server) authStatus() authStatusResp {
	stored := s.cfg.Get().Server.Token
	return authStatusResp{
		PasswordSet: stored != "",
		Legacy:      stored != "" && !config.IsHashedPassword(stored),
	}
}

func (s *server) handleAuthStatus(c *wsConn, _ json.RawMessage) error {
	c.sendEvent(eventAuth, s.authStatus())
	return nil
}

// authSetPasswordReq 是设置密码的请求。
type authSetPasswordReq struct {
	Password string `json:"password"`
}

// handleAuthSetPassword 设置访问密码。
//
// 落盘的是加盐哈希，不是明文：配置文件会被拷来拷去、贴进 issue、丢进网盘，
// 而面板密码可能和用户别处在用的密码重合。
//
// 改完密码会把**所有已有会话一并作废**——包括发起这次修改的那个。密码换了，
// 之前签发的通行证就不该继续有效；而让当前这个人重新输一次新密码，代价远小于
// 留着一堆用旧密码换来的会话。
func (s *server) handleAuthSetPassword(c *wsConn, data json.RawMessage) error {
	var req authSetPasswordReq
	if err := decodeReq(data, &req, "访问密码"); err != nil {
		return err
	}

	password := strings.TrimSpace(req.Password)
	if len([]rune(password)) < minPasswordLen {
		return fail(CodeInvalidParam, "访问密码至少 8 位")
	}

	hash, err := config.HashPassword(password)
	if err != nil {
		return fail(CodeIO, err.Error())
	}

	// patch 必须是嵌套对象：点号扁平键会被 deepMerge 当成陌生顶层键丢掉。
	if _, err := s.cfg.Patch(map[string]any{
		"server": map[string]any{"token": hash},
	}, nil); err != nil {
		return fail(CodeInvalidParam, err.Error())
	}

	s.auth.revokeAll()
	s.logger.Info("访问密码已更新，已有会话全部作废")
	c.sendEvent(eventAuth, s.authStatus())
	return nil
}
