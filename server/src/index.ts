import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { WebSocketServer, type WebSocket } from "ws";
import { CodexClient } from "./codexClient.js";
import { loadConfig } from "./config.js";
import { validateWorkspacePath } from "./paths.js";
import { redactSecrets } from "./redact.js";
import { TEST_PAGE } from "./testPage.js";
import type { GatewayClientMessage, GatewayServerMessage, HistoryLine, JsonObject, JsonValue } from "./types.js";

const config = loadConfig();
const codex = new CodexClient(config);
const clients = new Set<WebSocket>();
const clientStates = new WeakMap<WebSocket, { lastSeenAt: number }>();
let currentThreadId = config.threadId ?? "";
let currentThreadCwd: string | null = config.defaultCwd;
let activeTurnId: string | null = null;
const streamedItemKeys = new Set<string>();

/**
 * 使用定长哈希比较 token，避免普通字符串比较泄露时序差异。
 */
function isValidToken(candidate: string | null): boolean {
  if (!candidate) return false;
  const expected = crypto.createHash("sha256").update(config.token).digest();
  const actual = crypto.createHash("sha256").update(candidate).digest();
  return crypto.timingSafeEqual(expected, actual);
}

/**
 * 把服务端消息安全发送给指定手机端连接。
 */
function send(socket: WebSocket, message: GatewayServerMessage): void {
  if (socket.readyState === socket.OPEN) {
    socket.send(JSON.stringify(message));
  }
}

/**
 * 向所有已连接客户端广播 Codex 的实时通知。
 */
function broadcast(message: GatewayServerMessage): void {
  for (const client of clients) send(client, message);
}

/**
 * 记录客户端最近活跃时间，用于长连接心跳和失联清理。
 */
function markClientAlive(socket: WebSocket): void {
  clientStates.set(socket, { lastSeenAt: Date.now() });
}

/**
 * 汇总网关和 Codex app-server 的健康状态，供手机端诊断页展示。
 */
function healthReport(): JsonObject {
  return {
    ok: codex.isConnected(),
    gateway: "ok",
    appServer: codex.isConnected() ? "connected" : "disconnected",
    threadId: currentThreadId,
    defaultThreadId: config.threadId ?? currentThreadId,
    cwd: currentThreadCwd,
    activeTurnId,
    timestamp: Date.now(),
  };
}

/**
 * 解析手机端 JSON 消息，并统一处理解析错误。
 */
function parseClientMessage(raw: string): GatewayClientMessage {
  const parsed = JSON.parse(raw) as Partial<GatewayClientMessage>;
  if (!parsed || typeof parsed !== "object" || typeof parsed.type !== "string") {
    throw new Error("消息缺少 type 字段");
  }
  return parsed as GatewayClientMessage;
}

/**
 * 从 Codex 响应里取出 thread 对象，兼容 alpha 协议中的宽松 JSON 形态。
 */
function readThread(result: JsonValue): JsonObject | null {
  if (!result || typeof result !== "object" || Array.isArray(result)) return null;
  const thread = (result as JsonObject).thread;
  if (!thread || typeof thread !== "object" || Array.isArray(thread)) return null;
  return thread as JsonObject;
}

/**
 * 从 Codex 响应里取出 turn 对象。
 */
function readTurn(result: JsonValue): JsonObject | null {
  if (!result || typeof result !== "object" || Array.isArray(result)) return null;
  const turn = (result as JsonObject).turn;
  if (!turn || typeof turn !== "object" || Array.isArray(turn)) return null;
  return turn as JsonObject;
}

/**
 * 组装 Codex 文本输入，保持和 app-server 协议一致。
 */
function textInput(text: string): JsonValue[] {
  return [{ type: "text", text, text_elements: [] }];
}

/**
 * 为流式 item 生成稳定 key，用于避免完成事件再次发送完整文本造成重复。
 */
function itemKey(turnId: unknown, itemId: unknown): string {
  return `${String(turnId ?? "")}:${String(itemId ?? "")}`;
}

/**
 * 读取对象里的字符串字段，避免宽松 JSON 形态造成 undefined/null 泄露到界面。
 */
function stringField(source: JsonObject, key: string): string {
  const value = source[key];
  return typeof value === "string" ? value : "";
}

/**
 * 转义 SQLite 字符串字面量，避免内部索引修补时破坏 SQL 语句。
 */
function sqlString(value: string): string {
  return `'${value.replaceAll("'", "''")}'`;
}

/**
 * 让手机端新建的空线程进入 Codex 本地线程列表，但不写入假消息。
 */
function markThreadVisible(threadId: string): void {
  if (!/^[0-9a-f-]{36}$/i.test(threadId)) return;
  const stateDb = path.join(process.env.CODEX_HOME || path.join(os.homedir(), ".codex"), "state_5.sqlite");
  if (!fs.existsSync(stateDb)) return;
  const nowMs = Date.now();
  const nowSeconds = Math.floor(nowMs / 1000);
  const sql = [
    "UPDATE threads SET",
    "has_user_event = 1,",
    `title = CASE WHEN title = '' THEN ${sqlString("新会话")} ELSE title END,`,
    `updated_at = ${nowSeconds},`,
    `updated_at_ms = ${nowMs}`,
    `WHERE id = ${sqlString(threadId)}`,
  ].join(" ");

  try {
    execFileSync("sqlite3", [stateDb, sql], { stdio: "ignore" });
  } catch (error) {
    console.warn(`更新 Codex 线程索引失败: ${redactSecrets(error instanceof Error ? error.message : error)}`);
  }
}

/**
 * 从 Codex 本地索引补充空线程，保证手机端新建会话立即出现在列表中。
 */
function localThreadSummaries(cwd: string, limit: number): JsonValue[] {
  const stateDb = path.join(process.env.CODEX_HOME || path.join(os.homedir(), ".codex"), "state_5.sqlite");
  if (!fs.existsSync(stateDb)) return [];
  const sql = [
    "SELECT",
    "id,",
    "title AS name,",
    "CASE WHEN first_user_message != '' THEN first_user_message ELSE title END AS preview,",
    "cwd,",
    "updated_at AS updatedAt",
    "FROM threads",
    `WHERE cwd = ${sqlString(cwd)} AND archived = 0`,
    "ORDER BY updated_at DESC, updated_at_ms DESC",
    `LIMIT ${limit}`,
  ].join(" ");

  try {
    const output = execFileSync("sqlite3", ["-json", stateDb, sql], { encoding: "utf8" });
    const parsed = JSON.parse(output || "[]");
    return Array.isArray(parsed) ? parsed as JsonValue[] : [];
  } catch (error) {
    console.warn(`读取 Codex 本地线程索引失败: ${redactSecrets(error instanceof Error ? error.message : error)}`);
    return [];
  }
}

/**
 * 读取 app-server 线程列表，并合并本地索引中尚未产生首条消息的空线程。
 */
async function listThreads(cwd: string): Promise<{ result: JsonValue; threads: JsonValue[] }> {
  const result = await codex.request("thread/list", {
    cwd,
    limit: 25,
    archived: false,
    sortKey: "updated_at",
    sortDirection: "desc",
    useStateDbOnly: false,
  });
  const remoteThreads = result && typeof result === "object" && !Array.isArray(result) && Array.isArray((result as JsonObject).data)
    ? ((result as JsonObject).data as JsonValue[])
    : [];
  const seen = new Set(
    remoteThreads
      .map((thread) => thread && typeof thread === "object" && !Array.isArray(thread) ? stringField(thread as JsonObject, "id") : "")
      .filter(Boolean),
  );
  const merged = [...remoteThreads];
  for (const thread of localThreadSummaries(cwd, 25)) {
    if (!thread || typeof thread !== "object" || Array.isArray(thread)) continue;
    const id = stringField(thread as JsonObject, "id");
    if (!id || seen.has(id)) continue;
    seen.add(id);
    merged.push(thread);
  }
  merged.sort((left, right) => {
    const leftUpdated = left && typeof left === "object" && !Array.isArray(left) && typeof (left as JsonObject).updatedAt === "number"
      ? (left as JsonObject).updatedAt as number
      : 0;
    const rightUpdated = right && typeof right === "object" && !Array.isArray(right) && typeof (right as JsonObject).updatedAt === "number"
      ? (right as JsonObject).updatedAt as number
      : 0;
    return rightUpdated - leftUpdated;
  });
  return { result, threads: merged.slice(0, 25) };
}

/**
 * 等待 Codex 完成本地索引落盘，避免马上修补后又被异步写回覆盖。
 */
function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * 从用户输入片段中提取可读文本，非文本输入用占位说明，避免历史渲染空白。
 */
function userInputText(input: JsonValue): string {
  if (!input || typeof input !== "object" || Array.isArray(input)) return "";
  const item = input as JsonObject;
  if (item.type === "text") return stringField(item, "text");
  if (item.type === "image") return "[图片]";
  if (item.type === "localImage") return `[本地图片] ${stringField(item, "path")}`;
  if (item.type === "skill") return `[Skill] ${stringField(item, "name")}`;
  if (item.type === "mention") return `[Mention] ${stringField(item, "name")}`;
  return "";
}

/**
 * 把 thread/read 返回的 turns/items 压平成手机端历史消息列表。
 */
function historyLinesFromThread(thread: JsonObject | null): HistoryLine[] {
  const turns = Array.isArray(thread?.turns) ? thread.turns : [];
  const lines: HistoryLine[] = [];

  for (const turnValue of turns) {
    if (!turnValue || typeof turnValue !== "object" || Array.isArray(turnValue)) continue;
    const turn = turnValue as JsonObject;
    const turnId = stringField(turn, "id");
    const items = Array.isArray(turn.items) ? turn.items : [];

    for (const itemValue of items) {
      if (!itemValue || typeof itemValue !== "object" || Array.isArray(itemValue)) continue;
      const item = itemValue as JsonObject;
      const itemId = stringField(item, "id") || `${turnId}:${lines.length}`;

      if (item.type === "userMessage") {
        const content = Array.isArray(item.content) ? item.content : [];
        const text = content.map(userInputText).filter(Boolean).join("\n").trim();
        if (text) lines.push({ kind: "user", text, itemId });
      } else if (item.type === "agentMessage") {
        const text = stringField(item, "text").trim();
        if (text) lines.push({ kind: "assistant", text, itemId });
      }
    }
  }

  return lines;
}

/**
 * 找到指定 thread 对应的 PC 本地 session jsonl 文件。
 */
function findSessionFile(threadId: string): string | null {
  const sessionsDir = path.join(process.env.CODEX_HOME || path.join(os.homedir(), ".codex"), "sessions");
  if (!fs.existsSync(sessionsDir)) return null;
  let matchedFile: string | null = null;
  let matchedMtimeMs = -1;

  const visit = (directory: string) => {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const fullPath = path.join(directory, entry.name);
      if (entry.isDirectory()) {
        visit(fullPath);
      } else if (entry.isFile() && entry.name.endsWith(".jsonl") && entry.name.includes(threadId)) {
        const stat = fs.statSync(fullPath);
        if (stat.mtimeMs > matchedMtimeMs) {
          matchedFile = fullPath;
          matchedMtimeMs = stat.mtimeMs;
        }
      }
    }
  };

  visit(sessionsDir);
  return matchedFile;
}

/**
 * 从 PC 本地 session jsonl 中只提取用户消息和助手消息，过滤工具、命令和内部 JSON。
 */
function historyLinesFromSessionFile(threadId: string): HistoryLine[] {
  const file = findSessionFile(threadId);
  if (!file) return [];
  const lines: HistoryLine[] = [];

  for (const rawLine of fs.readFileSync(file, "utf8").split(/\r?\n/)) {
    if (!rawLine.trim()) continue;
    let record: JsonObject;
    try {
      record = JSON.parse(rawLine) as JsonObject;
    } catch {
      continue;
    }

    if (record.type !== "event_msg") continue;
    const payload = record.payload;
    if (!payload || typeof payload !== "object" || Array.isArray(payload)) continue;
    const event = payload as JsonObject;
    const message = stringField(event, "message").trim();
    if (!message) continue;

    if (event.type === "user_message") {
      lines.push({ kind: "user", text: message, itemId: `session-${lines.length}` });
    } else if (event.type === "agent_message") {
      lines.push({ kind: "assistant", text: message, itemId: `session-${lines.length}` });
    }
  }

  return lines;
}

/**
 * 读取当前线程完整历史并推送给指定手机端连接。
 */
async function sendThreadHistory(socket: WebSocket, threadId: string): Promise<JsonValue> {
  const sessionLines = historyLinesFromSessionFile(threadId);
  if (sessionLines.length > 0) {
    send(socket, { type: "history", threadId, lines: sessionLines });
    return { thread: { id: threadId, turns: [] } };
  }

  const result = await codex.request("thread/read", { threadId, includeTurns: true });
  const thread = readThread(result);
  send(socket, { type: "history", threadId, lines: historyLinesFromThread(thread) });
  return result;
}

/**
 * 处理 Codex 通知并映射为手机端事件。
 */
function handleCodexNotification(message: JsonObject): void {
  const method = message.method;
  const params = message.params;
  if (!params || typeof params !== "object" || Array.isArray(params)) return;
  const data = params as JsonObject;
  const threadId = typeof data.threadId === "string" ? data.threadId : null;
  if (threadId && threadId !== currentThreadId) return;

  if (method === "turn/started") {
    const turn = data.turn as JsonObject | undefined;
    const turnId = typeof turn?.id === "string" ? turn.id : "";
    activeTurnId = turnId || activeTurnId;
    broadcast({ type: "turn.started", threadId: threadId ?? currentThreadId, turnId });
  } else if (method === "turn/completed") {
    activeTurnId = null;
    streamedItemKeys.clear();
    broadcast({ type: "turn.completed", threadId: threadId ?? currentThreadId, turn: data.turn ?? null });
  } else if (method === "item/agentMessage/delta") {
    streamedItemKeys.add(itemKey(data.turnId, data.itemId));
    broadcast({
      type: "delta",
      threadId: String(data.threadId),
      turnId: String(data.turnId),
      itemId: String(data.itemId),
      text: String(data.delta ?? ""),
    });
  } else if (method === "item/commandExecution/outputDelta") {
    streamedItemKeys.add(itemKey(data.turnId, data.itemId));
    broadcast({
      type: "command.delta",
      threadId: String(data.threadId),
      turnId: String(data.turnId),
      itemId: String(data.itemId),
      text: String(data.delta ?? ""),
    });
  } else if (method === "command/exec/outputDelta") {
    const deltaBase64 = stringField(data, "deltaBase64");
    const text = deltaBase64 ? Buffer.from(deltaBase64, "base64").toString("utf8") : String(data.delta ?? "");
    broadcast({
      type: "command.delta",
      threadId: currentThreadId,
      turnId: activeTurnId ?? "",
      itemId: stringField(data, "processId") || "command-exec",
      text,
    });
  } else if (method === "item/plan/delta") {
    streamedItemKeys.add(itemKey(data.turnId, data.itemId));
    broadcast({
      type: "plan.delta",
      threadId: String(data.threadId),
      turnId: String(data.turnId),
      itemId: String(data.itemId),
      text: String(data.delta ?? ""),
    });
  } else if (method === "item/fileChange/outputDelta") {
    streamedItemKeys.add(itemKey(data.turnId, data.itemId));
    broadcast({
      type: "command.delta",
      threadId: String(data.threadId),
      turnId: String(data.turnId),
      itemId: String(data.itemId),
      text: String(data.delta ?? ""),
    });
  } else if (method === "item/completed") {
    const item = data.item;
    if (!item || typeof item !== "object" || Array.isArray(item)) return;
    const completedItem = item as JsonObject;
    const completedItemId = stringField(completedItem, "id");
    if (streamedItemKeys.has(itemKey(data.turnId, completedItemId))) return;

    if (completedItem.type === "agentMessage") {
      broadcast({
        type: "delta",
        threadId: String(data.threadId),
        turnId: String(data.turnId),
        itemId: completedItemId,
        text: stringField(completedItem, "text"),
      });
    } else if (completedItem.type === "plan") {
      broadcast({
        type: "plan.delta",
        threadId: String(data.threadId),
        turnId: String(data.turnId),
        itemId: completedItemId,
        text: stringField(completedItem, "text"),
      });
    } else if (completedItem.type === "commandExecution") {
      const output = stringField(completedItem, "aggregatedOutput");
      if (output) {
        broadcast({
          type: "command.delta",
          threadId: String(data.threadId),
          turnId: String(data.turnId),
          itemId: completedItemId,
          text: output,
        });
      }
    }
  } else if (method === "turn/plan/updated") {
    broadcast({
      type: "plan.updated",
      threadId: String(data.threadId),
      turnId: String(data.turnId),
      explanation: typeof data.explanation === "string" ? data.explanation : null,
      plan: Array.isArray(data.plan) ? data.plan : [],
    });
  } else if (method === "item/fileChange/patchUpdated") {
    broadcast({
      type: "file.patch",
      threadId: String(data.threadId),
      turnId: String(data.turnId),
      itemId: String(data.itemId),
      changes: Array.isArray(data.changes) ? data.changes : [],
    });
  } else if (method === "thread/status/changed") {
    broadcast({ type: "status", threadId: String(data.threadId), status: data.status ?? null });
  } else if (method === "error") {
    broadcast({ type: "error", message: String(data.message ?? "Codex 错误"), detail: data });
  } else if (method === "gateway/error") {
    broadcast({ type: "error", message: String(data.message ?? "Codex 错误"), detail: data });
  }
}

/**
 * 恢复当前桌面线程，并按需要覆盖后续 turn 的工作区。
 */
async function resumeCurrentThread(threadId?: string, cwd?: string): Promise<JsonValue> {
  if (activeTurnId) {
    throw new Error("当前已有 turn 正在执行，请等待完成或先 interrupt");
  }

  const nextThreadId = typeof threadId === "string" && threadId.trim().length > 0 ? threadId.trim() : currentThreadId;
  const resolvedCwd = cwd ? await validateWorkspacePath(cwd) : currentThreadCwd ?? config.defaultCwd;
  currentThreadId = nextThreadId;
  currentThreadCwd = resolvedCwd;
  return codex.request("thread/resume", {
    threadId: currentThreadId,
    cwd: resolvedCwd,
    approvalPolicy: "never",
    sandbox: "danger-full-access",
    excludeTurns: true,
  });
}

/**
 * 创建一个持久化的新 Codex 线程，并切换网关当前线程。
 */
async function startNewThread(cwd?: string): Promise<JsonValue> {
  if (activeTurnId) {
    throw new Error("当前已有 turn 正在执行，请等待完成或先 interrupt");
  }

  const resolvedCwd = cwd ? await validateWorkspacePath(cwd) : currentThreadCwd ?? config.defaultCwd;
  const result = await codex.request("thread/start", {
    cwd: resolvedCwd,
    approvalPolicy: "never",
    sandbox: "danger-full-access",
    ephemeral: false,
    sessionStartSource: "clear",
  });
  const thread = readThread(result);
  const nextThreadId = thread ? stringField(thread, "id") : "";
  if (!nextThreadId) {
    throw new Error("创建线程失败：Codex 未返回 thread id");
  }
  currentThreadId = nextThreadId;
  currentThreadCwd = thread ? stringField(thread, "cwd") || resolvedCwd : resolvedCwd;
  await codex.request("thread/name/set", { threadId: currentThreadId, name: "新会话" });
  await delay(500);
  markThreadVisible(currentThreadId);
  if (thread) thread.name = "新会话";
  streamedItemKeys.clear();
  return result;
}

/**
 * 启动时初始化默认线程：有配置则恢复，没有配置则创建一个持久化线程。
 */
async function initializeDefaultThread(): Promise<void> {
  if (config.threadId) {
    const resumed = await resumeCurrentThread(config.threadId, config.defaultCwd);
    const thread = readThread(resumed);
    if (thread && typeof thread.cwd === "string") currentThreadCwd = thread.cwd;
    return;
  }

  const result = await startNewThread(config.defaultCwd);
  const thread = readThread(result);
  if (thread && typeof thread.cwd === "string") currentThreadCwd = thread.cwd;
}

/**
 * 处理手机端发来的业务消息。
 */
async function handleClientMessage(socket: WebSocket, message: GatewayClientMessage): Promise<void> {
  const requestId = message.id;

  if (message.type === "ping") {
    send(socket, { type: "pong", timestamp: Date.now(), threadId: currentThreadId });
    return;
  }

  if (message.type === "health.check") {
    const report = healthReport();
    send(socket, { type: "health", report });
    send(socket, { type: "response", requestId, ok: true, data: report });
    return;
  }

  if (message.type === "workspace.check") {
    try {
      const cwd = await validateWorkspacePath(message.cwd);
      send(socket, { type: "workspace", ok: true, cwd });
      send(socket, { type: "response", requestId, ok: true, data: { cwd } });
    } catch (error) {
      const messageText = error instanceof Error ? error.message : String(error);
      send(socket, { type: "workspace", ok: false, error: messageText });
      send(socket, { type: "response", requestId, ok: false, error: messageText });
    }
    return;
  }

  if (message.type === "thread.read") {
    const result = await sendThreadHistory(socket, currentThreadId);
    send(socket, { type: "thread", thread: readThread(result) ?? result });
    send(socket, { type: "response", requestId, ok: true, data: result });
    return;
  }

  if (message.type === "thread.list") {
    const cwd = await validateWorkspacePath(message.cwd);
    const { result, threads } = await listThreads(cwd);
    send(socket, { type: "threads", threads });
    send(socket, { type: "response", requestId, ok: true, data: result });
    return;
  }

  if (message.type === "thread.start") {
    const result = await startNewThread(message.cwd);
    const thread = readThread(result);
    send(socket, { type: "thread", thread: thread ?? result });
    send(socket, { type: "history", threadId: currentThreadId, lines: [] });
    if (currentThreadCwd) {
      const { threads } = await listThreads(currentThreadCwd);
      send(socket, { type: "threads", threads });
    }
    send(socket, { type: "response", requestId, ok: true, data: result });
    return;
  }

  if (message.type === "thread.resume") {
    const result = await resumeCurrentThread(message.threadId, message.cwd);
    send(socket, { type: "thread", thread: readThread(result) ?? result });
    const historyResult = await sendThreadHistory(socket, currentThreadId);
    const historyThread = readThread(historyResult);
    if (historyThread && typeof historyThread.cwd === "string") currentThreadCwd = historyThread.cwd;
    send(socket, { type: "response", requestId, ok: true, data: result });
    return;
  }

  if (message.type === "turn.start") {
    if (activeTurnId) {
      throw new Error("当前已有 turn 正在执行，请等待完成或先 interrupt");
    }
    if (typeof message.text !== "string" || message.text.trim().length === 0) {
      throw new Error("消息内容不能为空");
    }
    const cwd = message.cwd ? await validateWorkspacePath(message.cwd) : currentThreadCwd ?? config.defaultCwd;
    currentThreadCwd = cwd;
    const result = await codex.request("turn/start", {
      threadId: message.threadId || currentThreadId,
      cwd,
      approvalPolicy: "never",
      sandboxPolicy: { type: "dangerFullAccess" },
      input: textInput(message.text),
    });
    const turn = readTurn(result);
    activeTurnId = typeof turn?.id === "string" ? turn.id : activeTurnId;
    send(socket, { type: "response", requestId, ok: true, data: result });
    return;
  }

  if (message.type === "turn.steer") {
    if (typeof message.text !== "string" || message.text.trim().length === 0) {
      throw new Error("追加指令不能为空");
    }
    const result = await codex.request("turn/steer", {
      threadId: message.threadId || currentThreadId,
      expectedTurnId: message.expectedTurnId,
      input: textInput(message.text),
    });
    send(socket, { type: "response", requestId, ok: true, data: result });
    return;
  }

  if (message.type === "turn.interrupt") {
    const turnId = message.turnId || activeTurnId;
    if (!turnId) {
      throw new Error("当前没有可停止的 turn");
    }
    const result = await codex.request("turn/interrupt", {
      threadId: message.threadId || currentThreadId,
      turnId,
    });
    activeTurnId = null;
    send(socket, { type: "response", requestId, ok: true, data: result });
    return;
  }

  throw new Error(`不支持的消息类型: ${(message as { type: string }).type}`);
}

/**
 * 绑定 WebSocket 连接生命周期。
 */
function attachClient(socket: WebSocket): void {
  clients.add(socket);
  markClientAlive(socket);
  send(socket, { type: "ready", threadId: currentThreadId, cwd: currentThreadCwd });

  socket.on("pong", () => markClientAlive(socket));

  socket.on("message", (data) => {
    markClientAlive(socket);
    Promise.resolve()
      .then(() => parseClientMessage(data.toString()))
      .then((message) => handleClientMessage(socket, message))
      .catch((error) => {
        send(socket, {
          type: "response",
          ok: false,
          error: error instanceof Error ? error.message : String(error),
        });
      });
  });

  socket.on("close", () => clients.delete(socket));
  socket.on("error", () => {
    clients.delete(socket);
    socket.terminate();
  });
}

/**
 * 定期向手机端连接发送 WebSocket ping，及时关闭云隧道或网络切换后的半开连接。
 */
function startClientHeartbeat(wss: WebSocketServer): NodeJS.Timeout {
  return setInterval(() => {
    const now = Date.now();
    for (const socket of wss.clients) {
      const state = clientStates.get(socket);
      if (state && now - state.lastSeenAt > config.clientIdleTimeoutMs) {
        clients.delete(socket);
        socket.terminate();
        continue;
      }

      if (socket.readyState === socket.OPEN) {
        socket.ping();
      }
    }
  }, config.clientPingIntervalMs);
}

/**
 * 判断带 Origin 的浏览器请求是否来自预期域名。
 */
function isAllowedOrigin(origin: string | undefined): boolean {
  if (!origin) return true;
  const allowed = new Set([
    "https://xxx.com",
    `http://${config.host}:${config.port}`,
    `http://127.0.0.1:${config.port}`,
  ]);
  return allowed.has(origin);
}

/**
 * 启动 HTTP 与 WebSocket 网关。
 */
async function main(): Promise<void> {
  codex.onNotification(handleCodexNotification);
  await codex.start();
  await initializeDefaultThread();

  const server = http.createServer((request, response) => {
    if (request.url?.startsWith("/health")) {
      response.writeHead(200, { "content-type": "application/json; charset=utf-8" });
      response.end(JSON.stringify(healthReport()));
      return;
    }

    response.writeHead(200, {
      "content-type": "text/html; charset=utf-8",
      "cache-control": "no-store",
      "x-content-type-options": "nosniff",
    });
    response.end(TEST_PAGE);
  });

  const wss = new WebSocketServer({ noServer: true });
  wss.on("connection", attachClient);
  const heartbeatTimer = startClientHeartbeat(wss);

  server.on("upgrade", (request, socket, head) => {
    const url = new URL(request.url || "/", `http://${request.headers.host || "localhost"}`);
    const token = url.searchParams.get("token");

    if (url.pathname !== "/ws" || !isAllowedOrigin(request.headers.origin) || !isValidToken(token)) {
      socket.write("HTTP/1.1 401 Unauthorized\r\nConnection: close\r\n\r\n");
      socket.destroy();
      return;
    }

    wss.handleUpgrade(request, socket, head, (webSocket) => {
      wss.emit("connection", webSocket, request);
    });
  });

  server.listen(config.port, config.host, () => {
    console.log(`Codex Mobile Gateway 已启动: http://${config.host}:${config.port}`);
    console.log(`默认线程: ${config.threadId ?? "auto-created"}`);
    console.log(`当前线程: ${currentThreadId}`);
    console.log(`当前工作区: ${currentThreadCwd}`);
  });

  const shutdown = async () => {
    clearInterval(heartbeatTimer);
    for (const client of clients) client.close(1001, "server shutting down");
    server.close();
    await codex.stop();
    process.exit(0);
  };
  process.on("SIGINT", shutdown);
  process.on("SIGTERM", shutdown);
}

main().catch((error) => {
  console.error(redactSecrets(error instanceof Error ? error.message : error));
  process.exit(1);
});
