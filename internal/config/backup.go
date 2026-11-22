package config

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// SchemaVersion 是导出文件的格式版本。
//
// 只增不改：导入端按「未知字段忽略、缺字段按默认补齐」处理，版本号留给
// 将来需要改写结构时判断文件是哪一代写的。
const SchemaVersion = 1

// credentialPaths 是导出时必须剔除的键（顶层段名 + 字段名）。
//
// 访问密码在配置里存的是 PBKDF2 哈希，而导出的文件会被当备份到处传——
// 把哈希带出去等于把离线爆破的样本一起发出去。会话 TTL 跟着密码走，
// 一并剔除。
var credentialPaths = [][2]string{
	{"server", "token"},
	{"server", "session_ttl_min"},
}

// ExportBytes 返回一份可备份的配置内容。
//
// 与 SaveFile 的区别只有两点：剔除凭据、带上格式版本号。其余字段与
// settings.json 完全一致，用户拿导出的文件直接当配置文件用也能启动。
func ExportBytes(cfg Config) ([]byte, error) {
	n := cfg.Clone()
	n.normalize()
	raw, err := json.Marshal(n)
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	if section, ok := top["server"]; ok {
		var srv map[string]json.RawMessage
		if err := json.Unmarshal(section, &srv); err != nil {
			return nil, err
		}
		for _, p := range credentialPaths {
			delete(srv, p[1])
		}
		trimmed, err := json.Marshal(srv)
		if err != nil {
			return nil, err
		}
		top["server"] = trimmed
	}
	top["schema_version"] = json.RawMessage(strconv.Itoa(SchemaVersion))

	data, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// ImportBytes 解析一份备份内容。
//
// 未知字段直接忽略（新版导出、旧版程序也能读），缺失字段保持默认值
// （旧版导出、新版程序也能读）。文件里若带了凭据键，会被丢弃并通过返回值
// 报出，由界面提示「凭据不会被导入，请在本机重新设置」——静默接受等于让
// 一次导入把本机的密码换掉，用户下次就登不上了。
func ImportBytes(data []byte) (Config, []string, error) {
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, nil, fmt.Errorf("配置内容不是合法的 JSON：%w", err)
	}

	ignored := presentCredentialPaths(data)
	if len(ignored) > 0 {
		def := Default()
		cfg.Server.Token = def.Server.Token
		cfg.Server.SessionTTLMin = def.Server.SessionTTLMin
	}
	cfg.normalize()
	return cfg, ignored, nil
}

// presentCredentialPaths 列出文件里实际出现的凭据键（点号路径）。
func presentCredentialPaths(data []byte) []string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil
	}
	section, ok := top["server"]
	if !ok {
		return nil
	}
	var srv map[string]json.RawMessage
	if err := json.Unmarshal(section, &srv); err != nil {
		return nil
	}
	var out []string
	for _, p := range credentialPaths {
		if _, ok := srv[p[1]]; ok {
			out = append(out, p[0]+"."+p[1])
		}
	}
	return out
}
