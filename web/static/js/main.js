// ============================================================
// Main — エントリポイント + WebSocket / HTTP ポーリング
// ============================================================

import { buildSpeedGauge, updateThrottle, updateRPM, updateGear, speedColor, setThrottleIdleBaseline, setThrottleMaxPct } from './gauge.js';
import { createIndicators, updateIndicators, setCoolantThresholds, setEcoGradientMax } from './indicators.js';

const DEFAULTS = {
  max_speed_kmh: 180,
  throttle_idle_pct: 0, throttle_max_pct: 200,
  eco_gradient_max_kmpl: 15,
  trip_warn_km: 300, trip_danger_km: 500,
};

// HTTP フォールバック用
const POLL_INTERVAL_MS = 50;
const FETCH_TIMEOUT_MS = 3000;

// WebSocket 再接続
const WS_RECONNECT_BASE_MS = 1000;
const WS_RECONNECT_MAX_MS = 10000;
const WS_MAX_RETRIES = 10;

let conf = DEFAULTS;
let gs;
let dom;
let connected = false;
let ws = null;
let wsReconnectDelay = WS_RECONNECT_BASE_MS;
let wsEverConnected = false;
let wsRetryCount = 0;
let usingPolling = false;

// --- データ適用 ---
function applyData(d) {
  const spd = d.speed_kmh || 0;
  gs.update(spd, speedColor(spd));
  updateThrottle(d.throttle_pos || 0);
  updateRPM(d.rpm || 0);
  updateGear(d.gear || 0, d.at_range_str || '?', d.hold || false, d.tc_locked || false);
  updateIndicators(dom, d, conf);
}

// --- WebSocket 接続 ---
function connectWebSocket() {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(`${proto}//${location.host}/ws/realtime`);

  ws.onopen = () => {
    connected = true;
    wsEverConnected = true;
    wsReconnectDelay = WS_RECONNECT_BASE_MS;
    wsRetryCount = 0;
  };

  ws.onmessage = (ev) => {
    connected = true;
    applyData(JSON.parse(ev.data));
  };

  ws.onclose = () => {
    connected = false;
    ws = null;
    wsRetryCount++;
    if (!wsEverConnected || wsRetryCount >= WS_MAX_RETRIES) {
      // WS 未接続 or 再接続上限超過 → HTTP polling にフォールバック
      usingPolling = true;
      startPolling();
      return;
    }
    // Exponential backoff で再接続
    setTimeout(connectWebSocket, wsReconnectDelay);
    wsReconnectDelay = Math.min(wsReconnectDelay * 1.5, WS_RECONNECT_MAX_MS);
  };

  ws.onerror = () => {
    ws.close();
  };
}

// --- HTTP ポーリング（フォールバック用） ---
async function fetchRealtime() {
  try {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), FETCH_TIMEOUT_MS);
    const resp = await fetch('/api/realtime', { signal: ctrl.signal });
    clearTimeout(timer);
    if (!resp.ok) throw new Error(resp.status);
    connected = true;
    applyData(await resp.json());
  } catch {
    connected = false;
  }
}

function startPolling() {
  (function poll() {
    fetchRealtime().then(() => setTimeout(poll, POLL_INTERVAL_MS));
  })();
}

// --- 初期化 ---
async function initApp() {
  dom = createIndicators(document.getElementById('panel'));

  try {
    const resp = await fetch('/api/config');
    if (resp.ok) conf = { ...DEFAULTS, ...await resp.json() };
  } catch { /* file:// mode */ }

  setThrottleIdleBaseline(conf.throttle_idle_pct);
  setThrottleMaxPct(conf.throttle_max_pct);
  if (conf.coolant_cold_max) {
    setCoolantThresholds(conf.coolant_cold_max, conf.coolant_normal_max, conf.coolant_warning_max);
  }
  if (conf.eco_gradient_max_kmpl) {
    setEcoGradientMax(conf.eco_gradient_max_kmpl);
  }

  // --- 画面長押しでキオスク終了（3秒） ---
  let kioskTimer = null;
  const KIOSK_HOLD_MS = 3000;

  function startHold() {
    kioskTimer = setTimeout(async () => {
      try { await fetch('/api/kiosk/stop', { method: 'POST' }); } catch {}
    }, KIOSK_HOLD_MS);
  }
  function cancelHold() { if (kioskTimer) { clearTimeout(kioskTimer); kioskTimer = null; } }

  document.body.addEventListener('touchstart', startHold, { passive: true });
  document.body.addEventListener('touchend', cancelHold);
  document.body.addEventListener('touchmove', cancelHold);
  document.body.addEventListener('mousedown', startHold);
  document.body.addEventListener('mouseup', cancelHold);

  gs = buildSpeedGauge('gs', {
    cx: 280, cy: 270, r: 230,
    min: 0, max: conf.max_speed_kmh, color: '#78909c',
    unit: 'km/h', mj: 9, mn: 5, numSz: 84, tkSz: 28,
    fmt: v => v > 0.5 ? String(Math.round(v)) : '0'
  });

  // WebSocket 優先、失敗時は HTTP polling にフォールバック
  connectWebSocket();
}

initApp();
