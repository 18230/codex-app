package main

import (
	"fmt"
	"regexp"
	"strings"
)

var tokenLikePattern = regexp.MustCompile(`(?i)(token=)[^&\s]+`)

// redactSecrets 清理日志里的 token，避免桌面日志或错误消息泄露远程控制凭据。
func redactSecrets(input any) string {
	text := strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(toString(input)), "\x00", ""))
	text = tokenLikePattern.ReplaceAllString(text, "${1}<redacted>")
	return text
}

// toString 把错误和普通值统一转换为字符串。
func toString(input any) string {
	switch value := input.(type) {
	case nil:
		return ""
	case error:
		return value.Error()
	case string:
		return value
	default:
		return fmt.Sprint(value)
	}
}
