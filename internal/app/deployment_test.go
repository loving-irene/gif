package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func deploymentTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git required for deployment version test")
	}
	command := exec.Command(git, append([]string{"-C", dir}, args...)...)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func deploymentTestRepo(t *testing.T, now time.Time) (string, string) {
	t.Helper()
	dir := t.TempDir()
	deploymentTestGit(t, dir, "init", "-b", "main")
	deploymentTestGit(t, dir, "config", "user.email", "deploy-test@example.com")
	deploymentTestGit(t, dir, "config", "user.name", "Deploy Test")
	if err := os.WriteFile(filepath.Join(dir, "version.txt"), []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	deploymentTestGit(t, dir, "add", "version.txt")
	deploymentTestGit(t, dir, "commit", "-m", "first deployment")
	first := deploymentTestGit(t, dir, "rev-parse", "HEAD")
	deploymentTestGit(t, dir, "update-ref", "refs/remotes/origin/main", first)
	if err := os.WriteFile(filepath.Join(dir, ".last_deployed_commit"), []byte(first+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fetchHead := filepath.Join(dir, ".git", "FETCH_HEAD")
	if err := os.WriteFile(fetchHead, []byte(first+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fetchHead, now, now); err != nil {
		t.Fatal(err)
	}
	return dir, first
}

func TestInspectDeploymentVersionStates(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	dir, first := deploymentTestRepo(t, now)
	env := Env{DeployDir: dir, DeployBranch: "main", DeployStateFile: ".last_deployed_commit"}

	version := inspectDeploymentVersion(context.Background(), env, now)
	if version.Status != "up-to-date" || !version.Latest || version.Deployed == nil || version.Deployed.Hash != first || !version.FetchFresh {
		t.Fatalf("up-to-date version mismatch: %+v", version)
	}

	if err := os.WriteFile(filepath.Join(dir, "version.txt"), []byte("second\n"), 0600); err != nil {
		t.Fatal(err)
	}
	deploymentTestGit(t, dir, "add", "version.txt")
	deploymentTestGit(t, dir, "commit", "-m", "second deployment")
	second := deploymentTestGit(t, dir, "rev-parse", "HEAD")
	deploymentTestGit(t, dir, "update-ref", "refs/remotes/origin/main", second)
	version = inspectDeploymentVersion(context.Background(), env, now)
	if version.Status != "stuck" || version.Latest {
		t.Fatalf("stuck version mismatch: %+v", version)
	}

	deploymentTestGit(t, dir, "update-ref", "refs/heads/main", first)
	version = inspectDeploymentVersion(context.Background(), env, now)
	if version.Status != "newer-commit-available" || version.Latest {
		t.Fatalf("newer version mismatch: %+v", version)
	}

	if err := os.WriteFile(filepath.Join(dir, ".last_deployed_commit"), []byte(second+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fetchHead := filepath.Join(dir, ".git", "FETCH_HEAD")
	stale := now.Add(-deploymentFetchFreshness - time.Minute)
	if err := os.Chtimes(fetchHead, stale, stale); err != nil {
		t.Fatal(err)
	}
	version = inspectDeploymentVersion(context.Background(), env, now)
	if version.Status != "stale-reference" || version.Latest || version.FetchFresh {
		t.Fatalf("stale remote version mismatch: %+v", version)
	}
	if err := os.Remove(filepath.Join(dir, ".last_deployed_commit")); err != nil {
		t.Fatal(err)
	}
	version = inspectDeploymentVersion(context.Background(), env, now)
	if version.Status != "unknown" || version.Reason != "missing-state-file" || version.Latest {
		t.Fatalf("missing deployment state mismatch: %+v", version)
	}
}

func TestAdminDeploymentVersionPageAndAuthorization(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	dir, _ := deploymentTestRepo(t, now)
	a := testApp(t)
	a.env.DeployDir = dir
	a.env.DeployBranch = "main"
	a.env.DeployStateFile = ".last_deployed_commit"
	user := loginDevice(t, a, "deployment-version-user")
	if w := request(t, a, user, "GET", "/api/admin/deployment-version", nil); w.Code != 403 {
		t.Fatalf("non-admin deployment version status=%d want 403", w.Code)
	}
	loginAdmin(t, a, user)
	w := request(t, a, user, "GET", "/api/admin/deployment-version", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var version deploymentVersion
	if err := json.Unmarshal(w.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if version.Status != "up-to-date" || !version.Latest || version.Remote == nil {
		t.Fatalf("admin deployment version mismatch: %+v", version)
	}

	body := request(t, a, nil, "GET", "/who", nil).Body.String()
	for _, want := range []string{"/assets/admin.v27.js", `data-tab="deploymentSection"`, `id="deploymentSection"`, `id="refreshDeployment"`} {
		if !strings.Contains(body, want) {
			t.Fatal("admin deployment page missing", want)
		}
	}
	raw, err := web.ReadFile("web/admin.v27.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, want := range []string{"/api/admin/deployment-version", "loadDeploymentVersion", "deploymentSection: loadDeploymentVersion"} {
		if !strings.Contains(source, want) {
			t.Fatal("admin deployment script missing", want)
		}
	}
}
