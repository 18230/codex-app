import './style.css';
import {App} from '../bindings/CodexMobileGateway';
import {Events} from '@wailsio/runtime';

const {
  BindThread,
  ConnectionURL,
  DetectCodexBinary,
  GenerateToken,
  GetConfig,
  HealthCheck,
  ListThreads,
  SaveConfig,
  SelectWorkspace,
  StartGateway,
  StopGateway
} = App;

const state = {
  config: {},
  draft: null,
  status: {},
  threads: [],
  activeTab: 'config',
  message: '',
  messageKind: '',
  refreshTimer: null
};

const app = document.querySelector('#app');

function maskToken(token = '') {
  if (!token) return '';
  if (token.length <= 12) return '••••';
  return `${token.slice(0, 6)}••••${token.slice(-6)}`;
}

function statusText() {
  if (state.status.error) return state.status.error;
  if (!state.status.running) return '网关未启动';
  if (state.status.appServer !== 'connected') return 'Codex 未连接';
  return `运行中 · ${state.status.threadId ? state.status.threadId.slice(0, 8) : '未绑定'}`;
}

function websocketURL(cfg = {}) {
  const token = String(cfg.token || '').trim();
  let baseUrl = String(cfg.lastConnectionBaseUrl || 'https://xxx.com').trim();
  if (!baseUrl) baseUrl = 'https://xxx.com';
  if (!/^https?:\/\//i.test(baseUrl) && !/^wss?:\/\//i.test(baseUrl)) {
    baseUrl = `https://${baseUrl}`;
  }
  try {
    const url = new URL(baseUrl);
    url.protocol = url.protocol === 'http:' || url.protocol === 'ws:' ? 'ws:' : 'wss:';
    url.pathname = '/ws';
    url.search = '';
    if (token) url.searchParams.set('token', token);
    return url.toString();
  } catch {
    const stripped = baseUrl.replace(/\/+$/, '');
    const wsBase = stripped
      .replace(/^https:\/\//i, 'wss://')
      .replace(/^http:\/\//i, 'ws://')
      .replace(/^wss?:\/\//i, match => match.toLowerCase());
    return `${wsBase}/ws${token ? `?token=${encodeURIComponent(token)}` : ''}`;
  }
}

function render() {
  const cfg = state.draft || state.config || {};
  const status = state.status || {};
  const topbarMessageClass = status.error ? 'error' : (status.running ? 'success' : 'muted');
  const messageClass = state.messageKind || (state.message ? 'success' : '');
  const wsUrl = websocketURL(cfg);
  app.innerHTML = `
    <main class="page">
      <header class="topbar">
        <div>
          <h1>CodexMobileGateway</h1>
          <p class="${topbarMessageClass}">${statusText()}</p>
          ${state.message ? `<p class="topbar-log ${messageClass}">${escapeHtml(state.message)}</p>` : ''}
        </div>
        <div class="status ${status.running ? 'ok' : ''}">${status.running ? '运行中' : '已停止'}</div>
      </header>

      <nav class="tabs" role="tablist" aria-label="网关设置">
        <button id="tabConfig" class="tab ${state.activeTab === 'config' ? 'active' : ''}" role="tab" aria-selected="${state.activeTab === 'config'}">基础配置</button>
        <button id="tabThreads" class="tab ${state.activeTab === 'threads' ? 'active' : ''}" role="tab" aria-selected="${state.activeTab === 'threads'}">会话列表</button>
      </nav>

      ${state.activeTab === 'config' ? `
        <section class="panel tab-panel">
          <label>
            <span>工作目录</span>
            <div class="inline">
              <input id="workspace" value="${escapeHtml(cfg.workspace || '')}" />
              <button id="chooseWorkspace">选择</button>
            </div>
          </label>
          <label>
            <span>Token</span>
            <div class="inline">
              <input id="token" value="${escapeHtml(cfg.token || '')}" />
              <button id="generateToken">生成</button>
            </div>
            <small>当前显示：${maskToken(cfg.token)}</small>
          </label>
          <label>
            <span>Codex 可执行文件</span>
            <div class="inline">
              <input id="codexBinary" value="${escapeHtml(cfg.codexBinary || 'codex')}" />
              <button id="detectCodex">自动查找</button>
            </div>
          </label>
          <div class="grid two">
            <label>
              <span>监听地址</span>
              <input id="host" value="${escapeHtml(cfg.host || '127.0.0.1')}" />
            </label>
            <label>
              <span>端口</span>
              <input id="port" type="number" min="1" max="65535" value="${cfg.port || 8000}" />
            </label>
          </div>
          <label>
            <span>公网连接地址</span>
            <div class="inline">
              <input id="baseUrl" value="${escapeHtml(cfg.lastConnectionBaseUrl || 'https://xxx.com')}" />
              <button id="copyUrl">复制连接</button>
            </div>
          </label>
          <label>
            <span>WSS 连接地址</span>
            <div class="inline">
              <input id="wssUrl" value="${escapeHtml(wsUrl)}" readonly />
              <button id="copyWssUrl" class="icon-button" title="复制 WSS 连接地址" aria-label="复制 WSS 连接地址">
                ${copyIcon()}
              </button>
            </div>
            <small>只需要把这个地址填写入手机即可。</small>
          </label>
          <div class="actions">
            <button id="save">保存配置</button>
            <button id="start" ${status.running ? 'disabled' : ''}>启动网关</button>
            <button id="stop" ${status.running ? '' : 'disabled'}>停止网关</button>
            <button id="health">健康检查</button>
          </div>
        </section>
      ` : `
        <section class="panel tab-panel">
          <div class="section-heading">
            <div>
              <div class="section-title">会话列表</div>
              <p>从当前工作目录的会话列表中选择一个线程绑定。</p>
            </div>
            <button id="refreshThreads" ${status.running ? '' : 'disabled'}>刷新线程</button>
          </div>
          <div class="thread-list">
            ${state.threads.length === 0 ? '<div class="empty">暂无线程，启动网关后刷新。</div>' : state.threads.map(thread => `
              <div class="thread ${thread.id === status.threadId ? 'active' : ''}">
                <div class="thread-main">
                  <strong>${escapeHtml(thread.name || thread.preview || '无标题会话')}</strong>
                  <span>${escapeHtml(thread.id)}${thread.cwd ? ` · ${escapeHtml(thread.cwd)}` : ''}</span>
                </div>
                <div class="thread-action">
                  ${thread.id === status.threadId
                    ? '<span class="bound-label">已绑定</span>'
                    : `<button class="bind-thread" data-thread-id="${escapeHtml(thread.id)}">绑定</button>`}
                </div>
              </div>
            `).join('')}
          </div>
        </section>
      `}

      <section class="panel diagnostics">
        <div class="section-title">诊断</div>
        <dl>
          <dt>配置文件</dt><dd>${escapeHtml(status.configPath || '')}</dd>
          <dt>网关</dt><dd>${escapeHtml(status.gateway || 'unknown')}</dd>
          <dt>Codex</dt><dd>${escapeHtml(status.appServer || 'unknown')}</dd>
          <dt>工作目录</dt><dd>${escapeHtml(status.cwd || cfg.workspace || '')}</dd>
          <dt>当前线程</dt><dd>${escapeHtml(status.threadId || '未绑定')}</dd>
        </dl>
      </section>
    </main>
  `;
  bindEvents();
}

function bindEvents() {
  document.querySelector('#tabConfig')?.addEventListener('click', () => switchTab('config'));
  document.querySelector('#tabThreads')?.addEventListener('click', () => switchTab('threads'));
  document.querySelectorAll('input').forEach(input => {
    input.addEventListener('input', syncDraftFromForm);
    input.addEventListener('change', syncDraftFromForm);
  });
  document.querySelector('#chooseWorkspace')?.addEventListener('click', async () => {
    const selected = await SelectWorkspace();
    if (selected) {
      document.querySelector('#workspace').value = selected;
      syncDraftFromForm();
    }
  });
  document.querySelector('#generateToken')?.addEventListener('click', async () => {
    document.querySelector('#token').value = await GenerateToken();
    syncDraftFromForm();
  });
  document.querySelector('#detectCodex')?.addEventListener('click', async () => {
    try {
      const resolved = await DetectCodexBinary(document.querySelector('#codexBinary').value.trim());
      document.querySelector('#codexBinary').value = resolved;
      syncDraftFromForm();
      state.message = '已找到 Codex 可执行文件';
      state.messageKind = 'success';
    } catch (error) {
      state.message = error.toString();
      state.messageKind = 'error';
    }
    render();
  });
  document.querySelector('#save')?.addEventListener('click', saveConfig);
  document.querySelector('#start')?.addEventListener('click', startGateway);
  document.querySelector('#stop')?.addEventListener('click', async () => runAction('停止网关', StopGateway));
  document.querySelector('#health')?.addEventListener('click', async () => {
    syncDraftFromForm();
    state.status = await HealthCheck();
    state.message = '健康检查完成';
    state.messageKind = state.status.error ? 'error' : 'success';
    render();
  });
  document.querySelector('#refreshThreads')?.addEventListener('click', refreshThreads);
  document.querySelector('#copyUrl')?.addEventListener('click', copyConnectionURL);
  document.querySelector('#copyWssUrl')?.addEventListener('click', copyWebSocketURL);
  document.querySelectorAll('.bind-thread').forEach(button => {
    button.addEventListener('click', async () => {
      await runAction('绑定线程', () => BindThread(button.dataset.threadId));
      await refreshThreads();
    });
  });
}

async function switchTab(tab) {
  syncDraftFromForm();
  state.activeTab = tab;
  if (tab === 'threads' && state.status?.running) {
    await refreshThreadsSilently();
  }
  render();
}

function collectConfig() {
  return {
    ...state.config,
    workspace: document.querySelector('#workspace').value.trim(),
    token: document.querySelector('#token').value.trim(),
    codexBinary: document.querySelector('#codexBinary').value.trim(),
    host: document.querySelector('#host').value.trim(),
    port: Number(document.querySelector('#port').value || 8000),
    lastConnectionBaseUrl: document.querySelector('#baseUrl').value.trim()
  };
}

function syncDraftFromForm() {
  if (!document.querySelector('#workspace')) return;
  state.draft = collectConfig();
}

async function saveConfig() {
  try {
    const previousWorkspace = state.config?.workspace || '';
    const snapshot = await SaveConfig(collectConfig());
    state.config = snapshot.config;
    state.draft = null;
    state.status = snapshot.status;
    state.messageKind = 'success';
    const workspaceChanged = previousWorkspace && previousWorkspace !== snapshot.config.workspace;
    if (snapshot.status.running) {
      state.threads = await ListThreads();
      state.message = workspaceChanged
        ? `工作目录已切换，网关已重启，已刷新 ${state.threads.length} 个会话`
        : `配置已保存，已刷新 ${state.threads.length} 个会话`;
    } else {
      state.threads = workspaceChanged ? [] : state.threads;
      state.message = workspaceChanged ? '工作目录已切换，启动网关后会使用新目录' : '配置已保存';
    }
  } catch (error) {
    state.message = error.toString();
    state.messageKind = 'error';
  }
  render();
}

async function runAction(label, action) {
  try {
    syncDraftFromForm();
    state.status = await action();
    state.message = `${label}完成`;
    state.messageKind = state.status.error ? 'error' : 'success';
  } catch (error) {
    state.message = error.toString();
    state.messageKind = 'error';
  }
  render();
}

async function startGateway() {
  try {
    syncDraftFromForm();
    const snapshot = await SaveConfig(collectConfig());
    state.config = snapshot.config;
    state.draft = null;
    state.status = snapshot.status;
    state.status = await StartGateway();
    state.messageKind = state.status.error ? 'error' : 'success';
    if (state.status.error) {
      state.message = state.status.error;
      render();
      return;
    }
    state.threads = await ListThreads();
    state.message = `启动网关完成，已刷新 ${state.threads.length} 个会话`;
  } catch (error) {
    state.message = error.toString();
    state.messageKind = 'error';
  }
  render();
}

async function refreshThreads() {
  try {
    syncDraftFromForm();
    state.threads = await ListThreads();
    state.message = `已刷新 ${state.threads.length} 个线程`;
    state.messageKind = 'success';
  } catch (error) {
    state.message = error.toString();
    state.messageKind = 'error';
  }
  render();
}

async function refreshThreadsSilently() {
  if (!state.status?.running) return;
  try {
    state.threads = await ListThreads();
  } catch (error) {
    state.message = error.toString();
    state.messageKind = 'error';
  }
}

function scheduleThreadRefresh(delay = 0) {
  if (state.refreshTimer) {
    window.clearTimeout(state.refreshTimer);
  }
  state.refreshTimer = window.setTimeout(async () => {
    state.refreshTimer = null;
    await refreshThreadsSilently();
    render();
  }, delay);
}

async function copyConnectionURL() {
  try {
    syncDraftFromForm();
    const url = await ConnectionURL(document.querySelector('#baseUrl').value.trim());
    await navigator.clipboard.writeText(url);
    state.message = '连接地址已复制';
    state.messageKind = 'success';
  } catch (error) {
    state.message = error.toString();
    state.messageKind = 'error';
  }
  render();
}

async function copyWebSocketURL() {
  try {
    syncDraftFromForm();
    await navigator.clipboard.writeText(websocketURL(state.draft || state.config || {}));
    state.message = 'WSS 连接地址复制成功';
    state.messageKind = 'success';
  } catch (error) {
    state.message = error.toString();
    state.messageKind = 'error';
  }
  render();
}

function copyIcon() {
  return `
    <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <rect x="9" y="9" width="11" height="11" rx="2"></rect>
      <path d="M5 15V6a2 2 0 0 1 2-2h9"></path>
    </svg>
  `;
}

function escapeHtml(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}

async function boot() {
  try {
    const snapshot = await GetConfig();
    state.config = snapshot.config;
    state.status = snapshot.status;
  } catch (error) {
    state.message = error.toString();
    state.messageKind = 'error';
  }
  render();
}

Events.On('gateway:status', async (event) => {
  const previousThreadId = state.status?.threadId || '';
  state.status = event.data;
  state.messageKind = state.status.error ? 'error' : state.messageKind;
  const nextThreadId = state.status?.threadId || '';
  if (nextThreadId !== previousThreadId) {
    await refreshThreadsSilently();
  }
  render();
});

Events.On('gateway:threadsChanged', async () => {
  await refreshThreadsSilently();
  render();
  scheduleThreadRefresh(1200);
});

boot();
