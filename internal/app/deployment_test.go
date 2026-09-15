package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func deploymentTestScript(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("Bash required for deployment version test")
	}
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}
	source := `#!/usr/bin/env bash
printf 'application: %s\n' "$APP_DIR"
printf 'arguments: %s\n' "$*"
printf 'state file: %s\n' "$DEPLOY_STATE_FILE"
printf 'status: newer-commit-available\n'
printf 'detail: pending deployment\n' >&2
exit 3
`
	if err := os.WriteFile(filepath.Join(scriptDir, "check_deploy_version.sh"), []byte(source), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func deploymentTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git required for deployment version test")
	}
	command := exec.Command(git, append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func deploymentTestRepo(t *testing.T) string {
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
	commit := deploymentTestGit(t, dir, "rev-parse", "HEAD")
	deploymentTestGit(t, dir, "update-ref", "refs/remotes/origin/main", commit)
	if err := os.WriteFile(filepath.Join(dir, ".last_deployed_commit"), []byte(commit+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	scriptDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "check_deploy_version.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "check_deploy_version.sh"), script, 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInspectDeploymentVersionReturnsScriptOutput(t *testing.T) {
	dir := deploymentTestScript(t)
	result, err := inspectDeploymentVersion(context.Background(), Env{
		DeployDir: dir, DeployBranch: "release", DeployStateFile: "deploy.state",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 3 {
		t.Fatalf("exit code=%d want 3", result.ExitCode)
	}
	for _, want := range []string{
		"application: ",
		filepath.Base(dir),
		"arguments: --non-interactive --branch release",
		"state file: deploy.state",
		"status: newer-commit-available",
		"detail: pending deployment",
	} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("script output missing %q:\n%s", want, result.Output)
		}
	}
}

func TestInspectDeploymentVersionRequiresScript(t *testing.T) {
	_, err := inspectDeploymentVersion(context.Background(), Env{DeployDir: t.TempDir()})
	if err == nil {
		t.Fatal("missing deployment version script accepted")
	}
}

func TestInspectDeploymentVersionWithDifferentRepositoryOwner(t *testing.T) {
	// 必须先创建仓库，再开启所有者模拟；否则测试自身的 git config 就会
	// 被 Git 拒绝，尚未运行待测的部署版本检查。
	dir := deploymentTestRepo(t)
	t.Setenv("GIT_TEST_ASSUME_DIFFERENT_OWNER", "1")
	result, err := inspectDeploymentVersion(context.Background(), Env{
		DeployDir: dir, DeployBranch: "main", DeployStateFile: ".last_deployed_commit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Output, "status:           up-to-date") {
		t.Fatalf("different-owner deployment output mismatch: %+v", result)
	}
}

func TestAdminDeploymentVersionPageAndAuthorization(t *testing.T) {
	dir := deploymentTestScript(t)
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
	var result deploymentVersionOutput
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 3 || !strings.Contains(result.Output, "status: newer-commit-available") {
		t.Fatalf("admin deployment output mismatch: %+v", result)
	}

	body := request(t, a, nil, "GET", "/who", nil).Body.String()
	for _, want := range []string{"/assets/admin.v32.js", `data-tab="gallerySection"`, `id="gallerySection"`, `data-tab="deploymentSection"`, `id="deploymentSection"`, `id="refreshDeployment"`, `id="deploymentOutput"`} {
		if !strings.Contains(body, want) {
			t.Fatal("admin deployment page missing", want)
		}
	}
	raw, err := web.ReadFile("web/admin.v32.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, want := range []string{"/api/admin/gallery", "loadGallery", "gallerySection: loadGallery", "/api/admin/deployment-version", "loadDeploymentVersion", "deploymentSection: loadDeploymentVersion", `$("deploymentOutput").value = result.output`} {
		if !strings.Contains(source, want) {
			t.Fatal("admin deployment script missing", want)
		}
	}
}
