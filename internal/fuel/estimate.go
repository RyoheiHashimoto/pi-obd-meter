package fuel

import (
	"encoding/json"
	"math"
	"os"
	"sync"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/atomicfile"
)

const (
	// EstimateTauSec は推定値を燃料計へ寄せる時定数 (秒)。
	//
	// 燃料計の生値はスロッシングで大きく振れる。2026-09-07/08 の実走ログ
	// (停車38回) では、停車して5秒以内に航続距離換算で 10〜25km、30秒以上
	// 止まっていても 3〜6km 振れた。一方、全サンプルを5分平均すると
	// ±0.5〜0.7pt に収まる。減速中は −1.0〜−1.55pt、加速中は +0.6pt 偏るが、
	// 長く平均すれば打ち消し合う。
	//
	// 10分なら揺れは表示に出ず、燃料積算の誤差 (レシート比 ±3.5%) は走り
	// ながら燃料計に引き戻される。同じログで 300〜1200秒を模擬したところ、
	// 1秒あたりの表示の変化は最大 0.1〜0.3km で大差なかった (旧方式は 13.7〜21.1km)。
	EstimateTauSec = 600.0

	// estimateSyncL は起動時に燃料計の値へ合わせ直す差 (L)。
	//
	// 給油検出と同じ 10pt にそろえる。停車直後の1秒平均の揺れは 1〜2.5L
	// (航続距離換算 10〜25km) なので、信号待ちで再起動しても合わせ直さない。
	estimateSyncL = DetectThresholdPt * LitersPerPoint

	// estimateMaxDtSec を超える周期は通信断などの空白とみなし、燃料計へ寄せない。
	// 1回で大きく寄せると、その瞬間の揺れた値に引っ張られる。
	estimateMaxDtSec = 10.0

	// 保存の間引き。燃料残量は分単位でしか動かず、書き込みはそのまま
	// 電源断時の破損リスクになる (Detector と同じ理由)。
	// 0.05L は航続距離で約 0.5km。電源断で失っても燃料計が引き戻す。
	estimateSaveInterval  = 30 * time.Second
	estimateSaveMinDeltaL = 0.05
)

// Estimator は航続距離の計算に使う燃料残量 (L) を推定する。
//
// 燃料計の値をそのまま使うと、停車のたびにスロッシングで航続距離が跳ねる。
// 以前は停車中だけ直近1秒の平均を取り直していたため (#188 の実装)、
//
//	停車直後に 10〜25km 振れる
//	走行中は値が止まり、使った燃料の分が減らない (平均燃費の変化で増えることもある)
//	発進直前の揺れた値のまま、次の停車まで固定される
//
// が起きていた。2026-09-13 のライブ記録でも、1回の停車で 18km 跳んだ。
//
// そこで量産車と同じく、燃料の消費量で減らし、燃料計へはゆっくり寄せる
// (日産 US8260534B2、Deutz US9389112B2 も消費量を主、センダーを従にしている)。
// 燃料計の値へ一気に合わせるのは、起動して最初に落ち着いた値が推定値から
// 大きく離れていたとき (給油) だけにする。
//
// 推定値は不揮発に保存し、再起動したら保存値から続ける。走行中や信号待ちで
// 再起動したときに、揺れている燃料計の値で初期化しないためである。起動時の
// センサー値で初期化する方式は、坂での始動や停車直後の再起動で誤った値が
// 長く残る (Scania の燃料推定の修士論文が弱点として挙げている)。
//
// エンジンをかけたまま給油した場合は起動を経ないので一気には合わせない。
// 燃料計への寄せで 10〜30分かけて追いつく。
type Estimator struct {
	mu        sync.Mutex
	statePath string
	tankL     float64
	now       func() time.Time

	liters float64
	valid  bool
	synced bool // この起動で燃料計と突き合わせ済みか

	lastSaved  float64
	lastSaveAt time.Time
}

type estimateState struct {
	Liters  float64   `json:"liters"`
	SavedAt time.Time `json:"saved_at"`
}

// NewEstimator は保存済みの推定値を読み込んで推定器を作る。
// tankL はタンク容量 (L)。推定値はこれを超えない。0 以下なら上限を設けない。
func NewEstimator(statePath string, tankL float64) *Estimator {
	e := &Estimator{statePath: statePath, tankL: tankL, now: time.Now}
	e.lastSaveAt = e.now()
	if b, err := os.ReadFile(statePath); err == nil {
		var s estimateState
		if json.Unmarshal(b, &s) == nil && s.Liters > 0 {
			e.liters = e.clamp(s.Liters)
			e.valid = true
			e.lastSaved = e.liters
		}
	}
	return e
}

// Update は1周期分を取り込む。
//
//	levelPt   燃料計の生値 (0x430 B0 / 2.55)。0 以下は未取得として無視する
//	burnL     この周期に使った燃料 (L)。燃料カット中は 0
//	dtSec     この周期の長さ (秒)
//	settledL  起動後に落ち着いた燃料残量 (Detector.SettledLiters)。まだなら 0
//
// 燃料計の値は走行中も停車中も使う。どちらも時定数 EstimateTauSec で平均される。
func (e *Estimator) Update(levelPt, burnL, dtSec, settledL float64) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	// 起動につき1回、最初に落ち着いた値と突き合わせる。
	if !e.synced && settledL > 0 {
		e.synced = true
		switch {
		case !e.valid:
			// 保存値が無い (初回)。燃料計の値から始めるしかない。
			e.liters = e.clamp(settledL)
			e.valid = true
			e.save()
		case math.Abs(settledL-e.liters) >= estimateSyncL:
			// 給油した、または保存値が大きく外れている。
			e.liters = e.clamp(settledL)
			e.save()
		}
	}
	if !e.valid {
		return
	}

	if burnL > 0 {
		e.liters -= burnL
	}
	if levelPt > 0 && dtSec > 0 && dtSec <= estimateMaxDtSec {
		e.liters += (levelPt*LitersPerPoint - e.liters) * (dtSec / EstimateTauSec)
	}
	e.liters = e.clamp(e.liters)

	if e.now().Sub(e.lastSaveAt) >= estimateSaveInterval &&
		math.Abs(e.liters-e.lastSaved) >= estimateSaveMinDeltaL {
		e.save()
	}
}

// Liters は推定残量 (L) を返す。まだ推定できていなければ 0。
func (e *Estimator) Liters() float64 {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.valid {
		return 0
	}
	return e.liters
}

// Save は推定値を保存する。終了時に呼ぶ。
func (e *Estimator) Save() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.valid {
		e.save()
	}
}

func (e *Estimator) clamp(l float64) float64 {
	if l < 0 {
		return 0
	}
	if e.tankL > 0 && l > e.tankL {
		return e.tankL
	}
	return l
}

func (e *Estimator) save() {
	e.lastSaved = e.liters
	e.lastSaveAt = e.now()
	if e.statePath == "" {
		return
	}
	b, err := json.Marshal(estimateState{Liters: e.liters, SavedAt: e.lastSaveAt})
	if err != nil {
		return
	}
	_ = atomicfile.Write(e.statePath, b, 0644)
}
