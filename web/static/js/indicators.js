// ============================================================
// Indicators — 右パネル 3連二重アークメーター + 警告灯
// ============================================================

const DEG = Math.PI / 180;
const MG_ARC_START = -135;
const MG_ARC_END = 135;
const MG_ARC_SWEEP = 270;
const MG_LERP = 0.7;
const MG_LERP_TH = 0.05;
const MG_LERP_STOP = 0.01;
const HUE_MAX = 210;

const GAUGE_CX = 144;
const GAUGE_R = 80;
const GAUGE_SPACING = 155;
const GAUGE_Y_START = 98;

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

// --- ZJ-VE Performance Curve ---
const VE_TABLE_RPM =  [1000, 1500, 2000, 2500, 3000, 3500, 4000, 4500, 5000, 5500, 6000, 6500];
const VE_TABLE_TQ_NM = [90,  105,  115,  120,  123,  124,  122,  118,  113,  108,  103,   95];

function interpolateTorque(rpm) {
  if (rpm <= VE_TABLE_RPM[0]) return VE_TABLE_TQ_NM[0];
  if (rpm >= VE_TABLE_RPM[VE_TABLE_RPM.length - 1]) return VE_TABLE_TQ_NM[VE_TABLE_RPM.length - 1];
  for (let i = 0; i < VE_TABLE_RPM.length - 1; i++) {
    if (rpm <= VE_TABLE_RPM[i + 1]) {
      const t = (rpm - VE_TABLE_RPM[i]) / (VE_TABLE_RPM[i + 1] - VE_TABLE_RPM[i]);
      return VE_TABLE_TQ_NM[i] + t * (VE_TABLE_TQ_NM[i + 1] - VE_TABLE_TQ_NM[i]);
    }
  }
  return VE_TABLE_TQ_NM[VE_TABLE_TQ_NM.length - 1];
}

function estimatePSTorque(mapKpa, rpm, atmKpa) {
  if (rpm < 100) return { ps: 0, tq: 0 };
  const loadFrac = mapKpa / (atmKpa || 101.3);
  const wotTorque = interpolateTorque(rpm);
  const torque = wotTorque * loadFrac;
  const ps = torque * rpm / 7162;
  return { ps: Math.max(0, ps), tq: Math.max(0, torque) };
}

// --- Mini Gauge Builder ---
function buildMiniGauge(svg, cfg) {
  const { cx, cy, r, outerMin, outerMax, innerMin, innerMax, outerUnit, innerUnit,
          outerColor, innerColor, outerFmt, innerFmt, outerInvert, innerInvert } = cfg;
  const oMin = outerMin || 0;
  const iMin = innerMin || 0;
  const ARC_W = 8;
  const outerR = r;
  const innerR = r - ARC_W - 4;

  mgSvgEl(svg, 'path', { d: mgArcPath(cx, cy, outerR, MG_ARC_START, MG_ARC_END), fill: 'none', stroke: '#181820', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });
  mgSvgEl(svg, 'path', { d: mgArcPath(cx, cy, innerR, MG_ARC_START, MG_ARC_END), fill: 'none', stroke: '#0e0e14', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });

  const outerArc = mgSvgEl(svg, 'path', { d: '', fill: 'none', stroke: outerColor || '#555', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });
  const innerArc = mgSvgEl(svg, 'path', { d: '', fill: 'none', stroke: innerColor || '#555', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });

  const outerValEl = mgSvgEl(svg, 'text', { x: cx, y: cy - 22, class: 'g-num', fill: '#333', 'font-size': 22, 'text-anchor': 'middle' });
  outerValEl.textContent = '--';
  const outerUnitEl = mgSvgEl(svg, 'text', { x: cx, y: cy - 4, class: 'g-unit', fill: '#fff', 'font-size': 14, 'text-anchor': 'middle' });
  outerUnitEl.textContent = outerUnit || '';

  const innerValEl = mgSvgEl(svg, 'text', { x: cx, y: cy + 36, class: 'g-num', fill: '#333', 'font-size': 22, 'text-anchor': 'middle' });
  innerValEl.textContent = '--';
  const innerUnitEl = mgSvgEl(svg, 'text', { x: cx, y: cy + 56, class: 'g-unit', fill: '#fff', 'font-size': 14, 'text-anchor': 'middle' });
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

// --- A/F Gauge (center-zero style) ---
function buildAFGauge(svg, cfg) {
  const { cx, cy, r } = cfg;
  const ARC_W = 8;
  const outerR = r;
  const innerR = r - ARC_W - 4;

  mgSvgEl(svg, 'path', { d: mgArcPath(cx, cy, outerR, MG_ARC_START, MG_ARC_END), fill: 'none', stroke: '#181820', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });
  mgSvgEl(svg, 'path', { d: mgArcPath(cx, cy, innerR, MG_ARC_START, MG_ARC_END), fill: 'none', stroke: '#0e0e14', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });

  const mapArc = mgSvgEl(svg, 'path', { d: '', fill: 'none', stroke: '#555', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });
  const afArc = mgSvgEl(svg, 'path', { d: '', fill: 'none', stroke: '#555', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });

  const mapValEl = mgSvgEl(svg, 'text', { x: cx, y: cy - 22, class: 'g-num', fill: '#333', 'font-size': 22, 'text-anchor': 'middle' });
  mapValEl.textContent = '--';
  const mapUnitEl = mgSvgEl(svg, 'text', { x: cx, y: cy - 4, class: 'g-unit', fill: '#fff', 'font-size': 14, 'text-anchor': 'middle' });
  mapUnitEl.textContent = 'kPa';

  const afValEl = mgSvgEl(svg, 'text', { x: cx, y: cy + 36, class: 'g-num', fill: '#333', 'font-size': 22, 'text-anchor': 'middle' });
  afValEl.textContent = '--';
  const afUnitEl = mgSvgEl(svg, 'text', { x: cx, y: cy + 56, class: 'g-unit', fill: '#fff', 'font-size': 14, 'text-anchor': 'middle' });
  afUnitEl.textContent = 'A/F';

  let mapCur = 0, mapTgt = 0, mapRaf = 0;
  function lerpMap() {
    const delta = mapTgt - mapCur;
    mapCur = Math.abs(delta) > MG_LERP_TH ? mapCur + delta * MG_LERP : mapTgt;
    const pct = Math.max(0, Math.min(100, mapCur / 101 * 100));
    const angle = MG_ARC_START + (pct / 100) * MG_ARC_SWEEP;
    mapArc.setAttribute('d', pct > 0.5 ? mgArcPath(cx, cy, outerR, MG_ARC_START, angle) : '');
    const hue = HUE_MAX - (pct / 100) * HUE_MAX;
    const active = mapCur > 5;
    const col = active ? `hsl(${hue}, 100%, 55%)` : '#333';
    mapArc.setAttribute('stroke', col);
    mapValEl.setAttribute('fill', active ? col : '#333');
    mapValEl.textContent = active ? Math.round(mapCur) : '--';
    mapRaf = Math.abs(mapCur - mapTgt) > MG_LERP_STOP ? requestAnimationFrame(lerpMap) : 0;
  }

  let afCur = 14.7, afTgt = 14.7, afRaf = 0;
  const AF_MIN = 10, AF_MAX = 20;
  function lerpAF() {
    const delta = afTgt - afCur;
    afCur = Math.abs(delta) > 0.01 ? afCur + delta * 0.5 : afTgt;
    const pct = Math.max(0, Math.min(100, (afCur - AF_MIN) / (AF_MAX - AF_MIN) * 100));
    const angle = MG_ARC_START + (pct / 100) * MG_ARC_SWEEP;
    const stoichPct = (14.7 - AF_MIN) / (AF_MAX - AF_MIN) * 100;
    const stoichAng = MG_ARC_START + (stoichPct / 100) * MG_ARC_SWEEP;
    if (Math.abs(angle - stoichAng) > 1) {
      const s = Math.min(angle, stoichAng);
      const e = Math.max(angle, stoichAng);
      afArc.setAttribute('d', mgArcPath(cx, cy, innerR, s, e));
    } else {
      afArc.setAttribute('d', '');
    }
    const isRich = afCur < 14.5;
    const isLean = afCur > 14.9;
    const col = isRich ? '#ff6e40' : isLean ? '#29b6f6' : '#4caf50';
    afArc.setAttribute('stroke', col);
    afValEl.setAttribute('fill', col);
    afValEl.textContent = afCur.toFixed(1);
    afRaf = Math.abs(afCur - afTgt) > 0.005 ? requestAnimationFrame(lerpAF) : 0;
  }

  return {
    updateMAP(v) { mapTgt = v; if (!mapRaf) mapRaf = requestAnimationFrame(lerpMap); },
    updateAF(v) { afTgt = v; if (!afRaf) afRaf = requestAnimationFrame(lerpAF); },
  };
}

// --- Warning Lamps ---
const LAMP_COLORS = { yellow: '#fdd835', orange: '#ff9800', red: '#f44336' };
function setLampColor(el, alert) {
  const col = LAMP_COLORS[alert];
  el.setAttribute('fill', col || '#333');
}

// All lamps in display order
const LAMP_DEFS = [
  { lamp: 'LINK',    system: true },
  { lamp: 'NETWORK', system: true },
  { lamp: 'CHECK',   system: true },
  { lamp: '24M' },
  { lamp: '12M' },
  { lamp: 'OIL' },
  { lamp: 'AC.F' },
  { lamp: 'TIRE' },
  { lamp: 'WIPER' },
  { lamp: 'FILTER' },
  { lamp: 'BRK.F' },
  { lamp: 'LLC' },
  { lamp: 'ATF' },
  { lamp: 'BATT' },
  { lamp: 'PLUG' },
  { lamp: 'COIL' },
];

// --- Module state ---
let gGear, gMap, gPower;
let wlamps = {};

// createIndicators: 右パネルに3連メーター + 警告灯を構築
export function createIndicators(panelEl) {
  const svg = document.getElementById('rg');

  // Gauge 1: Gear Ratio + TCC Lock %
  gGear = buildMiniGauge(svg, {
    cx: GAUGE_CX, cy: GAUGE_Y_START, r: GAUGE_R,
    outerMin: 0.7, outerMax: 3.0, outerInvert: true,
    innerMin: 0, innerMax: 100,
    outerUnit: ':1', innerUnit: 'LOCK %',
    outerFmt: v => v.toFixed(2),
    innerFmt: v => v.toFixed(1),
  });

  // Gauge 2: MAP + A/F
  gMap = buildAFGauge(svg, {
    cx: GAUGE_CX, cy: GAUGE_Y_START + GAUGE_SPACING, r: GAUGE_R,
  });

  // Gauge 3: PS + Torque
  gPower = buildMiniGauge(svg, {
    cx: GAUGE_CX, cy: GAUGE_Y_START + GAUGE_SPACING * 2, r: GAUGE_R,
    outerMax: 91, innerMax: 12.6,
    outerUnit: 'PS', innerUnit: 'kgf\u00B7m',
    outerFmt: v => Math.round(v),
    innerFmt: v => v.toFixed(1),
  });

  // Warning lamps (left column between gauges and main gauge)
  const WLAMP_GAP_RIGHT = GAUGE_CX - GAUGE_R - 12;
  const WLAMP_X = WLAMP_GAP_RIGHT / 2 - 12;
  const WLAMP_LINE_H = 20;
  const wlampTotalH = LAMP_DEFS.length * WLAMP_LINE_H;
  const wlampStartY = (480 - wlampTotalH) / 2 + 54;

  LAMP_DEFS.forEach((def, i) => {
    const el = mgSvgEl(svg, 'text', {
      x: WLAMP_X, y: wlampStartY + i * WLAMP_LINE_H,
      'font-family': "'Share Tech Mono', monospace",
      'font-size': 14, 'font-weight': 700,
      fill: '#333', 'text-anchor': 'middle',
    });
    el.textContent = def.lamp;
    wlamps[def.lamp] = el;
  });

  return {};
}

// updateIndicators: APIデータで3連メーター + 警告灯を更新
export function updateIndicators(dom, d, conf) {
  const range = d.at_range_str || '?';
  const hold = d.hold || false;
  const gear = d.gear || 0;
  const gearRatio = d.gear_ratio || 0;
  const lockPct = d.tcc_lock_pct || 0;
  const mapKpa = d.intake_map || 0;
  const rpm = d.rpm || 0;

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

  // Gauge 2: MAP + A/F
  gMap.updateMAP(mapKpa);
  // A/F estimation from MAP (until Go side provides real A/F)
  let af = 14.7;
  if (mapKpa > 5 && rpm > 100) {
    if (mapKpa < 25) af = 16 + (25 - mapKpa) / 15 * 2;      // decel/coast = lean
    else if (mapKpa > 80) af = 12.0 + (101 - mapKpa) / 21;   // WOT = rich
    else af = 14.5 + (mapKpa - 25) / 55 * 0.4;               // part throttle ≈ stoich
  }
  gMap.updateAF(af);

  // Gauge 3: PS + Torque
  const atmKpa = d.barometric_pressure || 101.3;
  const { ps, tq } = estimatePSTorque(mapKpa, rpm, atmKpa);
  gPower.updateOuter(ps);
  gPower.updateInner(tq / 9.807); // Nm → kgf·m

  // Warning lamps
  const wifiOk = d.wifi_connected !== false;
  const canOk = d.can_connected !== false && d.obd_connected !== false;
  setLampColor(wlamps['LINK'], canOk ? '' : 'red');
  setLampColor(wlamps['NETWORK'], wifiOk ? '' : 'red');

  // CHECK lamp: MIL or fuel trim ±20%
  let checkAlert = '';
  if (d.mil_on) checkAlert = 'red';
  else if (d.short_fuel_trim != null && Math.abs(d.short_fuel_trim) > 20) checkAlert = 'orange';
  setLampColor(wlamps['CHECK'], checkAlert);

  // Maintenance lamps from alerts
  // Reset all maintenance lamps first
  for (const def of LAMP_DEFS) {
    if (def.system) continue;
    setLampColor(wlamps[def.lamp], '');
  }
  // Apply alerts from API
  if (d.alerts) {
    for (const a of d.alerts) {
      const r = a.reminder;
      const lamp = r.lamp;
      if (lamp && wlamps[lamp]) {
        const severity = r.severity || 'soft';
        const pct = a.progress || 0;
        const overdue = pct >= 1.0;
        let color = '';
        if (severity === 'hard') {
          color = overdue ? 'red' : 'orange';
        } else {
          color = overdue ? 'orange' : 'yellow';
        }
        setLampColor(wlamps[lamp], color);
      }
    }
  }
}
