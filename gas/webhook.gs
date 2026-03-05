/**
 * DYデミオ 燃費メーター — Google Apps Script
 * 
 * セットアップ手順:
 * 1. Google Sheetsで新しいスプレッドシートを作成
 * 2. シート名を「トリップ」「給油記録」「メンテナンス」「ダッシュボード」に変更
 * 3. 拡張機能 → Apps Script を開く
 * 4. このコードを貼り付け
 * 5. DISCORD_WEBHOOK_URL を設定（不要なら空文字のまま）
 * 6. デプロイ → 新しいデプロイ → ウェブアプリ
 *    - 実行するユーザー: 自分
 *    - アクセスできるユーザー: 全員
 * 7. 表示されたURLをラズパイの config.json の webhook_url に設定
 */

// === 設定 ===
const DISCORD_WEBHOOK_URL = ''; // Discord Webhook URL（空なら通知しない）

// === Webhook エンドポイント ===
function doPost(e) {
  try {
    const payload = JSON.parse(e.postData.contents);
    
    switch (payload.type) {
      case 'trip':
        return handleTrip(payload.data);
      default:
        return jsonResponse({ error: '不明なtype: ' + payload.type }, 400);
    }
  } catch (err) {
    return jsonResponse({ error: err.message }, 500);
  }
}

// テスト用GETエンドポイント
function doGet(e) {
  return jsonResponse({ status: 'ok', message: 'DYデミオ燃費メーター Webhook' });
}

// === トリップデータ処理 ===
function handleTrip(data) {
  const sheet = getOrCreateSheet('トリップ', [
    '日時', 'トリップID', '距離(km)', '燃料(L)', '平均燃費(km/L)',
    '最高速度(km/h)', '平均速度(km/h)', '走行時間(分)', 'アイドル(分)'
  ]);
  
  const drivingMin = Math.round((data.driving_time_sec || 0) / 60);
  const idleMin = Math.round((data.idle_time_sec || 0) / 60);
  
  sheet.appendRow([
    new Date(data.start_time || Date.now()),
    data.trip_id || '',
    round(data.distance_km, 1),
    round(data.fuel_used_l, 2),
    round(data.avg_fuel_econ || 0, 1),
    round(data.max_speed_kmh || 0, 0),
    round(data.avg_speed_kmh || 0, 0),
    drivingMin,
    idleMin
  ]);
  
  // Discord通知
  if (DISCORD_WEBHOOK_URL) {
    sendDiscord(
      '🚗 トリップ完了',
      `距離: **${round(data.distance_km, 1)} km** | ` +
      `燃費: **${round(data.avg_fuel_econ || 0, 1)} km/L** | ` +
      `燃料: **${round(data.fuel_used_l, 2)} L** | ` +
      `最高速度: ${round(data.max_speed_kmh || 0, 0)} km/h | ` +
      `走行: ${drivingMin}分`
    );
  }
  
  // ダッシュボード更新
  updateDashboard();
  
  return jsonResponse({ status: 'ok', trip_id: data.trip_id });
}

// === ダッシュボード自動更新 ===
function updateDashboard() {
  const tripSheet = SpreadsheetApp.getActiveSpreadsheet().getSheetByName('トリップ');
  const fuelSheet = SpreadsheetApp.getActiveSpreadsheet().getSheetByName('給油記録');
  const dashSheet = getOrCreateSheet('ダッシュボード', []);
  
  // ダッシュボードをクリアして再構築
  dashSheet.clear();
  
  const bold = SpreadsheetApp.newTextStyle().setBold(true).build();
  const headerStyle = SpreadsheetApp.newTextStyle().setBold(true).setFontSize(12).build();
  
  let row = 1;
  
  // ヘッダー
  dashSheet.getRange(row, 1).setValue('DYデミオ 燃費ダッシュボード')
    .setTextStyle(SpreadsheetApp.newTextStyle().setBold(true).setFontSize(14).build());
  dashSheet.getRange(row, 3).setValue('更新: ' + Utilities.formatDate(new Date(), 'Asia/Tokyo', 'yyyy/MM/dd HH:mm'));
  row += 2;
  
  // --- トリップ統計 ---
  if (tripSheet && tripSheet.getLastRow() > 1) {
    const tripData = tripSheet.getRange(2, 1, tripSheet.getLastRow() - 1, 9).getValues();
    
    let totalDist = 0, totalFuel = 0, maxSpeed = 0, tripCount = 0;
    let last30Dist = 0, last30Fuel = 0;
    const thirtyDaysAgo = new Date();
    thirtyDaysAgo.setDate(thirtyDaysAgo.getDate() - 30);
    
    tripData.forEach(r => {
      const dist = r[2] || 0;
      const fuel = r[3] || 0;
      const spd = r[5] || 0;
      if (dist > 0) {
        totalDist += dist;
        totalFuel += fuel;
        tripCount++;
        if (spd > maxSpeed) maxSpeed = spd;
        
        if (new Date(r[0]) > thirtyDaysAgo) {
          last30Dist += dist;
          last30Fuel += fuel;
        }
      }
    });
    
    dashSheet.getRange(row, 1).setValue('📊 走行統計').setTextStyle(headerStyle);
    row++;
    const stats = [
      ['総トリップ数', tripCount + ' 回'],
      ['総走行距離', round(totalDist, 1) + ' km'],
      ['総燃料消費', round(totalFuel, 1) + ' L'],
      ['通算燃費', totalFuel > 0 ? round(totalDist / totalFuel, 1) + ' km/L' : '---'],
      ['直近30日燃費', last30Fuel > 0 ? round(last30Dist / last30Fuel, 1) + ' km/L' : '---'],
      ['最高速度', maxSpeed + ' km/h'],
    ];
    stats.forEach(s => {
      dashSheet.getRange(row, 1).setValue(s[0]).setTextStyle(bold);
      dashSheet.getRange(row, 2).setValue(s[1]);
      row++;
    });
    row++;
  }
  
  // --- 給油統計 ---
  if (fuelSheet && fuelSheet.getLastRow() > 1) {
    const fuelData = fuelSheet.getRange(2, 1, fuelSheet.getLastRow() - 1, 9).getValues();
    
    let totalCost = 0, totalLiters = 0, fuelCount = 0;
    fuelData.forEach(r => {
      totalCost += r[4] || 0;   // 合計金額
      totalLiters += r[2] || 0; // 給油量
      fuelCount++;
    });
    
    dashSheet.getRange(row, 1).setValue('⛽ 給油統計').setTextStyle(headerStyle);
    row++;
    const fuelStats = [
      ['給油回数', fuelCount + ' 回'],
      ['総給油量', round(totalLiters, 1) + ' L'],
      ['総給油費用', '¥' + Math.round(totalCost).toLocaleString()],
      ['平均単価', fuelCount > 0 ? '¥' + round(totalCost / totalLiters, 1) + '/L' : '---'],
    ];
    fuelStats.forEach(s => {
      dashSheet.getRange(row, 1).setValue(s[0]).setTextStyle(bold);
      dashSheet.getRange(row, 2).setValue(s[1]);
      row++;
    });
  }
  
  // 列幅調整
  dashSheet.setColumnWidth(1, 160);
  dashSheet.setColumnWidth(2, 200);
}

// === 給油記録シートの初期化（手入力用） ===
function initFuelSheet() {
  const sheet = getOrCreateSheet('給油記録', [
    '日付', 'ODO(km)', '給油量(L)', '単価(円/L)', '合計(円)',
    '満タン', '燃費(km/L)', 'スタンド', 'メモ'
  ]);
  
  // 燃費の自動計算式（満タン法）をヘルプとして2行目に入れる
  if (sheet.getLastRow() <= 1) {
    sheet.getRange(2, 7).setFormula(
      '=IF(AND(F2="○",B2>0), (B2 - IFERROR(VLOOKUP("○",SORT(FILTER(B$2:B,F$2:F="○",ROW(B$2:B)<ROW(B2)),1,FALSE),1,FALSE),0)) / C2, "")'
    );
    sheet.getRange(2, 7).setNote(
      '満タン法燃費: 前回満タン時のODOとの差 ÷ 今回給油量\n' +
      '満タン列に○を入れると自動計算されます\n' +
      'この行をコピーして使ってください'
    );
  }
  
  return sheet;
}

// === メンテナンスシートの初期化（手入力用） ===
function initMaintenanceSheet() {
  const sheet = getOrCreateSheet('メンテナンス', [
    '日付', 'ODO(km)', 'カテゴリ', '内容', '費用(円)', '店名', 'メモ'
  ]);
  
  // カテゴリのデータバリデーション
  if (sheet.getLastRow() <= 1) {
    const categories = [
      'エンジンオイル', 'オイルフィルター', 'エアフィルター',
      'エアコンフィルター', 'タイヤローテーション', 'タイヤ交換',
      'ブレーキパッド', 'ブレーキフルード', 'クーラント', 'ATF',
      'スパークプラグ', 'バッテリー', 'ワイパー',
      '12ヶ月点検', '車検', '修理', 'その他'
    ];
    const rule = SpreadsheetApp.newDataValidation()
      .requireValueInList(categories, true)
      .setAllowInvalid(true)
      .build();
    sheet.getRange('C2:C1000').setDataValidation(rule);
  }
  
  return sheet;
}

// === 初期セットアップ（1回だけ実行） ===
function setup() {
  initFuelSheet();
  initMaintenanceSheet();
  getOrCreateSheet('トリップ', [
    '日時', 'トリップID', '距離(km)', '燃料(L)', '平均燃費(km/L)',
    '最高速度(km/h)', '平均速度(km/h)', '走行時間(分)', 'アイドル(分)'
  ]);
  updateDashboard();
  
  SpreadsheetApp.getUi().alert(
    'セットアップ完了\n\n' +
    '1. デプロイ → 新しいデプロイ → ウェブアプリ\n' +
    '2. URLをラズパイの config.json に設定\n' +
    '3. 給油記録とメンテナンスは各シートに直接入力'
  );
}

// === ユーティリティ ===

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

function jsonResponse(data, statusCode) {
  return ContentService
    .createTextOutput(JSON.stringify(data))
    .setMimeType(ContentService.MimeType.JSON);
}

function sendDiscord(title, description) {
  if (!DISCORD_WEBHOOK_URL) return;
  
  try {
    UrlFetchApp.fetch(DISCORD_WEBHOOK_URL, {
      method: 'post',
      contentType: 'application/json',
      payload: JSON.stringify({
        embeds: [{
          title: title,
          description: description,
          color: 0x42a5f5,
          timestamp: new Date().toISOString()
        }]
      })
    });
  } catch (err) {
    Logger.log('Discord通知エラー: ' + err.message);
  }
}
