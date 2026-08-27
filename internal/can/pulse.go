package can

// PulsesPerKm は距離パルス (0x420 B1) の1kmあたりのパルス数。
//
// 実測値。オドメーター (0x430 B4-B5) が10km境界を跨いだ区間で積算して求めた。
//
//	独立した7ファイル・17区間の平均   25,681.4 パルス / 10km
//	ばらつき                          25,674〜25,686 (±0.02%)
//	端から端まで (2.7日・25回の再起動をまたぐ)
//	  738,805パルス = 287.7km   対 オドメーター 290km (10km刻みのため真値281〜299)
//
// 日本のVSS標準 637パルス/km のちょうど4倍にあたる (2568.14 ÷ 637 = 4.03)。
const PulsesPerKm = 2568.14

// MetersPerPulse は1パルスあたりの距離 (m)。
const MetersPerPulse = 1000.0 / PulsesPerKm // 0.389387 m

// PulseCounter は 0x420 B1 の 8bit ローリングカウンタから累積距離を求める。
//
// 車速の積分ではなく計数のため、誤差が蓄積しない。
// 車速積分は実測で -0.25% の系統誤差があるのに対し、パルス計数は ±0.02%。
//
// 8bitで一周するため、2.3秒以上読み飛ばすと周回を見失う。
// 115km/h でも 100ms あたり最大11しか進まないため、256に対して23倍の余裕がある。
// can0 の down/up やプロセス再起動をまたぐ場合は Invalidate() を呼び、
// 最初の1サンプルを基準値として捨てること。
type PulseCounter struct {
	prev  uint8
	valid bool
	total uint64
}

// Add はローリングカウンタの現在値を取り込み、累積パルス数を進める。
// 直前の値が無い場合 (初回・Invalidate直後) は基準値として記録するだけで進めない。
func (p *PulseCounter) Add(v uint8) {
	if !p.valid {
		p.prev = v
		p.valid = true
		return
	}
	p.total += uint64(v - p.prev) // uint8 の減算がラップを自然に処理する
	p.prev = v
}

// Invalidate は基準値を破棄する。通信断からの復帰時に呼ぶ。
// 累積値は保持されるため、断絶中に進んだ分だけが失われる。
func (p *PulseCounter) Invalidate() { p.valid = false }

// Valid は基準値を持っているか (＝次の Add で距離が進むか) を返す。
func (p *PulseCounter) Valid() bool { return p.valid }

// Total は累積パルス数を返す。
func (p *PulseCounter) Total() uint64 { return p.total }

// DistanceKm は累積距離 (km) を返す。
func (p *PulseCounter) DistanceKm() float64 { return float64(p.total) / PulsesPerKm }

// SetTotal は累積パルス数を復元する (不揮発からの復帰用)。
func (p *PulseCounter) SetTotal(total uint64) { p.total = total }
