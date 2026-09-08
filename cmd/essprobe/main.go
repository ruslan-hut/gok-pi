// Command essprobe is a read-only diagnostic for Huawei LUNA2000B energy
// storage systems reachable over Modbus-TCP.
//
// It exists to answer, without changing anything about the equipment, the
// questions that have to be settled before an agent is pointed at a site:
// what actually answers on the endpoint, whether the point table in
// battery/driver/huawei matches the installation, which way the sign of the
// charge/discharge power runs, and whether something else on the network is
// already dispatching the ESS.
//
// It cannot write. The Modbus client it uses implements only function codes
// 0x03 and 0x2B, so no code path in this binary can alter the state of the
// equipment it is pointed at. That is what makes it safe to run against a site
// in production service.
//
// Usage:
//
//	essprobe -addr 10.0.0.5:502 identify
//	essprobe -addr 10.0.0.5:502 dump [-json] [-all]
//	essprobe -addr 10.0.0.5:502 watch [-interval 10s] [-out obs.jsonl] [-duration 24h]
//	essprobe -addr 10.0.0.5:502 alarms
//
// Connecting consumes a client slot on the endpoint. If the installation has an
// EMS of its own and the endpoint permits few concurrent clients, that is not
// free: the probe therefore holds a single connection and reuses it rather than
// dialling per request.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gok-pi/internal/modbus"
)

const usage = `essprobe is a read-only Modbus-TCP diagnostic for Huawei LUNA2000B systems.

Usage:
  essprobe [flags] <command>

Commands:
  identify   report what answers on the endpoint: vendor, product, device list
  dump       read the point table once and print name, raw value and decoded value
  watch      poll telemetry, setpoints and alarms, appending one JSON object per sample
  alarms     read the 52 alarm words and list the alarms currently raised

Flags:
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "essprobe:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr    = flag.String("addr", "", "ESS endpoint as host:port (port 502 unless the site says otherwise)")
		unit    = flag.Uint("unit", 0, "Modbus unit id; 0 is the directly connected node")
		timeout = flag.Duration("timeout", 5*time.Second, "per-request timeout")
		retries = flag.Int("retries", 2, "retries for a request the device rejects as busy or times out")

		asJSON   = flag.Bool("json", false, "dump: emit JSON instead of a table")
		all      = flag.Bool("all", false, "dump: read every documented register, not just the observation set")
		interval = flag.Duration("interval", 10*time.Second, "watch: seconds between samples")
		duration = flag.Duration("duration", 0, "watch: stop after this long (0 runs until interrupted)")
		out      = flag.String("out", "", "watch: append JSON Lines to this file (stdout if empty)")
	)

	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usage)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *addr == "" {
		flag.Usage()

		return errors.New("-addr is required")
	}
	if *unit > 255 {
		return fmt.Errorf("unit %d is out of range", *unit)
	}

	cmd := flag.Arg(0)
	if cmd == "" {
		flag.Usage()

		return errors.New("a command is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := modbus.New(*addr, *timeout)
	defer func() { _ = client.Close() }()

	r := &reader{client: client, unit: uint8(*unit), retries: *retries}

	switch cmd {
	case "identify":
		return identify(ctx, r, os.Stdout)
	case "dump":
		return dump(ctx, r, os.Stdout, *asJSON, *all)
	case "watch":
		return watch(ctx, r, watchOptions{
			interval: *interval,
			duration: *duration,
			path:     *out,
			log:      os.Stderr,
		})
	case "alarms":
		return alarms(ctx, r, os.Stdout)
	default:
		flag.Usage()

		return fmt.Errorf("unknown command %q", cmd)
	}
}
