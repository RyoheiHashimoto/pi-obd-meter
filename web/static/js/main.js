// ============================================================
// Main — エントリポイント + WebSocket / HTTP ポーリング
// ============================================================

import { buildSpeedGauge, updateThrottle, updateGear, speedColor, rpmColor, setThrottleIdleBaseline, setThrottleMaxPct } from './gauge.js';
import { createIndicators, updateIndicators, setCoolantThresholds, setEcoGradientMax, setMapDirect, restoreMapTransition } from './indicators.js';

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
  // OBD 未接続時 (ACC/エンジン停止) はプレースホルダー状態
  const obdOn = d.obd_connected !== false;
  document.body.classList.toggle('obd-offline', !obdOn);
  const spd = obdOn ? (d.speed_kmh || 0) : 0;
  const rpm = obdOn ? (d.rpm || 0) : 0;
  gs.update(spd, rpm, speedColor(spd), rpmColor(rpm));
  updateThrottle(obdOn ? (d.throttle_pos || 0) : 0);
  updateGear(obdOn ? (d.gear || 0) : 0, obdOn ? (d.at_range_str || '-') : '-', obdOn && (d.hold || false), obdOn && (d.tc_locked || false));
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

  let lastMsgAt = performance.now();
  let lastObdConnected = true;
  ws.onmessage = (ev) => {
    const now = performance.now();
    const gap = now - lastMsgAt;
    lastMsgAt = now;
    connected = true;
    if (window.__wsAlive) window.__wsAlive();
    const d = JSON.parse(ev.data);
    const obdOn = d.obd_connected !== false;
    // OBD 接続中のみ ws_gap 判定 (未接続時は backend heartbeat で 1s 間隔=正常)
    if (obdOn && lastObdConnected && gap > 500) {
      reportError('ws_gap', { gap_ms: Math.round(gap) });
    }
    lastObdConnected = obdOn;
    applyData(d);
  };

  ws.onclose = () => {
    connected = false;
    ws = null;
    wsRetryCount++;
    reportError('ws_close', { retry: wsRetryCount });
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

// --- クライアント側エラーを backend に送信 ---
function reportError(type, detail) {
  try {
    fetch('/api/client-error', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ type, detail, url: location.href, ua: navigator.userAgent, t: Date.now() }),
      keepalive: true,
    }).catch(() => {});
  } catch {}
}

window.addEventListener('error', (e) => {
  reportError('window_error', { msg: e.message, src: e.filename, line: e.lineno, col: e.colno, stack: e.error && e.error.stack });
});
window.addEventListener('unhandledrejection', (e) => {
  reportError('unhandled_rejection', { reason: String(e.reason), stack: e.reason && e.reason.stack });
});

// --- 初期化 ---
async function initApp() {
  // 起動アニメ中はテキスト非表示 (Phase 3 で CSS 経由フェードイン)
  document.body.classList.add('booting');

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
    unit: 'km/h', mj: 9, mn: 5, numSz: 84, tkSz: 32,
    fmt: v => v > 0.5 ? String(Math.round(v)) : '0'
  });

  // --- 起動アニメーション (SDL 版と同じ 4 Phase) ---
  await bootAnimation(gs);

  // フリーズ検知 watchdog (rAF 停止時 自動リロード)
  startWatchdog();
  // バージョン更新検知 (auto-update 後に自動リロード)
  startVersionCheck();

  // WebSocket 優先、失敗時は HTTP polling にフォールバック
  connectWebSocket();
}

// 起動アニメーション: sweep out(1.2s) → back(0.8s) → fade in(0.8s) → 通常
function bootAnimation(gauge) {
  return new Promise(resolve => {
    const SWEEP_OUT = 1200;
    const SWEEP_BACK = 800;
    const FADE_IN = 800;
    const spdCol = '#69f0ae';
    const rpmCol = '#42a5f5';
    const thrCol = '#26c6da';
    const mapCol = '#42a5f5';
    const start = performance.now();

    function easeInOut(t) { return t < 0.5 ? 2 * t * t : -1 + (4 - 2 * t) * t; }

    function frame(now) {
      const elapsed = now - start;
      if (elapsed < SWEEP_OUT) {
        // Phase 1: 針 0 → MAX
        const t = easeInOut(elapsed / SWEEP_OUT);
        gauge.setDirect(t, spdCol);
        gauge.setSpeedDirect(t, rpmCol);
        gauge.setThrDirect(t, thrCol);
        setMapDirect(t, mapCol);
        requestAnimationFrame(frame);
      } else if (elapsed < SWEEP_OUT + SWEEP_BACK) {
        // Phase 2: 針 MAX → 0
        const t = easeInOut((elapsed - SWEEP_OUT) / SWEEP_BACK);
        gauge.setDirect(1 - t, spdCol);
        gauge.setSpeedDirect(1 - t, rpmCol);
        gauge.setThrDirect(1 - t, thrCol);
        setMapDirect(1 - t, mapCol);
        requestAnimationFrame(frame);
      } else if (elapsed < SWEEP_OUT + SWEEP_BACK + FADE_IN) {
        // Phase 3: 針は 0、テキスト/ラベルが CSS transition でフェードイン
        gauge.setDirect(0, '#78909c');
        gauge.setSpeedDirect(0, '#222');
        gauge.setThrDirect(0, '#333');
        setMapDirect(0, '#78909c');
        // 一度だけ class 除去 (CSS で 800ms フェード開始)
        document.body.classList.remove('booting');
        requestAnimationFrame(frame);
      } else {
        // 完了: transition 復帰
        gauge.restoreTransition();
        restoreMapTransition();
        resolve();
      }
    }
    requestAnimationFrame(frame);
  });
}

// バージョン検知: auto-update 後にページ自動リロード
function startVersionCheck() {
  let currentVersion = null;
  setInterval(async () => {
    try {
      const resp = await fetch('/api/config');
      if (!resp.ok) return;
      const cfg = await resp.json();
      if (!cfg.version) return;
      if (currentVersion === null) { currentVersion = cfg.version; return; }
      if (cfg.version !== currentVersion) {
        console.log('version changed:', currentVersion, '→', cfg.version, '→ reload');
        location.reload();
      }
    } catch {}
  }, 30000); // 30秒ごとにチェック
}

// フリーズ検知 watchdog: rAF が 3 秒以上止まったら自動リロード
// setInterval は rAF が死んでも動き続けるので、独立した stuck 検知ができる
function startWatchdog() {
  const THRESH_MS = 3000;
  let lastRAF = performance.now();
  let lastWS = performance.now();
  function rafHeartbeat() { lastRAF = performance.now(); requestAnimationFrame(rafHeartbeat); }
  requestAnimationFrame(rafHeartbeat);
  // WebSocket 最終受信時刻も監視
  window.__wsAlive = () => { lastWS = performance.now(); };
  setInterval(() => {
    const rafStuck = performance.now() - lastRAF;
    const wsStuck = performance.now() - lastWS;
    if (rafStuck > THRESH_MS) {
      reportError('raf_freeze', { stuck_ms: rafStuck, ws_stuck_ms: wsStuck });
      setTimeout(() => location.reload(), 200); // backend送信猶予
    }
  }, 1000);
}

initApp();
