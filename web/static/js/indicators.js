// ============================================================
// Indicators — 右パネルのインジケーター生成と更新
// ============================================================

const ECO_LOW_SPEED_THRESHOLD = 30;

const INDICATOR_DEFS = [
  { id: 'eco',   label: 'ECO' },
  { id: 'map',   label: 'MAP',   defaultVal: '--' },
  { id: 'trip',  label: 'TRIP',  defaultVal: '--' },
  { id: 'temp',  label: 'TEMP',  defaultVal: '--' },
  { id: 'maint', label: 'MAINT' },
  { id: 'send',  label: 'SEND' },
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

  // SEND
  const pending = d.pending_count || 0;
  const sending = d.send_sending || false;
  if (pending > 0) {
    setDot(dom.send, 'red');
    dom.send.val.textContent = String(pending);
  } else if (sending) {
    setDot(dom.send, 'orange');
    dom.send.val.textContent = '...';
  } else {
    setDot(dom.send, 'green');
    dom.send.val.textContent = 'OK';
  }

  // MAINT
  const alerts = d.alerts || [];
  const hasOverdue = alerts.some(a => a.is_overdue);
  setDot(dom.maint, hasOverdue ? 'red' : alerts.length > 0 ? 'orange' : 'green');
  dom.maint.val.textContent = String(alerts.length);

  // ECO — エンブレ/停車/クリープ時は平均燃費(▸付き)、走行中は瞬間燃費
  const eco = d.fuel_economy || 0;
  const avgEco = Math.min(d.avg_fuel_economy || 0, 99.9);
  const fuelRate = d.fuel_rate_lh || 0;
  const speed = d.speed_kmh || 0;
  if (eco < 0) {
    // エンブレ・燃料カット: 平均燃費を表示
    if (avgEco > 0.1) {
      setDot(dom.eco, 'green');
      dom.eco.val.textContent = avgEco.toFixed(1) + '\u25B8';
    } else {
      setDot(dom.eco, 'green');
      dom.eco.val.textContent = '--';
    }
  } else if (eco < 0.1) {
    // 停車・クリープ: 平均燃費を表示
    if (avgEco > 0.1) {
      if (avgEco >= conf.eco_kmpl_green)      setDot(dom.eco, 'green');
      else if (avgEco >= conf.eco_kmpl_orange) setDot(dom.eco, 'orange');
      else                                     setDot(dom.eco, 'red');
      dom.eco.val.textContent = avgEco.toFixed(1) + '\u25B8';
    } else {
      setDot(dom.eco, null);
      dom.eco.val.textContent = '0';
    }
  } else if (eco > 30) {
    // 高燃費（エンブレ等の検出漏れ）: 平均燃費を表示
    if (avgEco > 0.1) {
      setDot(dom.eco, 'green');
      dom.eco.val.textContent = avgEco.toFixed(1) + '\u25B8';
    } else {
      setDot(dom.eco, 'green');
      dom.eco.val.textContent = '--';
    }
  } else if (speed < ECO_LOW_SPEED_THRESHOLD && fuelRate > 0) {
    if (fuelRate < conf.eco_lh_green)      setDot(dom.eco, 'green');
    else if (fuelRate < conf.eco_lh_red)   setDot(dom.eco, 'orange');
    else                                   setDot(dom.eco, 'red');
    dom.eco.val.textContent = eco.toFixed(1);
  } else {
    if (eco >= conf.eco_kmpl_green)      setDot(dom.eco, 'green');
    else if (eco >= conf.eco_kmpl_orange) setDot(dom.eco, 'orange');
    else                                  setDot(dom.eco, 'red');
    dom.eco.val.textContent = eco.toFixed(1);
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

  // MAP (Intake Manifold Absolute Pressure)
  const mapKPa = d.intake_map || 0;
  if (mapKPa > 0) {
    dom.map.val.textContent = Math.round(mapKPa);
    // 大気圧101kPa基準: 低い=バキューム(エンブレ/アイドル)、高い=高負荷
    if (mapKPa < 35)       setDot(dom.map, 'blue');   // 強い負圧（エンブレ）
    else if (mapKPa < 60)  setDot(dom.map, 'green');  // 巡航
    else if (mapKPa < 85)  setDot(dom.map, 'orange'); // 高負荷
    else                   setDot(dom.map, 'red');     // ほぼ全開
  } else {
    dom.map.val.textContent = '--';
    setDot(dom.map, null);
  }

  // TEMP
  const ct = d.coolant_temp || 0;
  if (ct > 0) {
    dom.temp.val.textContent = Math.round(ct) + '\u00B0';
    setDot(dom.temp, ct < 70 ? 'orange' : ct < 105 ? 'green' : 'red');
  } else {
    dom.temp.val.textContent = '--';
    setDot(dom.temp, null);
  }
}
