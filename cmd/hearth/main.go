// Command hearth builds and serves Hearth sites.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/orelhadad/hearth/internal/build"
	"github.com/orelhadad/hearth/internal/config"
	"github.com/orelhadad/hearth/internal/dev"
	"github.com/orelhadad/hearth/internal/starter"
)

const usage = `hearth: SEO and performance focused sites, one template location for static and SSR pages.

Usage:
  hearth build   [-site DIR] [-out DIR]           run Vite, prerender pages, write dist/
  hearth check   [-site DIR]                      validate routes, data, locales; trial-render every page
  hearth dev     [-site DIR] [-addr A] [-live]    mock data from *.mock.json, or -live to proxy your Go server
  hearth init    [-name NAME] [DIR]               write the Vite starter into DIR (default: current directory)
  hearth version
  hearth preview [-site DIR] [-addr A] [-out DIR] production flow: dist/public file, else your Go server

Options come from hearth.config.json in the site directory.
`

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stderr, usage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, "hearth", version)
		return 0
	case "init":
		return runInit(rest, stdout, stderr)
	}
	fs := flag.NewFlagSet("hearth "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	site := fs.String("site", ".", "site directory")
	var out, addr *string
	var live *bool
	switch cmd {
	case "build":
		out = fs.String("out", "", "output directory (default <site>/dist)")
	case "dev":
		addr = fs.String("addr", "", "listen address (default from config, 127.0.0.1:3000)")
		live = fs.Bool("live", false, "proxy to dev.server instead of rendering mock data")
	case "preview":
		addr = fs.String("addr", "", "listen address")
		out = fs.String("out", "", "build output directory (default <site>/dist)")
	case "check":
	default:
		fmt.Fprintf(stderr, "hearth: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "hearth %s: unexpected argument %q\n", cmd, fs.Arg(0))
		return 2
	}
	cfg, err := config.Load(*site)
	if err != nil {
		fmt.Fprintln(stderr, "hearth:", err)
		return 1
	}
	switch cmd {
	case "build":
		rep, err := build.Run(build.Options{SiteDir: *site, OutDir: *out, Config: cfg, Log: stderr, Context: signalContext()})
		if err != nil {
			fmt.Fprintln(stderr, "hearth: build failed:", err)
			return 1
		}
		fmt.Fprintf(stdout, "built %d pages into %s\n", len(rep.Pages), rep.Out)
	case "check":
		errs := build.Check(*site, cfg)
		for _, e := range errs {
			fmt.Fprintln(stderr, "  -", e)
		}
		if len(errs) > 0 {
			fmt.Fprintf(stderr, "hearth: check found %d problem(s)\n", len(errs))
			return 1
		}
		fmt.Fprintln(stdout, "ok")
	case "dev", "preview":
		var h http.Handler
		switch {
		case cmd == "dev" && *live:
			h, err = dev.NewLive(cfg.Dev.Server)
		case cmd == "dev":
			h, err = dev.NewMock(*site, cfg)
		default:
			dist := *out
			if dist == "" {
				dist = filepath.Join(*site, "dist")
			}
			h, err = dev.NewPreview(filepath.Join(dist, "public"), cfg.Dev.Server)
		}
		if err != nil {
			fmt.Fprintln(stderr, "hearth:", err)
			return 1
		}
		a := cfg.Dev.Addr
		if *addr != "" {
			a = *addr
		}
		if err := serve(a, h, stdout); err != nil {
			fmt.Fprintln(stderr, "hearth:", err)
			return 1
		}
	}
	return 0
}

func signalContext() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}

func serve(addr string, h http.Handler, stdout io.Writer) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ctx := signalContext()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	fmt.Fprintf(stdout, "hearth: listening on http://%s\n", addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hearth init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "package name (default: directory name)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "hearth init: at most one directory")
		return 2
	}
	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(stderr, "hearth:", err)
		return 1
	}
	n := *name
	if n == "" {
		n = strings.ToLower(strings.NewReplacer(" ", "-", ".", "-").Replace(filepath.Base(abs)))
	}
	files, err := starter.Init(abs, n)
	if err != nil {
		fmt.Fprintln(stderr, "hearth:", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %d files to %s\nnext: npm install && npm run build\n", len(files), abs)
	return 0
}
