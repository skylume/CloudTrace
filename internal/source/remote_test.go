package source

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"cloudtrace/internal/model"
)

func TestParseRemotePayloadJSONContainers(t *testing.T) {
	const node = `{"ip":"1.2.3.4","port":443,"country":"HK"}`

	tests := []struct {
		name string
		body string
	}{
		{name: "nodes 字段", body: `{"nodes":[` + node + `]}`},
		{name: "data 字段", body: `{"data":[` + node + `]}`},
		{name: "result 字段", body: `{"result":[` + node + `]}`},
		{name: "list 字段", body: `{"list":[` + node + `]}`},
		{name: "顶层数组", body: `[` + node + `]`},
		{name: "嵌套容器", body: `{"code":0,"data":{"list":[` + node + `]}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRemotePayload([]byte(tt.body))
			want := []model.IPRecord{{IP: "1.2.3.4", Port: 443, Loc: "HK"}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("记录 = %+v，期望 %+v", got, want)
			}
		})
	}
}

func TestParseRemotePayloadJSONFieldNames(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []model.IPRecord
	}{
		{
			name: "ip 加 country",
			body: `{"nodes":[{"ip":"1.2.3.4","port":443,"country":"JP"}]}`,
			want: []model.IPRecord{{IP: "1.2.3.4", Port: 443, Loc: "JP"}},
		},
		{
			name: "host 加 cc",
			body: `{"nodes":[{"host":"5.6.7.8","port":8443,"cc":"US"}]}`,
			want: []model.IPRecord{{IP: "5.6.7.8", Port: 8443, Loc: "US"}},
		},
		{
			name: "端口写成字符串",
			body: `{"nodes":[{"ip":"1.2.3.4","port":"2053","cc":"sg"}]}`,
			want: []model.IPRecord{{IP: "1.2.3.4", Port: 2053, Loc: "SG"}},
		},
		{
			name: "三位地区代码",
			body: `{"nodes":[{"ip":"1.2.3.4","port":443,"country":"HKG"}]}`,
			want: []model.IPRecord{{IP: "1.2.3.4", Port: 443, Loc: "HK"}},
		},
		{
			name: "中文地区名",
			body: `{"nodes":[{"ip":"1.2.3.4","port":443,"country":"日本"}]}`,
			want: []model.IPRecord{{IP: "1.2.3.4", Port: 443, Loc: "JP"}},
		},
		{
			name: "emoji 国旗",
			body: `{"nodes":[{"ip":"1.2.3.4","port":443,"cc":"🇸🇬"}]}`,
			want: []model.IPRecord{{IP: "1.2.3.4", Port: 443, Loc: "SG"}},
		},
		{
			name: "缺少端口",
			body: `{"nodes":[{"ip":"1.2.3.4","cc":"HK"}]}`,
			want: []model.IPRecord{{IP: "1.2.3.4", Loc: "HK"}},
		},
		{
			name: "端口越界时丢弃",
			body: `{"nodes":[{"ip":"1.2.3.4","port":99999,"cc":"HK"}]}`,
			want: []model.IPRecord{{IP: "1.2.3.4", Loc: "HK"}},
		},
		{
			name: "没有地址的对象被跳过",
			body: `{"nodes":[{"name":"node-1","port":443},{"ip":"1.2.3.4","cc":"HK"}]}`,
			want: []model.IPRecord{{IP: "1.2.3.4", Loc: "HK"}},
		},
		{
			name: "数组里混着字符串",
			body: `{"nodes":["5.6.7.8:443#US"]}`,
			want: []model.IPRecord{{IP: "5.6.7.8", Port: 443, Loc: "US"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRemotePayload([]byte(tt.body))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("记录 = %+v，期望 %+v", got, tt.want)
			}
		})
	}
}

func TestParseRemotePayloadText(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []model.IPRecord
	}{
		{
			name: "井号分隔地区",
			line: "1.2.3.4:443#HK",
			want: []model.IPRecord{{IP: "1.2.3.4", Port: 443, Loc: "HK"}},
		},
		{
			name: "空白分隔地区",
			line: "1.2.3.4:443 香港",
			want: []model.IPRecord{{IP: "1.2.3.4", Port: 443, Loc: "HK"}},
		},
		{
			name: "只有地址",
			line: "1.2.3.4:443",
			want: []model.IPRecord{{IP: "1.2.3.4", Port: 443}},
		},
		{
			name: "IPv6 带端口",
			line: "[2606:4700::1]:8443#JP",
			want: []model.IPRecord{{IP: "2606:4700::1", Port: 8443, Loc: "JP"}},
		},
		{
			name: "URL 形式",
			line: "https://1.2.3.4:8443/x#US",
			want: []model.IPRecord{{IP: "1.2.3.4", Port: 8443, Loc: "US"}},
		},
		{
			name: "域名节点",
			line: "edge.example.com:443#SG",
			want: []model.IPRecord{{IP: "edge.example.com", Port: 443, Loc: "SG"}},
		},
		{
			name: "注释行被跳过",
			line: "# 这是注释",
			want: nil,
		},
		{
			name: "双斜杠注释被跳过",
			line: "// 注释",
			want: nil,
		},
		{
			name: "无法识别的行被跳过",
			line: "这不是节点",
			want: nil,
		},
		{
			name: "空行被跳过",
			line: "",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRemotePayload([]byte(tt.line))
			if len(got) != len(tt.want) {
				t.Fatalf("记录 = %+v，期望 %+v", got, tt.want)
			}
			if len(tt.want) > 0 && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("记录 = %+v，期望 %+v", got, tt.want)
			}
		})
	}
}

func TestParseRemotePayloadIgnoresGarbage(t *testing.T) {
	if got := ParseRemotePayload([]byte("完全不是节点列表")); len(got) != 0 {
		t.Errorf("记录 = %+v，期望空", got)
	}
}

func TestFetchRemoteCollectsSuccessAndFailures(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"nodes":[{"ip":"1.2.3.4","port":443,"cc":"HK"}]}`))
	}))
	defer good.Close()

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	result, err := FetchRemote(context.Background(), []string{good.URL, bad.URL}, RemoteOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("FetchRemote 返回错误：%v", err)
	}
	if len(result.Records) != 1 || result.Records[0].IP != "1.2.3.4" {
		t.Errorf("Records = %+v，期望一条 1.2.3.4", result.Records)
	}
	if len(result.Failed) != 1 || result.Failed[0].URL != bad.URL {
		t.Errorf("Failed = %+v，期望恰好记录失败的那个地址", result.Failed)
	}
}

func TestFetchRemoteKeepsInputOrder(t *testing.T) {
	server := func(ip string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"nodes":[{"ip":"` + ip + `","port":443}]}`))
		}))
	}
	first, second := server("1.1.1.1"), server("2.2.2.2")
	defer first.Close()
	defer second.Close()

	result, err := FetchRemote(context.Background(), []string{first.URL, second.URL}, RemoteOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("FetchRemote 返回错误：%v", err)
	}
	got := []string{result.Records[0].IP, result.Records[1].IP}
	if want := []string{"1.1.1.1", "2.2.2.2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("记录顺序 = %v，期望 %v", got, want)
	}
}

func TestFetchRemoteRetriesThenSucceeds(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"nodes":[{"ip":"1.2.3.4","port":443}]}`))
	}))
	defer server.Close()

	result, err := FetchRemote(context.Background(), []string{server.URL}, RemoteOptions{
		Timeout:  2 * time.Second,
		Retries:  2,
		Interval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("FetchRemote 返回错误：%v", err)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("重试后应当成功，实际失败清单 %+v", result.Failed)
	}
	if len(result.Records) != 1 {
		t.Errorf("Records = %+v，期望一条记录", result.Records)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Errorf("请求次数 = %d，期望 3（首次失败后重试两次）", n)
	}
}

func TestFetchRemoteExhaustsRetries(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	result, err := FetchRemote(context.Background(), []string{server.URL}, RemoteOptions{
		Timeout:  2 * time.Second,
		Retries:  1,
		Interval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("FetchRemote 返回错误：%v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("Failed = %+v，期望一条失败记录", result.Failed)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("请求次数 = %d，期望 2（首次加一次重试）", n)
	}
}

func TestFetchRemoteRejectsEmptyURLList(t *testing.T) {
	if _, err := FetchRemote(context.Background(), nil, RemoteOptions{}); err == nil {
		t.Error("没有地址时应返回错误")
	}
}

func TestFetchRemoteStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := FetchRemote(ctx, []string{"http://127.0.0.1:1/x"}, RemoteOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("取消不应让整体返回错误：%v", err)
	}
	if len(result.Failed) != 1 {
		t.Errorf("Failed = %+v，期望一条失败记录", result.Failed)
	}
}

func TestNewRemoteClientDisablesProxy(t *testing.T) {
	client := newRemoteClient(0)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("传输层类型 = %T，期望 *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Error("远程源必须禁用代理")
	}
	if client.Timeout != defaultRemoteTimeout {
		t.Errorf("超时 = %v，期望默认值 %v", client.Timeout, defaultRemoteTimeout)
	}
	if got := newRemoteClient(time.Second).Timeout; got != time.Second {
		t.Errorf("显式超时 = %v，期望 1s", got)
	}
}

func TestFetchRemoteAgainstUnreachableHost(t *testing.T) {
	// 回环地址上必定没有服务在监听，用来验证失败被记入清单而不是中断整体。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("获取空闲端口失败：%v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/x"
	result, err := FetchRemote(context.Background(), []string{url}, RemoteOptions{Timeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("FetchRemote 返回错误：%v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("Failed = %+v，期望一条失败记录", result.Failed)
	}
	if result.Failed[0].Error == "" {
		t.Error("失败原因不应为空")
	}
	if len(result.Records) != 0 {
		t.Errorf("Records = %+v，期望空", result.Records)
	}
}

func TestFetchRemoteRecordsMalformedURL(t *testing.T) {
	// 地址本身非法也走失败清单：一个坏地址不该让整次拉取失败。
	result, err := FetchRemote(context.Background(), []string{"http://[::1/x"}, RemoteOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("FetchRemote 返回错误：%v", err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("Failed = %+v，期望一条失败记录", result.Failed)
	}
	if len(result.Records) != 0 {
		t.Errorf("Records = %+v，期望空", result.Records)
	}
}
