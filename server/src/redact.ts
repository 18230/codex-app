const SECRET_PATTERNS = [
  /(token=)[^&\s]+/gi,
  /(authorization:\s*bearer\s+)[^\s]+/gi,
  /((?:api[_-]?key|secret|password|token)["']?\s*[:=]\s*["']?)[^"',\s]+/gi,
];

/**
 * 对日志文本做最小脱敏，防止 token 或常见密钥字段进入终端输出。
 */
export function redactSecrets(input: unknown): string {
  let text = typeof input === "string" ? input : JSON.stringify(input);
  for (const pattern of SECRET_PATTERNS) {
    text = text.replace(pattern, "$1[REDACTED]");
  }
  return text;
}

