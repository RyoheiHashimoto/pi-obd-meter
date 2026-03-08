/**
 * DYデミオ 車載メーター — Google Apps Script
 *
 * 役割:
 * - doPost: Pi からのデータ受信 (メンテナンス状態)
 * - doGet: スマホ向けダッシュボード (給油記録・ODO補正・メンテ管理)
 *
 * シート構成:
 * - 給油記録: 手動入力した給油データ + 燃費自動算出
 * - メンテ状態: Pi から受信した最新のメンテナンス進捗
 * - メンテ完了: ダッシュボードから「完了」を押した項目 (Pi がリセット後に自動削除)
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

// === Webhook エンドポイント (Pi → GAS) ===
// Pi から type: "maintenance" のペイロードを受信し、メンテ状態シートを更新する。
// レスポンスで pending_resets, odometer_correction, trip_reset を Pi に返す。
function doPost(e) {
  try {
    const payload = JSON.parse(e.postData.contents);

    switch (payload.type) {
      case 'maintenance':
        return handleMaintenance(payload.data);
      default:
        return jsonResponse({ error: '不明なtype: ' + payload.type }, 400);
    }
  } catch (err) {
    return jsonResponse({ error: err.message }, 500);
  }
}

// === Webダッシュボード (スマホ → GAS) ===
// スマホブラウザからアクセスすると、給油記録・メンテ管理・ODO補正の画面を返す。
function doGet(e) {
  return HtmlService.createHtmlOutput(buildDashboardHtml())
    .setTitle('DYデミオ ダッシュボード')
    .setXFrameOptionsMode(HtmlService.XFrameOptionsMode.ALLOWALL);
}

// === メンテナンス状態処理 (Pi → GAS、5分間隔) ===
// Pi からメンテナンス進捗を受信し、シートを最新状態で上書きする。
// レスポンスで Pi 向けの指示（メンテリセット・ODO補正・トリップリセット）を返す。
function handleMaintenance(data) {
  const sheet = getOrCreateSheet('メンテ状態', [
    '項目ID', '項目名', 'タイプ', '進捗(%)', '残り', '要アラート', '超過', '更新日時'
  ]);

  // 既存データをクリアして最新状態で上書き
  if (sheet.getLastRow() > 1) {
    sheet.getRange(2, 1, sheet.getLastRow() - 1, 8).clearContent();
  }

  const statuses = data.statuses || [];
  const now = new Date();

  statuses.forEach(function(s) {
    var remaining = '';
    if (s.type === 'distance') {
      remaining = round(s.remaining_km || 0, 0) + ' km';
    } else {
      remaining = (s.days_left || 0) + ' 日';
    }

    sheet.appendRow([
      s.id || '',
      s.name || s.id,
      s.type === 'distance' ? '距離' : '期日',
      round((s.progress || 0) * 100, 0),
      remaining,
      s.needs_alert ? '⚠' : '',
      s.is_overdue ? '🔴' : '',
      now
    ]);
  });

  // PiのtotalKmを設定シートに保存（給油記録時に使用）
  if (data.total_km > 0) {
    upsertSetting('total_km', data.total_km);
  }

  // ODO補正適用確認: Piが補正を適用したら設定をクリア
  if (data.odometer_applied) {
    clearSetting('odometer_correction');
  }

  // 完了済みアイテムのリセット待ちIDを取得（Phase B準備）
  var completedIds = getCompletedIds();
  var pendingIds = Object.keys(completedIds);

  // 自動クリーンアップ: Piがリセット済み(progress < 10%)なら完了シートから削除
  statuses.forEach(function(s) {
    if (completedIds[s.id] && (s.progress || 0) < 0.1) {
      removeCompleted(s.id);
    }
  });

  // 設定シートからPi向けの指示を取得
  var odoCorrection = getSettingValue('odometer_correction');
  var tripReset = getSettingValue('trip_reset');

  // trip_resetは読んだら即クリア（1回だけ実行すればよい）
  if (tripReset) {
    clearSetting('trip_reset');
  }

  return jsonResponse({
    status: 'ok',
    type: 'maintenance',
    count: statuses.length,
    pending_resets: pendingIds,
    odometer_correction: odoCorrection ? parseFloat(odoCorrection) : null,
    trip_reset: !!tripReset
  });
}

// === 手動給油記録 (ダッシュボードから呼ばれる) ===
// 給油量を記録し、前回給油時からの走行距離から燃費を算出する。
// 記録後、Pi にトリップリセットを依頼する（設定シート経由）。
function recordManualRefuel(data) {
  var sheet = getOrCreateSheet('給油記録', [
    '日時', '距離(km)', '消費燃料(L)', '燃費(km/L)', '給油量(L)',
    'タンク%(前)', 'タンク%(後)', '最高速度(km/h)', '平均速度(km/h)', '走行時間(分)'
  ]);

  var amount = parseFloat(data.amount) || 0;
  if (amount <= 0) {
    throw new Error('給油量を入力してください');
  }

  // PiのtotalKmを設定シートから取得
  var currentKm = parseFloat(getSettingValue('total_km')) || 0;
  var lastKm = parseFloat(getSettingValue('last_refuel_km')) || 0;

  var distance = (currentKm > 0 && lastKm > 0) ? currentKm - lastKm : 0;
  var fuelEconomy = (distance > 0 && amount > 0) ? round(distance / amount, 1) : 0;

  sheet.appendRow([
    new Date(),           // 日時
    round(distance, 1),   // 距離(km)
    '',                   // 消費燃料(L)
    fuelEconomy,          // 燃費(km/L)
    round(amount, 1),     // 給油量(L)
    '', '',               // タンク%(前)(後)
    '', '', ''            // 最高速度, 平均速度, 走行時間
  ]);

  // 今回のtotalKmを記録（次回の差分計算用）
  if (currentKm > 0) {
    upsertSetting('last_refuel_km', currentKm);
  }

  // Piにトリップリセットを依頼
  upsertSetting('trip_reset', 'true');

  return { status: 'ok', fuel_economy: fuelEconomy, distance: round(distance, 1) };
}

// === ODO補正 (ダッシュボードから呼ばれる) ===
// 設定シートに odometer_correction を書き込む。
// Pi が次回メンテナンス送信時にこの値を読み取り、累計走行距離を補正する。
function updateOdometer(km) {
  var val = parseFloat(km);
  if (!val || val <= 0) throw new Error('有効なODO値を入力してください');

  upsertSetting('odometer_correction', val);
  return { status: 'ok', odometer: val };
}

// === 設定値取得 (設定シートからキーで検索) ===
function getSettingValue(key) {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var sheet = ss.getSheetByName('設定');
  if (!sheet || sheet.getLastRow() <= 1) return null;

  var data = sheet.getRange(2, 1, sheet.getLastRow() - 1, 2).getValues();
  for (var i = 0; i < data.length; i++) {
    if (data[i][0] === key) return data[i][1];
  }
  return null;
}

// === 設定値書き込み (upsert: 既存なら更新、なければ追加) ===
function upsertSetting(key, value) {
  var sheet = getOrCreateSheet('設定', ['キー', '値', '更新日時']);
  if (sheet.getLastRow() > 1) {
    var keys = sheet.getRange(2, 1, sheet.getLastRow() - 1, 1).getValues();
    for (var i = 0; i < keys.length; i++) {
      if (keys[i][0] === key) {
        sheet.getRange(i + 2, 2).setValue(value);
        sheet.getRange(i + 2, 3).setValue(new Date());
        return;
      }
    }
  }
  sheet.appendRow([key, value, new Date()]);
}

// === 設定値削除 (設定シートから行ごと削除) ===
function clearSetting(key) {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var sheet = ss.getSheetByName('設定');
  if (!sheet || sheet.getLastRow() <= 1) return;

  var data = sheet.getRange(2, 1, sheet.getLastRow() - 1, 1).getValues();
  for (var i = data.length - 1; i >= 0; i--) {
    if (data[i][0] === key) {
      sheet.deleteRow(i + 2);
      return;
    }
  }
}

// === メンテナンス完了マーク (ダッシュボードから呼ばれる) ===
// 「メンテ完了」シートに記録 → Pi が次回送信時に pending_resets としてリセット指示を受け取る。
// Pi がリセット完了（progress < 10%）したら自動的にこのシートから削除される。
function markMaintenanceDone(itemId, itemName) {
  var sheet = getOrCreateSheet('メンテ完了', ['項目ID', '項目名', '完了日時']);

  // 重複チェック: 同じIDがあれば日時を更新
  if (sheet.getLastRow() > 1) {
    var data = sheet.getRange(2, 1, sheet.getLastRow() - 1, 1).getValues();
    for (var i = 0; i < data.length; i++) {
      if (data[i][0] === itemId) {
        sheet.getRange(i + 2, 3).setValue(new Date());
        return { status: 'ok' };
      }
    }
  }

  sheet.appendRow([itemId, itemName, new Date()]);
  return { status: 'ok' };
}

// === 完了済みIDマップ取得 (メンテ完了シートから {id: {name, date}} を返す) ===
function getCompletedIds() {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var sheet = ss.getSheetByName('メンテ完了');
  if (!sheet || sheet.getLastRow() <= 1) return {};

  var data = sheet.getRange(2, 1, sheet.getLastRow() - 1, 3).getValues();
  var result = {};
  data.forEach(function(r) {
    if (r[0]) result[r[0]] = { name: r[1], date: r[2] };
  });
  return result;
}

// === 完了済みアイテム削除 (Pi がリセット済みの項目をメンテ完了シートから除去) ===
function removeCompleted(itemId) {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var sheet = ss.getSheetByName('メンテ完了');
  if (!sheet || sheet.getLastRow() <= 1) return;

  var data = sheet.getRange(2, 1, sheet.getLastRow() - 1, 1).getValues();
  for (var i = data.length - 1; i >= 0; i--) {
    if (data[i][0] === itemId) {
      sheet.deleteRow(i + 2);
      return;
    }
  }
}

// === Webダッシュボード HTML ===
// ダークテーマのモバイル対応HTML を生成する。
// セクション: 給油記録フォーム → 給油履歴 → メンテナンス必要 → メンテ済 → ODO補正
function buildDashboardHtml() {
  // データ取得
  var fuelData = getSheetData('給油記録');
  var maintData = getSheetData('メンテ状態');
  var completedIds = getCompletedIds();
  var currentOdo = parseFloat(getSettingValue('total_km')) || 0;

  // 給油: 新しい順、最大10件
  var recentFuel = fuelData.length > 0 ? fuelData.slice(-10).reverse() : [];

  // メンテ分割（メンテ状態: ID=r[0], name=r[1], type=r[2], progress=r[3], remaining=r[4], alert=r[5], overdue=r[6]）
  var alertItems = [];
  maintData.forEach(function(r) {
    var id = r[0];
    if (!completedIds[id] && (r[5] === '⚠' || r[6] === '🔴')) {
      alertItems.push(r);
    }
  });

  // 完了済みエントリ（メンテ完了シートから）
  var completedEntries = [];
  Object.keys(completedIds).forEach(function(id) {
    completedEntries.push({ id: id, name: completedIds[id].name, date: completedIds[id].date });
  });

  // === HTML ===
  var html = '<!DOCTYPE html>';
  html += '<html lang="ja"><head>';
  html += '<meta charset="UTF-8">';
  html += '<meta name="viewport" content="width=device-width, initial-scale=1.0">';
  html += '<meta name="apple-mobile-web-app-capable" content="yes">';
  html += '<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">';
  html += '<title>DYデミオ</title>';
  html += '<style>' + getDashboardCSS() + '</style>';
  html += '</head><body>';

  // ヘッダー
  html += '<div class="wrap">';
  html += '<h1>DYデミオ ダッシュボード</h1>';
  html += '<div class="sub">更新: ' + Utilities.formatDate(new Date(), 'Asia/Tokyo', 'yyyy/MM/dd HH:mm') + '</div>';

  // === 給油記録フォーム ===
  html += '<div class="card">';
  html += '<h2>⛽ 給油記録</h2>';
  html += '<div class="form-group"><label>給油量 (L)</label><input type="number" id="rf-amount" class="form-input" inputmode="decimal" step="0.1" placeholder="30.5"></div>';
  html += '<button class="form-submit" id="rf-btn" onclick="submitRefuel()">給油を記録</button>';
  html += '<div class="form-result" id="rf-result"></div>';
  html += '</div>';

  // === セクション1: 給油記録 ===
  if (recentFuel.length > 0) {
    html += '<div class="card">';
    html += '<h2>📊 給油履歴</h2>';
    html += '<div class="tbl-wrap"><table><tr><th>日付</th><th>距離</th><th>燃費</th><th>給油量</th></tr>';
    var fuelLimit = Math.min(5, recentFuel.length);
    for (var i = 0; i < fuelLimit; i++) {
      html += renderFuelRow(recentFuel[i]);
    }
    html += '</table></div>';
    if (recentFuel.length > 5) {
      html += '<div id="fuel-extra" style="display:none"><div class="tbl-wrap"><table>';
      for (var i = 5; i < recentFuel.length; i++) {
        html += renderFuelRow(recentFuel[i]);
      }
      html += '</table></div></div>';
      html += '<button class="toggle-btn" id="fuel-extra-btn" onclick="toggleSection(\'fuel-extra\')">もっと見る ▼</button>';
    }
    html += '</div>';
  }

  // === セクション2: メンテナンス必要 ===
  if (alertItems.length > 0) {
    html += '<div class="card">';
    html += '<h2>⚠ メンテナンス必要</h2>';
    var alertLimit = Math.min(5, alertItems.length);
    for (var i = 0; i < alertLimit; i++) {
      html += renderAlertItem(alertItems[i]);
    }
    if (alertItems.length > 5) {
      html += '<div id="alert-extra" style="display:none">';
      for (var i = 5; i < alertItems.length; i++) {
        html += renderAlertItem(alertItems[i]);
      }
      html += '</div>';
      html += '<button class="toggle-btn" id="alert-extra-btn" onclick="toggleSection(\'alert-extra\')">もっと見る ▼</button>';
    }
    html += '</div>';
  }

  // === セクション3: メンテ済 ===
  if (completedEntries.length > 0) {
    html += '<div class="card">';
    html += '<h2>✅ メンテ済</h2>';
    var doneLimit = Math.min(5, completedEntries.length);
    for (var i = 0; i < doneLimit; i++) {
      html += renderCompletedItem(completedEntries[i]);
    }
    if (completedEntries.length > 5) {
      html += '<div id="done-extra" style="display:none">';
      for (var i = 5; i < completedEntries.length; i++) {
        html += renderCompletedItem(completedEntries[i]);
      }
      html += '</div>';
      html += '<button class="toggle-btn" id="done-extra-btn" onclick="toggleSection(\'done-extra\')">もっと見る ▼</button>';
    }
    html += '</div>';
  }

  // === ODO補正フォーム ===
  html += '<div class="card">';
  html += '<h2>🔧 ODO補正</h2>';
  if (currentOdo > 0) {
    html += '<div class="form-hint" style="font-size:26px;color:#aaa;margin-bottom:14px">現在の記録値: <b style="color:#fff">' + Math.round(currentOdo).toLocaleString() + ' km</b></div>';
  }
  html += '<div class="form-group"><label>現在のODOメーター (km)</label><input type="number" id="odo-val" class="form-input" inputmode="numeric" placeholder="' + (currentOdo > 0 ? Math.round(currentOdo) : '98500') + '"></div>';
  html += '<div class="form-hint">車のメーターと合わせて補正します。次回メンテナンス送信時にPiに反映されます。</div>';
  html += '<button class="form-submit" id="odo-btn" onclick="submitOdo()">ODOを補正</button>';
  html += '<div class="form-result" id="odo-result"></div>';
  html += '</div>';

  // JavaScript
  html += '<script>' + getDashboardJS() + '</script>';

  html += '</div>';
  html += '</body></html>';
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
    + '.bar-bg{height:16px;background:#1a1a24;border-radius:8px;overflow:hidden;margin-top:14px}'
    + '.bar-fg{height:100%;border-radius:8px}'
    + '.ok{background:#4caf50} .warn{background:#ff9800} .danger{background:#f44336}'
    + '.maint-item{padding:18px 0;border-bottom:1px solid #1a1a24}'
    + '.maint-item:last-child{border:none}'
    + '.maint-name{font-weight:600;color:#ddd;font-size:28px}'
    + '.maint-detail{font-size:24px;color:#888;margin-top:6px}'
    + '.maint-row{display:flex;justify-content:space-between;align-items:center;gap:16px}'
    + '.done-btn{background:#2a2a35;color:#aaa;border:1px solid #3a3a45;border-radius:12px;padding:18px 32px;font-size:24px;cursor:pointer;white-space:nowrap}'
    + '.done-btn:active{background:#3a3a45}'
    + '.toggle-btn{background:none;border:none;color:#666;cursor:pointer;padding:18px 0;font-size:24px;width:100%;text-align:center}'
    + '.completed-date{font-size:22px;color:#4caf50}'
    + '.form-group{margin-bottom:18px}'
    + '.form-group label{display:block;color:#888;font-size:22px;margin-bottom:6px}'
    + '.form-input{width:100%;background:#1a1a24;border:1px solid #3a3a45;border-radius:10px;color:#fff;font-size:28px;padding:16px;outline:none}'
    + '.form-input:focus{border-color:#2196f3}'
    + '.form-row{display:flex;gap:14px}'
    + '.form-row .form-group{flex:1}'
    + '.form-check{display:flex;align-items:center;gap:12px;padding:12px 0}'
    + '.form-check input[type=checkbox]{width:28px;height:28px;accent-color:#2196f3}'
    + '.form-check label{color:#ddd;font-size:26px;margin:0}'
    + '.form-submit{width:100%;background:#2196f3;color:#fff;border:none;border-radius:12px;padding:18px;font-size:28px;font-weight:600;cursor:pointer;margin-top:8px}'
    + '.form-submit:active{background:#1976d2}'
    + '.form-submit:disabled{background:#333;color:#666;cursor:not-allowed}'
    + '.form-result{margin-top:14px;padding:14px;border-radius:10px;font-size:24px;display:none}'
    + '.form-result.success{display:block;background:#1b3a1b;color:#69f0ae}'
    + '.form-result.error{display:block;background:#3a1b1b;color:#f44336}'
    + '.form-hint{font-size:20px;color:#555;margin-top:6px}';
}

// === ダッシュボード JavaScript ===
function getDashboardJS() {
  return 'function toggleSection(id){'
    + 'var el=document.getElementById(id);var btn=document.getElementById(id+"-btn");'
    + 'if(el.style.display==="none"){el.style.display="block";btn.textContent="閉じる ▲";}'
    + 'else{el.style.display="none";btn.textContent="もっと見る ▼";}}'
    // メンテナンス完了
    + 'function markDone(id,name){'
    + 'if(!confirm(name+" を完了にしますか？"))return;'
    + 'google.script.run.withSuccessHandler(function(){location.reload();})'
    + '.withFailureHandler(function(e){alert("エラー: "+e.message);})'
    + '.markMaintenanceDone(id,name);}'
    // 給油記録送信
    + 'function submitRefuel(){'
    + 'var amount=document.getElementById("rf-amount").value;'
    + 'if(!amount){alert("給油量を入力してください");return;}'
    + 'if(!confirm(amount+" L を記録しますか？"))return;'
    + 'var btn=document.getElementById("rf-btn");btn.disabled=true;btn.textContent="送信中...";'
    + 'var res=document.getElementById("rf-result");res.className="form-result";'
    + 'google.script.run'
    + '.withSuccessHandler(function(r){'
    + 'res.className="form-result success";'
    + 'var msg="記録しました";'
    + 'if(r.fuel_economy>0)msg+="　燃費: "+r.fuel_economy+" km/L（"+r.distance+" km走行）";'
    + 'res.textContent=msg;'
    + 'btn.disabled=false;btn.textContent="給油を記録";'
    + 'document.getElementById("rf-amount").value="";})'
    + '.withFailureHandler(function(e){'
    + 'res.className="form-result error";res.textContent="エラー: "+e.message;'
    + 'btn.disabled=false;btn.textContent="給油を記録";})'
    + '.recordManualRefuel({amount:amount});}'
    // ODO補正送信
    + 'function submitOdo(){'
    + 'var km=document.getElementById("odo-val").value;'
    + 'if(!km){alert("ODO値を入力してください");return;}'
    + 'if(!confirm("ODOを "+km+" km に補正しますか？"))return;'
    + 'var btn=document.getElementById("odo-btn");btn.disabled=true;btn.textContent="送信中...";'
    + 'var res=document.getElementById("odo-result");res.className="form-result";'
    + 'google.script.run'
    + '.withSuccessHandler(function(r){'
    + 'res.className="form-result success";res.textContent="ODOを "+r.odometer+" km に設定しました";'
    + 'btn.disabled=false;btn.textContent="ODOを補正";'
    + 'document.getElementById("odo-val").value="";})'
    + '.withFailureHandler(function(e){'
    + 'res.className="form-result error";res.textContent="エラー: "+e.message;'
    + 'btn.disabled=false;btn.textContent="ODOを補正";})'
    + '.updateOdometer(km);}';
}

// === Render helper: 給油行 ===
function renderFuelRow(r) {
  var dateStr = '';
  try { dateStr = Utilities.formatDate(new Date(r[0]), 'Asia/Tokyo', 'yyyy/MM/dd'); } catch(e) { dateStr = '-'; }
  var html = '<tr>';
  html += '<td>' + dateStr + '</td>';
  html += '<td>' + round(r[1] || 0, 0) + 'km</td>';
  html += '<td style="color:#69f0ae;font-weight:600">' + round(r[3] || 0, 1) + '</td>';
  html += '<td>' + round(r[4] || 0, 1) + 'L</td>';
  html += '</tr>';
  return html;
}

// === Render helper: アラート項目 ===
function renderAlertItem(r) {
  var id = r[0] || '';
  var name = r[1] || '';
  var progress = r[3] || 0;
  var remaining = r[4] || '';
  var needsAlert = r[5] === '⚠';
  var isOverdue = r[6] === '🔴';

  var barClass = isOverdue ? 'danger' : needsAlert ? 'warn' : 'ok';
  var pct = Math.min(100, progress);
  var nameColor = isOverdue ? '#f44336' : needsAlert ? '#ff9800' : '#ddd';

  var html = '<div class="maint-item">';
  html += '<div class="maint-row">';
  html += '<div>';
  html += '<div class="maint-name" style="color:' + nameColor + '">' + name + '</div>';
  html += '<div class="maint-detail">残り ' + remaining + ' (' + progress + '%)</div>';
  html += '</div>';
  html += '<button class="done-btn" onclick="markDone(\'' + id + '\',\'' + name + '\')">完了</button>';
  html += '</div>';
  html += '<div class="bar-bg"><div class="bar-fg ' + barClass + '" style="width:' + pct + '%"></div></div>';
  html += '</div>';
  return html;
}

// === Render helper: 完了済み項目 ===
function renderCompletedItem(entry) {
  var dateStr = '';
  try { dateStr = Utilities.formatDate(new Date(entry.date), 'Asia/Tokyo', 'yyyy/MM/dd'); } catch(e) { dateStr = '-'; }

  var html = '<div class="maint-item">';
  html += '<div class="maint-row">';
  html += '<div class="maint-name" style="color:#666">' + (entry.name || '') + '</div>';
  html += '<div class="completed-date">' + dateStr + ' 完了</div>';
  html += '</div>';
  html += '</div>';
  return html;
}

// === ユーティリティ: シートデータ取得（ヘッダー行を除く全行を返す） ===
function getSheetData(sheetName) {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var sheet = ss.getSheetByName(sheetName);
  if (!sheet || sheet.getLastRow() <= 1) return [];
  return sheet.getRange(2, 1, sheet.getLastRow() - 1, sheet.getLastColumn()).getValues();
}

// === 初期セットアップ (Apps Script エディタから1回だけ手動実行) ===
function setup() {
  getOrCreateSheet('給油記録', [
    '日時', '距離(km)', '消費燃料(L)', '燃費(km/L)', '給油量(L)',
    'タンク%(前)', 'タンク%(後)', '最高速度(km/h)', '平均速度(km/h)', '走行時間(分)'
  ]);
  getOrCreateSheet('メンテ状態', [
    '項目ID', '項目名', 'タイプ', '進捗(%)', '残り', '要アラート', '超過', '更新日時'
  ]);
  getOrCreateSheet('メンテ完了', [
    '項目ID', '項目名', '完了日時'
  ]);
  getOrCreateSheet('設定', [
    'キー', '値', '更新日時'
  ]);

  Logger.log('セットアップ完了: 給油記録 / メンテ状態 / メンテ完了 / 設定 シートを作成しました');
}

// === ユーティリティ: シート操作 ===

// getOrCreateSheet はシートを取得し、存在しなければヘッダー付きで作成する
function getOrCreateSheet(name, headers) {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var sheet = ss.getSheetByName(name);

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

// round は指定桁数で四捨五入する
function round(val, decimals) {
  var factor = Math.pow(10, decimals);
  return Math.round(val * factor) / factor;
}

// jsonResponse は JSON 形式の ContentService レスポンスを返す
function jsonResponse(data, statusCode) {
  return ContentService
    .createTextOutput(JSON.stringify(data))
    .setMimeType(ContentService.MimeType.JSON);
}
