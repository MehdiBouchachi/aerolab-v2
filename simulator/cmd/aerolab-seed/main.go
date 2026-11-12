// aerolab-seed provisions the DEV environment (broker credentials, ACL,
// edge env file). Secrets are written to an ignored directory only.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mehdibouchachi/aerolab-v2/simulator/seed"
)

func main() {
	out := flag.String("out", ".dev", "output directory (must be git-ignored)")
	site := flag.String("site", "site-sim-01", "site id")
	n := flag.Int("devices", 10, "number of simulated devices (1..64)")
	port := flag.Int("port", 18830, "broker listen port (loopback)")
	flag.Parse()
	r, err := seed.Provision(seed.Options{OutDir: *out, SiteID: *site, Devices: *n, BrokerPort: *port})
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
	fmt.Printf("Provisioned %d SIMULATED devices for %s in %s\n", len(r.Devices), r.SiteID, r.OutDir)
	fmt.Printf("  mosquitto -c %s\n  env file : %s\n  creds    : %s\n", r.MosquittoConf, r.EdgeEnvFile, r.CredsFile)
	fmt.Printf("  dev admin login: %s / %s   (random, dev only; stored in edge.env)\n", r.AdminUser, r.AdminPassword)
}
