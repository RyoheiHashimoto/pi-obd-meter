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
    '項目名', 'タイプ', '進捗(%)', '残り', '要アラート', '超過', '更新日時'
  ]);

  // 既存データをクリアして最新状態で上書き
  if (sheet.getLastRow() > 1) {
    sheet.getRange(2, 1, sheet.getLastRow() - 1, 7).clearContent();
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
      s.name || s.id,
      s.type === 'distance' ? '距離' : '期日',
      round((s.progress || 0) * 100, 0),
      remaining,
      s.needs_alert ? '⚠' : '',
      s.is_overdue ? '🔴' : '',
      now
    ]);
  });

  return jsonResponse({ status: 'ok', type: 'maintenance', count: statuses.length });
}

// === Webダッシュボード HTML ===
function buildDashboardHtml() {
  // データ取得
  var fuelData = getSheetData('給油記録');
  var maintData = getSheetData('メンテ状態');

  // 燃費統計
  var totalDist = 0, totalFuel = 0, fuelCount = 0;
  var recentRows = [];

  if (fuelData.length > 0) {
    fuelCount = fuelData.length;
    fuelData.forEach(function(r) {
      totalDist += r[1] || 0;  // 距離
      totalFuel += r[2] || 0;  // 消費燃料
    });
    // 直近10件（新しい順）
    recentRows = fuelData.slice(-10).reverse();
  }

  var avgEcon = totalFuel > 0 ? round(totalDist / totalFuel, 1) : 0;

  // メンテナンス行
  var maintRows = maintData || [];

  // HTML生成
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
  html += '.stat-row{display:flex;justify-content:space-between;padding:6px 0;border-bottom:1px solid #1a1a24}';
  html += '.stat-row:last-child{border:none}';
  html += '.stat-label{color:#888}';
  html += '.stat-val{color:#fff;font-weight:600}';
  html += '.big-num{font-size:32px;color:#69f0ae;font-weight:700;text-align:center;padding:8px 0}';
  html += '.big-unit{font-size:14px;color:#666;font-weight:400}';
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
  html += '</style></head><body>';

  // ヘッダー
  html += '<h1>DYデミオ ダッシュボード</h1>';
  html += '<div class="sub">更新: ' + Utilities.formatDate(new Date(), 'Asia/Tokyo', 'yyyy/MM/dd HH:mm') + '</div>';

  // 通算燃費
  html += '<div class="card">';
  html += '<h2>⛽ 通算燃費</h2>';
  html += '<div class="big-num">' + avgEcon + ' <span class="big-unit">km/L</span></div>';
  html += '<div class="stat-row"><span class="stat-label">総走行距離</span><span class="stat-val">' + round(totalDist, 0) + ' km</span></div>';
  html += '<div class="stat-row"><span class="stat-label">総消費燃料</span><span class="stat-val">' + round(totalFuel, 1) + ' L</span></div>';
  html += '<div class="stat-row"><span class="stat-label">給油回数</span><span class="stat-val">' + fuelCount + ' 回</span></div>';
  html += '</div>';

  // 直近の給油記録
  if (recentRows.length > 0) {
    html += '<div class="card">';
    html += '<h2>📊 給油履歴</h2>';
    html += '<table><tr><th>日付</th><th>距離</th><th>燃費</th><th>給油量</th></tr>';
    recentRows.forEach(function(r) {
      var dateStr = '';
      try { dateStr = Utilities.formatDate(new Date(r[0]), 'Asia/Tokyo', 'M/d'); } catch(e) { dateStr = '-'; }
      html += '<tr>';
      html += '<td>' + dateStr + '</td>';
      html += '<td>' + round(r[1] || 0, 0) + 'km</td>';
      html += '<td style="color:#69f0ae;font-weight:600">' + round(r[3] || 0, 1) + '</td>';
      html += '<td>' + round(r[4] || 0, 1) + 'L</td>';
      html += '</tr>';
    });
    html += '</table></div>';
  }

  // メンテナンス状態
  if (maintRows.length > 0) {
    html += '<div class="card">';
    html += '<h2>🔧 メンテナンス</h2>';
    maintRows.forEach(function(r) {
      var name = r[0] || '';
      var progress = r[2] || 0;
      var remaining = r[3] || '';
      var needsAlert = r[4] === '⚠';
      var isOverdue = r[5] === '🔴';

      var barClass = 'ok';
      if (isOverdue) barClass = 'danger';
      else if (needsAlert) barClass = 'warn';

      var pct = Math.min(100, progress);
      var nameColor = isOverdue ? '#f44336' : needsAlert ? '#ff9800' : '#ddd';

      html += '<div class="maint-item">';
      html += '<div class="maint-name" style="color:' + nameColor + '">' + name + '</div>';
      html += '<div class="maint-detail">残り ' + remaining + ' (' + progress + '%)</div>';
      html += '<div class="bar-bg"><div class="bar-fg ' + barClass + '" style="width:' + pct + '%"></div></div>';
      html += '</div>';
    });
    html += '</div>';
  }

  html += '</body></html>';
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
    '項目名', 'タイプ', '進捗(%)', '残り', '要アラート', '超過', '更新日時'
  ]);

  Logger.log('セットアップ完了: 給油記録 / メンテ状態 シートを作成しました');
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
