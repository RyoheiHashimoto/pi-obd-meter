// ============================================================
// 状態画面 (#178) — 停車中に「SSH しないと分からないこと」を出す
// ============================================================
//
// 載せる基準は「見て、何か変わるか」。実際に車の中で聞かれて、Mac から
// SSH するか GAS のシートを開くまで答えられなかったものだけを置いてある。
// 水温の最高も速度の最高も一度も聞かれていないので載せない。ログにある。
//
// データ源は2本に分けてある (#178 の案A)。
//
//   /api/health   この画面が出ている間だけ 3秒ごとに取りに行く
//   realtime      メーター画面がすでに受けている流れを横流しする
//
// ディスクの空きも SoC 温度も 5Hz で動く値ではない。realtime に相乗り
// させると、見ていない間もずっと載せ続けることになる。

import { OIL_COLORS, ATF_COLORS } from './indicators.js';

// この画面が出ている間だけ叩く。3秒より速くしても読めるものが増えない。
// readLoggers() が systemctl を起動するので、無闇に縮めるとその分だけ
// Pi のプロセス起動が増える。
const HEALTH_POLL_MS = 3000;

// 最終送信がこれより古ければ注意色にする。
// メンテナンス送信は5分間隔なので、2回続けて落ちたと分かる長さに取る。
const SENT_STALE_SEC = 12 * 60;

// /data の空き (GB)。走行ログは実測 4.5MB/時 なので 2GB でも当面は保つが、
// 気づいてから動ける余地を残して 5GB から注意にする。
const DATA_WARN_GB = 5;
const DATA_BAD_GB = 2;

// 車の電圧。#178 が挙げた「13.5V を下回ったらオルタネータ」に合わせる。
// エンジンが回っているときだけ見る。ACC 中は 12V 台が正常。
const VOLT_WARN = 13.5;
const VOLT_BAD = 12.5;
const ENGINE_RUNNING_RPM = 400;

// 燃料トリム (短期+長期) の合計。±10% を超えたらエア吸い・インジェクタ・
// O2 センサを疑う。クローズドループのときだけ意味を持つ。
const TRIM_WARN_PCT = 10;
const TRIM_BAD_PCT = 20;

// ロガーの表示名。キーは health.go の loggerUnits と揃えること。
// ここに無いユニットが増えても拾えるよう、未知のキーはそのまま出す。
const LOGGER_LABELS = {
  'drive-verify': 'drive',
  'gps-log': 'gps',
  'imu-log': 'imu',
  'can-verify': 'can',
};
const LOGGER_ORDER = ['drive-verify', 'gps-log', 'imu-log', 'can-verify'];

let el = null;
let lastHealth = null;
let lastRealtime = null;
let healthFailed = false;
let pollTimer = null;

// --- DOM helpers ---
function div(cls, parent) {
  const n = document.createElement('div');
  n.className = cls;
  if (parent) parent.appendChild(n);
  return n;
}

function row(parent, label) {
  const r = div('status-row', parent);
  div('status-label', r).textContent = label;
  return div('status-body', r);
}

function field(parent, label) {
  const f = div('status-field', parent);
  if (label) div('status-field-label', f).textContent = label;
  return div('status-field-val', f);
}

function chip(parent, label) {
  const c = div('status-chip', parent);
  const dot = div('status-chip-dot', c);
  const name = div('status-chip-label', c);
  name.textContent = label;
  const age = div('status-chip-age', c);
  return { root: c, dot, name, age };
}

// setLevel は色の段階を付け替える。lv は ok / warn / bad / off。
function setLevel(node, lv) {
  node.classList.remove('is-ok', 'is-warn', 'is-bad', 'is-off');
  node.classList.add(`is-${lv}`);
}

// --- 表示の整形 ---

// uptimeText は稼働時間を 2:14 / 14分 の形にする。
function uptimeText(sec) {
  const m = Math.floor(sec / 60);
  const h = Math.floor(m / 60);
  return h > 0 ? `${h}:${String(m % 60).padStart(2, '0')}` : `${m}分`;
}

// agoText は経過秒を「2分前」にする。null は未送信。
function agoText(sec) {
  if (sec === null) return '未送信';
  if (sec < 60) return `${sec}秒前`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}分前`;
  return `${Math.floor(m / 60)}時間${String(m % 60).padStart(2, '0')}分前`;
}

// secondsSince は RFC3339 文字列からの経過秒。空・不正なら null。
//
// Pi に RTC が無く壁時計が飛ぶ (#195) ため、負になることがある。
// その場合は 0 に寄せる。ブラウザも Pi も同じ時計を見ているので、
// 飛んだ直後の一瞬を除けば一致する。
function secondsSince(iso) {
  if (!iso) return null;
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return null;
  return Math.max(0, Math.round((Date.now() - t) / 1000));
}

function kmText(km) {
  return Math.round(km).toLocaleString('en-US');
}

// createStatusScreen は DOM を1度だけ組み立てる。
export function createStatusScreen(parent) {
  const wrap = div('status', parent);

  const head = div('status-head', wrap);
  const ver = div('status-ver', head);
  ver.textContent = '--';
  const uptime = div('status-uptime', head);

  const rows = div('status-rows', wrap);

  // 「ログ取れてる？」— プロセスの生存と、実際に書けているかは別物
  const loggersBody = row(rows, '記録');

  // 「wifi繋がってる？」— つながっているのと送れているのも別物
  const commBody = row(rows, '通信');
  const wifi = chip(commBody, 'WiFi');
  const sent = field(commBody, '最終送信');
  const pending = field(commBody, '未送信');

  // 「SDには問題ない？」
  //
  // health.Status の DiskWrittenGB (/proc/diskstats) は載せない。あれは
  // 「起動してから」の書き込み量で、走行1回ぶんしか入らないので見ても
  // 何も変わらない。寿命の目安にしたいのは dumpe2fs -h の Lifetime writes で、
  // これは別物。同じ「書込 GB」の顔をして並べると取り違える。
  const diskBody = row(rows, '保存');
  const free = field(diskBody, '空き');
  const unclean = field(diskBody, '異常終了');

  // 「油温はどこまで上がってた？」
  const atf = field(row(rows, 'ATF'), 'この走行の最高');

  // 1画面目から追い出した行き場
  const oil = field(row(rows, 'オイル'), '交換まで');

  // 「給油した。動いてる？」
  const refuel = field(row(rows, '給油'), '');

  const alerts = div('status-alerts', wrap);
  alerts.hidden = true;

  const foot = div('status-foot', wrap);
  const dots = div('status-dots', foot);
  div('status-dot', dots);
  div('status-dot is-on', dots);
  div('status-hint', foot).textContent = '左右スワイプ / ← → で切替';

  el = {
    ver, uptime, loggersBody, loggerChips: new Map(),
    wifi, sent, pending, free, unclean, atf, oil, refuel, alerts,
  };
  render();
  return el;
}

// --- 更新 ---

// noteRealtime は realtime の最新値を覚えるだけ。
//
// realtime は 20Hz で来る。ここで DOM を書くと、オイル残量のように
// 分単位でしか動かない値を毎フレーム書き直すことになる。描画は
// /api/health の 3秒ごとの tick にまとめる。
export function noteRealtime(d) {
  lastRealtime = d;
}

// setStatusVisible は画面の表示・非表示に合わせてポーリングを開始・停止する。
export function setStatusVisible(visible) {
  if (visible) {
    if (pollTimer) return;
    render();       // 前回の値で即座に描く (取得を待たせない)
    pollHealth();   // 出した瞬間に1回
    pollTimer = setInterval(pollHealth, HEALTH_POLL_MS);
  } else if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

async function pollHealth() {
  try {
    const resp = await fetch('/api/health');
    if (!resp.ok) throw new Error(resp.status);
    lastHealth = await resp.json();
    healthFailed = false;
  } catch {
    healthFailed = true;
  }
  render();
}

function render() {
  if (!el) return;
  renderHealth();
  renderRealtime();
  renderAlerts();
}

function renderHealth() {
  const h = lastHealth || {};
  const pi = h.pi || {};

  el.ver.textContent = h.version || '--';
  el.uptime.textContent = h.uptime_sec ? `稼働 ${uptimeText(h.uptime_sec)}` : '';

  renderLoggers(pi.loggers || {});

  const wifiOk = h.wifi_connected === true;
  setLevel(el.wifi.dot, wifiOk ? 'ok' : 'bad');
  setLevel(el.wifi.name, wifiOk ? 'ok' : 'bad');

  // 「未送信 0件」は「送れている」を意味しない。一度も送ろうとしていない
  // ときも 0 件になる。最終送信時刻と並べて初めて読める (#178)。
  const sentSec = secondsSince(h.last_sent_at);
  el.sent.textContent = agoText(sentSec);
  setLevel(el.sent, sentSec === null || sentSec > SENT_STALE_SEC ? 'warn' : 'ok');

  const pending = h.pending_count || 0;
  el.pending.textContent = `${pending}件`;
  setLevel(el.pending, pending > 0 ? 'warn' : 'ok');

  // 0, 0 は「未マウント・取得できない」。「空きゼロ」ではない (health.go)
  if (pi.data_total_gb > 0) {
    el.free.textContent = `${pi.data_free_gb.toFixed(1)} / ${Math.round(pi.data_total_gb)} GB`;
    setLevel(el.free, pi.data_free_gb < DATA_BAD_GB ? 'bad'
      : pi.data_free_gb < DATA_WARN_GB ? 'warn' : 'ok');
  } else {
    el.free.textContent = '未マウント';
    setLevel(el.free, 'warn');
  }

  // 数えているのは OS の起動ではなくアプリの起動。make deploy も
  // auto-update も1回に数える。走行に対する電源断の割合とは読めない
  // (health.go の Status のコメント)。だから分母も一緒に出す。
  el.unclean.textContent = `${pi.unclean_shutdowns || 0} / 起動 ${pi.boot_count || 0}`;
  setLevel(el.unclean, 'off');

  if (h.atf_max_c > 0) {
    el.atf.textContent = `${h.atf_max_c.toFixed(1)} ℃`;
    el.atf.style.color = ATF_COLORS[h.atf_max_level || ''] || ATF_COLORS[''];
  } else {
    el.atf.textContent = '--';
    el.atf.style.color = '';
  }

  el.refuel.textContent = h.pending_fuel ? '未送信あり' : 'なし';
  setLevel(el.refuel, h.pending_fuel ? 'warn' : 'off');
}

function renderLoggers(loggers) {
  const units = Object.keys(loggers);
  units.sort((a, b) => {
    const ia = LOGGER_ORDER.indexOf(a);
    const ib = LOGGER_ORDER.indexOf(b);
    return (ia < 0 ? LOGGER_ORDER.length : ia) - (ib < 0 ? LOGGER_ORDER.length : ib);
  });

  for (const u of units) {
    let c = el.loggerChips.get(u);
    if (!c) {
      c = chip(el.loggersBody, LOGGER_LABELS[u] || u);
      el.loggerChips.set(u, c);
    }
    const st = loggers[u] || {};

    // active と writing を分ける。プロセスが生きていても書けていないことが
    // ある。#164 の gps-log は active のまま 480km 衛星0個だった。
    let lv;
    if (!st.active) lv = 'off';            // 止めてある (can-verify 等) ことも多い
    else if (st.writing) lv = 'ok';
    else lv = 'warn';                      // 動いているのに伸びていない

    setLevel(c.dot, lv);
    setLevel(c.name, lv);
    // 伸びているときは秒数を出さない。全部緑を一瞥するための画面なので、
    // 数字は異常なときだけ出す。
    c.age.textContent = lv === 'warn' ? (st.age_sec >= 0 ? `${st.age_sec}s` : 'ログ無し') : '';
  }

  // バックエンドから消えたユニットの chip を残さない
  for (const [u, c] of el.loggerChips) {
    if (!(u in loggers)) {
      c.root.remove();
      el.loggerChips.delete(u);
    }
  }
}

function renderRealtime() {
  const d = lastRealtime;
  if (!d || typeof d.oil_remaining_km !== 'number') return;
  const km = d.oil_remaining_km;
  el.oil.textContent = km >= 0 ? `${kmText(km)} km` : `${kmText(-km)} km 超過`;
  // 色はアプリ側の判定 (oil_alert) に従う。閾値を UI 側で持たない。
  el.oil.style.color = OIL_COLORS[d.oil_alert || 'green'] || OIL_COLORS.green;
}

// renderAlerts は異常のときだけ出る行を組み直す。
//
// #178 で「保留」になっていた電圧と燃料トリムはここに置いた。一度も
// 聞かれていないが、壊れたときに真っ先に見る値である。平時は1行も
// 使わせず、外れたときだけ場所を取る形にすれば、載せない理由がなくなる。
function renderAlerts() {
  const out = [];
  const h = lastHealth || {};
  const pi = h.pi || {};
  const d = lastRealtime || {};

  if (healthFailed) out.push({ text: '状態を取得できない', lv: 'bad' });

  if (pi.under_voltage_now) out.push({ text: 'Pi 電圧低下', lv: 'bad' });
  else if (pi.under_voltage_ever) out.push({ text: 'Pi 電圧低下の履歴', lv: 'warn' });

  if (pi.throttled_now) out.push({ text: 'Pi 高温で制限中', lv: 'bad' });
  else if (pi.soc_temp_c >= 80) out.push({ text: `SoC ${Math.round(pi.soc_temp_c)}℃`, lv: 'warn' });

  // エンジンが回っているときだけ見る。止まっている車の電圧を毎回
  // 警告しても、見て変えられることが無い。
  const running = d.obd_connected !== false && (d.rpm || 0) > ENGINE_RUNNING_RPM;
  if (running && d.voltage > 0 && d.voltage < VOLT_WARN) {
    out.push({ text: `電圧 ${d.voltage.toFixed(1)}V`, lv: d.voltage < VOLT_BAD ? 'bad' : 'warn' });
  }

  // 燃料トリムはクローズドループのときだけ意味を持つ (internal/obd/pids.go)。
  // 暖機中や全開のオープンループでは O2 で補正していないので、値が
  // 振れていても異常ではない。
  if (running && String(d.fuel_system_str || '').startsWith('クローズドループ')) {
    const trim = (d.short_fuel_trim || 0) + (d.long_fuel_trim || 0);
    if (Math.abs(trim) >= TRIM_WARN_PCT) {
      out.push({
        text: `燃料トリム ${trim > 0 ? '+' : ''}${trim.toFixed(0)}%`,
        lv: Math.abs(trim) >= TRIM_BAD_PCT ? 'bad' : 'warn',
      });
    }
  }

  el.alerts.textContent = '';
  el.alerts.hidden = out.length === 0;
  for (const a of out) {
    const n = div(`status-alert is-${a.lv}`, el.alerts);
    n.textContent = a.text;
  }
}
