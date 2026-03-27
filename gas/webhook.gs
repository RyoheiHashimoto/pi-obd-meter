/**
 * DYデミオ 車載メーター — Google Apps Script
 *
 * 役割:
 * - doPost: Pi からのデータ受信 (オイル交換状態)
 * - doGet: スマホ向けダッシュボード (給油記録・オイル交換・ODO補正)
 *
 * シート構成:
 * - 給油記録: 手動入力した給油データ + 燃費自動算出
 * - 設定: Pi との通信用 KVS (odometer_correction, trip_reset, total_km 等)
 *
 * セットアップ手順:
 * 1. Google Sheets で新しいスプレッドシートを作成
 * 2. 拡張機能 → Apps Script を開く
 * 3. このコードを貼り付け
 * 4. setup() を実行（シート初期化）
 * 5. デプロイ → 新しいデプロイ → ウェブアプリ
 *    - 実行するユーザー: 自分
 *    - アクセスできるユーザー: 全員
 * 6. 表示されたURLをラズパイの config.json の webhook_url に設定
 */

// === doPost ハンドラーマップ ===
const POST_HANDLERS = {
  maintenance: handleMaintenance,
  restore: handleRestore,
};

// === Webhook エンドポイント (Pi → GAS) ===
function doPost(e) {
  try {
    const { type, data } = JSON.parse(e.postData.contents);
    const handler = POST_HANDLERS[type];
    if (!handler) {
      return jsonResponse({ error: `不明なtype: ${type}` }, 400);
    }
    return handler(data);
  } catch (err) {
    return jsonResponse({ error: err.message }, 500);
  }
}

// === Webダッシュボード (スマホ → GAS) ===
function doGet() {
  return HtmlService.createHtmlOutput(buildDashboardHtml())
    .setTitle('DYデミオ ダッシュボード')
    .setXFrameOptionsMode(HtmlService.XFrameOptionsMode.ALLOWALL);
}

// === メンテナンス状態処理 (Pi → GAS、5分間隔) ===
function handleMaintenance(data) {
  // OIL 状態を設定シートに保存
  if (data.oil_current_km != null) {
    upsertSetting('oil_current_km', data.oil_current_km);
  }
  if (data.oil_remaining_km != null) {
    upsertSetting('oil_remaining_km', data.oil_remaining_km);
  }
  if (data.oil_alert) {
    upsertSetting('oil_alert', data.oil_alert);
  }

  // PiのtotalKm・tripKmを設定シートに保存
  if (data.total_km > 0) {
    upsertSetting('total_km', data.total_km);
  }
  if (data.trip_km >= 0) {
    upsertSetting('trip_km', data.trip_km);
    if (data.total_km > 0) {
      upsertSetting('last_refuel_km', data.total_km - data.trip_km);
    }
  }

  // ODO補正適用確認: Piが補正を適用したら設定をクリア
  if (data.odometer_applied) {
    clearSetting('odometer_correction');
  }

  // OILリセット待ちチェック
  const oilResetPending = getSettingValue('oil_reset_pending');
  const pendingResets = [];
  if (oilResetPending) {
    pendingResets.push('oil_change');
    // Pi がリセットしたら次回送信で oil_current_km ≈ 0 になるのでクリア
    if ((data.oil_current_km || 0) < 100) {
      clearSetting('oil_reset_pending');
    }
  }

  // 設定シートからPi向けの指示を取得
  const odoCorrection = getSettingValue('odometer_correction');
  const tripCorrectionKm = getSettingValue('trip_correction_km');

  // trip_correction_kmは読んだら即クリア（1回だけ実行すればよい）
  // 注意: 値が 0 の場合もあるので null チェック（0 は falsy）
  if (tripCorrectionKm != null) {
    clearSetting('trip_correction_km');
  }

  return jsonResponse({
    status: 'ok',
    type: 'maintenance',
    pending_resets: pendingResets,
    odometer_correction: odoCorrection != null ? parseFloat(odoCorrection) : null,
    trip_correction_km: tripCorrectionKm != null ? parseFloat(tripCorrectionKm) : null,
  });
}

// === 状態復元 (Pi起動時 → GAS) ===
// 設定シートから total_km と last_refuel_km を返す。
// overlayFS環境でリブート後に累計走行距離を復元するために使用。
function handleRestore() {
  const totalKm = parseFloat(getSettingValue('total_km')) || 0;
  const lastRefuelKm = parseFloat(getSettingValue('last_refuel_km')) || 0;

  return jsonResponse({
    status: 'ok',
    type: 'restore',
    total_km: totalKm,
    last_refuel_km: lastRefuelKm,
  });
}

// === 手動給油記録 (ダッシュボードから呼ばれる) ===
function recordManualRefuel({ amount: rawAmount }) {
  const sheet = getOrCreateSheet('給油記録', [
    '日時', '距離(km)', '燃費(km/L)', '給油量(L)'
  ]);

  const amount = parseFloat(rawAmount) || 0;
  if (amount <= 0) {
    throw new Error('給油量を入力してください');
  }

  const currentKm = parseFloat(getSettingValue('total_km')) || 0;
  const lastKm = parseFloat(getSettingValue('last_refuel_km')) || 0;
  const distance = (currentKm > 0 && lastKm > 0) ? currentKm - lastKm : 0;
  const fuelEconomy = (distance > 0 && amount > 0) ? round(distance / amount, 1) : 0;

  sheet.appendRow([
    new Date(),
    round(distance, 1),
    fuelEconomy,
    round(amount, 1)
  ]);

  if (currentKm > 0) {
    upsertSetting('last_refuel_km', currentKm);
  }

  upsertSetting('trip_correction_km', '0');
  upsertSetting('trip_km', 0);

  return { status: 'ok', fuel_economy: fuelEconomy, distance: round(distance, 1) };
}

// === ODO補正 (ダッシュボードから呼ばれる) ===
function updateOdometer(km) {
  const val = parseFloat(km);
  if (!val || val <= 0) throw new Error('有効なODO値を入力してください');

  upsertSetting('odometer_correction', val);
  return { status: 'ok', odometer: val };
}

// === トリップ補正 (ダッシュボードから呼ばれる) ===
// トリップ距離を直接指定し、last_refuel_km を逆算して Pi に通知
function correctTrip(tripDistance) {
  const tripKm = parseFloat(tripDistance);
  if (!tripKm || tripKm <= 0) throw new Error('トリップ距離を入力してください');

  const currentKm = parseFloat(getSettingValue('total_km')) || 0;
  if (currentKm <= 0) throw new Error('ODOデータがありません');
  if (tripKm >= currentKm) throw new Error('現在のODO(' + Math.round(currentKm) + ' km)より小さい値を入力してください');

  const lastRefuelKm = currentKm - tripKm;
  upsertSetting('last_refuel_km', lastRefuelKm);
  upsertSetting('trip_correction_km', tripKm);
  upsertSetting('trip_km', tripKm);
  return { status: 'ok', last_refuel_km: round(lastRefuelKm, 1), trip_km: round(tripKm, 1) };
}

// === オイル交換リセット (ダッシュボードから呼ばれる) ===
function resetOilChange() {
  upsertSetting('oil_reset_pending', '1');
  return { status: 'ok' };
}

// === 設定値取得 (設定シートからキーで検索) ===
function getSettingValue(key) {
  const ss = SpreadsheetApp.getActiveSpreadsheet();
  const sheet = ss.getSheetByName('設定');
  if (!sheet || sheet.getLastRow() <= 1) return null;

  const data = sheet.getRange(2, 1, sheet.getLastRow() - 1, 2).getValues();
  const row = data.find(r => r[0] === key);
  return row ? row[1] : null;
}

// === 設定値書き込み (upsert: 既存なら更新、なければ追加) ===
function upsertSetting(key, value) {
  const sheet = getOrCreateSheet('設定', ['キー', '値', '更新日時']);
  if (sheet.getLastRow() > 1) {
    const keys = sheet.getRange(2, 1, sheet.getLastRow() - 1, 1).getValues();
    const idx = keys.findIndex(r => r[0] === key);
    if (idx >= 0) {
      sheet.getRange(idx + 2, 2).setValue(value);
      sheet.getRange(idx + 2, 3).setValue(new Date());
      return;
    }
  }
  sheet.appendRow([key, value, new Date()]);
}

// === 設定値削除 (設定シートから行ごと削除) ===
function clearSetting(key) {
  const ss = SpreadsheetApp.getActiveSpreadsheet();
  const sheet = ss.getSheetByName('設定');
  if (!sheet || sheet.getLastRow() <= 1) return;

  const data = sheet.getRange(2, 1, sheet.getLastRow() - 1, 1).getValues();
  for (let i = data.length - 1; i >= 0; i--) {
    if (data[i][0] === key) {
      sheet.deleteRow(i + 2);
      return;
    }
  }
}

// === Webダッシュボード HTML ===
function buildDashboardHtml() {
  const fuelData = getSheetData('給油記録');
  const currentOdo = parseFloat(getSettingValue('total_km')) || 0;
  const lastRefuelKm = parseFloat(getSettingValue('last_refuel_km')) || 0;
  const piTripKm = parseFloat(getSettingValue('trip_km')) || 0;
  const oilCurrentKm = parseFloat(getSettingValue('oil_current_km')) || 0;
  const oilRemainingKm = parseFloat(getSettingValue('oil_remaining_km')) || 0;
  const oilAlert = getSettingValue('oil_alert') || 'green';

  const recentFuel = fuelData.length > 0 ? fuelData.slice(-10).reverse() : [];

  // 走行統計の算出（Pi の trip_km を優先）
  const tripStats = computeTripStats(fuelData, currentOdo, lastRefuelKm, piTripKm);

  const now = Utilities.formatDate(new Date(), 'Asia/Tokyo', 'yyyy/MM/dd HH:mm');

  // === HTML 組み立て ===
  let html = '<!DOCTYPE html>';
  html += '<html lang="ja"><head>';
  html += '<meta charset="UTF-8">';
  html += '<meta name="viewport" content="width=device-width, initial-scale=1.0">';
  html += '<meta name="apple-mobile-web-app-capable" content="yes">';
  html += '<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">';
  html += '<title>DYデミオ</title>';
  html += `<style>${getDashboardCSS()}</style>`;
  html += '</head><body>';

  html += '<div class="wrap">';
  html += '<h1>DYデミオ ダッシュボード</h1>';
  html += `<div class="sub">更新: ${now}</div>`;

  // 走行統計カード
  html += renderTripStatsCard(tripStats);

  // オイル交換カード（常時表示）
  html += renderOilCard(oilCurrentKm, oilRemainingKm, oilAlert);

  // 給油記録フォーム
  html += '<div class="card">';
  html += '<h2>⛽ 給油記録</h2>';
  html += '<div class="form-group"><label>給油量 (L)</label><input type="number" id="rf-amount" class="form-input" inputmode="decimal" step="0.1" placeholder="30.5"></div>';
  html += '<button class="form-submit" id="rf-btn" onclick="submitRefuel()">給油を記録</button>';
  html += '<div class="form-result" id="rf-result"></div>';
  html += '</div>';

  // 給油履歴
  if (recentFuel.length > 0) {
    html += '<div class="card">';
    html += '<h2>📊 給油履歴</h2>';
    html += '<div class="tbl-wrap"><table><tr><th>日付</th><th>距離</th><th>燃費</th><th>給油量</th></tr>';
    const fuelLimit = Math.min(5, recentFuel.length);
    for (let i = 0; i < fuelLimit; i++) {
      html += renderFuelRow(recentFuel[i]);
    }
    html += '</table></div>';
    if (recentFuel.length > 5) {
      html += '<div id="fuel-extra" style="display:none"><div class="tbl-wrap"><table>';
      for (let i = 5; i < recentFuel.length; i++) {
        html += renderFuelRow(recentFuel[i]);
      }
      html += '</table></div></div>';
      html += '<button class="toggle-btn" id="fuel-extra-btn" onclick="toggleSection(\'fuel-extra\')">もっと見る ▼</button>';
    }
    html += '</div>';
  }

  // トリップ補正フォーム
  html += '<div class="card">';
  html += '<h2>🔄 トリップ補正</h2>';
  if (tripStats.tripKm > 0) {
    html += `<div class="form-hint" style="font-size:26px;color:#aaa;margin-bottom:14px">現在のトリップ: <b style="color:#fff">${round(tripStats.tripKm, 0)} km</b></div>`;
  }
  const tripPlaceholder = tripStats.tripKm > 0 ? Math.round(tripStats.tripKm) : '150';
  html += `<div class="form-group"><label>トリップ距離 (km)</label><input type="number" id="trip-val" class="form-input" inputmode="numeric" placeholder="${tripPlaceholder}"></div>`;
  html += '<div class="form-hint">純正メーターのトリップ値を入力してください。次回の燃費計算にも反映されます。</div>';
  html += '<button class="form-submit" id="trip-btn" onclick="submitTripCorrection()">トリップを補正</button>';
  html += '<div class="form-result" id="trip-result"></div>';
  html += '</div>';

  // ODO補正フォーム
  html += '<div class="card">';
  html += '<h2>🔧 ODO補正</h2>';
  if (currentOdo > 0) {
    html += `<div class="form-hint" style="font-size:26px;color:#aaa;margin-bottom:14px">現在の記録値: <b style="color:#fff">${Math.round(currentOdo).toLocaleString()} km</b></div>`;
  }
  const odoPlaceholder = currentOdo > 0 ? Math.round(currentOdo) : '98500';
  html += `<div class="form-group"><label>現在のODOメーター (km)</label><input type="number" id="odo-val" class="form-input" inputmode="numeric" placeholder="${odoPlaceholder}"></div>`;
  html += '<div class="form-hint">車のメーターと合わせて補正します。次回メンテナンス送信時にPiに反映されます。</div>';
  html += '<button class="form-submit" style="background:#ff9800" id="odo-btn" onclick="submitOdo()">ODOを補正</button>';
  html += '<div class="form-result" id="odo-result"></div>';
  html += '</div>';

  html += `<script>${getDashboardJS()}</script>`;
  html += '</div></body></html>';
  return html;
}

// === オイル交換カードの描画 ===
function renderOilCard(currentKm, remainingKm, alert) {
  const colors = { green: '#4caf50', yellow: '#fdd835', orange: '#ff9800', red: '#f44336' };
  const labels = { green: '正常', yellow: 'そろそろ', orange: '交換時期', red: '要交換' };
  const col = colors[alert] || colors.green;
  const label = labels[alert] || '正常';

  let html = '<div class="card">';
  html += '<h2>🛢 オイル交換</h2>';
  html += '<div class="stat-grid">';
  html += renderStatItem('走行距離', round(currentKm, 0) + ' km', col);
  html += renderStatItem('残り', round(remainingKm, 0) + ' km', remainingKm < 0 ? '#f44336' : '');
  html += '</div>';
  html += `<div style="text-align:center;margin-top:14px;font-size:28px;font-weight:700;color:${col}">${label}</div>`;
  html += '<button class="form-submit" style="background:#4caf50;margin-top:14px" id="oil-btn" onclick="resetOil()">オイル交換完了</button>';
  html += '<div class="form-result" id="oil-result"></div>';
  html += '</div>';
  return html;
}

// === ダッシュボード CSS ===
function getDashboardCSS() {
  return '*{box-sizing:border-box;-webkit-tap-highlight-color:transparent}'
    + 'body{margin:0;padding:0;width:100%;min-height:100vh;background:#0a0a10;color:#ddd;font-family:-apple-system,sans-serif;font-size:32px;-webkit-text-size-adjust:100%}'
    + '.wrap{width:100%;padding:28px}'
    + 'h1{font-size:40px;color:#fff;margin:0 0 8px}'
    + '.sub{font-size:22px;color:#666;margin-bottom:28px}'
    + '.card{background:#12121a;border-radius:18px;padding:24px;margin-bottom:24px}'
    + '.card h2{font-size:28px;color:#888;margin:0 0 18px;letter-spacing:1px}'
    + '.tbl-wrap{overflow-x:auto;-webkit-overflow-scrolling:touch}'
    + 'table{width:100%;border-collapse:collapse;font-size:28px}'
    + 'th{text-align:left;color:#666;padding:14px 16px;border-bottom:1px solid #1a1a24}'
    + 'td{padding:14px 16px;border-bottom:1px solid #0f0f18}'
    + '.toggle-btn{background:none;border:none;color:#666;cursor:pointer;padding:18px 0;font-size:24px;width:100%;text-align:center}'
    + '.form-group{margin-bottom:18px}'
    + '.form-group label{display:block;color:#888;font-size:22px;margin-bottom:6px}'
    + '.form-input{width:100%;background:#1a1a24;border:1px solid #3a3a45;border-radius:10px;color:#fff;font-size:28px;padding:16px;outline:none}'
    + '.form-input:focus{border-color:#2196f3}'
    + '.form-submit{width:100%;background:#2196f3;color:#fff;border:none;border-radius:12px;padding:18px;font-size:28px;font-weight:600;cursor:pointer;margin-top:8px}'
    + '.form-submit:active{background:#1976d2}'
    + '.form-submit:disabled{background:#333;color:#666;cursor:not-allowed}'
    + '.form-result{margin-top:14px;padding:14px;border-radius:10px;font-size:24px;display:none}'
    + '.form-result.success{display:block;background:#1b3a1b;color:#69f0ae}'
    + '.form-result.error{display:block;background:#3a1b1b;color:#f44336}'
    + '.form-hint{font-size:20px;color:#555;margin-top:6px}'
    + '.stat-grid{display:grid;grid-template-columns:1fr 1fr;gap:16px}'
    + '.stat-item{background:#1a1a24;border-radius:12px;padding:18px;text-align:center}'
    + '.stat-value{font-size:32px;font-weight:700;color:#fff}'
    + '.stat-label{font-size:20px;color:#666;margin-top:4px}';
}

// === ダッシュボード JavaScript ===
function getDashboardJS() {
  return `
function toggleSection(id) {
  const el = document.getElementById(id);
  const btn = document.getElementById(id + '-btn');
  const hidden = el.style.display === 'none';
  el.style.display = hidden ? 'block' : 'none';
  btn.textContent = hidden ? '閉じる ▲' : 'もっと見る ▼';
}

function resetOil() {
  if (!confirm('オイル交換を完了にしますか？')) return;
  const btn = document.getElementById('oil-btn');
  const res = document.getElementById('oil-result');
  btn.disabled = true;
  btn.textContent = '送信中...';
  res.className = 'form-result';

  google.script.run
    .withSuccessHandler(function() {
      res.className = 'form-result success';
      res.textContent = 'オイル交換をリセットしました（次回送信時にPiに反映）';
      btn.disabled = false;
      btn.textContent = 'オイル交換完了';
    })
    .withFailureHandler(function(e) {
      res.className = 'form-result error';
      res.textContent = 'エラー: ' + e.message;
      btn.disabled = false;
      btn.textContent = 'オイル交換完了';
    })
    .resetOilChange();
}

function submitRefuel() {
  const amount = document.getElementById('rf-amount').value;
  if (!amount) { alert('給油量を入力してください'); return; }
  if (!confirm(amount + ' L を記録しますか？')) return;

  const btn = document.getElementById('rf-btn');
  const res = document.getElementById('rf-result');
  btn.disabled = true;
  btn.textContent = '送信中...';
  res.className = 'form-result';

  google.script.run
    .withSuccessHandler(r => {
      res.className = 'form-result success';
      let msg = '記録しました';
      if (r.fuel_economy > 0) msg += '　燃費: ' + r.fuel_economy + ' km/L（' + r.distance + ' km走行）';
      res.textContent = msg;
      btn.disabled = false;
      btn.textContent = '給油を記録';
      document.getElementById('rf-amount').value = '';
    })
    .withFailureHandler(e => {
      res.className = 'form-result error';
      res.textContent = 'エラー: ' + e.message;
      btn.disabled = false;
      btn.textContent = '給油を記録';
    })
    .recordManualRefuel({ amount });
}

function submitOdo() {
  const km = document.getElementById('odo-val').value;
  if (!km) { alert('ODO値を入力してください'); return; }
  if (!confirm('ODOを ' + km + ' km に補正しますか？')) return;

  const btn = document.getElementById('odo-btn');
  const res = document.getElementById('odo-result');
  btn.disabled = true;
  btn.textContent = '送信中...';
  res.className = 'form-result';

  google.script.run
    .withSuccessHandler(r => {
      res.className = 'form-result success';
      res.textContent = 'ODOを ' + r.odometer + ' km に設定しました';
      btn.disabled = false;
      btn.textContent = 'ODOを補正';
      document.getElementById('odo-val').value = '';
    })
    .withFailureHandler(e => {
      res.className = 'form-result error';
      res.textContent = 'エラー: ' + e.message;
      btn.disabled = false;
      btn.textContent = 'ODOを補正';
    })
    .updateOdometer(km);
}

function submitTripCorrection() {
  var km = document.getElementById('trip-val').value;
  if (!km) { alert('トリップ距離を入力してください'); return; }
  if (!confirm('トリップを ' + km + ' km に補正しますか？')) return;

  var btn = document.getElementById('trip-btn');
  var res = document.getElementById('trip-result');
  btn.disabled = true;
  btn.textContent = '送信中...';
  res.className = 'form-result';

  google.script.run
    .withSuccessHandler(function(r) {
      res.className = 'form-result success';
      res.textContent = 'トリップを補正しました（' + r.trip_km + ' km）';
      btn.disabled = false;
      btn.textContent = 'トリップを補正';
      document.getElementById('trip-val').value = '';
    })
    .withFailureHandler(function(e) {
      res.className = 'form-result error';
      res.textContent = 'エラー: ' + e.message;
      btn.disabled = false;
      btn.textContent = 'トリップを補正';
    })
    .correctTrip(km);
}
`.trim();
}

// === 走行統計の算出 ===
// 給油記録: [0]=日時, [1]=距離(km), [2]=燃費(km/L), [3]=給油量(L)
function computeTripStats(fuelData, currentOdo, lastRefuelKm, piTripKm) {
  // Pi の trip_km を優先（Pi が source of truth）、なければ従来計算にフォールバック
  const fallbackTrip = (currentOdo > 0 && lastRefuelKm > 0) ? currentOdo - lastRefuelKm : 0;
  const stats = {
    currentOdo: currentOdo,
    tripKm: piTripKm > 0 ? piTripKm : fallbackTrip,
    totalFuelL: 0,
    totalDistKm: 0,
    avgEconomy: 0,
    recentAvg: 0,
    bestEconomy: 0,
    recordCount: 0,
  };

  // 有効な給油記録（燃費 > 0）のみ集計
  const valid = fuelData.filter(r => (r[2] || 0) > 0);
  stats.recordCount = valid.length;

  for (const r of valid) {
    stats.totalDistKm += r[1] || 0;
    stats.totalFuelL += r[3] || 0;
    const e = r[2] || 0;
    if (e > stats.bestEconomy) stats.bestEconomy = e;
  }

  if (stats.totalFuelL > 0) {
    stats.avgEconomy = stats.totalDistKm / stats.totalFuelL;
  }

  // 直近3回の平均
  const recent = valid.slice(-3);
  if (recent.length > 0) {
    const dist = recent.reduce((s, r) => s + (r[1] || 0), 0);
    const fuel = recent.reduce((s, r) => s + (r[3] || 0), 0);
    stats.recentAvg = fuel > 0 ? dist / fuel : 0;
  }

  return stats;
}

// === 走行統計カードの描画 ===
function renderTripStatsCard(s) {
  let html = '<div class="card">';
  html += '<h2>📈 走行統計</h2>';

  html += '<div class="stat-grid">';

  if (s.tripKm > 0) {
    html += renderStatItem('給油後', round(s.tripKm, 0) + ' km', '');
  }
  if (s.currentOdo > 0) {
    html += renderStatItem('総走行', Math.round(s.currentOdo).toLocaleString() + ' km', '');
  }
  if (s.avgEconomy > 0) {
    html += renderStatItem('通算燃費', round(s.avgEconomy, 1) + ' km/L', '#69f0ae');
  }
  if (s.recentAvg > 0) {
    html += renderStatItem('直近3回', round(s.recentAvg, 1) + ' km/L', '#4fc3f7');
  }
  if (s.bestEconomy > 0) {
    html += renderStatItem('最高燃費', round(s.bestEconomy, 1) + ' km/L', '#ffd54f');
  }
  if (s.totalFuelL > 0) {
    html += renderStatItem('総給油量', round(s.totalFuelL, 0) + ' L', '');
  }

  html += '</div>';

  if (s.recordCount === 0) {
    html += '<div style="color:#555;font-size:24px;text-align:center;padding:12px 0">給油記録がありません</div>';
  }

  html += '</div>';
  return html;
}

function renderStatItem(label, value, color) {
  const style = color ? ` style="color:${color}"` : '';
  return `<div class="stat-item"><div class="stat-value"${style}>${value}</div><div class="stat-label">${label}</div></div>`;
}

// === Render helper: 給油行 ===
function renderFuelRow(r) {
  let dateStr = '-';
  try { dateStr = Utilities.formatDate(new Date(r[0]), 'Asia/Tokyo', 'yyyy/MM/dd'); } catch (e) { /* skip */ }
  return `<tr><td>${dateStr}</td><td>${round(r[1] || 0, 0)}km</td>`
    + `<td style="color:#69f0ae;font-weight:600">${round(r[2] || 0, 1)}</td>`
    + `<td>${round(r[3] || 0, 1)}L</td></tr>`;
}

// === ユーティリティ: シートデータ取得 ===
function getSheetData(sheetName) {
  const ss = SpreadsheetApp.getActiveSpreadsheet();
  const sheet = ss.getSheetByName(sheetName);
  if (!sheet || sheet.getLastRow() <= 1) return [];
  return sheet.getRange(2, 1, sheet.getLastRow() - 1, sheet.getLastColumn()).getValues();
}

// === 初期セットアップ ===
function setup() {
  getOrCreateSheet('給油記録', [
    '日時', '距離(km)', '燃費(km/L)', '給油量(L)'
  ]);
  getOrCreateSheet('設定', ['キー', '値', '更新日時']);

  Logger.log('セットアップ完了: 給油記録 / 設定 シートを作成しました');
}

// === ユーティリティ: シート操作 ===
function getOrCreateSheet(name, headers) {
  const ss = SpreadsheetApp.getActiveSpreadsheet();
  let sheet = ss.getSheetByName(name);

  if (!sheet) {
    sheet = ss.insertSheet(name);
    if (headers.length > 0) {
      sheet.getRange(1, 1, 1, headers.length).setValues([headers]);
      sheet.getRange(1, 1, 1, headers.length)
        .setFontWeight('bold')
        .setBackground('#e8eaf6');
      sheet.setFrozenRows(1);
    }
  }

  return sheet;
}

function round(val, decimals) {
  const factor = Math.pow(10, decimals);
  return Math.round(val * factor) / factor;
}

function jsonResponse(data) {
  return ContentService
    .createTextOutput(JSON.stringify(data))
    .setMimeType(ContentService.MimeType.JSON);
}
