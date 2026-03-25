package can

import "errors"

// ErrTimeout はCAN読み取りタイムアウト時に返される
var ErrTimeout = errors.New("CAN読み取りタイムアウト")
