package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"cloudtrace/internal/model"
)

// ResetKeys 把指定配置项恢复为内置默认值。
//
// key 支持两种粒度：顶层分组（scan）与具体参数（scan.workers）。设置页上
// 每一项都要能单独恢复默认——只支持整组重置的话，用户为了改回一个键就得把
// 同组其它已经调好的项一起丢掉。
//
// keys 为空等价于整份重置，语义与 Reset 完全一致。
//
// 服务相关三项（Token / 端口 / 监听地址）**一律保留**，不管用户点的是
// 「server」还是「server.port」：换 Token 会把所有已登录会话踢下线，改端口
// 与监听地址要重启才生效，静默重置的结果是用户下次启动找不到面板。
func (s *Store) ResetKeys(keys []string) (Config, error) {
	if len(keys) == 0 {
		return s.Reset()
	}

	before := s.Get()
	cur, err := toObject(before)
	if err != nil {
		return Config{}, err
	}
	def, err := toObject(Default())
	if err != nil {
		return Config{}, err
	}

	cleared := make([]string, 0, len(keys))
	for _, key := range keys {
		path := splitKey(key)
		if len(path) == 0 {
			return Config{}, unknownKey(key)
		}
		value, ok := lookupPath(def, path)
		if !ok {
			return Config{}, unknownKey(key)
		}
		setPath(cur, path, value)
		cleared = append(cleared, strings.Join(path, "."))
	}

	next, err := fromObject(cur)
	if err != nil {
		return Config{}, err
	}
	next.Server.Token = before.Server.Token
	next.Server.Port = before.Server.Port
	next.Server.Bind = before.Server.Bind
	next.Origins = dropOrigins(next.Origins, cleared)
	return s.Set(next)
}

// unknownKey 把认不出来的配置项报成校验错误。
//
// 归到校验错误是为了让前端能把它标在具体的设置项上；静默忽略则会让用户
// 以为「点了恢复默认」，其实什么都没发生。
func unknownKey(key string) error {
	return &ValidationError{Fields: []FieldError{
		{Key: key, Reason: "没有这个配置项"},
	}}
}

// toObject 把配置转成可逐层定位的 JSON 对象。
func toObject(c Config) (map[string]any, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// fromObject 把 JSON 对象还原成配置。
func fromObject(obj map[string]any) (Config, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("重置配置失败：%w", err)
	}
	return c, nil
}

// splitKey 把点号路径切成层级，顺带去掉空格与空段。
func splitKey(key string) []string {
	parts := strings.Split(key, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// lookupPath 按层级取出对象里的值，路径上任意一层不是对象都算没找到。
func lookupPath(obj map[string]any, path []string) (any, bool) {
	var cur any = obj
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// setPath 按层级替换对象里的值，顺带补出缺失的中间层。
//
// 走「替换」而不是「合并」：合并一个 map 类型的配置项（字段别名、实验开关）
// 会把用户已有的键留下来，重置就成了半吊子。
func setPath(obj map[string]any, path []string, value any) {
	cur := obj
	for i := 0; i < len(path)-1; i++ {
		next, ok := cur[path[i]].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[path[i]] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = value
}

// dropOrigins 去掉被重置项本身及其下级的参数来源标记。
//
// 恢复默认之后就不该再留着「用户手改过」的记录：留着的话自适应逻辑会一直
// 把这一项当作不可动的用户选择，而用户恰恰刚刚把它交还给了系统。
func dropOrigins(origins model.ParamOrigins, keys []string) model.ParamOrigins {
	if len(origins) == 0 {
		return origins
	}
	out := make(model.ParamOrigins, len(origins))
	for k, v := range origins {
		if coveredBy(k, keys) {
			continue
		}
		out[k] = v
	}
	return out
}

// coveredBy 报告某个参数路径是否落在任一被重置的键之下。
func coveredBy(key string, keys []string) bool {
	for _, k := range keys {
		if key == k || strings.HasPrefix(key, k+".") {
			return true
		}
	}
	return false
}
