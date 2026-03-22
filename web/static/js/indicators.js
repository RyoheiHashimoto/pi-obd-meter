// ============================================================
// Indicators — 右パネルのインジケーター生成と更新
// ============================================================

const INDICATOR_DEFS = [
  { id: 'eco',   label: 'ECO' },
  { id: 'trip',  label: 'TRIP',  defaultVal: '--' },
  { id: 'temp',  label: 'TEMP',  defaultVal: '--' },
  { id: 'maint', label: 'MAINT' },
  { id: 'wifi',  label: 'WiFi' },
  { id: 'obd',   label: 'OBD' },
];

// インジケーター行を生成してパネルに追加し、DOM参照を返す
export function createIndicators(panelEl) {
  const dom = {};
  for (const { id, label, defaultVal } of INDICATOR_DEFS) {
    const row = document.createElement('div');
    row.className = 'ind-row';
    row.innerHTML =
      `<div class="ind-dot"></div>` +
      `<span class="ind-label">${label}</span>` +
      `<span class="ind-val">${defaultVal || ''}</span>`;
    panelEl.appendChild(row);
    dom[id] = {
      dot: row.querySelector('.ind-dot'),
      val: row.querySelector('.ind-val'),
    };
  }
  dom.toast = document.getElementById('toast');
  return dom;
}

// ドット色を設定
export function setDot(indicator, colorClass) {
  indicator.dot.className = colorClass ? `ind-dot on-${colorClass}` : 'ind-dot';
}

// 全インジケーターを更新
export function updateIndicators(dom, d, conf) {
  // OBD
  const obdOk = d.obd_connected;
  setDot(dom.obd, obdOk ? 'green' : 'red');
  dom.obd.val.textContent = obdOk ? 'OK' : 'NG';

  // WiFi
  const wifiOk = d.wifi_connected;
  setDot(dom.wifi, wifiOk ? 'green' : 'red');
  dom.wifi.val.textContent = wifiOk ? 'OK' : 'NG';

  // MAINT
  const alerts = d.alerts || [];
  const hasOverdue = alerts.some(a => a.is_overdue);
  setDot(dom.maint, hasOverdue ? 'red' : alerts.length > 0 ? 'orange' : 'green');
  dom.maint.val.textContent = String(alerts.length);

  // ECO — 数値: 常に平均燃費、ドット: 瞬間燃費で色判定
  const eco = d.fuel_economy || 0;
  const avgEco = Math.min(d.avg_fuel_economy || 0, 99.9);
  // 数値: 平均燃費を常時表示
  if (avgEco > 0.1) {
    dom.eco.val.textContent = avgEco.toFixed(1);
  } else {
    dom.eco.val.textContent = '--';
  }
  // ドット: 瞬間燃費で色判定（緑=エンブレのみ、黄>=6、赤<6）
  if (eco < 0) {
    // エンブレ（燃料カット）= 緑
    setDot(dom.eco, 'green');
  } else if (eco < 0.1) {
    // 停車・アイドル = 判定なし
    setDot(dom.eco, null);
  } else if (eco >= conf.eco_kmpl_orange) {
    setDot(dom.eco, 'orange');
  } else {
    setDot(dom.eco, 'red');
  }

  // TRIP
  const tripKm = d.trip_km || 0;
  if (tripKm >= 0.1) {
    dom.trip.val.textContent = tripKm.toFixed(1);
    setDot(dom.trip, tripKm < conf.trip_warn_km ? 'green' : tripKm <= conf.trip_danger_km ? 'orange' : 'red');
  } else {
    dom.trip.val.textContent = '0';
    setDot(dom.trip, 'green');
  }

  // TEMP
  const ct = d.coolant_temp || 0;
  if (ct > 0) {
    dom.temp.val.textContent = Math.round(ct) + '\u00B0';
    setDot(dom.temp, ct < 60 ? 'orange' : ct < 100 ? 'green' : 'red');
  } else {
    dom.temp.val.textContent = '--';
    setDot(dom.temp, null);
  }
}
