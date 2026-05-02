import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import WebSocket from "ws";
import { redactSecrets } from "./redact.js";
import type { GatewayConfig } from "./config.js";
import type { JsonObject, JsonValue } from "./types.js";

type PendingRequest = {
  resolve: (value: JsonValue) => void;
  reject: (error: Error) => void;
  timer: NodeJS.Timeout;
};

type CodexNotificationHandler = (message: JsonObject) => void;

/**
 * 裁剪子进程日志，避免远端 HTML 错误页刷满网关输出。
 */
function compactLog(input: unknown): string {
  const text = redactSecrets(input).trim();
  return text.length > 1_200 ? `${text.slice(0, 1_200)}... [truncated]` : text;
}

/**
 * 把 app-server 的 stderr 归一化为适合手机端展示的错误，避免只看到泛化失败。
 */
function readableProcessError(text: string): string | null {
  const lowerText = text.toLowerCase();
  const isUnauthorized = lowerText.includes("401 unauthorized");
  const isCodexResponsesSocket = lowerText.includes("codex/responses") || lowerText.includes("failed to connect to websocket");
  if (isUnauthorized && isCodexResponsesSocket) {
    return "Codex 登录态失效：后台 app-server 连接 ChatGPT Codex 后端返回 401 Unauthorized。请先在电脑端确认 Codex 已登录，再重启手机网关。";
  }

  return null;
}

/**
 * 管理 Codex app-server 子进程，并提供 JSON-RPC 请求封装。
 */
export class CodexClient {
  private child: ChildProcessWithoutNullStreams | null = null;
  private socket: WebSocket | null = null;
  private nextId = 1;
  private stopping = false;
  private readonly pending = new Map<number, PendingRequest>();
  private readonly notificationHandlers = new Set<CodexNotificationHandler>();

  public constructor(private readonly config: GatewayConfig) {}

  /**
   * 启动内部 app-server 并完成 initialize 握手。
   */
  public async start(): Promise<void> {
    this.stopping = false;
    const child = spawn(
      this.config.codexBinary,
      ["app-server", "--listen", `ws://${this.config.codexHost}:${this.config.codexPort}`],
      {
        stdio: ["pipe", "pipe", "pipe"],
        env: process.env,
      },
    );
    this.child = child;

    child.stdout.on("data", (chunk) => {
      const text = compactLog(chunk.toString());
      if (text) console.log(`[codex] ${text}`);
    });

    child.stderr.on("data", (chunk) => {
      const text = compactLog(chunk.toString());
      if (text) {
        console.warn(`[codex] ${text}`);
        const readableError = readableProcessError(text);
        if (readableError) {
          this.emitNotification({
            method: "gateway/error",
            params: {
              message: readableError,
              source: "codex-app-server",
            },
          });
        }
      }
    });

    child.on("exit", (code, signal) => {
      const error = new Error(`Codex app-server 已退出 code=${code ?? "null"} signal=${signal ?? "null"}`);
      this.rejectAll(error);
      if (!this.stopping) this.fatal(error);
    });

    await this.connectWithRetry();
    await this.request("initialize", {
      clientInfo: {
        name: "codex-mobile-gateway",
        title: "Codex Mobile Gateway",
        version: "0.1.0",
      },
      capabilities: {
        experimentalApi: true,
        optOutNotificationMethods: null,
      },
    });
    this.notify("initialized", undefined);
  }

  /**
   * 关闭 WebSocket 与 app-server 子进程。
   */
  public async stop(): Promise<void> {
    this.stopping = true;
    this.socket?.close();
    this.socket = null;
    this.child?.kill("SIGTERM");
    this.child = null;
    this.rejectAll(new Error("CodexClient 已关闭"));
  }

  /**
   * 注册 Codex 通知监听器，用于把实时输出广播到手机端。
   */
  public onNotification(handler: CodexNotificationHandler): () => void {
    this.notificationHandlers.add(handler);
    return () => this.notificationHandlers.delete(handler);
  }

  /**
   * 返回 app-server WebSocket 是否处于可发送状态，用于手机端健康检查。
   */
  public isConnected(): boolean {
    return this.socket?.readyState === WebSocket.OPEN;
  }

  /**
   * 向外层广播本地归一化事件，与 app-server notification 使用同一条处理链路。
   */
  private emitNotification(message: JsonObject): void {
    for (const handler of this.notificationHandlers) handler(message);
  }

  /**
   * 发送 JSON-RPC 请求并等待响应。
   */
  public request(method: string, params: JsonValue, timeoutMs = 120_000): Promise<JsonValue> {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error("Codex app-server 尚未连接"));
    }

    const id = this.nextId++;
    const payload = { id, method, params };
    this.socket.send(JSON.stringify(payload));

    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`Codex 请求超时: ${method}`));
      }, timeoutMs);

      this.pending.set(id, { resolve, reject, timer });
    });
  }

  /**
   * 发送 JSON-RPC notification，不等待响应。
   */
  private notify(method: string, params: JsonValue | undefined): void {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
      throw new Error("Codex app-server 尚未连接");
    }
    this.socket.send(JSON.stringify(params === undefined ? { method } : { method, params }));
  }

  /**
   * 带重试连接刚启动的 app-server。
   */
  private async connectWithRetry(): Promise<void> {
    const url = `ws://${this.config.codexHost}:${this.config.codexPort}`;
    let lastError: Error | null = null;

    for (let attempt = 0; attempt < 40; attempt += 1) {
      try {
        await this.connect(url);
        return;
      } catch (error) {
        lastError = error instanceof Error ? error : new Error(String(error));
        await new Promise((resolve) => setTimeout(resolve, 250));
      }
    }

    throw lastError ?? new Error("无法连接 Codex app-server");
  }

  /**
   * 建立 WebSocket 连接并绑定消息处理。
   */
  private connect(url: string): Promise<void> {
    return new Promise((resolve, reject) => {
      const socket = new WebSocket(url);
      const timer = setTimeout(() => {
        socket.close();
        reject(new Error(`连接 Codex app-server 超时: ${url}`));
      }, 2_000);

      socket.once("open", () => {
        clearTimeout(timer);
        this.socket = socket;
        socket.on("message", (data) => this.handleMessage(data.toString()));
        socket.on("close", () => {
          const error = new Error("Codex app-server 连接已关闭");
          this.rejectAll(error);
          if (!this.stopping) this.fatal(error);
        });
        socket.on("error", (error) => console.warn(`[codex] ${redactSecrets(error.message)}`));
        resolve();
      });

      socket.once("error", (error) => {
        clearTimeout(timer);
        reject(error);
      });
    });
  }

  /**
   * 处理 Codex JSON-RPC 响应、通知和服务端请求。
   */
  private handleMessage(raw: string): void {
    let message: JsonObject;
    try {
      message = JSON.parse(raw) as JsonObject;
    } catch {
      console.warn(`[codex] 收到无法解析的消息: ${redactSecrets(raw)}`);
      return;
    }

    const id = typeof message.id === "number" ? message.id : null;
    if (id !== null && this.pending.has(id)) {
      const pending = this.pending.get(id)!;
      this.pending.delete(id);
      clearTimeout(pending.timer);
      if ("error" in message) {
        pending.reject(new Error(redactSecrets(message.error)));
      } else {
        pending.resolve(message.result ?? null);
      }
      return;
    }

    if (typeof message.method === "string" && "id" in message) {
      this.answerServerRequest(message).catch((error) => {
        console.warn(`[codex] 自动响应服务端请求失败: ${redactSecrets(error.message)}`);
      });
      return;
    }

    if (typeof message.method === "string") {
      this.emitNotification(message);
    }
  }

  /**
   * 在自动执行模式下处理 Codex 发来的审批类请求，避免 turn 被挂起。
   */
  private async answerServerRequest(message: JsonObject): Promise<void> {
    const id = message.id;
    const method = message.method;
    let result: JsonValue | null = null;

    if (method === "item/commandExecution/requestApproval") {
      result = { decision: "acceptForSession" };
    } else if (method === "item/fileChange/requestApproval") {
      result = { decision: "acceptForSession" };
    } else if (method === "execCommandApproval" || method === "applyPatchApproval") {
      result = { decision: "approved_for_session" };
    } else if (method === "item/permissions/requestApproval") {
      result = { decision: "acceptForSession" };
    }

    if (result !== null && this.socket?.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify({ id, result }));
    }
  }

  /**
   * 拒绝所有等待中的请求，通常用于连接断开或进程退出。
   */
  private rejectAll(error: Error): void {
    for (const [id, pending] of this.pending.entries()) {
      clearTimeout(pending.timer);
      pending.reject(error);
      this.pending.delete(id);
    }
  }

  /**
   * app-server 异常退出时让外层守护进程重启整个网关，避免进入半可用状态。
   */
  private fatal(error: Error): void {
    console.error(`[codex] ${redactSecrets(error.message)}`);
    setTimeout(() => process.exit(1), 100);
  }
}
