// Command edge is the AeroLab Raspberry Pi service.
//
//	edge [serve]            run the service (default)
//	edge migrate            apply database migrations and exit
//	edge create-user ...    create a local account; password is read from
//	                        the AEROLAB_NEW_PASSWORD env var or stdin (never argv)
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/app"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/config"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/roles"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/internal/storage"
	"github.com/mehdibouchachi/aerolab-v2/services/edge-api/migrations"
)

var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		cmd, os.Args = os.Args[1], append(os.Args[:1], os.Args[2:]...)
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(2)
	}
	lvl := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		lvl = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "serve":
		a, err := app.New(ctx, cfg, log)
		if err != nil {
			log.Error("startup failed", "err", err)
			os.Exit(1)
		}
		log.Info("starting", "version", version)
		if err := a.Run(ctx); err != nil {
			log.Error("stopped with error", "err", err)
			os.Exit(1)
		}
	case "migrate":
		db, err := storage.Open(cfg.DBPath, 0)
		fatal(err)
		applied, err := db.Migrate(ctx, migrations.FS)
		fatal(err)
		fmt.Println("applied:", applied)
		fatal(db.Close())
	case "create-user":
		fs := flag.NewFlagSet("create-user", flag.ExitOnError)
		user := fs.String("username", "", "username")
		role := fs.String("role", "VIEWER", "ADMIN | OPERATOR | VIEWER")
		_ = fs.Parse(os.Args[1:])
		pw := os.Getenv("AEROLAB_NEW_PASSWORD")
		if pw == "" {
			fmt.Fprint(os.Stderr, "password (min 12 chars): ")
			pw, _ = bufio.NewReader(os.Stdin).ReadString('\n')
			pw = strings.TrimRight(pw, "\r\n")
		}
		db, err := storage.Open(cfg.DBPath, 0)
		fatal(err)
		_, err = db.Migrate(ctx, migrations.FS)
		fatal(err)
		u, err := auth.New(db, cfg.SiteID).CreateUser(ctx, *user, pw, roles.Role(strings.ToUpper(*role)), "cli")
		fatal(err)
		fmt.Println("created", u.Username, u.Role)
		fatal(db.Close())
	default:
		fmt.Fprintln(os.Stderr, "unknown command", cmd)
		os.Exit(2)
	}
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
