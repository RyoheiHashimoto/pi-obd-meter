/**
 * DYデミオ 燃費メーター — Google Apps Script
 *
 * セットアップ手順:
 * 1. Google Sheetsで新しいスプレッドシートを作成
 * 2. 拡張機能 → Apps Script を開く
 * 3. このコードを貼り付け
 * 4. setup() を実行（シート初期化）
 * 5. デプロイ → 新しいデプロイ → ウェブアプリ
 *    - 実行するユーザー: 自分
 *    - アクセスできるユーザー: 全員
 * 6. 表示されたURLをラズパイの config.json の webhook_url に設定
 */

// === Webhook エンドポイント ===
function doPost(e) {
  try {
    const payload = JSON.parse(e.postData.contents);

    switch (payload.type) {
      case 'refuel':
        return handleRefuel(payload.data);
      case 'maintenance':
        return handleMaintenance(payload.data);
      default:
        return jsonResponse({ error: '不明なtype: ' + payload.type }, 400);
    }
  } catch (err) {
    return jsonResponse({ error: err.message }, 500);
  }
}

// === Webダッシュボード ===
function doGet(e) {
  return HtmlService.createHtmlOutput(buildDashboardHtml())
    .setTitle('DYデミオ ダッシュボード')
    .setXFrameOptionsMode(HtmlService.XFrameOptionsMode.ALLOWALL);
}

// === 給油データ処理 ===
function handleRefuel(data) {
  const sheet = getOrCreateSheet('給油記録', [
    '日時', '距離(km)', '消費燃料(L)', '燃費(km/L)', '給油量(L)',
    'タンク%(前)', 'タンク%(後)', '最高速度(km/h)', '平均速度(km/h)', '走行時間(分)'
  ]);

  const drivingMin = Math.round((data.driving_time_sec || 0) / 60);

  sheet.appendRow([
    new Date(data.start_time || Date.now()),
    round(data.distance_km || 0, 1),
    round(data.fuel_used_l || 0, 2),
    round(data.fuel_economy || 0, 1),
    round(data.refuel_amount_l || 0, 1),
    round(data.old_level_pct || 0, 1),
    round(data.new_level_pct || 0, 1),
    round(data.max_speed_kmh || 0, 0),
    round(data.avg_speed_kmh || 0, 0),
    drivingMin
  ]);

  return jsonResponse({ status: 'ok', type: 'refuel' });
}

// === メンテナンス状態処理 ===
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

  // 完了済みアイテムのリセット待ちIDを取得（Phase B準備）
  var completedIds = getCompletedIds();
  var pendingIds = Object.keys(completedIds);

  // 自動クリーンアップ: Piがリセット済み(progress < 10%)なら完了シートから削除
  statuses.forEach(function(s) {
    if (completedIds[s.id] && (s.progress || 0) < 0.1) {
      removeCompleted(s.id);
    }
  });

  return jsonResponse({
    status: 'ok',
    type: 'maintenance',
    count: statuses.length,
    pending_resets: pendingIds
  });
}

// === メンテナンス完了マーク（ダッシュボードから呼ばれる） ===
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

// === 完了済みIDマップ取得 ===
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

// === 完了済みアイテム削除 ===
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
function buildDashboardHtml() {
  // データ取得
  var fuelData = getSheetData('給油記録');
  var maintData = getSheetData('メンテ状態');
  var completedIds = getCompletedIds();

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
  html += '<style>';
  html += 'body{margin:0;padding:16px;background:#0a0a10;color:#ccc;font-family:-apple-system,sans-serif;font-size:14px}';
  html += 'h1{font-size:18px;color:#fff;margin:0 0 4px}';
  html += '.sub{font-size:11px;color:#666;margin-bottom:16px}';
  html += '.card{background:#12121a;border-radius:8px;padding:12px;margin-bottom:12px}';
  html += '.card h2{font-size:14px;color:#888;margin:0 0 8px;letter-spacing:1px}';
  html += 'table{width:100%;border-collapse:collapse;font-size:12px}';
  html += 'th{text-align:left;color:#666;padding:4px 6px;border-bottom:1px solid #1a1a24}';
  html += 'td{padding:4px 6px;border-bottom:1px solid #0f0f18}';
  html += '.bar-bg{height:6px;background:#1a1a24;border-radius:3px;overflow:hidden;margin-top:4px}';
  html += '.bar-fg{height:100%;border-radius:3px}';
  html += '.ok{background:#4caf50} .warn{background:#ff9800} .danger{background:#f44336}';
  html += '.maint-item{padding:8px 0;border-bottom:1px solid #1a1a24}';
  html += '.maint-item:last-child{border:none}';
  html += '.maint-name{font-weight:600;color:#ddd}';
  html += '.maint-detail{font-size:12px;color:#888;margin-top:2px}';
  html += '.maint-row{display:flex;justify-content:space-between;align-items:center}';
  html += '.done-btn{background:#2a2a35;color:#888;border:1px solid #3a3a45;border-radius:4px;padding:4px 10px;font-size:11px;cursor:pointer;white-space:nowrap}';
  html += '.toggle-btn{background:none;border:none;color:#666;cursor:pointer;padding:8px 0;font-size:12px;width:100%;text-align:center}';
  html += '.completed-date{font-size:11px;color:#4caf50}';
  html += '</style></head><body>';

  // ヘッダー
  html += '<h1>DYデミオ ダッシュボード</h1>';
  html += '<div class="sub">更新: ' + Utilities.formatDate(new Date(), 'Asia/Tokyo', 'yyyy/MM/dd HH:mm') + '</div>';

  // === セクション1: 給油記録 ===
  if (recentFuel.length > 0) {
    html += '<div class="card">';
    html += '<h2>📊 給油履歴</h2>';
    html += '<table><tr><th>日付</th><th>距離</th><th>燃費</th><th>給油量</th></tr>';
    var fuelLimit = Math.min(5, recentFuel.length);
    for (var i = 0; i < fuelLimit; i++) {
      html += renderFuelRow(recentFuel[i]);
    }
    html += '</table>';
    if (recentFuel.length > 5) {
      html += '<div id="fuel-extra" style="display:none"><table>';
      for (var i = 5; i < recentFuel.length; i++) {
        html += renderFuelRow(recentFuel[i]);
      }
      html += '</table></div>';
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

  // JavaScript
  html += '<script>';
  html += 'function toggleSection(id){';
  html += 'var el=document.getElementById(id);var btn=document.getElementById(id+"-btn");';
  html += 'if(el.style.display==="none"){el.style.display="block";btn.textContent="閉じる ▲";}';
  html += 'else{el.style.display="none";btn.textContent="もっと見る ▼";}';
  html += '}';
  html += 'function markDone(id,name){';
  html += 'if(!confirm(name+" を完了にしますか？"))return;';
  html += 'google.script.run.withSuccessHandler(function(){location.reload();})';
  html += '.withFailureHandler(function(e){alert("エラー: "+e.message);})';
  html += '.markMaintenanceDone(id,name);';
  html += '}';
  html += '</script>';

  html += '</body></html>';
  return html;
}

// === Render helper: 給油行 ===
function renderFuelRow(r) {
  var dateStr = '';
  try { dateStr = Utilities.formatDate(new Date(r[0]), 'Asia/Tokyo', 'M/d'); } catch(e) { dateStr = '-'; }
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
  try { dateStr = Utilities.formatDate(new Date(entry.date), 'Asia/Tokyo', 'yyyy/M/d'); } catch(e) { dateStr = '-'; }

  var html = '<div class="maint-item">';
  html += '<div class="maint-row">';
  html += '<div class="maint-name" style="color:#666">' + (entry.name || '') + '</div>';
  html += '<div class="completed-date">' + dateStr + ' 完了</div>';
  html += '</div>';
  html += '</div>';
  return html;
}

// === シートデータ取得（ヘッダー行を除く） ===
function getSheetData(sheetName) {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var sheet = ss.getSheetByName(sheetName);
  if (!sheet || sheet.getLastRow() <= 1) return [];
  return sheet.getRange(2, 1, sheet.getLastRow() - 1, sheet.getLastColumn()).getValues();
}

// === 初期セットアップ（1回だけ実行） ===
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

  Logger.log('セットアップ完了: 給油記録 / メンテ状態 / メンテ完了 シートを作成しました');
}

// === ユーティリティ ===

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

function round(val, decimals) {
  var factor = Math.pow(10, decimals);
  return Math.round(val * factor) / factor;
}

function jsonResponse(data, statusCode) {
  return ContentService
    .createTextOutput(JSON.stringify(data))
    .setMimeType(ContentService.MimeType.JSON);
}
