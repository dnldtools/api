package http

import (
	"context"
	"time"
)

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxRequestID, id)
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
