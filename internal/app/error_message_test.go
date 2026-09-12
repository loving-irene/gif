package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFailureMessageIsUnifiedAndQuotaContractPreserved(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "message-test")
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		return "", errors.New("private upstream diagnostic")
	})
	id := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	j := waitJob(t, a, s, id)
	if j.Status != "failed" || j.Error != "网络异常，请稍后重试~" || !j.Charged {
		t.Fatal("failure contract changed", j.Status, j.Error, j.Charged)
	}
	for _, status := range []int{500, 502, 503} {
		w := httptest.NewRecorder()
		fail(w, status, "internal failure details")
		var body map[string]string
		json.Unmarshal(w.Body.Bytes(), &body)
		if body["error"] != networkErrorMessage || w.Code != status {
			t.Fatal("HTTP failure leaked internal reason")
		}
	}
}

// TestAdminJobHistoryShowsFailureReason 验证失败原因入库并在管理后台任务历史展示，
// 同时用户侧查询仍然只返回统一的网络异常文案。
func TestAdminJobHistoryShowsFailureReason(t *testing.T) {
	a := testApp(t)
	admin := codeAdmin(t, a, loginDevice(t, a, "history-admin"))
	s := loginDevice(t, a, "history-user")
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		return "", &providerFailure{Phase: "submit", Status: 401, Code: "401", Message: "Invalid API key"}
	})
	id := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	j := waitJob(t, a, s, id)
	if j.Status != "failed" || j.Error != networkErrorMessage {
		t.Fatal("user-facing failure contract changed", j.Status, j.Error)
	}
	w := request(t, a, admin, "GET", "/api/admin/jobs/history", nil)
	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || list.Total != 1 || len(list.Items) != 1 {
		t.Fatal("history list wrong", w.Code, w.Body.String())
	}
	item := list.Items[0]
	if item["id"] != id || item["status"] != "failed" || item["kind"] != "draft" || item["user"] != s.User.ID {
		t.Fatal("history item wrong", item)
	}
	if created, _ := item["created"].(float64); created <= 0 {
		t.Fatal("history created missing", item["created"])
	}
	reason, _ := item["reason"].(string)
	if !strings.Contains(reason, "submit") || !strings.Contains(reason, "Invalid API key") {
		t.Fatal("failure reason missing or sanitized away", reason)
	}
	// 原因同时落库，重启后仍可追溯。
	var stored string
	if a.db.QueryRow("SELECT error_message FROM jobs WHERE id=?", id).Scan(&stored) != nil || stored != reason {
		t.Fatal("error_message not persisted", stored)
	}
	// 非管理员不能访问任务历史。
	w = request(t, a, s, "GET", "/api/admin/jobs/history", nil)
	if w.Code != 403 {
		t.Fatal("non-admin should be rejected", w.Code)
	}
}
