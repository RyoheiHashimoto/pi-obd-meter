'use strict';

// ============================================================
// SVG Geometry Constants & Helpers
// 270° arc gauge: 12時を0°として左右±135°の範囲で描画する
// ============================================================
const DEG_TO_RAD = Math.PI / 180;
const ARC_START = -135;
const ARC_END = 135;
const ARC_SWEEP = 270;

const GAUGE_CX = 280;
const GAUGE_CY = 260;
const GAUGE_R = 220;
const THROTTLE_R = GAUGE_R - 80;

const LERP_SPEED = 0.15;
const LERP_THR_SPEED = 0.2;
const LERP_THRESHOLD = 0.05;
const LERP_STOP = 0.01;

const THR_IDLE_BASELINE = 11.5;
const THR_HUE_MAX = 210;

const TOAST_DURATION_MS = 5000;
const ALERT_INTERVAL_MS = 5500;
const POLL_INTERVAL_MS = 200;
const SIM_TICK_MS = 50;
const SIM_CYCLE_S = 16;

const DEFAULTS = { max_speed_kmh: 180, eco_lh_green: 2.0, eco_lh_red: 3.9 };
const ECO_LOW_SPEED_THRESHOLD = 30;

// 極座標→直交座標（12時=0°、時計回り正）
function polarToXY(cx, cy, r, deg) {
  const rad = deg * DEG_TO_RAD;
  return [cx + r * Math.sin(rad), cy - r * Math.cos(rad)];
}

// SVGアークパス文字列を生成（角度 s→e）
function arcPath(cx, cy, r, s, e) {
  if (Math.abs(e - s) < 0.3) return '';
  const [x1, y1] = polarToXY(cx, cy, r, s);
  const [x2, y2] = polarToXY(cx, cy, r, e);
  const lg = Math.abs(e - s) > 180 ? 1 : 0;
  return `M${x1.toFixed(1)},${y1.toFixed(1)}A${r},${r},0,${lg},1,${x2.toFixed(1)},${y2.toFixed(1)}`;
}

// SVG要素を生成して親に追加
function svgEl(parent, tag, attrs) {
  const e = document.createElementNS('http://www.w3.org/2000/svg', tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  parent.appendChild(e);
  return e;
}

// グロー効果用SVGフィルター（1回だけ生成）
let glowFilterCreated = false;
function ensureGlowFilter(svg) {
  if (glowFilterCreated) return;
  glowFilterCreated = true;
  const defs = svgEl(svg, 'defs', {});
  defs.innerHTML = '<filter id="gl" x="-50%" y="-50%" width="200%" height="200%">' +
    '<feGaussianBlur stdDeviation="3" result="b"/>' +
    '<feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>';
}

// ============================================================
// DOM Elements — 右パネルのインジケーター要素をまとめて取得
// ============================================================
const dom = {
  eco:  { dot: null, val: null },
  trip: { dot: null, val: null },
  temp: { dot: null, val: null },
  maint:{ dot: null, val: null },
  send: { dot: null, val: null },
  wifi: { dot: null, val: null },
  obd:  { dot: null, val: null },
  toast: null,
};

function initDOM() {
  for (const key of Object.keys(dom)) {
    if (key === 'toast') {
      dom.toast = document.getElementById('toast');
    } else {
      dom[key].dot = document.getElementById(`ind-${key}`);
      dom[key].val = document.getElementById(`${key}-val`);
    }
  }
}

// インジケーターのドット色を設定
function setDot(indicator, colorClass) {
  indicator.dot.className = colorClass ? `ind-dot on-${colorClass}` : 'ind-dot';
}

// ============================================================
// Speed Gauge Builder
// 外周: 速度アークゲージ（針 + 値アーク + 目盛り）
// 内周: スロットル開度アーク（HSLグラデーション）
// requestAnimationFrame で 60fps 補間
// ============================================================
let thrArc, thrLabel;

function buildSpeedGauge(svgId, cfg) {
  const svg = document.getElementById(svgId);
  const { cx, cy, r, min, max, unit, mj, mn, numSz, tkSz } = cfg;

  ensureGlowFilter(svg);

  // Track (thick bezel)
  svgEl(svg, 'path', { d: arcPath(cx, cy, r, ARC_START, ARC_END), fill: 'none', stroke: '#181820', 'stroke-width': 16, 'stroke-linecap': 'round' });

  // Ticks
  const total = mj * mn;
  for (let i = 0; i <= total; i++) {
    const a = ARC_START + (i / total) * ARC_SWEEP;
    const isMj = i % mn === 0;
    const ri = isMj ? r - 30 : r - 18;
    const ro = r + 4;
    const [x1, y1] = polarToXY(cx, cy, ri, a);
    const [x2, y2] = polarToXY(cx, cy, ro, a);
    svgEl(svg, 'line', { x1, y1, x2, y2, stroke: isMj ? '#aaa' : '#444', 'stroke-width': isMj ? 5 : 2.5 });
    if (isMj) {
      const v = min + (i / total) * (max - min);
      const [lx, ly] = polarToXY(cx, cy, r - 50, a);
      const t = svgEl(svg, 'text', { x: lx, y: ly, class: 'tk-lbl', fill: '#fff', 'font-size': tkSz });
      t.textContent = Math.round(v);
    }
  }

  // Throttle inner arc (track)
  svgEl(svg, 'path', { d: arcPath(cx, cy, THROTTLE_R, ARC_START, ARC_END), fill: 'none', stroke: '#111', 'stroke-width': 10, 'stroke-linecap': 'round' });
  thrArc = svgEl(svg, 'path', { d: '', fill: 'none', stroke: '#555', 'stroke-width': 10, 'stroke-linecap': 'round', filter: 'url(#gl)' });

  // THROTTLE label
  thrLabel = svgEl(svg, 'text', { x: cx, y: cy - Math.round(THROTTLE_R / 2), class: 'g-unit', fill: '#333', 'font-size': 20 });
  thrLabel.textContent = 'THROTTLE';

  // Value arc
  const va = svgEl(svg, 'path', { d: '', fill: 'none', stroke: cfg.color, 'stroke-width': 16, 'stroke-linecap': 'round', filter: 'url(#gl)' });

  // Needle
  const [nx0, ny0] = polarToXY(cx, cy, r - 24, ARC_START);
  const [tx0, ty0] = polarToXY(cx, cy, -16, ARC_START);
  const nd = svgEl(svg, 'line', { x1: tx0, y1: ty0, x2: nx0, y2: ny0, stroke: cfg.color, 'stroke-width': 6, 'stroke-linecap': 'round', filter: 'url(#gl)', 'transform-origin': `${cx}px ${cy}px` });
  nd.style.transition = 'transform 0.3s ease-out';

  // Center dot
  svgEl(svg, 'circle', { cx, cy, r: 8, fill: '#1a1a22', stroke: '#444', 'stroke-width': 2 });

  // Number display
  const numY = cy + r * 0.35;
  const nm = svgEl(svg, 'text', { x: cx, y: numY, class: 'g-num', fill: cfg.color, 'font-size': numSz });
  nm.textContent = '--';

  // Unit label
  const ut = svgEl(svg, 'text', { x: cx, y: numY + numSz * 0.55, class: 'g-unit', fill: '#fff', 'font-size': 28 });
  ut.textContent = unit;

  // Lerp animation
  let curVal = min, tgtVal = min, rafId = 0;
  function lerp() {
    const delta = tgtVal - curVal;
    curVal = Math.abs(delta) > LERP_THRESHOLD ? curVal + delta * LERP_SPEED : tgtVal;
    const clamped = Math.max(min, Math.min(max, curVal));
    const angle = ARC_START + ((clamped - min) / (max - min)) * ARC_SWEEP;
    va.setAttribute('d', clamped > min + 0.001 ? arcPath(cx, cy, r, ARC_START, angle) : '');
    nm.textContent = cfg.fmt(curVal);
    rafId = Math.abs(curVal - tgtVal) > LERP_STOP ? requestAnimationFrame(lerp) : 0;
  }

  return {
    update(value, col) {
      tgtVal = value;
      const clamped = Math.max(min, Math.min(max, value));
      const angle = ARC_START + ((clamped - min) / (max - min)) * ARC_SWEEP;
      nd.style.transform = `rotate(${angle - ARC_START}deg)`;
      if (col) { nd.setAttribute('stroke', col); va.setAttribute('stroke', col); nm.setAttribute('fill', col); }
      if (!rafId) rafId = requestAnimationFrame(lerp);
    }
  };
}

// ============================================================
// Throttle Arc — 内側アークの更新
// 色相: 210(青)→0(赤) をスロットル開度に応じてスムーズ遷移
// アイドル時ベースライン (~11.5%) を差し引いて 0% 基準にする
// ============================================================
let thrCur = 0, thrTgt = 0, thrRafId = 0;

function thrLerp() {
  const delta = thrTgt - thrCur;
  thrCur = Math.abs(delta) > LERP_THRESHOLD ? thrCur + delta * LERP_THR_SPEED : thrTgt;
  const pct = Math.max(0, Math.min(100, thrCur));
  const angle = ARC_START + (pct / 100) * ARC_SWEEP;
  thrArc.setAttribute('d', pct > 0.5 ? arcPath(GAUGE_CX, GAUGE_CY, THROTTLE_R, ARC_START, angle) : '');
  const hue = THR_HUE_MAX - (pct / 100) * THR_HUE_MAX;
  const col = pct > 0.5 ? `hsl(${hue}, 100%, 55%)` : '#333';
  thrArc.setAttribute('stroke', col);
  thrLabel.setAttribute('fill', col);
  thrRafId = Math.abs(thrCur - thrTgt) > LERP_STOP ? requestAnimationFrame(thrLerp) : 0;
}

function updateThrottle(pct) {
  thrTgt = Math.max(0, pct - THR_IDLE_BASELINE);
  if (!thrRafId) thrRafId = requestAnimationFrame(thrLerp);
}

// 速度→ゲージ色（寒色→暖色）
function speedColor(v) {
  if (v >= 120) return '#f44336';
  if (v >= 100) return '#ff9800';
  if (v >= 80)  return '#ffeb3b';
  if (v >= 60)  return '#69f0ae';
  if (v >= 30)  return '#42a5f5';
  return '#78909c';
}

// ============================================================
// Right Panel Indicators
// ============================================================
function updateIndicators(d) {
  // OBD
  const obdOk = d.obd_connected;
  setDot(dom.obd, obdOk ? 'green' : 'red');
  dom.obd.val.textContent = obdOk ? 'OK' : 'NG';

  // WiFi
  const wifiOk = d.wifi_connected;
  setDot(dom.wifi, wifiOk ? 'green' : 'red');
  dom.wifi.val.textContent = wifiOk ? 'OK' : 'NG';

  // SEND
  const pending = d.pending_count || 0;
  const sending = d.send_sending || false;
  if (pending > 0) {
    setDot(dom.send, 'red');
    dom.send.val.textContent = String(pending);
  } else if (sending) {
    setDot(dom.send, 'orange');
    dom.send.val.textContent = '...';
  } else {
    setDot(dom.send, 'green');
    dom.send.val.textContent = 'OK';
  }

  // MAINT
  const alerts = d.alerts || [];
  const hasOverdue = alerts.some(a => a.is_overdue);
  setDot(dom.maint, hasOverdue ? 'red' : alerts.length > 0 ? 'orange' : 'green');
  dom.maint.val.textContent = String(alerts.length);

  // ECO — エンブレ/停車/クリープ時は平均燃費(▸付き)、走行中は瞬間燃費
  const eco = d.fuel_economy || 0;
  const avgEco = d.avg_fuel_economy || 0;
  const fuelRate = d.fuel_rate_lh || 0;
  const speed = d.speed_kmh || 0;
  if (eco < 0) {
    // エンブレ・燃料カット: 平均燃費を表示
    if (avgEco > 0.1) {
      setDot(dom.eco, 'green');
      dom.eco.val.textContent = avgEco.toFixed(1) + '\u25B8';
    } else {
      setDot(dom.eco, 'green');
      dom.eco.val.textContent = '--';
    }
  } else if (eco < 0.1) {
    // 停車・クリープ: 平均燃費を表示
    if (avgEco > 0.1) {
      if (avgEco >= 15)      setDot(dom.eco, 'green');
      else if (avgEco >= 10) setDot(dom.eco, 'orange');
      else                   setDot(dom.eco, 'red');
      dom.eco.val.textContent = avgEco.toFixed(1) + '\u25B8';
    } else {
      setDot(dom.eco, null);
      dom.eco.val.textContent = '0';
    }
  } else if (speed < ECO_LOW_SPEED_THRESHOLD && fuelRate > 0) {
    if (fuelRate < conf.eco_lh_green)      setDot(dom.eco, 'green');
    else if (fuelRate < conf.eco_lh_red)   setDot(dom.eco, 'orange');
    else                                   setDot(dom.eco, 'red');
    dom.eco.val.textContent = eco.toFixed(1);
  } else {
    if (eco >= 15)      setDot(dom.eco, 'green');
    else if (eco >= 10) setDot(dom.eco, 'orange');
    else                setDot(dom.eco, 'red');
    dom.eco.val.textContent = eco.toFixed(1);
  }

  // TRIP
  const tripKm = d.trip_km || 0;
  if (tripKm >= 0.1) {
    dom.trip.val.textContent = tripKm < 10 ? tripKm.toFixed(1) : Math.round(tripKm);
    setDot(dom.trip, tripKm < 300 ? 'green' : tripKm <= 500 ? 'orange' : 'red');
  } else {
    dom.trip.val.textContent = '0';
    setDot(dom.trip, 'green');
  }

  // TEMP
  const ct = d.coolant_temp || 0;
  if (ct > 0) {
    dom.temp.val.textContent = Math.round(ct) + '\u00B0';
    setDot(dom.temp, ct < 70 ? 'orange' : ct <= 100 ? 'green' : 'red');
  } else {
    dom.temp.val.textContent = '--';
    setDot(dom.temp, null);
  }
}

// ============================================================
// Toast Notification
// ============================================================
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

// ============================================================
// Realtime API Polling
// ============================================================
let gs;
let connected = false;
let prevAlertCount = 0;
let alertQueue = [];
let alertQueueTimer = null;

function showAlertQueue() {
  if (alertQueue.length === 0) { alertQueueTimer = null; return; }
  showToast(alertQueue.shift());
  alertQueueTimer = setTimeout(showAlertQueue, ALERT_INTERVAL_MS);
}

function applyData(d) {
  const spd = d.speed_kmh || 0;
  gs.update(spd, speedColor(spd));
  updateThrottle(d.throttle_pos || 0);
  updateIndicators(d);

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

async function fetchRealtime() {
  try {
    const resp = await fetch('/api/realtime');
    if (!resp.ok) throw new Error(resp.status);
    connected = true;
    applyData(await resp.json());
  } catch {
    if (connected || !simMode) {
      connected = false;
      if (!simMode) startSimMode();
    }
  }
}

// ============================================================
// Simulation Fallback — API接続不可時のデモ表示
// 16秒周期で 5フェーズの走行パターンをループ再生
// ============================================================
let simMode = false;
let simT = 0;

function startSimMode() {
  simMode = true;
  setInterval(simTick, SIM_TICK_MS);
}

function simTick() {
  simT += 0.02;
  const phase = simT % SIM_CYCLE_S;
  let speed, throttle, fuelEco;

  if (phase < 3) {
    const p = phase / 3;
    speed = p * p * 60; throttle = 20 + p * 50;
    fuelEco = 3 + p * 12;
  } else if (phase < 7) {
    speed = 60 + Math.sin(simT * 3) * 3;
    throttle = 18 + Math.sin(simT * 2) * 4;
    fuelEco = 18 + Math.sin(simT * 2) * 3;
  } else if (phase < 10) {
    speed = 45 + Math.sin(simT * 2) * 3;
    throttle = 55 + Math.sin(simT * 1.5) * 10;
    fuelEco = 10 + Math.sin(simT * 1.5) * 2;
  } else if (phase < 13) {
    const p = (phase - 10) / 3;
    speed = 45 + p * 40; throttle = 40 + p * 40;
    fuelEco = 6 + (1 - p) * 5;
  } else {
    const p = (phase - 13) / 3;
    speed = 85 * (1 - p * p); throttle = 3 + (1 - p) * 5;
    fuelEco = 25 + p * 20;
  }

  gs.update(Math.max(0, speed), speedColor(speed));
  updateThrottle(throttle);

  setDot(dom.obd, 'green');   dom.obd.val.textContent = 'OK';
  setDot(dom.wifi, 'green');  dom.wifi.val.textContent = 'OK';
  setDot(dom.send, 'green');  dom.send.val.textContent = '0';
  setDot(dom.maint, 'green'); dom.maint.val.textContent = '0';
  setDot(dom.trip, 'green');  dom.trip.val.textContent = '12.3';
  setDot(dom.temp, 'green');  dom.temp.val.textContent = '82\u00B0';

  setDot(dom.eco, fuelEco >= 15 ? 'green' : fuelEco >= 10 ? 'orange' : 'red');
  dom.eco.val.textContent = fuelEco.toFixed(1);
}

// ============================================================
// Initialization
// ============================================================
let conf = DEFAULTS;

async function initApp() {
  initDOM();

  conf = DEFAULTS;
  try {
    const resp = await fetch('/api/config');
    if (resp.ok) conf = { ...DEFAULTS, ...await resp.json() };
  } catch { /* file:// mode */ }

  if (conf.version) {
    document.getElementById('version').textContent = conf.version;
  }

  gs = buildSpeedGauge('gs', {
    cx: GAUGE_CX, cy: GAUGE_CY, r: GAUGE_R,
    min: 0, max: conf.max_speed_kmh, color: '#78909c',
    unit: 'km/h', mj: 9, mn: 5, numSz: 84, tkSz: 28,
    fmt: v => v > 0.5 ? String(Math.round(v)) : '--'
  });

  fetchRealtime();
  setInterval(fetchRealtime, POLL_INTERVAL_MS);
}

initApp();
