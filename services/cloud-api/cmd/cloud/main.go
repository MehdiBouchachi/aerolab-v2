// Command cloud is the AeroLab cloud API.
//
//	cloud [serve]                         run the API (migrates first)
//	cloud migrate                         apply migrations and exit
//	cloud create-user  -email E           password from AEROLAB_NEW_PASSWORD or stdin
//	cloud create-site  -id S -name N -admin E   creates org+site, makes E its admin,
//	                                      prints the per-site edge token ONCE
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/api"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/auth"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/config"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/internal/db"
	"github.com/mehdibouchachi/aerolab-v2/services/cloud-api/migrations"
)

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
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d, err := db.Open(ctx, cfg.DatabaseURL)
	fatal(err)
	defer d.Close()
	applied, err := db.Migrate(ctx, d, migrations.FS)
	fatal(err)
	if len(applied) > 0 {
		log.Info("migrations applied", "versions", applied)
	}
	a := auth.New(d)

	switch cmd {
	case "migrate":
		fmt.Println("applied:", applied)
	case "create-user":
		fs := flag.NewFlagSet("create-user", flag.ExitOnError)
		email := fs.String("email", "", "email")
		_ = fs.Parse(os.Args[1:])
		u, err := a.CreateUser(ctx, *email, password())
		fatal(err)
		fmt.Println("created", u.Email, u.ID)
	case "create-site":
		fs := flag.NewFlagSet("create-site", flag.ExitOnError)
		id, name, admin := fs.String("id", "", "site id"), fs.String("name", "", "site name"), fs.String("admin", "", "admin email")
		_ = fs.Parse(os.Args[1:])
		if !mc.ValidID(*id) || *name == "" {
			fatal(fmt.Errorf("-id must match [a-z0-9-]{3,40} and -name is required"))
		}
		var uid string
		fatal(d.QueryRowContext(ctx, `SELECT id FROM cloud_users WHERE email=lower($1)`, *admin).Scan(&uid))
		org := "org-" + *id
		_, err := d.ExecContext(ctx, `INSERT INTO organizations(id,name) VALUES ($1,$2) ON CONFLICT DO NOTHING`, org, *name)
		fatal(err)
		_, err = d.ExecContext(ctx, `INSERT INTO organization_members(org_id,user_id,role) VALUES ($1,$2,'OWNER') ON CONFLICT DO NOTHING`, org, uid)
		fatal(err)
		_, err = d.ExecContext(ctx, `INSERT INTO sites(id,org_id,name) VALUES ($1,$2,$3)`, *id, org, *name)
		fatal(err)
		_, err = d.ExecContext(ctx, `INSERT INTO site_members(site_id,user_id,role) VALUES ($1,$2,'ADMIN')`, *id, uid)
		fatal(err)
		tok, err := a.IssueEdgeToken(ctx, *id, false)
		fatal(err)
		fmt.Printf("site %s created. EDGE TOKEN (shown once, store as AEROLAB_CLOUD_TOKEN on the Pi):\n%s\n", *id, tok)
	case "serve":
		srv := api.New(cfg, d, a, nil, log)
		ln, err := net.Listen("tcp", cfg.HTTPAddr)
		fatal(err)
		go func() { // remote requests that were never picked up must expire, not linger
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if _, err := api.ExpireIntents(ctx, d); err != nil {
						log.Warn("expire intents", "err", err)
					}
				}
			}
		}()
		log.Info("cloud API ready", "http", ln.Addr().String())
		fatal(srv.Serve(ctx, ln))
	default:
		fatal(fmt.Errorf("unknown command %q", cmd))
	}
}

func password() string {
	if p := os.Getenv("AEROLAB_NEW_PASSWORD"); p != "" {
		return p
	}
	fmt.Fprint(os.Stderr, "password (min 12): ")
	p, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(p, "\r\n")
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
