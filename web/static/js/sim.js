// ============================================================
// Simulation — API接続不可時のデモ表示
// 16秒周期で 5フェーズの走行パターンをループ再生
// ============================================================

const SIM_TICK_MS = 50;
const SIM_CYCLE_S = 16;

export function createSimulation(gauge, updateThrottle, dom, setDot, speedColor, conf) {
  let t = 0;

  function tick() {
    t += 0.02;
    const phase = t % SIM_CYCLE_S;
    let speed, throttle, fuelEco;

    if (phase < 3) {
      const p = phase / 3;
      speed = p * p * 60; throttle = 20 + p * 50;
      fuelEco = 3 + p * 12;
    } else if (phase < 7) {
      speed = 60 + Math.sin(t * 3) * 3;
      throttle = 18 + Math.sin(t * 2) * 4;
      fuelEco = 18 + Math.sin(t * 2) * 3;
    } else if (phase < 10) {
      speed = 45 + Math.sin(t * 2) * 3;
      throttle = 55 + Math.sin(t * 1.5) * 10;
      fuelEco = 10 + Math.sin(t * 1.5) * 2;
    } else if (phase < 13) {
      const p = (phase - 10) / 3;
      speed = 45 + p * 40; throttle = 40 + p * 40;
      fuelEco = 6 + (1 - p) * 5;
    } else {
      const p = (phase - 13) / 3;
      speed = 85 * (1 - p * p); throttle = 3 + (1 - p) * 5;
      fuelEco = 25 + p * 20;
    }

    gauge.update(Math.max(0, speed), speedColor(speed));
    updateThrottle(throttle);

    setDot(dom.obd, 'green');   dom.obd.val.textContent = 'OK';
    setDot(dom.wifi, 'green');  dom.wifi.val.textContent = 'OK';
    setDot(dom.send, 'green');  dom.send.val.textContent = '0';
    setDot(dom.maint, 'green'); dom.maint.val.textContent = '0';
    setDot(dom.trip, 'green');  dom.trip.val.textContent = '12.3';
    setDot(dom.temp, 'green');  dom.temp.val.textContent = '82\u00B0';

    setDot(dom.eco, fuelEco >= conf.eco_kmpl_green ? 'green' : fuelEco >= conf.eco_kmpl_orange ? 'orange' : 'red');
    dom.eco.val.textContent = fuelEco.toFixed(1);
  }

  return {
    start() { setInterval(tick, SIM_TICK_MS); }
  };
}
