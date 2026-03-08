// ============================================================
// SVG Geometry Helpers
// 270° arc gauge: 12時を0°として左右±135°の範囲で描画する
// ============================================================
const D = Math.PI / 180;          // deg→rad 変換定数
const SA = -135, EA = 135, SW = 270; // 開始角, 終了角, 全角度幅

// 極座標→直交座標（12時=0°、時計回り正）
function p2xy(cx, cy, r, deg) {
  const rad = deg * D;
  return [cx + r * Math.sin(rad), cy - r * Math.cos(rad)];
}

// SVGアークパス文字列を生成（角度 s→e）
function arcPath(cx, cy, r, s, e) {
  if (Math.abs(e - s) < 0.3) return '';
  const [x1, y1] = p2xy(cx, cy, r, s);
  const [x2, y2] = p2xy(cx, cy, r, e);
  const lg = Math.abs(e - s) > 180 ? 1 : 0;
  return `M${x1.toFixed(1)},${y1.toFixed(1)}A${r},${r},0,${lg},1,${x2.toFixed(1)},${y2.toFixed(1)}`;
}

// SVG要素を生成して親に追加
function el(svg, tag, attrs) {
  const e = document.createElementNS('http://www.w3.org/2000/svg', tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  svg.appendChild(e);
  return e;
}

// グロー効果用SVGフィルター（1回だけ生成）
let filterCreated = false;
function ensureFilter(svg) {
  if (filterCreated) return;
  filterCreated = true;
  const defs = el(svg, 'defs', {});
  defs.innerHTML = '<filter id="gl" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="3" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>';
}

// ============================================================
// Speed Gauge Builder
// 外周: 速度アークゲージ（針 + 値アーク + 目盛り + レッドゾーン）
// 内周: スロットル開度アーク（HSLグラデーション）
// requestAnimationFrame で 60fps 補間（ease-out, ~300ms）
// ============================================================
let thrArc, thrLabel;
function buildSpeedGauge(svgId, cfg) {
  const svg = document.getElementById(svgId);
  const { cx, cy, r, min, max, unit, mj, mn, numSz, tkSz } = cfg;

  ensureFilter(svg);

  // Track (thick bezel)
  el(svg, 'path', { d: arcPath(cx, cy, r, SA, EA), fill: 'none', stroke: '#181820', 'stroke-width': 16, 'stroke-linecap': 'round' });

  // Ticks (bold)
  const total = mj * mn;
  for (let i = 0; i <= total; i++) {
    const a = SA + (i / total) * SW;
    const isMj = i % mn === 0;
    const ri = isMj ? r - 30 : r - 18;
    const ro = r + 4;
    const [x1, y1] = p2xy(cx, cy, ri, a);
    const [x2, y2] = p2xy(cx, cy, ro, a);
    el(svg, 'line', { x1, y1, x2, y2, stroke: isMj ? '#aaa' : '#444', 'stroke-width': isMj ? 5 : 2.5 });
    if (isMj) {
      const v = min + (i / total) * (max - min);
      const [lx, ly] = p2xy(cx, cy, r - 50, a);
      const t = el(svg, 'text', { x: lx, y: ly, class: 'tk-lbl', fill: '#fff', 'font-size': tkSz });
      t.textContent = Math.round(v);
    }
  }

  // THR inner arc (track, thick)
  const thrR = r - 80;
  el(svg, 'path', { d: arcPath(cx, cy, thrR, SA, EA), fill: 'none', stroke: '#111', 'stroke-width': 10, 'stroke-linecap': 'round' });
  thrArc = el(svg, 'path', { d: '', fill: 'none', stroke: '#555', 'stroke-width': 10, 'stroke-linecap': 'round', filter: 'url(#gl)' });

  // THROTTLE label (between THR arc top and center)
  thrLabel = el(svg, 'text', { x: cx, y: cy - Math.round(thrR / 2), class: 'g-unit', fill: '#333', 'font-size': 20 });
  thrLabel.textContent = 'THROTTLE';

  // Value arc (thick)
  const va = el(svg, 'path', { d: '', fill: 'none', stroke: cfg.color, 'stroke-width': 16, 'stroke-linecap': 'round', filter: 'url(#gl)' });

  // Needle (bold)
  const [nx0, ny0] = p2xy(cx, cy, r - 24, SA);
  const [tx0, ty0] = p2xy(cx, cy, -16, SA);
  const nd = el(svg, 'line', { x1: tx0, y1: ty0, x2: nx0, y2: ny0, stroke: cfg.color, 'stroke-width': 6, 'stroke-linecap': 'round', filter: 'url(#gl)', 'transform-origin': `${cx}px ${cy}px` });
  nd.style.transition = 'transform 0.3s ease-out';

  // Center dot
  el(svg, 'circle', { cx, cy, r: 8, fill: '#1a1a22', stroke: '#444', 'stroke-width': 2 });

  // Number
  const numY = cy + r * 0.35;
  const nm = el(svg, 'text', { x: cx, y: numY, class: 'g-num', fill: cfg.color, 'font-size': numSz });
  nm.textContent = '--';

  // Unit
  const ut = el(svg, 'text', { x: cx, y: numY + numSz * 0.55, class: 'g-unit', fill: '#fff', 'font-size': 28 });
  ut.textContent = unit;

  // Lerp
  let curVal = min, tgtVal = min, rafId = 0;
  function lerp() {
    const d = tgtVal - curVal;
    if (Math.abs(d) > 0.05) { curVal += d * 0.15; } else { curVal = tgtVal; }
    const c = Math.max(min, Math.min(max, curVal));
    const a = SA + ((c - min) / (max - min)) * SW;
    va.setAttribute('d', c > min + 0.001 ? arcPath(cx, cy, r, SA, a) : '');
    nm.textContent = cfg.fmt(curVal);
    if (Math.abs(curVal - tgtVal) > 0.01) { rafId = requestAnimationFrame(lerp); } else { rafId = 0; }
  }

  return {
    update(value, col) {
      tgtVal = value;
      const clamped = Math.max(min, Math.min(max, value));
      const angle = SA + ((clamped - min) / (max - min)) * SW;
      nd.style.transform = `rotate(${angle - SA}deg)`;
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
const THR_IDLE = 11.5;
let thrCur = 0, thrTgt = 0, thrRafId = 0;
function thrLerp() {
  const d = thrTgt - thrCur;
  if (Math.abs(d) > 0.05) { thrCur += d * 0.2; } else { thrCur = thrTgt; }
  const pct = Math.max(0, Math.min(100, thrCur));
  const thrR = GR - 80;
  const a = SA + (pct / 100) * SW;
  thrArc.setAttribute('d', pct > 0.5 ? arcPath(GCX, GCY, thrR, SA, a) : '');
  const hue = 210 - (pct / 100) * 210; // blue(210)→red(0)
  const col = pct > 0.5 ? `hsl(${hue}, 100%, 55%)` : '#333';
  thrArc.setAttribute('stroke', col);
  thrLabel.setAttribute('fill', col);
  if (Math.abs(thrCur - thrTgt) > 0.01) { thrRafId = requestAnimationFrame(thrLerp); } else { thrRafId = 0; }
}
function updateThrottle(pct) {
  thrTgt = Math.max(0, pct - THR_IDLE);
  if (!thrRafId) thrRafId = requestAnimationFrame(thrLerp);
}

// 速度→ゲージ色（寒色→暖色）
function spdCol(v) {
  if (v >= 120) return '#f44336';
  if (v >= 100) return '#ff9800';
  if (v >= 80) return '#ffeb3b';
  if (v >= 60) return '#69f0ae';
  if (v >= 30) return '#42a5f5';
  return '#78909c';
}

// ============================================================
// Right Panel Indicators (ECO, TRIP, TEMP, MAINT, SEND, WiFi, OBD)
// ============================================================
const indEco = document.getElementById('ind-eco');
const ecoVal = document.getElementById('eco-val');
const indSend = document.getElementById('ind-send');
const sendVal = document.getElementById('send-val');
const indMaint = document.getElementById('ind-maint');
const maintVal = document.getElementById('maint-val');
const indObd = document.getElementById('ind-obd');
const obdVal = document.getElementById('obd-val');
const indWifi = document.getElementById('ind-wifi');
const wifiVal = document.getElementById('wifi-val');
const indTrip = document.getElementById('ind-trip');
const tripVal = document.getElementById('trip-val');
const indTemp = document.getElementById('ind-temp');
const tempVal = document.getElementById('temp-val');

function updateIndicators(d) {
  // OBD
  const obdOk = d.obd_connected;
  indObd.className = 'ind-dot ' + (obdOk ? 'on-green' : 'on-red');
  obdVal.textContent = obdOk ? 'OK' : 'NG';

  // WiFi
  const wifiOk = d.wifi_connected;
  indWifi.className = 'ind-dot ' + (wifiOk ? 'on-green' : 'on-red');
  wifiVal.textContent = wifiOk ? 'OK' : 'NG';

  // SEND (sending=orange, failed in queue=red, idle=green)
  const pending = d.pending_count || 0;
  const sending = d.send_sending || false;
  if (pending > 0) {
    indSend.className = 'ind-dot on-red';
    sendVal.textContent = String(pending);
  } else if (sending) {
    indSend.className = 'ind-dot on-orange';
    sendVal.textContent = '...';
  } else {
    indSend.className = 'ind-dot on-green';
    sendVal.textContent = 'OK';
  }

  // MAINT (alert count)
  const alerts = d.alerts || [];
  const hasOverdue = alerts.some(a => a.is_overdue);
  const hasWarning = alerts.length > 0;
  indMaint.className = 'ind-dot ' + (hasOverdue ? 'on-red' : hasWarning ? 'on-orange' : 'on-green');
  maintVal.textContent = String(alerts.length);

  // ECO (instantaneous fuel economy km/L)
  const eco = d.fuel_economy || 0;
  if (eco < 0.1) {
    indEco.className = 'ind-dot';
  } else if (eco >= 15) {
    indEco.className = 'ind-dot on-green';
  } else if (eco >= 10) {
    indEco.className = 'ind-dot on-orange';
  } else {
    indEco.className = 'ind-dot on-red';
  }
  ecoVal.textContent = eco > 0.1 ? eco.toFixed(1) : '0';

  // TRIP: 走行距離 (green=<100km, orange=100-200km, red=>200km)
  const tripKm = d.trip_km || 0;
  if (tripKm >= 0.1) {
    tripVal.textContent = tripKm < 10 ? tripKm.toFixed(1) : Math.round(tripKm);
    if (tripKm < 300) indTrip.className = 'ind-dot on-green';
    else if (tripKm <= 500) indTrip.className = 'ind-dot on-orange';
    else indTrip.className = 'ind-dot on-red';
  } else {
    tripVal.textContent = '0';
    indTrip.className = 'ind-dot on-green';
  }

  // TEMP: 冷却水温 (blue=暖機中<70, green=正常70-105, red=過熱>105)
  const ct = d.coolant_temp || 0;
  if (ct > 0) {
    tempVal.textContent = Math.round(ct) + '\u00B0';
    if (ct < 70) indTemp.className = 'ind-dot on-orange';
    else if (ct <= 100) indTemp.className = 'ind-dot on-green';
    else indTemp.className = 'ind-dot on-red';
  } else {
    tempVal.textContent = '--';
    indTemp.className = 'ind-dot';
  }
}

// ============================================================
// Toast Notification — メンテナンス警告等を画面中央に一時表示
// ============================================================
const toastEl = document.getElementById('toast');
let lastNotification = '';
let toastTimer = null;
function showToast(msg) {
  if (msg === lastNotification) return;
  lastNotification = msg;
  toastEl.textContent = msg;
  toastEl.classList.add('show');
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { toastEl.classList.remove('show'); }, 5000);
}

// ============================================================
// Config & Gauge Layout Constants
// ============================================================
const DEFAULTS = { max_speed_kmh: 180 };
const GCX = 280, GCY = 260, GR = 220; // ゲージ中心座標・半径

let gs;

// ============================================================
// Realtime API Polling — /api/realtime を 200ms 間隔でポーリング
// 接続失敗時はシミュレーションモードにフォールバック
// ============================================================
let connected = false;
let prevAlertCount = 0;
let alertQueue = [];
let alertQueueTimer = null;

function showAlertQueue() {
  if (alertQueue.length === 0) { alertQueueTimer = null; return; }
  const msg = alertQueue.shift();
  showToast(msg);
  alertQueueTimer = setTimeout(showAlertQueue, 5500);
}

function applyData(d) {
  const spd = d.speed_kmh || 0;
  const throttle = d.throttle_pos || 0;

  gs.update(spd, spdCol(spd));
  updateThrottle(throttle);
  updateIndicators(d);

  // Maintenance toast（全件を順番に表示）
  const alertCount = d.alerts ? d.alerts.length : 0;
  if (alertCount > 0 && alertCount !== prevAlertCount) {
    alertQueue = [];
    if (alertQueueTimer) { clearTimeout(alertQueueTimer); alertQueueTimer = null; }
    d.alerts.forEach(a => {
      const r = a.reminder;
      const remain = r.type === 'distance'
        ? `${Math.round(a.remaining_km).toLocaleString()} km`
        : `${a.days_left} 日`;
      alertQueue.push(`\u26A0 ${r.name}まで ${remain}`);
    });
    showAlertQueue();
  }
  prevAlertCount = alertCount;

  if (d.notification) { showToast(d.notification); }
  else if (lastNotification) { lastNotification = ''; }
}

async function fetchRealtime() {
  try {
    const resp = await fetch('/api/realtime');
    if (!resp.ok) throw new Error(resp.status);
    const d = await resp.json();
    connected = true;
    applyData(d);
  } catch (e) {
    if (connected || !simMode) {
      connected = false;
      if (!simMode) startSimMode();
    }
  }
}

// ============================================================
// Simulation Fallback — API接続不可時のデモ表示
// 16秒周期で 5フェーズの走行パターンをループ再生:
//   0-3s: 発進加速  3-7s: 巡航  7-10s: 登坂  10-13s: 再加速  13-16s: 減速
// ============================================================
let simMode = false;
let simT = 0;

function startSimMode() {
  simMode = true;
  setInterval(simTick, 50);
}

function simTick() {
  simT += 0.02;
  const phase = simT % 16;
  let speed, throttle, fuelEco;

  if (phase < 3) {        // 発進加速 (0→60km/h)
    const p = phase / 3;
    speed = p * p * 60; throttle = 20 + p * 50;
    fuelEco = 3 + p * 12;
  } else if (phase < 7) { // 巡航 (~60km/h)
    speed = 60 + Math.sin(simT * 3) * 3;
    throttle = 18 + Math.sin(simT * 2) * 4;
    fuelEco = 18 + Math.sin(simT * 2) * 3;
  } else if (phase < 10) { // 登坂 (~45km/h, 高スロットル)
    speed = 45 + Math.sin(simT * 2) * 3;
    throttle = 55 + Math.sin(simT * 1.5) * 10;
    fuelEco = 10 + Math.sin(simT * 1.5) * 2;
  } else if (phase < 13) { // 再加速 (45→85km/h)
    const p = (phase - 10) / 3;
    speed = 45 + p * 40; throttle = 40 + p * 40;
    fuelEco = 6 + (1 - p) * 5;
  } else {                 // 減速 (85→0km/h, エンブレ)
    const p = (phase - 13) / 3;
    speed = 85 * (1 - p * p); throttle = 3 + (1 - p) * 5;
    fuelEco = 25 + p * 20;
  }

  gs.update(Math.max(0, speed), spdCol(speed));
  updateThrottle(throttle);

  // Simulate indicators
  indObd.className = 'ind-dot on-green'; obdVal.textContent = 'OK';
  indWifi.className = 'ind-dot on-green'; wifiVal.textContent = 'OK';
  indSend.className = 'ind-dot on-green'; sendVal.textContent = '0';
  indMaint.className = 'ind-dot on-green'; maintVal.textContent = '0';
  indTrip.className = 'ind-dot on-green'; tripVal.textContent = '12.3';
  indTemp.className = 'ind-dot on-green'; tempVal.textContent = '82\u00B0';

  if (fuelEco >= 15) indEco.className = 'ind-dot on-green';
  else if (fuelEco >= 10) indEco.className = 'ind-dot on-orange';
  else indEco.className = 'ind-dot on-red';
  ecoVal.textContent = fuelEco.toFixed(1);
}

// ============================================================
// Initialization — /api/config でゲージ設定を取得し、ポーリング開始
// ============================================================
async function initApp() {
  let conf = DEFAULTS;
  try {
    const resp = await fetch('/api/config');
    if (resp.ok) conf = { ...DEFAULTS, ...await resp.json() };
  } catch (e) { /* file:// mode */ }

  gs = buildSpeedGauge('gs', {
    cx: GCX, cy: GCY, r: GR, min: 0, max: conf.max_speed_kmh, color: '#78909c',
    unit: 'km/h', mj: 9, mn: 5, numSz: 84, tkSz: 28,
    fmt: v => v > 0.5 ? String(Math.round(v)) : '--'
  });

  fetchRealtime();
  setInterval(fetchRealtime, 200);
}
initApp();
