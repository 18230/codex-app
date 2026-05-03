package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGatewayLoggerWriteAndRead 验证三类日志会写入当天文件并可按前 100 条读取。
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
		entries, err := logger.ReadEntries(day, kind, 100)
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

// TestGatewayLoggerReadLimit 验证日志页最多只返回 100 条，避免大日志拖慢界面。
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
		lines = append(lines, "2026-05-03 12:00:00 line")
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := logger.ReadEntries(day, logKindRun, 100)
	if err != nil {
		t.Fatalf("ReadEntries failed: %v", err)
	}
	if len(entries) != 100 {
		t.Fatalf("expected 100 entries, got %d", len(entries))
	}
}

// TestGatewayLoggerRejectsInvalidInput 验证日志读取参数不会被用作任意文件路径。
func TestGatewayLoggerRejectsInvalidInput(t *testing.T) {
	logger := &GatewayLogger{dir: t.TempDir()}
	if _, err := logger.ReadEntries("../config", logKindRun, 100); err == nil {
		t.Fatal("expected invalid date error")
	}
	if _, err := logger.ReadEntries("2026-05-03", "../run", 100); err == nil {
		t.Fatal("expected invalid kind error")
	}
}
