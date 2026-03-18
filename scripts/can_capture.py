#!/usr/bin/env python3
"""CAN ID キャプチャスクリプト — ELM327 V1.5 経由

使い方:
  python3 can_capture.py [CAN_ID] [秒数] [出力ファイル]
  python3 can_capture.py 200 600 /tmp/can_200_warmup.csv

対話モード (引数なし):
  python3 can_capture.py
"""

import serial
import time
import sys
import os
import signal

SERIAL_PORT = "/dev/rfcomm0"
BAUD_RATE = 38400
DEFAULT_OUTPUT_DIR = "/opt/pi-obd-meter/can_capture"


def send_cmd(ser, cmd, wait=0.5):
    """ATコマンド送信して応答を返す"""
    ser.reset_input_buffer()
    ser.write((cmd + "\r").encode())
    time.sleep(wait)
    resp = ser.read(ser.in_waiting).decode(errors="replace").strip()
    return resp


def init_elm(ser):
    """ELM327 初期化"""
    print("ELM327 初期化中...")
    # ATZ リセット — 応答が遅いので十分待つ
    ser.reset_input_buffer()
    ser.write(b"ATZ\r")
    time.sleep(3)
    resp = ser.read(ser.in_waiting).decode(errors="replace")
    if "ELM" in resp:
        ver = [l.strip() for l in resp.splitlines() if "ELM" in l]
        if ver:
            print(f"  接続: {ver[0]}")

    # バッファ完全クリア後にコマンド送信
    ser.reset_input_buffer()
    for cmd in ["ATE0", "ATE0", "ATL0", "ATH1", "ATSP6"]:
        ser.reset_input_buffer()
        ser.write((cmd + "\r").encode())
        time.sleep(0.5)
        ser.read(ser.in_waiting)
    print("  初期化完了")


def capture(ser, can_id, duration_sec, output_file):
    """指定 CAN ID をモニターしてCSVに保存"""
    # フィルタ設定
    resp = send_cmd(ser, f"AT CRA {can_id}", 0.5)
    print(f"  フィルタ設定: AT CRA {can_id} → {resp}")
    ser.reset_input_buffer()

    # モニター開始
    ser.write(b"ATMA\r")
    time.sleep(0.5)

    print(f"  キャプチャ中: CAN ID 0x{can_id} ({duration_sec}秒)")
    print(f"  Ctrl+C で早期終了可")

    start = time.time()
    frames = []
    errors = 0

    try:
        while time.time() - start < duration_sec:
            if ser.in_waiting:
                line = ser.readline().decode(errors="replace").strip()
                if not line or line.startswith(">") or "SEARCHING" in line:
                    continue
                if "BUFFER FULL" in line:
                    errors += 1
                    continue
                # ATコマンドエコーやOKを除外
                if line.startswith("AT") or line == "OK":
                    continue
                # DATA ERROR はカウントしつつデータ部分は保存
                if "DATA ERROR" in line:
                    errors += 1
                    line = line.replace("DATA ERROR", "").strip().rstrip("<").strip()
                ts = time.time() - start
                frames.append(f"{ts:.3f},{line}")
            else:
                time.sleep(0.01)
    except KeyboardInterrupt:
        print("\n  手動停止")

    # モニター停止
    ser.write(b"\r")
    time.sleep(0.5)
    ser.read(ser.in_waiting)

    # 保存
    os.makedirs(os.path.dirname(output_file) or ".", exist_ok=True)
    with open(output_file, "w") as f:
        f.write("timestamp,raw\n")
        for frame in frames:
            f.write(frame + "\n")

    elapsed = time.time() - start
    print(f"  完了: {len(frames)} フレーム, {errors} エラー, {elapsed:.0f}秒")
    print(f"  保存: {output_file}")
    return len(frames)


def wait_for_enter(msg):
    """ユーザーの Enter 待ち"""
    input(f"\n>>> {msg} [Enter で開始] ")


def run_interactive(ser):
    """対話モード: 3テストを順番に実行"""
    os.makedirs(DEFAULT_OUTPUT_DIR, exist_ok=True)
    ts = time.strftime("%Y%m%d_%H%M")

    print()
    print("=" * 50)
    print(" CAN キャプチャ — 3テスト実行")
    print("=" * 50)
    print()
    print("テスト順序:")
    print("  1. 0x430 (電圧?) — ACC ON, エンジン OFF")
    print("  2. 0x200 (温度?) — エンジン始動→暖機")
    print("  3. 0x210 (AT?)   — 走行中")
    print()

    # --- テスト1: 0x430 ---
    print("-" * 50)
    print("テスト1: 0x430 — 電圧/補機")
    print("-" * 50)
    print("手順:")
    print("  1. エンジン OFF のまま (ACC ON で Pi 起動済み)")
    print("  2. Enter でキャプチャ開始 (60秒)")
    print("  3. 10秒待つ → エンジン始動")
    print("  4. 20秒アイドル")
    print("  5. エアコン ON → 10秒 → OFF")
    print("  6. ヘッドライト ON → 10秒 → OFF")
    wait_for_enter("準備OK?")
    capture(ser, "430", 60, f"{DEFAULT_OUTPUT_DIR}/{ts}_430_voltage.csv")

    # --- テスト2: 0x200 ---
    print()
    print("-" * 50)
    print("テスト2: 0x200 — エンジンセンサー (暖機)")
    print("-" * 50)
    print("手順:")
    print("  1. Enter でキャプチャ開始 (10分)")
    print("  2. そのままアイドル放置")
    print("  3. 暖機完了まで待つ (水温計が動くまで)")
    print("  4. 終わったら Ctrl+C で早期終了OK")
    wait_for_enter("準備OK?")
    capture(ser, "200", 600, f"{DEFAULT_OUTPUT_DIR}/{ts}_200_warmup.csv")

    # --- テスト3: 0x210 ---
    print()
    print("-" * 50)
    print("テスト3: 0x210 — AT 制御")
    print("-" * 50)
    print("手順:")
    print("  1. 停車中に P → N → D → L をゆっくり切り替え (各5秒)")
    print("  2. Enter でキャプチャ開始 (5分)")
    print("  3. D で発進 → 40km/h まで加速")
    print("  4. 巡航10秒")
    print("  5. 減速 → 停車")
    print("  6. 終わったら Ctrl+C で早期終了OK")
    wait_for_enter("準備OK?")
    capture(ser, "210", 300, f"{DEFAULT_OUTPUT_DIR}/{ts}_210_at.csv")

    print()
    print("=" * 50)
    print(" 全テスト完了!")
    print("=" * 50)
    print()
    print(f"データ: {DEFAULT_OUTPUT_DIR}/")
    for f in sorted(os.listdir(DEFAULT_OUTPUT_DIR)):
        if f.startswith(ts):
            path = f"{DEFAULT_OUTPUT_DIR}/{f}"
            size = os.path.getsize(path)
            print(f"  {f}  ({size:,} bytes)")
    print()
    print("このウィンドウを閉じてOK。")
    print("メーターを復帰するには:")
    print("  sudo systemctl start pi-obd-meter")


def main():
    # 単発モード: can_capture.py 200 600 /tmp/out.csv
    if len(sys.argv) >= 3:
        can_id = sys.argv[1]
        duration = int(sys.argv[2])
        output = sys.argv[3] if len(sys.argv) >= 4 else f"/tmp/can_{can_id}.csv"
        ser = serial.Serial(SERIAL_PORT, BAUD_RATE, timeout=1)
        init_elm(ser)
        capture(ser, can_id, duration, output)
        ser.close()
        return

    # 対話モード
    try:
        ser = serial.Serial(SERIAL_PORT, BAUD_RATE, timeout=1)
    except serial.SerialException as e:
        print(f"エラー: {SERIAL_PORT} に接続できません")
        print(f"  {e}")
        print()
        print("確認:")
        print("  1. sudo systemctl stop pi-obd-meter")
        print("  2. sudo rfcomm bind 0 <ELM327のBTアドレス>")
        sys.exit(1)

    init_elm(ser)
    run_interactive(ser)
    ser.close()


if __name__ == "__main__":
    main()
