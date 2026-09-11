package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestFailureMessageIsUnifiedAndQuotaContractPreserved(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "message-test")
	a.provider = func(context.Context, Settings, string, []string) (string, error) {
		return "", errors.New("private upstream diagnostic")
	}
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
