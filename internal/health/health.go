// Package health は Raspberry Pi 本体の健全性を読み取る。
//
// 車載機は毎回エンジン停止で電源を失うため、電圧降下と不正終了が蓄積する。
// 症状が出てから調べるのでは遅いので、常時記録して傾向を見る。
package health

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/atomicfile"
)

// throttled レジスタのビット。Raspberry Pi のファームウェアが返す。
// 下位16bitが「今」、上位16bitが「起動してから一度でも」を表す。
const (
	bitUnderVoltageNow  = 1 << 0
	bitFreqCappedNow    = 1 << 1
	bitThrottledNow     = 1 << 2
	bitUnderVoltageEver = 1 << 16
	bitFreqCappedEver   = 1 << 17
	bitThrottledEver    = 1 << 18
)

// Status は Pi の健全性を表す。
type Status struct {
	SoCTempC float64 `json:"soc_temp_c"`

	// 電圧降下。車載では最も重要。セル始動時やオルタ不調で出る。
	UnderVoltageNow  bool `json:"under_voltage_now"`
	UnderVoltageEver bool `json:"under_voltage_ever"`

	// 熱による性能制限。夏のダッシュボード上では起こりうる。
	ThrottledNow  bool `json:"throttled_now"`
	ThrottledEver bool `json:"throttled_ever"`
	FreqCappedNow bool `json:"freq_capped_now"`

	// SDへの書き込み量。寿命の目安になる。
	DiskWrittenGB float64 `json:"disk_written_gb"`

	// 不正終了の累計。エンジン停止のたびに増えるのが正常な状態。
	// 増え方が異常なら、走行中に電源が落ちている疑いがある。
	UncleanShutdowns int `json:"unclean_shutdowns"`
	BootCount        int `json:"boot_count"`

	UptimeSec int64 `json:"uptime_sec"`
}

// Alert は注意すべき状態を短い日本語で返す。何も無ければ空文字列。
func (s Status) Alert() string {
	switch {
	case s.UnderVoltageNow:
		return "電圧低下"
	case s.ThrottledNow:
		return "高温で制限中"
	case s.SoCTempC >= 80:
		return "SoC高温"
	case s.UnderVoltageEver:
		return "電圧低下の履歴あり"
	}
	return ""
}

// state は再起動をまたいで保つ情報。
//
// journald を RAM 運用にしたため起動履歴が残らない。不正終了を数えるには
// 別の仕掛けが要る。起動時に running を立て、正常終了時に降ろす。
// 起動時に running が立ったままなら、前回は不正終了だったと分かる。
type state struct {
	Running          bool      `json:"running"`
	BootCount        int       `json:"boot_count"`
	UncleanShutdowns int       `json:"unclean_shutdowns"`
	LastBootAt       time.Time `json:"last_boot_at"`
}

// Monitor は健全性の読み取りと不正終了の計数を行う。
type Monitor struct {
	mu        sync.Mutex
	statePath string
	st        state
}

// NewMonitor は状態ファイルを読み、今回の起動を記録する。
// 前回 running が立ったままなら不正終了として数える。
func NewMonitor(statePath string) *Monitor {
	m := &Monitor{statePath: statePath}
	if b, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(b, &m.st)
	}
	if m.st.Running {
		m.st.UncleanShutdowns++
	}
	m.st.Running = true
	m.st.BootCount++
	m.st.LastBootAt = time.Now()
	m.save()
	return m
}

// MarkCleanShutdown は正常終了を記録する。次回起動で不正終了と数えられなくなる。
func (m *Monitor) MarkCleanShutdown() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.Running = false
	m.save()
}

// save は呼び出し側でロック済みか、初期化中であることを前提とする。
func (m *Monitor) save() {
	b, err := json.MarshalIndent(m.st, "", "  ")
	if err != nil {
		return
	}
	_ = atomicfile.Write(m.statePath, b, 0o644)
}

// Status は現在の健全性を返す。
func (m *Monitor) Status() Status {
	s := Status{
		SoCTempC:      readSoCTemp(),
		DiskWrittenGB: readDiskWrittenGB(),
		UptimeSec:     readUptimeSec(),
	}
	if raw, ok := readThrottled(); ok {
		s.UnderVoltageNow = raw&bitUnderVoltageNow != 0
		s.FreqCappedNow = raw&bitFreqCappedNow != 0
		s.ThrottledNow = raw&bitThrottledNow != 0
		s.UnderVoltageEver = raw&bitUnderVoltageEver != 0
		s.ThrottledEver = raw&(bitThrottledEver|bitFreqCappedEver) != 0
	}
	if m != nil {
		m.mu.Lock()
		s.UncleanShutdowns = m.st.UncleanShutdowns
		s.BootCount = m.st.BootCount
		m.mu.Unlock()
	}
	return s
}

// readSoCTemp は SoC の温度を℃で返す。取れなければ 0。
func readSoCTemp() float64 {
	b, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0
	}
	milli, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0
	}
	return milli / 1000.0
}

// readThrottled は vcgencmd get_throttled の値を返す。
// Pi 以外や vcgencmd が無い環境では ok=false。
func readThrottled() (uint32, bool) {
	out, err := exec.Command("vcgencmd", "get_throttled").Output()
	if err != nil {
		return 0, false
	}
	// "throttled=0x0" の形で返る
	_, hex, found := strings.Cut(strings.TrimSpace(string(out)), "=")
	if !found {
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "0x"), 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

// readDiskWrittenGB は起動してからのSDへの書き込み量をGBで返す。
// /proc/diskstats の第10フィールドが書き込みセクタ数 (512バイト単位)。
func readDiskWrittenGB() float64 {
	b, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[2] != "mmcblk0" {
			continue
		}
		sectors, err := strconv.ParseFloat(f[9], 64)
		if err != nil {
			return 0
		}
		return sectors * 512 / (1024 * 1024 * 1024)
	}
	return 0
}

func readUptimeSec() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	sec, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return int64(sec)
}

// String は人が読める1行にまとめる。
func (s Status) String() string {
	return fmt.Sprintf("SoC %.1f℃ 書込 %.2fGB 不正終了 %d/%d回",
		s.SoCTempC, s.DiskWrittenGB, s.UncleanShutdowns, s.BootCount)
}
