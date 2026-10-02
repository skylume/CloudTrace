package geo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------

func TestInfoCacheUpdateAndGet(t *testing.T) {
	c := NewInfoCache(filepath.Join(t.TempDir(), "ipinfo.json"), 0)

	if _, _, ok := c.Get("1.1.1.1"); ok {
		t.Fatal("空缓存不该查到东西")
	}
	if !c.Update("1.1.1.1", "cn", "lax") {
		t.Fatal("首次写入应当报告有变化")
	}
	loc, colo, ok := c.Get("1.1.1.1")
	if !ok || loc != "CN" || colo != "LAX" {
		t.Fatalf("读回 = (%q, %q, %v)，期望大写归一化", loc, colo, ok)
	}

	// 同一个值再写一次不算变化：省掉一次无谓的落盘。
	if c.Update("1.1.1.1", "CN", "LAX") {
		t.Error("重复写入同一个值不该报告变化")
	}
	if !c.Update("1.1.1.1", "CN", "SJC") {
		t.Error("值变了应当报告变化")
	}

	// 两个都为空时忽略：没有信息不该占一个条目。
	if c.Update("2.2.2.2", "", "") {
		t.Error("空值不该写入")
	}
	if c.Update("", "CN", "HKG") {
		t.Error("空 IP 不该写入")
	}
	if c.Len() != 1 {
		t.Fatalf("条目数 = %d，期望 1", c.Len())
	}
}

func TestInfoCachePersistsAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ipinfo.json")

	c := NewInfoCache(path, 0)
	c.Update("1.1.1.1", "CN", "LAX")
	c.Update("8.8.8.8", "US", "SJC")
	if err := c.Save(); err != nil {
		t.Fatalf("落盘失败：%v", err)
	}

	// 没有改动时再存一次不该报错，也不该重写文件。
	if err := c.Save(); err != nil {
		t.Fatalf("重复落盘失败：%v", err)
	}

	reloaded := NewInfoCache(path, 0)
	reloaded.Load()
	if reloaded.Len() != 2 {
		t.Fatalf("重新加载后条目数 = %d，期望 2", reloaded.Len())
	}
	if loc, colo, ok := reloaded.Get("8.8.8.8"); !ok || loc != "US" || colo != "SJC" {
		t.Fatalf("读回 = (%q, %q, %v)", loc, colo, ok)
	}
}

// 文件格式要与既有实现一致：条目值是「国家码 + 数据中心代码」的二元数组。
func TestInfoCacheFileFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ipinfo.json")
	c := NewInfoCache(path, 0)
	c.Update("1.1.1.1", "CN", "LAX")
	if err := c.Save(); err != nil {
		t.Fatalf("落盘失败：%v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	var file cacheFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("不是合法 JSON：%v", err)
	}
	if file.Version != cacheVersion {
		t.Errorf("版本 = %d，期望 %d", file.Version, cacheVersion)
	}
	if file.Count != 1 {
		t.Errorf("条数 = %d，期望 1", file.Count)
	}
	if got := file.IPs["1.1.1.1"]; len(got) != 2 || got[0] != "CN" || got[1] != "LAX" {
		t.Errorf("条目 = %v，期望 [CN LAX]", got)
	}
}

// 缓存损坏时重建，不阻断任何流程。
func TestInfoCacheCorruptedFileIsRebuilt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ipinfo.json")
	if err := os.WriteFile(path, []byte("{不是 JSON"), 0o600); err != nil {
		t.Fatalf("准备文件失败：%v", err)
	}

	c := NewInfoCache(path, 0)
	c.Load()
	if c.Len() != 0 {
		t.Fatalf("坏缓存应当被丢弃，实际有 %d 条", c.Len())
	}
	// 仍然可以继续用，并把新内容覆盖回去。
	c.Update("1.1.1.1", "CN", "LAX")
	if err := c.Save(); err != nil {
		t.Fatalf("覆盖坏缓存失败：%v", err)
	}
	again := NewInfoCache(path, 0)
	again.Load()
	if again.Len() != 1 {
		t.Fatalf("覆盖后重新加载 = %d 条，期望 1", again.Len())
	}
}

// 坏条目跳过，不让一条坏记录毁掉整份缓存。
func TestInfoCacheSkipsBadEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ipinfo.json")
	body := `{"version":1,"count":4,"ips":{
		"1.1.1.1":["CN","LAX"],
		"2.2.2.2":[],
		"3.3.3.3":["",""],
		"4.4.4.4":["US"]
	}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("准备文件失败：%v", err)
	}

	c := NewInfoCache(path, 0)
	c.Load()
	if c.Len() != 2 {
		t.Fatalf("条目数 = %d，期望只留下 2 条有效记录", c.Len())
	}
	if _, colo, ok := c.Get("4.4.4.4"); !ok || colo != "" {
		t.Errorf("只有国家码的条目应当保留：colo=%q ok=%v", colo, ok)
	}
}

func TestInfoCacheEvictsOldest(t *testing.T) {
	c := NewInfoCache("", 3)
	for i := 0; i < 6; i++ {
		c.Update(fmt.Sprintf("10.0.0.%d", i), "CN", "HKG")
	}
	if c.Len() != 3 {
		t.Fatalf("条目数 = %d，期望上限 3", c.Len())
	}
	if _, _, ok := c.Get("10.0.0.0"); ok {
		t.Error("最旧的条目没有被淘汰")
	}
	if _, _, ok := c.Get("10.0.0.5"); !ok {
		t.Error("最新的条目不该被淘汰")
	}
}

// 路径为空时只在内存里工作，不落盘也不报错。
func TestInfoCacheMemoryOnly(t *testing.T) {
	c := NewInfoCache("", 0)
	c.Update("1.1.1.1", "CN", "LAX")
	if err := c.Save(); err != nil {
		t.Fatalf("无路径时落盘应当是无操作：%v", err)
	}
	if c.Len() != 1 {
		t.Fatalf("条目数 = %d", c.Len())
	}
}

// 扫描是并发的，缓存必须能同时被多个 goroutine 读写。
func TestInfoCacheConcurrentAccess(t *testing.T) {
	c := NewInfoCache(filepath.Join(t.TempDir(), "ipinfo.json"), 0)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				ip := fmt.Sprintf("10.0.%d.%d", n, j)
				c.Update(ip, "CN", "HKG")
				_, _, _ = c.Get(ip)
				_ = c.Len()
			}
		}(i)
	}
	wg.Wait()

	if c.Len() != 400 {
		t.Fatalf("条目数 = %d，期望 400", c.Len())
	}
	if err := c.Save(); err != nil {
		t.Fatalf("并发写后落盘失败：%v", err)
	}
}
