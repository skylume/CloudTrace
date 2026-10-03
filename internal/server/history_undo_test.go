package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"cloudtrace/internal/model"
)

// 删除之后在撤销窗口内把它捞回来。
//
// 软删除的价值全在这条命令上：没有它，用户点错一次删除就只能看着记录消失。
func TestWSHistoryUndoRestoresRecord(t *testing.T) {
	st := newTestStack(t, nil)
	seeded := seedSpeedHistory(t, st, []model.IPRecord{
		{IP: "1.1.1.1", Port: 443, Latency: 40, LatencyAvg: 42, Sent: 3, Recv: 3, Colo: "HKG", SpeedMBps: 12.5},
	})

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, fmt.Sprintf(`{"type":"history/delete","data":{"id":%q}}`, seeded.ID))
	var deleted historyDeleteResp
	decode(t, readUntil(t, conn, cmdHistoryDelete, 5*time.Second), &deleted)
	if deleted.ID != seeded.ID {
		t.Fatalf("删除响应的 ID = %q，期望 %q", deleted.ID, seeded.ID)
	}
	if deleted.UndoMS <= 0 {
		t.Fatal("撤销窗口为 0，前端没有机会撤销")
	}

	// 删除后列表里应该没有了。
	if count := listHistoryCount(t, conn); count != 0 {
		t.Fatalf("删除后列表里还有 %d 条", count)
	}

	send(t, conn, fmt.Sprintf(`{"type":"history/undo","data":{"id":%q}}`, seeded.ID))
	var restored historyIDResp
	decode(t, readUntil(t, conn, cmdHistoryUndo, 5*time.Second), &restored)
	if restored.ID != seeded.ID {
		t.Fatalf("撤销响应的 ID = %q，期望 %q", restored.ID, seeded.ID)
	}

	if count := listHistoryCount(t, conn); count != 1 {
		t.Fatalf("撤销后列表里有 %d 条，期望 1 条", count)
	}
}

// 撤销一个不存在的 ID 要报错，而不是静默成功——静默成功会让前端把撤销入口
// 收掉却以为记录回来了。
func TestWSHistoryUndoUnknownIDFails(t *testing.T) {
	st := newTestStack(t, nil)

	conn := st.mustDial(t)
	readUntil(t, conn, eventState, 3*time.Second)

	send(t, conn, `{"type":"history/undo","data":{"id":"根本没有这个"}}`)
	var payload errorPayload
	decode(t, readUntil(t, conn, eventError, 5*time.Second), &payload)
	if payload.Code == "" {
		t.Fatal("撤销不存在的记录应当报错")
	}
}

// listHistoryCount 拉一次历史列表并返回条数。
func listHistoryCount(t *testing.T, conn *websocket.Conn) int {
	t.Helper()
	send(t, conn, `{"type":"history/list","data":{"filter":{}}}`)
	var list historyListResp
	decode(t, readUntil(t, conn, cmdHistoryList, 5*time.Second), &list)
	return len(list.Entries)
}
