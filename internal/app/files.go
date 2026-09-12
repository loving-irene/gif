package app

import (
	"os"
	"path/filepath"
	"time"
)

// 任务的生成输入与结果文件保存在数据库同目录的 files 子目录，最多保留3天。
const resultRetention = 3 * 24 * time.Hour

func (a *App) filePath(id, name string) string {
	// id 由服务端生成的十六进制 token，name 为固定值，无路径穿越风险。
	return filepath.Join(a.files, id+"."+name)
}
func (a *App) saveFile(id, name string, data []byte) error {
	return os.WriteFile(a.filePath(id, name), data, 0600)
}
func (a *App) readFile(id, name string) ([]byte, error) {
	return os.ReadFile(a.filePath(id, name))
}
func (a *App) removeFile(id, name string) {
	os.Remove(a.filePath(id, name))
}

// cleanupFiles 兜底清理超过保留期的任务文件；正常流程中输入文件会在任务结束后主动删除。
func (a *App) cleanupFiles() {
	entries, err := os.ReadDir(a.files)
	if err != nil {
		return
	}
	deadline := time.Now().Add(-resultRetention)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().Before(deadline) {
			os.Remove(filepath.Join(a.files, entry.Name()))
		}
	}
}
