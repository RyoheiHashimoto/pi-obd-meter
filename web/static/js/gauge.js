// ============================================================
// Speed Gauge — SVGアークゲージ + ArcAnimator + 60fps補間
// ============================================================

const DEG_TO_RAD = Math.PI / 180;
const ARC_START = -135;
const ARC_END = 135;
const ARC_SWEEP = 270;
const MAP_R_OFFSET = 80;
const FUEL_R_OFFSET = 18;
const THROTTLE_R_OFFSET = 18;

const LERP_SPEED = 0.35;
const LERP_THRESHOLD = 0.05;
const LERP_STOP = 0.01;
const HUE_MAX = 210;

const MAP_MAX_KPA = 101.3;  // NA車の最大（大気圧）
const FUEL_MAX_LH = 20;     // 燃料レートアーク最大値

let thrIdleBaseline = 11.5;
let thrMaxPct = 78;

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

// グロー効果（CSS drop-shadow）をSVG要素に適用
function applyGlow(el, color) {
  el.style.filter = `drop-shadow(0 0 6px ${color})`;
}

// 速度→ゲージ色（寒色→暖色）
export function speedColor(v) {
  if (v >= 120) return '#f44336';
  if (v >= 100) return '#ff9800';
  if (v >= 80)  return '#ffeb3b';
  if (v >= 60)  return '#69f0ae';
  if (v >= 30)  return '#42a5f5';
  return '#78909c';
}

// --- ArcAnimator: アーク LERP アニメーションの共通クラス ---
// スロットル・MAP・燃料レートの3アークで共通のパターンを集約。
// 各アークは value → percentage → angle → arcPath → HSL色 → glow の流れで描画する。
class ArcAnimator {
  constructor({ cx, cy, r, maxVal, lerpSpeed, arcEl, offColor, activeThreshold, labelEl, readoutVal, readoutUnit, formatVal }) {
    this.cx = cx;
    this.cy = cy;
    this.r = r;
    this.maxVal = maxVal;
    this.lerpSpeed = lerpSpeed;
    this.arcEl = arcEl;
    this.offColor = offColor || '#222';
    this.activeThreshold = activeThreshold ?? 0.5;
    this.labelEl = labelEl || null;
    this.readoutVal = readoutVal || null;
    this.readoutUnit = readoutUnit || null;
    this.formatVal = formatVal || (v => String(Math.round(v)));
    this.cur = 0;
    this.tgt = 0;
    this.rafId = 0;
    this._lerp = this._lerp.bind(this);
  }

  update(value) {
    this.tgt = value;
    if (!this.rafId) this.rafId = requestAnimationFrame(this._lerp);
  }

  _lerp() {
    const delta = this.tgt - this.cur;
    this.cur = Math.abs(delta) > LERP_THRESHOLD ? this.cur + delta * this.lerpSpeed : this.tgt;
    const pct = Math.max(0, Math.min(100, this.cur / this.maxVal * 100));
    const angle = ARC_START + (pct / 100) * ARC_SWEEP;
    this.arcEl.setAttribute('d', pct > 0.5 ? arcPath(this.cx, this.cy, this.r, ARC_START, angle) : '');
    const hue = HUE_MAX - (pct / 100) * HUE_MAX;
    const active = this.cur > this.activeThreshold;
    const col = active ? `hsl(${hue}, 100%, 55%)` : this.offColor;
    this.arcEl.setAttribute('stroke', col);
    applyGlow(this.arcEl, col);
    if (this.labelEl) this.labelEl.setAttribute('fill', col);
    if (this.readoutVal) {
      const rdCol = active ? col : '#333';
      this.readoutVal.setAttribute('fill', rdCol);
      this.readoutUnit.setAttribute('fill', rdCol);
      this.readoutVal.textContent = active ? this.formatVal(this.cur) : '--';
    }
    this.rafId = Math.abs(this.cur - this.tgt) > LERP_STOP ? requestAnimationFrame(this._lerp) : 0;
  }
}

// --- スロットル / MAP / FuelRate アニメーター ---
let thrAnimator, mapAnimator, fuelAnimator;

export function setThrottleIdleBaseline(val) { thrIdleBaseline = val; }
export function setThrottleMaxPct(val) { thrMaxPct = val; }

export function updateThrottle(pct) {
  const range = thrMaxPct - thrIdleBaseline;
  const normalized = range > 0 ? Math.min(100, Math.max(0, (pct - thrIdleBaseline) / range * 100)) : 0;
  thrAnimator.update(normalized);
}

export function updateMAP(kPa) {
  mapAnimator.update(kPa);
}

export function updateFuelRate(lh) {
  fuelAnimator.update(lh);
}

// --- スピードゲージ構築 ---
export function buildSpeedGauge(svgId, cfg) {
  const svg = document.getElementById(svgId);
  const { cx, cy, r, min, max, unit, mj, mn, numSz, tkSz } = cfg;
  const mapArcR = r - MAP_R_OFFSET;
  const fuelArcR = mapArcR - FUEL_R_OFFSET;
  const throttleR = fuelArcR - THROTTLE_R_OFFSET;

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

  // MAP arc (track)
  svgEl(svg, 'path', { d: arcPath(cx, cy, mapArcR, ARC_START, ARC_END), fill: 'none', stroke: '#111', 'stroke-width': 10, 'stroke-linecap': 'round' });
  const mapArcEl = svgEl(svg, 'path', { d: '', fill: 'none', stroke: '#222', 'stroke-width': 10, 'stroke-linecap': 'round' });

  // Fuel rate arc (track)
  svgEl(svg, 'path', { d: arcPath(cx, cy, fuelArcR, ARC_START, ARC_END), fill: 'none', stroke: '#0e0e14', 'stroke-width': 10, 'stroke-linecap': 'round' });
  const fuelArcEl = svgEl(svg, 'path', { d: '', fill: 'none', stroke: '#222', 'stroke-width': 10, 'stroke-linecap': 'round' });

  // Throttle arc (track) — innermost
  svgEl(svg, 'path', { d: arcPath(cx, cy, throttleR, ARC_START, ARC_END), fill: 'none', stroke: '#0a0a0f', 'stroke-width': 10, 'stroke-linecap': 'round' });
  const thrArcEl = svgEl(svg, 'path', { d: '', fill: 'none', stroke: '#555', 'stroke-width': 10, 'stroke-linecap': 'round' });

  // THROTTLE label
  const thrLabel = svgEl(svg, 'text', { x: cx, y: cy - Math.round(throttleR / 2), class: 'g-unit', fill: '#333', 'font-size': 20 });
  thrLabel.textContent = 'THROTTLE';

  // Value arc
  const va = svgEl(svg, 'path', { d: '', fill: 'none', stroke: cfg.color, 'stroke-width': 16, 'stroke-linecap': 'round' });
  applyGlow(va, cfg.color);

  // Needle
  const [nx0, ny0] = polarToXY(cx, cy, r - 24, ARC_START);
  const [tx0, ty0] = polarToXY(cx, cy, -16, ARC_START);
  const nd = svgEl(svg, 'line', { x1: tx0, y1: ty0, x2: nx0, y2: ny0, stroke: cfg.color, 'stroke-width': 6, 'stroke-linecap': 'round', 'transform-origin': `${cx}px ${cy}px` });
  applyGlow(nd, cfg.color);
  nd.style.transition = 'transform 0.15s ease-out';

  // Center dot
  svgEl(svg, 'circle', { cx, cy, r: 8, fill: '#1a1a22', stroke: '#444', 'stroke-width': 2 });

  // Number display
  const numY = cy + r * 0.35;
  const nm = svgEl(svg, 'text', { x: cx, y: numY, class: 'g-num', fill: cfg.color, 'font-size': numSz });
  nm.textContent = '0';

  // Unit label
  const ut = svgEl(svg, 'text', { x: cx, y: numY + numSz * 0.55, class: 'g-unit', fill: '#fff', 'font-size': 28 });
  ut.textContent = unit;

  // MAP / Fuel readouts (km/h の下) — 値と単位を分離して単位位置を固定
  const rdY = numY + numSz * 0.55 + 58;
  const rdFs = 24;
  const mapValEl  = svgEl(svg, 'text', { x: cx - 88, y: rdY, class: 'g-num', fill: '#333', 'font-size': rdFs, style: 'text-anchor:end' });
  mapValEl.textContent = '--';
  const mapUnitEl = svgEl(svg, 'text', { x: cx - 82, y: rdY, class: 'g-num', fill: '#333', 'font-size': rdFs, style: 'text-anchor:start' });
  mapUnitEl.textContent = 'kPa';
  const fuelValEl  = svgEl(svg, 'text', { x: cx + 82, y: rdY, class: 'g-num', fill: '#333', 'font-size': rdFs, style: 'text-anchor:end' });
  fuelValEl.textContent = '--';
  const fuelUnitEl = svgEl(svg, 'text', { x: cx + 88, y: rdY, class: 'g-num', fill: '#333', 'font-size': rdFs, style: 'text-anchor:start' });
  fuelUnitEl.textContent = 'L/h';

  // ArcAnimator インスタンス生成
  thrAnimator = new ArcAnimator({
    cx, cy, r: throttleR, maxVal: 100, lerpSpeed: 0.4,
    arcEl: thrArcEl, offColor: '#333', activeThreshold: 0.5, labelEl: thrLabel,
  });
  mapAnimator = new ArcAnimator({
    cx, cy, r: mapArcR, maxVal: MAP_MAX_KPA, lerpSpeed: 0.3,
    arcEl: mapArcEl, offColor: '#222', activeThreshold: 0.5,
    readoutVal: mapValEl, readoutUnit: mapUnitEl, formatVal: v => String(Math.round(v)),
  });
  fuelAnimator = new ArcAnimator({
    cx, cy, r: fuelArcR, maxVal: FUEL_MAX_LH, lerpSpeed: 0.35,
    arcEl: fuelArcEl, offColor: '#222', activeThreshold: 0.01,
    readoutVal: fuelValEl, readoutUnit: fuelUnitEl, formatVal: v => v.toFixed(1),
  });

  // Speed Lerp animation
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
      if (col) { nd.setAttribute('stroke', col); va.setAttribute('stroke', col); nm.setAttribute('fill', col); applyGlow(nd, col); applyGlow(va, col); }
      if (!rafId) rafId = requestAnimationFrame(lerp);
    }
  };
}
