import path from "node:path";

export type GatewayConfig = {
  host: string;
  port: number;
  codexHost: string;
  codexPort: number;
  codexBinary: string;
  token: string;
  threadId: string | null;
  defaultCwd: string;
  clientPingIntervalMs: number;
  clientIdleTimeoutMs: number;
};

/**
 * 读取正整数环境变量，避免错误配置导致心跳或端口行为异常。
 */
function readPositiveIntegerEnv(name: string, fallback: number): number {
  const raw = process.env[name]?.trim();
  if (!raw) return fallback;
  const parsed = Number(raw);
  if (!Number.isInteger(parsed) || parsed <= 0) {
    throw new Error(`${name} 必须是正整数`);
  }
  return parsed;
}

/**
 * 读取并校验网关启动配置，避免服务在半配置状态下暴露到隧道。
 */
export function loadConfig(): GatewayConfig {
  const token = process.env.CODEX_MOBILE_TOKEN?.trim();
  const threadId = process.env.CODEX_THREAD_ID?.trim();

  if (!token || token.length < 16) {
    throw new Error("CODEX_MOBILE_TOKEN 必须设置，且长度至少 16 位");
  }

  const inferredCwd =
    path.basename(process.cwd()) === "server" ? path.dirname(process.cwd()) : process.cwd();

  return {
    host: process.env.CODEX_MOBILE_HOST || "127.0.0.1",
    port: Number(process.env.CODEX_MOBILE_PORT || 8000),
    codexHost: process.env.CODEX_APP_SERVER_HOST || "127.0.0.1",
    codexPort: Number(process.env.CODEX_APP_SERVER_PORT || 39000),
    codexBinary: process.env.CODEX_BINARY || "codex",
    token,
    threadId: threadId || null,
    defaultCwd: path.resolve(process.env.CODEX_MOBILE_DEFAULT_CWD || inferredCwd),
    clientPingIntervalMs: readPositiveIntegerEnv("CODEX_MOBILE_CLIENT_PING_INTERVAL_MS", 15_000),
    clientIdleTimeoutMs: readPositiveIntegerEnv("CODEX_MOBILE_CLIENT_IDLE_TIMEOUT_MS", 45_000),
  };
}
