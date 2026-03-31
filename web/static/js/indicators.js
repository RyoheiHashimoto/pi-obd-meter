// ============================================================
// Indicators — 右パネル MAP メーター + 4行インジケーター
// ============================================================

const DEG = Math.PI / 180;
const MG_ARC_START = -135;
const MG_ARC_END = 135;
const MG_ARC_SWEEP = 270;
const MG_LERP = 0.35;
const MG_LERP_TH = 0.05;
const MG_LERP_STOP = 0.01;
const HUE_MAX = 210;

// MAP メーター配置
const MAP_CX = 110;
const MAP_CY = 155;
const MAP_R = 125;
const ARC_W = 10;

// インジケーター配置
const IND_X_ICON = 2;
const IND_X_VAL = 110;  // MAP_CX に合わせる
const IND_X_UNIT = 200;
const IND_Y_START = 305;
const IND_SPACING = 49;

// --- SVG helpers ---
function polar(cx, cy, r, deg) {
  const rad = deg * DEG;
  return [cx + r * Math.sin(rad), cy - r * Math.cos(rad)];
}

function arcPath(cx, cy, r, s, e) {
  if (Math.abs(e - s) < 0.3) return '';
  const [x1, y1] = polar(cx, cy, r, s);
  const [x2, y2] = polar(cx, cy, r, e);
  const lg = Math.abs(e - s) > 180 ? 1 : 0;
  return `M${x1.toFixed(1)},${y1.toFixed(1)}A${r},${r},0,${lg},1,${x2.toFixed(1)},${y2.toFixed(1)}`;
}

function svgEl(parent, tag, attrs) {
  const e = document.createElementNS('http://www.w3.org/2000/svg', tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  parent.appendChild(e);
  return e;
}

// --- アイコンパス (24x24 viewBox) ---
const ICON_LEAF = 'M0 -12C-5 -4 -7 2 -7 7c0 3 3 6 7 6s7-3 7-6c0-5-2-11-7-19z';
const ICON_THERMO = 'M12 2C10.34 2 9 3.34 9 5v8.59c-1.22.73-2 2.05-2 3.41 0 2.76 2.24 5 5 5s5-2.24 5-5c0-1.36-.78-2.68-2-3.41V5c0-1.66-1.34-3-3-3zm0 2c.55 0 1 .45 1 1v9.13l.5.29C14.46 15 15 15.96 15 17c0 1.65-1.35 3-3 3s-3-1.35-3-3c0-1.04.54-2 1.5-2.58l.5-.29V5c0-.55.45-1 1-1z';
const ICON_ROAD = 'M11 2h2v4h-2zm0 6h2v4h-2zm0 6h2v4h-2zM2 2l4 20h2L5 2zm20 0h-2L16 22h2z';
const ICON_OIL = 'M12 2C12 2 6 10 6 15a6 6 0 0 0 12 0c0-5-6-13-6-13zm0 17a3 3 0 0 1-3-3c0-.5.1-1 .3-1.5.2-.4.8-.3.9.2.1.3.1.6.1.9a1.8 1.8 0 0 0 1.8 1.8c.4 0 .7-.3.6-.7-.3-1.5-1.2-2.8-2.2-3.9-.3-.3 0-.8.4-.6C13.3 12.5 15 14.5 15 16a3 3 0 0 1-3 3z';

// --- Module state ---
let mapArcEl, mapValEl, mapUnitEl, mapNeedleEl, vacLabelEl;
let mapCur = 0, mapTgt = 0, mapRaf = 0;

let ecoValEl, ecoIconEls;
let tempValEl, tempIconEl;
let tripValEl, tripIconEl;
let oilValEl, oilIconEl, oilLabelEl;

// 閾値（config から設定可能）
let coolantColdMax = 60;
let coolantNormalMax = 100;
let coolantWarningMax = 104;
let ecoGradientMax = 15;

export function setCoolantThresholds(cold, normal, warning) {
  coolantColdMax = cold;
  coolantNormalMax = normal;
  coolantWarningMax = warning;
}

export function setEcoGradientMax(max) {
  ecoGradientMax = max;
}

// --- MAP meter LERP ---
// バキューム計: mapCur/mapTgt は bar 値 (-1.0 〜 0)
const VAC_MIN = -1.0;
const VAC_MAX = 0;

function lerpMap() {
  const delta = mapTgt - mapCur;
  mapCur = Math.abs(delta) > MG_LERP_TH * 0.01 ? mapCur + delta * MG_LERP : mapTgt;
  // -1.0=左端(0%), 0=右端(100%)
  const pct = Math.max(0, Math.min(100, (mapCur - VAC_MIN) / (VAC_MAX - VAC_MIN) * 100));
  const angle = MG_ARC_START + (pct / 100) * MG_ARC_SWEEP;
  mapArcEl.setAttribute('d', pct > 0.5 ? arcPath(MAP_CX, MAP_CY, MAP_R, MG_ARC_START, angle) : '');
  mapNeedleEl.style.transform = `rotate(${angle - MG_ARC_START}deg)`;
  const active = mapCur < 0.01;
  // 色: 0 bar(大気圧/全開)=赤, -1 bar(深い負圧)=青
  const hue = (1 - pct / 100) * HUE_MAX;
  const col = active ? (hue < 5 ? '#f44336' : `hsl(${hue}, 100%, 55%)`) : '#333';
  mapArcEl.setAttribute('stroke', col);
  mapArcEl.style.filter = active ? `drop-shadow(0 0 6px ${col})` : '';
  mapNeedleEl.setAttribute('stroke', active ? col : '#78909c');
  mapNeedleEl.style.filter = active ? `drop-shadow(0 0 6px ${col})` : '';
  // VACUUM label: 深い負圧=暗い, 浅い負圧=明るく色付きに
  if (vacLabelEl) {
    const lum = 10 + (pct / 100) * 45; // 10%(暗い) → 55%(明るい)
    const sat = Math.min(100, pct * 1.5); // 0%(グレー) → 100%(鮮やか)
    const vacCol = `hsl(${hue}, ${sat}%, ${lum}%)`;
    vacLabelEl.setAttribute('fill', vacCol);
    vacLabelEl.style.filter = active ? `drop-shadow(0 0 ${3 + pct / 100 * 5}px ${vacCol})` : '';
  }
  mapValEl.setAttribute('fill', active ? col : '#333');
  mapValEl.textContent = active ? mapCur.toFixed(2) : '--';
  mapRaf = Math.abs(mapCur - mapTgt) > MG_LERP_STOP * 0.01 ? requestAnimationFrame(lerpMap) : 0;
}

// --- アイコン生成 ---
function createIconPath(svg, x, y, pathD, size) {
  const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
  g.setAttribute('transform', `translate(${x - size/2}, ${y - size/2}) scale(${size/24})`);
  const p = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  p.setAttribute('d', pathD);
  p.setAttribute('fill', '#444');
  g.appendChild(p);
  svg.appendChild(g);
  return p;
}

function createLeafIcon(svg, x, y, size) {
  const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
  g.setAttribute('transform', `translate(${x}, ${y}) rotate(60) scale(${size/20})`);
  const outline = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  outline.setAttribute('d', ICON_LEAF);
  outline.setAttribute('fill', 'none');
  outline.setAttribute('stroke', '#444');
  outline.setAttribute('stroke-width', '1.5');
  g.appendChild(outline);
  const vein = document.createElementNS('http://www.w3.org/2000/svg', 'line');
  vein.setAttribute('x1', '0'); vein.setAttribute('y1', '-8');
  vein.setAttribute('x2', '0'); vein.setAttribute('y2', '10');
  vein.setAttribute('stroke', '#444');
  vein.setAttribute('stroke-width', '1.5');
  vein.setAttribute('stroke-dasharray', '3 2');
  g.appendChild(vein);
  const stem = document.createElementNS('http://www.w3.org/2000/svg', 'line');
  stem.setAttribute('x1', '0'); stem.setAttribute('y1', '13');
  stem.setAttribute('x2', '0'); stem.setAttribute('y2', '18');
  stem.setAttribute('stroke', '#444');
  stem.setAttribute('stroke-width', '1.5');
  g.appendChild(stem);
  svg.appendChild(g);
  return { outline, vein, stem };
}


// createIndicators: 右パネル構築
export function createIndicators(panelEl) {
  const svg = document.getElementById('rg');

  // === バキューム計 (-1.0 〜 0 bar) ===
  const VAC_MJ = 5;    // 主目盛り数 (-1.0, -0.8, -0.6, -0.4, -0.2, 0)
  const VAC_MN = 4;    // 主目盛り間の副目盛り数
  const VAC_TOTAL = VAC_MJ * VAC_MN;

  // Track
  svgEl(svg, 'path', { d: arcPath(MAP_CX, MAP_CY, MAP_R, MG_ARC_START, MG_ARC_END), fill: 'none', stroke: '#181820', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });

  // Ticks
  for (let i = 0; i <= VAC_TOTAL; i++) {
    const a = MG_ARC_START + (i / VAC_TOTAL) * MG_ARC_SWEEP;
    const isMj = i % VAC_MN === 0;
    const ri = isMj ? MAP_R - 14 : MAP_R - 11;
    const ro = MAP_R + 3;
    const [x1, y1] = polar(MAP_CX, MAP_CY, ri, a);
    const [x2, y2] = polar(MAP_CX, MAP_CY, ro, a);
    svgEl(svg, 'line', { x1, y1, x2, y2, stroke: isMj ? '#aaa' : '#444', 'stroke-width': isMj ? 4 : 2 });
    if (isMj) {
      const v = VAC_MIN + (i / VAC_TOTAL) * (VAC_MAX - VAC_MIN);
      const [lx, ly] = polar(MAP_CX, MAP_CY, MAP_R - 32, a);
      const t = svgEl(svg, 'text', { x: lx, y: ly, class: 'tk-lbl', fill: '#fff', 'font-size': 22 });
      t.textContent = v === 0 ? '0' : v.toFixed(1).replace('-0.', '-.');
    }
  }

  // Active arc
  mapArcEl = svgEl(svg, 'path', { d: '', fill: 'none', stroke: '#555', 'stroke-width': ARC_W, 'stroke-linecap': 'round' });

  // VACUUM label (負圧が浅いほど明るく赤く) — 針の下に配置
  vacLabelEl = svgEl(svg, 'text', { x: MAP_CX, y: MAP_CY - 30, class: 'g-unit', fill: '#222', 'font-size': 24, 'text-anchor': 'middle' });
  vacLabelEl.textContent = 'VACUUM';

  // Needle (VACUUM ラベルの上)
  const [mnx0, mny0] = polar(MAP_CX, MAP_CY, MAP_R - 18, MG_ARC_START);
  const [mtx0, mty0] = polar(MAP_CX, MAP_CY, -10, MG_ARC_START);
  mapNeedleEl = svgEl(svg, 'line', { x1: mtx0, y1: mty0, x2: mnx0, y2: mny0, stroke: '#78909c', 'stroke-width': 6, 'stroke-linecap': 'round', 'transform-origin': `${MAP_CX}px ${MAP_CY}px` });
  mapNeedleEl.style.transition = 'transform 0.15s ease-out';

  // Center dot
  svgEl(svg, 'circle', { cx: MAP_CX, cy: MAP_CY, r: 5, fill: '#1a1a22', stroke: '#444', 'stroke-width': 2 });

  // Value
  mapValEl = svgEl(svg, 'text', { x: MAP_CX, y: MAP_CY + MAP_R * 0.38, class: 'g-num', fill: '#333', 'font-size': 48, 'text-anchor': 'middle' });
  mapValEl.textContent = '--';
  // Unit
  mapUnitEl = svgEl(svg, 'text', { x: MAP_CX, y: MAP_CY + MAP_R * 0.38 + 44, class: 'g-unit', fill: '#fff', 'font-size': 24, 'text-anchor': 'middle' });
  mapUnitEl.textContent = 'Bar';

  // === 4行インジケーター ===
  // 区切り線
  svgEl(svg, 'line', { x1: 10, y1: IND_Y_START - 16, x2: 210, y2: IND_Y_START - 16, stroke: '#222', 'stroke-width': 1 });

  // Row 0: ECO
  const ecoY = IND_Y_START;
  const leafIcons = createLeafIcon(svg, IND_X_ICON + 16, ecoY - 8, 30);
  ecoIconEls = leafIcons;
  ecoValEl = svgEl(svg, 'text', { x: IND_X_VAL, y: ecoY + 6, class: 'g-num', fill: '#333', 'font-size': 40, 'text-anchor': 'middle' });
  ecoValEl.textContent = '--';
  svgEl(svg, 'text', { x: IND_X_UNIT, y: ecoY + 4, class: 'g-unit', fill: '#fff', 'font-size': 24, 'text-anchor': 'end' }).textContent = 'km/L';

  // Row 1: TEMP
  const tempY = IND_Y_START + IND_SPACING;
  tempIconEl = createIconPath(svg, IND_X_ICON + 10, tempY - 8, ICON_THERMO, 40);
  tempValEl = svgEl(svg, 'text', { x: IND_X_VAL, y: tempY + 6, class: 'g-num', fill: '#333', 'font-size': 40, 'text-anchor': 'middle' });
  tempValEl.textContent = '--';
  svgEl(svg, 'text', { x: IND_X_UNIT, y: tempY + 4, class: 'g-unit', fill: '#fff', 'font-size': 24, 'text-anchor': 'end' }).textContent = '°C';

  // Row 2: TRIP
  const tripY = IND_Y_START + IND_SPACING * 2;
  tripIconEl = createIconPath(svg, IND_X_ICON + 10, tripY - 8, ICON_ROAD, 40);
  tripValEl = svgEl(svg, 'text', { x: IND_X_VAL, y: tripY + 6, class: 'g-num', fill: '#333', 'font-size': 40, 'text-anchor': 'middle' });
  tripValEl.textContent = '0';
  svgEl(svg, 'text', { x: IND_X_UNIT, y: tripY + 4, class: 'g-unit', fill: '#fff', 'font-size': 24, 'text-anchor': 'end' }).textContent = 'km';

  // Row 3: OIL
  const oilY = IND_Y_START + IND_SPACING * 3;
  oilIconEl = createIconPath(svg, IND_X_ICON + 10, oilY - 8, ICON_OIL, 40);
  oilValEl = svgEl(svg, 'text', { x: IND_X_VAL, y: oilY + 6, class: 'g-num', fill: '#333', 'font-size': 40, 'text-anchor': 'middle' });
  oilValEl.textContent = '--';
  oilLabelEl = svgEl(svg, 'text', { x: IND_X_UNIT, y: oilY + 4, class: 'g-unit', fill: '#fff', 'font-size': 24, 'text-anchor': 'end' });
  oilLabelEl.textContent = 'km';

  return {};
}

// OIL lamp colors
const OIL_COLORS = { green: '#69f0ae', yellow: '#fdd835', orange: '#ff9800', red: '#f44336' };

// updateIndicators: APIデータで更新
export function updateIndicators(dom, d, conf) {
  // バキューム (kPa → bar)
  const mapKpa = d.intake_map || 0;
  mapTgt = (mapKpa - 101.3) / 100;
  if (!mapRaf) mapRaf = requestAnimationFrame(lerpMap);

  // ECO (平均燃費の数値、色は瞬間燃費)
  const avgEco = Math.min(d.avg_fuel_economy || 0, 99.9);
  const instantEco = d.fuel_economy || 0;
  ecoValEl.textContent = avgEco > 0.1 ? avgEco.toFixed(1) : '--';
  let ecoCol;
  if (instantEco < 0) ecoCol = '#29b6f6';          // エンブレ
  else if (instantEco < 0.1) ecoCol = '#ddd';      // 停車
  else {
    const hue = Math.min(instantEco / ecoGradientMax, 1) * 153;
    ecoCol = `hsl(${hue}, 100%, 55%)`;
  }
  ecoValEl.setAttribute('fill', ecoCol);
  ecoIconEls.outline.setAttribute('stroke', ecoCol);
  ecoIconEls.vein.setAttribute('stroke', ecoCol);
  ecoIconEls.stem.setAttribute('stroke', ecoCol);

  // TEMP
  const coolant = d.coolant_temp || 0;
  if (coolant > 0) {
    tempValEl.textContent = Math.round(coolant);
    const col = coolant < coolantColdMax ? '#29b6f6' : coolant <= coolantNormalMax ? '#69f0ae' : coolant <= coolantWarningMax ? '#ff9800' : '#f44336';
    tempValEl.setAttribute('fill', col);
    tempIconEl.setAttribute('fill', col);
  } else {
    tempValEl.textContent = '--';
    tempValEl.setAttribute('fill', '#333');
    tempIconEl.setAttribute('fill', '#333');
  }

  // TRIP
  const tripKm = d.trip_km || 0;
  tripValEl.textContent = tripKm >= 0.1 ? tripKm.toFixed(1) : '0';
  const tripCol = tripKm < 350 ? '#69f0ae' : tripKm < 400 ? '#fdd835' : tripKm < 450 ? '#ff9800' : '#f44336';
  tripValEl.setAttribute('fill', tripCol);
  tripIconEl.setAttribute('fill', tripCol);

  // OIL
  const oilAlert = d.oil_alert || 'green';
  const oilRemaining = d.oil_remaining_km;
  const oilCol = OIL_COLORS[oilAlert] || OIL_COLORS.green;
  if (oilRemaining != null) {
    oilValEl.textContent = Math.round(oilRemaining).toLocaleString();
  } else {
    oilValEl.textContent = '--';
  }
  oilValEl.setAttribute('fill', oilCol);
  oilIconEl.setAttribute('fill', oilCol);
}
