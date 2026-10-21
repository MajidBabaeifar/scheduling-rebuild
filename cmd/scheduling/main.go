// Command scheduling serves the scheduling app (RB-7).
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"scheduling-rebuild/internal/app"
	"scheduling-rebuild/internal/web"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dbPath := flag.String("db", "scheduling.db", "SQLite file")
	signup := flag.Bool("signup", true, "allow sign-up (AUTH-2)")
	baseURL := flag.String("base-url", "", "absolute URL used in emails (default http://localhost<addr>)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	db, err := app.OpenDB(*dbPath)
	if err != nil {
		slog.Error("open db", "err", err)
		os.Exit(1)
	}
	if *baseURL == "" {
		*baseURL = "http://localhost" + *addr
	}
	a := app.New(db, *baseURL)
	a.SignupEnabled = *signup
	srv := &http.Server{Addr: *addr, Handler: web.New(a).Handler(), ReadHeaderTimeout: 10 * time.Second}
	slog.Info("listening", "addr", *addr, "base_url", *baseURL)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
