// aerolab-sim runs N simulated ESP32 controllers against an MQTT broker.
// All published data carries data_source=SIMULATOR.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mehdibouchachi/aerolab-v2/simulator/esp32"
)

type credsFile struct {
	SiteID    string `json:"site_id"`
	BrokerURL string `json:"broker_url"`
	Devices   []struct {
		DeviceID string `json:"device_id"`
		UnitID   string `json:"unit_id"`
		Password string `json:"password"`
	} `json:"devices"`
}

func main() {
	creds := flag.String("creds", ".dev/devices.json", "provisioning file written by aerolab-seed")
	n := flag.Int("devices", 0, "number of devices to run (0 = all in creds file)")
	broker := flag.String("broker", "", "override broker URL")
	hb := flag.Duration("heartbeat", 5*time.Second, "state heartbeat period")
	tel := flag.Duration("telemetry", 60*time.Second, "telemetry snapshot period")
	ctl := flag.String("control", "127.0.0.1:19090", "loopback fault-injection API ('' to disable)")
	rtc := flag.Bool("rtc", false, "simulate a board with a synced RTC (default: monotonic clock only)")
	state := flag.String("state-dir", "", "directory for simulated NVS files (default: volatile)")
	verbose := flag.Bool("v", false, "debug logging")
	flag.Parse()

	lvl := slog.LevelInfo
	if *verbose {
		lvl = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))

	raw, err := os.ReadFile(*creds)
	if err != nil {
		log.Error("cannot read provisioning file; run aerolab-seed first", "err", err)
		os.Exit(1)
	}
	var cf credsFile
	if err := json.Unmarshal(raw, &cf); err != nil {
		log.Error("bad provisioning file", "err", err)
		os.Exit(1)
	}
	if *broker == "" {
		*broker = cf.BrokerURL
	}
	count := len(cf.Devices)
	if *n > 0 && *n < count {
		count = *n
	}

	fleet := esp32.NewFleet()
	for i := 0; i < count; i++ {
		c := cf.Devices[i]
		sp := ""
		if *state != "" {
			sp = fmt.Sprintf("%s/%s.json", *state, c.DeviceID)
		}
		fleet.Add(esp32.New(esp32.Config{SiteID: cf.SiteID, DeviceID: c.DeviceID, UnitID: c.UnitID, BrokerURL: *broker,
			Username: c.DeviceID, Password: c.Password, HeartbeatEvery: *hb, TelemetryEvery: *tel, RTC: *rtc,
			Seed: int64(i + 1), StatePath: sp, Logger: log}))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fleet.Start(ctx)
	log.Warn("SIMULATED controllers running - all data is data_source=SIMULATOR", "devices", count, "broker", *broker)

	if *ctl != "" {
		host, _, _ := net.SplitHostPort(*ctl)
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			log.Error("control API must bind to loopback", "addr", *ctl)
			os.Exit(1)
		}
		srv := &http.Server{Addr: *ctl, Handler: fleet.ControlHandler(), ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = srv.ListenAndServe() }()
		log.Info("fault-injection API", "addr", *ctl)
	}
	<-ctx.Done()
	fleet.Stop()
}
