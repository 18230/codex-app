export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

export type HistoryLine = {
  kind: "system" | "user" | "assistant" | "command" | "plan" | "error";
  text: string;
  itemId?: string;
};

export type GatewayClientMessage =
  | { id?: string; type: "ping" }
  | { id?: string; type: "health.check" }
  | { id?: string; type: "workspace.check"; cwd: string }
  | { id?: string; type: "thread.read"; cwd?: string }
  | { id?: string; type: "thread.list"; cwd: string }
  | { id?: string; type: "thread.start"; cwd?: string }
  | { id?: string; type: "thread.resume"; threadId?: string; cwd?: string }
  | { id?: string; type: "turn.start"; threadId?: string; cwd?: string; text: string }
  | { id?: string; type: "turn.steer"; threadId?: string; expectedTurnId: string; text: string }
  | { id?: string; type: "turn.interrupt"; threadId?: string; turnId?: string };

export type GatewayServerMessage =
  | { type: "ready"; threadId: string; cwd: string | null }
  | { type: "pong"; timestamp: number; threadId: string }
  | { type: "health"; report: JsonValue }
  | { type: "workspace"; ok: boolean; cwd?: string; error?: string }
  | { type: "response"; requestId?: string; ok: true; data: JsonValue }
  | { type: "response"; requestId?: string; ok: false; error: string }
  | { type: "thread"; thread: JsonValue }
  | { type: "history"; threadId: string; lines: HistoryLine[] }
  | { type: "threads"; threads: JsonValue[] }
  | { type: "turn.started"; threadId: string; turnId: string }
  | { type: "turn.completed"; threadId: string; turn: JsonValue }
  | { type: "delta"; threadId: string; turnId: string; itemId: string; text: string }
  | { type: "command.delta"; threadId: string; turnId: string; itemId: string; text: string }
  | { type: "plan.delta"; threadId: string; turnId: string; itemId: string; text: string }
  | { type: "plan.updated"; threadId: string; turnId: string; explanation: string | null; plan: JsonValue[] }
  | { type: "file.patch"; threadId: string; turnId: string; itemId: string; changes: JsonValue[] }
  | { type: "status"; threadId: string; status: JsonValue }
  | { type: "error"; message: string; detail?: JsonValue };
