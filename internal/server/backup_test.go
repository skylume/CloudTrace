package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"cloudtrace/internal/config"
)

// getBody 取响应体，失败即终止。
func getBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应失败：%v", err)
	}
	return string(raw)
}

// TestSettingsBackupOmitsCredentials 备份文件里不能出现密码哈希。
func TestSettingsBackupOmitsCredentials(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Server.Token = "pbkdf2-sha256$210000$c2FsdA==$aGFzaA=="
	})

	resp, err := http.Get(st.ts.URL + settingsBackupRoute)
	if err != nil {
		t.Fatalf("请求备份失败：%v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，想要 200：%s", resp.StatusCode, getBody(t, resp))
	}
	disposition := resp.Header.Get("Content-Disposition")
	if !strings.Contains(disposition, "attachment") {
		t.Fatalf("Content-Disposition = %q，应当作为附件下载", disposition)
	}
	body := getBody(t, resp)
	if strings.Contains(body, "pbkdf2-sha256") {
		t.Fatalf("备份内容里出现了密码哈希：%s", body)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("备份不是合法 JSON：%v", err)
	}
	if _, ok := doc["schema_version"]; !ok {
		t.Fatal("备份缺少 schema_version")
	}
	var srv map[string]json.RawMessage
	if err := json.Unmarshal(doc["server"], &srv); err != nil {
		t.Fatalf("server 段不是对象：%v", err)
	}
	if _, ok := srv["token"]; ok {
		t.Fatal("备份的 server 段里仍有 token")
	}
}

// TestSettingsRestoreAppliesAndReportsRestart 恢复后配置生效，并报出要重启的项。
func TestSettingsRestoreAppliesAndReportsRestart(t *testing.T) {
	st := newTestStack(t, nil)
	body := `{"advanced":{"log_level":"debug"},"scan":{"workers":77}}`

	resp, err := http.Post(st.ts.URL+settingsRestoreRoute, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("请求恢复失败：%v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，想要 200：%s", resp.StatusCode, getBody(t, resp))
	}
	var out settingsRestoreResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	_ = resp.Body.Close()

	if got := st.store.Get().Scan.Workers; got != 77 {
		t.Fatalf("scan.workers = %d，恢复没有生效", got)
	}
	found := false
	for _, key := range out.RestartRequired {
		if key == "advanced.log_level" {
			found = true
		}
	}
	if !found {
		t.Fatalf("restart_required = %v，应当包含 advanced.log_level", out.RestartRequired)
	}
}

// TestSettingsRestoreDropsCredentials 文件里带的凭据要丢弃并报出。
func TestSettingsRestoreDropsCredentials(t *testing.T) {
	st := newTestStack(t, nil)
	body := `{"server":{"token":"pbkdf2-sha256$1$x$y","session_ttl_min":5}}`

	resp, err := http.Post(st.ts.URL+settingsRestoreRoute, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("请求恢复失败：%v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，想要 200：%s", resp.StatusCode, getBody(t, resp))
	}
	var out settingsRestoreResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	_ = resp.Body.Close()

	if len(out.Ignored) != 2 {
		t.Fatalf("ignored = %v，应当报出两个凭据键", out.Ignored)
	}
	if got := st.store.Get().Server.Token; got != "" {
		t.Fatalf("密码哈希被写进了配置：%q", got)
	}
}

// TestSettingsRestoreRejectsBrokenJSON 坏文件要 400 而不是静默变默认值。
func TestSettingsRestoreRejectsBrokenJSON(t *testing.T) {
	st := newTestStack(t, nil)
	resp, err := http.Post(st.ts.URL+settingsRestoreRoute, "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("请求恢复失败：%v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，想要 400：%s", resp.StatusCode, getBody(t, resp))
	}
	var payload errorPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("解析错误响应失败：%v", err)
	}
	_ = resp.Body.Close()
	if payload.Code != CodeInvalidParam {
		t.Fatalf("code = %q，想要 %q", payload.Code, CodeInvalidParam)
	}
}
