// ============================================================
// Main — エントリポイント + API ポーリング
// ============================================================

import { buildSpeedGauge, updateThrottle, updateRPM, updateGear, updateBottomIndicators, speedColor, setThrottleIdleBaseline, setThrottleMaxPct, setCoolantThresholds, setEcoGradientMax } from './gauge.js';
import { createIndicators, updateIndicators } from './indicators.js';

const DEFAULTS = {
  max_speed_kmh: 180,
  throttle_idle_pct: 11.5, throttle_max_pct: 78,
  eco_gradient_max_kmpl: 15,
  trip_warn_km: 300, trip_danger_km: 500,
};
const POLL_INTERVAL_MS = 200;
const FETCH_TIMEOUT_MS = 3000;

let conf = DEFAULTS;
let gs;
let dom;
let connected = false;

// --- データ適用 ---
function applyData(d) {
  const spd = d.speed_kmh || 0;
  gs.update(spd, speedColor(spd));
  updateThrottle(d.throttle_pos || 0);
  updateRPM(d.rpm || 0);
  updateGear(d.gear || 0, d.at_range_str || '?', d.hold || false, d.tc_locked || false);
  updateBottomIndicators(d.coolant_temp || 0, d.trip_km || 0, d.avg_fuel_economy || 0, d.fuel_economy || 0);
  updateIndicators(dom, d, conf);
}

// --- APIポーリング ---
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
    cx: 280, cy: 260, r: 220,
    min: 0, max: conf.max_speed_kmh, color: '#78909c',
    unit: 'km/h', mj: 9, mn: 5, numSz: 84, tkSz: 28,
    fmt: v => v > 0.5 ? String(Math.round(v)) : '0'
  });

  (function poll() {
    fetchRealtime().then(() => setTimeout(poll, POLL_INTERVAL_MS));
  })();
}

initApp();
