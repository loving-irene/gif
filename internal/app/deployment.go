package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type deploymentVersionOutput struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exitCode"`
}

// homeDeployInfo 是首页页脚展示的轻量部署状态，口径对齐 AVS：
// [Asia/Shanghai 更新时间  状态关键字]。
type homeDeployInfo struct {
	UpdatedAt string
	Status    string
}

// FooterText 返回首页页脚文案，格式 [最新更新时间  更新状态]。
func (info homeDeployInfo) FooterText() string {
	updated := info.UpdatedAt
	if updated == "" {
		updated = "未知"
	}
	status := info.Status
	if status == "" {
		status = "unknown"
	}
	return "[" + updated + "  " + status + "]"
}

func (a *App) adminDeploymentVersion(w http.ResponseWriter, r *http.Request) {
	result, err := inspectDeploymentVersion(r.Context(), a.env)
	if err != nil {
		fail(w, http.StatusInternalServerError, "部署版本检查失败")
		return
	}
	respond(w, http.StatusOK, result)
}

func resolveDeployDir(env Env) (string, error) {
	dir := strings.TrimSpace(env.DeployDir)
	if dir == "" {
		dir = "."
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve deployment directory: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(dir); resolveErr == nil {
		dir = resolved
	}
	return dir, nil
}

func deployStatePath(env Env, dir string) string {
	state := env.DeployStateFile
	if state == "" {
		state = ".last_deployed_commit"
	}
	if filepath.IsAbs(state) {
		return state
	}
	return filepath.Join(dir, state)
}

func deployBranch(env Env) string {
	if env.DeployBranch != "" {
		return env.DeployBranch
	}
	return "main"
}

// inspectHomeDeploy 用状态文件与 origin/<branch> 比较部署状态，不执行 fetch/部署。
// 更新时间优先取 gif-server 二进制修改时间，其次状态文件，再次当前可执行文件。
func inspectHomeDeploy(parent context.Context, env Env) homeDeployInfo {
	out := homeDeployInfo{UpdatedAt: "未知", Status: "unknown"}
	dir, err := resolveDeployDir(env)
	if err != nil {
		return out
	}
	stateFile := deployStatePath(env, dir)
	branch := deployBranch(env)

	for _, candidate := range []string{
		filepath.Join(dir, "gif-server"),
		filepath.Join(dir, "gif-server.exe"),
		stateFile,
	} {
		if fileInfo, statErr := os.Stat(candidate); statErr == nil && !fileInfo.IsDir() {
			out.UpdatedAt = formatAsiaShanghai(fileInfo.ModTime())
			break
		}
	}
	if out.UpdatedAt == "未知" {
		if exe, exeErr := os.Executable(); exeErr == nil {
			if fileInfo, statErr := os.Stat(exe); statErr == nil && !fileInfo.IsDir() {
				out.UpdatedAt = formatAsiaShanghai(fileInfo.ModTime())
			}
		}
	}

	deployed := "未知"
	if raw, readErr := os.ReadFile(stateFile); readErr == nil {
		if rev := strings.TrimSpace(string(raw)); validDeployRevision(rev) {
			deployed = rev
		}
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	git := func(ref string) string {
		cmd := exec.CommandContext(ctx, "git", "-c", "safe.directory="+filepath.ToSlash(dir), "-C", dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if raw, runErr := cmd.Output(); runErr == nil {
			return strings.TrimSpace(string(raw))
		}
		return ""
	}
	head := git("HEAD")
	remote := git("refs/remotes/origin/" + branch)
	if deployed == "未知" || remote == "" {
		return out
	}
	out.Status = "newer-commit-available"
	if deployed == remote {
		out.Status = "up-to-date"
	} else if head != "" && head == remote {
		out.Status = "stuck"
	}
	return out
}

func validDeployRevision(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func formatAsiaShanghai(value time.Time) string {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("CST", 8*3600)
	}
	return value.In(location).Format("2006-01-02 15:04:05")
}

func inspectDeploymentVersion(parent context.Context, env Env) (deploymentVersionOutput, error) {
	dir, err := resolveDeployDir(env)
	if err != nil {
		return deploymentVersionOutput{}, err
	}

	script := filepath.Join(dir, "scripts", "check_deploy_version.sh")
	if _, err := os.Stat(script); err != nil {
		return deploymentVersionOutput{}, fmt.Errorf("stat deployment version script: %w", err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return deploymentVersionOutput{}, fmt.Errorf("find bash: %w", err)
	}

	branch := deployBranch(env)
	if env.DeployStateFile == "" {
		env.DeployStateFile = ".last_deployed_commit"
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	shellScript, shellDir := filepath.ToSlash(script), filepath.ToSlash(dir)
	isWSL := runtime.GOOS == "windows" && strings.Contains(strings.ToLower(filepath.Clean(bash)), `\windows\system32\bash.exe`)
	if isWSL {
		if converted, convertErr := wslPath(ctx, script); convertErr == nil {
			shellScript = converted
		}
		if converted, convertErr := wslPath(ctx, dir); convertErr == nil {
			shellDir = converted
		}
	}
	var command *exec.Cmd
	if isWSL {
		wsl, lookErr := exec.LookPath("wsl")
		if lookErr != nil {
			return deploymentVersionOutput{}, fmt.Errorf("find wsl: %w", lookErr)
		}
		stateFile := env.DeployStateFile
		if filepath.IsAbs(stateFile) {
			if converted, convertErr := wslPath(ctx, stateFile); convertErr == nil {
				stateFile = converted
			}
		}
		args := []string{"--exec", "env",
			"APP_DIR=" + shellDir,
			"DEPLOY_STATE_FILE=" + stateFile,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=safe.directory",
			"GIT_CONFIG_VALUE_0=" + shellDir,
		}
		if differentOwner, ok := os.LookupEnv("GIT_TEST_ASSUME_DIFFERENT_OWNER"); ok {
			args = append(args, "GIT_TEST_ASSUME_DIFFERENT_OWNER="+differentOwner)
		}
		args = append(args, "/bin/bash", shellScript, "--non-interactive", "--branch", branch)
		command = exec.CommandContext(ctx, wsl, args...)
	} else {
		command = exec.CommandContext(ctx, bash, shellScript, "--non-interactive", "--branch", branch)
		command.Env = deploymentCommandEnv(env, shellDir)
	}
	output, runErr := command.CombinedOutput()
	result := deploymentVersionOutput{Output: string(output)}
	if runErr == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return deploymentVersionOutput{}, fmt.Errorf("deployment version script timed out: %w", ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return deploymentVersionOutput{}, fmt.Errorf("run deployment version script: %w", runErr)
}

func wslPath(ctx context.Context, path string) (string, error) {
	wsl, err := exec.LookPath("wsl")
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, wsl, "wslpath", "-a", "-u", filepath.ToSlash(path))
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func deploymentCommandEnv(env Env, dir string) []string {
	values := os.Environ()
	set := func(key, value string) {
		prefix := key + "="
		for i, item := range values {
			if strings.HasPrefix(item, prefix) {
				values[i] = prefix + value
				return
			}
		}
		values = append(values, prefix+value)
	}
	set("APP_DIR", dir)
	set("GIT_CONFIG_COUNT", "1")
	set("GIT_CONFIG_KEY_0", "safe.directory")
	set("GIT_CONFIG_VALUE_0", dir)
	set("DEPLOY_STATE_FILE", env.DeployStateFile)
	return values
}
