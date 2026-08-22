// gate-provision performs the one-time Linux gate installation handshake.
// The activation code is read from stdin and is never accepted as a command
// line flag or environment variable. Long-lived device credentials are only
// written to the root-owned configuration file after the authenticated
// envelope has been decrypted locally.
package main

import (
	"bufio"
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"ticket-backend/internal/deviceauth"
	"ticket-backend/internal/deviceprovision"
	"time"
)

type installState struct {
	InstallationID string `json:"installation_id"`
	PrivateKey     string `json:"private_key"`
	PublicKey      string `json:"public_key"`
	Fingerprint    string `json:"fingerprint"`
}

type claimResponse struct {
	Status   string `json:"status"`
	Envelope string `json:"envelope"`
}

func main() {
	serverFlag := flag.String("server-url", strings.TrimSpace(os.Getenv("GATE_SERVER_URL")), "HTTPS server base URL (not a secret)")
	configFlag := flag.String("config", "/etc/ticket-gate/gate-client.env", "gate-client environment file")
	stateDirFlag := flag.String("state-dir", "/var/lib/ticket-gate", "local installation state directory")
	flag.Parse()

	serverURL, err := normalizeServerURL(*serverFlag)
	if err != nil {
		fatal(err)
	}
	configPath := filepath.Clean(*configFlag)
	stateDir := filepath.Clean(*stateDirFlag)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		fatal(fmt.Errorf("创建安装状态目录失败: %w", err))
	}
	statePath := filepath.Join(stateDir, ".provisioning.json")

	state, privateKey, err := loadOrCreateState(statePath)
	if err != nil {
		fatal(err)
	}
	if err := ensurePrivatePublicMatch(state, privateKey); err != nil {
		fatal(err)
	}

	envValues, envExists, err := readEnvFile(configPath)
	if err != nil {
		fatal(fmt.Errorf("读取现有配置失败: %w", err))
	}
	if envExists && state.InstallationID != "" {
		if err := confirmFromEnv(serverURL, state, envValues); err == nil {
			_ = os.Remove(statePath)
			fmt.Fprintln(os.Stdout, "闸机安装已确认，现有配置可继续使用。")
			return
		}
	}
	if envExists && state.InstallationID == "" {
		fatal(errors.New("目标配置已存在；如需重新绑定，请先备份并移除旧配置"))
	}

	activationCode := readActivationCode()
	if activationCode == "" {
		fatal(errors.New("安装绑定码不能为空"))
	}
	claim, err := claim(serverURL, activationCode, state)
	if err != nil {
		fatal(err)
	}
	bundle, err := deviceprovision.DecryptBundle(claim.Envelope, privateKey)
	if err != nil {
		fatal(fmt.Errorf("解密安装配置失败: %w", err))
	}
	if err := validateBundle(bundle, serverURL); err != nil {
		fatal(err)
	}
	state.Fingerprint = deviceprovision.Fingerprint(privateKey.PublicKey().Bytes())
	if err := writeState(statePath, state); err != nil {
		fatal(err)
	}

	envValues = map[string]string{
		"GATE_SERVER_URL":         bundle.ServerURL,
		"GATE_SYSTEM_CODE":        bundle.SystemCode,
		"GATE_SERIAL_NUMBER":      bundle.SerialNumber,
		"GATE_DEVICE_KEY":         bundle.DeviceKey,
		"GATE_MAINTENANCE_SECRET": bundle.MaintenanceSecret,
		"GATE_MAINTENANCE_URL":    bundle.MaintenanceURL,
		"GATE_DRIVER_URL":         "",
		"GATE_SCAN_TOKEN":         randomToken(),
		"GATE_STATE_FILE":         filepath.Join(stateDir, "state.json"),
		"GATE_SCAN_LISTEN":        "127.0.0.1:19300",
	}
	if err := writeEnvFile(configPath, envValues); err != nil {
		fatal(fmt.Errorf("写入 gate-client 配置失败: %w", err))
	}
	if err := confirmFromEnv(serverURL, state, envValues); err != nil {
		fmt.Fprintf(os.Stderr, "配置已安全写入 %s，但安装确认暂未完成：%v\n请保持此文件不变，网络恢复后重新运行安装器并再次输入同一绑定码。\n", configPath, err)
		os.Exit(2)
	}
	if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		fatal(fmt.Errorf("清理临时安装状态失败: %w", err))
	}
	fmt.Fprintf(os.Stdout, "安装绑定完成。配置：%s；真实闸机驱动尚未配置，当前扫码会保持 unknown/fail-closed。\n", configPath)
}

func normalizeServerURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return "", errors.New("请使用 --server-url 提供有效的 HTTPS 云端地址")
	}
	if parsed.Scheme != "https" && os.Getenv("GATE_ALLOW_INSECURE_HTTP") != "true" {
		return "", errors.New("生产安装必须使用 HTTPS；本地调试请显式设置 GATE_ALLOW_INSECURE_HTTP=true")
	}
	return raw, nil
}

func loadOrCreateState(path string) (installState, *ecdh.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var state installState
		if err := json.Unmarshal(data, &state); err != nil {
			return installState{}, nil, fmt.Errorf("解析临时安装状态失败: %w", err)
		}
		privateBytes, err := base64.RawURLEncoding.DecodeString(state.PrivateKey)
		if err != nil {
			return installState{}, nil, errors.New("临时安装私钥格式无效")
		}
		privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
		if err != nil {
			return installState{}, nil, fmt.Errorf("读取临时安装私钥失败: %w", err)
		}
		return state, privateKey, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return installState{}, nil, err
	}
	privateKey, publicBytes, err := deviceprovision.GenerateKeyPair()
	if err != nil {
		return installState{}, nil, err
	}
	state := installState{
		InstallationID: randomID(),
		PrivateKey:     base64.RawURLEncoding.EncodeToString(privateKey.Bytes()),
		PublicKey:      deviceprovision.EncodePublicKey(publicBytes),
		Fingerprint:    deviceprovision.Fingerprint(publicBytes),
	}
	if err := writeState(path, state); err != nil {
		return installState{}, nil, err
	}
	return state, privateKey, nil
}

func ensurePrivatePublicMatch(state installState, privateKey *ecdh.PrivateKey) error {
	if strings.TrimSpace(state.InstallationID) == "" || strings.TrimSpace(state.PublicKey) == "" {
		return errors.New("临时安装状态不完整")
	}
	publicBytes, err := deviceprovision.DecodePublicKey(state.PublicKey)
	fingerprint := deviceprovision.Fingerprint(privateKey.PublicKey().Bytes())
	if err != nil || deviceprovision.Fingerprint(publicBytes) != fingerprint || (state.Fingerprint != "" && state.Fingerprint != fingerprint) {
		return errors.New("临时安装公私钥不匹配")
	}
	return nil
}

func claim(serverURL, token string, state installState) (claimResponse, error) {
	body, _ := json.Marshal(map[string]string{"token": token, "installation_id": state.InstallationID, "public_key": state.PublicKey})
	return postJSON(serverURL+"/api/v1/hardware/provision", body)
}

func confirmFromEnv(serverURL string, state installState, values map[string]string) error {
	state.Fingerprint = strings.TrimSpace(state.Fingerprint)
	if state.Fingerprint == "" {
		return errors.New("临时安装状态缺少指纹")
	}
	body, _ := json.Marshal(map[string]string{"installation_id": state.InstallationID, "fingerprint": state.Fingerprint})
	return signedPost(serverURL+"/api/v1/hardware/provision/confirm", body, values["GATE_SYSTEM_CODE"], values["GATE_SERIAL_NUMBER"], values["GATE_DEVICE_KEY"])
}

func signedPost(rawURL string, body []byte, systemCode, serial, deviceKey string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	requestID, timestamp, nonce := randomID(), fmt.Sprintf("%d", time.Now().Unix()), randomID()
	req, err := http.NewRequest(http.MethodPost, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(deviceauth.HeaderSystemCode, strings.TrimSpace(systemCode))
	req.Header.Set(deviceauth.HeaderSerial, strings.TrimSpace(serial))
	req.Header.Set(deviceauth.HeaderRequestID, requestID)
	req.Header.Set(deviceauth.HeaderTimestamp, timestamp)
	req.Header.Set(deviceauth.HeaderNonce, nonce)
	req.Header.Set(deviceauth.HeaderSignature, deviceauth.Sign(deviceauth.DeriveKey(deviceKey), http.MethodPost, parsed.Path, timestamp, nonce, requestID, body))
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(resp)
	}
	return nil
}

func postJSON(rawURL string, body []byte) (claimResponse, error) {
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Post(rawURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return claimResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return claimResponse{}, responseError(resp)
	}
	var result claimResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return claimResponse{}, err
	}
	if result.Envelope == "" {
		return claimResponse{}, errors.New("云端没有返回安装配置")
	}
	return result, nil
}

func responseError(resp *http.Response) error {
	var failure struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&failure)
	if failure.Error == "" {
		failure.Error = resp.Status
	}
	return fmt.Errorf("云端返回 %s：%s", resp.Status, failure.Error)
}

func validateBundle(bundle deviceprovision.Bundle, requestedServer string) error {
	if strings.TrimSpace(bundle.ServerURL) == "" || strings.TrimSpace(bundle.SystemCode) == "" || strings.TrimSpace(bundle.SerialNumber) == "" || strings.TrimSpace(bundle.DeviceKey) == "" {
		return errors.New("云端安装配置不完整")
	}
	if strings.TrimRight(bundle.ServerURL, "/") != strings.TrimRight(requestedServer, "/") {
		return errors.New("云端返回地址与安装目标不一致")
	}
	return nil
}

func readActivationCode() string {
	fmt.Fprint(os.Stderr, "请输入管理端生成的一次性安装绑定码（不会写入命令行或环境变量）： ")
	value, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(value)
}

func writeState(path string, state installState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0o600)
}

func writeEnvFile(path string, values map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var builder strings.Builder
	for _, key := range []string{"GATE_SERVER_URL", "GATE_SYSTEM_CODE", "GATE_SERIAL_NUMBER", "GATE_DEVICE_KEY", "GATE_MAINTENANCE_SECRET", "GATE_MAINTENANCE_URL", "GATE_DRIVER_URL", "GATE_SCAN_TOKEN", "GATE_STATE_FILE", "GATE_SCAN_LISTEN"} {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(strings.ReplaceAll(strings.ReplaceAll(values[key], "\\", "\\\\"), "\n", ""))
		builder.WriteByte('\n')
	}
	return atomicWrite(path, []byte(builder.String()), 0o600)
}

func readEnvFile(path string) (map[string]string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			values[strings.TrimSpace(parts[0])] = strings.Trim(strings.TrimSpace(parts[1]), "\"")
		}
	}
	return values, true, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(temporary, mode); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func randomToken() string {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func randomID() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("install-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data)
}

func fatal(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
