// Package trace 在 context 中传递 trace ID，Log 时每行带 TRACE=id 便于排查。
package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
)

type ctxKey int

const traceIDKey ctxKey = 0

func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

func TraceID(ctx context.Context) string {
	if id, ok := ctx.Value(traceIDKey).(string); ok {
		return id
	}
	return ""
}

func NewTraceID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "0"
	}
	return hex.EncodeToString(b)
}

var logMu sync.Mutex

// Log 打日志，每行开头固定为 TRACE=id，便于一眼看到 trace 并 grep
func Log(ctx context.Context, format string, args ...interface{}) {
	id := TraceID(ctx)
	if id == "" {
		id = "-"
	}
	logMu.Lock()
	msg := fmt.Sprintf(format, args...)
	log.Printf("TRACE=%s | %s", id, msg)
	logMu.Unlock()
}
