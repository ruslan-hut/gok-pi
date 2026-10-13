package main

import (
	"context"
	"fmt"
	"gok-pi/battery/driver"
	"gok-pi/battery/entity"
	"gok-pi/internal/config"
	"gok-pi/internal/netscan"
	"gok-pi/internal/remote/wsclient"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// newBatteryScanner builds the scanner behind the battery-scan RPC.
//
// It lives here rather than in the transport because it needs the two things the
// transport deliberately does not have: the driver registry, so the probe speaks
// the battery's own protocol instead of guessing from an HTTP status, and the
// battery's token, which the probe cannot authenticate without.
//
// The battery config is read live from internal/config rather than from a
// snapshot. The scan is run precisely when the configured address has stopped
// working, which is also when someone is editing it, and sweeping with the
// address the agent held at startup would answer the wrong question.
func newBatteryScanner(log *slog.Logger) wsclient.BatteryScanner {
	// The driver constructors log their creation at INFO, token and all. One
	// line per probe would bury the scan's own result in the log an operator is
	// about to read, so the probes get a logger that keeps nothing.
	probeLog := slog.New(slog.DiscardHandler)

	return func(ctx context.Context, req wsclient.BatteryScanRequest) (wsclient.BatteryScanResult, error) {
		battery, err := resolveScanBattery(config.GetBatteries(), req.Battery)
		if err != nil {
			return wsclient.BatteryScanResult{}, err
		}

		target, err := parseBatteryURL(battery.Url)
		if err != nil {
			return wsclient.BatteryScanResult{}, err
		}

		rangeSpec := strings.TrimSpace(req.Range)
		if rangeSpec == "" {
			rangeSpec, err = wsclient.DeriveScanRange(target.host)
			if err != nil {
				return wsclient.BatteryScanResult{}, err
			}
		}

		addresses, err := netscan.ParseRange(rangeSpec)
		if err != nil {
			return wsclient.BatteryScanResult{}, err
		}

		port := req.Port
		if port <= 0 {
			port = target.port
		}
		if port <= 0 || port > 65535 {
			return wsclient.BatteryScanResult{}, fmt.Errorf("invalid port %d", port)
		}

		log.With(
			slog.String("battery", battery.Name),
			slog.String("driver", battery.Driver),
			slog.String("range", rangeSpec),
			slog.Int("addresses", len(addresses)),
			slog.Int("port", port),
		).Info("sweeping for battery")

		summary := netscan.Scan(ctx, netscan.Options{
			Addresses: addresses,
			Port:      port,
			Probe:     batteryProbe(battery, target, port, probeLog),
		})

		return wsclient.BatteryScanResult{
			Battery:       battery.Name,
			ConfiguredURL: battery.Url,
			Range:         rangeSpec,
			Port:          port,
			Results:       summary.Results,
			Scanned:       summary.Scanned,
			Responded:     summary.Responded,
			Found:         summary.Found,
			ElapsedMs:     summary.ElapsedMs,
			Truncated:     summary.Truncated,
		}, nil
	}
}

// resolveScanBattery picks the battery whose driver and credentials the probe
// borrows. Disabled batteries are eligible: a battery is often switched off in
// the config precisely because it stopped answering, and that is the one an
// operator is looking for.
func resolveScanBattery(batteries []entity.BatteryConfig, name string) (entity.BatteryConfig, error) {
	if len(batteries) == 0 {
		return entity.BatteryConfig{}, fmt.Errorf("no batteries are configured")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		if len(batteries) == 1 {
			return batteries[0], nil
		}
		names := make([]string, 0, len(batteries))
		for _, b := range batteries {
			names = append(names, b.Name)
		}
		return entity.BatteryConfig{}, fmt.Errorf(
			"several batteries are configured (%s); name the one to scan for",
			strings.Join(names, ", "),
		)
	}

	for _, b := range batteries {
		if strings.EqualFold(b.Name, name) {
			return b, nil
		}
	}
	return entity.BatteryConfig{}, fmt.Errorf("no battery named %q is configured", name)
}

// batteryTarget is the configured URL taken apart, so a probe can rebuild it
// against a different address without disturbing the scheme or the API path.
type batteryTarget struct {
	scheme string
	host   string
	port   int
	path   string
	query  string
}

func parseBatteryURL(raw string) (batteryTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return batteryTarget{}, fmt.Errorf("battery has no configured URL to scan from")
	}
	if !strings.Contains(raw, "://") {
		// Tolerate a bare host:port, which is what a Modbus-style config looks
		// like and what an operator is apt to type.
		raw = "http://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return batteryTarget{}, fmt.Errorf("battery URL %q is not usable: %w", raw, err)
	}

	host, portStr := u.Hostname(), u.Port()
	if host == "" {
		return batteryTarget{}, fmt.Errorf("battery URL %q has no host", raw)
	}

	port := 0
	if portStr != "" {
		port, err = strconv.Atoi(portStr)
		if err != nil {
			return batteryTarget{}, fmt.Errorf("battery URL %q has an invalid port: %w", raw, err)
		}
	} else {
		switch u.Scheme {
		case "https":
			port = 443
		default:
			port = 80
		}
	}

	return batteryTarget{
		scheme: u.Scheme,
		host:   host,
		port:   port,
		path:   u.Path,
		query:  u.RawQuery,
	}, nil
}

// urlFor rebuilds the configured URL against a candidate address. Everything but
// the host is carried over: the API lives at the same path on whatever address
// the battery answers on, and losing the path is how a probe ends up asking a
// web server at the root and calling the 404 a verdict.
func (t batteryTarget) urlFor(addr string, port int) string {
	hostport := addr
	// Default ports are left off so the probed URL reads like the configured one.
	if !(port == 80 && t.scheme == "http") && !(port == 443 && t.scheme == "https") {
		hostport = net.JoinHostPort(addr, strconv.Itoa(port))
	}
	u := url.URL{
		Scheme:   t.scheme,
		Host:     hostport,
		Path:     t.path,
		RawQuery: t.query,
	}
	return u.String()
}

// batteryProbe asks the battery's own driver whether the device at an address is
// the battery. A status read is the right question: it is read-only, it needs the
// token, and it returns the state of charge — which is what tells an operator
// that the thing found is their battery and not another of the same model.
func batteryProbe(battery entity.BatteryConfig, target batteryTarget, port int, log *slog.Logger) netscan.Probe {
	return func(_ context.Context, addr string) (string, error) {
		cfg := battery
		cfg.Url = target.urlFor(addr, port)

		api, err := driver.New(cfg.Driver, cfg, log)
		if err != nil {
			return "", fmt.Errorf("driver %q: %w", cfg.Driver, err)
		}

		status, err := api.Status()
		if err != nil {
			return "", err
		}
		if status == nil {
			return "", fmt.Errorf("driver returned no reading")
		}

		return describeBattery(battery.Driver, status), nil
	}
}

// describeBattery renders the one line an operator reads to decide whether the
// address found is the battery they lost.
func describeBattery(driverName string, status *entity.SystemStatus) string {
	parts := []string{
		fmt.Sprintf("%s: usoc %.0f%%", driverName, status.USOC),
		fmt.Sprintf("rsoc %.0f%%", status.RSOC),
	}
	if status.RemainingCapacityWh > 0 {
		parts = append(parts, fmt.Sprintf("%.0f Wh", status.RemainingCapacityWh))
	}
	if status.OperatingMode != "" {
		parts = append(parts, "mode "+status.OperatingMode)
	}
	if status.SystemStatus != "" {
		parts = append(parts, status.SystemStatus)
	}
	return strings.Join(parts, ", ")
}
