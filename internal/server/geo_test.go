package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cloudtrace/internal/config"
	"cloudtrace/internal/geo"
	"cloudtrace/internal/model"
)

// 地理状态要能读到，且快捷选项名由后端下发。
func TestWSGeoStatus(t *testing.T) {
	st := newTestStack(t, nil)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"geo/status"}`)
	m := readUntil(t, conn, eventGeo, 5*time.Second)

	var resp geoStatusResp
	decode(t, m, &resp)

	if resp.Status.Source != geo.SourceIPToASN {
		t.Errorf("数据源 = %q，期望 %q", resp.Status.Source, geo.SourceIPToASN)
	}
	if len(resp.QuickFilters) != 3 {
		t.Fatalf("快捷选项 = %v，期望 3 项", resp.QuickFilters)
	}
	for _, name := range resp.QuickFilters {
		if len(geo.QuickFilter(name)) == 0 {
			t.Errorf("快捷选项 %s 没有对应的匹配词", name)
		}
	}
	// 测试环境里没有库文件，状态要如实反映「不可用」，而不是假装正常。
	if resp.Status.Loaded {
		t.Errorf("没有库文件却报告已加载：%+v", resp.Status)
	}
	if resp.Status.Error == "" {
		t.Error("库不可用时应当给出原因，设置页要显示它")
	}
	// 没有探测过出口地区时不提示。
	if resp.Warning != nil {
		t.Errorf("没有探测结论时不该给出代理提示：%+v", resp.Warning)
	}
}

// 关掉 ASN 查询时状态要如实说是关闭，而不是「加载失败」。
func TestWSGeoStatusWhenSourceOff(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Geo.ASNSource = geo.SourceOff
	})

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"geo/status"}`)
	var resp geoStatusResp
	decode(t, readUntil(t, conn, eventGeo, 5*time.Second), &resp)

	if resp.Status.Source != geo.SourceOff || resp.Status.Loaded {
		t.Fatalf("状态 = %+v", resp.Status)
	}
	if resp.Status.Error != "" {
		t.Errorf("关掉查询是用户的选择，不该报成失败：%q", resp.Status.Error)
	}
}

// 关掉查询时点「立即更新」要给一句能照做的提示。
func TestWSGeoUpdateWhenSourceOff(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Geo.ASNSource = geo.SourceOff
	})

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"geo/update"}`)

	// 失败也要广播状态，否则别的面板还停在旧数字上。
	m := readUntil(t, conn, eventGeo, 5*time.Second)
	var resp geoStatusResp
	decode(t, m, &resp)
	if resp.Status.Source != geo.SourceOff {
		t.Errorf("广播的状态 = %+v", resp.Status)
	}

	var p errorPayload
	decode(t, readUntil(t, conn, eventError, 5*time.Second), &p)
	if p.Code != CodeInvalidParam {
		t.Fatalf("错误码 = %q，期望 %q", p.Code, CodeInvalidParam)
	}
	if !strings.Contains(p.Msg, "设置") {
		t.Errorf("提示信息没有告诉用户去哪里改：%q", p.Msg)
	}
}

// 状态广播要发给所有连接，不能只回发起方。
func TestGeoUpdateBroadcastsToAll(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Geo.ASNSource = geo.SourceOff
	})

	first := st.mustDial(t)
	second := st.mustDial(t)
	readUntil(t, first, eventState, 3*time.Second)
	readUntil(t, second, eventState, 3*time.Second)

	send(t, first, `{"type":"geo/update"}`)

	// 两个连接都要收到 geo 事件。
	if m := readUntil(t, second, eventGeo, 5*time.Second); m.Type != eventGeo {
		t.Fatalf("第二个连接收到的事件 = %q", m.Type)
	}
	if m := readUntil(t, first, eventGeo, 5*time.Second); m.Type != eventGeo {
		t.Fatalf("发起方收到的事件 = %q", m.Type)
	}
}

// 配置里的运营商过滤要作用到本地结果地址上。
func TestLatestAppliesASNFilter(t *testing.T) {
	st := newTestStack(t, func(c *config.Config) {
		c.Geo.FilterASN = []string{"9808"}
	})

	records := []model.IPRecord{
		{IP: "1.1.1.1", Port: 443, Latency: 10, LatencyAvg: 10, Sent: 3, Recv: 3, Colo: "HKG", ASN: 9808, ASOrg: "CHINA MOBILE"},
		{IP: "2.2.2.2", Port: 443, Latency: 20, LatencyAvg: 20, Sent: 3, Recv: 3, Colo: "HKG", ASN: 13335, ASOrg: "CLOUDFLARENET"},
	}
	seedSpeedHistory(t, st, records)

	body := latestBody(t, st)
	if !strings.Contains(body, "1.1.1.1") {
		t.Errorf("命中的节点被筛掉了：%q", body)
	}
	if strings.Contains(body, "2.2.2.2") {
		t.Errorf("没命中的节点没有被筛掉：%q", body)
	}
}

// 没有配置过滤时一个节点都不能少。
func TestLatestWithoutASNFilterKeepsAll(t *testing.T) {
	st := newTestStack(t, nil)

	records := []model.IPRecord{
		{IP: "1.1.1.1", Port: 443, Latency: 10, LatencyAvg: 10, Sent: 3, Recv: 3, Colo: "HKG", ASN: 9808},
		{IP: "2.2.2.2", Port: 443, Latency: 20, LatencyAvg: 20, Sent: 3, Recv: 3, Colo: "HKG", ASN: 13335},
	}
	seedSpeedHistory(t, st, records)

	if lines := strings.Count(strings.TrimRight(latestBody(t, st), "\n"), "\n"); lines != 1 {
		t.Fatalf("行数 = %d，期望 2", lines+1)
	}
}

// 状态载荷要能被前端直接消费。
func TestGeoStatusPayloadShape(t *testing.T) {
	st := newTestStack(t, nil)
	s := &server{cfg: st.store, svc: st.svc}

	raw, err := json.Marshal(s.geoStatus())
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("载荷不是合法 JSON：%v", err)
	}
	status, ok := obj["status"].(map[string]any)
	if !ok {
		t.Fatalf("载荷缺少 status 对象：%s", raw)
	}
	for _, key := range []string{"source", "path", "file_time", "records", "updated_at", "loaded"} {
		if _, ok := status[key]; !ok {
			t.Errorf("状态里缺少 %q：%s", key, raw)
		}
	}
	if _, ok := obj["quick_filters"].([]any); !ok {
		t.Errorf("载荷缺少 quick_filters 数组：%s", raw)
	}
}
