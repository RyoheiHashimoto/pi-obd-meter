// ============================================================
// Indicators — 右パネル 2連二重アークメーター + OIL ランプ
// ============================================================

const DEG = Math.PI / 180;
const MG_ARC_START = -135;
const MG_ARC_END = 135;
const MG_ARC_SWEEP = 270;
const MG_LERP = 0.35;
const MG_LERP_TH = 0.05;
const MG_LERP_STOP = 0.01;
const HUE_MAX = 210;

// 2連メーター: 上下に240pxずつ、メーター大きく
const GAUGE_CX = 110;
const GAUGE_R = 118;
const GAUGE_Y1 = 135;  // 上メーター中心
const GAUGE_Y2 = 355;  // 下メーター中心

// --- SVG helpers ---
function mgPolar(cx, cy, r, deg) {
  const rad = deg * DEG;
  return [cx + r * Math.sin(rad), cy - r * Math.cos(rad)];
}

function mgArcPath(cx, cy, r, s, e) {
  if (Math.abs(e - s) < 0.3) return '';
  const [x1, y1] = mgPolar(cx, cy, r, s);
  const [x2, y2] = mgPolar(cx, cy, r, e);
  const lg = Math.abs(e - s) > 180 ? 1 : 0;
  return `M${x1.toFixed(1)},${y1.toFixed(1)}A${r},${r},0,${lg},1,${x2.toFixed(1)},${y2.toFixed(1)}`;
}

function mgSvgEl(parent, tag, attrs) {
  const e = document.createElementNS('http://www.w3.org/2000/svg', tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  parent.appendChild(e);
  return e;
}

// --- Mini Gauge Builder ---
function buildMiniGauge(svg, cfg) {
  const { cx, cy, r, outerMin, outerMax, innerMin, innerMax, outerUnit, innerUnit,
          outerColor, innerColor, outerFmt, innerFmt, outerInvert, innerInvert } = cfg;
  const oMin = outerMin || 0;
  const iMin = innerMin || 0;
  const ARC_W = 10;
  const outerR = r;
  const innerR = r - ARC_W - 5;

  mgSvgEl(svg, 'path', { d: mgArcPath(cx, cy, outerR, MG_ARC_START, MG_ARC_END), fill: 'none', stroke: '#181820', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });
  mgSvgEl(svg, 'path', { d: mgArcPath(cx, cy, innerR, MG_ARC_START, MG_ARC_END), fill: 'none', stroke: '#0e0e14', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });

  const outerArc = mgSvgEl(svg, 'path', { d: '', fill: 'none', stroke: outerColor || '#555', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });
  const innerArc = mgSvgEl(svg, 'path', { d: '', fill: 'none', stroke: innerColor || '#555', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });

  const outerValEl = mgSvgEl(svg, 'text', { x: cx, y: cy - 28, class: 'g-num', fill: '#333', 'font-size': 40, 'text-anchor': 'middle' });
  outerValEl.textContent = '--';
  const outerUnitEl = mgSvgEl(svg, 'text', { x: cx, y: cy - 4, class: 'g-unit', fill: '#fff', 'font-size': 20, 'text-anchor': 'middle' });
  outerUnitEl.textContent = outerUnit || '';

  const innerValEl = mgSvgEl(svg, 'text', { x: cx, y: cy + 44, class: 'g-num', fill: '#333', 'font-size': 40, 'text-anchor': 'middle' });
  innerValEl.textContent = '--';
  const innerUnitEl = mgSvgEl(svg, 'text', { x: cx, y: cy + 68, class: 'g-unit', fill: '#fff', 'font-size': 20, 'text-anchor': 'middle' });
  innerUnitEl.textContent = innerUnit || '';

  let colorMode = 'default';

  function resolveColor(pct, sat, lum) {
    if (colorMode.startsWith('fixed:')) return colorMode.slice(6);
    if (colorMode === 'blue-green') {
      const hue = 210 - (pct / 100) * 70;
      return `hsl(${hue}, ${sat}%, ${lum}%)`;
    }
    const hue = HUE_MAX - (pct / 100) * HUE_MAX;
    return `hsl(${hue}, ${sat}%, ${lum}%)`;
  }

  let outerCur = 0, outerTgt = 0, outerRaf = 0;
  let innerCur = 0, innerTgt = 0, innerRaf = 0;

  function lerpOuter() {
    const delta = outerTgt - outerCur;
    outerCur = Math.abs(delta) > MG_LERP_TH ? outerCur + delta * MG_LERP : outerTgt;
    let pct = Math.max(0, Math.min(100, (outerCur - oMin) / (outerMax - oMin) * 100));
    if (outerInvert) pct = 100 - pct;
    const angle = MG_ARC_START + (pct / 100) * MG_ARC_SWEEP;
    outerArc.setAttribute('d', pct > 0.5 ? mgArcPath(cx, cy, outerR, MG_ARC_START, angle) : '');
    const active = outerCur > oMin + 0.01;
    const col = active ? resolveColor(pct, 100, 55) : '#333';
    outerArc.setAttribute('stroke', col);
    outerArc.style.filter = active ? `drop-shadow(0 0 4px ${col})` : '';
    outerValEl.setAttribute('fill', active ? col : '#333');
    outerValEl.textContent = active ? (outerFmt || (v => Math.round(v)))(outerCur) : '--';
    outerRaf = Math.abs(outerCur - outerTgt) > MG_LERP_STOP ? requestAnimationFrame(lerpOuter) : 0;
  }

  function lerpInner() {
    const delta = innerTgt - innerCur;
    innerCur = Math.abs(delta) > MG_LERP_TH ? innerCur + delta * MG_LERP : innerTgt;
    let pct = Math.max(0, Math.min(100, (innerCur - iMin) / (innerMax - iMin) * 100));
    if (innerInvert) pct = 100 - pct;
    const angle = MG_ARC_START + (pct / 100) * MG_ARC_SWEEP;
    innerArc.setAttribute('d', pct > 0.5 ? mgArcPath(cx, cy, innerR, MG_ARC_START, angle) : '');
    const active = innerCur > iMin + 0.01;
    const col = active ? resolveColor(pct, 85, 50) : '#222';
    innerArc.setAttribute('stroke', col);
    innerArc.style.filter = active ? `drop-shadow(0 0 3px ${col})` : '';
    innerValEl.setAttribute('fill', active ? col : '#333');
    innerValEl.textContent = active ? (innerFmt || (v => Math.round(v)))(innerCur) : '--';
    innerRaf = Math.abs(innerCur - innerTgt) > MG_LERP_STOP ? requestAnimationFrame(lerpInner) : 0;
  }

  return {
    updateOuter(v) { outerTgt = v; if (!outerRaf) outerRaf = requestAnimationFrame(lerpOuter); },
    updateInner(v) { innerTgt = v; if (!innerRaf) innerRaf = requestAnimationFrame(lerpInner); },
    setColorMode(mode) { colorMode = mode; },
  };
}

// --- OIL lamp colors ---
const OIL_COLORS = { green: '#69f0ae', yellow: '#fdd835', orange: '#ff9800', red: '#f44336' };

// --- Module state ---
let gGear, gMapMaf;
let oilLampEl;

// createIndicators: 右パネルに2連メーターを構築
export function createIndicators(panelEl) {
  const svg = document.getElementById('rg');

  // Gauge 1 (上): Gear Ratio + TCC Lock %
  gGear = buildMiniGauge(svg, {
    cx: GAUGE_CX, cy: GAUGE_Y1, r: GAUGE_R,
    outerMin: 0.7, outerMax: 3.0, outerInvert: true,
    innerMin: 0, innerMax: 100,
    outerUnit: ':1', innerUnit: 'LOCK %',
    outerFmt: v => v.toFixed(2),
    innerFmt: v => v.toFixed(1),
  });

  // Gauge 2 (下): MAP + MAF
  gMapMaf = buildMiniGauge(svg, {
    cx: GAUGE_CX, cy: GAUGE_Y2, r: GAUGE_R,
    outerMin: 0, outerMax: 101,
    innerMin: 0, innerMax: 70,
    outerUnit: 'kPa', innerUnit: 'g/s',
    outerFmt: v => Math.round(v),
    innerFmt: v => v.toFixed(1),
  });

  // OIL CHANGE ランプ（下メーター下）
  const oilY = GAUGE_Y2 + GAUGE_R - 6;
  oilLampEl = mgSvgEl(svg, 'text', {
    x: GAUGE_CX, y: oilY,
    'font-family': "'Share Tech Mono', monospace",
    'font-size': 16, 'font-weight': 700,
    fill: '#69f0ae', 'text-anchor': 'middle',
  });
  oilLampEl.textContent = 'OIL CHANGE';

  return {};
}

// updateIndicators: APIデータで2連メーターを更新
export function updateIndicators(dom, d, conf) {
  const range = d.at_range_str || '?';
  const hold = d.hold || false;
  const gear = d.gear || 0;
  const gearRatio = d.gear_ratio || 0;
  const lockPct = d.tcc_lock_pct || 0;
  const mapKpa = d.intake_map || 0;
  const mafGs = d.maf_airflow || 0;

  // Gauge 1: Gear Ratio + TCC Lock %
  if (range === 'P' || range === 'N') {
    gGear.setColorMode('default');
    gGear.updateOuter(0);
    gGear.updateInner(0);
  } else if (range === 'R') {
    gGear.setColorMode('fixed:#ff9800');
    gGear.updateOuter(gearRatio);
    gGear.updateInner(lockPct);
  } else if (hold) {
    gGear.setColorMode('fixed:#fdd835');
    gGear.updateOuter(gearRatio);
    gGear.updateInner(lockPct);
  } else {
    gGear.setColorMode('blue-green');
    gGear.updateOuter(gearRatio);
    gGear.updateInner(lockPct);
  }

  // Gauge 2: MAP + MAF
  gMapMaf.updateOuter(mapKpa);
  gMapMaf.updateInner(mafGs);

  // OIL lamp
  const oilAlert = d.oil_alert || 'green';
  const oilCol = OIL_COLORS[oilAlert] || OIL_COLORS.green;
  oilLampEl.setAttribute('fill', oilCol);
  oilLampEl.style.filter = oilAlert !== 'green' ? `drop-shadow(0 0 6px ${oilCol})` : '';
}
