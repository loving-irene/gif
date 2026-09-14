package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConcurrentGenerationDeduplicatesBeforeCharging(t *testing.T) {
	for _, sameRequest := range []bool{false, true} {
		t.Run(fmt.Sprint(sameRequest), func(t *testing.T) {
			a := testApp(t)
			s := loginDevice(t, a, "concurrent-duplicate")
			release := blockingProvider(a)
			defer close(release)
			inputs := []GenerateInput{draftInput(), draftInput()}
			if sameRequest {
				inputs[1] = inputs[0]
			}
			var wg sync.WaitGroup
			start := make(chan struct{})
			statuses := make([]int, 2)
			bodies := make([]map[string]any, 2)
			for i := range inputs {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					w := request(t, a, s, "POST", "/api/generate", inputs[i])
					statuses[i] = w.Code
					json.Unmarshal(w.Body.Bytes(), &bodies[i])
				}(i)
			}
			close(start)
			wg.Wait()
			if sameRequest {
				if statuses[0] != 202 || statuses[1] != 202 || bodies[0]["id"] != bodies[1]["id"] {
					t.Fatalf("并发重传应返回同一任务：%v %v", statuses, bodies)
				}
			} else {
				if statuses[0]+statuses[1] != 202+409 || (bodies[0]["duplicate"] != true && bodies[1]["duplicate"] != true) {
					t.Fatalf("并发同款提交应触发二次确认：%v %v", statuses, bodies)
				}
			}
			if u, _ := a.readUser(s.User.ID); u.Credits != s.User.Credits-1 {
				t.Fatal("并发提交重复扣次", u.Credits)
			}
		})
	}
}

func TestCloseWaitsForGenerationAndPreservesInput(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "close-job")
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseJob := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseJob()
	a.providerCall = func(ctx context.Context, _ Settings, _ string, _ []string, onTaskID func(string)) (string, error) {
		onTaskID("shutdown-upstream")
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return "", ctx.Err()
	}
	id := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	// 确认生成回调已启动，避免只测到还没开始的任务。
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("生成回调没有启动")
	}
	closed := make(chan struct{})
	go func() { a.Close(); close(closed) }()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("关停未取消生成上下文")
	}
	select {
	case <-closed:
		t.Fatal("生成回调未退出时就关闭了数据库")
	case <-time.After(50 * time.Millisecond):
	}
	if err := a.db.Ping(); err != nil {
		t.Fatal("生成回调退出前数据库必须可用", err)
	}
	releaseJob()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("生成回调退出后服务没有完成关停")
	}
	if _, err := a.readFile(id, "input.json"); err != nil {
		t.Fatal("关停必须保留输入供重启恢复", err)
	}
}

func TestInputWriteFailureReleasesGenerationSlot(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "write-failure")
	dir := a.files
	// 用普通文件替代目录，跨平台稳定模拟输入写盘失败。
	a.files = filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(a.files, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < generationSlots+1; i++ {
		w := request(t, a, s, "POST", "/api/generate", draftInput())
		// 释放通知可能让调度器短暂借用槽位检查队列，等待它归还后再判断泄漏。
		deadline := time.Now().Add(time.Second)
		for len(a.slots) != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if w.Code != 500 || len(a.slots) != 0 {
			t.Fatalf("写盘失败后槽位未释放：status=%d slots=%d", w.Code, len(a.slots))
		}
	}
	if u, _ := a.readUser(s.User.ID); u.Credits != s.User.Credits {
		t.Fatal("输入写盘失败不应扣次")
	}
	a.files = dir
	a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
		return sampleImage(false), nil
	})
	id := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
	if j := waitJob(t, a, s, id); j.Status != "succeeded" {
		t.Fatal("磁盘恢复后应继续执行任务", j.Status)
	}
}

func TestRejectedGenerationRemovesInput(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "no-credits")
	a.cancel()
	a.wg.Wait()
	a.db.Exec("UPDATE users SET gift=0,paid=0 WHERE id=?", s.User.ID)
	if w := request(t, a, s, "POST", "/api/generate", draftInput()); w.Code != 402 {
		t.Fatal(w.Code, w.Body.String())
	}
	entries, err := os.ReadDir(a.files)
	if err != nil || len(entries) != 0 || len(a.slots) != 0 {
		t.Fatalf("拒绝创建后有残留：files=%d slots=%d err=%v", len(entries), len(a.slots), err)
	}
}

func TestFailureUsesSubmissionRefundPolicy(t *testing.T) {
	for _, charge := range []bool{false, true} {
		t.Run(fmt.Sprint(charge), func(t *testing.T) {
			a := testApp(t)
			setChargeOnFailure(t, a, charge)
			s := loginDevice(t, a, "refund-policy")
			// 占满槽位，让管理员在真正执行之前修改失败扣次策略。
			for i := 0; i < cap(a.slots); i++ {
				a.slots <- struct{}{}
			}
			a.providerCall = asProviderCall(func(context.Context, Settings, string, []string) (string, error) {
				return "", errors.New("test provider failure")
			})
			id := jobID(t, request(t, a, s, "POST", "/api/generate", draftInput()))
			setChargeOnFailure(t, a, !charge)
			for i := 0; i < cap(a.slots); i++ {
				<-a.slots
			}
			a.signalDispatch()
			j := waitForStatus(t, a, s, id, "failed")
			want := s.User.Credits
			if charge {
				want--
			}
			if u, _ := a.readUser(s.User.ID); u.Credits != want || j.Charged != charge {
				t.Fatalf("应沿用提交时策略：credits=%d want=%d charged=%v", u.Credits, want, j.Charged)
			}
			a.jobsMu.Lock()
			delete(a.jobs, id)
			a.jobsMu.Unlock()
			if j := fetchJSONJob(t, a, s, id); j.Charged != charge {
				t.Fatalf("缓存淘汰后扣次状态不一致：%v", j.Charged)
			}
		})
	}
}

func TestCleanupExpiredPendingJobRefundsOnce(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "cleanup-refund")
	// 停止后台调度，独立验证周期清理本身的收口路径。
	a.cancel()
	a.wg.Wait()
	id := token(16)
	created := time.Now().Add(-jobTimeLimit - time.Minute).Unix()
	_, err := a.db.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,refund_failure,created,upstream_task_id) VALUES(?,?,?,'test','draft',?,1,0,1,?,'test-upstream')", id, s.User.ID, id, statusPendingUpstream, created)
	if err != nil {
		t.Fatal(err)
	}
	a.db.Exec("UPDATE users SET gift=gift-1 WHERE id=?", s.User.ID)
	a.db.Exec("INSERT INTO usage_stats(job_id,user_id,kind,created) VALUES(?,?,'draft',?)", id, s.User.ID, created)
	a.jobs[id] = &Job{ID: id, User: s.User.ID, Status: statusPendingUpstream, Charged: true, StartedAt: created * 1000}
	if err := a.saveFile(id, "input.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	// 模拟余额写入失败：终态、余额、扣次字段和统计必须整体回滚，之后仍可重试。
	if _, err := a.db.Exec("CREATE TRIGGER reject_refund BEFORE UPDATE OF gift ON users BEGIN SELECT RAISE(ABORT, 'test refund failure'); END"); err != nil {
		t.Fatal(err)
	}
	a.cleanup()
	if j := fetchJSONJob(t, a, s, id); j.Status != statusPendingUpstream || !j.Charged {
		t.Fatalf("退款失败时任务不应提前结束：%+v", j)
	}
	if u, _ := a.readUser(s.User.ID); u.Credits != s.User.Credits-1 {
		t.Fatal("退款失败后余额未回滚", u.Credits)
	}
	if _, err := a.db.Exec("DROP TRIGGER reject_refund"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		a.cleanup()
		j := fetchJSONJob(t, a, s, id)
		if j.Status != "failed" || j.Charged {
			t.Fatalf("清理后仍返回旧状态：status=%s charged=%v", j.Status, j.Charged)
		}
		if u, _ := a.readUser(s.User.ID); u.Credits != s.User.Credits {
			t.Fatalf("清理退款缺失或重复：%d", u.Credits)
		}
	}
	var count int
	a.db.QueryRow("SELECT COUNT(*) FROM usage_stats WHERE job_id=?", id).Scan(&count)
	if count != 0 {
		t.Fatal("已退款任务仍计入消耗统计")
	}
	if _, err := a.readFile(id, "input.json"); !os.IsNotExist(err) {
		t.Fatal("超时收口后应删除输入文件", err)
	}
	if a.markRunning(id) {
		t.Fatal("清理后的失败任务不能被调度器重新启动")
	}
}

func TestPruneCallsPreservesActiveJobs(t *testing.T) {
	a := testApp(t)
	s := loginDevice(t, a, "prune-active")
	a.cancel()
	a.wg.Wait()
	for i := 0; i < 104; i++ {
		status := "succeeded"
		if i < 3 {
			status = []string{statusQueued, statusRunning, statusPendingUpstream}[i]
		}
		id := fmt.Sprintf("prune-%03d", i)
		if _, err := a.db.Exec("INSERT INTO jobs(id,user_id,request_id,digest,kind,status,gift_cost,paid_cost,created) VALUES(?,?,?,'test','draft',?,1,0,?)", id, s.User.ID, id, status, i); err != nil {
			t.Fatal(err)
		}
	}
	a.pruneCalls(s.User.ID)
	if n := countJobs(t, a, s.User.ID, statusQueued, statusRunning, statusPendingUpstream); n != 3 {
		t.Fatalf("日志裁剪误删在途任务：剩余%d", n)
	}
	w := request(t, a, s, "GET", "/api/calls", nil)
	var result struct {
		Items []json.RawMessage `json:"items"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != 100 {
		t.Fatal("调用记录接口仍应最多返回100条", w.Body.String())
	}
}
