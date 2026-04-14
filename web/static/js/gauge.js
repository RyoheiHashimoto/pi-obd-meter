// ============================================================
// Speed Gauge — SVGアークゲージ + ArcAnimator + 60fps補間
// ============================================================

const DEG_TO_RAD = Math.PI / 180;
const ARC_START = -135;
const ARC_END = 135;
const ARC_SWEEP = 270;
const THROTTLE_R_OFFSET = 84;
const RPM_R_OFFSET = 24; // 速度アークの外側

const LERP_SPEED = 0.35;
const LERP_THRESHOLD = 0.05;
const LERP_STOP = 0.01;
const HUE_MAX = 210;

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

// グロー効果: SVG filter (WebKit Bug 246106 対策: CSS filter は SVG 子要素に効かない)
// 色は要素自身の stroke/fill から取る
// パフォーマンス: 同じ filter が既に設定されていれば setAttribute を呼ばない
// (setAttribute は同値でも WPE WebKit の filter 領域を再計算させフリーズ原因)
function applyGlow(el, color, strength = 'strong') {
  const want = `url(#glow-${strength})`;
  if (el._glow !== want) { el.setAttribute('filter', want); el._glow = want; }
}
function removeGlow(el) {
  if (el._glow !== '') { el.removeAttribute('filter'); el._glow = ''; }
}

// 速度→ゲージ色（ZJ-VE / DYデミオ実用域に合わせた8段階）
export function speedColor(v) {
  if (v >= 130) return '#f44336'; // 赤
  if (v >= 120) return '#ff9800'; // 橙
  if (v >= 100) return '#ffeb3b'; // 黄
  if (v >= 80)  return '#76ff03'; // 黄緑（高速）
  if (v >= 60)  return '#69f0ae'; // 緑（巡航）
  if (v >= 30)  return '#26c6da'; // 水色（市街地）
  if (v >= 10)  return '#42a5f5'; // 青（低速）
  return '#78909c'; // 停車・非アクティブ
}

// 立体トラック描画（SVG radialGradient で内暗→中明→外暗）
let trackGradCount = 0;
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

// --- ArcAnimator: アーク LERP アニメーション ---
// value → percentage → angle → arcPath → HSL色 → glow の流れで描画する。
class ArcAnimator {
  constructor({ cx, cy, r, maxVal, lerpSpeed, arcEl, offColor, activeThreshold, labelEl, readoutVal, readoutUnit, formatVal, dimZone }) {
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
    this.dimZone = dimZone || 0; // pct がこの値以下で暗→明グラデーション
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
    let col;
    if (!active) {
      col = this.offColor;
    } else if (this.dimZone > 0 && pct < this.dimZone) {
      const dim = pct / this.dimZone; // 0→1
      const lum = 15 + dim * 40;     // 15%→55%
      const sat = dim * 100;          // 0%→100%
      col = `hsl(${hue}, ${sat}%, ${lum}%)`;
    } else {
      col = hue < 5 ? '#f44336' : `hsl(${hue}, 100%, 55%)`;
    }
    this.arcEl.setAttribute('stroke', col);
    applyGlow(this.arcEl, col);
    if (this.labelEl) {
      this.labelEl.setAttribute('fill', col);
      if (active && (!this.dimZone || pct >= this.dimZone)) applyGlow(this.labelEl, col, 'mid');
      else removeGlow(this.labelEl);
    }
    if (this.readoutVal) {
      const rdCol = active ? col : '#333';
      this.readoutVal.setAttribute('fill', rdCol);
      this.readoutUnit.setAttribute('fill', rdCol);
      this.readoutVal.textContent = active ? this.formatVal(this.cur) : '--';
    }
    this.rafId = Math.abs(this.cur - this.tgt) > LERP_STOP ? requestAnimationFrame(this._lerp) : 0;
  }
}


// --- ギアポジション表示 ---
let gearEl, gearSubEl, holdLabelEl, lockLabelEl;

export function updateGear(gear, range, hold, tcLocked) {
  if (!gearEl) return;
  const color = range === 'P' ? '#ffffff' : range === 'R' ? '#ff9800' : range === 'N' ? '#ffffff' : hold ? '#fdd835' : '#69f0ae';

  // 右上: ギア番号
  if (range === 'P' || range === 'N' || range === 'R') {
    gearEl.textContent = '--';
  } else if (gear >= 1 && gear <= 4) {
    gearEl.textContent = String(gear);
  } else {
    gearEl.textContent = '-';
  }
  gearEl.setAttribute('fill', color);
  applyGlow(gearEl, color, 'strong');
  gearEl._box.setAttribute('stroke', color);

  // 左上: レンジ
  gearSubEl.textContent = range || '';
  gearSubEl.setAttribute('fill', color);
  applyGlow(gearSubEl, color, 'strong');
  gearSubEl._box.setAttribute('stroke', color);

  // HOLD label
  if (holdLabelEl) {
    holdLabelEl.setAttribute('fill', hold ? '#fdd835' : '#333');
    if (hold) applyGlow(holdLabelEl, '#fdd835', 'strong'); else removeGlow(holdLabelEl);
  }
  // LOCK label
  if (lockLabelEl) {
    lockLabelEl.setAttribute('fill', tcLocked ? '#69f0ae' : '#333');
    if (tcLocked) applyGlow(lockLabelEl, '#69f0ae', 'strong'); else removeGlow(lockLabelEl);
  }
}

// --- RPM色: 回転数に応じた色 ---
// RPM→色（ZJ-VE 91PS/6000rpm, 124Nm/3500rpm に合わせた8段階）
function rpmColor(rpm) {
  if (rpm >= 5000) return '#f44336';  // 赤
  if (rpm >= 4000) return '#ff9800';  // 橙
  if (rpm >= 3500) return '#fdd835';  // 黄
  if (rpm >= 3000) return '#76ff03';  // 黄緑（パワーバンド突入）
  if (rpm >= 2000) return '#69f0ae';  // 緑（通常走行）
  if (rpm >= 1500) return '#26c6da';  // 水色（街中走行）
  if (rpm >= 1000) return '#42a5f5';  // 青（アイドル付近）
  return '#78909c';                    // 非アクティブ
}

// --- RPM アニメーター ---
let rpmAnimator;
const RPM_MAX = 8000;

export function updateRPM(rpm) {
  if (rpmAnimator) rpmAnimator.update(rpm);
}

// --- スロットル アニメーター ---
let thrAnimator;

export function setThrottleIdleBaseline(val) { thrIdleBaseline = val; }
export function setThrottleMaxPct(val) { thrMaxPct = val; }

export function updateThrottle(pct) {
  const range = thrMaxPct - thrIdleBaseline;
  const normalized = range > 0 ? Math.min(100, Math.max(0, (pct - thrIdleBaseline) / range * 100)) : 0;
  thrAnimator.update(normalized);
}

// --- スピードゲージ構築 ---
export function buildSpeedGauge(svgId, cfg) {
  const svg = document.getElementById(svgId);
  const { cx, cy, r, min, max, unit, mj, mn, numSz, tkSz } = cfg;
  const throttleR = r - THROTTLE_R_OFFSET;

  // 速度計中心グラデーション
  let defs = svg.querySelector('defs');
  if (!defs) { defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs'); svg.insertBefore(defs, svg.firstChild); }
  const spdGrad = document.createElementNS('http://www.w3.org/2000/svg', 'radialGradient');
  spdGrad.setAttribute('id', 'spdGlow');
  spdGrad.setAttribute('cx', cx); spdGrad.setAttribute('cy', cy); spdGrad.setAttribute('r', r);
  spdGrad.setAttribute('gradientUnits', 'userSpaceOnUse');
  [['0%', '#3a3a58'], ['50%', '#14141e'], ['100%', '#000000']].forEach(([o, c]) => {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'stop');
    s.setAttribute('offset', o); s.setAttribute('stop-color', c);
    spdGrad.appendChild(s);
  });
  defs.appendChild(spdGrad);

  // === メタリックベゼル (外周金属リング) ===
  // 明るい上→暗い下のグラデーションで「光が上から当たる金属」を表現
  const mkGrad = (id, stops, vertical = true) => {
    const g = document.createElementNS('http://www.w3.org/2000/svg', 'linearGradient');
    g.setAttribute('id', id);
    g.setAttribute('x1', '0%'); g.setAttribute('y1', vertical ? '0%' : '0%');
    g.setAttribute('x2', vertical ? '0%' : '100%'); g.setAttribute('y2', vertical ? '100%' : '0%');
    stops.forEach(([o, c]) => {
      const s = document.createElementNS('http://www.w3.org/2000/svg', 'stop');
      s.setAttribute('offset', o); s.setAttribute('stop-color', c);
      g.appendChild(s);
    });
    defs.appendChild(g);
  };
  mkGrad('bezelOuter', [['0%', '#6a6f78'], ['45%', '#2a2d33'], ['55%', '#1a1c20'], ['100%', '#3a3d44']]);
  mkGrad('bezelInner', [['0%', '#0a0a0e'], ['50%', '#1c1e24'], ['100%', '#04040a']]);
  // ハブキャップ用 (needle center 用)
  const hubGrad = document.createElementNS('http://www.w3.org/2000/svg', 'radialGradient');
  hubGrad.setAttribute('id', 'hubGrad');
  hubGrad.setAttribute('cx', '40%'); hubGrad.setAttribute('cy', '30%'); hubGrad.setAttribute('r', '70%');
  [['0%', '#8a8d94'], ['40%', '#3a3d44'], ['100%', '#0a0a0e']].forEach(([o, c]) => {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'stop');
    s.setAttribute('offset', o); s.setAttribute('stop-color', c);
    hubGrad.appendChild(s);
  });
  defs.appendChild(hubGrad);
  // ガラスハイライト (上部からの光反射)
  const glassGrad = document.createElementNS('http://www.w3.org/2000/svg', 'radialGradient');
  glassGrad.setAttribute('id', 'glassHL');
  glassGrad.setAttribute('cx', '35%'); glassGrad.setAttribute('cy', '20%'); glassGrad.setAttribute('r', '60%');
  [['0%', 'rgba(255,255,255,0.22)'], ['55%', 'rgba(255,255,255,0.05)'], ['100%', 'rgba(255,255,255,0)']].forEach(([o, c]) => {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'stop');
    s.setAttribute('offset', o); s.setAttribute('stop-color', c);
    glassGrad.appendChild(s);
  });
  defs.appendChild(glassGrad);
  // ビネット (周辺減光)
  const vigGrad = document.createElementNS('http://www.w3.org/2000/svg', 'radialGradient');
  vigGrad.setAttribute('id', 'vignette');
  vigGrad.setAttribute('cx', '50%'); vigGrad.setAttribute('cy', '50%'); vigGrad.setAttribute('r', '55%');
  [['70%', 'rgba(0,0,0,0)'], ['100%', 'rgba(0,0,0,0.85)']].forEach(([o, c]) => {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'stop');
    s.setAttribute('offset', o); s.setAttribute('stop-color', c);
    vigGrad.appendChild(s);
  });
  defs.appendChild(vigGrad);

  const bg = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
  bg.setAttribute('cx', cx); bg.setAttribute('cy', cy); bg.setAttribute('r', r);
  bg.setAttribute('fill', 'url(#spdGlow)');
  svg.insertBefore(bg, defs.nextSibling);

  // メタリック外周ベゼル (2層)
  svgEl(svg, 'circle', { cx, cy, r: r + 30, fill: 'none', stroke: 'url(#bezelOuter)', 'stroke-width': 10 });
  svgEl(svg, 'circle', { cx, cy, r: r + 22, fill: 'none', stroke: 'url(#bezelInner)', 'stroke-width': 4 });

  // 同心円ガイドライン（階層感）
  svgEl(svg, 'path', { d: arcPath(cx, cy, 200, ARC_START, ARC_END), fill: 'none', stroke: '#1a1a24', 'stroke-width': 1 });
  svgEl(svg, 'path', { d: arcPath(cx, cy, 175, ARC_START, ARC_END), fill: 'none', stroke: '#1a1a24', 'stroke-width': 1 });

  // RPM トラック（radialGradient ストローク）
  const rpmR = r + RPM_R_OFFSET;
  createGradientTrack(svg, cx, cy, rpmR, 12, ARC_START, ARC_END, '#040408', '#3a3a48', '#040408');
  // Redzone background (6500-8000)
  const redStart = ARC_START + (6500 / RPM_MAX) * ARC_SWEEP;
  createGradientTrack(svg, cx, cy, rpmR, 12, redStart, ARC_END, '#200000', '#7a0000', '#200000');
  const rpmArcEl = svgEl(svg, 'path', { d: '', fill: 'none', stroke: '#555', 'stroke-width': 14, 'stroke-linecap': 'round' });

  // 速度トラック（radialGradient ストローク）
  createGradientTrack(svg, cx, cy, r, 16, ARC_START, ARC_END, '#040408', '#34344a', '#040408');

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
      const [lx, ly] = polarToXY(cx, cy, r - 54, a);
      const t = svgEl(svg, 'text', { x: lx, y: ly, class: 'tk-lbl', fill: '#fff', 'font-size': tkSz });
      t.textContent = Math.round(v);
    }
  }

  // 目盛り最内端のインナーリング
  const innerRingR = r + 4 - 30;
  createGradientTrack(svg, cx, cy, innerRingR, 10, ARC_START, ARC_END, '#020204', '#22222e', '#020204');

  // スロットルトラック
  createGradientTrack(svg, cx, cy, throttleR, 10, ARC_START, ARC_END, '#020204', '#22222e', '#020204');
  const thrArcEl = svgEl(svg, 'path', { d: '', fill: 'none', stroke: '#555', 'stroke-width': 12, 'stroke-linecap': 'round' });

  // THROTTLE label は unitY 確定後に配置
  let thrLabel;

  // Shift position (左上、アークの外) — 枠付き
  const rangeX = cx - r - 10;
  const rangeY = 62;
  const boxW = 64, boxH = 62, boxR = 8;
  const rangeBox = svgEl(svg, 'rect', { x: rangeX - boxW/2, y: rangeY - boxH + 14, width: boxW, height: boxH, rx: boxR, fill: '#000', stroke: '#444', 'stroke-width': 3 });
  gearSubEl = svgEl(svg, 'text', { x: rangeX, y: rangeY, class: 'g-num', fill: '#555', 'font-size': 52, 'text-anchor': 'middle', 'dominant-baseline': 'auto' });
  gearSubEl.textContent = '';
  gearSubEl._box = rangeBox;
  // Gear number (右上、アークの外) — 枠付き
  const gearNumX = cx + r + 2;
  const gearNumY = 62;
  const gearBox = svgEl(svg, 'rect', { x: gearNumX - boxW/2, y: gearNumY - boxH + 14, width: boxW, height: boxH, rx: boxR, fill: '#000', stroke: '#444', 'stroke-width': 3 });
  gearEl = svgEl(svg, 'text', { x: gearNumX, y: gearNumY, class: 'g-num', fill: '#555', 'font-size': 52, 'text-anchor': 'middle', 'dominant-baseline': 'auto' });
  gearEl._box = gearBox;

  // HOLD label (レンジ枠の下)
  holdLabelEl = svgEl(svg, 'text', { x: rangeX, y: rangeY + boxH - 22, class: 'g-unit', fill: '#333', 'font-size': 24, 'text-anchor': 'middle' });
  holdLabelEl.textContent = 'HOLD';
  // LOCK label (ギア枠の下)
  lockLabelEl = svgEl(svg, 'text', { x: gearNumX, y: gearNumY + boxH - 22, class: 'g-unit', fill: '#333', 'font-size': 24, 'text-anchor': 'middle' });
  lockLabelEl.textContent = 'LOCK';
  gearEl.textContent = '-';

  // Value arc
  const va = svgEl(svg, 'path', { d: '', fill: 'none', stroke: cfg.color, 'stroke-width': 18, 'stroke-linecap': 'round' });
  applyGlow(va, cfg.color);

  // Needle
  const [nx0, ny0] = polarToXY(cx, cy, r - 24, ARC_START);
  const [tx0, ty0] = polarToXY(cx, cy, -16, ARC_START);
  const nd = svgEl(svg, 'line', { x1: tx0, y1: ty0, x2: nx0, y2: ny0, stroke: cfg.color, 'stroke-width': 6, 'stroke-linecap': 'round', 'transform-origin': `${cx}px ${cy}px` });
  applyGlow(nd, cfg.color);

  // ハブキャップ (立体感のある中心: リング + 光沢)
  svgEl(svg, 'circle', { cx, cy, r: 14, fill: 'url(#hubGrad)', stroke: '#000', 'stroke-width': 1 });
  svgEl(svg, 'circle', { cx: cx - 3, cy: cy - 4, r: 3, fill: 'rgba(255,255,255,0.25)' });

  // RPM readout (針の上に表示)
  const rpmReadY = cy - Math.round(throttleR / 2) + 5;
  const rpmValEl = svgEl(svg, 'text', { x: cx, y: rpmReadY, class: 'g-num', fill: '#333', 'font-size': 48, 'text-anchor': 'middle' });
  rpmValEl.setAttribute('filter', 'url(#text-shadow)');
  rpmValEl.textContent = '--';
  const rpmUnitEl = svgEl(svg, 'text', { x: cx, y: rpmReadY + 34, class: 'g-unit', fill: '#333', 'font-size': 24, 'text-anchor': 'middle' });
  rpmUnitEl.textContent = 'r/min';

  // Number display (ドロップシャドウ付き)
  const numY = cy + r * 0.35;
  const nm = svgEl(svg, 'text', { x: cx, y: numY, class: 'g-num', fill: cfg.color, 'font-size': numSz });
  nm.setAttribute('filter', 'url(#text-shadow)');
  nm.textContent = '0';

  // Unit label
  const unitY = numY + numSz * 0.45;
  const ut = svgEl(svg, 'text', { x: cx, y: unitY, class: 'g-unit', fill: '#fff', 'font-size': 28 });
  ut.textContent = unit;

  // THROTTLE label (km/hの下)
  thrLabel = svgEl(svg, 'text', { x: cx, y: unitY + 64, class: 'g-unit', fill: '#333', 'font-size': 24, 'text-anchor': 'middle' });
  thrLabel.textContent = 'THROTTLE';

  // === ガラス反射オーバーレイ (「ガラス越し」の質感) ===
  // ※ mix-blend-mode: screen で下層を明るくするだけの非破壊ハイライト
  const glass = svgEl(svg, 'ellipse', {
    cx, cy: cy - 30, rx: r + 20, ry: r * 0.55,
    fill: 'url(#glassHL)',
    style: 'mix-blend-mode: screen; pointer-events: none;'
  });

  // ArcAnimator インスタンス生成
  thrAnimator = new ArcAnimator({
    cx, cy, r: throttleR, maxVal: 100, lerpSpeed: 0.4,
    arcEl: thrArcEl, offColor: '#333', activeThreshold: 0.5, labelEl: thrLabel, dimZone: 5,
  });

  // RPM ArcAnimator（目盛り数字の上を通過、色はRPMに応じて動的変化）
  rpmAnimator = new ArcAnimator({
    cx, cy, r: rpmR, maxVal: RPM_MAX, lerpSpeed: 0.4,
    arcEl: rpmArcEl, offColor: '#222', activeThreshold: 100,
  });
  rpmAnimator._lerp = function() {
    const delta = rpmAnimator.tgt - rpmAnimator.cur;
    rpmAnimator.cur = Math.abs(delta) > LERP_THRESHOLD ? rpmAnimator.cur + delta * rpmAnimator.lerpSpeed : rpmAnimator.tgt;
    const pct = Math.max(0, Math.min(100, rpmAnimator.cur / rpmAnimator.maxVal * 100));
    const angle = ARC_START + (pct / 100) * ARC_SWEEP;
    rpmArcEl.setAttribute('d', pct > 0.5 ? arcPath(cx, cy, rpmR, ARC_START, angle) : '');
    const active = rpmAnimator.cur > rpmAnimator.activeThreshold;
    const col = active ? rpmColor(rpmAnimator.cur) : rpmAnimator.offColor;
    rpmArcEl.setAttribute('stroke', col);
    applyGlow(rpmArcEl, col);
    rpmValEl.textContent = active ? Math.round(rpmAnimator.cur).toLocaleString() : '--';
    rpmValEl.setAttribute('fill', col);
    rpmUnitEl.setAttribute('fill', '#fff');
    rpmAnimator.rafId = Math.abs(rpmAnimator.cur - rpmAnimator.tgt) > LERP_STOP ? requestAnimationFrame(rpmAnimator._lerp) : 0;
  };

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

  // 直接アニメーション（LERP なし、起動アニメ用）
  function setDirect(pct, col) {
    const angle = ARC_START + pct * ARC_SWEEP;
    va.setAttribute('d', pct > 0.001 ? arcPath(cx, cy, r, ARC_START, angle) : '');
    nd.style.transition = 'none';
    nd.style.transform = `rotate(${angle - ARC_START}deg)`;
    if (col) { nd.setAttribute('stroke', col); va.setAttribute('stroke', col); applyGlow(nd, col); applyGlow(va, col); }
  }

  function setRPMDirect(pct, col) {
    const angle = ARC_START + pct * ARC_SWEEP;
    rpmArcEl.setAttribute('d', pct > 0.001 ? arcPath(cx, cy, rpmR, ARC_START, angle) : '');
    if (col) { rpmArcEl.setAttribute('stroke', col); applyGlow(rpmArcEl, col); }
  }

  function setThrDirect(pct, col) {
    const angle = ARC_START + pct * ARC_SWEEP;
    thrArcEl.setAttribute('d', pct > 0.001 ? arcPath(cx, cy, throttleR, ARC_START, angle) : '');
    if (col) { thrArcEl.setAttribute('stroke', col); applyGlow(thrArcEl, col); }
  }

  function restoreTransition() {
    nd.style.transition = 'transform 0.15s ease-out';
  }

  return {
    update(value, col) {
      tgtVal = value;
      const clamped = Math.max(min, Math.min(max, value));
      const angle = ARC_START + ((clamped - min) / (max - min)) * ARC_SWEEP;
      nd.style.transform = `rotate(${angle - ARC_START}deg)`;
      if (col) { nd.setAttribute('stroke', col); va.setAttribute('stroke', col); nm.setAttribute('fill', col); applyGlow(nd, col); applyGlow(va, col); }
      if (!rafId) rafId = requestAnimationFrame(lerp);
    },
    setDirect, setRPMDirect, setThrDirect, restoreTransition,
    getElements() { return { nm, rpmValEl, rpmUnitEl, thrLabel }; }
  };
}
