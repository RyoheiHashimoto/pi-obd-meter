// ============================================================
// Indicators — 右パネルのインジケーター生成と更新
// ============================================================

const INDICATOR_DEFS = [
  { id: 'eco',   label: 'ECO' },
  { id: 'trip',  label: 'TRIP',  defaultVal: '--' },
  { id: 'temp',  label: 'TEMP',  defaultVal: '--' },
  { id: 'map',   label: 'MAP',   defaultVal: '--' },
  { id: 'load',  label: 'LOAD',  defaultVal: '--' },
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
    setDot(dom.temp, ct < 60 ? 'orange' : ct < 105 ? 'green' : 'red');
  } else {
    dom.temp.val.textContent = '--';
    setDot(dom.temp, null);
  }

  // MAP
  const mapVal = d.intake_map || 0;
  if (mapVal > 0) {
    dom.map.val.textContent = Math.round(mapVal);
    setDot(dom.map, mapVal < 35 ? 'green' : mapVal < 80 ? 'orange' : 'red');
  } else {
    dom.map.val.textContent = '--';
    setDot(dom.map, null);
  }

  // LOAD
  const loadVal = d.engine_load_pct || 0;
  if (d.obd_connected) {
    dom.load.val.textContent = Math.round(loadVal) + '%';
    setDot(dom.load, loadVal < 60 ? 'green' : loadVal < 85 ? 'orange' : 'red');
  } else {
    dom.load.val.textContent = '--';
    setDot(dom.load, null);
  }

}
