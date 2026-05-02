export const TEST_PAGE = String.raw`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Codex Mobile Gateway Tester</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f7f7f3;
      --panel: #ffffff;
      --line: #d7d8d0;
      --text: #1f2622;
      --muted: #5a665f;
      --primary: #2f6f5e;
      --danger: #9b2f2f;
      --code: #1f2925;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: var(--bg);
      color: var(--text);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    main {
      width: min(980px, calc(100vw - 32px));
      margin: 32px auto;
      display: grid;
      gap: 16px;
    }
    header {
      display: flex;
      align-items: end;
      justify-content: space-between;
      gap: 16px;
    }
    h1 {
      margin: 0;
      font-size: 28px;
      letter-spacing: 0;
    }
    .status {
      color: var(--muted);
      font-size: 14px;
    }
    section {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 16px;
    }
    .grid {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 12px;
    }
    label {
      display: grid;
      gap: 6px;
      font-size: 13px;
      color: var(--muted);
    }
    input, textarea {
      width: 100%;
      border: 1px solid #b8bbb2;
      border-radius: 6px;
      padding: 10px 12px;
      font: inherit;
      color: var(--text);
      background: #fff;
    }
    textarea {
      min-height: 84px;
      resize: vertical;
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    }
    .actions {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      margin-top: 12px;
    }
    button {
      border: 0;
      border-radius: 6px;
      padding: 10px 14px;
      font: inherit;
      font-weight: 650;
      color: #fff;
      background: var(--primary);
      cursor: pointer;
    }
    button.secondary { background: #52645d; }
    button.danger { background: var(--danger); }
    button:disabled {
      cursor: not-allowed;
      opacity: .55;
    }
    .log {
      min-height: 360px;
      max-height: 58vh;
      overflow: auto;
      background: var(--code);
      color: #e8f5ee;
      border-radius: 8px;
      padding: 12px;
      font: 13px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }
    .hint {
      margin: 0;
      color: var(--muted);
      font-size: 13px;
    }
    @media (max-width: 720px) {
      main { width: calc(100vw - 24px); margin: 16px auto; }
      header { display: grid; }
      .grid { grid-template-columns: 1fr; }
      button { width: 100%; }
    }
  </style>
</head>
<body>
  <main>
    <header>
      <div>
        <h1>Codex Gateway Tester</h1>
        <div id="status" class="status">未连接</div>
      </div>
      <div class="status" id="thread"></div>
    </header>

    <section>
      <div class="grid">
        <label>
          WebSocket
          <input id="wsUrl" autocomplete="off" />
        </label>
        <label>
          Token
          <input id="token" autocomplete="off" type="password" />
        </label>
        <label>
          工作区
          <input id="cwd" autocomplete="off" value="/Users/gaoqi/Desktop/data/local" />
        </label>
        <label>
          消息
          <input id="message" autocomplete="off" placeholder="输入后可发起 turn.start" />
        </label>
      </div>
      <div class="actions">
        <button id="connect">连接</button>
        <button id="resume" class="secondary" disabled>恢复线程</button>
        <button id="read" class="secondary" disabled>读取线程</button>
        <button id="send" disabled>发送消息</button>
        <button id="interrupt" class="danger" disabled>停止</button>
        <button id="clear" class="secondary">清空日志</button>
      </div>
      <p class="hint">第三方 WebSocket 测试站不适合粘贴这个 token；这个页面从你的网关本机提供。</p>
    </section>

    <section>
      <label>
        原始 JSON
        <textarea id="raw">{"type":"thread.read","id":"manual-read"}</textarea>
      </label>
      <div class="actions">
        <button id="sendRaw" class="secondary" disabled>发送 JSON</button>
      </div>
    </section>

    <section>
      <div id="log" class="log"></div>
    </section>
  </main>

  <script>
    const query = new URLSearchParams(location.search);
    const wsDefault = (location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/ws";
    const state = { socket: null, activeTurnId: null, heartbeatTimer: null };

    const els = {
      wsUrl: document.getElementById("wsUrl"),
      token: document.getElementById("token"),
      cwd: document.getElementById("cwd"),
      message: document.getElementById("message"),
      status: document.getElementById("status"),
      thread: document.getElementById("thread"),
      log: document.getElementById("log"),
      raw: document.getElementById("raw"),
      connect: document.getElementById("connect"),
      resume: document.getElementById("resume"),
      read: document.getElementById("read"),
      send: document.getElementById("send"),
      interrupt: document.getElementById("interrupt"),
      clear: document.getElementById("clear"),
      sendRaw: document.getElementById("sendRaw"),
    };

    els.wsUrl.value = wsDefault;
    els.token.value = query.get("token") || "";

    function log(label, value) {
      const time = new Date().toLocaleTimeString();
      const body = typeof value === "string" ? value : JSON.stringify(value, null, 2);
      els.log.textContent += "[" + time + "] " + label + "\n" + body + "\n\n";
      els.log.scrollTop = els.log.scrollHeight;
    }

    function setConnected(connected) {
      els.resume.disabled = !connected;
      els.read.disabled = !connected;
      els.send.disabled = !connected;
      els.interrupt.disabled = !connected;
      els.sendRaw.disabled = !connected;
      els.connect.textContent = connected ? "重连" : "连接";
    }

    function wsUrlWithToken() {
      const base = els.wsUrl.value.trim();
      const token = els.token.value.trim();
      if (!base) throw new Error("WebSocket 地址不能为空");
      if (!token) throw new Error("Token 不能为空");
      const url = new URL(base);
      url.searchParams.set("token", token);
      return url.toString();
    }

    function sendJson(payload) {
      if (!state.socket || state.socket.readyState !== WebSocket.OPEN) {
        throw new Error("WebSocket 尚未连接");
      }
      if (!payload.id) payload.id = "web-" + Date.now();
      state.socket.send(JSON.stringify(payload));
      log("SEND", payload);
    }

    function connect() {
      if (state.socket) state.socket.close();
      if (state.heartbeatTimer) clearInterval(state.heartbeatTimer);
      let url;
      try {
        url = wsUrlWithToken();
      } catch (error) {
        log("ERROR", error.message);
        return;
      }
      els.status.textContent = "连接中";
      state.socket = new WebSocket(url);
      state.socket.onopen = function () {
        els.status.textContent = "已连接";
        setConnected(true);
        log("OPEN", els.wsUrl.value.trim());
        state.heartbeatTimer = setInterval(function () {
          if (state.socket && state.socket.readyState === WebSocket.OPEN) {
            state.socket.send(JSON.stringify({ type: "ping", id: "web-heartbeat-" + Date.now() }));
          }
        }, 25000);
      };
      state.socket.onmessage = function (event) {
        let data;
        try {
          data = JSON.parse(event.data);
        } catch {
          log("MESSAGE", event.data);
          return;
        }
        if (data.type === "ready") {
          els.thread.textContent = data.threadId || "";
        }
        if (data.type === "pong") {
          return;
        }
        if (data.type === "turn.started") {
          state.activeTurnId = data.turnId || null;
        }
        if (data.type === "turn.completed") {
          state.activeTurnId = null;
        }
        log("RECV " + (data.type || ""), data);
      };
      state.socket.onerror = function () {
        els.status.textContent = "连接错误";
        log("ERROR", "WebSocket 连接错误");
      };
      state.socket.onclose = function (event) {
        if (state.heartbeatTimer) clearInterval(state.heartbeatTimer);
        state.heartbeatTimer = null;
        els.status.textContent = "已关闭 " + event.code;
        setConnected(false);
        log("CLOSE", { code: event.code, reason: event.reason });
      };
    }

    els.connect.onclick = connect;
    els.resume.onclick = function () {
      sendJson({ type: "thread.resume", cwd: els.cwd.value.trim() });
    };
    els.read.onclick = function () {
      sendJson({ type: "thread.read" });
    };
    els.send.onclick = function () {
      const text = els.message.value.trim();
      if (!text) return;
      sendJson({ type: "turn.start", cwd: els.cwd.value.trim(), text: text });
    };
    els.interrupt.onclick = function () {
      sendJson({ type: "turn.interrupt", turnId: state.activeTurnId || undefined });
    };
    els.clear.onclick = function () {
      els.log.textContent = "";
    };
    els.sendRaw.onclick = function () {
      try {
        sendJson(JSON.parse(els.raw.value));
      } catch (error) {
        log("ERROR", error.message);
      }
    };

    if (els.token.value) {
      connect();
    }
  </script>
</body>
</html>`;
