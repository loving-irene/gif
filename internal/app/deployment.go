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

func (a *App) adminDeploymentVersion(w http.ResponseWriter, r *http.Request) {
	result, err := inspectDeploymentVersion(r.Context(), a.env)
	if err != nil {
		fail(w, http.StatusInternalServerError, "部署版本检查失败")
		return
	}
	respond(w, http.StatusOK, result)
}

func inspectDeploymentVersion(parent context.Context, env Env) (deploymentVersionOutput, error) {
	dir := strings.TrimSpace(env.DeployDir)
	if dir == "" {
		dir = "."
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return deploymentVersionOutput{}, fmt.Errorf("resolve deployment directory: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(dir); resolveErr == nil {
		dir = resolved
	}

	script := filepath.Join(dir, "scripts", "check_deploy_version.sh")
	if _, err := os.Stat(script); err != nil {
		return deploymentVersionOutput{}, fmt.Errorf("stat deployment version script: %w", err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return deploymentVersionOutput{}, fmt.Errorf("find bash: %w", err)
	}

	branch := env.DeployBranch
	if branch == "" {
		branch = "main"
	}
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
