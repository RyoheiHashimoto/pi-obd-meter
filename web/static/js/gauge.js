// ============================================================
// Speed Gauge — SVGアークゲージ + スロットルアーク + 60fps補間
// ============================================================

const DEG_TO_RAD = Math.PI / 180;
const ARC_START = -135;
const ARC_END = 135;
const ARC_SWEEP = 270;
const THROTTLE_R_OFFSET = 80;

const LERP_SPEED = 0.15;
const LERP_THR_SPEED = 0.2;
const LERP_THRESHOLD = 0.05;
const LERP_STOP = 0.01;

const THR_IDLE_BASELINE = 11.5;
const THR_HUE_MAX = 210;

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

// --- スロットルアーク ---
let thrArc, thrLabel;
let thrCx, thrCy, thrR;
let thrCur = 0, thrTgt = 0, thrRafId = 0;

function thrLerp() {
  const delta = thrTgt - thrCur;
  thrCur = Math.abs(delta) > LERP_THRESHOLD ? thrCur + delta * LERP_THR_SPEED : thrTgt;
  const pct = Math.max(0, Math.min(100, thrCur));
  const angle = ARC_START + (pct / 100) * ARC_SWEEP;
  thrArc.setAttribute('d', pct > 0.5 ? arcPath(thrCx, thrCy, thrR, ARC_START, angle) : '');
  const hue = THR_HUE_MAX - (pct / 100) * THR_HUE_MAX;
  const col = pct > 0.5 ? `hsl(${hue}, 100%, 55%)` : '#333';
  thrArc.setAttribute('stroke', col);
  applyGlow(thrArc, col);
  thrLabel.setAttribute('fill', col);
  thrRafId = Math.abs(thrCur - thrTgt) > LERP_STOP ? requestAnimationFrame(thrLerp) : 0;
}

export function updateThrottle(pct) {
  thrTgt = Math.max(0, pct - THR_IDLE_BASELINE);
  if (!thrRafId) thrRafId = requestAnimationFrame(thrLerp);
}

// --- スピードゲージ構築 ---
export function buildSpeedGauge(svgId, cfg) {
  const svg = document.getElementById(svgId);
  const { cx, cy, r, min, max, unit, mj, mn, numSz, tkSz } = cfg;
  const throttleR = r - THROTTLE_R_OFFSET;

  thrCx = cx;
  thrCy = cy;
  thrR = throttleR;


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
  svgEl(svg, 'path', { d: arcPath(cx, cy, throttleR, ARC_START, ARC_END), fill: 'none', stroke: '#111', 'stroke-width': 10, 'stroke-linecap': 'round' });
  thrArc = svgEl(svg, 'path', { d: '', fill: 'none', stroke: '#555', 'stroke-width': 10, 'stroke-linecap': 'round' });

  // THROTTLE label
  thrLabel = svgEl(svg, 'text', { x: cx, y: cy - Math.round(throttleR / 2), class: 'g-unit', fill: '#333', 'font-size': 20 });
  thrLabel.textContent = 'THROTTLE';

  // Value arc
  const va = svgEl(svg, 'path', { d: '', fill: 'none', stroke: cfg.color, 'stroke-width': 16, 'stroke-linecap': 'round' });
  applyGlow(va, cfg.color);

  // Needle
  const [nx0, ny0] = polarToXY(cx, cy, r - 24, ARC_START);
  const [tx0, ty0] = polarToXY(cx, cy, -16, ARC_START);
  const nd = svgEl(svg, 'line', { x1: tx0, y1: ty0, x2: nx0, y2: ny0, stroke: cfg.color, 'stroke-width': 6, 'stroke-linecap': 'round', 'transform-origin': `${cx}px ${cy}px` });
  applyGlow(nd, cfg.color);
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
      if (col) { nd.setAttribute('stroke', col); va.setAttribute('stroke', col); nm.setAttribute('fill', col); applyGlow(nd, col); applyGlow(va, col); }
      if (!rafId) rafId = requestAnimationFrame(lerp);
    }
  };
}
