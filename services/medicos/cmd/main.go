// Medicos: doctor profiles (MySQL, source of truth) and doctor search
// (OpenSearch projection kept in sync through the outbox, ADR-006).
//
// Usage: medicos            serve HTTP, relay the outbox and run the indexer
//
//	medicos reindex    rebuild doctors-vN+1 from MySQL, swap the alias and exit
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/medicos/internal/adapters/httpapi"
	"portal-paciente-clinica/services/medicos/internal/adapters/indexer"
	"portal-paciente-clinica/services/medicos/internal/adapters/mysqlstore"
	"portal-paciente-clinica/services/medicos/internal/adapters/opensearch"
	"portal-paciente-clinica/services/medicos/internal/adapters/outbox"
	"portal-paciente-clinica/services/medicos/internal/app"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func openStore(ctx context.Context) *mysqlstore.Store {
	dsn := os.Getenv("MEDICOS_DB_DSN")
	if dsn == "" {
		slog.Error("MEDICOS_DB_DSN is required")
		os.Exit(1)
	}
	// MySQL may take a while to accept connections when the environment starts.
	for attempt := 1; ; attempt++ {
		store, err := mysqlstore.Open(ctx, dsn)
		if err == nil {
			return store
		}
		if attempt == 30 || ctx.Err() != nil {
			slog.Error("database unavailable", "error", err.Error())
			os.Exit(1)
		}
		slog.Warn("waiting for database", "event", "db.waiting", "attempt", attempt)
		time.Sleep(2 * time.Second)
	}
}

// bootstrapIndex creates doctors-v1 behind the alias on the first start. It
// retries while OpenSearch is down; search answers 503 meanwhile.
func bootstrapIndex(ctx context.Context, svc *app.Service, ix *opensearch.Index) bool {
	for ctx.Err() == nil {
		exists, err := ix.AliasExists(ctx)
		if err == nil && exists {
			return true
		}
		if err == nil {
			var name string
			var n int
			if name, n, err = svc.Reindex(ctx); err == nil {
				slog.InfoContext(ctx, "search index created", "event", "search.bootstrapped", "index", name, "documents", n)
				return true
			}
		}
		slog.WarnContext(ctx, "search index not ready, retrying", "event", "search.bootstrap_failed", "error", err.Error())
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
	return false
}

func main() {
	obs.NewLogger("medicos")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := openStore(ctx)
	index := opensearch.New(env("OPENSEARCH_URL", "http://127.0.0.1:9200"))
	svc := &app.Service{Store: store, Index: index, Now: time.Now, NewID: obs.NewID}

	if len(os.Args) > 1 && os.Args[1] == "reindex" {
		name, n, err := svc.Reindex(ctx)
		if err != nil {
			slog.Error("reindex failed", "event", "search.reindex_failed", "error", err.Error())
			os.Exit(1)
		}
		fmt.Printf("alias %s now points to %s (%d documents)\n", opensearch.Alias, name, n)
		return
	}

	shutdown, err := obs.InitTracing(ctx, "medicos")
	if err != nil {
		slog.Error("tracing init failed", "error", err.Error())
		os.Exit(1)
	}
	defer shutdown(context.Background())

	amqpURL := env("RABBITMQ_URL", "amqp://guest:guest@127.0.0.1:5672/")
	relay := &outbox.Relay{DB: store.DB, URL: amqpURL, Interval: 500 * time.Millisecond}
	go relay.Run(ctx)
	go func() {
		// The consumer starts once the alias exists, so events are never indexed
		// into an auto-created index; until then they wait in the outbox or queue.
		if bootstrapIndex(ctx, svc, index) {
			(&indexer.Consumer{URL: amqpURL, Handler: svc, Now: time.Now}).Run(ctx)
		}
	}()

	mux := http.NewServeMux()
	httpapi.Register(mux, svc)
	mux.Handle("GET /metrics", obs.MetricsHandler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		status, code, search := "ok", 200, "ok"
		if err := store.DB.PingContext(r.Context()); err != nil {
			status, code = "degraded", 503
		}
		// Search is a derived projection: its outage degrades search only.
		if err := index.Ping(r.Context()); err != nil {
			search = "unavailable"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"service": "medicos", "status": status, "search": search})
	})

	addr := env("HTTP_ADDR", "127.0.0.1:8083")
	server := &http.Server{Addr: addr, Handler: obs.Middleware("medicos", mux), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(sctx)
	}()
	slog.Info("medicos listening", "event", "service.started", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("http server failed", "error", err.Error())
		os.Exit(1)
	}
}
