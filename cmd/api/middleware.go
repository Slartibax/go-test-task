package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

func withRequestTimeout(timeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recoveredPanic := recover()
			if recoveredPanic == nil {
				return
			}
			if panicErr, ok := recoveredPanic.(error); ok && errors.Is(panicErr, http.ErrAbortHandler) {
				panic(panicErr)
			}
			slog.Error("panic", "err", recoveredPanic)
			if writer, ok := w.(*responseStatusWriter); ok && writer.status != 0 {
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			if _, err := w.Write([]byte(`{"error":"internal error"}`)); err != nil {
				slog.Error("write panic response", "err", err)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestStartedAt := time.Now()
		responseWriter := &responseStatusWriter{ResponseWriter: w}
		next.ServeHTTP(responseWriter, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", responseWriter.status,
			"duration_ms", time.Since(requestStartedAt).Milliseconds(),
		)
	})
}

type responseStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseStatusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStatusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}
