// ============================================================
// Indicators — 右パネル MAP メーター + 4行インジケーター
// ============================================================

import { addOffsetShadow } from './gauge.js';

// SVG filter glow 無効化 (fake bloom で代替)
function setFilter(_el, _v) { /* no-op */ }

// テキスト bloom (stroke で外縁発光)
function bloomText(textEl, strokeWidth = 3, opacity = 0.4) {
  const bloom = textEl.cloneNode(true);
  bloom.removeAttribute('id');
  const c = textEl.getAttribute('fill') || '#fff';
  bloom.setAttribute('fill', c);
  bloom.setAttribute('stroke', c);
  bloom.setAttribute('stroke-width', strokeWidth);
  bloom.setAttribute('stroke-linejoin', 'round');
  bloom.setAttribute('opacity', opacity);
  textEl.parentNode.insertBefore(bloom, textEl);
  textEl._bloom = bloom;
  const origSet = textEl.setAttribute.bind(textEl);
  textEl.setAttribute = (k, v) => {
    origSet(k, v);
    if (k === 'fill') { bloom.setAttribute('fill', v); bloom.setAttribute('stroke', v); }
  };
  new MutationObserver(() => { bloom.textContent = textEl.textContent; })
    .observe(textEl, { childList: true, characterData: true, subtree: true });
  return textEl;
}

// 全 bloom clone 方式 (<use> は stroke-width を継承して幅広にできない)
// 針: transform transition で残像、arc: d 即時同期 (lag なし、幅広静的グロー)
function createBloom(parent, tag, attrs, bloomExtra = 10, bloomOpacity = 0.3) {
  const sw = parseFloat(attrs['stroke-width'] || '1');
  const mkEl = (t, a) => {
    const e = document.createElementNS('http://www.w3.org/2000/svg', t);
    for (const [k, v] of Object.entries(a)) e.setAttribute(k, v);
    parent.appendChild(e);
    return e;
  };
  const bloom = mkEl(tag, { ...attrs, 'stroke-width': sw + bloomExtra, opacity: bloomOpacity });
  const main = mkEl(tag, attrs);
  main._bloom = bloom;
  if (attrs['transform-origin']) {
    bloom.style.transition = 'transform 0.6s cubic-bezier(0.18, 0.89, 0.32, 1.15)';
  }
  const origSet = main.setAttribute.bind(main);
  main.setAttribute = (k, v) => {
    origSet(k, v);
    if (k === 'd' || k === 'stroke' || k === 'fill') bloom.setAttribute(k, v);
  };
  return main;
}
function rotateWithBloom(el, t) {
  el.style.transform = t;
  if (el._bloom) el._bloom.style.transform = t;
}

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
const ICON_ROAD = 'M11 2h2v4h-2zm0 6h2v4h-2zm0 6h2v4h-2zM2 2l4 20h2L5 2zm20 0h-2L16 22h2z';
const ICON_OIL = 'M12 2C12 2 6 10 6 15a6 6 0 0 0 12 0c0-5-6-13-6-13zm0 17a3 3 0 0 1-3-3c0-.5.1-1 .3-1.5.2-.4.8-.3.9.2.1.3.1.6.1.9a1.8 1.8 0 0 0 1.8 1.8c.4 0 .7-.3.6-.7-.3-1.5-1.2-2.8-2.2-3.9-.3-.3 0-.8.4-.6C13.3 12.5 15 14.5 15 16a3 3 0 0 1-3 3z';
// 給油ポンプ (給油までの残距離用)
// 温度計 (ATF 油温)
const ICON_THERMO = 'M12 2a3 3 0 0 0-3 3v8.6a5 5 0 1 0 6 0V5a3 3 0 0 0-3-3zm0 2a1 1 0 0 1 1 1v9.4l.6.4a3 3 0 1 1-3.2 0l.6-.4V5a1 1 0 0 1 1-1zm-.5 3h1v7h-1z';

const ICON_FUELPUMP = 'M19.77 7.23l.01-.01-3.72-3.72L15 4.56l2.11 2.11c-.94.36-1.61 1.26-1.61 2.33 0 1.38 1.12 2.5 2.5 2.5.36 0 .69-.08 1-.21v7.21c0 .55-.45 1-1 1s-1-.45-1-1V14c0-1.1-.9-2-2-2h-1V5c0-1.1-.9-2-2-2H6c-1.1 0-2 .9-2 2v16h10v-7.5h1.5v5c0 1.38 1.12 2.5 2.5 2.5s2.5-1.12 2.5-2.5V9c0-.69-.28-1.32-.73-1.77zM12 10H6V5h6v5zm6 0c-.55 0-1-.45-1-1s.45-1 1-1 1 .45 1 1-.45 1-1 1z';
// 立体トラック描画（SVG radialGradient で内暗→中明→外暗）
let trackGradCount = 100;
function createGradientTrack(svg, cx, cy, r, strokeW, startDeg, endDeg, innerCol, midCol, outerCol) {
  const id = `trkGrad${trackGradCount++}`;
  let defs = svg.querySelector('defs');
  if (!defs) { defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs'); svg.insertBefore(defs, svg.firstChild); }
  const grad = document.createElementNS('http://www.w3.org/2000/svg', 'radialGradient');
  grad.setAttribute('id', id);
  grad.setAttribute('cx', cx); grad.setAttribute('cy', cy);
  grad.setAttribute('r', r + strokeW / 2);
  grad.setAttribute('gradientUnits', 'userSpaceOnUse');
  const innerR = (r - strokeW / 2) / (r + strokeW / 2);
  [
    [innerR.toFixed(3), innerCol],
    [((innerR + 1) / 2).toFixed(3), midCol],
    ['1', outerCol],
  ].forEach(([o, c]) => {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'stop');
    s.setAttribute('offset', o); s.setAttribute('stop-color', c);
    grad.appendChild(s);
  });
  defs.appendChild(grad);
  return svgEl(svg, 'path', { d: arcPath(cx, cy, r, startDeg, endDeg), fill: 'none', stroke: `url(#${id})`, 'stroke-width': strokeW, 'stroke-linecap': 'round' });
}

// --- Module state ---
let mapArcEl, mapValEl, mapUnitEl, mapNeedleEl;
let mapCur = 0, mapTgt = 0, mapRaf = 0;
let instValEl, instUnitEl;
let instArcEl, instArcCur = 0, instArcTgt = 0, instArcCol = '', instArcRaf = 0;

let ecoValEl, ecoIconEls;
let rngValEl, rngIconEl;
let tripValEl, tripIconEl;
let oilValEl, oilIconEl, oilLabelEl;
let atfValEl, atfIconEl, atfLabelEl;  // 2画面目(#178)で再利用する

// 閾値（config から設定可能、TEMP 削除後も coolant 関連は保持してダミーで吸収）
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
  rotateWithBloom(mapNeedleEl, `rotate(${angle - MG_ARC_START}deg)`);
  const active = mapCur < 0.01;
  // 色: 0 bar(大気圧/全開)=赤, -1 bar(深い負圧)=青
  const hue = (1 - pct / 100) * HUE_MAX;
  const col = active ? (hue < 5 ? '#f44336' : `hsl(${hue}, 100%, 55%)`) : '#333';
  mapArcEl.setAttribute('stroke', col);
  setFilter(mapArcEl, active ? 'url(#glow-strong)' : '');
  mapNeedleEl.setAttribute('stroke', active ? col : '#78909c');
  setFilter(mapNeedleEl, active ? 'url(#glow-strong)' : '');
  mapValEl.setAttribute('fill', active ? col : '#333');
  mapValEl.textContent = active ? mapCur.toFixed(2) : '--';
  mapRaf = Math.abs(mapCur - mapTgt) > MG_LERP_STOP * 0.01 ? requestAnimationFrame(lerpMap) : 0;
}

// --- 瞬間燃費 (バキューム計の中、針の付け根の上) ---
//
// 値は 50 ms ごとに届くが、届いた瞬間燃費をそのまま出すと読めない。
// 直近2秒の距離と燃料をそれぞれ合計してから割り、数字は1秒ごとに書き換える。
//
// 2026-09-17〜28 の走行ログ (10 km/h 以上で約20時間) で、2秒平均を小数2桁で
// 出したときに数字が1秒あたり何回変わるかを数えた。0.4秒ごとの書き換えなら
// 2.4回、1秒ごとなら 0.9回。MIL-STD-1472F 5.14.3.4.1 は「確実に読ませたい
// 数字は毎秒1回より速く更新しない」としている。マツダ純正 (DJ デミオ) と
// ScanGauge は約2秒ごと。踏み方の良し悪しは、毎回書き換える色で追える。
//
// 出し分けは docs/calculation-logic.md の仕様どおり:
//   fuel_economy > 0  → km/L
//   fuel_economy = 0  → L/h  (停車・10 km/h 未満。アイドリングでも 0.87 L/h 流れている)
//   fuel_economy = -1 → "--" (エンブレ判定。約半分が外れているので数値を出さない)
// エンブレ判定中の値は合計にも入れない。入れると、終わった直後の燃費が良く出る。
const INST_WINDOW_MS = 2000;  // 合計する長さ
const INST_TEXT_MS = 1000;    // 数字を書き換える間隔
const INST_MIN_MS = 500;      // 合計できた長さがこれ未満なら出さない (エンブレ明けなど)
// 届く間隔がこれより空いたら合計をやり直す。同じ内容の配信は省かれて
// 1秒ごとの heartbeat だけになる (ws_hub.go) ので、それより長くとる。
const INST_GAP_MS = 1500;
const INST_MAX_KML = 99.99;   // 平均燃費と同じ上限

// 内側のリングに出すアークの振り切り。2026-09-17〜28 の走行ログ (2秒平均) で決めた。
//   km/L (10 km/h 以上): 中央値 12.7・75% 点 19・90% 点 27.6。30 を超えるのは 7.7%
//   L/h  (10 km/h 未満): 停車の中央値 0.88・99% 点 1.76、低速の 99% 点 2.90
// 速度計の内側のアーク (スロットル) と同じく、主のアーク (太さ 6) より細く、
// にじみも薄くして、外側のバキュームと見分けられるようにする。
const INST_ARC_MAX_KML = 30;
const INST_ARC_MAX_LH = 3;
const VAC_INNER_R = MAP_R - 16;  // バキューム計の内側のリング
let instSamples = [];         // { t, dt, km, l }
let instLastAt = 0;
let instTextAt = 0;
let instMode = '';

// 内側のアーク。数字と同じ値・同じ色で、毎フレームなめらかに寄せる。
function lerpInstArc() {
  const delta = instArcTgt - instArcCur;
  instArcCur = Math.abs(delta) > 0.001 ? instArcCur + delta * MG_LERP : instArcTgt;
  const angle = MG_ARC_START + instArcCur * MG_ARC_SWEEP;
  instArcEl.setAttribute('d', instArcCur > 0.005 ? arcPath(MAP_CX, MAP_CY, VAC_INNER_R, MG_ARC_START, angle) : '');
  instArcEl.setAttribute('stroke', instArcCol);
  instArcRaf = Math.abs(instArcCur - instArcTgt) > 0.0005 ? requestAnimationFrame(lerpInstArc) : 0;
}

// バキューム計と同じ色相 (0 bar = 赤, -1 bar = 青)
function vacHueOf(mapKpa) {
  const bar = (mapKpa - 101.3) / 100;
  const pct = Math.max(0, Math.min(100, (bar - VAC_MIN) / (VAC_MAX - VAC_MIN) * 100));
  return (1 - pct / 100) * HUE_MAX;
}

function updateInstantEco(d, obdOn, mapKpa, now) {
  const fe = d.fuel_economy || 0;
  const dt = instLastAt ? now - instLastAt : 0;
  instLastAt = now;
  if (!obdOn || dt > INST_GAP_MS) instSamples = [];
  if (obdOn && fe >= 0 && dt > 0) {
    const h = dt / 3600000;
    instSamples.push({ t: now, dt, km: (d.speed_kmh || 0) * h, l: (d.fuel_rate_lh || 0) * h });
  }
  while (instSamples.length && instSamples[0].t <= now - INST_WINDOW_MS) instSamples.shift();

  let ms = 0, km = 0, l = 0;
  for (const s of instSamples) { ms += s.dt; km += s.km; l += s.l; }

  // frac はアークの長さ (0〜1)。null ならアークを消す (km/L も L/h も出さない場面)
  let mode, text = '--', col, frac = null;
  if (!obdOn) {
    mode = 'off';
    col = '#333';
  } else if (fe < 0 || ms < INST_MIN_MS) {
    mode = 'none';
    col = `hsl(${vacHueOf(mapKpa)}, 100%, 55%)`;
  } else if (fe === 0) {
    // 停車・低速。少ないほど緑、多いほど赤 (km/L と同じく「緑が良い」)
    mode = 'L/h';
    const lh = l / (ms / 3600000);
    text = lh.toFixed(2);
    frac = Math.min(lh / INST_ARC_MAX_LH, 1);
    col = `hsl(${(1 - frac) * 153}, 100%, 55%)`;
  } else {
    mode = 'km/L';
    const kmL = l > 0 ? Math.min(km / l, INST_MAX_KML) : INST_MAX_KML;
    text = kmL.toFixed(2);
    frac = Math.min(kmL / INST_ARC_MAX_KML, 1);
    col = `hsl(${Math.min(kmL / ecoGradientMax, 1) * 153}, 100%, 55%)`;
  }
  instValEl.setAttribute('fill', col);
  if (frac === null) {
    instArcTgt = instArcCur = 0;
    instArcEl.setAttribute('d', '');
  } else {
    instArcTgt = frac;
    instArcCol = col;
    if (!instArcRaf) instArcRaf = requestAnimationFrame(lerpInstArc);
  }
  // 出し方が変わったとき (エンブレに入った、止まった) は1秒を待たずに書き換える
  if (mode !== instMode || now - instTextAt >= INST_TEXT_MS) {
    instValEl.textContent = text;
    instUnitEl.textContent = mode === 'L/h' ? 'L/h' : 'km/L';
    instMode = mode;
    instTextAt = now;
  }
}

// --- アイコン生成 ---
function createIconPath(svg, x, y, pathD, size) {
  const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
  g.setAttribute('class', 'acc-dim');
  g.setAttribute('transform', `translate(${x - size/2}, ${y - size/2}) scale(${size/24})`);
  // bloom outline (下敷き、fill色を stroke として太く縁取り)
  const bloom = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  bloom.setAttribute('d', pathD);
  bloom.setAttribute('fill', 'none');
  bloom.setAttribute('stroke', '#444');
  bloom.setAttribute('stroke-width', '3');
  bloom.setAttribute('stroke-linejoin', 'round');
  bloom.setAttribute('opacity', '0.45');
  g.appendChild(bloom);
  const p = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  p.setAttribute('d', pathD);
  p.setAttribute('fill', '#444');
  g.appendChild(p);
  svg.appendChild(g);
  // fill 変更 → bloom stroke 同期
  const origSet = p.setAttribute.bind(p);
  p.setAttribute = (k, v) => {
    origSet(k, v);
    if (k === 'fill') bloom.setAttribute('stroke', v);
  };
  return p;
}

function createLeafIcon(svg, x, y, size) {
  const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
  g.setAttribute('class', 'acc-dim');
  g.setAttribute('transform', `translate(${x}, ${y}) rotate(60) scale(${size/20})`);
  // bloom outline (下敷き)
  const outlineBloom = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  outlineBloom.setAttribute('d', ICON_LEAF);
  outlineBloom.setAttribute('fill', 'none');
  outlineBloom.setAttribute('stroke', '#444');
  outlineBloom.setAttribute('stroke-width', '3.5');
  outlineBloom.setAttribute('opacity', '0.4');
  outlineBloom.setAttribute('stroke-linejoin', 'round');
  g.appendChild(outlineBloom);
  const outline = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  outline.setAttribute('d', ICON_LEAF);
  outline.setAttribute('fill', 'none');
  outline.setAttribute('stroke', '#444');
  outline.setAttribute('stroke-width', '1.5');
  g.appendChild(outline);
  // outline stroke 変更を bloom に同期
  const origSet = outline.setAttribute.bind(outline);
  outline.setAttribute = (k, v) => {
    origSet(k, v);
    if (k === 'stroke') outlineBloom.setAttribute('stroke', v);
  };
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

  // バキューム計中心グラデーション
  let vDefs = svg.querySelector('defs');
  if (!vDefs) { vDefs = document.createElementNS('http://www.w3.org/2000/svg', 'defs'); svg.insertBefore(vDefs, svg.firstChild); }
  const vrg = document.createElementNS('http://www.w3.org/2000/svg', 'radialGradient');
  vrg.setAttribute('id', 'vacGlow');
  vrg.setAttribute('cx', MAP_CX); vrg.setAttribute('cy', MAP_CY); vrg.setAttribute('r', MAP_R);
  vrg.setAttribute('gradientUnits', 'userSpaceOnUse');
  [['0%', '#58587a'], ['50%', '#181824'], ['100%', '#000000']].forEach(([o, c]) => {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'stop');
    s.setAttribute('offset', o); s.setAttribute('stop-color', c);
    vrg.appendChild(s);
  });
  vDefs.appendChild(vrg);
  // メタリックベゼル用 linearGradient (速度計と同じ)
  const vBezel = document.createElementNS('http://www.w3.org/2000/svg', 'linearGradient');
  vBezel.setAttribute('id', 'vacBezelOuter');
  vBezel.setAttribute('x1', '0%'); vBezel.setAttribute('y1', '0%');
  vBezel.setAttribute('x2', '0%'); vBezel.setAttribute('y2', '100%');
  [['0%', '#3a3d44'], ['50%', '#5a5f68'], ['100%', '#4a4d54']].forEach(([o, c]) => {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'stop');
    s.setAttribute('offset', o); s.setAttribute('stop-color', c);
    vBezel.appendChild(s);
  });
  vDefs.appendChild(vBezel);
  const vBezelInner = document.createElementNS('http://www.w3.org/2000/svg', 'linearGradient');
  vBezelInner.setAttribute('id', 'vacBezelInner');
  vBezelInner.setAttribute('x1', '0%'); vBezelInner.setAttribute('y1', '0%');
  vBezelInner.setAttribute('x2', '0%'); vBezelInner.setAttribute('y2', '100%');
  [['0%', '#0a0a0e'], ['50%', '#1c1e24'], ['100%', '#04040a']].forEach(([o, c]) => {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'stop');
    s.setAttribute('offset', o); s.setAttribute('stop-color', c);
    vBezelInner.appendChild(s);
  });
  vDefs.appendChild(vBezelInner);

  const vBg = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
  vBg.setAttribute('cx', MAP_CX); vBg.setAttribute('cy', MAP_CY); vBg.setAttribute('r', MAP_R);
  vBg.setAttribute('fill', 'url(#vacGlow)');
  svg.insertBefore(vBg, vDefs.nextSibling);

  // (ベゼル一時無効化)

  // バキュームトラック（radialGradient ストローク）
  createGradientTrack(svg, MAP_CX, MAP_CY, MAP_R, ARC_W, MG_ARC_START, MG_ARC_END, '#040408', '#34344a', '#040408');
  // バキュームインナーリング
  createGradientTrack(svg, MAP_CX, MAP_CY, VAC_INNER_R, 10, MG_ARC_START, MG_ARC_END, '#020204', '#333345', '#020204');

  // Ticks
  for (let i = 0; i <= VAC_TOTAL; i++) {
    const a = MG_ARC_START + (i / VAC_TOTAL) * MG_ARC_SWEEP;
    const isMj = i % VAC_MN === 0;
    const ri = isMj ? MAP_R - 14 : MAP_R - 11;
    const ro = MAP_R + 3;
    const [x1, y1] = polar(MAP_CX, MAP_CY, ri, a);
    const [x2, y2] = polar(MAP_CX, MAP_CY, ro, a);
    svgEl(svg, 'line', { x1, y1, x2, y2, stroke: isMj ? '#aaa' : '#444', 'stroke-width': isMj ? 4 : 2 });
  }
  // 目盛りの数字 (-1.0〜0) は出さない (2026-10-04)。値は下の数字で読め、内側の
  // 瞬間燃費のアークの始まりが「-1.0」に重なる。速度計の外側・内側のアークにも
  // 目盛りの数字は無い。

  // Active arc
  mapArcEl = createBloom(svg, 'path', { d: '', fill: 'none', stroke: '#555', 'stroke-width': 6, 'stroke-linecap': 'round' }, 10, 0.35);
  // 瞬間燃費のアーク (内側のリング)。主のアークの 2/3 の太さで、にじみも細く薄く
  instArcEl = createBloom(svg, 'path', { d: '', fill: 'none', stroke: '#555', 'stroke-width': 4, 'stroke-linecap': 'round' }, 7, 0.30);

  // Needle
  const [mnx0, mny0] = polar(MAP_CX, MAP_CY, MAP_R - 18, MG_ARC_START);
  const [mtx0, mty0] = polar(MAP_CX, MAP_CY, -10, MG_ARC_START);
  mapNeedleEl = createBloom(svg, 'line', { x1: mtx0, y1: mty0, x2: mnx0, y2: mny0, stroke: '#78909c', 'stroke-width': 4.5, 'stroke-linecap': 'round', 'transform-origin': `${MAP_CX}px ${MAP_CY}px` }, 8, 0.3);
  // Center dot
  svgEl(svg, 'circle', { cx: MAP_CX, cy: MAP_CY, r: 5, fill: '#1a1a22', stroke: '#444', 'stroke-width': 2 });

  // 瞬間燃費 — 針の上に重ねる (後に描いた方が前に出る)。速度計の回転数の数字と同じく、
  // 針が通っても数字が隠れない。下半分の「-0.47 / Bar」と同じく、数字の下に単位を
  // 置いて中央にそろえる。数字は 36 (38 まで入るが、見比べて一段控えめにした)。
  // いちばん広い「99.99」でも角が内側のアークのにじみ (中心から約 103.5 より外) に
  // 掛からない。
  // 単位は針の付け根 (y 150〜) の手前で止まる (2026-10-04 に実測)
  instValEl = svgEl(svg, 'text', { x: MAP_CX, y: MAP_CY - 47, class: 'g-num', fill: '#333', 'font-size': 36, 'text-anchor': 'middle' });
  instValEl.textContent = '--';
  bloomText(instValEl, 2.5, 0.45);
  // 単位は他の単位と同じ g-unit・白。大きさだけ 20 に落とす。数字が 26 と小さく、
  // 24 だと単位が数字とほぼ同じ大きさに見えたため (Bar は数字 48 に対して 24)
  instUnitEl = svgEl(svg, 'text', { x: MAP_CX, y: MAP_CY - 17, class: 'g-unit', fill: '#fff', 'font-size': 20, 'text-anchor': 'middle' });
  instUnitEl.textContent = 'km/L';

  // Value（ドロップシャドウ付き）
  mapValEl = svgEl(svg, 'text', { x: MAP_CX, y: MAP_CY + MAP_R * 0.38, class: 'g-num', fill: '#333', 'font-size': 48, 'text-anchor': 'middle' });
  addOffsetShadow(mapValEl);
  mapValEl.textContent = '--';
  // Unit — 数字との間を瞬間燃費 (数字と単位の間 約 16) とつり合わせる。
  // +44 では約 27 空いて、単位だけ離れて見えた (2026-10-02)
  mapUnitEl = svgEl(svg, 'text', { x: MAP_CX, y: MAP_CY + MAP_R * 0.38 + 36, class: 'g-unit', fill: '#fff', 'font-size': 24, 'text-anchor': 'middle' });
  mapUnitEl.textContent = 'Bar';

  // === 3行インジケーター ===
  // ガラスパネル（各行に角丸背景 + 色付きボーダー）
  function addIndPanel(y) {
    createBloom(svg, 'rect', { class: 'acc-dim', x: -12, y: y - 30, width: 270, height: 44, rx: 6, fill: 'rgba(255,255,255,0.13)', stroke: 'rgba(255,255,255,0.22)', 'stroke-width': 1.5 }, 6, 0.25);
  }

  // Row 0: ECO (葉アイコン、数字も色も平均燃費)
  const ecoY = IND_Y_START;
  addIndPanel(ecoY);
  const leafIcons = createLeafIcon(svg, IND_X_ICON + 16, ecoY - 12, 30);
  ecoIconEls = leafIcons;
  ecoValEl = svgEl(svg, 'text', { x: IND_X_VAL, y: ecoY + 6, class: 'g-num', fill: '#333', 'font-size': 40, 'text-anchor': 'middle' });
  ecoValEl.textContent = '--';
  svgEl(svg, 'text', { x: IND_X_UNIT, y: ecoY + 4, class: 'g-unit', fill: '#fff', 'font-size': 24, 'text-anchor': 'end' }).textContent = 'km/L';

  // Row 1: RNG (給油までの推定残距離、km)
  const rngY = IND_Y_START + IND_SPACING;
  addIndPanel(rngY);
  rngIconEl = createIconPath(svg, IND_X_ICON + 10, rngY - 8, ICON_FUELPUMP, 40);
  rngIconEl.setAttribute('fill', '#fff');
  rngValEl = svgEl(svg, 'text', { x: IND_X_VAL, y: rngY + 6, class: 'g-num', fill: '#fff', 'font-size': 40, 'text-anchor': 'middle' });
  rngValEl.textContent = '--';
  svgEl(svg, 'text', { x: IND_X_UNIT, y: rngY + 4, class: 'g-unit', fill: '#fff', 'font-size': 24, 'text-anchor': 'end' }).textContent = 'km';

  // Row 2: TRIP (今走った距離)
  const tripY = IND_Y_START + IND_SPACING * 2;
  addIndPanel(tripY);
  tripIconEl = createIconPath(svg, IND_X_ICON + 10, tripY - 8, ICON_ROAD, 40);
  tripValEl = svgEl(svg, 'text', { x: IND_X_VAL, y: tripY + 6, class: 'g-num', fill: '#333', 'font-size': 40, 'text-anchor': 'middle' });
  tripValEl.textContent = '0';
  svgEl(svg, 'text', { x: IND_X_UNIT, y: tripY + 4, class: 'g-unit', fill: '#fff', 'font-size': 24, 'text-anchor': 'end' }).textContent = 'km';

  // Row 3: OIL (オイル交換までの残距離)
  //
  // 一度 ATF 油温に入れ替えたが、オイル交換距離に戻した。全走行ログ
  // 622,534 サンプルを集計すると ATF は 97.4% の時間が 90℃以下で、
  // 危険域に入るのは新東名のような長い登り勾配に限られる。走行中に
  // 常時見る枠としては動きが乏しい。整備の残距離は毎日行動に繋がる
  // （あと何kmで交換するか）ため、こちらを常設に戻した。
  // ATF 油温は API とログには残っており、2画面目 (#178) で扱う。
  const oilY = IND_Y_START + IND_SPACING * 3;
  addIndPanel(oilY);
  oilIconEl = createIconPath(svg, IND_X_ICON + 10, oilY - 8, ICON_OIL, 40);
  oilValEl = svgEl(svg, 'text', { x: IND_X_VAL, y: oilY + 6, class: 'g-num', fill: '#333', 'font-size': 40, 'text-anchor': 'middle' });
  oilValEl.textContent = '--';
  oilLabelEl = svgEl(svg, 'text', { x: IND_X_UNIT, y: oilY + 4, class: 'g-unit', fill: '#fff', 'font-size': 24, 'text-anchor': 'end' });
  oilLabelEl.textContent = 'km';

  return {};
}

// MAP 直接アニメーション（起動アニメ用）
export function setMapDirect(pct, col) {
  if (!mapArcEl) return;
  const angle = MG_ARC_START + pct * MG_ARC_SWEEP;
  mapArcEl.setAttribute('d', pct > 0.001 ? arcPath(MAP_CX, MAP_CY, MAP_R, MG_ARC_START, angle) : '');
  mapNeedleEl.style.transition = 'none';
  rotateWithBloom(mapNeedleEl, `rotate(${angle - MG_ARC_START}deg)`);
  if (col) { mapArcEl.setAttribute('stroke', col); mapNeedleEl.setAttribute('stroke', col); }
}

export function restoreMapTransition() {
  if (mapNeedleEl) {
    mapNeedleEl.style.transition = 'transform 0.05s ease-out';  // 針は即応答
    if (mapNeedleEl._bloom) mapNeedleEl._bloom.style.transition = 'transform 0.8s cubic-bezier(0.18, 0.89, 0.32, 1.15)';
  }
}

// OIL lamp colors (2画面目へ移した際に再利用する)
const OIL_COLORS = { green: '#69f0ae', yellow: '#fdd835', orange: '#ff9800', red: '#f44336' };

// ATF 油温の色。キーは API の atf_level (空文字 = 正常)。
//
// 区分は「1段 = 油の寿命が半分」で刻んである (internal/can/obd.go を参照)。
// 実測 24.1時間での滞在割合は 緑59% / 黄緑21% / 黄14% / 橙5% / 赤0%。
// 高速に乗ると黄緑が主役になり、踏み続けると黄へ移る。
const ATF_COLORS = {
  '': '#69f0ae',        // 〜90℃    緑     普段。街乗りとアイドリングはほぼここ
  warm: '#c6ff00',      // 90-100   黄緑   高速に乗った。想定内
  caution: '#fdd835',   // 100-110  黄     踏んでいる。劣化が5倍で進む
  hot: '#ff9800',       // 110-120  橙     新東名で58分続いた領域
  danger: '#f44336',    // 120〜    赤     24時間の実測で未到達
};

// updateIndicators: APIデータで更新
export function updateIndicators(dom, d, conf) {
  // バキューム (kPa → bar)
  const mapKpa = d.intake_map || 0;
  // OBD 未接続時 (ACC OFF) は大気圧 = 101.3 kPa (= 0 bar) に針を強制
  if (d.obd_connected !== false) {
    mapTgt = (mapKpa - 101.3) / 100;
  } else {
    mapTgt = -1.0;
  }
  if (!mapRaf) mapRaf = requestAnimationFrame(lerpMap);

  // 瞬間燃費 (バキューム計の中)
  updateInstantEco(d, d.obd_connected !== false, mapKpa, performance.now());

  // ECO 平均燃費 (Row 0) — 色も平均燃費で決める (0 km/L 赤 → ecoGradientMax 以上で緑)。
  // 以前は色だけ瞬間燃費で、数字と色が別の値を指していた。瞬間燃費はバキューム計の中に出す
  const avgEco = Math.min(d.avg_fuel_economy || 0, 99.99);
  const hasAvg = avgEco > 0.1;
  ecoValEl.textContent = hasAvg ? avgEco.toFixed(2) : '--';
  const ecoCol = hasAvg ? `hsl(${Math.min(avgEco / ecoGradientMax, 1) * 153}, 100%, 55%)` : '#fff';
  ecoValEl.setAttribute('fill', ecoCol);
  ecoIconEls.outline.setAttribute('stroke', ecoCol);
  ecoIconEls.vein.setAttribute('stroke', ecoCol);
  ecoIconEls.stem.setAttribute('stroke', ecoCol);


  // RNG (給油までの推定残距離) — 色は残量警告 (ZJ-VE 46L タンク基準)
  const rngKm = d.range_to_empty_km || 0;
  if (rngKm > 0) {
    rngValEl.textContent = rngKm.toFixed(1);
    // TRIP (350/400/450) と等価な閾値、満タン 500km 想定
    const rngCol = rngKm >= 150 ? '#69f0ae' : rngKm >= 100 ? '#fdd835' : rngKm >= 50 ? '#ff9800' : '#f44336';
    rngValEl.setAttribute('fill', rngCol);
    rngIconEl.setAttribute('fill', rngCol);
  } else {
    rngValEl.textContent = '--';
    rngValEl.setAttribute('fill', '#fff');
    rngIconEl.setAttribute('fill', '#fff');
  }

  // TRIP
  const tripKm = d.trip_km || 0;
  tripValEl.textContent = tripKm >= 0.1 ? tripKm.toFixed(1) : '0';
  const tripCol = tripKm < 350 ? '#69f0ae' : tripKm < 400 ? '#fdd835' : tripKm < 450 ? '#ff9800' : '#f44336';
  tripValEl.setAttribute('fill', tripCol);
  tripIconEl.setAttribute('fill', tripCol);
  setFilter(tripIconEl.parentNode, 'url(#glow-mid)');

  // OIL (オイル交換までの残距離)
  //
  // 色はアプリ側の判定 (oil_alert) に従う。閾値を UI 側で持たないのは、
  // 判定を1か所にまとめて食い違いを防ぐため。
  const oilCurrent = d.oil_current_km;
  const oilCol = OIL_COLORS[d.oil_alert || 'green'] || OIL_COLORS.green;
  if (oilCurrent != null) {
    oilValEl.textContent = Math.round(oilCurrent).toLocaleString();
  } else {
    oilValEl.textContent = '--';
  }
  oilValEl.setAttribute('fill', oilCol);
  oilIconEl.setAttribute('fill', oilCol);
  setFilter(oilIconEl.parentNode, 'url(#glow-mid)');
}
