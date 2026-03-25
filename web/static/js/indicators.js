// ============================================================
// Indicators — 右パネルのインジケーター生成と更新
// ============================================================

const INDICATOR_DEFS = [
  { id: 'gear',  label: 'GEAR',  defaultVal: '--' },
  { id: 'eco',   label: 'ECO' },
  { id: 'trip',  label: 'TRIP',  defaultVal: '--' },
  { id: 'temp',  label: 'TEMP',  defaultVal: '--' },
  { id: 'map',   label: 'MAP',   defaultVal: '--' },
  { id: 'maf',   label: 'MAF',   defaultVal: '--' },
  { id: 'o2',    label: 'O2',    defaultVal: '--' },
  { id: 'trim',  label: 'TRIM',  defaultVal: '--' },
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
  // GEAR — ギア + レンジ + HOLD + ロックアップ
  const gear = d.gear || 0;
  const range = d.at_range_str || '?';
  const hold = d.hold || false;
  const tcLocked = d.tc_locked || false;
  if (range === 'P' || range === 'N') {
    dom.gear.val.textContent = range;
    setDot(dom.gear, null);
  } else if (range === 'R') {
    dom.gear.val.textContent = 'R';
    setDot(dom.gear, 'orange');
  } else if (gear >= 1 && gear <= 4) {
    let gearText = String(gear) + range;
    if (hold) gearText += 'H';
    dom.gear.val.textContent = gearText;
    setDot(dom.gear, hold ? 'orange' : 'green');
  } else {
    dom.gear.val.textContent = '--';
    setDot(dom.gear, null);
  }

  // ECO — 数値: 常に平均燃費、ドット: 瞬間燃費で色判定
  const eco = d.fuel_economy || 0;
  const avgEco = Math.min(d.avg_fuel_economy || 0, 99.9);
  if (avgEco > 0.1) {
    dom.eco.val.textContent = avgEco.toFixed(1);
  } else {
    dom.eco.val.textContent = '--';
  }
  if (eco < 0) {
    setDot(dom.eco, 'green');
  } else if (eco < 0.1) {
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
    setDot(dom.map, mapVal < 35 ? 'green' : null);
  } else {
    dom.map.val.textContent = '--';
    setDot(dom.map, null);
  }

  // MAF
  const maf = d.maf_airflow || 0;
  if (maf > 0) {
    dom.maf.val.textContent = maf.toFixed(1);
    setDot(dom.maf, 'green');
  } else {
    dom.maf.val.textContent = '--';
    setDot(dom.maf, null);
  }

  // O2
  const o2 = d.o2_voltage;
  if (o2 != null && o2 > 0) {
    dom.o2.val.textContent = o2.toFixed(2) + 'V';
    // 0.45V が理論空燃比の境目。<0.45=リーン、>0.45=リッチ
    setDot(dom.o2, o2 < 0.3 ? 'orange' : o2 > 0.7 ? 'red' : 'green');
  } else {
    dom.o2.val.textContent = '--';
    setDot(dom.o2, null);
  }

  // TRIM (短期燃料トリム)
  const trim = d.short_fuel_trim;
  if (trim != null) {
    dom.trim.val.textContent = (trim >= 0 ? '+' : '') + trim.toFixed(0) + '%';
    setDot(dom.trim, Math.abs(trim) < 10 ? 'green' : Math.abs(trim) < 20 ? 'orange' : 'red');
  } else {
    dom.trim.val.textContent = '--';
    setDot(dom.trim, null);
  }
}
