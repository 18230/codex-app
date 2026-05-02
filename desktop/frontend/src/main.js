import './style.css';
import {
  BindThread,
  ConnectionURL,
  GenerateToken,
  GetConfig,
  HealthCheck,
  ListThreads,
  SaveConfig,
  SelectWorkspace,
  StartGateway,
  StopGateway
} from '../wailsjs/go/main/App';
import {EventsOn} from '../wailsjs/runtime/runtime';

const state = {
  config: {},
  status: {},
  threads: [],
  message: ''
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

function render() {
  const cfg = state.config || {};
  const status = state.status || {};
  app.innerHTML = `
    <main class="page">
      <header class="topbar">
        <div>
          <h1>CodexMobileGateway</h1>
          <p>${statusText()}</p>
        </div>
        <div class="status ${status.running ? 'ok' : ''}">${status.running ? '运行中' : '已停止'}</div>
      </header>

      <section class="panel">
        <div class="section-title">配置</div>
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
          <input id="codexBinary" value="${escapeHtml(cfg.codexBinary || 'codex')}" />
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
        <div class="actions">
          <button id="save">保存配置</button>
          <button id="start" ${status.running ? 'disabled' : ''}>启动网关</button>
          <button id="stop" ${status.running ? '' : 'disabled'}>停止网关</button>
          <button id="health">健康检查</button>
        </div>
      </section>

      <section class="panel">
        <div class="section-heading">
          <div>
            <div class="section-title">线程绑定</div>
            <p>CODEX_THREAD_ID 不需要手动填写，从当前工作目录的线程列表选择即可。</p>
          </div>
          <button id="refreshThreads" ${status.running ? '' : 'disabled'}>刷新线程</button>
        </div>
        <div class="thread-list">
          ${state.threads.length === 0 ? '<div class="empty">暂无线程，启动网关后刷新。</div>' : state.threads.map(thread => `
            <button class="thread ${thread.id === status.threadId ? 'active' : ''}" data-thread-id="${escapeHtml(thread.id)}">
              <strong>${escapeHtml(thread.name || thread.preview || '无标题会话')}</strong>
              <span>${escapeHtml(thread.id)}${thread.cwd ? ` · ${escapeHtml(thread.cwd)}` : ''}</span>
            </button>
          `).join('')}
        </div>
      </section>

      <section class="panel diagnostics">
        <div class="section-title">诊断</div>
        <dl>
          <dt>配置文件</dt><dd>${escapeHtml(status.configPath || '')}</dd>
          <dt>网关</dt><dd>${escapeHtml(status.gateway || 'unknown')}</dd>
          <dt>Codex</dt><dd>${escapeHtml(status.appServer || 'unknown')}</dd>
          <dt>工作目录</dt><dd>${escapeHtml(status.cwd || cfg.workspace || '')}</dd>
          <dt>当前线程</dt><dd>${escapeHtml(status.threadId || '未绑定')}</dd>
        </dl>
        <div class="message">${escapeHtml(state.message)}</div>
      </section>
    </main>
  `;
  bindEvents();
}

function bindEvents() {
  document.querySelector('#chooseWorkspace')?.addEventListener('click', async () => {
    const selected = await SelectWorkspace();
    if (selected) document.querySelector('#workspace').value = selected;
  });
  document.querySelector('#generateToken')?.addEventListener('click', async () => {
    document.querySelector('#token').value = await GenerateToken();
  });
  document.querySelector('#save')?.addEventListener('click', saveConfig);
  document.querySelector('#start')?.addEventListener('click', async () => runAction('启动网关', StartGateway));
  document.querySelector('#stop')?.addEventListener('click', async () => runAction('停止网关', StopGateway));
  document.querySelector('#health')?.addEventListener('click', async () => {
    state.status = await HealthCheck();
    state.message = '健康检查完成';
    render();
  });
  document.querySelector('#refreshThreads')?.addEventListener('click', refreshThreads);
  document.querySelector('#copyUrl')?.addEventListener('click', copyConnectionURL);
  document.querySelectorAll('.thread').forEach(button => {
    button.addEventListener('click', async () => {
      await runAction('绑定线程', () => BindThread(button.dataset.threadId));
      await refreshThreads();
    });
  });
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

async function saveConfig() {
  try {
    const snapshot = await SaveConfig(collectConfig());
    state.config = snapshot.config;
    state.status = snapshot.status;
    state.message = '配置已保存';
  } catch (error) {
    state.message = error.toString();
  }
  render();
}

async function runAction(label, action) {
  try {
    state.status = await action();
    state.message = `${label}完成`;
  } catch (error) {
    state.message = error.toString();
  }
  render();
}

async function refreshThreads() {
  try {
    state.threads = await ListThreads();
    state.message = `已刷新 ${state.threads.length} 个线程`;
  } catch (error) {
    state.message = error.toString();
  }
  render();
}

async function copyConnectionURL() {
  try {
    const url = await ConnectionURL(document.querySelector('#baseUrl').value.trim());
    await navigator.clipboard.writeText(url);
    state.message = '连接地址已复制';
  } catch (error) {
    state.message = error.toString();
  }
  render();
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
  }
  render();
}

EventsOn('gateway:status', (status) => {
  state.status = status;
  render();
});

boot();
