package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var buildVersion = "dev"
var updateManifestURL = "https://ymsq.edvulcan.top/downloads/pos-version.json"

const maxUpdateSize = 200 << 20

func trustedUpdateURL(rawURL string) bool {
	parsedURL, err := url.Parse(rawURL)
	return err == nil && parsedURL.Scheme == "https" && parsedURL.Hostname() == "ymsq.edvulcan.top"
}

func trustedUpdateClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if !trustedUpdateURL(request.URL.String()) {
				return errors.New("更新服务重定向到不可信地址")
			}
			if len(via) >= 5 {
				return errors.New("更新服务重定向次数过多")
			}
			return nil
		},
	}
}

type updateManifest struct {
	Version     string `json:"version"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256"`
}

type UpdateCheckResult struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"current_version"`
	Version        string `json:"version"`
	Message        string `json:"message"`
}

func fetchUpdateManifest() (*updateManifest, error) {
	if !trustedUpdateURL(updateManifestURL) {
		return nil, errors.New("更新清单地址不可信")
	}
	client := trustedUpdateClient(10 * time.Second)
	response, err := client.Get(updateManifestURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("更新服务返回状态 %d", response.StatusCode)
	}
	var manifest updateManifest
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&manifest); err != nil {
		return nil, err
	}
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.DownloadURL = strings.TrimSpace(manifest.DownloadURL)
	manifest.SHA256 = strings.ToLower(strings.TrimSpace(manifest.SHA256))
	if !trustedUpdateURL(manifest.DownloadURL) {
		return nil, errors.New("更新下载地址不可信")
	}
	decodedHash, err := hex.DecodeString(manifest.SHA256)
	if err != nil || len(decodedHash) != sha256.Size || manifest.Version == "" {
		return nil, errors.New("更新清单不完整")
	}
	return &manifest, nil
}

func (a *App) CheckForUpdate() UpdateCheckResult {
	result := UpdateCheckResult{CurrentVersion: buildVersion}
	if runtime.GOOS != "windows" || buildVersion == "dev" {
		result.Message = "当前版本不支持自动更新检查"
		return result
	}
	manifest, err := fetchUpdateManifest()
	if err != nil {
		result.Message = "暂时无法检查窗口端更新"
		return result
	}
	result.Version = manifest.Version
	result.Available = manifest.Version != buildVersion
	if result.Available {
		result.Message = "发现窗口端新版本"
	} else {
		result.Message = "当前已是最新版本"
	}
	return result
}

func (a *App) InstallUpdate() HardwareResult {
	if runtime.GOOS != "windows" || buildVersion == "dev" {
		return HardwareResult{Success: false, Message: "当前版本不支持自动更新"}
	}
	manifest, err := fetchUpdateManifest()
	if err != nil {
		return HardwareResult{Success: false, Message: "获取更新信息失败"}
	}
	if manifest.Version == buildVersion {
		return HardwareResult{Success: true, Message: "当前已是最新版本"}
	}
	client := trustedUpdateClient(5 * time.Minute)
	response, err := client.Get(manifest.DownloadURL)
	if err != nil {
		return HardwareResult{Success: false, Message: "下载更新失败"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return HardwareResult{Success: false, Message: "下载更新失败"}
	}
	binary, err := io.ReadAll(io.LimitReader(response.Body, maxUpdateSize+1))
	if err != nil || len(binary) == 0 || len(binary) > maxUpdateSize {
		return HardwareResult{Success: false, Message: "更新文件无效或超过大小限制"}
	}
	digest := sha256.Sum256(binary)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), manifest.SHA256) {
		return HardwareResult{Success: false, Message: "更新文件校验失败，已拒绝安装"}
	}
	executable, err := os.Executable()
	if err != nil {
		return HardwareResult{Success: false, Message: "无法定位当前窗口端程序"}
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return HardwareResult{Success: false, Message: "无法定位当前窗口端程序"}
	}
	updatePath := executable + ".update"
	if err := os.WriteFile(updatePath, binary, 0600); err != nil {
		return HardwareResult{Success: false, Message: "无法写入更新文件，请检查安装目录权限"}
	}
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("ticket-pos-update-%d.cmd", os.Getpid()))
	script := fmt.Sprintf("@echo off\r\n:wait\r\ntasklist /FI \"PID eq %d\" 2>NUL | find \"%d\" >NUL\r\nif not errorlevel 1 (\r\n timeout /T 1 /NOBREAK >NUL\r\n goto wait\r\n)\r\nmove /Y \"%s\" \"%s\" >NUL\r\nstart \"\" \"%s\"\r\ndel \"%%~f0\"\r\n", os.Getpid(), os.Getpid(), updatePath, executable, executable)
	if err := os.WriteFile(scriptPath, []byte(script), 0600); err != nil {
		_ = os.Remove(updatePath)
		return HardwareResult{Success: false, Message: "无法准备更新安装程序"}
	}
	if err := startUpdateScript(scriptPath); err != nil {
		_ = os.Remove(updatePath)
		_ = os.Remove(scriptPath)
		return HardwareResult{Success: false, Message: "无法启动更新安装程序"}
	}
	wailsruntime.Quit(a.ctx)
	return HardwareResult{Success: true, Message: "正在安装更新并重启"}
}
