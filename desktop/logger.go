package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	logKindRun     = "run"
	logKindError   = "error"
	logKindRequest = "request"
	logTimeLayout  = "2006-01-02 15:04:05"
	logDateLayout  = "2006-01-02"
)

// GatewayLogger 按日期和类型写入网关日志，避免运行日志、错误日志和请求日志互相混杂。
type GatewayLogger struct {
	dir string
	mu  sync.Mutex
}

// NewGatewayLogger 创建桌面网关日志器，目录创建失败时仍保留路径并在写入时返回错误。
func NewGatewayLogger() *GatewayLogger {
	return &GatewayLogger{dir: defaultLogDir()}
}

// Directory 返回日志目录，供界面诊断和人工排查使用。
func (l *GatewayLogger) Directory() string {
	if l == nil {
		return ""
	}
	return l.dir
}

// Run 记录网关生命周期和关键状态变化。
func (l *GatewayLogger) Run(message string) {
	l.write(logKindRun, message)
}

// Error 记录脱敏后的错误信息。
func (l *GatewayLogger) Error(message any) {
	l.write(logKindError, redactSecrets(message))
}

// Request 记录 HTTP、WebSocket 和 Codex RPC 的请求摘要，不写入 token 和正文内容。
func (l *GatewayLogger) Request(message string) {
	l.write(logKindRequest, message)
}

// ListDays 返回已经存在日志文件的日期，按最新日期优先排序。
func (l *GatewayLogger) ListDays() ([]string, error) {
	if l == nil {
		return nil, nil
	}
	entries, err := os.ReadDir(l.dir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取日志目录失败: %w", err)
	}
	days := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		kind, day, ok := parseLogFileName(entry.Name())
		if ok && isValidLogKind(kind) {
			days[day] = struct{}{}
		}
	}
	result := make([]string, 0, len(days))
	for day := range days {
		result = append(result, day)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(result)))
	return result, nil
}

// ReadEntries 读取指定日期和类型的前 limit 条日志，limit 小于等于 0 时使用 100 条。
func (l *GatewayLogger) ReadEntries(day string, kind string, limit int) ([]LogEntry, error) {
	if l == nil {
		return nil, nil
	}
	day, err := normalizeLogDay(day)
	if err != nil {
		return nil, err
	}
	if !isValidLogKind(kind) {
		return nil, fmt.Errorf("日志类型无效: %s", kind)
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	file, err := os.Open(l.path(kind, day))
	if os.IsNotExist(err) {
		return []LogEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取日志失败: %w", err)
	}
	defer file.Close()

	entries := make([]LogEntry, 0, limit)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if len(entries) >= limit {
			break
		}
		entries = append(entries, parseLogLine(scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取日志内容失败: %w", err)
	}
	return entries, nil
}

// write 将日志追加到当日文件，写入失败只输出到 stderr，避免日志问题影响网关主流程。
func (l *GatewayLogger) write(kind string, message string) {
	if l == nil || !isValidLogKind(kind) {
		return
	}
	message = strings.TrimSpace(redactSecrets(message))
	if message == "" {
		return
	}
	now := time.Now()
	line := fmt.Sprintf("%s %s\n", now.Format(logTimeLayout), sanitizeLogLine(message))

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "写入日志失败: %v\n", err)
		return
	}
	file, err := os.OpenFile(l.path(kind, now.Format(logDateLayout)), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "写入日志失败: %v\n", err)
		return
	}
	defer file.Close()
	if _, err := file.WriteString(line); err != nil {
		fmt.Fprintf(os.Stderr, "写入日志失败: %v\n", err)
	}
}

// path 根据日志类型和日期生成完整文件路径。
func (l *GatewayLogger) path(kind string, day string) string {
	return filepath.Join(l.dir, fmt.Sprintf("%s-%s.log", kind, day))
}

// defaultLogDir 返回各平台习惯的网关日志目录。
func defaultLogDir() string {
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Logs", configDirectoryName)
		}
	}
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, configDirectoryName, "Logs")
		}
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, configDirectoryName, "logs")
	}
	return filepath.Join(".", "logs")
}

// parseLogFileName 解析 run-YYYY-MM-DD.log 这类文件名。
func parseLogFileName(name string) (string, string, bool) {
	if !strings.HasSuffix(name, ".log") {
		return "", "", false
	}
	base := strings.TrimSuffix(name, ".log")
	parts := strings.SplitN(base, "-", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	day, err := normalizeLogDay(parts[1])
	if err != nil {
		return "", "", false
	}
	return parts[0], day, true
}

// parseLogLine 将单行日志拆成时间和正文，兼容旧格式无法拆分时整体作为正文展示。
func parseLogLine(line string) LogEntry {
	if len(line) >= len(logTimeLayout) {
		prefix := line[:len(logTimeLayout)]
		if _, err := time.ParseInLocation(logTimeLayout, prefix, time.Local); err == nil {
			return LogEntry{Timestamp: prefix, Message: strings.TrimSpace(line[len(logTimeLayout):])}
		}
	}
	return LogEntry{Message: strings.TrimSpace(line)}
}

// normalizeLogDay 校验日志日期，防止通过日期参数读取任意路径。
func normalizeLogDay(day string) (string, error) {
	day = strings.TrimSpace(day)
	parsed, err := time.ParseInLocation(logDateLayout, day, time.Local)
	if err != nil {
		return "", fmt.Errorf("日志日期无效: %s", day)
	}
	return parsed.Format(logDateLayout), nil
}

// isValidLogKind 限制日志类型只能读取和写入约定的三类文件。
func isValidLogKind(kind string) bool {
	return kind == logKindRun || kind == logKindError || kind == logKindRequest
}

// sanitizeLogLine 把多行内容压成单行，避免单条日志破坏列表展示。
func sanitizeLogLine(message string) string {
	message = strings.ReplaceAll(message, "\r", "\\r")
	message = strings.ReplaceAll(message, "\n", "\\n")
	return message
}
