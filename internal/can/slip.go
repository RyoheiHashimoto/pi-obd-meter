package can

import "sync"

// DefaultSpeedRatioK は校正定数 k の初期値。
//
//	k = エンジン回転数 / (車速 km/h × 機械ギア比)
//
// ロックアップ中は engine = タービン回転数なので、この式が厳密に成り立つ。
// k には最終減速比とタイヤ周長が畳み込まれている:
//
//	k = (1000/60) / タイヤ周長[m] × 最終減速比
//
// 初期値 38.42 は 2026-08-27 の実走 (ロックアップ 88サンプル) から求めた。
// 純正 175/65R14 (周長 1.832 m) と最終減速比 4.23 に相当する。
const DefaultSpeedRatioK = 38.42

// SlipCalibrator はトルコンの滑り比を rpm と車速から求める。
//
// 0x230 B2 のギア比には滑りが含まれないため (DecodeATCtrl のコメント参照)、
// 滑りは物理量から計算するしかない。従来この方式はタイヤ周長と最終減速比を
// 定数で持っていて、実測とズレて滑り比が systematically 低く出ていた
// (ロック中に 96.71%、σ=11)。本実装はロックアップ中のサンプルから k を
// 自動校正するため、タイヤの摩耗や銘柄変更でも狂わない。
//
// 実測での分離性能 (2026-08-27, 238サンプル):
//
//	ロックアップ中 平均 0.999 (0.931〜1.096)
//	解放中         平均 1.141 (0.616〜2.024)
type SlipCalibrator struct {
	mu sync.Mutex
	k  float64
	n  int
}

// NewSlipCalibrator は初期値 DefaultSpeedRatioK で校正器を作る。
func NewSlipCalibrator() *SlipCalibrator {
	return &SlipCalibrator{k: DefaultSpeedRatioK}
}

// 校正の重み。1サンプル 0.5秒として時定数はおよそ100秒。
const slipCalAlpha = 0.005

// Observe はロックアップ係合中のサンプルを与えて k を更新する。
// 呼び出し側は 0x231 の TCロックフラグが立ち、かつ十分な車速があるときだけ
// 呼ぶこと。変速の過渡や低速では滑りが残り、校正が汚れる。
func (c *SlipCalibrator) Observe(rpm, speedKmh, mechRatio float64) {
	if c == nil || speedKmh <= 0 || mechRatio <= 0 || rpm <= 0 {
		return
	}
	k := rpm / (speedKmh * mechRatio)
	// 初期値から大きく外れた値は変速過渡やノイズとみなして捨てる。
	if k < DefaultSpeedRatioK*0.7 || k > DefaultSpeedRatioK*1.4 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.k = c.k*(1-slipCalAlpha) + k*slipCalAlpha
	c.n++
}

// K は現在の校正定数を返す。
func (c *SlipCalibrator) K() float64 {
	if c == nil {
		return DefaultSpeedRatioK
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.k
}

// Slip はトルコンの滑り比 (エンジン回転 / タービン回転) を返す。
// 1.0 = 滑りゼロ = 完全ロックアップ。値が大きいほど滑っている。
// 計算できない条件では ok=false を返す。
func (c *SlipCalibrator) Slip(rpm, speedKmh, mechRatio float64) (float64, bool) {
	if speedKmh <= 0 || mechRatio <= 0 || rpm <= 0 {
		return 0, false
	}
	slip := rpm / (speedKmh * mechRatio * c.K())
	if slip <= 0 {
		return 0, false
	}
	return slip, true
}

// LockPct は滑り比をロック率 (%) に直す。100% = 完全ロック。
func (c *SlipCalibrator) LockPct(rpm, speedKmh, mechRatio float64) float64 {
	slip, ok := c.Slip(rpm, speedKmh, mechRatio)
	if !ok {
		return 0
	}
	pct := 100.0 / slip
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return pct
}
