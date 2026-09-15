package app

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const deploymentFetchFreshness = 15 * time.Minute

var (
	deployBranchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	commitHashPattern   = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)
)

type deploymentCommit struct {
	Hash        string `json:"hash"`
	Short       string `json:"short"`
	Subject     string `json:"subject"`
	CommittedAt int64  `json:"committedAt"`
}

type deploymentVersion struct {
	Status     string            `json:"status"`
	Latest     bool              `json:"latest"`
	Reason     string            `json:"reason,omitempty"`
	Branch     string            `json:"branch"`
	Deployed   *deploymentCommit `json:"deployed,omitempty"`
	Head       *deploymentCommit `json:"head,omitempty"`
	Remote     *deploymentCommit `json:"remote,omitempty"`
	FetchedAt  int64             `json:"fetchedAt"`
	FetchFresh bool              `json:"fetchFresh"`
	CheckedAt  int64             `json:"checkedAt"`
}

func (a *App) adminDeploymentVersion(w http.ResponseWriter, r *http.Request) {
	respond(w, 200, inspectDeploymentVersion(r.Context(), a.env, time.Now()))
}

func inspectDeploymentVersion(parent context.Context, env Env, now time.Time) deploymentVersion {
	branch := env.DeployBranch
	if branch == "" {
		branch = "main"
	}
	result := deploymentVersion{Status: "unknown", Branch: branch, CheckedAt: now.Unix()}
	if !deployBranchPattern.MatchString(branch) || strings.Contains(branch, "..") {
		result.Reason = "invalid-branch"
		return result
	}
	dir := env.DeployDir
	if dir == "" {
		dir = "."
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	root, err := gitOutput(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		result.Reason = "not-git-repository"
		return result
	}
	headHash, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil || !commitHashPattern.MatchString(headHash) {
		result.Reason = "head-unavailable"
		return result
	}
	remoteHash, err := gitOutput(ctx, root, "rev-parse", "--verify", "refs/remotes/origin/"+branch)
	if err != nil || !commitHashPattern.MatchString(remoteHash) {
		result.Reason = "no-origin-ref"
		result.Head = gitCommitInfo(ctx, root, headHash)
		return result
	}

	stateFile := env.DeployStateFile
	if stateFile == "" {
		stateFile = ".last_deployed_commit"
	}
	if !filepath.IsAbs(stateFile) {
		stateFile = filepath.Join(root, stateFile)
	}
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		result.Reason = "missing-state-file"
		result.Head = gitCommitInfo(ctx, root, headHash)
		result.Remote = gitCommitInfo(ctx, root, remoteHash)
		setFetchFreshness(ctx, root, now, &result)
		return result
	}
	deployedRaw := strings.TrimSpace(string(raw))
	if !commitHashPattern.MatchString(deployedRaw) {
		result.Reason = "invalid-state-commit"
		return result
	}
	deployedHash, err := gitOutput(ctx, root, "rev-parse", "--verify", deployedRaw+"^{commit}")
	if err != nil || !commitHashPattern.MatchString(deployedHash) {
		result.Reason = "unresolvable-state-commit"
		return result
	}

	result.Deployed = gitCommitInfo(ctx, root, deployedHash)
	result.Head = gitCommitInfo(ctx, root, headHash)
	result.Remote = gitCommitInfo(ctx, root, remoteHash)
	setFetchFreshness(ctx, root, now, &result)
	switch {
	case deployedHash == remoteHash && result.FetchFresh:
		result.Status = "up-to-date"
		result.Latest = true
	case deployedHash == remoteHash:
		result.Status = "stale-reference"
		result.Reason = "remote-reference-stale"
	case headHash == remoteHash:
		result.Status = "stuck"
		result.Reason = "deployment-stuck"
	default:
		result.Status = "newer-commit-available"
		result.Reason = "deployment-behind"
	}
	return result
}

func setFetchFreshness(ctx context.Context, root string, now time.Time, result *deploymentVersion) {
	path, err := gitOutput(ctx, root, "rev-parse", "--git-path", "FETCH_HEAD")
	if err != nil || path == "" {
		return
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	result.FetchedAt = info.ModTime().Unix()
	result.FetchFresh = now.Sub(info.ModTime()) <= deploymentFetchFreshness
}

func gitCommitInfo(ctx context.Context, root, hash string) *deploymentCommit {
	out, err := gitOutput(ctx, root, "show", "-s", "--format=%H%x1f%h%x1f%s%x1f%ct", hash)
	if err != nil {
		return &deploymentCommit{Hash: hash, Short: shortCommit(hash)}
	}
	parts := strings.SplitN(out, "\x1f", 4)
	if len(parts) != 4 {
		return &deploymentCommit{Hash: hash, Short: shortCommit(hash)}
	}
	committedAt, _ := strconv.ParseInt(parts[3], 10, 64)
	return &deploymentCommit{Hash: parts[0], Short: parts[1], Subject: parts[2], CommittedAt: committedAt}
}

func shortCommit(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := command.Output()
	return strings.TrimSpace(string(out)), err
}
