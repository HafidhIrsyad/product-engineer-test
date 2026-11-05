package logging

import (
	"context"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/tracelog"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"
)

// New builds the base logger: human-readable console output when local, JSON otherwise.
func New(local bool, level string) (zerolog.Logger, error) {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		return zerolog.Logger{}, err
	}
	var w io.Writer = os.Stdout
	if local {
		w = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	}
	logger := zerolog.New(w).Level(lvl).With().Timestamp().Logger()
	// Used by zerolog.Ctx outside a request, e.g. queries run by the expiry sweeper.
	zerolog.DefaultContextLogger = &logger
	return logger, nil
}

func Middleware(base zerolog.Logger) []func(http.Handler) http.Handler {
	return []func(http.Handler) http.Handler{
		hlog.NewHandler(base),
		hlog.RequestIDHandler("request_id", "X-Request-Id"),
		hlog.MethodHandler("method"),
		hlog.URLHandler("url"),
		hlog.RemoteAddrHandler("remote"),
		requestStarted,
		hlog.AccessHandler(requestCompleted),
	}
}

func requestStarted(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hlog.FromRequest(r).Info().Msg("request started")
		next.ServeHTTP(w, r)
	})
}

func requestCompleted(r *http.Request, status, size int, duration time.Duration) {
	hlog.FromRequest(r).Info().
		Int("status", status).
		Int("size", size).
		Dur("duration", duration).
		Msg("request completed")
}

func SQLTracer() pgx.QueryTracer {
	return &tracelog.TraceLog{
		Logger:   tracelog.LoggerFunc(logSQL),
		LogLevel: tracelog.LogLevelInfo,                         // pgx logs success at Info, failure at Error
		Config:   &tracelog.TraceLogConfig{TimeKey: "duration"}, // "time" would clash with zerolog's timestamp
	}
}

func logSQL(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	l := zerolog.Ctx(ctx) // request logger (carries request_id), or the base logger
	ev := l.Debug()
	if level == tracelog.LogLevelError {
		ev = l.Error()
	}
	ev.Fields(data).Msg("sql " + msg)
}
