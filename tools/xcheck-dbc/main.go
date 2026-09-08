// Go 側デコーダ (internal/can) で candump ログをデコードして CSV に出す。
//
// contrib/mazda_demio_dy.dbc を cantools でデコードした結果と突き合わせ、
// 独立した2実装が同じ値を出すことを確かめる。DBC 内部の整合 (車速と4輪速が
// 一致する等) を見ても「DBC を DBC で検証」しているだけなので、外部の実装と
// 比べる必要がある。
//
//	go run ./tools/xcheck-dbc <candump.log> > /tmp/go.csv
//	python3 scripts/ops/xcheck-dbc.py <candump.log> /tmp/go.csv
package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/hashimoto/pi-obd-meter/internal/can"
)

func main() {
	fh, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer fh.Close()
	limit := 200000
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() && n < limit {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || !strings.HasPrefix(f[3], "[") {
			continue
		}
		ln, err := strconv.Atoi(strings.Trim(f[3], "[]"))
		if err != nil || len(f) < 4+ln {
			continue
		}
		b, err := hex.DecodeString(strings.Join(f[4:4+ln], ""))
		if err != nil {
			continue
		}
		id, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil {
			continue
		}
		var d [8]byte
		copy(d[:], b)
		n++
		switch uint32(id) {
		case can.IDEngine:
			rpm, spd, load := can.DecodeEngine(d)
			fmt.Fprintf(w, "%d,201,RPM,%.9f\n%d,201,SPEED,%.9f\n%d,201,ENGINE_LOAD,%.9f\n", n, rpm, n, spd, n, load)
		case can.IDElectric:
			b0, b1, odo := can.DecodeElectric(d)
			fmt.Fprintf(w, "%d,430,FUEL_LEVEL,%.9f\n%d,430,UNKNOWN_B1,%.9f\n%d,430,ODOMETER,%.9f\n", n, b0, n, b1, n, odo)
		case can.IDCoolant:
			t, p := can.DecodeCoolant(d)
			fmt.Fprintf(w, "%d,420,COOLANT_TEMP,%.9f\n%d,420,DISTANCE_PULSE,%.9f\n", n, t, n, float64(p))
		case can.IDWheels:
			fmt.Fprintf(w, "%d,4B0,WHEEL_MEAN,%.9f\n", n, can.DecodeWheelSpeed(d))
		}
	}
}
