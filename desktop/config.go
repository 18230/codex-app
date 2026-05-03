package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	defaultHost             = "127.0.0.1"
	defaultPort             = 8000
	defaultCodexHost        = "127.0.0.1"
	defaultCodexPort        = 39000
	defaultPingIntervalMs   = 15000
	defaultIdleTimeoutMs    = 45000
	configDirectoryName     = "CodexMobileGateway"
	configFileName          = "config.json"
	defaultCodexBinaryValue = "codex"
)

// AppConfig 是桌面网关持久化配置，字段保持简单以方便迁移和手动排查。
type AppConfig struct {
	Workspace             string `json:"workspace"`
	Token                 string `json:"token"`
	CodexBinary           string `json:"codexBinary"`
	Host                  string `json:"host"`
	Port                  int    `json:"port"`
	CodexHost             string `json:"codexHost"`
	CodexPort             int    `json:"codexPort"`
	BoundThreadID         string `json:"boundThreadId"`
	ClientPingIntervalMs  int    `json:"clientPingIntervalMs"`
	ClientIdleTimeoutMs   int    `json:"clientIdleTimeoutMs"`
	LastConnectionBaseURL string `json:"lastConnectionBaseUrl"`
}

// AppSnapshot 是前端初始化需要的一次性状态。
type AppSnapshot struct {
	Config AppConfig     `json:"config"`
	Status GatewayStatus `json:"status"`
}

// ConfigStore 管理跨平台配置文件路径和读写。
type ConfigStore struct {
	path string
}

// NewConfigStore 创建配置存储实例。
func NewConfigStore() *ConfigStore {
	return &ConfigStore{path: configPath()}
}

// Path 返回配置文件路径，便于日志和诊断展示。
func (s *ConfigStore) Path() string {
	return s.path
}

// Ensure 确保配置文件存在。
func (s *ConfigStore) Ensure() error {
	if _, err := os.Stat(s.path); err == nil {
		return nil
	}
	cfg, err := DefaultConfig()
	if err != nil {
		return err
	}
	return s.Save(cfg)
}

// Load 读取配置文件，不存在时返回默认配置。
func (s *ConfigStore) Load() (AppConfig, error) {
	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		return DefaultConfig()
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return AppConfig{}, fmt.Errorf("读取配置失败: %w", err)
	}
	var cfg AppConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return AppConfig{}, fmt.Errorf("解析配置失败: %w", err)
	}
	return NormalizeConfig(cfg)
}

// Save 校验并写入配置文件。
func (s *ConfigStore) Save(cfg AppConfig) error {
	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	raw, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	return os.WriteFile(s.path, append(raw, '\n'), 0o600)
}

// DefaultConfig 生成首次启动配置。
func DefaultConfig() (AppConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return AppConfig{}, fmt.Errorf("读取用户目录失败: %w", err)
	}
	token, err := randomToken()
	if err != nil {
		return AppConfig{}, err
	}
	return NormalizeConfig(AppConfig{
		Workspace:             home,
		Token:                 token,
		CodexBinary:           defaultCodexBinary(),
		Host:                  defaultHost,
		Port:                  defaultPort,
		CodexHost:             defaultCodexHost,
		CodexPort:             defaultCodexPort,
		ClientPingIntervalMs:  defaultPingIntervalMs,
		ClientIdleTimeoutMs:   defaultIdleTimeoutMs,
		LastConnectionBaseURL: "https://xxx.com",
	})
}

// NormalizeConfig 补齐默认值并做边界校验。
func NormalizeConfig(cfg AppConfig) (AppConfig, error) {
	cfg.Workspace = strings.TrimSpace(cfg.Workspace)
	if cfg.Workspace == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return AppConfig{}, fmt.Errorf("读取用户目录失败: %w", err)
		}
		cfg.Workspace = home
	}
	workspace, err := validateWorkspacePath(cfg.Workspace)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.Workspace = workspace

	cfg.Token = strings.TrimSpace(cfg.Token)
	if len(cfg.Token) < 16 {
		return AppConfig{}, fmt.Errorf("token 至少需要 16 位")
	}
	cfg.CodexBinary = strings.TrimSpace(cfg.CodexBinary)
	if cfg.CodexBinary == "" {
		cfg.CodexBinary = defaultCodexBinary()
	} else if cfg.CodexBinary == defaultCodexBinaryValue {
		cfg.CodexBinary = defaultCodexBinary()
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	if cfg.Host == "" {
		cfg.Host = defaultHost
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		cfg.Port = defaultPort
	}
	cfg.CodexHost = strings.TrimSpace(cfg.CodexHost)
	if cfg.CodexHost == "" {
		cfg.CodexHost = defaultCodexHost
	}
	if cfg.CodexPort <= 0 || cfg.CodexPort > 65535 {
		cfg.CodexPort = defaultCodexPort
	}
	cfg.BoundThreadID = strings.TrimSpace(cfg.BoundThreadID)
	cfg.ClientPingIntervalMs = defaultPingIntervalMs
	cfg.ClientIdleTimeoutMs = defaultIdleTimeoutMs
	cfg.LastConnectionBaseURL = strings.TrimSpace(cfg.LastConnectionBaseURL)
	if cfg.LastConnectionBaseURL == "" {
		cfg.LastConnectionBaseURL = "https://xxx.com"
	}
	return cfg, nil
}

// ValidateConfigTargets 确认启动和保存依赖的本地路径都真实可用。
func ValidateConfigTargets(cfg AppConfig) (AppConfig, error) {
	workspace, err := validateWorkspacePath(cfg.Workspace)
	if err != nil {
		return AppConfig{}, err
	}
	cfg.Workspace = workspace
	resolvedCodex, err := ResolveCodexBinary(cfg.CodexBinary)
	if err != nil {
		return AppConfig{}, fmt.Errorf("Codex 可执行文件不可用: %w", err)
	}
	cfg.CodexBinary = resolvedCodex
	return cfg, nil
}

// defaultCodexBinary 返回当前机器可用的 Codex 可执行文件路径，避免 GUI 应用拿不到终端 PATH。
func defaultCodexBinary() string {
	if resolved, err := ResolveCodexBinary(defaultCodexBinaryValue); err == nil {
		return resolved
	}
	return defaultCodexBinaryValue
}

// ResolveCodexBinary 尽量解析 Codex 可执行文件真实路径，兼容 macOS GUI 环境 PATH 缺失。
func ResolveCodexBinary(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		input = defaultCodexBinaryValue
	}
	if filepath.IsAbs(input) || strings.ContainsAny(input, `/\`) {
		if err := validateExecutable(input); err != nil {
			return "", err
		}
		return input, nil
	}
	if path, err := exec.LookPath(input); err == nil {
		return path, nil
	}
	for _, candidate := range codexBinaryCandidates(input) {
		if err := validateExecutable(candidate); err == nil {
			return candidate, nil
		}
	}
	if path, err := resolveFromLoginShell(input); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("未找到 Codex 可执行文件: %s", input)
}

// codexBinaryCandidates 返回 macOS 常见的 Codex 安装位置。
func codexBinaryCandidates(name string) []string {
	home, _ := os.UserHomeDir()
	return []string{
		"/Applications/Codex.app/Contents/Resources/codex",
		"/opt/homebrew/bin/" + name,
		"/usr/local/bin/" + name,
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(home, "go", "bin", name),
	}
}

// resolveFromLoginShell 通过用户登录 shell 获取 PATH，解决 macOS 双击启动应用时 PATH 不完整的问题。
func resolveFromLoginShell(name string) (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	output, err := exec.Command(shell, "-lc", "command -v "+shellQuote(name)).Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", fmt.Errorf("登录 shell 未返回 Codex 路径")
	}
	if err := validateExecutable(path); err != nil {
		return "", err
	}
	return path, nil
}

// validateExecutable 确认路径存在、不是目录，并且当前用户可执行。
func validateExecutable(path string) error {
	stat, err := os.Stat(path)
	if err != nil {
		return err
	}
	if stat.IsDir() {
		return fmt.Errorf("Codex 可执行文件不能是目录: %s", path)
	}
	if stat.Mode()&0o111 == 0 {
		return fmt.Errorf("Codex 文件不可执行: %s", path)
	}
	return nil
}

// shellQuote 对登录 shell 命令参数做最小安全转义。
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// validateWorkspacePath 校验工作目录必须存在且是目录。
func validateWorkspacePath(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("工作目录不能为空")
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("工作目录路径无效: %w", err)
	}
	stat, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("工作目录不存在: %s", abs)
	}
	if !stat.IsDir() {
		return "", fmt.Errorf("工作目录不是目录: %s", abs)
	}
	return abs, nil
}

// configPath 返回符合平台习惯的配置路径。
func configPath() string {
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", configDirectoryName, configFileName)
		}
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, configDirectoryName, configFileName)
	}
	return filepath.Join(".", configFileName)
}

// randomToken 生成默认鉴权 token。
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成 token 失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
