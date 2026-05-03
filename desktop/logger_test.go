package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestGatewayLoggerWriteAndRead 验证三类日志会写入当天文件并可按最新日志读取。
func TestGatewayLoggerWriteAndRead(t *testing.T) {
	tempDir := t.TempDir()
	logger := &GatewayLogger{dir: tempDir}

	logger.Run("run message")
	logger.Error("error token=secret-value")
	logger.Request("request message")

	day := time.Now().Format(logDateLayout)
	days, err := logger.ListDays()
	if err != nil {
		t.Fatalf("ListDays failed: %v", err)
	}
	if len(days) != 1 || days[0] != day {
		t.Fatalf("expected day %s, got %#v", day, days)
	}

	for _, kind := range []string{logKindRun, logKindError, logKindRequest} {
		entries, err := logger.ReadEntries(day, kind, defaultLogLimit)
		if err != nil {
			t.Fatalf("ReadEntries(%s) failed: %v", kind, err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected one %s entry, got %d", kind, len(entries))
		}
		if entries[0].Timestamp == "" || entries[0].Message == "" {
			t.Fatalf("expected timestamp and message for %s entry: %#v", kind, entries[0])
		}
	}
}

// TestGatewayLoggerReadLimit 验证日志页最多只返回 50 条，避免大日志拖慢界面。
func TestGatewayLoggerReadLimit(t *testing.T) {
	tempDir := t.TempDir()
	logger := &GatewayLogger{dir: tempDir}
	day := time.Now().Format(logDateLayout)
	path := filepath.Join(tempDir, "run-"+day+".log")
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, 120)
	for i := 0; i < 120; i++ {
		lines = append(lines, "2026-05-03 12:00:00 line-"+strconv.Itoa(i))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := logger.ReadEntries(day, logKindRun, defaultLogLimit)
	if err != nil {
		t.Fatalf("ReadEntries failed: %v", err)
	}
	if len(entries) != defaultLogLimit {
		t.Fatalf("expected %d entries, got %d", defaultLogLimit, len(entries))
	}
	if !strings.Contains(entries[0].Message, "line-119") {
		t.Fatalf("expected latest entry first, got %#v", entries[0])
	}
	if !strings.Contains(entries[len(entries)-1].Message, "line-70") {
		t.Fatalf("expected oldest retained entry last, got %#v", entries[len(entries)-1])
	}
}

// TestGatewayLoggerRejectsInvalidInput 验证日志读取参数不会被用作任意文件路径。
func TestGatewayLoggerRejectsInvalidInput(t *testing.T) {
	logger := &GatewayLogger{dir: t.TempDir()}
	if _, err := logger.ReadEntries("../config", logKindRun, defaultLogLimit); err == nil {
		t.Fatal("expected invalid date error")
	}
	if _, err := logger.ReadEntries("2026-05-03", "../run", defaultLogLimit); err == nil {
		t.Fatal("expected invalid kind error")
	}
}
