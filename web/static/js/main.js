// ============================================================
// Main — エントリポイント + API ポーリング + Toast
// ============================================================

import { buildSpeedGauge, updateThrottle, speedColor, setThrottleIdleBaseline, setThrottleMaxPct } from './gauge.js';
import { createIndicators, updateIndicators } from './indicators.js';

const DEFAULTS = {
  max_speed_kmh: 180, eco_lh_green: 2.0, eco_lh_red: 3.9,
  throttle_idle_pct: 11.5, throttle_max_pct: 78,
  eco_kmpl_green: 20, eco_kmpl_orange: 10,
  trip_warn_km: 300, trip_danger_km: 500,
};
const POLL_INTERVAL_MS = 200;
const TOAST_DURATION_MS = 5000;
const ALERT_INTERVAL_MS = 5500;

let conf = DEFAULTS;
let gs;
let dom;
let connected = false;

// --- Toast ---
let lastNotification = '';
let toastTimer = null;

function showToast(msg) {
  if (msg === lastNotification) return;
  lastNotification = msg;
  dom.toast.textContent = msg;
  dom.toast.classList.add('show');
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { dom.toast.classList.remove('show'); }, TOAST_DURATION_MS);
}

// --- メンテナンスアラートキュー ---
let prevAlertCount = 0;
let alertQueue = [];
let alertQueueTimer = null;

function showAlertQueue() {
  if (alertQueue.length === 0) { alertQueueTimer = null; return; }
  showToast(alertQueue.shift());
  alertQueueTimer = setTimeout(showAlertQueue, ALERT_INTERVAL_MS);
}

// --- データ適用 ---
function applyData(d) {
  const spd = d.speed_kmh || 0;
  gs.update(spd, speedColor(spd));
  updateThrottle(d.throttle_pos || 0);
  updateIndicators(dom, d, conf);

  // Maintenance toast（全件を順番に表示）
  const alertCount = d.alerts ? d.alerts.length : 0;
  if (alertCount > 0 && alertCount !== prevAlertCount) {
    alertQueue = [];
    if (alertQueueTimer) { clearTimeout(alertQueueTimer); alertQueueTimer = null; }
    for (const a of d.alerts) {
      const { reminder: r } = a;
      const remain = r.type === 'distance'
        ? `${Math.round(a.remaining_km).toLocaleString()} km`
        : `${a.days_left} \u65E5`;
      alertQueue.push(`\u26A0 ${r.name}\u307E\u3067 ${remain}`);
    }
    showAlertQueue();
  }
  prevAlertCount = alertCount;

  if (d.notification) showToast(d.notification);
  else if (lastNotification) lastNotification = '';
}

// --- APIポーリング ---
async function fetchRealtime() {
  try {
    const resp = await fetch('/api/realtime');
    if (!resp.ok) throw new Error(resp.status);
    connected = true;
    applyData(await resp.json());
  } catch {
    connected = false;
  }
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

  // --- 画面長押しでキオスク終了（3秒） ---
  let kioskTimer = null;
  const KIOSK_HOLD_MS = 3000;

  function startHold(e) {
    kioskTimer = setTimeout(async () => {
      showToast('Closing...');
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
    cx: 280, cy: 260, r: 220,
    min: 0, max: conf.max_speed_kmh, color: '#78909c',
    unit: 'km/h', mj: 9, mn: 5, numSz: 84, tkSz: 28,
    fmt: v => v > 0.5 ? String(Math.round(v)) : '0'
  });

  // fetch完了後に次を予約（setIntervalだとリクエスト重複の恐れ）
  (function poll() {
    fetchRealtime().then(() => setTimeout(poll, POLL_INTERVAL_MS));
  })();
}

initApp();
