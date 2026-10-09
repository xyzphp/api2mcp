(function () {
  'use strict';

  const C = window.Api2McpCore;
  const app = document.getElementById('app');
  const dialogs = document.getElementById('dialogs');
  const notifications = document.getElementById('notifications');
  const appBasePath = new URL(document.querySelector('base')?.href || '/', window.location.origin).pathname.replace(/\/$/, '');
  const appPath = route => `${appBasePath}${route.startsWith('/') ? route : `/${route}`}`;
  const mobileViewport = window.matchMedia('(max-width: 760px)');
  const state = {
    workspace: { documents: [], servers: [], logs: [], settings: { endpointOrigin: window.location.origin } }, page: 'servers', selectedId: '', selectedDocumentId: '',
    serverQuery: '', serverStatus: 'all', serverType: 'all', documentQuery: '', documentCredential: 'all', apiQuery: '', apiMethod: 'all', logQuery: '', logStatus: 'all',
    sidebarOpen: false, credentials: {}, storageError: false,
    dialog: null, connectionTab: 'address', clientDrafts: {}, clientHeaderConfigs: {}, clientHeaderLoads: {}, clientHeaderErrors: {}, clientHeaderGeneration: 0,
  };
  let toastTimer;
  let modalReturnFocus;
  let modalReturnSelector;
  let modalTimers = [];
  let fetchController;

  async function api(path, method = 'GET', body, signal) {
    const response = await fetch(appPath(path), {
      method, signal, credentials: 'same-origin', cache: 'no-store',
      headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'API2MCP' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    if (response.status === 401) { window.location.replace(appPath('/login')); throw new Error('登录已过期，请重新登录。'); }
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || `请求失败（HTTP ${response.status}）`);
    return result;
  }
  async function refresh() {
    state.workspace = await api('/api/workspace');
    state.credentials = Object.fromEntries(state.workspace.documents.map(doc => [doc.id, doc.credential]));
  }
  function invalidateClientHeaders() {
    state.clientHeaderGeneration += 1;
    state.clientHeaderConfigs = {};
    state.clientHeaderLoads = {};
    state.clientHeaderErrors = {};
  }
  function dialogBusy(d, busy) {
    d.busy = busy;
    dialogs.querySelectorAll('input, select, textarea, button:not([data-action="close-dialog"])').forEach(control => {
      if (busy) {
        if (control.dataset.busyDisabled === undefined) control.dataset.busyDisabled = String(control.disabled);
        control.disabled = true;
      } else if (control.dataset.busyDisabled !== undefined) {
        control.disabled = control.dataset.busyDisabled === 'true';
        delete control.dataset.busyDisabled;
      }
    });
  }

  // Imported document content is always rendered as text, including attribute values.
  const escape = value => String(value ?? '').replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
  const e = escape;
  const color = value => ['green', 'purple', 'blue'].includes(value) ? value : 'blue';
  const allOperations = () => state.workspace.documents.flatMap(doc => doc.operations);
  const getServer = id => state.workspace.servers.find(server => server.id === id);
  const getDocument = id => state.workspace.documents.find(doc => doc.id === id);
  const getOperation = id => allOperations().find(op => op.id === id);
  const endpoint = server => C.endpointFor(server, state.workspace.settings.endpointOrigin);
  const isProxy = server => server?.type === 'proxy';
  const configLabel = server => isProxy(server) ? '代理配置' : '配置 API';
  const matches = (value, query) => value.toLowerCase().includes(query.trim().toLowerCase());
  const navigation = [
    ['overview', '概览', 'home'], ['documents', 'API 文档', 'file'], ['servers', 'MCP Server', 'box'],
    ['logs', '调用日志', 'clipboard'],
  ];
  const paths = {
    home: '<path d="m3 10 9-7 9 7v10a1 1 0 0 1-1 1h-5v-8H9v8H4a1 1 0 0 1-1-1Z"/>',
    file: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z"/><path d="M14 2v6h6M8 13h8M8 17h6"/>',
    box: '<path d="m21 8-9 5-9-5M12 13v9M21 7v10a2 2 0 0 1-1 1.7l-7 4a2 2 0 0 1-2 0l-7-4A2 2 0 0 1 3 17V7a2 2 0 0 1 1-1.7l7-4a2 2 0 0 1 2 0l7 4A2 2 0 0 1 21 7Z"/>',
    clipboard: '<rect x="5" y="4" width="14" height="18" rx="2"/><rect x="9" y="2" width="6" height="4" rx="1"/><path d="M9 11h6M9 15h6M9 19h4"/>',
    sliders: '<path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6"/>',
    search: '<circle cx="10.5" cy="10.5" r="7.5"/><path d="m16 16 5 5"/>',
    upload: '<path d="M12 16V3m-5 5 5-5 5 5M4 15v5a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-5"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    copy: '<rect x="8" y="8" width="13" height="13" rx="2"/><path d="M16 8V4a1 1 0 0 0-1-1H4a1 1 0 0 0-1 1v11a1 1 0 0 0 1 1h4"/>',
    play: '<path d="m8 4 12 8-12 8Z"/>', pause: '<path d="M8 4v16M16 4v16"/>',
    more: '<circle cx="4" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="20" cy="12" r="1"/>',
    check: '<path d="m5 12 4 4L19 6"/>', circleCheck: '<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',
    shield: '<path d="M12 2 3 6v6c0 5 9 10 9 10s9-5 9-10V6Z"/><path d="m8 12 3 3 5-6"/>',
    eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>',
    eyeOff: '<path d="m3 3 18 18M10.6 5.1 12 5c6.5 0 10 7 10 7a18 18 0 0 1-3 4M6.5 6.5A19 19 0 0 0 2 12s3.5 7 10 7a13 13 0 0 0 5.5-1.5M10 10a3 3 0 0 0 4 4"/>',
    code: '<path d="m8 5-6 7 6 7m8-14 6 7-6 7m-3-16-2 18"/>',
    info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7v.1"/>',
    chevron: '<path d="m9 5 7 7-7 7"/>',
    arrow: '<path d="M4 12h16m-6-6 6 6-6 6"/>',
    layers: '<path d="m12 2 10 6-10 6L2 8Zm-9 10 9 5 9-5M3 17l9 5 9-5"/>',
    globe: '<circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18"/>',
    activity: '<path d="M2 12h4l3-8 6 16 3-8h4"/>',
    trash: '<path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7"/>',
    x: '<path d="m6 6 12 12M6 18 18 6"/>', menu: '<path d="M4 6h16M4 12h16M4 18h16"/>',
    loader: '<path d="M12 3a9 9 0 1 1-9 9"/>', circle: '<circle cx="12" cy="12" r="9"/>',
  };
  function icon(name, size = 18, className = '') {
    return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"${className ? ` class="${className}"` : ''}>${paths[name] || paths.box}</svg>`;
  }
  function iconButton(label, name, action, id = '', disabled = false) {
    return `<button type="button" class="icon-button" title="${e(label)}" aria-label="${e(label)}" data-action="${action}" data-id="${e(id)}" ${disabled ? 'disabled' : ''}>${icon(name)}</button>`;
  }
  function button(label, action, name = '', variant = '', id = '', disabled = false) {
    return `<button type="button" class="button ${variant}" data-action="${action}" data-id="${e(id)}" ${disabled ? 'disabled' : ''}>${name ? icon(name, 17) : ''}${e(label)}</button>`;
  }
  function statusBadge(status) {
    return `<span class="status-badge ${e(status)}"><i></i>${{ running: '运行中', stopped: '已停用', draft: '草稿' }[status]}</span>`;
  }
  function methodBadge(method) {
    return `<span class="method-badge method-${e(method.toLowerCase())}">${e(method)}</span>`;
  }
  function empty(title, description, action = '') {
    return `<div class="empty-state"><div class="empty-icon">${icon('box', 28)}</div><h3>${e(title)}</h3><p>${e(description)}</p>${action}</div>`;
  }
  function search(field, placeholder, label, value) {
    return `<div class="search-input">${icon('search')}<input type="search" data-field="${field}" id="${field}" aria-label="${label}" placeholder="${placeholder}" value="${e(value)}" autocomplete="off"></div>`;
  }
  function logo() {
    return `<div class="brand"><svg width="36" height="36" viewBox="0 0 48 48" fill="none" aria-hidden="true"><path d="m24 13-13 22m13-22 13 22" stroke="currentColor" stroke-width="3"/><circle cx="24" cy="10" r="6" fill="white" stroke="currentColor" stroke-width="3"/><circle cx="10" cy="36" r="6" fill="white" stroke="currentColor" stroke-width="3"/><circle cx="38" cy="36" r="6" fill="white" stroke="currentColor" stroke-width="3"/><circle cx="24" cy="10" r="2" fill="currentColor"/><circle cx="10" cy="36" r="2" fill="currentColor"/><circle cx="38" cy="36" r="2" fill="currentColor"/></svg><span>API2MCP</span></div>`;
  }

  function preserveFocus(callback, container = app) {
    const active = document.activeElement;
    const id = container.contains(active) ? active.id : '';
    const action = container.contains(active) ? active.dataset?.action : '';
    const selector = action ? `${active.tagName.toLowerCase()}[data-action="${CSS.escape(action)}"][data-id="${CSS.escape(active.dataset.id || '')}"]` : '';
    const start = active.selectionStart, end = active.selectionEnd;
    callback();
    if (id || selector) {
      const next = id ? document.getElementById(id) : container.querySelector(selector);
      next?.focus({ preventScroll: true });
      if (typeof start === 'number' && next?.setSelectionRange) {
        try { next.setSelectionRange(start, end); } catch { /* Non-text fields have no caret. */ }
      }
    }
  }
  function render() {
    const activePage = { 'server-detail': 'servers', 'document-detail': 'documents' }[state.page] || state.page;
    const label = { 'server-detail': '服务详情', 'document-detail': '文档详情' }[state.page] || navigation.find(item => item[0] === activePage)[1];
    document.title = `API2MCP · ${label}`;
    preserveFocus(() => {
      app.innerHTML = `<div class="app-shell">
        ${state.sidebarOpen ? '<button class="sidebar-scrim" data-action="close-sidebar" aria-label="关闭导航"></button>' : ''}
        <aside class="sidebar ${state.sidebarOpen ? 'sidebar-open' : ''}" aria-label="主导航" ${mobileViewport.matches && !state.sidebarOpen ? 'inert aria-hidden="true"' : ''}>
          <div class="sidebar-brand">${logo()}<button class="icon-button mobile-close" data-action="close-sidebar" aria-label="关闭导航">${icon('x')}</button></div>
          <nav>${navigation.map(([id, text, glyph]) => `<button class="nav-item ${activePage === id ? 'active' : ''}" data-action="navigate" data-id="${id}" title="${text}" ${activePage === id ? 'aria-current="page"' : ''}>${icon(glyph, 21)}<span>${text}</span>${activePage === id ? '<i></i>' : ''}</button>`).join('')}</nav>
        </aside>
        <div class="workspace-main"><header class="topbar"><div class="breadcrumbs"><button class="icon-button mobile-menu" data-action="open-sidebar" aria-label="展开导航" aria-expanded="${state.sidebarOpen}">${icon('menu', 21)}</button><span>工作空间</span><span class="breadcrumb-slash">/</span><span>${label}</span></div><button class="text-button logout-button" data-action="logout">退出登录</button></header>
          <main class="main-content" id="main-content"><div class="preview-notice">${icon('shield', 13)}<span>后台已连接 · Streamable HTTP MCP 服务</span><span class="local-save-state"><i></i>配置已保存到服务端</span></div>${pageContent()}</main>
          <footer class="app-footer"><span>API2MCP</span><span>连接你的 API 与 AI</span><span>v0.2.0</span></footer>
      </div></div>`;
    });
    if (state.page === 'server-detail' && state.connectionTab === 'json' && state.selectedId) {
      queueMicrotask(() => ensureClientHeaders(state.selectedId));
    }
  }
  function pageContent() {
    return ({ servers: serversPage, 'server-detail': serverDetailPage, overview: overviewPage, documents: documentsPage, 'document-detail': documentDetailPage, logs: logsPage })[state.page]();
  }
  function heading(title, subtitle, actions = '', count = '') {
    return `<div class="page-heading"><div><h1>${title}${count ? ` <span class="heading-count"><i></i>${count}</span>` : ''}</h1><p>${subtitle}</p></div>${actions ? `<div class="button-group page-actions">${actions}</div>` : ''}</div>`;
  }
  function filteredServers() {
    return state.workspace.servers.filter(server => matches(`${server.name} ${endpoint(server)} ${server.proxy?.url || ''}`, state.serverQuery) && (state.serverStatus === 'all' || state.serverStatus === server.status) && (state.serverType === 'all' || state.serverType === (server.type || 'api')));
  }
  const sourceDocuments = server => state.workspace.documents.filter(doc => doc.operations.some(op => server.operationIds.includes(op.id)));
  function serversPage() {
    const servers = filteredServers();
    return heading('MCP Server 管理', '组合 API 或代理已有 MCP，为客户端提供独立调用地址。', button('导入 API 文档', 'import', 'upload') + button('添加 MCP 代理', 'create-proxy', 'globe') + button('创建 MCP Server', 'create', 'plus', 'primary'), `${state.workspace.servers.length} 个服务`) +
      `<div class="server-summary-strip"><span><i class="summary-dot"></i><strong>${state.workspace.servers.filter(s => s.status === 'running').length}</strong> 个运行中</span><span><strong>${state.workspace.servers.filter(s => s.status === 'stopped').length}</strong> 个已停用</span><span><strong>${state.workspace.servers.filter(s => s.status === 'draft').length}</strong> 个草稿</span><span><strong>${state.workspace.servers.filter(isProxy).length}</strong> 个 MCP 代理</span><span class="summary-protocol">${icon('globe', 15)}Streamable HTTP</span></div>
      <section class="panel servers-panel server-directory" aria-label="MCP Server 列表"><div class="list-toolbar">${search('serverQuery', '搜索服务名称、调用地址或上游地址', '搜索服务', state.serverQuery)}<select data-field="serverType" aria-label="筛选服务类型">${[['all', '全部类型'], ['api', 'API 转换'], ['proxy', 'MCP 代理']].map(([id, label]) => `<option value="${id}" ${state.serverType === id ? 'selected' : ''}>${label}</option>`).join('')}</select><select data-field="serverStatus" aria-label="筛选服务状态">${[['all', '全部状态'], ['running', '运行中'], ['stopped', '已停用'], ['draft', '草稿']].map(([id, label]) => `<option value="${id}" ${state.serverStatus === id ? 'selected' : ''}>${label}</option>`).join('')}</select></div><div class="server-list">${servers.length ? servers.map(serverCard).join('') : empty(state.workspace.servers.length ? '没有找到匹配的服务' : '创建第一个 MCP Server', state.workspace.servers.length ? '尝试其他关键词或服务状态。' : '导入文档组合 API，或填写已有 MCP 地址添加代理。', state.workspace.servers.length ? button('清除筛选', 'clear-server-filters') : button('创建 MCP Server', 'create', 'plus', 'primary'))}</div><div class="list-footer"><span>共 ${servers.length} 个服务</span><span>点击服务名称查看详情与客户端配置</span></div></section>`;
  }
  function serverCard(server) {
    const sources = sourceDocuments(server);
    const proxy = isProxy(server);
    const metadata = proxy ? `<span class="server-type-badge">MCP 代理</span><i></i><span>Streamable HTTP</span><i></i><span>${server.proxy?.headerNames?.length || 0} 个认证 Header</span>` : `<span>${server.operationIds.length} 个 API</span><i></i><span>${sources.length} 份文档</span>`;
    return `<article class="server-card directory-card" data-server="${e(server.slug)}">
      <div class="server-card-heading"><div class="service-icon ${color(server.color)}">${icon(proxy ? 'globe' : 'box', 25)}</div><div class="server-card-info"><div class="server-title-line"><button class="server-title" data-action="show-server" data-id="${e(server.id)}">${e(server.name)}</button>${statusBadge(server.status)}${server.draft ? '<span class="tiny-badge">有未发布更改</span>' : ''}</div><div class="server-metadata">${metadata}<i></i><span>${server.status === 'draft' ? '待发布' : C.versionLabel(server)}</span><div class="document-chips">${sources.slice(0, 2).map(doc => `<span class="document-chip">${e(doc.name)}</span>`).join('')}</div></div></div>
      <details class="more-menu"><summary aria-label="${e(server.name)}更多操作" title="更多操作">${icon('more', 21)}</summary><div class="menu-popover"><button data-action="edit" data-id="${e(server.id)}">${icon('sliders', 15)}${configLabel(server)}</button><button data-action="toggle-server" data-id="${e(server.id)}">${icon(server.status === 'running' ? 'pause' : 'play', 15)}${server.status === 'running' ? '停用服务' : server.status === 'draft' ? '编辑并发布' : '启用服务'}</button><button data-action="duplicate-server" data-id="${e(server.id)}">${icon('copy', 15)}复制为新服务</button><div class="menu-divider"></div><button class="destructive-text" data-action="delete-server" data-id="${e(server.id)}">${icon('trash', 15)}删除服务</button></div></details></div>
      ${server.description ? `<p class="server-description">${e(server.description)}</p>` : ''}${proxy ? `<div class="proxy-upstream-line">${icon('globe', 14)}<span>上游</span><code>${e(server.proxy?.url || '尚未配置')}</code></div>` : ''}
      <div class="server-card-bottom"><div class="endpoint-field ${server.status === 'draft' ? 'endpoint-unpublished' : ''}"><span class="endpoint-prefix">HTTP</span><code title="${server.status === 'draft' ? '' : e(endpoint(server))}">${server.status === 'draft' ? '发布后生成独立调用地址' : e(endpoint(server))}</code>${iconButton(`复制${server.name}地址`, 'copy', 'copy-url', server.id, server.status === 'draft')}</div><div class="card-actions">${button('JSON 配置', 'open-client-config', 'code', '', server.id, server.status === 'draft')}${button('测试连接', 'test', 'play', '', server.id, server.status === 'draft')}${button('查看详情', 'show-server', 'arrow', 'detail-link', server.id)}</div></div>
    </article>`;
  }
  function serverDetailPage() {
    const server = getServer(state.selectedId);
    if (!server) return empty('服务不存在', '该服务可能已被删除。', button('返回服务列表', 'navigate', 'arrow', '', 'servers'));
    const proxy = isProxy(server);
    const selected = server.operationIds.map(getOperation).filter(Boolean);
    const sources = sourceDocuments(server);
    const connectionContent = state.connectionTab === 'json'
      ? clientConfigPanel(server)
      : `<div class="connection-fields"><div class="connection-address"><label>MCP 调用地址 <span class="tiny-badge">Streamable HTTP</span></label><div class="endpoint-field"><code>${server.status === 'draft' ? '发布后生成' : e(C.endpointFor(server, state.workspace.settings.endpointOrigin))}</code>${iconButton('复制服务连接地址', 'copy', 'copy-url', server.id, server.status === 'draft')}</div></div><div class="connection-token"><label>调用 Token ${iconButton('重置调用 Token', 'shield', 'rotate-token', server.id, !server.token)}</label><div class="token-field"><code class="visible-token" aria-label="MCP 调用 Token">${e(server.token || '发布后生成')}</code>${iconButton('复制访问 Token', 'copy', 'copy-token', server.id, !server.token)}</div></div></div>`;
    return `<button class="text-button back-to-servers" data-action="navigate" data-id="servers">${icon('arrow', 16)}返回服务列表</button>` +
      heading(`<span class="service-icon service-icon-small ${color(server.color)}">${icon(proxy ? 'globe' : 'box', 24)}</span>${e(server.name)}${statusBadge(server.status)}`, e(server.description || (proxy ? '代理已有 MCP 服务，管理连接信息与访问凭证。' : '管理开放 API 与 MCP 客户端连接配置。')), button(configLabel(server), 'edit', 'sliders', '', server.id) + button('测试连接', 'test', 'play', 'primary', server.id, server.status === 'draft')) +
      `${server.draft ? `<div class="draft-note">${icon('info', 16)}有未发布更改，下方展示当前已发布配置。${button('继续编辑', 'edit', '', '', server.id)}</div>` : ''}
      <section class="panel server-connection ${state.connectionTab === 'json' ? 'server-connection-json' : ''}" aria-label="服务连接信息"><div class="connection-tabs" role="tablist" aria-label="连接信息视图"><button role="tab" aria-selected="${state.connectionTab === 'address'}" aria-controls="connection-view" data-action="connection-tab" data-id="address">地址与 Token</button><button role="tab" aria-selected="${state.connectionTab === 'json'}" aria-controls="connection-view" data-action="connection-tab" data-id="json">JSON 配置</button></div><div id="connection-view" role="tabpanel">${connectionContent}</div><div class="connection-meta"><span>服务标识 <code>${e(server.slug)}</code></span><span>版本 <strong>${server.status === 'draft' ? '待发布' : C.versionLabel(server)}</strong></span><span>${proxy ? 'MCP 代理 · Streamable HTTP' : `${selected.length} 个 API · ${sources.length} 份文档`}</span></div></section>
      ${proxy ? proxyDetailPanel(server) : `<section class="panel server-api-panel"><div class="section-heading"><div><h2>开放的 API</h2><p>客户端可以调用以下接口。点击接口可直接测试。</p></div>${button('管理 API', 'edit', 'sliders', '', server.id)}</div><div class="table-scroll"><table class="server-api-table"><thead><tr><th>方法</th><th>接口</th><th>API 文档</th><th></th></tr></thead><tbody>${selected.map(op => `<tr><td>${methodBadge(op.method)}</td><td><button class="api-name-button" data-action="operation" data-id="${e(op.id)}">${e(op.name)}</button><code>${e(op.path)}</code></td><td>${e(getDocument(op.documentId)?.name)}</td><td>${button('测试', 'operation', 'play', '', op.id)}</td></tr>`).join('')}</tbody></table></div>${selected.length ? '' : empty('还没有开放 API', '选择接口并发布服务后即可调用。', button('配置 API', 'edit', 'plus', '', server.id))}</section>`}`;
  }
  function proxyDetailPanel(server) {
    const names = server.proxy?.headerNames || [];
    return `<section class="panel server-api-panel proxy-detail-panel"><div class="section-heading"><div><h2>上游 MCP 服务</h2><p>工具、资源和提示词由上游提供，协议消息与流式响应直接透传。</p></div>${button('查看上游工具', 'test', 'play', '', server.id, server.status === 'draft')}</div><div class="proxy-route"><div><span>当前代理地址</span><code>${server.status === 'draft' ? '发布后生成' : e(endpoint(server))}</code></div><span class="proxy-route-arrow">${icon('arrow', 21)}</span><div><span>上游 MCP 地址</span><code>${e(server.proxy?.url || '尚未配置')}</code></div></div><div class="proxy-auth-summary"><span>上游认证 Header</span>${names.length ? names.map(name => `<code>${e(name)}</code>`).join('') : '<span class="muted">未配置</span>'}${button('编辑代理', 'edit', 'sliders', '', server.id)}</div><div class="proxy-feature-strip"><span>${icon('check', 15)}独立访问 Token</span><span>${icon('check', 15)}会话与流式响应透传</span><span>${icon('check', 15)}客户端 Header 优先</span></div></section>`;
  }
  function clientDraft(server) {
    return state.clientDrafts[server.id] ||= { text: '{}' };
  }
  function clientHeaderConfig(server) {
    return state.clientHeaderConfigs[server.id] || { headers: {}, conflicts: [] };
  }
  function ensureClientHeaders(serverId) {
    if (Object.prototype.hasOwnProperty.call(state.clientHeaderConfigs, serverId)) return Promise.resolve();
    if (state.clientHeaderErrors[serverId]) return Promise.resolve();
    if (state.clientHeaderLoads[serverId]) return state.clientHeaderLoads[serverId];
    const generation = state.clientHeaderGeneration;
    const request = api(`/api/servers/${encodeURIComponent(serverId)}/client-headers`).then(result => {
      if (generation === state.clientHeaderGeneration) state.clientHeaderConfigs[serverId] = result;
    }).catch(error => {
      if (generation === state.clientHeaderGeneration) state.clientHeaderErrors[serverId] = error.message;
    }).finally(() => {
      if (state.clientHeaderLoads[serverId] === request) delete state.clientHeaderLoads[serverId];
      if (generation === state.clientHeaderGeneration && state.page === 'server-detail' && state.selectedId === serverId && state.connectionTab === 'json') render();
    });
    state.clientHeaderLoads[serverId] = request;
    return request;
  }
  function clientConfigJSON(server) {
    return C.clientConfig(server, state.workspace.settings.endpointOrigin, clientDraft(server).text, clientHeaderConfig(server).headers);
  }
  function clientConfigPanel(server) {
    if (server.status === 'draft') return empty('发布后生成 JSON 配置', isProxy(server) ? '先配置上游 MCP 地址并发布此服务。' : '先选择 API 并发布此服务。', button('编辑并发布', 'edit', 'arrow', 'primary', server.id));
    const proxy = isProxy(server);
    const draft = clientDraft(server);
    const loaded = Object.prototype.hasOwnProperty.call(state.clientHeaderConfigs, server.id);
    const defaults = clientHeaderConfig(server);
    const loading = !loaded && !state.clientHeaderErrors[server.id];
    const loadingMessage = loading ? `<div class="client-header-status">${icon('loader', 15, 'spin')}正在读取${proxy ? '上游 MCP' : '所选 API 文档'}的凭证配置…</div>` : '';
    const fetchError = state.clientHeaderErrors[server.id] || '';
    let json = '', error = '';
    try { json = loaded ? C.clientConfig(server, state.workspace.settings.endpointOrigin, draft.text, defaults.headers) : ''; } catch (err) { error = err.message; }
    const conflictMessage = defaults.conflicts.length ? `<div class="client-header-conflicts" role="status">${icon('info', 15)}所选文档中以下 Header 配置不一致，未放入公共 JSON：<code>${e(defaults.conflicts.join('、'))}</code>。后台仍按各文档自己的凭证调用。</div>` : '';
    const emptyCredentials = loaded && !Object.keys(defaults.headers).length ? `<p class="form-hint">${proxy ? '未配置上游认证 Header。' : '没有可自动导出的共同 Header 凭证。Query、Body 等凭证继续由服务端按 API 文档配置使用。'}</p>` : '';
    const sourceLabel = proxy ? '上游 MCP' : 'API';
    const priorityDescription = proxy ? '后台保存的 MCP 地址和认证 Header 会自动加入右侧 JSON；自定义项同名时优先。' : '所选 API 文档的 Header 凭证会自动加入右侧 JSON；自定义项同名时优先。';
    const proxyHint = proxy ? 'base_url 指定完整上游 MCP 地址，可包含查询参数。会话标识、协议版本和流式消息由代理保留。' : 'Query、Body 等参数由服务端按各文档配置使用。可在这里设置 base_url 或 X-API2MCP-Base-URL 覆盖上游 API 地址；代理会识别该 Header，不会将其转发给上游。';
    const copyDisabled = !loaded || !!error || !!fetchError;
    return `<div class="client-config-layout"><section class="panel client-variables"><div class="section-heading"><div><h2>自定义 Header 覆盖项</h2><p>直接通过 MCP 请求 Header 透传，可覆盖自动带入的${sourceLabel}凭证。</p></div><span class="count-badge">优先级最高</span></div>${loadingMessage}${fetchError ? `<div class="form-error" role="alert">${e(fetchError)}${button('重试读取', 'retry-client-headers', '', '', server.id)}</div>` : ''}<div class="client-template-buttons">${button(proxy ? '上游 MCP 地址' : 'API 基础地址', 'client-template', '', '', 'base')}${button('Basic Auth', 'client-template', '', '', 'basic')}${button('Bearer Token', 'client-template', '', '', 'bearer')}${button('API Key', 'client-template', '', '', 'custom')}${button('清空覆盖项', 'client-template', '', '', 'empty')}</div><label class="field">可选 Header 覆盖 JSON<textarea id="client-credentials-json" data-field="client-credentials-json" aria-label="客户端自定义 Header JSON" spellcheck="false" rows="12" ${loaded ? '' : 'disabled'}>${e(draft.text)}</textarea></label><div class="credential-priority">${icon('layers', 16)}${priorityDescription}</div><p class="form-hint">${proxyHint}</p>${conflictMessage}${emptyCredentials}</section><section class="panel client-json-panel"><div class="section-heading"><div><h2>MCP JSON 配置</h2><p>地址包含 MCP 调用 Token；headers 包含${sourceLabel}凭证。</p></div>${button('复制 JSON', 'copy-client-json', 'copy', 'primary', server.id, copyDisabled)}</div><div class="client-json-toolbar"><span>mcpServers · ${e(server.slug)}${loaded ? ` · ${Object.keys(defaults.headers).length} 个自动配置 Header` : ''}</span></div><pre class="client-json-preview" tabindex="0" aria-label="MCP JSON 配置"><code id="client-json-code">${e(json)}</code></pre><div id="client-config-error" role="alert" class="form-error" ${error ? '' : 'hidden'}>${e(error)}</div><p class="form-hint">复制后的 JSON 会携带其中显示的认证值，请按客户端配置文件的访问权限妥善保管。</p></section></div>`;
  }
  function updateClientConfig() {
    const server = getServer(state.selectedId);
    if (!server || state.connectionTab !== 'json') return;
    const draft = clientDraft(server);
    const preview = document.getElementById('client-json-code');
    const error = document.getElementById('client-config-error');
    if (!preview || !error) return;
    let message = '';
    try { preview.textContent = clientConfigJSON(server); }
    catch (err) { message = err.message; preview.textContent = ''; }
    error.textContent = message; error.hidden = !message;
    document.querySelectorAll('[data-action="copy-client-json"]').forEach(button => { button.disabled = !!message || !Object.prototype.hasOwnProperty.call(state.clientHeaderConfigs, server.id); });
  }
  function clientTemplate(kind) {
    const server = getServer(state.selectedId);
    const doc = sourceDocuments(server)[0];
    const templates = {
      empty: {}, base: { base_url: isProxy(server) ? server.proxy?.url || '<上游 MCP 地址>' : doc?.baseUrl || '<上游 API 基础地址>' },
      basic: { Authorization: 'Basic <base64(username:password)>' },
      bearer: { Authorization: 'Bearer <上游 Token>' },
      custom: { 'X-API-Key': '<你的 API Key>' },
    };
    clientDraft(server).text = JSON.stringify(templates[kind] || {}, null, 2); render();
  }

  function overviewPage() {
    const w = state.workspace;
    const stats = [
      ['MCP Server', w.servers.length, `${w.servers.filter(s => s.status === 'running').length} 个已发布并启用`, 'box', 'blue'],
      ['API 文档', w.documents.length, `${allOperations().length} 个可用接口`, 'code', 'purple'],
      ['调用记录', w.logs.length, '最近 1000 条工具调用与连接记录', 'activity', 'green'],
    ];
    return heading('工作空间概览', '把已有 API，变成 AI 可以调用的工具。', button('创建 MCP Server', 'create', 'plus', 'primary')) +
      `<div class="overview-stats">${stats.map(([label, value, detail, glyph, tone]) => `<div class="panel stat-card"><div><span>${label}</span><strong>${value}</strong><small>${detail}</small></div><div class="stat-icon ${tone}">${icon(glyph, 24)}</div></div>`).join('')}</div>
      <section class="panel getting-started"><div class="section-heading"><div><h2>从文档到工具，只需三步</h2><p>保留 API 的现有实现，用 MCP 连接你的 AI 客户端。</p></div><span class="tiny-badge">开始使用</span></div><div class="getting-started-steps">${[['import', 'upload', '导入 API 文档', '上传文件、粘贴内容或填写 URL'], ['create', 'layers', '选择需要的 API', '自由组合不同文档里的接口'], ['create', 'globe', '发布 MCP 地址', '每个服务拥有独立地址与 Token']].map(([action, glyph, label, desc]) => `<button data-action="${action}"><span class="step-icon">${icon(glyph, 21)}</span><div><strong>${label}</strong><small>${desc}</small></div>${icon('chevron')}</button>`).join('')}</div></section>
      <section class="panel overview-servers"><div class="section-heading"><h2>我的 MCP Server</h2><span class="muted small">${w.servers.length} 个服务</span></div>${w.servers.length ? w.servers.map(server => `<button class="overview-server-row" data-action="show-server" data-id="${e(server.id)}"><span class="service-icon service-icon-small ${color(server.color)}">${icon('box', 22)}</span><span class="overview-server-name"><strong>${e(server.name)}</strong><code>${server.status === 'draft' ? '待发布' : e(endpoint(server))}</code></span>${statusBadge(server.status)}${icon('arrow')}</button>`).join('') : empty('创建第一个 MCP Server', '选择 API，发布一个属于你的调用地址。', button('创建服务', 'create', 'plus', 'primary'))}</section>`;
  }

  function credentialStatus(doc) {
    const credential = state.credentials[doc.id];
    const label = credential?.configured ? '已配置凭证' : credential?.entries?.length ? '参数未启用' : '未配置凭证';
    return `<span class="credential-status ${credential?.configured ? 'configured' : ''}">${icon('shield', 14)}${label}</span>`;
  }
  function documentMenu(doc) {
    return `<details class="more-menu"><summary aria-label="${e(doc.name)}的更多操作" title="更多操作">${icon('more')}</summary><div class="menu-popover">${button('更新文档', 'update-document', 'upload', '', doc.id)}<div class="menu-divider"></div>${button('删除文档', 'delete-document', 'trash', 'destructive-text', doc.id)}</div></details>`;
  }
  function documentsPage() {
    const all = state.workspace.documents;
    const docs = all.filter(doc => matches(`${doc.name} ${doc.filename} ${doc.baseUrl}`, state.documentQuery) && (state.documentCredential === 'all' || Boolean(state.credentials[doc.id]?.configured) === (state.documentCredential === 'configured')));
    const configured = all.filter(doc => state.credentials[doc.id]?.configured).length;
    return heading('API 文档', '管理 API 定义与凭证，选择接口组合成 MCP 服务。', button('导入 API 文档', 'import', 'upload', 'primary'), `${all.length} 份文档`) +
      `<div class="server-summary-strip document-summary-strip"><span><strong>${allOperations().length}</strong> 个 API</span><span>${icon('shield', 15)}<strong>${configured}</strong> 份已配置凭证</span><span class="summary-protocol">OpenAPI 3.x / Swagger 2.0</span></div>
      <section class="panel document-directory" aria-label="API 文档列表"><div class="list-toolbar">${search('documentQuery', '搜索文档名称、文件名、API 地址', '搜索 API 文档', state.documentQuery)}<select data-field="documentCredential" aria-label="筛选文档凭证状态">${[['all', '全部凭证状态'], ['configured', '已配置凭证'], ['unconfigured', '未配置凭证']].map(([value, label]) => `<option value="${value}" ${state.documentCredential === value ? 'selected' : ''}>${label}</option>`).join('')}</select></div>
      ${docs.length ? `<div class="document-list-head" aria-hidden="true"><span>文档</span><span>API</span><span>凭证</span><span>服务引用</span><span>操作</span></div><div class="document-directory-list">${docs.map(doc => `<article class="document-directory-row"><div class="document-row-main"><span class="document-file-icon ${doc.filename.toLowerCase().endsWith('.json') ? 'purple' : 'green'}">${icon('file', 23)}</span><div><button class="document-title" data-action="select-document" data-id="${e(doc.id)}">${e(doc.name)}</button><p>${e(doc.filename)}<span>v${e(doc.version)}</span></p><code title="${e(doc.baseUrl)}">${e(doc.baseUrl || '尚未设置 API 基础地址')}</code></div></div><div class="document-row-count"><strong>${doc.operations.length}</strong><span>个 API</span></div><div class="document-row-credential">${credentialStatus(doc)}</div><div class="document-row-usage"><strong>${documentUsage(doc.id).length}</strong><span>个服务</span></div><div class="document-row-actions">${button('查看详情', 'select-document', 'arrow', 'detail-link', doc.id)}${documentMenu(doc)}</div></article>`).join('')}</div>` : empty(all.length ? '没有匹配的文档' : '导入第一份 API 文档', all.length ? '试试其他关键词，或清除凭证筛选条件。' : '上传 OpenAPI 文件、粘贴内容，或通过 URL 导入。', all.length ? button('清除筛选', 'clear-document-filters') : button('导入 API 文档', 'import', 'upload', 'primary'))}
      <div class="list-footer"><span>共 ${docs.length} 份文档</span><span>进入详情配置凭证与测试接口</span></div></section>`;
  }
  function documentDetailPage() {
    const doc = getDocument(state.selectedDocumentId);
    if (!doc) return empty('文档不存在', '该文档可能已被删除。', button('返回文档列表', 'navigate', 'arrow', '', 'documents'));
    const methods = sortedRequestMethods(doc.operations.map(op => op.method));
    const operations = doc.operations.filter(op => (state.apiMethod === 'all' || op.method.toUpperCase() === state.apiMethod) && matches(`${op.name} ${op.path} ${op.method} ${op.tag || ''}`, state.apiQuery));
    const usage = documentUsage(doc.id);
    const imported = new Date(doc.importedAt);
    return `<button class="text-button back-to-servers" data-action="navigate" data-id="documents">${icon('arrow', 16)}返回文档列表</button>` +
      heading(`<span class="document-file-icon document-detail-icon green">${icon('file', 24)}</span>${e(doc.name)}<span class="document-chip">v${e(doc.version)}</span>`, e(doc.filename), button('更新文档', 'update-document', 'upload', '', doc.id) + button('配置凭证', 'credentials', 'sliders', 'primary', doc.id)) +
      `<section class="panel document-info" aria-label="文档信息"><div class="document-info-main"><div><span class="detail-label">API 基础地址</span><div class="document-address">${icon('globe', 17)}<code>${e(doc.baseUrl || '尚未设置，请在配置凭证中填写')}</code>${iconButton('复制 API 基础地址', 'copy', 'copy-document-url', doc.id, !doc.baseUrl)}</div></div><div class="document-info-credential"><span class="detail-label">请求凭证</span>${credentialStatus(doc)}</div></div><div class="connection-meta"><span>定义 <strong>${doc.specVersion === '2.0' ? 'Swagger' : 'OpenAPI'} ${e(doc.specVersion)}</strong></span><span><strong>${doc.operations.length}</strong> 个 API</span><span>最近导入 <time datetime="${e(doc.importedAt)}">${Number.isNaN(imported.getTime()) ? '—' : e(imported.toLocaleString('zh-CN', { hour12: false }))}</time></span></div></section>
      <section class="panel document-api-panel" aria-label="文档 API"><div class="section-heading"><div><h2>API 接口 <span class="count-badge">${doc.operations.length}</span></h2><p>按请求方式查找接口，点击测试可直接调用。</p></div>${button('创建 MCP 服务', 'create-from-document', 'plus', '', doc.id)}</div><div class="document-api-toolbar">${search('apiQuery', '搜索接口名称、路径、标签', '搜索文档 API', state.apiQuery)}<span class="muted small">${operations.length} 个匹配接口</span></div><div class="method-filters" role="group" aria-label="按请求方式筛选接口">${[['all', '全部', doc.operations.length], ...methods.map(method => [method, method, doc.operations.filter(op => op.method.toUpperCase() === method).length])].map(([value, label, count]) => `<button type="button" data-action="filter-document-method" data-id="${e(value)}" aria-pressed="${state.apiMethod === value}">${e(label)}<span>${count}</span></button>`).join('')}</div>
      ${operations.length ? `<div class="table-scroll"><table class="server-api-table document-api-table"><thead><tr><th scope="col">方法</th><th scope="col">接口 / 路径</th><th scope="col">标签</th><th scope="col"><span class="visually-hidden">操作</span></th></tr></thead>${methods.filter(method => operations.some(op => op.method.toUpperCase() === method)).map(method => { const group = operations.filter(op => op.method.toUpperCase() === method); return `<tbody><tr class="document-method-heading"><th scope="rowgroup" colspan="${mobileViewport.matches ? 3 : 4}">${methodBadge(method)}<span>${group.length} 个接口</span></th></tr>${group.map(op => `<tr><td>${methodBadge(op.method)}</td><td><button class="api-name-button" data-action="operation" data-id="${e(op.id)}">${e(op.name)}</button><code>${e(op.path)}</code></td><td>${e(op.tag || '—')}</td><td>${button('测试', 'operation', 'play', '', op.id)}</td></tr>`).join('')}</tbody>`; }).join('')}</table></div>` : empty('没有匹配的接口', '清除搜索词或切换请求方式。', button('清除筛选', 'clear-api-filters'))}</section>
      <section class="panel document-services"><div class="section-heading"><div><h2>引用此文档的服务 <span class="count-badge">${usage.length}</span></h2><p>包含已发布配置和未发布的草稿。</p></div>${documentMenu(doc)}</div>${usage.length ? `<div class="document-service-links">${usage.map(server => `<button data-action="show-server" data-id="${e(server.id)}"><span class="service-icon service-icon-small ${color(server.color)}">${icon('box', 20)}</span><span><strong>${e(server.name)}</strong><small>${server.operationIds.filter(id => doc.operations.some(op => op.id === id)).length} 个${server.status === 'draft' ? '草稿' : '已发布'} API${server.draft ? ' · 有未发布更改' : ''}</small></span>${statusBadge(server.status)}${icon('chevron', 16)}</button>`).join('')}</div>` : '<p class="document-no-services">还没有服务引用这份文档，可选择其中的 API 创建 MCP 服务。</p>'}</section>`;
  }

  function logsPage() {
    const logs = state.workspace.logs.filter(log => matches(`${log.serverName} ${log.action}`, state.logQuery) && (state.logStatus === 'all' || String(log.success) === state.logStatus));
    return heading('调用日志', '查看 API 测试、工具调用与连接记录，自动保留最近 1000 条。', button('清空记录', 'clear-logs', 'trash', '', '', !state.workspace.logs.length)) +
      `<section class="panel logs-panel"><div class="list-toolbar">${search('logQuery', '搜索服务名称、操作', '搜索调用日志', state.logQuery)}<select data-field="logStatus" aria-label="筛选调用结果">${[['all', '全部结果'], ['true', '成功'], ['false', '失败']].map(([value, label]) => `<option value="${value}" ${state.logStatus === value ? 'selected' : ''}>${label}</option>`).join('')}</select></div>${logs.length ? `<div class="table-scroll"><table class="logs-table"><thead><tr><th>时间</th><th>服务名称</th><th>操作</th><th>结果</th><th>耗时</th></tr></thead><tbody>${logs.map(log => `<tr><td>${e(new Date(log.createdAt).toLocaleString('zh-CN', { hour12: false }))}</td><td>${e(log.serverName)}</td><td>${e(log.action)}${log.status ? `<span class="tiny-badge">HTTP ${log.status}</span>` : ''}</td><td><span class="log-status ${log.success ? 'success' : 'failure'}">${log.success ? '成功' : '失败'}</span></td><td class="mono">${log.duration} ms</td></tr>`).join('')}</tbody></table></div>` : empty(state.workspace.logs.length ? '没有匹配的记录' : '还没有调用记录', 'API 测试、客户端工具调用与后台连接测试会自动记录在这里。')}</section>`;
  }

  function modal(title, subtitle, body, footer, wide = false) {
    return `<div class="modal-backdrop"><section role="dialog" aria-modal="true" aria-labelledby="dialog-title" tabindex="-1" class="modal ${wide ? 'modal-wide' : ''}"><header class="modal-header"><div><h2 id="dialog-title">${e(title)}</h2>${subtitle ? `<p>${e(subtitle)}</p>` : ''}</div>${iconButton('关闭弹窗', 'x', 'close-dialog')}</header><div class="modal-body">${body}</div>${footer ? `<footer class="modal-footer">${footer}</footer>` : ''}</section></div>`;
  }
  function openDialog(dialog) {
    modalReturnFocus = document.activeElement;
    const action = modalReturnFocus?.dataset.action;
    modalReturnSelector = action ? `[data-action="${CSS.escape(action)}"][data-id="${CSS.escape(modalReturnFocus.dataset.id || '')}"]` : '';
    state.dialog = dialog;
    if (dialog.kind === 'editor') {
      const server = getServer(dialog.id);
      dialog.draft = server ? structuredClone(server.draft || { type: server.type || 'api', name: server.name, slug: server.slug, description: server.description, operationIds: server.operationIds, color: server.color, proxy: server.proxy }) : { type: dialog.serverType || 'api', name: '', slug: `service-${C.uid().slice(0, 6)}`, description: '', operationIds: [], color: 'blue' };
      dialog.draft.type ||= 'api';
      dialog.draft.proxy ||= { url: '', headers: {} };
      dialog.proxyHeadersText = JSON.stringify(dialog.draft.proxy.headers || {}, null, 2);
      dialog.loadingProxy = !!server && isProxy(dialog.draft);
      dialog.query = ''; dialog.documentFilter = 'all'; dialog.methodFilter = 'all';
      if (!server && dialog.sourceDocumentId) {
        const doc = getDocument(dialog.sourceDocumentId);
        if (doc) { dialog.documentFilter = doc.id; dialog.draft.name = `${doc.name.slice(0, 34)} MCP`; }
      }
    }
    if (dialog.kind === 'import') { const source = getDocument(dialog.id)?.source || ''; Object.assign(dialog, { tab: source.startsWith('http') ? 'url' : 'file', fileContent: '', pasteContent: '', filename: 'openapi.json', url: source.startsWith('http') ? source : '', busy: false }); }
    if (dialog.kind === 'credentials') {
      const credential = state.credentials[dialog.id];
      Object.assign(dialog, {
        baseUrl: getDocument(dialog.id).baseUrl, authKind: credential?.kind || 'none', savedKind: credential?.kind || 'none',
        header: credential?.header || 'X-API-Key', value: '', username: '', emptyPassword: credential?.kind === 'basic' && credential?.emptyValue || false, bodyFormat: credential?.bodyFormat || 'document',
        valueConfigured: credential?.valueConfigured || false, usernameConfigured: credential?.usernameConfigured || false,
        entries: (credential?.entries || []).map(entry => ({ ...entry, value: '', key: C.uid(), empty: entry.emptyValue || false })),
        bulkOpen: false, bulkLocation: 'header', bulkText: '',
      });
    }
    if (dialog.kind === 'test') { dialog.step = 0; dialog.finished = false; }
    if (dialog.kind === 'operation') Object.assign(dialog, { loading: true, requestTab: 'params', responseTab: 'body', rows: [], formRows: [], bodyText: '', bodyFormat: 'configured' });
    app.inert = true;
    document.body.style.overflow = 'hidden';
    renderDialog();
    if (dialog.kind === 'editor' && dialog.loadingProxy) loadProxyEditor(dialog);
    if (dialog.kind === 'test') startTest(dialog);
    if (dialog.kind === 'operation') loadOperationTest(dialog);
  }
  function closeDialog() {
    if (state.dialog?.busy || state.dialog?.callBusy) return;
    fetchController?.abort(); fetchController = undefined;
    modalTimers.forEach(clearTimeout); modalTimers = [];
    state.dialog = null;
    dialogs.innerHTML = '';
    app.inert = false;
    document.body.style.overflow = '';
    const target = modalReturnFocus?.isConnected ? modalReturnFocus : modalReturnSelector && document.querySelector(modalReturnSelector);
    target?.focus({ preventScroll: true });
  }
  function renderDialog(focus = true) {
    const d = state.dialog;
    if (!d) return;
    const scrollTop = dialogs.querySelector('.modal-body')?.scrollTop || 0;
    const renderers = { editor: editorDialog, import: importDialog, credentials: credentialDialog, operation: operationDialog, test: testDialog };
    const html = renderers[d.kind] ? renderers[d.kind](d) : confirmDialog(d);
    preserveFocus(() => { dialogs.innerHTML = html; }, dialogs);
    const modalBody = dialogs.querySelector('.modal-body');
    if (modalBody) modalBody.scrollTop = scrollTop;
    if (d.kind === 'editor') updateEditor();
    if (focus) {
      queueMicrotask(() => (dialogs.querySelector('[data-autofocus]') || dialogs.querySelector('button'))?.focus({ preventScroll: true }));
    }
  }
  function showError(message, id = 'dialog-error') {
    const error = document.getElementById(id);
    if (error) { error.hidden = false; error.textContent = message; error.scrollIntoView({ block: 'nearest' }); }
  }
  function clearError(id = 'dialog-error') {
    const error = document.getElementById(id);
    if (error) { error.hidden = true; error.textContent = ''; }
  }
  const errorSlot = '<div id="dialog-error" hidden role="alert" class="form-error"></div>';

  async function loadProxyEditor(d) {
    d.loadingProxy = true; d.proxyLoadError = ''; renderDialog(false);
    try {
      const config = await api(`/api/servers/${d.id}/proxy-config`);
      if (state.dialog !== d) return;
      d.draft.proxy = config;
      d.proxyHeadersText = JSON.stringify(config.headers || {}, null, 2);
    } catch (error) { d.proxyLoadError = error.message; }
    finally { d.loadingProxy = false; if (state.dialog === d) renderDialog(false); }
  }

  function editorDialog(d) {
    const server = getServer(d.id), draft = d.draft;
    const published = server && server.status !== 'draft';
    const proxy = isProxy(draft);
    const blocked = !!d.loadingProxy || !!d.proxyLoadError;
    const methods = sortedRequestMethods(allOperations().map(op => op.method));
    const body = `${d.loadingProxy ? '<div class="client-header-status" role="status">正在读取代理配置…</div>' : ''}${d.proxyLoadError ? `<div class="form-error" role="alert">${e(d.proxyLoadError)}${button('重新读取', 'reload-proxy-config')}</div>` : ''}<fieldset class="editor-fields" ${blocked ? 'disabled' : ''}><div class="service-type-options" aria-label="服务类型">${[['api', 'code', 'API 转换', '从 API 文档选择接口'], ['proxy', 'globe', 'MCP 代理', '连接已有 MCP 服务']].map(([type, glyph, label, hint]) => `<button data-action="editor-type" data-id="${type}" aria-pressed="${draft.type === type}" ${published ? 'disabled' : ''}>${icon(glyph, 22)}<span><strong>${label}</strong><small>${hint}</small></span>${draft.type === type ? icon('circleCheck', 18) : ''}</button>`).join('')}</div><div class="editor-steps"><span><b>1</b>服务信息</span>${icon('chevron', 15)}<span><b>2</b>${proxy ? '配置上游 MCP' : '选择 API'}</span>${icon('chevron', 15)}<span><b>3</b>发布调用地址</span></div>
      <div class="form-grid"><label class="field">服务名称 <span class="required">*</span><input id="draft-name" data-autofocus data-field="draft.name" value="${e(draft.name)}" placeholder="例如：客户服务 MCP" maxlength="40" required></label><label class="field">服务标识 <span class="required">*</span><input id="draft-slug" class="mono" data-field="draft.slug" value="${e(draft.slug)}" ${published ? 'readonly' : ''} maxlength="48" placeholder="customer-service" required><small>${published ? '已发布的标识固定，更新配置后地址不变。' : '使用小写字母、数字和中划线，作为地址中的唯一标识。'}</small></label><label class="field field-full">服务描述<input id="draft-description" data-field="draft.description" value="${e(draft.description)}" placeholder="简单描述这个服务的用途" maxlength="200"></label></div>
      <div class="editor-section-heading"><h3>${proxy ? '上游 MCP 配置' : '选择 API <span id="editor-count" class="count-badge"></span>'}</h3><div class="color-options" aria-label="服务图标颜色">${['green', 'purple', 'blue'].map(tone => `<button type="button" class="color-option ${tone}" aria-label="${{ green: '绿色', purple: '紫色', blue: '蓝色' }[tone]}图标" aria-pressed="${draft.color === tone}" data-action="editor-color" data-id="${tone}">${draft.color === tone ? icon('check', 13) : ''}</button>`).join('')}</div></div>
      ${proxy ? `<div class="proxy-editor"><label class="field">上游 MCP 地址 <span class="required">*</span><input id="proxy-url" class="mono" type="url" data-field="proxy-url" value="${e(draft.proxy.url)}" placeholder="https://mcp.example.com/mcp" maxlength="4096"><small>填写完整的 Streamable HTTP 地址，可包含上游要求的查询参数。</small></label><div class="label-with-action"><label for="proxy-headers">上游认证 Header（可选）</label><div class="button-group">${button('Bearer Token', 'proxy-header-template', '', '', 'bearer')}${button('API Key', 'proxy-header-template', '', '', 'key')}</div></div><textarea id="proxy-headers" class="mono proxy-headers-input" data-field="proxy-headers" spellcheck="false" rows="6" placeholder='{"Authorization": "Bearer 上游Token"}'>${e(d.proxyHeadersText)}</textarea><p class="form-hint">使用 JSON 对象配置 Authorization、X-API-Key 等。客户端请求中的同名 Header 优先。</p><div class="proxy-feature-strip"><span>${icon('check', 15)}无需导入 API 文档</span><span>${icon('check', 15)}保留上游 MCP 能力</span><span>${icon('check', 15)}支持流式响应</span></div></div>` : `<div class="table-toolbar editor-api-filters">${search('editor-query', '搜索接口名称、路径', '搜索可选 API', d.query)}<select data-field="editor-method" aria-label="按 HTTP 请求方式筛选">${[['all', '全部请求方式'], ...methods.map(method => [method, method])].map(([value, label]) => `<option value="${e(value)}" ${d.methodFilter === value ? 'selected' : ''}>${e(label)}</option>`).join('')}</select><select data-field="editor-document" aria-label="筛选 API 文档"><option value="all">全部文档</option>${state.workspace.documents.map(doc => `<option value="${e(doc.id)}" ${d.documentFilter === doc.id ? 'selected' : ''}>${e(doc.name)}</option>`).join('')}</select></div><div class="api-selection-table" id="editor-table"></div>`}
          <div class="endpoint-preview">${icon('arrow', 16)}<span>发布地址</span><code id="editor-endpoint"></code></div>${published ? '<p class="form-hint">保存草稿保留当前发布版本；发布更新后新配置生效，地址和 Token 不变。</p>' : ''}</fieldset>${errorSlot}`;
    return modal(server ? '配置 MCP Server' : proxy ? '添加 MCP 代理' : '创建 MCP Server', proxy ? '填写已有 MCP 地址，发布一个独立的代理访问地址。' : '选择需要开放的 API，为它们生成一个独立的调用地址。', body, `<span class="footer-summary" id="editor-summary"></span><div class="button-group">${button('保存草稿', 'save-draft', '', '', '', blocked)}${button(published ? '发布更新' : '发布并生成地址', 'publish', 'upload', 'primary', '', blocked)}</div>`, true);
  }
  function sortedRequestMethods(methods) {
    const order = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'TRACE', 'CONNECT'];
    return [...new Set(methods.map(method => method.toUpperCase()))].sort((a, b) => {
      const ai = order.indexOf(a), bi = order.indexOf(b);
      return (ai < 0 ? order.length : ai) - (bi < 0 ? order.length : bi) || a.localeCompare(b);
    });
  }
  function visibleEditorOperations() {
    const d = state.dialog;
    return allOperations().filter(op => (d.documentFilter === 'all' || op.documentId === d.documentFilter) && (d.methodFilter === 'all' || op.method.toUpperCase() === d.methodFilter) && matches(`${op.name} ${op.path} ${op.method} ${op.operationId}`, d.query));
  }
  function updateEditor(rows = true) {
    const d = state.dialog;
    if (d?.kind !== 'editor') return;
    document.getElementById('editor-endpoint').textContent = endpoint(d.draft);
    if (isProxy(d.draft)) {
      document.getElementById('editor-summary').textContent = 'MCP 代理 · Streamable HTTP';
      return;
    }
    const selected = new Set(d.draft.operationIds);
    const ops = visibleEditorOperations();
    const selectedVisible = ops.filter(op => selected.has(op.id)).length;
    document.getElementById('editor-count').textContent = `已选 ${selected.size} 个`;
    document.getElementById('editor-summary').innerHTML = `已选 <strong>${selected.size}</strong> 个 API · 来自 ${new Set(allOperations().filter(op => selected.has(op.id)).map(op => op.documentId)).size} 份文档`;
    document.getElementById('editor-endpoint').textContent = endpoint(d.draft);
    if (rows) {
      const table = document.getElementById('editor-table');
      const scroll = table.scrollTop;
      const focused = table.contains(document.activeElement) ? document.activeElement.id : '';
      const methods = sortedRequestMethods(ops.map(op => op.method));
      let index = 0;
      const groups = methods.map(method => {
        const group = ops.filter(op => op.method.toUpperCase() === method);
        const groupSelected = group.filter(op => selected.has(op.id)).length;
        const controlId = `editor-select-${method.toLowerCase()}`;
        const rows = group.map(op => {
          const rowIndex = index++;
          return `<tr class="${selected.has(op.id) ? 'checked-row' : ''}" data-action="toggle-operation" data-id="${e(op.id)}"><td class="checkbox-cell"><input type="checkbox" id="api-check-${rowIndex}" aria-label="选择 ${e(op.name)}（${e(method)}，${e(getDocument(op.documentId)?.name)}）" data-field="editor-operation" data-id="${e(op.id)}" ${selected.has(op.id) ? 'checked' : ''}></td><td>${methodBadge(method)}</td><td><span class="operation-name">${e(op.name)}</span><code>${e(op.path)}</code></td><td class="source-cell">${e(getDocument(op.documentId)?.name)}</td></tr>`;
        }).join('');
        return `<tr class="method-group-row"><td colspan="4"><label for="${controlId}"><input type="checkbox" id="${controlId}" aria-label="全选 ${method} 接口" data-field="editor-select-method" data-id="${method}" ${group.length && groupSelected === group.length ? 'checked' : ''}><strong>${method} 请求</strong><span>${groupSelected} / ${group.length} 已选</span></label></td></tr>${rows}`;
      }).join('');
      table.innerHTML = `<table><thead><tr><th class="checkbox-cell"><input type="checkbox" id="editor-select-all" aria-label="全选当前筛选结果" data-field="editor-select-all" ${!ops.length ? 'disabled' : ''} ${ops.length && selectedVisible === ops.length ? 'checked' : ''}></th><th>方法</th><th>接口</th><th>来源</th></tr></thead><tbody>${groups}</tbody></table>${!ops.length ? `<div class="table-empty">${state.workspace.documents.length ? '没有找到匹配的 API' : '请先在 API 文档页面导入文档'}</div>` : ''}`;
      table.scrollTop = scroll;
      if (focused) document.getElementById(focused)?.focus({ preventScroll: true });
    }
    document.getElementById('editor-select-all').indeterminate = selectedVisible > 0 && selectedVisible < ops.length;
    document.querySelectorAll('[data-field="editor-select-method"]').forEach(control => {
      const group = ops.filter(op => op.method.toUpperCase() === control.dataset.id);
      const count = group.filter(op => selected.has(op.id)).length;
      control.indeterminate = count > 0 && count < group.length;
    });
  }
  function toggleOperation(id) {
    const d = state.dialog;
    if (d?.kind !== 'editor') return;
    d.draft.operationIds = d.draft.operationIds.includes(id) ? d.draft.operationIds.filter(item => item !== id) : [...d.draft.operationIds, id];
    clearError(); updateEditor();
  }
  async function saveServer(mode) {
    const d = state.dialog;
    if (d.busy || d.loadingProxy || d.proxyLoadError) return;
    dialogBusy(d, true);
    try {
      if (isProxy(d.draft)) d.draft.proxy.headers = C.clientRequestHeaders(d.proxyHeadersText, true);
      const existing = getServer(d.id);
      const result = await api(`/api/servers${existing ? '/' + existing.id : ''}`, existing ? 'PUT' : 'POST', { draft: d.draft, mode });
      await refresh(); invalidateClientHeaders(); dialogBusy(d, false);
      if (state.dialog === d) closeDialog();
      showServer(result.id);
      notify(mode === 'draft' ? '草稿已保存' : existing && existing.status !== 'draft' ? '服务已更新，调用地址保持不变' : '服务已发布，MCP 调用地址已可用');
    } catch (error) { showError(error.message); }
    finally { dialogBusy(d, false); }
  }

  function importDialog(d) {
    const tabs = `<div class="segmented-tabs" role="tablist" aria-label="导入方式">${[['file', 'code', '上传文件'], ['paste', 'clipboard', '粘贴内容'], ['url', 'globe', '文档 URL']].map(([id, glyph, label]) => `<button role="tab" aria-selected="${d.tab === id}" data-action="import-tab" data-id="${id}">${icon(glyph, 16)}${label}</button>`).join('')}</div>`;
    let body;
    if (d.tab === 'file') body = `<input id="spec-file" type="file" accept=".json,.yaml,.yml,application/json" class="visually-hidden" aria-label="选择 API 文档文件"><button class="upload-zone" data-action="pick-file"><div class="upload-icon">${icon('upload', 27)}</div><strong>${d.fileContent ? e(d.filename) : '点击上传，或将文档拖到这里'}</strong><span>JSON / YAML 格式，最大 2 MB</span>${d.fileContent ? `<span class="upload-loaded">${icon('circleCheck', 15)}文件已读取，可以开始导入</span>` : ''}</button>`;
    if (d.tab === 'paste') body = `<div class="label-with-action"><label for="spec-content">API 文档内容</label><button class="text-button" data-action="example-spec">填入示例文档</button></div><textarea id="spec-content" data-autofocus data-field="import-content" class="spec-textarea mono" spellcheck="false" placeholder="粘贴 OpenAPI JSON 或 YAML 文档…">${e(d.pasteContent)}</textarea>`;
    if (d.tab === 'url') body = `<div class="url-import"><label class="field">文档 URL<input id="spec-url" data-autofocus type="url" data-field="import-url" value="${e(d.url)}" placeholder="https://api.example.com/openapi.json"></label><p class="form-hint">由 Go 服务下载文档，无需浏览器跨域。可访问服务端网络可达的 HTTP / HTTPS 文档地址。</p></div>`;
    return modal(d.id ? '更新 API 文档' : '导入 API 文档', d.id ? '保留文档凭证与接口标识；更新会同步应用到引用此文档的 MCP 服务。' : '支持 OpenAPI 3.0 / 3.1、Swagger 2.0 的 JSON 和 YAML 文档。', tabs + body + errorSlot, `<span class="muted small">导入后即可勾选 API 创建服务</span><div class="button-group">${button('取消', 'close-dialog')}<button class="button primary" data-action="submit-import" ${d.busy ? 'disabled' : ''}>${icon(d.busy ? 'loader' : 'upload', 16, d.busy ? 'spin' : '')}${d.busy ? '正在导入…' : '解析并导入'}</button></div>`);
  }
  async function loadFile(file) {
    const d = state.dialog;
    if (!file || d?.kind !== 'import') return;
    if (file.size > 2_000_000) { showError('请选择小于 2 MB 的文档。'); return; }
    try {
      const content = await file.text();
      if (state.dialog !== d) return;
      d.fileContent = content; d.filename = file.name;
      renderDialog(false);
    } catch { showError('文件读取失败，请重试。'); }
  }
  async function submitImport() {
    const d = state.dialog;
    if (d.busy) return;
    d.busy = true; clearError();
    const submit = dialogs.querySelector('[data-action="submit-import"]');
    submit.disabled = true; submit.textContent = '正在导入…';
    try {
      const content = d.tab === 'file' ? d.fileContent : d.pasteContent;
      const input = d.tab === 'url' ? { url: d.url.trim() } : { content, filename: d.tab === 'file' ? d.filename : 'pasted-openapi.yaml' };
      const doc = await api(`/api/documents${d.id ? '/' + d.id : ''}`, d.id ? 'PUT' : 'POST', input);
      await refresh();
      state.selectedDocumentId = doc.id; state.documentQuery = ''; state.apiQuery = '';
      if (state.dialog === d) { d.busy = false; closeDialog(); showDocument(doc.id); }
      notify(`已${d.id ? '更新' : '导入'}「${doc.name}」，解析出 ${doc.operations.length} 个 API`);
    } catch (error) { if (state.dialog === d) showError(error.message); }
    finally { d.busy = false; if (state.dialog === d) { submit.disabled = false; submit.innerHTML = `${icon('upload', 16)}解析并导入`; } }
  }

  function credentialDialog(d) {
    const saved = d.savedKind === d.authKind && d.valueConfigured;
    const secretType = d.showValues ? 'text' : 'password';
    const body = `<div class="credential-lead">${icon('sliders', 19)}<span>按 API 的要求组合认证方式与请求参数，一次配置用于整份文档。</span></div>
      <label class="field" for="credential-base">API 基础地址<input id="credential-base" data-autofocus data-field="credential-base" value="${e(d.baseUrl)}" placeholder="https://api.example.com/v1"></label>
      <section class="credential-section"><div class="credential-section-title"><h3>预设认证</h3><span>可选，与下面的参数组合使用</span></div><div class="form-grid">
      <label class="field" for="credential-kind">认证方式<select id="credential-kind" data-field="credential-kind">${[['none', '不使用预设认证'], ['bearer', 'Bearer Token'], ['apiKey', 'API Key / 自定义认证 Header'], ['basic', 'Basic Auth（用户名 + 密码）']].map(([value, label]) => `<option value="${value}" ${d.authKind === value ? 'selected' : ''}>${label}</option>`).join('')}</select></label>
      ${d.authKind === 'apiKey' ? `<label class="field" for="credential-header">认证 Header 名称<input id="credential-header" data-field="credential-header" value="${e(d.header)}" placeholder="X-API-Key 或 Authorization"></label>` : ''}
      ${d.authKind === 'basic' ? `<label class="field" for="credential-username">用户名<input id="credential-username" data-field="credential-username" value="${e(d.username)}" autocomplete="off" placeholder="${d.savedKind === 'basic' && d.usernameConfigured ? '已保存，留空保留原用户名' : '输入用户名'}"></label>` : ''}
      ${d.authKind !== 'none' ? `<label class="field ${d.authKind === 'basic' ? '' : 'field-full'}" for="credential-value">${d.authKind === 'basic' ? '密码' : d.authKind === 'bearer' ? 'Token' : '认证 Header 值'}<input id="credential-value" data-field="credential-value" type="${secretType}" value="${e(d.emptyPassword ? '' : d.value)}" autocomplete="off" ${d.emptyPassword ? 'disabled' : ''} placeholder="${saved ? '已保存，留空保留原凭证' : '输入凭证'}">${d.authKind === 'bearer' ? '<small>系统自动添加 Bearer 前缀，也可在自定义 Header 中填写其他 Authorization 格式。</small>' : ''}</label>` : ''}
      ${d.authKind === 'basic' ? `<label class="credential-check field-full"><input id="credential-empty-password" type="checkbox" data-field="credential-empty-password" ${d.emptyPassword ? 'checked' : ''}>使用空密码</label>` : ''}</div></section>
      <section class="credential-section"><div class="credential-section-title"><h3>自定义请求参数 <span class="count-badge">${d.entries.filter(entry => entry.enabled).length} 项启用</span></h3><button id="credential-show-values" class="text-button" data-action="credential-show-values">${icon(d.showValues ? 'eyeOff' : 'eye', 14)}${d.showValues ? '隐藏本次输入' : '显示本次输入'}</button></div>
      <p class="credential-help">Header、Query、Body 和 Cookie 可以同时配置。Body 支持嵌套字段与 JSON / 表单；已保存的值留空即可保留。</p>
      <label class="credential-body-format" for="credential-body-format"><span>Body 发送格式</span><select id="credential-body-format" data-field="credential-body-format">${[['document', '按 API 文档自动选择'], ['json', 'JSON · application/json'], ['form', '表单 · application/x-www-form-urlencoded']].map(([value, label]) => `<option value="${value}" ${d.bodyFormat === value ? 'selected' : ''}>${label}</option>`).join('')}</select></label>
      ${d.entries.length ? `<div class="credential-column-labels"><span>位置</span><span>名称 / Body 路径</span><span>值</span><span>值类型</span><span>操作</span></div><div class="credential-entries">${d.entries.map((entry, index) => credentialEntryRow(d, entry, index)).join('')}</div>` : '<div class="credential-empty">添加 API 需要的 app_id、token、签名或其他固定参数。</div>'}
      <div class="credential-add-actions">${[['header', 'Header'], ['query', 'Query'], ['body', 'Body'], ['cookie', 'Cookie']].map(([location, label]) => button(`添加 ${label}`, 'credential-add', 'plus', '', location)).join('')}<button id="credential-bulk-toggle" class="text-button" data-action="credential-bulk-toggle">${icon('clipboard', 14)}批量粘贴</button></div>
      ${d.bulkOpen ? `<div class="credential-bulk"><label class="field" for="credential-bulk-location">批量添加到<select id="credential-bulk-location" data-field="credential-bulk-location">${[['header', 'Header'], ['query', 'Query'], ['body', 'Body'], ['cookie', 'Cookie']].map(([location, label]) => `<option value="${location}" ${d.bulkLocation === location ? 'selected' : ''}>${label}</option>`).join('')}</select></label><label class="field" for="credential-bulk-text">参数内容<textarea id="credential-bulk-text" data-field="credential-bulk-text" rows="4" autocomplete="off" spellcheck="false" placeholder="${e({ header: 'X-API-Key: your-key\nX-Tenant-ID: your-tenant\n也支持 JSON 对象', query: 'api_key=your-key&tenant_id=demo\n也支持 JSON 对象', body: '{"app_id":"your-id","auth":{"token":"your-token"}}', cookie: 'session=your-value; tenant=demo\n也支持 JSON 对象' }[d.bulkLocation])}">${e(d.bulkText)}</textarea></label><div class="credential-bulk-footer"><span>加入待保存列表后，输入内容会清空。</span>${button('加入参数列表', 'credential-bulk-apply', 'plus')}</div></div>` : ''}
      <p class="credential-help credential-priority">启用的固定参数会覆盖同名调用参数；JSON 对象逐层合并，数组与普通值直接覆盖，多条 Body 配置冲突时后面的条目优先。预设认证优先于同名 Header。Body 路径示例：<code>app_id</code>、<code>/auth/token</code>；<code>$</code> 表示合并整个 JSON 对象。</p></section>
      <div class="info-note">${icon('shield', 17)}<span>所有值在服务端加密保存，不回显已保存的密钥。删除条目后保存即可移除该参数，取消启用可暂时保留。</span></div>${errorSlot}`;
    return modal('API 请求与凭证配置', getDocument(d.id).name, body, `<span class="muted small">修改立即生效于引用此文档的所有服务</span>${button('保存配置', 'save-credentials', 'check', 'primary')}`, true);
  }
  function credentialEntryRow(d, entry, index) {
    const number = index + 1, key = e(entry.key);
    const placeholder = entry.in === 'body' ? 'app_id 或 /auth/token' : { header: 'Authorization / X-API-Key', query: 'api_key / tenant_id', cookie: 'session / token' }[entry.in];
    return `<div class="credential-entry ${entry.enabled ? '' : 'inactive'}" data-entry-row="${key}">
      <select id="credential-location-${key}" aria-label="参数 ${number} 位置" data-field="credential-entry-in" data-entry="${key}">${[['header', 'Header'], ['query', 'Query'], ['body', 'Body'], ['cookie', 'Cookie']].map(([location, label]) => `<option value="${location}" ${entry.in === location ? 'selected' : ''}>${label}</option>`).join('')}</select>
      <input id="credential-name-${key}" aria-label="参数 ${number} 名称" data-field="credential-entry-name" data-entry="${key}" value="${e(entry.name)}" placeholder="${placeholder}" spellcheck="false">
      <input id="credential-value-${key}" aria-label="参数 ${number} 值" data-field="credential-entry-value" data-entry="${key}" type="${d.showValues ? 'text' : 'password'}" value="${e(entry.empty ? '' : entry.value)}" ${entry.empty ? 'disabled' : ''} placeholder="${entry.empty ? '发送空字符串' : entry.configured ? '已保存，留空保留' : entry.valueType === 'json' ? 'JSON 值，如 123、true、{}' : '输入参数值'}" autocomplete="off" spellcheck="false">
      ${entry.in === 'body' ? `<select id="credential-type-${key}" aria-label="参数 ${number} 值类型" data-field="credential-entry-type" data-entry="${key}"><option value="string" ${entry.valueType !== 'json' ? 'selected' : ''}>文本</option><option value="json" ${entry.valueType === 'json' ? 'selected' : ''}>JSON</option></select>` : '<span class="credential-text-type">文本</span>'}
      <div class="credential-row-options"><label class="credential-check"><input id="credential-enabled-${key}" type="checkbox" aria-label="启用参数 ${number}" data-field="credential-entry-enabled" data-entry="${key}" ${entry.enabled ? 'checked' : ''}>启用</label><label class="credential-check"><input id="credential-empty-${key}" type="checkbox" aria-label="参数 ${number} 使用空值" data-field="credential-entry-empty" data-entry="${key}" ${entry.empty ? 'checked' : ''} ${entry.valueType === 'json' ? 'disabled' : ''}>空值</label></div>
      ${iconButton(`移除参数 ${number}`, 'trash', 'credential-remove', entry.key)}</div>`;
  }
  function addCredentialEntry(location) {
    const d = state.dialog;
    if (d?.kind !== 'credentials' || d.busy) return;
    if (d.entries.length >= 100) { showError('最多配置 100 条请求参数。'); return; }
    const entry = { id: '', key: C.uid(), in: location, name: '', value: '', valueType: 'string', enabled: true, configured: false, empty: false };
    d.entries.push(entry); renderDialog(false);
    document.getElementById(`credential-name-${entry.key}`)?.focus();
  }
  function addBulkCredentials() {
    const d = state.dialog;
    if (d?.kind !== 'credentials' || d.busy) return;
    try {
      const parsed = C.parseCredentialEntries(d.bulkText, d.bulkLocation);
      const next = structuredClone(d.entries);
      for (const item of parsed) {
        const existing = next.find(entry => entry.in === item.in && (item.in === 'header' ? entry.name.toLowerCase() === item.name.toLowerCase() : entry.name === item.name));
        if (existing) Object.assign(existing, item, { empty: item.value === '' });
        else next.push({ ...item, id: '', key: C.uid(), configured: false, empty: item.value === '' });
      }
      if (next.length > 100) throw new Error('最多配置 100 条请求参数。');
      d.entries = next; d.bulkText = ''; d.bulkOpen = false; renderDialog(false);
      notify(`已加入 ${parsed.length} 条参数，保存配置后生效`);
    } catch (error) { showError(error.message); }
  }
  async function saveCredentials() {
    const d = state.dialog;
    if (d.busy) return;
    dialogBusy(d, true);
    try {
      const entries = d.entries.filter(entry => entry.id || entry.name.trim() || entry.value !== '').map(entry => ({
        id: entry.id, in: entry.in, name: entry.name.trim(), value: entry.empty ? '' : entry.value,
        valueType: entry.valueType || 'string', enabled: entry.enabled, replaceValue: entry.empty,
      }));
      await api(`/api/documents/${d.id}/credentials`, 'PUT', {
        baseUrl: d.baseUrl.trim(), kind: d.authKind, header: d.header.trim(), username: d.username,
        value: d.emptyPassword ? '' : d.value, replaceValue: d.emptyPassword, bodyFormat: d.bodyFormat, entries,
      });
      await refresh(); invalidateClientHeaders(); dialogBusy(d, false);
      if (state.dialog === d) closeDialog();
      render(); notify('API 配置已安全保存');
    } catch (error) { showError(error.message); }
    finally { dialogBusy(d, false); }
  }

  function operationDialog(d) {
    const op = getOperation(d.id), doc = getDocument(op.documentId);
    const busy = d.callBusy;
    const credential = doc.credential;
    const tabs = [['params', 'Params'], ['header', 'Headers'], ['body', 'Body'], ['cookie', 'Cookies'], ['details', '接口说明']];
    const body = `<div class="api-request-toolbar">${methodBadge(op.method)}<code title="${e(doc.baseUrl + op.path)}">${e((doc.baseUrl || '请先配置 API 基础地址') + op.path)}</code>${button(busy ? '请求中…' : '发送请求', 'api-send', busy ? 'loader' : 'play', 'primary', '', busy || d.loading || !!d.loadError || !doc.baseUrl)}${busy ? button('取消', 'api-cancel', 'x') : ''}</div>
      <div class="api-auth-line">${icon('shield', 14)}<span>${credential?.configured ? '自动使用已保存凭证与固定参数' : '当前文档未配置凭证'}</span><button class="text-button" data-action="api-credentials" ${busy ? 'disabled' : ''}>配置凭证</button></div>
      ${!['GET', 'HEAD', 'OPTIONS'].includes(op.method) ? '<p class="tool-warning api-write-warning">此接口会修改业务数据，点击发送将实际执行请求。</p>' : ''}
      ${d.loading ? '<div class="api-loading" role="status">正在加载接口参数…</div>' : d.loadError ? `<div class="form-error" role="alert">${e(d.loadError)}</div>${button('重新加载', 'api-reload', 'activity')}` : `<div class="api-request-tabs" role="tablist" aria-label="请求配置">${tabs.map(([id, label]) => `<button id="api-tab-${id}" role="tab" aria-selected="${d.requestTab === id}" aria-controls="api-request-content" data-action="api-request-tab" data-id="${id}" ${busy ? 'disabled' : ''}>${label}${id === 'params' ? `<span>${d.rows.filter(row => row.in === 'path' || row.in === 'query').length}</span>` : ''}</button>`).join('')}</div><section id="api-request-content" role="tabpanel" aria-labelledby="api-tab-${e(d.requestTab)}" class="api-request-content">${apiRequestContent(d, op, doc)}</section>${errorSlot}${apiResponsePanel(d)}`}`;
    return modal('API 请求测试', `${doc.name} · ${op.name}`, body, `<span class="muted small">直接调用业务 API · ${d.timeout || 20} 秒超时 · 无需发布 MCP</span>${button('关闭', 'close-dialog')}`, true);
  }
  function testRow(inValue, name, schema = {}, required = false, description = '', declared = true) {
    const value = schema.default === undefined ? '' : typeof schema.default === 'string' ? schema.default : JSON.stringify(schema.default);
    return { key: C.uid(), in: inValue, name, value, schema, required, description, declared, enabled: required || schema.default !== undefined };
  }
  async function loadOperationTest(d) {
    d.loading = true; d.loadError = ''; renderDialog(false);
    const op = getOperation(d.id);
    try {
      const description = await api(`/api/documents/${encodeURIComponent(op.documentId)}/test?operationId=${encodeURIComponent(op.id)}`);
      if (state.dialog !== d) return;
      Object.assign(d, { inputSchema: description.inputSchema, defaultFormat: description.bodyFormat, timeout: description.timeout, loading: false });
      d.rows = op.parameters.filter(p => Object.hasOwn(d.inputSchema.properties?.[p.location]?.properties || {}, p.name)).map(p => testRow(p.location, p.name, d.inputSchema.properties[p.location].properties[p.name], d.inputSchema.properties[p.location].required?.includes(p.name), p.description));
      const bodySchema = d.inputSchema.properties?.body || {};
      const fields = C.testBodyFields(bodySchema);
      d.formRows = Object.entries(fields.properties).map(([name, schema]) => testRow('body', name, schema, fields.required.includes(name), schema.description));
      d.bodyText = d.inputSchema.required?.includes('body') ? JSON.stringify(C.testExample(bodySchema), null, 2) : '';
      d.requestTab = d.rows.some(row => row.in === 'path' || row.in === 'query') ? 'params' : op.requestBody ? 'body' : 'params';
      renderDialog(false);
    } catch (error) { if (state.dialog === d) { d.loading = false; d.loadError = error.message; renderDialog(false); } }
  }
  function apiRows(d, rows, allowAdd = '') {
    const controls = rows.map(row => `<div class="api-parameter-row ${row.enabled ? '' : 'inactive'}" data-test-row="${e(row.key)}">
      <input id="api-enabled-${e(row.key)}" type="checkbox" data-field="api-row-enabled" data-entry="${e(row.key)}" aria-label="发送参数 ${e(row.name || '新参数')}" ${row.enabled ? 'checked' : ''} ${d.callBusy || row.in === 'path' ? 'disabled' : ''}>
      <span class="api-param-location">${e(row.in === 'body' ? 'form' : row.in)}</span>
      <div class="api-param-name"><input id="api-name-${e(row.key)}" data-field="api-row-name" data-entry="${e(row.key)}" aria-label="参数名称 ${e(row.name || '新参数')}" value="${e(row.name)}" placeholder="参数名" ${row.declared ? 'readonly' : ''} ${d.callBusy ? 'disabled' : ''} spellcheck="false">${row.required ? '<small class="required">必填</small>' : ''}</div>
      <div class="api-param-value"><input id="api-value-${e(row.key)}" data-field="api-row-value" data-entry="${e(row.key)}" aria-label="参数值 ${e(row.name || '新参数')}" value="${e(row.value)}" placeholder="${e(row.schema.type === 'boolean' ? 'true / false' : row.schema.type === 'array' ? '["value1", "value2"]' : row.schema.type === 'object' ? '{"key":"value"}' : '输入参数值，空文本将按空字符串发送')}" ${d.callBusy ? 'disabled' : ''} autocomplete="off" spellcheck="false"><small>${e(row.schema.type || 'string')}${row.description ? ` · ${e(row.description)}` : ''}</small></div>
      ${row.declared ? '<span></span>' : iconButton('移除请求参数', 'trash', 'api-remove-row', row.key, d.callBusy)}</div>`).join('');
    return `${rows.length ? `<div class="api-parameter-labels"><span></span><span>位置</span><span>参数名称</span><span>参数值</span><span></span></div><div class="api-parameter-list">${controls}</div>` : '<div class="api-parameter-empty">此处没有文档参数，可按需添加本次请求的参数。</div>'}${allowAdd ? `<div class="api-add-parameter">${button(`添加 ${allowAdd === 'body' ? '表单字段' : { query: 'Query', header: 'Header', cookie: 'Cookie' }[allowAdd]}`, 'api-add-row', 'plus', '', allowAdd, d.callBusy)}<span>仅用于本次测试，勾选后发送；数组或对象请填写 JSON。</span></div>` : ''}`;
  }
  function apiFixedParameters(doc, locations) {
    const entries = (doc.credential?.entries || []).filter(row => row.enabled && locations.includes(row.in));
    const preset = locations.includes('header') && doc.credential?.kind !== 'none' && doc.credential?.valueConfigured;
    if (!entries.length && !preset) return '';
    return `<div class="api-injected"><strong>${icon('shield', 13)}服务端自动注入</strong>${preset ? `<div><code>${e(doc.credential.kind === 'apiKey' ? doc.credential.header : 'Authorization')}</code><span>•••••• · ${e(doc.credential.kind)}</span></div>` : ''}${entries.map(row => `<div><code>${e(row.in)} · ${e(row.name)}</code><span>•••••• · 已保存</span></div>`).join('')}<p>固定参数优先，已保存的值不回显；修改请使用“配置凭证”。</p></div>`;
  }
  function currentBodyMode(d) { return d.bodyFormat === 'configured' ? d.defaultFormat || 'json' : d.bodyFormat; }
  function formBodyText(d) {
    return JSON.stringify(C.testArguments(d.formRows.map(row => ({ ...row, in: 'query' }))).query || {}, null, 2);
  }
  function apiRequestContent(d, op, doc) {
    if (d.requestTab === 'params') return `<p class="api-panel-help">Path 参数填写在对应行，Query 会自动进行 URL 编码。取消勾选即可省略可选参数。</p>${apiRows(d, d.rows.filter(row => row.in === 'path' || row.in === 'query'), 'query')}${apiFixedParameters(doc, ['query'])}`;
    if (d.requestTab === 'header' || d.requestTab === 'cookie') return `${apiRows(d, d.rows.filter(row => row.in === d.requestTab), d.requestTab)}${apiFixedParameters(doc, [d.requestTab])}`;
    if (d.requestTab === 'body') return `<div class="api-body-tools"><label for="api-body-format">发送格式<select id="api-body-format" data-field="api-body-format" ${d.callBusy ? 'disabled' : ''}><option value="configured" ${d.bodyFormat === 'configured' ? 'selected' : ''}>按已保存配置 · ${d.defaultFormat === 'form' ? '表单' : 'JSON'}</option><option value="json" ${d.bodyFormat === 'json' ? 'selected' : ''}>JSON · application/json</option><option value="form" ${d.bodyFormat === 'form' ? 'selected' : ''}>表单 · application/x-www-form-urlencoded</option></select></label>${button('填入示例', 'api-body-example', 'code', '', '', d.callBusy)}</div>${currentBodyMode(d) === 'form' ? apiRows(d, d.formRows, 'body') : `<label class="visually-hidden" for="api-body-text">请求 Body JSON</label><textarea id="api-body-text" class="api-body-editor mono" data-field="api-body-text" spellcheck="false" placeholder="输入 JSON 请求体；留空时仅发送已配置的固定 Body 参数" ${d.callBusy ? 'disabled' : ''}>${e(d.bodyText)}</textarea>`}<p class="api-panel-help">Body 固定字段会在服务端合并。此处切换格式只影响本次请求。</p>${apiFixedParameters(doc, ['body'])}`;
    return `${op.description ? `<p class="operation-description">${e(op.description)}</p>` : ''}<div class="detail-metadata"><span>工具名称</span><code>${e(op.toolName || op.operationId)}</code><span>接口路径</span><code>${e(op.path)}</code></div><h3 class="section-label">调用参数 Schema</h3><pre class="code-block">${e(JSON.stringify(d.inputSchema, null, 2))}</pre>${op.responseExample !== undefined ? `<h3 class="section-label">响应示例</h3><pre class="code-block">${e(JSON.stringify(op.responseExample, null, 2))}</pre>` : ''}`;
  }
  function apiHeaderTable(headers) {
    return `<div class="api-headers-table">${Object.entries(headers || {}).map(([name, values]) => `<div><code>${e(name)}</code><span>${e(Array.isArray(values) ? values.join('\n') : values)}</span></div>`).join('') || '<p class="api-panel-help">没有响应头</p>'}</div>`;
  }
  function apiResponseText(d) {
    const raw = d.result?.response?.body || '';
    if (d.rawResponse || !raw) return raw;
    try { return JSON.stringify(C.testValue(raw, { type: 'object' }), null, 2); } catch { /* Scalars, arrays and large integers can be inspected as original text. */ }
    try {
      const value = C.testValue(raw, { type: 'array' });
      return JSON.stringify(value, null, 2);
    } catch { return raw; }
  }
  function apiResponsePanel(d) {
    const result = d.result, response = result?.response;
    return `<section class="api-response-panel" aria-label="API 测试响应"><div class="api-response-heading"><h3>响应</h3>${response ? `<span class="api-http-status ${result.success ? 'success' : 'failure'}">${response.status} ${e(response.statusText)}</span><span>${result.duration} ms</span><span>${response.size < 1024 ? `${response.size} B` : `${(response.size / 1024).toFixed(1)} KB`}</span>` : ''}${d.callBusy ? '<span class="api-waiting" role="status">正在等待响应…</span>' : ''}</div>${d.requestError || result?.error ? `<div class="form-error" role="alert">${e(d.requestError || result.error)}</div>` : ''}${result ? `<div class="api-response-tabs" role="tablist" aria-label="响应视图">${[['body', 'Body'], ['headers', 'Headers'], ['request', '实际请求（凭证脱敏）']].map(([id, label]) => `<button id="api-response-tab-${id}" role="tab" aria-selected="${d.responseTab === id}" aria-controls="api-response-content" data-action="api-response-tab" data-id="${id}">${label}</button>`).join('')}${response && d.responseTab === 'body' ? `<button class="text-button api-response-raw" data-action="api-response-raw">${d.rawResponse ? '格式化' : '原文'}</button><button class="text-button" data-action="api-copy-response">${icon('copy', 13)}复制</button>` : ''}</div><div id="api-response-content" role="tabpanel" aria-labelledby="api-response-tab-${e(d.responseTab)}">${d.responseTab === 'headers' ? apiHeaderTable(response?.headers) : d.responseTab === 'request' ? `<code class="api-request-url">${e(result.request.method)} ${e(result.request.url)}</code>${apiHeaderTable(result.request.headers)}${result.request.body ? `<h4>请求 Body</h4><pre class="api-response-code">${e(result.request.body)}</pre>` : ''}` : `<pre class="api-response-code" role="status" aria-label="API 响应体">${e(response ? apiResponseText(d) || '（响应体为空）' : '未收到 HTTP 响应')}</pre>`}</div>` : d.callBusy || d.requestError ? '' : '<div class="api-response-empty">填写请求参数并点击“发送请求”，在这里查看状态码、耗时和响应内容。</div>'}</section>`;
  }
  function changeBodyFormat(d, value) {
    const oldMode = currentBodyMode(d);
    const nextMode = value === 'configured' ? d.defaultFormat : value;
    if (oldMode !== nextMode) {
      if (nextMode === 'json') d.bodyText = formBodyText(d);
      else if (d.bodyText.trim()) {
        const object = C.testValue(d.bodyText, { type: 'object' });
        d.formRows.forEach(row => { row.enabled = false; });
        for (const [name, value] of Object.entries(object)) {
          let row = d.formRows.find(row => row.name === name);
          if (!row) { row = testRow('body', name, { type: Array.isArray(value) ? 'array' : value === null ? 'null' : typeof value }, false, '', false); d.formRows.push(row); }
          row.enabled = true; row.value = typeof value === 'string' ? value : JSON.stringify(value);
        }
      }
    }
    d.bodyFormat = value;
  }
  function addTestRow(location) {
    const d = state.dialog;
    if (d.callBusy) return;
    if (d.rows.length + d.formRows.length >= 100) { showError('本次请求最多添加 100 条参数。'); return; }
    const row = testRow(location, '', {}, false, '', false); row.enabled = true;
    (location === 'body' ? d.formRows : d.rows).push(row); renderDialog(false);
    document.getElementById(`api-name-${row.key}`)?.focus();
  }
  async function sendApiRequest() {
    const d = state.dialog;
    if (d.callBusy || d.loading) return;
    const op = getOperation(d.id);
    let payload;
    try {
      const bodyText = currentBodyMode(d) === 'form' ? d.formRows.some(row => row.enabled && row.name) || d.inputSchema.required?.includes('body') ? formBodyText(d) : '' : d.bodyText;
      payload = C.testPayload(op.id, d.rows, bodyText, d.bodyFormat);
    } catch (error) { showError(error.message); return; }
    const controller = new AbortController(); fetchController = controller;
    d.controller = controller; d.callBusy = true; d.result = null; d.requestError = ''; d.responseTab = 'body'; renderDialog(false);
    try {
      const result = await api(`/api/documents/${encodeURIComponent(op.documentId)}/test`, 'POST', payload, controller.signal);
      if (state.dialog === d) { d.result = result; d.callBusy = false; renderDialog(false); dialogs.querySelector('.api-response-panel')?.scrollIntoView({ block: 'nearest' }); }
    } catch (error) {
      if (state.dialog === d) { d.requestError = error.name === 'AbortError' ? '请求已取消' : error.message; d.callBusy = false; renderDialog(false); dialogs.querySelector('.api-response-panel')?.scrollIntoView({ block: 'nearest' }); }
    } finally { if (fetchController === controller) fetchController = undefined; d.controller = undefined; }
  }
  function sampleArguments(schema, name = '') {
    if (schema.enum?.length) return schema.enum[0];
    if (schema.default !== undefined) return schema.default;
    if (schema.type === 'object') return Object.fromEntries(Object.entries(schema.properties || {}).filter(([key]) => schema.required?.includes(key)).map(([key, value]) => [key, sampleArguments(value, key)]));
    if (schema.type === 'array') return [sampleArguments(schema.items || {})];
    if (schema.type === 'integer' || schema.type === 'number') return schema.minimum ?? 1;
    if (schema.type === 'boolean') return true;
    return name === 'id' || name === 'userId' ? 'usr_001' : name === 'name' ? '示例用户' : '示例值';
  }
  function testDialog(d) {
    const server = getServer(d.id), success = d.success;
    const selected = d.tools?.find(tool => tool.name === d.toolName);
    const body = `<div class="test-address">${icon('globe', 17)}<code>${e(endpoint(server))}</code></div>
      <div class="test-steps">${['通过 HTTP 连接 MCP 服务', '验证独立调用 Token', '读取可用工具列表'].map((label, index) => `<div class="test-step ${d.step >= index ? 'active' : ''}">${icon(d.finished && success ? 'circleCheck' : d.finished && !success ? 'circle' : d.step === index ? 'loader' : 'circle', 21, !d.finished && d.step === index ? 'spin' : '')}<span>${label}</span></div>`).join('')}</div>
      ${d.finished ? `<div role="status" class="test-result ${success ? 'success' : 'failure'}">${icon(success ? 'circleCheck' : 'x', 23)}<div><strong>${success ? '连接成功' : '连接失败'}</strong><p>${success ? `发现 ${d.tools.length} 个工具，耗时 ${d.duration} ms。` : e(d.error)}</p></div></div>` : ''}
      ${success && d.tools.length ? `<div class="tool-test-form"><h3 class="section-label">试调用工具</h3><label class="field">选择工具<select data-field="test-tool" aria-label="选择测试工具" ${d.callBusy ? 'disabled' : ''}>${d.tools.map(tool => `<option value="${e(tool.name)}" ${d.toolName === tool.name ? 'selected' : ''}>${e(tool.annotations?.title || tool.name)}</option>`).join('')}</select></label><label class="field" for="test-arguments">工具参数（JSON）</label><textarea id="test-arguments" class="mono tool-arguments" data-field="test-arguments" spellcheck="false" ${d.callBusy ? 'disabled' : ''}>${e(d.arguments)}</textarea>${selected?.annotations?.readOnlyHint ? '' : '<p class="tool-warning">此工具会修改业务数据，执行后将实际调用对应 API。</p>'}${button(d.callBusy ? '调用中…' : '执行工具调用', 'call-tool', 'play', 'primary', '', d.callBusy)}${d.result ? `<pre class="code-block tool-result" role="status">${e(d.result)}</pre>` : ''}${errorSlot}</div>` : ''}`;
    return modal('测试 MCP 连接', server.name, body, `<span class="muted small">通过真实 MCP 协议完成验证</span>${button(d.finished ? '完成' : '关闭', 'close-dialog', '', 'primary')}`);
  }
  async function startTest(d) {
    try {
      const result = await api(`/api/servers/${d.id}/test`, 'POST');
      await refresh();
      if (state.dialog !== d) return;
      Object.assign(d, result, { finished: true, step: 2 });
      const initialTool = d.tools.find(tool => tool.annotations?.readOnlyHint && !tool.inputSchema?.required?.length) || d.tools.find(tool => tool.annotations?.readOnlyHint) || d.tools[0];
      d.toolName = initialTool?.name || '';
      d.arguments = JSON.stringify(sampleArguments(initialTool?.inputSchema || { type: 'object' }), null, 2);
      renderDialog(false);
    } catch (error) { if (state.dialog === d) { Object.assign(d, { finished: true, success: false, error: error.message }); renderDialog(false); } }
  }
  async function runTool() {
    const d = state.dialog;
    if (d.callBusy) return;
    try {
      let argumentsValue;
      try { argumentsValue = JSON.parse(d.arguments); } catch { throw new Error('请填写有效的 JSON 参数。'); }
      d.callBusy = true; d.result = ''; renderDialog(false);
      const result = await api(`/api/servers/${d.id}/call`, 'POST', { name: d.toolName, arguments: argumentsValue });
      await refresh();
      if (state.dialog !== d) return;
      d.result = JSON.stringify(result.structuredContent || result, null, 2);
      d.callBusy = false; renderDialog(false);
    } catch (error) { d.callBusy = false; if (state.dialog === d) { renderDialog(false); showError(error.message); } }
  }

  function confirmDialog(d) {
    const server = getServer(d.id), doc = getDocument(d.id);
    const content = {
      'delete-server': ['删除 MCP Server', `确定删除「${server?.name}」？服务配置将被移除，该调用地址立即失效。`, '删除服务'],
      'delete-document': ['删除 API 文档', `确定删除「${doc?.name}」？解析出的接口也会移除。`, '删除文档'],
      'clear-logs': ['清空调用记录', '确定清空服务端保存的所有调用记录？', '清空记录'],
      'rotate-token': ['重置调用 Token', '重置后旧 Token 立即失效，需要更新所有调用此服务的客户端配置。', '重置 Token'],
    }[d.kind];
    return modal(content[0], '', `<p class="confirm-description">${e(content[1])}</p>`, `<div class="button-group push-right">${button('取消', 'close-dialog')}${button(content[2], 'confirm', '', 'danger')}</div>`);
  }


  function readRoute() {
    const parts = window.location.hash.replace(/^#\/?/, '').split('/');
    if (parts[0] === 'servers' && parts[1]) {
      state.page = 'server-detail';
      state.selectedId = parts[1];
      state.connectionTab = parts[2] === 'config' ? 'json' : 'address';
    } else if (parts[0] === 'documents' && parts[1]) {
      state.page = 'document-detail';
      if (state.selectedDocumentId !== parts[1]) { state.apiQuery = ''; state.apiMethod = 'all'; }
      state.selectedDocumentId = parts[1];
    } else state.page = navigation.some(item => item[0] === parts[0]) ? parts[0] : 'servers';
  }
  function routeTo(hash) {
    if (window.location.hash !== hash) window.location.hash = hash;
    readRoute(); state.sidebarOpen = false; render(); window.scrollTo({ top: 0 });
  }
  function navigate(page) {
    if (!navigation.some(item => item[0] === page)) return;
    routeTo(`#/${page}`);
    if (page === 'logs' || page === 'overview') api('/api/logs').then(logs => { state.workspace.logs = logs; if (!state.dialog) render(); }).catch(error => notify(error.message, 'error'));
  }
  function showServer(id, tab = 'apis') {
    state.connectionTab = tab === 'config' ? 'json' : 'address';
    routeTo(`#/servers/${encodeURIComponent(id)}${tab === 'config' ? '/config' : ''}`);
  }
  function showDocument(id) {
    state.apiQuery = ''; state.apiMethod = 'all';
    routeTo(`#/documents/${encodeURIComponent(id)}`);
  }
  window.addEventListener('hashchange', () => {
    if (state.dialog) return;
    readRoute(); render(); window.scrollTo({ top: 0 });
  });
  function notify(message, variant = 'success') {
    clearTimeout(toastTimer);
    notifications.innerHTML = `<div class="toast ${variant}" role="status">${icon(variant === 'success' ? 'circleCheck' : 'info', 19)}<span>${e(message)}</span>${iconButton('关闭提示', 'x', 'dismiss-toast')}</div>`;
    toastTimer = setTimeout(() => { notifications.innerHTML = ''; }, 4500);
  }
  async function copy(value, label) {
    try {
      try {
        if (!navigator.clipboard) throw new Error('Clipboard unavailable');
        await navigator.clipboard.writeText(value);
      } catch {
        // file:// and older browsers can use the synchronous copy fallback.
        const input = document.createElement('textarea');
        input.value = value; input.className = 'clipboard-fallback';
        document.body.append(input);
        const previous = document.activeElement;
        input.select();
        const copied = document.execCommand('copy');
        input.remove(); previous?.focus({ preventScroll: true });
        if (!copied) throw new Error('Copy rejected');
      }
      notify(`${label}已复制`);
    } catch { notify('浏览器未允许复制，请手动选择内容复制。', 'error'); }
  }
  async function toggleServer(id) {
    const server = getServer(id);
    if (server.status === 'draft') { openDialog({ kind: 'editor', id }); return; }
    const status = server.status === 'running' ? 'stopped' : 'running';
    await api(`/api/servers/${id}/state`, 'POST', { status });
    await refresh(); render(); notify(status === 'running' ? '服务已启用' : '服务已停用');
  }
  async function duplicateServer(id) {
    const original = getServer(id);
    const draft = { type: original.type || 'api', name: `${original.name.slice(0, 35)} 副本`, slug: `${original.slug.slice(0, 37)}-copy-${C.uid().slice(0, 5)}`, description: original.description, color: original.color, operationIds: [...original.operationIds] };
    if (isProxy(original)) draft.proxy = await api(`/api/servers/${original.id}/proxy-config?published=true`);
    const server = await api('/api/servers', 'POST', { draft, mode: 'draft' });
    await refresh(); showServer(server.id); notify('已复制为新服务草稿，可配置后发布');
  }

  function documentUsage(id) {
    const ids = new Set(getDocument(id).operations.map(op => op.id));
    return state.workspace.servers.filter(server => [...server.operationIds, ...(server.draft?.operationIds || [])].some(opId => ids.has(opId)));
  }
  function deleteDocument(id) {
    const usage = documentUsage(id);
    if (usage.length) { notify(`文档被 ${usage.length} 个服务或草稿引用，请先在服务配置中移除相关 API。`, 'error'); return; }
    openDialog({ kind: 'delete-document', id });
  }
  async function confirmAction() {
    const d = state.dialog;
    if (d.busy) return;
    dialogBusy(d, true);
    try {
      const requests = {
        'delete-server': [`/api/servers/${d.id}`, 'DELETE', '服务已删除'],
        'delete-document': [`/api/documents/${d.id}`, 'DELETE', '文档已删除'],
        'clear-logs': ['/api/logs', 'DELETE', '调用记录已清空'],
        'rotate-token': [`/api/servers/${d.id}/token`, 'POST', '调用 Token 已重置，请更新客户端配置'],
      };
      const [path, method, message] = requests[d.kind];
      await api(path, method); await refresh(); dialogBusy(d, false);
      if (state.dialog === d) closeDialog();
      if (d.kind === 'delete-document' && state.page === 'document-detail' && state.selectedDocumentId === d.id) navigate('documents');
      else if (d.kind === 'delete-server' && state.page === 'server-detail' && state.selectedId === d.id) navigate('servers');
      else render();
      notify(message);
    } catch (error) { notify(error.message, 'error'); }
    finally { dialogBusy(d, false); }
  }

  document.addEventListener('click', event => {
    const target = event.target;
    if (target === dialogs.querySelector('.modal-backdrop')) { closeDialog(); return; }
    if (target.closest('summary') || target.matches('input[type="checkbox"]')) return;
    document.querySelectorAll('.more-menu[open]').forEach(menu => { if (!menu.contains(target)) menu.removeAttribute('open'); });
    const control = target.closest('[data-action]');
    if (!control || control.disabled) return;
    const { action, id } = control.dataset;
    if (state.dialog && !dialogs.contains(control) && action !== 'dismiss-toast') return;
    if (control.closest('.menu-popover')) control.closest('details').removeAttribute('open');
    const server = id ? getServer(id) : undefined;
    const actions = {
      navigate: () => navigate(id),
      logout: async () => { await api('/api/logout', 'POST'); window.location.replace(appPath('/login')); },
      retry: async () => { await refresh(); render(); },
      'rotate-token': () => openDialog({ kind: 'rotate-token', id }),
      'call-tool': runTool,
      'api-send': sendApiRequest,
      'api-cancel': () => state.dialog.controller?.abort(),
      'api-reload': () => loadOperationTest(state.dialog),
      'api-credentials': () => { const documentId = getOperation(state.dialog.id).documentId; closeDialog(); openDialog({ kind: 'credentials', id: documentId }); },
      'api-request-tab': () => { state.dialog.requestTab = id; renderDialog(false); },
      'api-response-tab': () => { state.dialog.responseTab = id; renderDialog(false); },
      'api-response-raw': () => { state.dialog.rawResponse = !state.dialog.rawResponse; renderDialog(false); },
      'api-copy-response': () => copy(state.dialog.result?.response?.body || '', '响应内容'),
      'api-add-row': () => addTestRow(id),
      'api-remove-row': () => { const d = state.dialog; if (!d.callBusy) { d.rows = d.rows.filter(row => row.key !== id); d.formRows = d.formRows.filter(row => row.key !== id); renderDialog(false); } },
      'api-body-example': () => {
        const d = state.dialog;
        d.bodyText = JSON.stringify(C.testExample(d.inputSchema.properties?.body || { type: 'object' }), null, 2);
        d.formRows.forEach(row => { row.value = typeof C.testExample(row.schema) === 'string' ? C.testExample(row.schema) : JSON.stringify(C.testExample(row.schema)); row.enabled = row.required || row.schema.default !== undefined; });
        renderDialog(false);
      },
      'open-sidebar': () => { state.sidebarOpen = true; render(); document.querySelector('.sidebar .nav-item.active')?.focus(); },
      'close-sidebar': () => { state.sidebarOpen = false; render(); document.querySelector('.mobile-menu')?.focus(); },
      'select-server': () => showServer(id),
      'show-server': () => showServer(id),
      'clear-server-filters': () => { state.serverQuery = ''; state.serverStatus = 'all'; state.serverType = 'all'; render(); },
      'select-document': () => showDocument(id),
      'clear-document-filters': () => { state.documentQuery = ''; state.documentCredential = 'all'; render(); },
      'filter-document-method': () => { state.apiMethod = id; render(); },
      'clear-api-filters': () => { state.apiQuery = ''; state.apiMethod = 'all'; render(); },
      'copy-document-url': () => copy(getDocument(id).baseUrl, 'API 基础地址'),
      'create-from-document': () => openDialog({ kind: 'editor', sourceDocumentId: id }),
      create: () => openDialog({ kind: 'editor' }),
      'create-proxy': () => openDialog({ kind: 'editor', serverType: 'proxy' }),
      edit: () => { state.selectedId = id; openDialog({ kind: 'editor', id }); },
      import: () => openDialog({ kind: 'import' }),
      'update-document': () => openDialog({ kind: 'import', id }),
      test: () => openDialog({ kind: 'test', id }),
      operation: () => openDialog({ kind: 'operation', id }),
      credentials: () => openDialog({ kind: 'credentials', id }),
      'toggle-server': () => toggleServer(id),
      'duplicate-server': () => duplicateServer(id),
      'delete-server': () => openDialog({ kind: 'delete-server', id }),
      'delete-document': () => deleteDocument(id),
      'copy-url': () => copy(C.endpointFor(server, state.workspace.settings.endpointOrigin), '调用地址'),
      'copy-token': () => copy(server.token, '访问 Token'),
      'copy-config': async () => { await ensureClientHeaders(server.id); if (state.clientHeaderErrors[server.id]) throw new Error(state.clientHeaderErrors[server.id]); await copy(clientConfigJSON(server), '客户端配置'); },
      'open-client-config': () => showServer(id, 'config'),
      'connection-tab': () => {
        const tab = id === 'json' ? 'json' : 'address';
        const hash = `#/servers/${encodeURIComponent(state.selectedId)}${tab === 'json' ? '/config' : ''}`;
        if (window.location.hash !== hash) window.location.hash = hash;
        readRoute(); render();
        if (tab === 'json') ensureClientHeaders(state.selectedId);
      },
      'retry-client-headers': () => { delete state.clientHeaderErrors[id]; render(); return ensureClientHeaders(id); },
      'client-template': () => clientTemplate(id),
      'copy-client-json': () => copy(clientConfigJSON(server), 'MCP JSON 配置'),
      'close-dialog': closeDialog,
      'save-draft': () => saveServer('draft'),
      publish: () => saveServer('publish'),
      'editor-type': () => { const d = state.dialog; if (getServer(d.id)?.status && getServer(d.id).status !== 'draft') return; d.draft.type = id; renderDialog(false); },
      'reload-proxy-config': () => loadProxyEditor(state.dialog),
      'proxy-header-template': () => { const d = state.dialog; try { const headers = C.clientRequestHeaders(d.proxyHeadersText, true); if (id === 'bearer') headers.Authorization = 'Bearer <上游 Token>'; else headers['X-API-Key'] = '<你的 API Key>'; d.proxyHeadersText = JSON.stringify(headers, null, 2); renderDialog(false); } catch (error) { showError(error.message); } },
      'toggle-operation': () => toggleOperation(id),
      'editor-color': () => {
        state.dialog.draft.color = id;
        dialogs.querySelectorAll('.color-option').forEach(option => { const selected = option.dataset.id === id; option.setAttribute('aria-pressed', String(selected)); option.innerHTML = selected ? icon('check', 13) : ''; });
      },
      'import-tab': () => { if (!state.dialog.busy) { state.dialog.tab = id; renderDialog(false); } },
      'pick-file': () => document.getElementById('spec-file').click(),
      'example-spec': async () => {
        const d = state.dialog;
        const example = await api('/api/example');
        if (state.dialog !== d) return;
        d.pasteContent = example.content;
        document.getElementById('spec-content').value = d.pasteContent; clearError();
      },
      'submit-import': submitImport,
      'save-credentials': saveCredentials,
      'credential-add': () => addCredentialEntry(id),
      'credential-remove': () => {
        const d = state.dialog;
        if (d?.kind === 'credentials' && !d.busy) { d.entries = d.entries.filter(entry => entry.key !== id); renderDialog(false); }
      },
      'credential-show-values': () => { state.dialog.showValues = !state.dialog.showValues; renderDialog(false); },
      'credential-bulk-toggle': () => { state.dialog.bulkOpen = !state.dialog.bulkOpen; renderDialog(false); },
      'credential-bulk-apply': addBulkCredentials,
      'clear-logs': () => openDialog({ kind: 'clear-logs' }),
      confirm: confirmAction,
      'dismiss-toast': () => { clearTimeout(toastTimer); notifications.innerHTML = ''; },
    };
    try { Promise.resolve(actions[action]?.()).catch(error => notify(error.message, 'error')); } catch (error) { notify(error.message, 'error'); }
  });
  document.addEventListener('input', event => {
    const { field } = event.target.dataset;
    const value = event.target.value;
    if (['serverQuery', 'documentQuery', 'apiQuery', 'logQuery'].includes(field)) { state[field] = value; render(); return; }
    if (field === 'client-credentials-json') { clientDraft(getServer(state.selectedId)).text = value; updateClientConfig(); return; }
    const d = state.dialog;
    if (!d || !field) return;
    if (field.startsWith('draft.')) {
      const key = field.slice(6);
      d.draft[key] = key === 'slug' ? value.toLowerCase() : value;
      if (key === 'slug') event.target.value = d.draft[key];
      clearError(); updateEditor(false);
    }
    if (field === 'editor-query') { d.query = value; updateEditor(); }
    if (field === 'proxy-url') { d.draft.proxy.url = value; clearError(); }
    if (field === 'proxy-headers') { d.proxyHeadersText = value; clearError(); }
    if (field === 'import-content') d.pasteContent = value;
    if (field === 'import-url') d.url = value;
    if (field === 'credential-base') d.baseUrl = value;
    if (field === 'credential-header') d.header = value;
    if (field === 'credential-value') d.value = value;
    if (field === 'credential-username') d.username = value;
    if (field === 'credential-bulk-text') d.bulkText = value;
    if (field === 'credential-entry-name' || field === 'credential-entry-value') {
      const entry = d.entries?.find(entry => entry.key === event.target.dataset.entry);
      if (entry) entry[field === 'credential-entry-name' ? 'name' : 'value'] = value;
      clearError();
    }
    if (field === 'test-arguments') d.arguments = value;
    if (field === 'api-body-text') { d.bodyText = value; clearError(); }
    if (field === 'api-row-name' || field === 'api-row-value') {
      const row = [...(d.rows || []), ...(d.formRows || [])].find(row => row.key === event.target.dataset.entry);
      if (row) {
        row[field === 'api-row-name' ? 'name' : 'value'] = value;
        if (value !== '') { row.enabled = true; document.getElementById(`api-enabled-${row.key}`).checked = true; event.target.closest('.api-parameter-row')?.classList.remove('inactive'); }
      }
      clearError();
    }
  });
  document.addEventListener('change', event => {
    const { field, id } = event.target.dataset;
    if (['serverStatus', 'serverType', 'logStatus', 'documentCredential'].includes(field)) { state[field] = event.target.value; render(); }
    if (event.target.id === 'spec-file') loadFile(event.target.files[0]);
    const d = state.dialog;
    if (!d) return;
    if (field === 'editor-document') { d.documentFilter = event.target.value; updateEditor(); }
    if (field === 'editor-method') { d.methodFilter = event.target.value; updateEditor(); }
    if (field === 'editor-operation') toggleOperation(id);
    if (field === 'editor-select-method') {
      const method = id;
      const visible = visibleEditorOperations().filter(op => op.method.toUpperCase() === method).map(op => op.id);
      d.draft.operationIds = event.target.checked ? [...new Set([...d.draft.operationIds, ...visible])] : d.draft.operationIds.filter(opId => !visible.includes(opId));
      clearError(); updateEditor();
    }
    if (field === 'editor-select-all') {
      const visible = visibleEditorOperations().map(op => op.id);
      d.draft.operationIds = event.target.checked ? [...new Set([...d.draft.operationIds, ...visible])] : d.draft.operationIds.filter(opId => !visible.includes(opId));
      clearError(); updateEditor();
    }
    if (field === 'test-tool') { d.toolName = event.target.value; d.arguments = JSON.stringify(sampleArguments(d.tools.find(tool => tool.name === d.toolName).inputSchema), null, 2); d.result = ''; renderDialog(false); }
    if (field === 'credential-kind') { d.authKind = event.target.value; d.value = ''; d.username = ''; d.emptyPassword = false; renderDialog(false); }
    if (field === 'credential-empty-password') { d.emptyPassword = event.target.checked; renderDialog(false); }
    if (field === 'credential-bulk-location') { d.bulkLocation = event.target.value; renderDialog(false); }
    if (field === 'credential-body-format') d.bodyFormat = event.target.value;
    if (field === 'api-row-enabled') {
      const row = [...d.rows, ...d.formRows].find(row => row.key === event.target.dataset.entry);
      if (row) { row.enabled = event.target.checked; renderDialog(false); }
    }
    if (field === 'api-body-format') {
      try { changeBodyFormat(d, event.target.value); renderDialog(false); }
      catch (error) { renderDialog(false); showError(error.message); }
    }
    if (field?.startsWith('credential-entry-')) {
      const entry = d.entries?.find(entry => entry.key === event.target.dataset.entry);
      if (!entry) return;
      if (field === 'credential-entry-in') { entry.in = event.target.value; if (entry.in !== 'body') entry.valueType = 'string'; }
      if (field === 'credential-entry-type') { entry.valueType = event.target.value; entry.empty = false; }
      if (field === 'credential-entry-enabled') entry.enabled = event.target.checked;
      if (field === 'credential-entry-empty') entry.empty = event.target.checked;
      renderDialog(false);
    }
  });
  document.addEventListener('dragover', event => {
    const zone = event.target.closest('.upload-zone');
    if (zone) { event.preventDefault(); zone.classList.add('dragging'); }
  });
  document.addEventListener('dragleave', event => event.target.closest('.upload-zone')?.classList.remove('dragging'));
  document.addEventListener('drop', event => {
    const zone = event.target.closest('.upload-zone');
    if (zone) { event.preventDefault(); zone.classList.remove('dragging'); loadFile(event.dataTransfer.files[0]); }
  });
  document.addEventListener('keydown', event => {
    if (!state.dialog) {
      if (event.key === 'Escape') {
        if (state.sidebarOpen) { state.sidebarOpen = false; render(); document.querySelector('.mobile-menu')?.focus(); }
        document.querySelectorAll('.more-menu[open]').forEach(menu => menu.removeAttribute('open'));
      }
      return;
    }
    if (state.dialog.kind === 'operation' && event.key === 'Enter' && (event.ctrlKey || event.metaKey)) { event.preventDefault(); sendApiRequest(); return; }
    if (event.key === 'Escape') { event.preventDefault(); closeDialog(); return; }
    if (event.key !== 'Tab') return;
    const focusable = [...dialogs.querySelectorAll('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [tabindex="0"]')].filter(element => element.getClientRects().length && !element.classList.contains('visually-hidden'));
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (!first) { event.preventDefault(); dialogs.querySelector('.modal').focus(); }
    else if (event.shiftKey && (document.activeElement === first || !dialogs.contains(document.activeElement))) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && (document.activeElement === last || !dialogs.contains(document.activeElement))) { event.preventDefault(); first.focus(); }
  });

  mobileViewport.addEventListener('change', () => { state.sidebarOpen = false; render(); });
  app.innerHTML = '<div class="boot-state">正在连接 API2MCP…</div>';
  if (window.location.protocol === 'file:') {
    app.innerHTML = '<div class="boot-state"><h1>请通过 Go 服务访问后台</h1><p>先启动 Docker Compose，再打开 <a href="http://localhost:8080">localhost:8080</a>。</p></div>';
    return;
  }
  refresh().then(() => { readRoute(); render(); }).catch(error => {
    app.innerHTML = `<div class="boot-state"><h1>暂时无法连接服务</h1><p>${e(error.message)}</p>${button('重试', 'retry', '', 'primary')}</div>`;
  });
  setInterval(async () => {
    if (state.page !== 'logs' || state.dialog || document.visibilityState !== 'visible') return;
    try { state.workspace.logs = await api('/api/logs'); render(); } catch { /* Session expiry is handled by api(). */ }
  }, 5000);
})();
