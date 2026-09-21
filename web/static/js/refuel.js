// ============================================================
// 給油ダイアログ (#120) — 自動検出の進み具合を停車中に見せる
// ============================================================
//
// 出すのは「検出 → 記録 → トリップリセット」の3段階だけ。入力は無い。
// 給油量はレシートとの較正用で、運転者が打ち込むものではない。
//
// pointer-events を持たせないので、画面3秒長押しのキオスク終了は
// ダイアログが出ていても今までどおり効く。

const STAGE_DETECTED = 1;
const STAGE_RECORDED = 2;
const STAGE_TRIP_RESET = 3;

let el = null; // { root, badge, steps: [...], note, prev, prevKm, prevEco }

function div(cls, parent) {
  const n = document.createElement('div');
  n.className = cls;
  if (parent) parent.appendChild(n);
  return n;
}

function step(parent, label) {
  const row = div('refuel-step', parent);
  const dot = div('refuel-dot', row);
  const text = div('refuel-step-label', row);
  text.textContent = label;
  return { row, dot, text, label };
}

// createRefuelDialog はダイアログの DOM を1度だけ作る（初期状態は非表示）。
export function createRefuelDialog(parent) {
  const root = div('refuel', parent || document.body);
  root.hidden = true;

  const head = div('refuel-head', root);
  const title = div('refuel-title', head);
  title.textContent = '給油を検出しました';
  const badge = div('refuel-badge', head);

  const steps = div('refuel-steps', root);
  const sDetect = step(steps, '検出');
  div('refuel-connector', steps);
  const sRecord = step(steps, '記録');
  div('refuel-connector', steps);
  const sTrip = step(steps, 'トリップリセット');

  const note = div('refuel-note', root);
  note.hidden = true;

  const prev = div('refuel-prev', root);
  prev.hidden = true;
  const prevLabel = div('refuel-prev-label', prev);
  prevLabel.textContent = '前のタンク';
  const prevKm = div('refuel-prev-val', prev);
  const prevEco = div('refuel-prev-val', prev);
  const prevNote = div('refuel-prev-note', prev);
  prevNote.textContent = 'Pi計算';

  el = { root, badge, steps: [sDetect, sRecord, sTrip], note, prev, prevKm, prevEco };
  return el;
}

function setState(s, state) {
  s.dot.className = `refuel-dot is-${state}`;
  s.text.className = `refuel-step-label is-${state}`;
}

// updateRefuelDialog はリアルタイムデータを受けて表示を更新する。
// d.refuel が無ければ隠す（走行中・検出なし・表示期限切れ）。
export function updateRefuelDialog(d) {
  if (!el) return;
  const r = d && d.refuel;
  if (!r || d.obd_connected === false) {
    el.root.hidden = true;
    return;
  }

  const stage = r.stage || STAGE_DETECTED;
  const wifi = d.wifi_connected !== false;

  // 満タンは量を出さない。センダーが上限でクリップしていて、
  // 跳躍量から出した数字には根拠が無い (refuel.go の FullTankPt)。
  if (r.full_tank) {
    el.badge.textContent = '満タン';
    el.badge.className = 'refuel-badge is-full';
    el.badge.hidden = false;
  } else if (r.amount_l > 0) {
    el.badge.textContent = `約 ${r.amount_l.toFixed(1)} L（推定）`;
    el.badge.className = 'refuel-badge is-partial';
    el.badge.hidden = false;
  } else {
    el.badge.hidden = true;
  }

  setState(el.steps[0], 'done');
  if (stage >= STAGE_RECORDED) {
    setState(el.steps[1], 'done');
  } else {
    setState(el.steps[1], wifi ? 'active' : 'waiting');
  }
  setState(el.steps[2], stage >= STAGE_TRIP_RESET ? 'done' : 'pending');

  const waiting = stage < STAGE_RECORDED && !wifi;
  el.note.hidden = !waiting;
  if (waiting) {
    el.note.textContent = 'WiFi 未接続。記録は保存してあり、つながり次第送ります';
  }

  const hasPrev = stage >= STAGE_TRIP_RESET && r.prev_trip_km > 0;
  el.prev.hidden = !hasPrev;
  if (hasPrev) {
    el.prevKm.textContent = `${r.prev_trip_km.toFixed(1)} km`;
    el.prevEco.textContent = r.prev_eco_kmpl > 0 ? `${r.prev_eco_kmpl.toFixed(1)} km/L` : '--';
  }

  el.root.hidden = false;
}
