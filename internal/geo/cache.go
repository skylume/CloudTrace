package geo

import (
	"encoding/json"
	"os"
	"strings"
	"sync"

	"cloudtrace/internal/atomicfile"
)

// cacheVersion 是缓存文件的格式版本。
const cacheVersion = 1

// defaultCacheMax 是缓存条目上限。
//
// 超限后按写入顺序丢弃最旧的：缓存是「省一次查询」的加速手段，不是数据
// 本身，丢掉最旧的条目最多让某个 IP 下次重新探测一遍。
const defaultCacheMax = 20000

// cachedInfo 是一个 IP 的归属地。
type cachedInfo struct {
	Loc  string
	Colo string
}

// InfoCache 是「IP → 出口国家码 + 数据中心代码」的本地增量缓存。
//
// 数据来自扫描时已经取到的 trace，不额外发任何请求：既然为了校验节点已经
// 拿过一次 trace，把它顺手记下来是零成本的，下次同一个 IP 出现在远程源里
// 就能直接给出地区。
//
// 增量写：只有真的新增或改动过条目才会落盘，且落盘时全量重写一次（文件
// 本身不大）。缓存损坏时重建，不阻断任何流程。
type InfoCache struct {
	path string
	max  int

	mu     sync.Mutex
	data   map[string]cachedInfo
	order  []string
	dirty  bool
	loaded bool
}

// NewInfoCache 构造缓存。path 为空时缓存只存在于内存。
func NewInfoCache(path string, max int) *InfoCache {
	if max <= 0 {
		max = defaultCacheMax
	}
	return &InfoCache{
		path: strings.TrimSpace(path),
		max:  max,
		data: make(map[string]cachedInfo),
	}
}

// Load 读取缓存文件；文件不存在或损坏都按空缓存处理。
//
// 刻意不返回错误：缓存是派生数据，读不出来重建即可，不该让调用方为难。
func (c *InfoCache) Load() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()
}

// loadLocked 执行一次加载，已加载过则直接返回。
func (c *InfoCache) loadLocked() {
	if c.loaded {
		return
	}
	c.loaded = true
	if c.path == "" {
		return
	}

	raw, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	var file cacheFile
	if err := json.Unmarshal(raw, &file); err != nil {
		// 损坏的缓存直接丢弃：它只是加速手段，为它报错没有任何意义。
		return
	}
	for ip, value := range file.IPs {
		info, ok := parseCachedValue(value)
		if !ok || ip == "" {
			continue
		}
		if _, exists := c.data[ip]; !exists {
			c.order = append(c.order, ip)
		}
		c.data[ip] = info
	}
}

// Get 查询一个 IP 的归属地。
func (c *InfoCache) Get(ip string) (loc, colo string, ok bool) {
	key := strings.TrimSpace(ip)
	if key == "" {
		return "", "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()

	info, found := c.data[key]
	if !found {
		return "", "", false
	}
	return info.Loc, info.Colo, true
}

// Update 增量写入一个 IP 的归属地；两者都为空则忽略。
//
// 返回是否真的产生了变化：调用方据此决定要不要落盘，避免无谓的写。
func (c *InfoCache) Update(ip, loc, colo string) bool {
	key := strings.TrimSpace(ip)
	if key == "" {
		return false
	}
	info := cachedInfo{
		Loc:  strings.ToUpper(strings.TrimSpace(loc)),
		Colo: strings.ToUpper(strings.TrimSpace(colo)),
	}
	if info.Loc == "" && info.Colo == "" {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()

	if old, exists := c.data[key]; exists && old == info {
		return false
	} else if !exists {
		c.order = append(c.order, key)
	}
	c.data[key] = info
	c.dirty = true
	c.evictLocked()
	return true
}

// Len 返回缓存里的条目数。
func (c *InfoCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked()
	return len(c.data)
}

// Save 把缓存原子落盘；没有改动时什么都不做。
func (c *InfoCache) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveLocked()
}

// saveLocked 执行一次落盘。
func (c *InfoCache) saveLocked() error {
	if !c.dirty || c.path == "" {
		return nil
	}
	payload := cacheFile{Version: cacheVersion, Count: len(c.data), IPs: make(map[string][]string, len(c.data))}
	for ip, info := range c.data {
		payload.IPs[ip] = []string{info.Loc, info.Colo}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(c.path, data, 0o600); err != nil {
		return err
	}
	c.dirty = false
	return nil
}

// evictLocked 按写入顺序淘汰超出上限的条目。
func (c *InfoCache) evictLocked() {
	for len(c.order) > 0 && len(c.data) > c.max {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.data, oldest)
	}
	if len(c.order) > 2*c.max {
		live := c.order[:0]
		for _, ip := range c.order {
			if _, ok := c.data[ip]; ok {
				live = append(live, ip)
			}
		}
		c.order = live
	}
}

// cacheFile 是缓存文件的结构。
//
// 与既有实现的格式保持一致：条目值是「国家码 + 数据中心代码」的二元数组，
// 紧凑且人眼可读。
type cacheFile struct {
	Version int                 `json:"version"`
	Count   int                 `json:"count"`
	IPs     map[string][]string `json:"ips"`
}

// parseCachedValue 解析一条缓存值。
//
// 认不出来的值直接跳过：一条坏记录不该毁掉整份缓存，而缓存本身是派生数据，
// 丢一条最多让那个 IP 下次重新探测一遍。
func parseCachedValue(raw []string) (cachedInfo, bool) {
	if len(raw) == 0 {
		return cachedInfo{}, false
	}
	loc := strings.ToUpper(strings.TrimSpace(raw[0]))
	colo := ""
	if len(raw) > 1 {
		colo = strings.ToUpper(strings.TrimSpace(raw[1]))
	}
	if loc == "" && colo == "" {
		return cachedInfo{}, false
	}
	return cachedInfo{Loc: loc, Colo: colo}, true
}
