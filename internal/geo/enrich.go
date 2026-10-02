package geo

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"cloudtrace/internal/model"
)

// Warning 是一条代理出口提示。
//
// 结构化而不是只给一句话：界面要拿国家码去拼图标与「不再提示」的记忆键，
// 也要拿整句话去显示。
type Warning struct {
	Country string `json:"country"`
	Message string `json:"message"`
}

// CheckExit 探测本机出口地区并记下结论。
//
// 只探一次：出口在几分钟内不会变，而这条信息是给「可能走了代理」这条提示
// 用的，没必要每批结果都去问一遍。
//
// 失败不记结论也不报错：拿不到就什么都不提示，这比猜一个更安全。
func (m *Manager) CheckExit(ctx context.Context) {
	if !m.cfg().GeoWarnEnabled {
		return
	}
	loc, err := DetectExitCountry(ctx)
	if err != nil {
		m.logger.Debug("探测本机出口地区失败，跳过代理提示", "err", err)
		return
	}

	m.mu.Lock()
	m.exitLoc = loc
	m.exitChecked = true
	m.mu.Unlock()

	if ShouldWarn(loc) {
		m.logger.Info("本机出口地区看起来不是国内，结果的地域代表性可能受影响", "loc", loc)
	}
}

// ExitWarning 返回当前是否应当提示代理出口。
//
// 用户把提示关掉时永远返回 false：这一项是善意提醒，开关在用户手里。
func (m *Manager) ExitWarning() (Warning, bool) {
	if !m.cfg().GeoWarnEnabled {
		return Warning{}, false
	}
	m.mu.RLock()
	loc := m.exitLoc
	checked := m.exitChecked
	m.mu.RUnlock()

	if !checked || !ShouldWarn(loc) {
		return Warning{}, false
	}
	return Warning{
		Country: loc,
		Message: fmt.Sprintf("检测到本机出口为 %s，你可能正在使用代理或 VPN。"+
			"这会影响扫描结果的地域代表性，建议关闭代理后重新扫描。", loc),
	}, true
}

// Enrich 给一条结果补齐归属地信息。
//
// 三件事：把数据中心代码翻成中文地名、查 ASN 与组织名、把这次学到的地区
// 记进缓存。全部是本地计算或内存查表，**不发任何网络请求**——它挂在每个
// 节点的结果产出路径上，一次网络往返就会让扫描慢一个数量级。
//
// 任何一项拿不到就跳过那一项，绝不返回错误：归属信息是锦上添花，为它中断
// 结果是本末倒置。
func (m *Manager) Enrich(rec *model.IPRecord) {
	if rec == nil {
		return
	}

	if rec.Colo != "" && rec.RegionName == "" {
		rec.RegionName = RegionName(rec.Colo)
	}

	if addr, err := netip.ParseAddr(strings.TrimSpace(rec.IP)); err == nil {
		if info, ok := m.Lookup(addr); ok {
			rec.ASN = info.ASN
			rec.ASOrg = info.Org
		}
	}

	if m.cache != nil {
		m.cache.Update(rec.IP, rec.Loc, rec.Colo)
	}
}

// CachedRegion 返回缓存里记着的归属地。
func (m *Manager) CachedRegion(ip string) (loc, colo string, ok bool) {
	if m.cache == nil {
		return "", "", false
	}
	return m.cache.Get(ip)
}

// SaveCache 把归属地缓存落盘。
func (m *Manager) SaveCache() error {
	if m.cache == nil {
		return nil
	}
	return m.cache.Save()
}
