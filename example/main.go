// Command example serves a Postgres-backed blog with Hearth: pages render on first visit, are
// written to disk as plain files, and a web server in front serves every later request from them.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // the database driver lives here, never in the library

	"github.com/orelhadad/hearth/pkg/hearth"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var store Store
	switch env("STORE", "postgres") {
	case "memory":
		store = NewMemory().Seed()
		slog.Info("using the in-memory demo store (STORE=memory)")
	case "postgres":
		url := os.Getenv("DATABASE_URL")
		if url == "" {
			return errors.New("DATABASE_URL is required (or run with STORE=memory)")
		}
		db, err := sql.Open("pgx", url)
		if err != nil {
			return err
		}
		defer db.Close()
		db.SetMaxOpenConns(10) // renders are limited to MaxConcurrent, so the pool stays small
		db.SetConnMaxLifetime(30 * time.Minute)
		store = Postgres{DB: db}
	default:
		return errors.New("STORE must be postgres or memory")
	}

	site, err := hearth.New(hearth.Options{
		Bundle:        os.DirFS(env("BUNDLE", "site/dist/templates")), // or an embed.FS, see docs/DEPLOY.md
		CacheDir:      env("CACHE_DIR", "var/cache"),
		BaseURL:       env("BASE_URL", "http://localhost:8080"),
		MaxConcurrent: 8, // protects the database from a burst of cache misses
		RenderTimeout: 5 * time.Second,
		// Database-backed pages are not known at build time, so the server lists them.
		SitemapEntries: func(ctx context.Context) ([]string, error) {
			slugs, err := store.Slugs(ctx)
			paths := make([]string, len(slugs))
			for i, s := range slugs {
				paths[i] = "/post/" + s
			}
			return paths, err
		},
	})
	if err != nil {
		return err
	}
	site.MustHandle("post/{slug}", postHandler(store)) // a typo in the pattern panics here, at startup
	site.MustHandle("/", indexHandler(store))          // a page without a parameter must be in "ssr" in the config
	if err := site.Validate(); err != nil {            // an ssr page without a handler would answer 404
		return err
	}

	// The library has no database driver, so the server decides what counts as a change:
	// here, whenever the version token of the posts changes, pages refill from the new data.
	interval, _ := strconv.Atoi(env("POLL_SECONDS", "5"))
	go func() {
		err := site.Subscribe(ctx, hearth.Poll(store.Version, time.Duration(interval)*time.Second))
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("subscribe", "err", err)
		}
	}()

	srv := &http.Server{
		Addr:              env("ADDR", ":8080"),
		Handler:           site,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", srv.Addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}
