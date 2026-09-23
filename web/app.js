/**
 * OpenClash & OpenWrt Controller - Frontend Core
 * Ultra-Lightweight, Zero-Emoji, Zero-Icon-With-Text, Fully Fluid Layout
 */

const state = {
  currentView: 'dashboard',
  status: null,
  profiles: [],
  subscriptions: [],
  groups: [],
  activeGroupName: '',
  searchQuery: '',
  delays: {},
  logs: [],
  logFilterLevel: 'ALL',
  logSearchQuery: '',
  connLogs: [],
  connTotal: 0,
  connPage: 1,
  connLimit: 200,
  connTotalPages: 1,
  connSearchQuery: '',
  connRetentionDays: 8,
  connAutoRefresh: localStorage.getItem('conn_auto_refresh') !== 'false',
  logAutoRefresh: localStorage.getItem('log_auto_refresh') !== 'false',
  isInactive: false,
  pollTimer: null,
  pollIntervalSec: 5,
  isTestingLatency: false,
  isPowerTransitioning: false,
  isAirportSwitching: false,
  transitionTarget: '',
  isSubModalOpen: false,
  isSpeedModalOpen: false,
  envContent: '',
  envPath: '',
  envExists: false,
  envExample: '',
  lastViewedErrorId: localStorage.getItem('last_viewed_error_id') !== null
    ? parseInt(localStorage.getItem('last_viewed_error_id'), 10)
    : null,
};

// Initialize Application
document.addEventListener('DOMContentLoaded', () => {
  initTheme();
  renderSpeedGrid();
  setupEventListeners();
  setupInactiveOptimization();
  
  // Initial data load
  refreshAll();
  startPolling();
});

// ----------------------------------------------------
// Inactive Tab / Window Optimization (Zero CPU / Network)
// ----------------------------------------------------
function setupInactiveOptimization() {
  const handleVisibilityChange = () => {
    if (document.hidden) {
      pauseOptimization();
    } else {
      resumeOptimization();
    }
  };

  document.addEventListener('visibilitychange', handleVisibilityChange);
  window.addEventListener('focus', () => {
    if (state.isInactive) {
      resumeOptimization();
    }
  });
}

function pauseOptimization() {
  if (state.isInactive) return;
  state.isInactive = true;
  if (state.pollTimer) {
    clearInterval(state.pollTimer);
    state.pollTimer = null;
  }
}

function resumeOptimization() {
  if (!state.isInactive) return;
  state.isInactive = false;
  refreshAll();
  startPolling();
}

// ----------------------------------------------------
// Auto Refresh Mechanism (Calm 5s interval, 0% when hidden)
// ----------------------------------------------------
function startPolling() {
  if (state.pollTimer) {
    clearInterval(state.pollTimer);
  }
  const intervalMs = (state.pollIntervalSec || 5) * 1000;
  let pollTicks = 0;
  state.pollTimer = setInterval(() => {
    if (!state.isInactive && !state.isTestingLatency && !state.isPowerTransitioning && !state.isAirportSwitching) {
      fetchStatus();
      if (state.currentView === 'logs') {
        if (state.logAutoRefresh) {
          fetchLogs();
        }
      } else if (state.currentView === 'connections') {
        if (state.connAutoRefresh) {
          fetchConnections();
        }
      } else if (state.currentView === 'dashboard') {
        pollTicks++;
        // If nodes haven't loaded yet while online, or periodically, sync proxies
        if (state.status?.clash_online && (state.groups.length === 0 || pollTicks % 6 === 0)) {
          fetchProxies();
        }
      }
    }
  }, intervalMs);
}

// ----------------------------------------------------
// Event Listeners
// ----------------------------------------------------
function setupEventListeners() {
  // Navigation tabs (Dashboard vs Logs)
  document.querySelectorAll('.nav-item').forEach(btn => {
    btn.addEventListener('click', () => {
      const view = btn.dataset.view;
      switchView(view);
    });
  });

  // Power switch toggle
  document.getElementById('btn-power-toggle').addEventListener('click', handlePowerToggle);
  
  // Latency test button
  document.getElementById('btn-test-latency').addEventListener('click', handleTestLatency);

  // Airport custom dropdown trigger
  const airportTrigger = document.getElementById('airport-custom-trigger');
  if (airportTrigger) {
    airportTrigger.addEventListener('click', toggleAirportDropdown);
  }

  // Close custom dropdown on outside click
  document.addEventListener('click', (e) => {
    const wrap = document.getElementById('airport-custom-select-wrap');
    if (wrap && !wrap.contains(e.target)) {
      closeAirportDropdown();
    }
  });

  // Network speed test button
  const btnTestWebSpeed = document.getElementById('btn-test-web-speed');
  if (btnTestWebSpeed) {
    btnTestWebSpeed.addEventListener('click', handleTestWebSpeed);
  }

  // Node search input
  const nodeSearch = document.getElementById('node-search');
  if (nodeSearch) {
    nodeSearch.addEventListener('input', (e) => {
      state.searchQuery = e.target.value.trim().toLowerCase();
      renderNodes();
    });
  }

  // Log level filter buttons
  document.querySelectorAll('.log-filter-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.log-filter-btn').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      state.logFilterLevel = btn.dataset.level;
      renderLogs();
    });
  });

  // Log search input
  const logSearch = document.getElementById('log-search');
  logSearch.addEventListener('input', (e) => {
    state.logSearchQuery = e.target.value.trim().toLowerCase();
    renderLogs();
  });

  // Log export button
  const btnExportLogs = document.getElementById('btn-export-logs');
  if (btnExportLogs) {
    btnExportLogs.addEventListener('click', handleExportLogs);
  }

  // Router OpenClash log modal triggers
  const btnRouterLog = document.getElementById('btn-router-log');
  if (btnRouterLog) {
    btnRouterLog.addEventListener('click', openRouterLogModal);
  }
  const btnCloseRouterLog = document.getElementById('btn-close-router-log-modal');
  if (btnCloseRouterLog) {
    btnCloseRouterLog.addEventListener('click', closeRouterLogModal);
  }
  const modalRouterLog = document.getElementById('modal-router-log');
  if (modalRouterLog) {
    modalRouterLog.addEventListener('click', (e) => {
      if (e.target === modalRouterLog) closeRouterLogModal();
    });
  }
  const btnRefreshRouterLog = document.getElementById('btn-refresh-router-log');
  if (btnRefreshRouterLog) {
    btnRefreshRouterLog.addEventListener('click', fetchRouterOpenClashLog);
  }
  const btnCopyRouterLog = document.getElementById('btn-copy-router-log');
  if (btnCopyRouterLog) {
    btnCopyRouterLog.addEventListener('click', handleCopyRouterLog);
  }

  // Log auto-refresh toggle
  const logAutoRefreshCheck = document.getElementById('log-auto-refresh-check');
  if (logAutoRefreshCheck) {
    logAutoRefreshCheck.checked = state.logAutoRefresh;
    logAutoRefreshCheck.addEventListener('change', (e) => {
      state.logAutoRefresh = e.target.checked;
      localStorage.setItem('log_auto_refresh', e.target.checked ? 'true' : 'false');
      if (state.logAutoRefresh) {
        fetchLogs();
        showToast('系统日志已开启自动刷新');
      } else {
        showToast('系统日志已暂停自动刷新');
      }
    });
  }

  // Subscription Modal open / close
  const btnOpenSubs = document.getElementById('btn-open-subs');
  if (btnOpenSubs) {
    btnOpenSubs.addEventListener('click', openSubModal);
  }
  const btnCloseSubModal = document.getElementById('btn-close-sub-modal');
  if (btnCloseSubModal) {
    btnCloseSubModal.addEventListener('click', closeSubModal);
  }
  const modalSub = document.getElementById('modal-subscriptions');
  if (modalSub) {
    modalSub.addEventListener('click', (e) => {
      if (e.target === modalSub) closeSubModal();
    });
  }

  // Add Subscription button
  const btnAddSub = document.getElementById('btn-add-sub');
  if (btnAddSub) {
    btnAddSub.addEventListener('click', handleAddSubscription);
  }

  // Upload YAML file trigger and input
  const btnUploadYaml = document.getElementById('btn-upload-yaml-trigger');
  const fileUploadYaml = document.getElementById('file-upload-yaml');
  if (btnUploadYaml && fileUploadYaml) {
    btnUploadYaml.addEventListener('click', () => {
      fileUploadYaml.click();
    });
    fileUploadYaml.addEventListener('change', handleUploadYamlFile);
  }

  // Batch update subscriptions button
  const btnBatchUpdate = document.getElementById('btn-batch-update-subs');
  if (btnBatchUpdate) {
    btnBatchUpdate.addEventListener('click', handleBatchUpdateSubscriptions);
  }

  // Speed test targets config modal listeners
  const btnConfigWebSpeed = document.getElementById('btn-config-web-speed');
  if (btnConfigWebSpeed) {
    btnConfigWebSpeed.addEventListener('click', openSpeedConfigModal);
  }
  const btnCloseSpeedConfig = document.getElementById('btn-close-speed-config-modal');
  if (btnCloseSpeedConfig) {
    btnCloseSpeedConfig.addEventListener('click', closeSpeedConfigModal);
  }
  const btnCancelSpeedConfig = document.getElementById('btn-cancel-speed-config');
  if (btnCancelSpeedConfig) {
    btnCancelSpeedConfig.addEventListener('click', closeSpeedConfigModal);
  }
  const btnResetSpeedConfig = document.getElementById('btn-reset-speed-config');
  if (btnResetSpeedConfig) {
    btnResetSpeedConfig.addEventListener('click', handleResetSpeedConfig);
  }
  const btnSaveSpeedConfig = document.getElementById('btn-save-speed-config');
  if (btnSaveSpeedConfig) {
    btnSaveSpeedConfig.addEventListener('click', handleSaveSpeedConfig);
  }
  const modalSpeed = document.getElementById('modal-speed-config');
  if (modalSpeed) {
    modalSpeed.addEventListener('click', (e) => {
      if (e.target === modalSpeed) closeSpeedConfigModal();
    });
  }

  // Global Confirm Modal listeners
  const btnCloseGlobalConfirm = document.getElementById('btn-close-global-confirm');
  if (btnCloseGlobalConfirm) {
    btnCloseGlobalConfirm.addEventListener('click', () => {
      closeGlobalConfirmModal();
      showToast('已取消出海下载，未对 OpenClash 运行模式进行任何变更');
    });
  }
  const btnCancelGlobalConfirm = document.getElementById('btn-cancel-global-confirm');
  if (btnCancelGlobalConfirm) {
    btnCancelGlobalConfirm.addEventListener('click', () => {
      closeGlobalConfirmModal();
      showToast('已取消出海下载，未对 OpenClash 运行模式进行任何变更');
    });
  }
  const btnApproveGlobalConfirm = document.getElementById('btn-approve-global-confirm');
  if (btnApproveGlobalConfirm) {
    btnApproveGlobalConfirm.addEventListener('click', () => {
      const ctx = pendingGlobalDownload;
      closeGlobalConfirmModal();
      if (!ctx) return;
      if (ctx.type === 'add') {
        executeAddSubscription(ctx.name, ctx.url, true);
      } else if (ctx.type === 'update') {
        executeUpdateSub(ctx.filename, ctx.name, true);
      }
    });
  }
  const modalGlobalConfirm = document.getElementById('modal-global-confirm');
  if (modalGlobalConfirm) {
    modalGlobalConfirm.addEventListener('click', (e) => {
      if (e.target === modalGlobalConfirm) {
        closeGlobalConfirmModal();
        showToast('已取消出海下载，未对 OpenClash 运行模式进行任何变更');
      }
    });
  }

  // Global Escape key listener to close modals/dropdowns
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      closeAirportDropdown();
      closeSubModal();
      closeSpeedConfigModal();
      closeGlobalConfirmModal();
    }
  });

  // Connection search input (with 300ms debounce)
  const connSearch = document.getElementById('conn-search');
  if (connSearch) {
    let connSearchTimer = null;
    connSearch.addEventListener('input', (e) => {
      clearTimeout(connSearchTimer);
      connSearchTimer = setTimeout(() => {
        state.connSearchQuery = e.target.value.trim().toLowerCase();
        state.connPage = 1;
        fetchConnections();
      }, 300);
    });
  }

  // Retention setting auto-save on input change
  const retentionInput = document.getElementById('conn-retention-input');
  if (retentionInput) {
    retentionInput.addEventListener('change', handleSaveRetention);
  }

  // Connection export button
  const btnExportConns = document.getElementById('btn-export-conns');
  if (btnExportConns) {
    btnExportConns.addEventListener('click', handleExportConnections);
  }

  // Connection auto-refresh toggle
  const connAutoRefreshCheck = document.getElementById('conn-auto-refresh-check');
  if (connAutoRefreshCheck) {
    connAutoRefreshCheck.checked = state.connAutoRefresh;
    connAutoRefreshCheck.addEventListener('change', (e) => {
      state.connAutoRefresh = e.target.checked;
      localStorage.setItem('conn_auto_refresh', e.target.checked ? 'true' : 'false');
      if (state.connAutoRefresh) {
        fetchConnections();
        showToast('连接日志已开启自动刷新');
      } else {
        showToast('连接日志已暂停自动刷新');
      }
    });
  }

  // Connection clear button
  const btnClearConns = document.getElementById('btn-clear-conns');
  if (btnClearConns) {
    btnClearConns.addEventListener('click', handleClearConnections);
  }

  // Connection pagination buttons
  const btnConnFirst = document.getElementById('btn-conn-first');
  if (btnConnFirst) {
    btnConnFirst.addEventListener('click', () => {
      if (state.connPage > 1) {
        state.connPage = 1;
        fetchConnections();
      }
    });
  }
  const btnConnPrev = document.getElementById('btn-conn-prev');
  if (btnConnPrev) {
    btnConnPrev.addEventListener('click', () => {
      if (state.connPage > 1) {
        state.connPage--;
        fetchConnections();
      }
    });
  }
  const btnConnNext = document.getElementById('btn-conn-next');
  if (btnConnNext) {
    btnConnNext.addEventListener('click', () => {
      if (state.connPage < state.connTotalPages) {
        state.connPage++;
        fetchConnections();
      }
    });
  }
  const btnConnLast = document.getElementById('btn-conn-last');
  if (btnConnLast) {
    btnConnLast.addEventListener('click', () => {
      if (state.connPage < state.connTotalPages) {
        state.connPage = state.connTotalPages;
        fetchConnections();
      }
    });
  }

  // Connection page size input
  const pageSizeInput = document.getElementById('conn-page-size');
  if (pageSizeInput) {
    pageSizeInput.addEventListener('change', () => {
      let sz = parseInt(pageSizeInput.value, 10);
      if (!sz || sz <= 0) sz = 200;
      pageSizeInput.value = sz;
      state.connLimit = sz;
      state.connPage = 1;
      fetchConnections();
    });
    pageSizeInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        pageSizeInput.blur();
      }
    });
  }

  // Connection page select dropdown
  const pageSelect = document.getElementById('conn-page-select');
  if (pageSelect) {
    pageSelect.addEventListener('change', () => {
      const p = parseInt(pageSelect.value, 10);
      if (p && p !== state.connPage) {
        state.connPage = p;
        fetchConnections();
      }
    });
  }

  // Environment variables editor buttons
  const btnInitEnv = document.getElementById('btn-init-env');
  if (btnInitEnv) {
    btnInitEnv.addEventListener('click', handleInitEnv);
  }
  const btnReloadEnv = document.getElementById('btn-reload-env');
  if (btnReloadEnv) {
    btnReloadEnv.addEventListener('click', handleReloadEnv);
  }
  const btnSaveEnv = document.getElementById('btn-save-env');
  if (btnSaveEnv) {
    btnSaveEnv.addEventListener('click', handleSaveEnv);
  }

  // Environment variables editor Tab key indentation
  const envEditor = document.getElementById('env-editor');
  if (envEditor) {
    envEditor.addEventListener('keydown', (e) => {
      if (e.key === 'Tab') {
        e.preventDefault();
        const start = envEditor.selectionStart;
        const end = envEditor.selectionEnd;
        envEditor.value = envEditor.value.substring(0, start) + '  ' + envEditor.value.substring(end);
        envEditor.selectionStart = envEditor.selectionEnd = start + 2;
      }
    });
  }
}

// ----------------------------------------------------
// View Navigation
// ----------------------------------------------------
function switchView(viewName) {
  state.currentView = viewName;

  document.querySelectorAll('.nav-item').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.view === viewName);
  });

  document.querySelectorAll('.view-panel').forEach(panel => {
    panel.style.display = panel.id === `view-${viewName}` ? 'flex' : 'none';
  });

  if (viewName === 'logs') {
    const errorLogs = (state.logs || []).filter(l => l.level === 'ERROR');
    if (errorLogs.length > 0) {
      const maxErrorId = Math.max(...errorLogs.map(l => l.id || 0));
      state.lastViewedErrorId = Math.max(state.lastViewedErrorId || 0, maxErrorId);
      localStorage.setItem('last_viewed_error_id', String(state.lastViewedErrorId));
    }
    updateLogDot();
    fetchLogs();
  } else if (viewName === 'connections') {
    fetchConnections();
    updateLogDot();
  } else if (viewName === 'dashboard') {
    refreshAll();
    updateLogDot();
  } else if (viewName === 'env') {
    fetchEnv();
    updateLogDot();
  }
}

// ----------------------------------------------------
// Theme: Strictly Follows System (prefers-color-scheme)
// ----------------------------------------------------
function initTheme() {
  const root = document.documentElement;
  root.removeAttribute('data-theme');
  localStorage.removeItem('ow_theme');
}

// ----------------------------------------------------
// API Data Fetching
// ----------------------------------------------------
async function refreshAll() {
  await Promise.all([
    fetchStatus(),
    fetchProfiles(),
    fetchProxies(),
    fetchLogs(),
  ]);
  handleTestWebSpeed();
}

async function fetchStatus() {
  const wasOnline = state.status?.clash_online;

  try {
    const res = await fetch('/api/status');
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();
    state.status = data;
    if (data.poll_interval && data.poll_interval !== state.pollIntervalSec) {
      state.pollIntervalSec = data.poll_interval;
      startPolling();
    }
    updateStatusUI();

    // Auto-detect when OpenClash comes online (e.g. user manually started it in OpenWrt)
    if (data.clash_online && (!wasOnline || state.groups.length === 0) && !state.isAirportSwitching) {
      fetchProxies();
      fetchProfiles();
    }
    // Auto-detect when OpenClash went offline
    if (!data.clash_online && wasOnline && !state.isAirportSwitching) {
      fetchProxies();
    }
  } catch (err) {
    console.error('Fetch status error:', err);
    updateOfflineUI();
  }
}

async function fetchProfiles() {
  try {
    const res = await fetch('/api/profiles');
    if (!res.ok) return;
    const data = await res.json();
    state.profiles = data.profiles || [];
    renderProfiles();
  } catch (err) {
    console.error('Fetch profiles error:', err);
  }
}

async function fetchProxies() {
  const groupsContainer = document.getElementById('groups-grid');
  const nodesContainer = document.getElementById('nodes-grid');

  // If airport is switching, NEVER overwrite with error or empty messages
  if (state.isAirportSwitching) {
    return;
  }

  // If service is currently disabled or offline and not transitioning, display stopped message
  if (state.status && !state.status.is_enabled && !state.status.clash_online && !state.isPowerTransitioning) {
    state.groups = [];
    if (groupsContainer) {
      groupsContainer.innerHTML = `
        <div class="empty-state">
          <p style="font-size: 14px; font-weight: 500; margin-bottom: 6px;">启动后才能获取数据</p>
          <p style="font-size: 12px; color: var(--text-muted);">服务就绪后将自动载入配置组</p>
        </div>
      `;
    }
    if (nodesContainer) {
      nodesContainer.innerHTML = `
        <div class="empty-state">
          <p style="font-size: 14px; font-weight: 500; margin-bottom: 6px;">启动后才能获取数据</p>
          <p style="font-size: 12px; color: var(--text-muted);">服务就绪后将自动载入代理节点列表</p>
        </div>
      `;
    }
    return;
  }

  try {
    const res = await fetch('/api/proxies');
    if (!res.ok) {
      if (state.isAirportSwitching) return;
      state.groups = [];
      if (groupsContainer) {
        groupsContainer.innerHTML = `
          <div class="empty-state">
            <p style="font-size: 14px; font-weight: 500; margin-bottom: 6px;">启动后才能获取数据</p>
            <p style="font-size: 12px; color: var(--text-muted);">Clash 核心正在启动或重载中，稍后将自动同步</p>
          </div>
        `;
      }
      if (nodesContainer) {
        nodesContainer.innerHTML = `
          <div class="empty-state">
            <p style="font-size: 14px; font-weight: 500; margin-bottom: 6px;">启动后才能获取数据</p>
            <p style="font-size: 12px; color: var(--text-muted);">Clash 核心正在启动或重载中，稍后将自动同步</p>
          </div>
        `;
      }
      return;
    }
    const data = await res.json();
    state.groups = data.groups || [];

    if (state.groups.length > 0) {
      const exists = state.groups.some(g => g.name === state.activeGroupName);
      if (!exists || !state.activeGroupName) {
        state.activeGroupName = state.groups[0].name;
      }
      renderGroups();
      renderNodes();
    } else {
      if (groupsContainer) groupsContainer.innerHTML = `<div class="empty-state">当前配置文件无可用配置组</div>`;
      if (nodesContainer) nodesContainer.innerHTML = `<div class="empty-state">当前配置文件无可用代理节点</div>`;
    }
  } catch (err) {
    if (state.isAirportSwitching) return;
    console.error('Fetch proxies error:', err);
    state.groups = [];
    if (groupsContainer) groupsContainer.innerHTML = `<div class="empty-state">启动后才能获取数据</div>`;
    if (nodesContainer) nodesContainer.innerHTML = `<div class="empty-state">启动后才能获取数据</div>`;
  }
}

async function fetchLogs() {
  try {
    const res = await fetch('/api/logs');
    if (!res.ok) return;
    const data = await res.json();
    state.logs = data.logs || [];
    renderLogs();
    updateLogDot();
  } catch (err) {
    console.error('Fetch logs error:', err);
  }
}

// ----------------------------------------------------
// UI Rendering & State Updates
// ----------------------------------------------------
function updateStatusUI() {
  const s = state.status;
  if (!s) return;

  const dot = document.getElementById('status-dot');
  const text = document.getElementById('status-text');
  const powerDisplay = document.getElementById('power-status-display');
  const powerMeta = document.getElementById('power-meta');
  const powerBtn = document.getElementById('btn-power-toggle');
  const powerBtnText = document.getElementById('power-btn-text');
  const activeNodeDisplay = document.getElementById('active-node-display');
  const activeGroupMeta = document.getElementById('active-group-meta');
  const sshNoticeCard = document.getElementById('ssh-notice-card');

  // Header status pill (outside nav box, if present)
  if (dot && text) {
    if (s.clash_online) {
      dot.className = 'status-dot online';
      text.textContent = '在线';
    } else {
      dot.className = 'status-dot offline';
      text.textContent = '离线 / 未响应';
    }
  }

  // Power Switch Status & Intermediate State Handling
  if (state.isAirportSwitching) {
    powerBtn.disabled = true;
    powerDisplay.textContent = '重载配置中...';
    powerDisplay.className = 'power-status-value transitioning';
    powerBtnText.textContent = '切换中...';
  } else if (state.isPowerTransitioning) {
    powerBtn.disabled = true;
    if (state.transitionTarget === 'enable') {
      powerDisplay.textContent = '启动中...';
      powerDisplay.className = 'power-status-value transitioning';
      powerBtnText.textContent = '启动中...';
      powerBtn.className = 'btn-power';
    } else {
      powerDisplay.textContent = '停止中...';
      powerDisplay.className = 'power-status-value transitioning';
      powerBtnText.textContent = '停止中...';
      powerBtn.className = 'btn-power btn-off';
    }
  } else {
    powerBtn.disabled = false;
    if (s.is_enabled) {
      powerDisplay.textContent = '运行中';
      powerDisplay.className = 'power-status-value enabled';
      powerBtn.className = 'btn-power btn-off';
      powerBtnText.textContent = '停止服务';
    } else {
      powerDisplay.textContent = '已停止';
      powerDisplay.className = 'power-status-value disabled';
      powerBtn.className = 'btn-power';
      powerBtnText.textContent = '启动服务';
    }
  }

  powerMeta.textContent = `模式: ${s.mode || '未知'} | 路由: ${s.router_host}`;

  // Exit node display: when stopped, show stopped status!
  if (!s.is_enabled && !s.clash_online) {
    if (activeNodeDisplay) activeNodeDisplay.textContent = '服务已停止';
    if (activeGroupMeta) activeGroupMeta.textContent = '当前未运行代理';
  } else if (!activeNodeDisplay.textContent || activeNodeDisplay.textContent === '-' || activeNodeDisplay.textContent === '服务已停止') {
    activeNodeDisplay.textContent = s.primary_node || '自动选择';
    activeGroupMeta.textContent = `策略组: ${s.primary_group || '默认'}`;
  }

  // Retention setting sync
  if (s.conn_log_retention_days) {
    state.connRetentionDays = s.conn_log_retention_days;
    const input = document.getElementById('conn-retention-input');
    if (input && document.activeElement !== input) {
      input.value = s.conn_log_retention_days;
    }
  }

  // Sync active profile selection if needed
  if (s.active_profile && !state.isAirportSwitching && state.profiles) {
    let changed = false;
    state.profiles.forEach(p => {
      const active = (p.filename === s.active_profile || p.name === s.active_profile);
      if (p.is_active !== active) {
        p.is_active = active;
        changed = true;
      }
    });
    if (changed) renderProfiles();
  }

  // SSH notice display
  if (!s.openwrt_ssh_configured) {
    sshNoticeCard.style.display = 'flex';
  } else {
    sshNoticeCard.style.display = 'none';
  }
}

function updateOfflineUI() {
  const dot = document.getElementById('status-dot');
  const text = document.getElementById('status-text');
  if (dot && text) {
    dot.className = 'status-dot offline';
    text.textContent = '无法连接服务';
  }
}

// Relative time formatter with largest time unit countdown
function formatRelativeTime(val) {
  if (!val) return '';
  let date = null;
  if (typeof val === 'number') {
    date = new Date(val > 1e11 ? val : val * 1000);
  } else if (typeof val === 'string') {
    const m = val.match(/^(\d{2})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})$/);
    if (m) {
      const year = 2000 + parseInt(m[1], 10);
      const month = parseInt(m[2], 10) - 1;
      const day = parseInt(m[3], 10);
      const hour = parseInt(m[4], 10);
      const min = parseInt(m[5], 10);
      const sec = parseInt(m[6], 10);
      date = new Date(year, month, day, hour, min, sec);
    } else {
      date = new Date(val);
    }
  }
  if (!date || isNaN(date.getTime())) return '';

  const now = Date.now();
  const diffSec = Math.max(0, Math.floor((now - date.getTime()) / 1000));

  if (diffSec < 60) return `${Math.max(1, diffSec)} 秒前`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin} 分钟前`;
  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) return `${diffHour} 小时前`;
  const diffDay = Math.floor(diffHour / 24);
  if (diffDay < 30) return `${diffDay} 天前`;
  const diffMonth = Math.floor(diffDay / 30);
  if (diffMonth < 12) return `${diffMonth} 个月前`;
  const diffYear = Math.floor(diffDay / 365);
  return `${Math.max(1, diffYear)} 年前`;
}

// Extract clean hostname / domain from URL
function extractDomain(urlStr) {
  if (!urlStr) return '';
  try {
    const u = new URL(urlStr);
    return u.hostname;
  } catch {
    const m = urlStr.match(/:\/\/(.[^/:]+)/);
    return m ? m[1] : '';
  }
}

// Render airport/subscription item HTML (Shared between Custom Dropdown and Modal)
function renderAirportItemHTML(s, isDropdown = false) {
  const activeClass = s.is_active ? 'active' : '';
  const isSub = Boolean(s.url && s.url.length > 0);
  const domain = s.domain || extractDomain(s.url);
  const relativeTime = formatRelativeTime(s.updated_at);
  const sizeStr = s.size ? formatBytes(s.size) : '';
  const usageStr = s.usage_text || '';

  let metaParts = [];
  if (isSub) {
    if (domain) metaParts.push(escapeHTML(domain));
  } else {
    metaParts.push(escapeHTML(s.filename || s.name));
    if (sizeStr) metaParts.push(sizeStr);
  }
  if (relativeTime) metaParts.push(relativeTime);
  if (usageStr) metaParts.push(escapeHTML(usageStr));

  const metaHTML = metaParts.join(' | ');

  return `
    <div class="sub-item ${activeClass}" onclick="handleSelectAirport('${escapeHTML(s.filename || s.name)}', '${escapeHTML(s.name)}')">
      <div class="sub-info">
        <div class="sub-name-row">
          <span class="sub-name">${escapeHTML(s.name)}</span>
        </div>
        <div class="sub-meta">
          ${metaHTML}
        </div>
      </div>
      ${(!isDropdown) ? `
        <div class="sub-actions">
          ${isSub ? `
            <button class="btn-sub-action" title="更新" aria-label="更新" onclick="event.stopPropagation(); handleUpdateSub('${escapeHTML(s.filename || s.name)}', '${escapeHTML(s.name)}')">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M21 2v6h-6"></path>
                <path d="M3 12a9 9 0 0 1 15-6.7L21 8"></path>
                <path d="M3 22v-6h6"></path>
                <path d="M21 12a9 9 0 0 1-15 6.7L3 16"></path>
              </svg>
            </button>
          ` : ''}
          <button class="btn-sub-action btn-sub-del" title="删除" aria-label="删除" onclick="event.stopPropagation(); handleDeleteSub('${escapeHTML(s.filename || s.name)}', '${escapeHTML(s.name)}')">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="3 6 5 6 21 6"></polyline>
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
              <line x1="10" y1="11" x2="10" y2="17"></line>
              <line x1="14" y1="11" x2="14" y2="17"></line>
            </svg>
          </button>
        </div>
      ` : ''}
    </div>
  `;
}

function toggleAirportDropdown(e) {
  if (e) e.stopPropagation();
  const dropdown = document.getElementById('airport-custom-dropdown');
  const wrap = document.getElementById('airport-custom-select-wrap');
  if (!dropdown || !wrap) return;
  const isOpen = dropdown.style.display !== 'none';
  if (isOpen) {
    dropdown.style.display = 'none';
    wrap.classList.remove('open');
  } else {
    dropdown.style.display = 'block';
    wrap.classList.add('open');
  }
}

function closeAirportDropdown() {
  const dropdown = document.getElementById('airport-custom-dropdown');
  const wrap = document.getElementById('airport-custom-select-wrap');
  if (dropdown) dropdown.style.display = 'none';
  if (wrap) wrap.classList.remove('open');
}

async function handleSelectAirport(filename, name) {
  closeAirportDropdown();
  closeSubModal();
  await switchAirportProfile(filename, name);
}

// Airport profiles rendering: all valid airport configurations
function renderProfiles() {
  const titleEl = document.getElementById('airport-card-title');
  const triggerName = document.getElementById('airport-trigger-name');
  const triggerMeta = document.getElementById('airport-trigger-meta');
  const optionsContainer = document.getElementById('airport-custom-options');

  const airports = state.profiles || [];

  if (titleEl) {
    titleEl.textContent = `选择机场(总数${airports.length})`;
  }

  if (airports.length === 0) {
    const isClashOnline = state.status && state.status.clash_online;
    if (triggerName) triggerName.textContent = isClashOnline ? '暂无可用机场配置' : '等待服务启动...';
    if (triggerMeta) triggerMeta.textContent = '';
    if (optionsContainer) optionsContainer.innerHTML = '<div class="empty-state">暂无可用机场配置</div>';
    return;
  }

  // Find active profile
  const activeProfile = airports.find(p => p.is_active) || airports[0];
  if (triggerName && activeProfile) {
    triggerName.textContent = activeProfile.name || '已选机场';
    const isSub = Boolean(activeProfile.url && activeProfile.url.length > 0);
    const domain = activeProfile.domain || extractDomain(activeProfile.url);
    const relTime = formatRelativeTime(activeProfile.updated_at);
    let parts = [];
    if (isSub && domain) parts.push(domain);
    if (activeProfile.usage_text) parts.push(activeProfile.usage_text);
    else if (relTime) parts.push(relTime);
    if (triggerMeta) triggerMeta.textContent = parts.join(' | ');
  }

  // Render dropdown items using shared renderAirportItemHTML
  if (optionsContainer) {
    optionsContainer.innerHTML = airports.map(p => renderAirportItemHTML(p, true)).join('');
  }
}

// Strategy Groups Rendering (Displayed as boxes above "切换节点")
function renderGroups() {
  const container = document.getElementById('groups-grid');
  if (!container) return;

  if (!state.groups || state.groups.length === 0) {
    container.innerHTML = '<div class="empty-state">当前无可用配置组</div>';
    return;
  }

  container.innerHTML = state.groups.map(g => {
    const isCurrentActive = g.name === state.activeGroupName;
    const activeClass = isCurrentActive ? 'active' : '';
    return `
      <div class="group-card ${activeClass}" data-name="${escapeHTML(g.name)}">
        <div class="group-card-header">
          <div class="group-card-title">${escapeHTML(g.name)}</div>
          <div class="group-card-type">${escapeHTML(g.type || 'Selector')}</div>
        </div>
        <div class="group-card-body">
          <div class="group-card-current">当前: <span class="group-current-val">${escapeHTML(g.current || '自动')}</span></div>
          <div class="group-card-meta">${g.nodes ? g.nodes.length : 0} 个节点</div>
        </div>
      </div>
    `;
  }).join('');

  container.querySelectorAll('.group-card').forEach(card => {
    card.addEventListener('click', () => {
      const name = card.dataset.name;
      if (state.activeGroupName === name) return;
      state.activeGroupName = name;
      renderGroups();
      renderNodes();
    });
  });
}

// Proxy Nodes Rendering
function renderNodes() {
  const container = document.getElementById('nodes-grid');
  const group = state.groups.find(g => g.name === state.activeGroupName) || state.groups[0];
  const sectionTitle = document.getElementById('nodes-section-title');

  if (!group || !group.nodes || group.nodes.length === 0) {
    if (sectionTitle) sectionTitle.textContent = '切换节点';
    container.innerHTML = `<div class="empty-state">当前配置组无可用节点</div>`;
    return;
  }

  if (sectionTitle) {
    sectionTitle.textContent = `切换节点 (${group.name})`;
  }

  // Update top exit node to match the current group selection
  if (group.current) {
    const activeNodeDisplay = document.getElementById('active-node-display');
    const activeGroupMeta = document.getElementById('active-group-meta');
    if (activeNodeDisplay) activeNodeDisplay.textContent = group.current;
    if (activeGroupMeta) activeGroupMeta.textContent = `策略组: ${group.name}`;
  }

  let filtered = group.nodes;
  if (state.searchQuery) {
    filtered = filtered.filter(n => n.name.toLowerCase().includes(state.searchQuery));
  }

  if (filtered.length === 0) {
    container.innerHTML = `<div class="empty-state">没有匹配 "${escapeHTML(state.searchQuery)}" 的节点</div>`;
    return;
  }

  container.innerHTML = filtered.map(node => {
    const isCurrent = node.name === group.current;
    const activeClass = isCurrent ? 'active' : '';
    
    const delay = state.delays[node.name] !== undefined ? state.delays[node.name] : node.delay;
    const { delayClass, delayText } = formatDelay(delay);

    return `
      <div class="node-card ${activeClass}" data-group="${escapeHTML(group.name)}" data-name="${escapeHTML(node.name)}">
        <div class="node-main">
          <div class="node-name" title="${escapeHTML(node.name)}">${escapeHTML(node.name)}</div>
          <div class="node-type">${escapeHTML(node.type || 'Proxy')}</div>
        </div>
        <div class="node-right">
          <span class="delay-tag ${delayClass}">${delayText}</span>
        </div>
      </div>
    `;
  }).join('');

  container.querySelectorAll('.node-card').forEach(card => {
    card.addEventListener('click', async () => {
      const gName = card.dataset.group;
      const nName = card.dataset.name;
      if (card.classList.contains('active')) return;
      await selectNode(gName, nName);
    });
  });
}

// Latency text formatting (displays "未测速" instead of cryptic "-")
function formatDelay(delay) {
  if (delay === undefined || delay === 0 || delay === null) {
    return { delayClass: 'untested', delayText: '未测速' };
  }
  if (delay === -1) {
    return { delayClass: 'timeout', delayText: '超时' };
  }
  if (delay < 200) {
    return { delayClass: 'fast', delayText: `${delay}ms` };
  }
  if (delay < 500) {
    return { delayClass: 'medium', delayText: `${delay}ms` };
  }
  return { delayClass: 'slow', delayText: `${delay}ms` };
}

// ----------------------------------------------------
// Log Rendering (Clean Terminal Monospace Text)
// ----------------------------------------------------
function renderLogs() {
  const container = document.getElementById('log-terminal');
  if (!state.logs || state.logs.length === 0) {
    container.innerHTML = `<div class="empty-state" style="color: #8b949e;">暂无日志记录</div>`;
    return;
  }

  let filtered = state.logs;

  if (state.logFilterLevel && state.logFilterLevel !== 'ALL') {
    filtered = filtered.filter(l => l.level === state.logFilterLevel);
  }

  if (state.logSearchQuery) {
    filtered = filtered.filter(l => 
      l.message.toLowerCase().includes(state.logSearchQuery) || 
      (l.detail && l.detail.toLowerCase().includes(state.logSearchQuery)) ||
      l.source.toLowerCase().includes(state.logSearchQuery)
    );
  }

  if (filtered.length === 0) {
    container.innerHTML = `<div class="empty-state" style="color: #8b949e;">没有符合筛选条件的日志</div>`;
    return;
  }

  container.innerHTML = filtered.slice().reverse().map(l => {
    const timeStr = escapeHTML(l.time || '');
    const lvl = escapeHTML(l.level || 'INFO');
    const src = escapeHTML(l.source || 'SYS');
    const msg = escapeHTML(l.message || '');
    const detailStr = l.detail ? `\n  ${escapeHTML(l.detail)}` : '';
    return `<div class="log-line">${timeStr} <span class="log-tag tag-${lvl}">[${lvl}]</span> [${src}] ${msg}${detailStr}</div>`;
  }).join('');
}

function updateLogDot() {
  const dot = document.getElementById('nav-log-dot');
  if (!dot) return;

  const errorLogs = (state.logs || []).filter(l => l.level === 'ERROR');
  const maxErrorId = errorLogs.length > 0 ? Math.max(...errorLogs.map(l => l.id || 0)) : 0;

  // On first run without existing record, mark all current historical errors as viewed
  if (state.lastViewedErrorId === null) {
    state.lastViewedErrorId = maxErrorId;
    localStorage.setItem('last_viewed_error_id', String(maxErrorId));
  }

  // If user is currently viewing system logs, keep lastViewedErrorId updated and hide dot
  if (state.currentView === 'logs') {
    if (maxErrorId > state.lastViewedErrorId) {
      state.lastViewedErrorId = maxErrorId;
      localStorage.setItem('last_viewed_error_id', String(maxErrorId));
    }
    dot.style.display = 'none';
    return;
  }

  // Only display the red dot if there are new unviewed errors AND current view is not logs
  if (maxErrorId > state.lastViewedErrorId) {
    dot.style.display = 'inline-block';
  } else {
    dot.style.display = 'none';
  }
}

// Download text or CSV content as a file
function downloadTextFile(content, filename, mimeType = 'text/plain;charset=utf-8') {
  const blob = new Blob([content], { type: mimeType });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

// Export plain text system logs
function handleExportLogs() {
  if (!state.logs || state.logs.length === 0) {
    showToast('暂无系统日志可供导出');
    return;
  }
  let filtered = state.logs;
  if (state.logFilterLevel && state.logFilterLevel !== 'ALL') {
    filtered = filtered.filter(l => l.level === state.logFilterLevel);
  }
  if (state.logSearchQuery) {
    filtered = filtered.filter(l => 
      (l.message || '').toLowerCase().includes(state.logSearchQuery) || 
      (l.detail && l.detail.toLowerCase().includes(state.logSearchQuery)) ||
      (l.source || '').toLowerCase().includes(state.logSearchQuery)
    );
  }
  if (filtered.length === 0) {
    showToast('没有符合筛选条件的日志可供导出');
    return;
  }

  const lines = filtered.map(l => {
    let line = `${l.time || ''} [${l.level || 'INFO'}] [${l.source || 'SYS'}] ${l.message || ''}`;
    if (l.detail) {
      line += `\n  ${l.detail}`;
    }
    return line;
  });

  const now = new Date();
  const dateStr = now.toISOString().slice(0, 10).replace(/-/g, '');
  downloadTextFile(lines.join('\n'), `openclash-logs-${dateStr}.txt`, 'text/plain;charset=utf-8');
  showToast(`已导出 ${filtered.length} 条系统日志`);
}

// ----------------------------------------------------
// Router OpenClash Logs Modal Actions
// ----------------------------------------------------
async function openRouterLogModal() {
  const modal = document.getElementById('modal-router-log');
  if (!modal) return;
  modal.style.display = 'flex';
  await fetchRouterOpenClashLog();
}

function closeRouterLogModal() {
  const modal = document.getElementById('modal-router-log');
  if (modal) modal.style.display = 'none';
}

async function fetchRouterOpenClashLog() {
  const term = document.getElementById('router-log-content');
  if (!term) return;
  term.textContent = '正在通过 SSH 读取 OpenWrt 路由器本地日志 (/tmp/openclash.log)...';

  try {
    const res = await fetch('/api/openwrt/openclash-log');
    const data = await res.json();
    if (data.success && data.content) {
      term.textContent = data.content;
      term.scrollTop = term.scrollHeight;
    } else {
      term.textContent = data.error || '未能获取到日志（可能 OpenClash 尚未运行或 SSH 未配置）';
    }
  } catch (err) {
    term.textContent = '请求异常: ' + err.message;
  }
}

function handleCopyRouterLog() {
  const term = document.getElementById('router-log-content');
  if (!term || !term.textContent) return;
  navigator.clipboard.writeText(term.textContent).then(() => {
    showToast('路由器日志已复制到剪贴板');
  }).catch(() => {
    showToast('复制失败，请手动选择文本复制');
  });
}

// ----------------------------------------------------
// User Actions
// ----------------------------------------------------
async function handlePowerToggle() {
  if (state.isPowerTransitioning || state.isAirportSwitching) return;

  const targetAction = (state.status && state.status.is_enabled) ? 'disable' : 'enable';
  state.isPowerTransitioning = true;
  state.transitionTarget = targetAction;

  const powerDisplay = document.getElementById('power-status-display');
  const powerBtn = document.getElementById('btn-power-toggle');
  const powerBtnText = document.getElementById('power-btn-text');

  powerBtn.disabled = true;
  if (targetAction === 'enable') {
    powerDisplay.textContent = '启动中...';
    powerDisplay.className = 'power-status-value transitioning';
    powerBtnText.textContent = '启动中...';
    powerBtn.className = 'btn-power';
    showToast('正在启动服务...');
  } else {
    powerDisplay.textContent = '停止中...';
    powerDisplay.className = 'power-status-value transitioning';
    powerBtnText.textContent = '停止中...';
    powerBtn.className = 'btn-power btn-off';
    showToast('正在停止服务...');
  }

  try {
    const res = await fetch('/api/power', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: targetAction }),
    });
    const data = await res.json();
    if (data.success) {
      showToast(targetAction === 'enable' ? '启动指令已下发，正在初始化...' : '服务已停止');
    } else {
      showToast('操作失败: ' + (data.error || '未知错误'));
    }
  } catch (err) {
    showToast('请求异常: ' + err.message);
  }

  // Poll state repeatedly until transition settles
  let attempts = 0;
  const maxAttempts = (targetAction === 'enable') ? 16 : 8;
  const pollTransition = async () => {
    attempts++;
    await fetchStatus();
    await fetchLogs();
    
    const isTargetReached = (targetAction === 'enable') 
      ? (state.status?.is_enabled && state.status?.clash_online) 
      : (!state.status?.is_enabled && !state.status?.clash_online);

    if (isTargetReached || attempts >= maxAttempts) {
      state.isPowerTransitioning = false;
      updateStatusUI();
      await fetchProxies();
      if (!isTargetReached && targetAction === 'enable') {
        showToast('启动超时或未就绪，请在“系统日志”中查看具体错误');
      }
    } else {
      setTimeout(pollTransition, 1500);
    }
  };

  setTimeout(pollTransition, 1500);
}

// Airport Switching with Smooth Reconnection and Proxy Reload
async function switchAirportProfile(filename, name) {
  if (state.isAirportSwitching) return;
  state.isAirportSwitching = true;

  // Immediately reflect selected airport in UI without waiting for reload
  if (state.profiles && state.profiles.length > 0) {
    state.profiles.forEach(p => {
      p.is_active = (p.filename === filename || p.name === name);
    });
    renderProfiles();
  }
  if (state.subscriptions && state.subscriptions.length > 0) {
    state.subscriptions.forEach(s => {
      s.is_active = (s.filename === filename || s.name === name);
    });
    renderSubscriptions();
  }

  const powerDisplay = document.getElementById('power-status-display');
  const powerBtn = document.getElementById('btn-power-toggle');
  const airportTrigger = document.getElementById('airport-custom-trigger');

  if (powerBtn) powerBtn.disabled = true;
  if (airportTrigger) airportTrigger.style.pointerEvents = 'none';
  powerDisplay.textContent = '重载配置中...';
  powerDisplay.className = 'power-status-value transitioning';

  const groupsContainer = document.getElementById('groups-grid');
  const nodesContainer = document.getElementById('nodes-grid');
  if (groupsContainer) groupsContainer.innerHTML = '<div class="empty-state">正在切换机场并重载配置组...</div>';
  if (nodesContainer) nodesContainer.innerHTML = '<div class="empty-state">正在重载节点列表...</div>';

  showToast(`正在切换至机场: ${name}...`);

  try {
    const res = await fetch('/api/profiles/switch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ filename }),
    });
    const data = await res.json();
    if (data.success) {
      showToast('配置切换指令已下发，正在重载 OpenClash...');
    } else {
      showToast(`切换失败: ${data.error || '未知错误'}`);
    }
  } catch (err) {
    showToast('切换异常: ' + err.message);
  }

  let isFinished = false;
  const finishSwitch = async () => {
    if (isFinished) return;
    isFinished = true;
    clearTimeout(watchdog);
    state.isAirportSwitching = false;
    if (powerBtn) powerBtn.disabled = false;
    if (airportTrigger) airportTrigger.style.pointerEvents = 'auto';
    state.activeGroupName = '';
    updateStatusUI();
    await fetchProxies();
    await fetchProfiles();
    if (state.status?.clash_online) {
      showToast(`机场 [${name}] 载入完成`);
    } else {
      showToast(`机场 [${name}] 重载后核心未上线，请在“系统日志”中查看具体报错`);
    }
  };

  // Watchdog timer: max 15 seconds to prevent ever hanging
  const watchdog = setTimeout(() => {
    if (!isFinished) {
      finishSwitch();
    }
  }, 15000);

  // Poll until OpenClash finishes restarting and core API is back online
  let attempts = 0;
  const pollReload = async () => {
    if (isFinished) return;
    attempts++;
    try {
      await fetchStatus();
      await fetchLogs();
    } catch (e) {}

    // The core is back online or max attempts reached
    if (state.status?.clash_online || attempts >= 8) {
      await new Promise(r => setTimeout(r, 800));
      await finishSwitch();
    } else {
      setTimeout(pollReload, 1500);
    }
  };

  setTimeout(pollReload, 2000);
}

// Update provider subscription directly via Clash Core REST API
async function updateProviderSubscription(name) {
  showToast(`正在更新同步机场订阅: ${name}...`);
  try {
    const res = await fetch('/api/profiles/update', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    });
    const data = await res.json();
    if (data.success) {
      showToast(`机场订阅 [${name}] 同步更新完成`);
      await fetchProxies();
      await fetchLogs();
    } else {
      showToast(`更新失败: ${data.error || '未知错误'}`);
      await fetchLogs();
    }
  } catch (err) {
    showToast('更新异常: ' + err.message);
  }
}

async function selectNode(groupName, name) {
  try {
    const res = await fetch('/api/proxies/select', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ group: groupName, name }),
    });
    const data = await res.json();
    if (data.success) {
      showToast(`已选中: ${name}`);
      const g = state.groups.find(x => x.name === groupName);
      if (g) g.current = name;

      // Synchronize top exit node display in real time
      const activeNodeDisplay = document.getElementById('active-node-display');
      const activeGroupMeta = document.getElementById('active-group-meta');
      if (activeNodeDisplay) activeNodeDisplay.textContent = name;
      if (activeGroupMeta) activeGroupMeta.textContent = `策略组: ${groupName}`;

      renderGroups();
      renderNodes();
      fetchLogs();
    } else {
      showToast('切换节点失败: ' + (data.error || ''));
      fetchLogs();
    }
  } catch (err) {
    showToast('节点切换异常: ' + err.message);
  }
}

// Latency Testing
async function handleTestLatency() {
  if (state.isTestingLatency) return;

  const group = state.groups.find(g => g.name === state.activeGroupName) || state.groups[0];
  if (!group || !group.nodes || group.nodes.length === 0) {
    showToast('当前策略组无可用节点可供测速');
    return;
  }

  state.isTestingLatency = true;
  const btn = document.getElementById('btn-test-latency');
  const btnText = document.getElementById('test-latency-text');
  btn.disabled = true;
  btnText.textContent = '测速中...';

  const nodeNames = group.nodes.map(n => n.name);
  showToast(`开始测试 ${nodeNames.length} 个节点的延迟...`);

  try {
    const res = await fetch('/api/proxies/delay', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ nodes: nodeNames }),
    });
    const data = await res.json();
    if (data.results) {
      Object.entries(data.results).forEach(([name, resItem]) => {
        state.delays[name] = resItem.delay;
      });
      showToast('测速完成');
      renderNodes();
      fetchLogs();
    }
  } catch (err) {
    showToast('测速异常: ' + err.message);
  } finally {
    state.isTestingLatency = false;
    btn.disabled = false;
    btnText.textContent = '测速';
  }
}

// ----------------------------------------------------
// UI Helpers
// ----------------------------------------------------
function showToast(msg) {
  const toast = document.getElementById('toast');
  toast.textContent = msg;
  toast.classList.add('show');
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => {
    toast.classList.remove('show');
  }, 2400);
}

function escapeHTML(str) {
  if (!str) return '';
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

function formatBytes(bytes) {
  if (!bytes || bytes <= 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  if (i <= 0) return bytes + ' B';
  return (bytes / Math.pow(k, i)).toFixed(1) + ' ' + sizes[i];
}

// ----------------------------------------------------
// Connection Logs Logic
// ----------------------------------------------------
async function fetchConnections() {
  const tbody = document.getElementById('conn-table-body');
  const pageInfo = document.getElementById('conn-page-info');
  const btnFirst = document.getElementById('btn-conn-first');
  const btnPrev = document.getElementById('btn-conn-prev');
  const btnNext = document.getElementById('btn-conn-next');
  const btnLast = document.getElementById('btn-conn-last');

  try {
    const url = `/api/connections?page=${state.connPage}&limit=${state.connLimit}&query=${encodeURIComponent(state.connSearchQuery)}`;
    const res = await fetch(url);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();

    state.connLogs = data.records || [];
    state.connTotal = data.total || 0;
    state.connTotalPages = data.total_pages || 1;
    state.connPage = data.page || 1;

    renderConnections();

    if (pageInfo) {
      pageInfo.textContent = `共 ${state.connTotal} 条记录，第 ${state.connPage} / ${state.connTotalPages} 页`;
    }

    const pageSizeInput = document.getElementById('conn-page-size');
    if (pageSizeInput && document.activeElement !== pageSizeInput) {
      pageSizeInput.value = state.connLimit;
    }

    const pageSelect = document.getElementById('conn-page-select');
    if (pageSelect) {
      let optionsHtml = '';
      const totalP = Math.max(1, state.connTotalPages);
      for (let i = 1; i <= totalP; i++) {
        optionsHtml += `<option value="${i}" ${i === state.connPage ? 'selected' : ''}>第 ${i} 页</option>`;
      }
      pageSelect.innerHTML = optionsHtml;
    }

    if (btnFirst) btnFirst.disabled = state.connPage <= 1;
    if (btnPrev) btnPrev.disabled = state.connPage <= 1;
    if (btnNext) btnNext.disabled = state.connPage >= state.connTotalPages;
    if (btnLast) btnLast.disabled = state.connPage >= state.connTotalPages;
  } catch (err) {
    console.error('Fetch connections error:', err);
    if (tbody) {
      tbody.innerHTML = `<tr><td colspan="7" class="empty-state">获取连接日志失败: ${escapeHTML(err.message)}</td></tr>`;
    }
  }
}

function renderConnections() {
  const tbody = document.getElementById('conn-table-body');
  if (!tbody) return;

  if (!state.connLogs || state.connLogs.length === 0) {
    tbody.innerHTML = `<tr><td colspan="7" class="empty-state" style="padding: 24px 0;">暂无连接日志记录</td></tr>`;
    return;
  }

  tbody.innerHTML = state.connLogs.map(c => {
    const uploadStr = formatBytes(c.upload);
    const downloadStr = formatBytes(c.download);
    const trafficStr = `${uploadStr} / ${downloadStr}`;
    const netType = `${c.network || 'TCP'} ${c.type ? `(${c.type})` : ''}`;

    return `
      <tr>
        <td class="conn-time">${escapeHTML(c.time || '-')}</td>
        <td class="conn-ip">${escapeHTML(c.source_ip || '-')}</td>
        <td class="conn-host" title="${escapeHTML(c.host || '')}">${escapeHTML(c.host || '-')}</td>
        <td class="conn-net"><span class="conn-badge">${escapeHTML(netType)}</span></td>
        <td class="conn-chain" title="${escapeHTML(c.chains || '')}">${escapeHTML(c.chains || 'DIRECT')}</td>
        <td class="conn-rule" title="${escapeHTML(c.rule || '')}">${escapeHTML(c.rule || 'Direct')}</td>
        <td class="conn-traffic">${trafficStr}</td>
      </tr>
    `;
  }).join('');
}

async function handleSaveRetention() {
  const input = document.getElementById('conn-retention-input');
  if (!input) return;

  const days = parseInt(input.value, 10);
  if (isNaN(days) || days <= 0) {
    showToast('请输入有效的保留天数 (>= 1)');
    return;
  }

  try {
    const res = await fetch('/api/connections/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ retention_days: days }),
    });
    const data = await res.json();
    if (res.ok && data.success) {
      state.connRetentionDays = days;
      showToast(`连接日志保留天数已设置为 ${days} 天`);
    } else {
      showToast('设置失败: ' + (data.error || '未知错误'));
    }
  } catch (err) {
    showToast('设置异常: ' + err.message);
  }
}

async function handleClearConnections() {
  if (!confirm('确认清空所有已保存的本地连接日志吗？')) return;

  try {
    const res = await fetch('/api/connections/clear', { method: 'POST' });
    const data = await res.json();
    if (res.ok && data.success) {
      state.connLogs = [];
      state.connTotal = 0;
      state.connPage = 1;
      state.connTotalPages = 1;
      renderConnections();
      const pageInfo = document.getElementById('conn-page-info');
      if (pageInfo) pageInfo.textContent = '共 0 条记录，第 1 / 1 页';
      showToast('本地连接日志已清空');
    } else {
      showToast('清空失败: ' + (data.error || '未知错误'));
    }
  } catch (err) {
    showToast('清空异常: ' + err.message);
  }
}

// Escape cell content for CSV output
function escapeCsv(val) {
  if (val === null || val === undefined) return '""';
  const str = String(val).replace(/"/g, '""');
  return `"${str}"`;
}

// Export connection logs as CSV
async function handleExportConnections() {
  const btn = document.getElementById('btn-export-conns');
  if (btn) btn.disabled = true;
  try {
    showToast('正在导出连接日志...');
    const url = `/api/connections?page=1&limit=10000&query=${encodeURIComponent(state.connSearchQuery || '')}`;
    const res = await fetch(url);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();
    const records = data.records || [];
    if (records.length === 0) {
      showToast('没有可导出的连接记录');
      return;
    }

    // CSV BOM for Excel UTF-8 encoding support
    let csvContent = '\uFEFF';
    csvContent += '时间,来源IP,目标地址,网络类型,代理节点,匹配规则,上传流量(字节),下载流量(字节)\n';

    records.forEach(r => {
      const netType = `${r.network || 'TCP'} ${r.type ? `(${r.type})` : ''}`.trim();
      const row = [
        escapeCsv(r.time || ''),
        escapeCsv(r.source_ip || ''),
        escapeCsv(r.host || ''),
        escapeCsv(netType),
        escapeCsv(r.chains || 'DIRECT'),
        escapeCsv(r.rule || 'Direct'),
        r.upload || 0,
        r.download || 0,
      ];
      csvContent += row.join(',') + '\n';
    });

    const now = new Date();
    const dateStr = now.toISOString().slice(0, 10).replace(/-/g, '');
    downloadTextFile(csvContent, `connections-${dateStr}.csv`, 'text/csv;charset=utf-8');
    showToast(`成功导出 ${records.length} 条连接日志`);
  } catch (err) {
    showToast('导出失败: ' + err.message);
  } finally {
    if (btn) btn.disabled = false;
  }
}

// ----------------------------------------------------
// Subscriptions Management Logic
// ----------------------------------------------------
function openSubModal() {
  const modal = document.getElementById('modal-subscriptions');
  if (modal) {
    modal.style.display = 'flex';
    state.isSubModalOpen = true;
    fetchSubscriptions();
  }
}

function closeSubModal() {
  const modal = document.getElementById('modal-subscriptions');
  if (modal) {
    modal.style.display = 'none';
    state.isSubModalOpen = false;
  }
}

async function fetchSubscriptions() {
  const container = document.getElementById('sub-list-container');
  try {
    const res = await fetch('/api/subscriptions');
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();
    state.subscriptions = data.subscriptions || [];
    renderSubscriptions();
  } catch (err) {
    console.error('Fetch subscriptions error:', err);
    if (container) {
      container.innerHTML = `<div class="empty-state">获取订阅列表失败: ${escapeHTML(err.message)}</div>`;
    }
  }
}

function renderSubscriptions() {
  const container = document.getElementById('sub-list-container');
  if (!container) return;

  const subs = state.subscriptions || [];

  if (subs.length === 0) {
    container.innerHTML = `<div class="empty-state" style="padding: 20px 0;">暂无订阅或配置文件</div>`;
    return;
  }

  container.innerHTML = subs.map(s => renderAirportItemHTML(s, false)).join('');
}

let pendingGlobalDownload = null;

function openGlobalConfirmModal(context) {
  pendingGlobalDownload = context;
  const modal = document.getElementById('modal-global-confirm');
  if (!modal) return;
  const desc = document.getElementById('global-confirm-desc');
  if (desc && context.detail) {
    desc.textContent = context.detail;
  } else if (desc) {
    desc.textContent = '订阅域名直连不可达，且当前 OpenClash 规则分流未包含该订阅域名（导致直接连接被防火墙阻断）。';
  }
  modal.style.display = 'flex';
  state.isGlobalConfirmModalOpen = true;
}

function closeGlobalConfirmModal() {
  const modal = document.getElementById('modal-global-confirm');
  if (modal) modal.style.display = 'none';
  state.isGlobalConfirmModalOpen = false;
  pendingGlobalDownload = null;
}

async function handleAddSubscription() {
  const nameInput = document.getElementById('sub-input-name');
  const urlInput = document.getElementById('sub-input-url');

  const name = nameInput ? nameInput.value.trim() : '';
  const url = urlInput ? urlInput.value.trim() : '';

  if (!url) {
    showToast('请输入有效的订阅链接 (https://...)');
    return;
  }

  await executeAddSubscription(name, url, false);
}

async function executeAddSubscription(name, url, allowGlobal = false) {
  const nameInput = document.getElementById('sub-input-name');
  const urlInput = document.getElementById('sub-input-url');
  const btn = document.getElementById('btn-add-sub');

  if (btn) {
    btn.disabled = true;
    btn.textContent = allowGlobal ? '出海导入中...' : '导入中...';
  }
  showToast(allowGlobal ? '已授权临时全局出海，正在下载并导入订阅...' : '正在下载并导入订阅...');

  try {
    const res = await fetch('/api/subscriptions/add', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, url, allow_global: allowGlobal }),
    });
    const data = await res.json();
    if (res.ok && data.success) {
      const addedName = (data.subscription && data.subscription.name) ? data.subscription.name : (name || '新订阅');
      showToast(`订阅 [${addedName}] 导入成功`);
      if (nameInput) nameInput.value = '';
      if (urlInput) urlInput.value = '';
      await fetchSubscriptions();
      await fetchProfiles();
      fetchLogs();
    } else if (data.need_global_confirm) {
      openGlobalConfirmModal({
        type: 'add',
        name,
        url,
        detail: data.detail || data.message,
      });
    } else {
      showToast(`导入失败: ${data.error || data.message || '未知错误'}`);
    }
  } catch (err) {
    showToast(`导入异常: ${err.message}`);
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.textContent = '导入';
    }
  }
}

async function handleUpdateSub(filename, name) {
  await executeUpdateSub(filename, name, false);
}

async function executeUpdateSub(filename, name, allowGlobal = false) {
  showToast(allowGlobal ? `已授权临时全局出海，正在更新订阅 [${name}]...` : `正在更新订阅 [${name}]...`);
  try {
    const res = await fetch('/api/subscriptions/update', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ filename, name, allow_global: allowGlobal }),
    });
    const data = await res.json();
    if (res.ok && data.success) {
      showToast(`订阅 [${name}] 更新成功`);
      await fetchSubscriptions();
      await fetchProfiles();
      fetchLogs();
    } else if (data.need_global_confirm) {
      openGlobalConfirmModal({
        type: 'update',
        filename,
        name,
        detail: data.detail || data.message,
      });
    } else {
      showToast(`更新失败: ${data.error || data.message || '未知错误'}`);
    }
  } catch (err) {
    showToast(`更新异常: ${err.message}`);
  }
}

async function handleDeleteSub(filename, name) {
  if (!confirm(`确认删除订阅配置文件 [${name}] 吗？`)) return;

  showToast(`正在删除 [${name}]...`);
  try {
    const res = await fetch('/api/subscriptions/delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ filename, name }),
    });
    const data = await res.json();
    if (res.ok && data.success) {
      showToast(`订阅 [${name}] 已删除`);
      await fetchSubscriptions();
      await fetchProfiles();
      fetchLogs();
    } else {
      showToast(`删除失败: ${data.error || '未知错误'}`);
    }
  } catch (err) {
    showToast(`删除异常: ${err.message}`);
  }
}

async function handleSwitchSub(filename, name) {
  closeSubModal();
  closeAirportDropdown();
  await switchAirportProfile(filename, name);
}

// Upload local YAML file
async function handleUploadYamlFile(e) {
  const file = e.target.files && e.target.files[0];
  if (!file) return;

  if (!file.name.endsWith('.yaml') && !file.name.endsWith('.yml')) {
    showToast('仅支持上传 .yaml 或 .yml 配置文件');
    e.target.value = '';
    return;
  }

  const formData = new FormData();
  formData.append('file', file);

  showToast(`正在上传配置文件: ${file.name}...`);
  try {
    const res = await fetch('/api/subscriptions/upload', {
      method: 'POST',
      body: formData,
    });
    const data = await res.json();
    if (res.ok && data.success) {
      showToast(`配置文件 [${file.name}] 上传成功`);
      await fetchSubscriptions();
      await fetchProfiles();
      fetchLogs();
    } else {
      showToast('上传失败: ' + (data.error || '未知错误'));
    }
  } catch (err) {
    showToast('上传异常: ' + err.message);
  } finally {
    e.target.value = '';
  }
}

// Batch update remote subscriptions
async function handleBatchUpdateSubscriptions() {
  const subs = (state.subscriptions || []).filter(s => s.url && s.url.length > 0);
  if (subs.length === 0) {
    showToast('暂无远程订阅链接可供更新');
    return;
  }

  const btn = document.getElementById('btn-batch-update-subs');
  const btnText = document.getElementById('batch-update-btn-text');
  if (btn) btn.disabled = true;
  if (btnText) btnText.textContent = '更新中...';

  showToast(`开始批量更新 ${subs.length} 个订阅...`);
  let successCount = 0;
  const needConfirmSubs = [];

  for (let i = 0; i < subs.length; i++) {
    const sub = subs[i];
    if (btnText) btnText.textContent = `[${i + 1}/${subs.length}] 更新中`;
    try {
      const res = await fetch('/api/subscriptions/update', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ filename: sub.filename || sub.name, name: sub.name, allow_global: false }),
      });
      const data = await res.json();
      if (res.ok && data.success) {
        successCount++;
      } else if (data.need_global_confirm) {
        needConfirmSubs.push(sub);
      }
    } catch (e) {
      console.warn(`Update ${sub.name} error:`, e);
    }
  }

  await fetchSubscriptions();
  await fetchProfiles();
  fetchLogs();

  if (btn) btn.disabled = false;
  if (btnText) btnText.textContent = '批量更新';
  if (needConfirmSubs.length > 0) {
    showToast(`批量更新完成: 成功 ${successCount}，${needConfirmSubs.length} 个因分流拦截需单独出海更新`);
  } else {
    showToast(`批量更新完成: 成功 ${successCount} / ${subs.length}`);
  }
}

// Card 4: Network Web Speed Test (Customizable, defaults: Baidu, B站, Google, YouTube)
const defaultSpeedTargets = [
  { id: 'baidu', name: '百度', url: 'https://www.baidu.com/favicon.ico' },
  { id: 'bilibili', name: 'B站', url: 'https://www.bilibili.com/favicon.ico' },
  { id: 'google', name: 'Google', url: 'https://www.google.com/generate_204' },
  { id: 'youtube', name: 'YouTube', url: 'https://www.youtube.com/generate_204' },
];

function getSpeedTargets() {
  try {
    const raw = localStorage.getItem('web_speed_targets');
    if (raw) {
      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed) && parsed.length === 4) {
        return parsed;
      }
    }
  } catch (e) {}
  return defaultSpeedTargets;
}

function saveSpeedTargets(targets) {
  localStorage.setItem('web_speed_targets', JSON.stringify(targets));
}

function renderSpeedGrid() {
  const container = document.getElementById('web-speed-grid');
  if (!container) return;
  const targets = getSpeedTargets();
  container.innerHTML = targets.map(t => `
    <div class="speed-item" id="speed-item-${t.id}">
      <span class="speed-name" title="${escapeHTML(t.url)}">${escapeHTML(t.name)}</span>
      <span class="speed-val" id="speed-val-${t.id}">-</span>
    </div>
  `).join('');
}

function openSpeedConfigModal() {
  const modal = document.getElementById('modal-speed-config');
  const listContainer = document.getElementById('speed-config-items');
  if (!modal || !listContainer) return;

  const targets = getSpeedTargets();
  listContainer.innerHTML = targets.map((t, idx) => `
    <div class="speed-config-row" data-idx="${idx}">
      <div class="form-group form-group-name">
        <label>目标 ${idx + 1} 名称</label>
        <input type="text" class="speed-cfg-name" value="${escapeHTML(t.name)}" autocomplete="off">
      </div>
      <div class="form-group form-group-url">
        <label>探测地址 (URL)</label>
        <input type="url" class="speed-cfg-url" value="${escapeHTML(t.url)}" autocomplete="off">
      </div>
    </div>
  `).join('');

  modal.style.display = 'flex';
  state.isSpeedModalOpen = true;
}

function closeSpeedConfigModal() {
  const modal = document.getElementById('modal-speed-config');
  if (modal) modal.style.display = 'none';
  state.isSpeedModalOpen = false;
}

function handleResetSpeedConfig() {
  saveSpeedTargets(defaultSpeedTargets);
  closeSpeedConfigModal();
  renderSpeedGrid();
  showToast('网络测速目标已恢复默认 (百度 / B站 / Google / YouTube)');
}

function handleSaveSpeedConfig() {
  const listContainer = document.getElementById('speed-config-items');
  if (!listContainer) return;

  const rows = listContainer.querySelectorAll('.speed-config-row');
  const newTargets = [];
  rows.forEach((row, idx) => {
    const nameInput = row.querySelector('.speed-cfg-name');
    const urlInput = row.querySelector('.speed-cfg-url');
    const name = (nameInput?.value || '').trim() || `目标 ${idx + 1}`;
    let url = (urlInput?.value || '').trim();
    if (!url) {
      url = defaultSpeedTargets[idx]?.url || 'https://www.google.com/generate_204';
    }
    newTargets.push({
      id: `target_${idx}`,
      name: name,
      url: url,
    });
  });

  saveSpeedTargets(newTargets);
  closeSpeedConfigModal();
  renderSpeedGrid();
  showToast('网络测速目标配置已保存');
}

async function handleTestWebSpeed() {
  const btn = document.getElementById('btn-test-web-speed');
  const btnText = document.getElementById('web-speed-btn-text');
  if (btn) btn.disabled = true;
  if (btnText) btnText.textContent = '测速中...';

  const targets = getSpeedTargets();

  targets.forEach(t => {
    const el = document.getElementById(`speed-val-${t.id}`);
    if (el) {
      el.className = 'speed-val testing';
      el.textContent = '...';
    }
  });

  const promises = targets.map(async (t) => {
    const el = document.getElementById(`speed-val-${t.id}`);
    const start = performance.now();
    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 4000);
      const sep = t.url.includes('?') ? '&' : '?';
      await fetch(t.url + sep + '_t=' + Date.now(), {
        mode: 'no-cors',
        cache: 'no-store',
        signal: controller.signal,
      });
      clearTimeout(timeoutId);
      const delay = Math.round(performance.now() - start);
      if (el) {
        el.textContent = `${delay} ms`;
        if (delay < 150) {
          el.className = 'speed-val fast';
        } else if (delay < 400) {
          el.className = 'speed-val medium';
        } else {
          el.className = 'speed-val slow';
        }
      }
    } catch {
      if (el) {
        el.textContent = '超时';
        el.className = 'speed-val slow';
      }
    }
  });

  await Promise.all(promises);

  if (btn) btn.disabled = false;
  if (btnText) btnText.textContent = '测速';
  showToast('网络测速完成');
}

// Attach action functions to window for onclick handlers
window.handleSelectAirport = handleSelectAirport;
window.handleSwitchSub = handleSwitchSub;
window.handleUpdateSub = handleUpdateSub;
window.handleDeleteSub = handleDeleteSub;
window.updateProviderSubscription = updateProviderSubscription;
window.openSpeedConfigModal = openSpeedConfigModal;
window.closeSpeedConfigModal = closeSpeedConfigModal;
window.handleResetSpeedConfig = handleResetSpeedConfig;
window.handleSaveSpeedConfig = handleSaveSpeedConfig;

// ----------------------------------------------------
// Environment Variables (.env) Management
// ----------------------------------------------------
async function fetchEnv() {
  const editor = document.getElementById('env-editor');
  const badge = document.getElementById('env-file-badge');
  const pathEl = document.getElementById('env-file-path');

  try {
    const res = await fetch('/api/env');
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();

    state.envExists = data.exists;
    state.envPath = data.path || '.env';
    state.envContent = data.content || '';
    state.envExample = data.example || '';

    if (editor) {
      editor.value = state.envContent || state.envExample || '';
    }

    if (badge) {
      if (state.envExists) {
        badge.textContent = '已加载';
        badge.className = 'env-file-badge loaded';
      } else {
        badge.textContent = '未初始化';
        badge.className = 'env-file-badge uninit';
      }
    }

    if (pathEl) {
      pathEl.textContent = state.envPath;
    }
  } catch (err) {
    if (editor) editor.value = '读取失败: ' + err.message;
    if (badge) {
      badge.textContent = '读取失败';
      badge.className = 'env-file-badge uninit';
    }
  }
}

async function handleSaveEnv() {
  const editor = document.getElementById('env-editor');
  if (!editor) return;

  const content = editor.value;
  const btnSave = document.getElementById('btn-save-env');
  if (btnSave) btnSave.disabled = true;

  try {
    const res = await fetch('/api/env', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content }),
    });

    if (!res.ok) {
      const errText = await res.text();
      throw new Error(errText || `HTTP ${res.status}`);
    }

    const data = await res.json();
    showToast(data.message || '环境变量已保存并热重载生效');
    state.envExists = true;
    state.envContent = content;

    const badge = document.getElementById('env-file-badge');
    if (badge) {
      badge.textContent = '已加载';
      badge.className = 'env-file-badge loaded';
    }

    // Refresh dashboard status with newly configured credentials
    fetchStatus();
  } catch (err) {
    showToast('保存失败: ' + err.message);
  } finally {
    if (btnSave) btnSave.disabled = false;
  }
}

async function handleInitEnv() {
  const ok = confirm('确定要初始化环境变量文件吗？此操作将生成标准配置模版。');
  if (!ok) return;

  const btnInit = document.getElementById('btn-init-env');
  if (btnInit) btnInit.disabled = true;

  try {
    const res = await fetch('/api/env/init', { method: 'POST' });
    if (!res.ok) {
      const errText = await res.text();
      throw new Error(errText || `HTTP ${res.status}`);
    }

    const data = await res.json();
    showToast(data.message || '已成功初始化环境变量');

    const editor = document.getElementById('env-editor');
    if (editor) editor.value = data.content || '';

    state.envExists = true;
    state.envContent = data.content || '';
    state.envPath = data.path || '.env';

    const badge = document.getElementById('env-file-badge');
    if (badge) {
      badge.textContent = '已加载';
      badge.className = 'env-file-badge loaded';
    }

    const pathEl = document.getElementById('env-file-path');
    if (pathEl) pathEl.textContent = state.envPath;

    fetchStatus();
  } catch (err) {
    showToast('初始化失败: ' + err.message);
  } finally {
    if (btnInit) btnInit.disabled = false;
  }
}

function handleReloadEnv() {
  fetchEnv();
  showToast('已重新读取环境变量文件');
}

window.handleSaveEnv = handleSaveEnv;
window.handleInitEnv = handleInitEnv;
window.handleReloadEnv = handleReloadEnv;



