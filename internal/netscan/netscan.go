// Package netscan sweeps a range of IP addresses looking for a device that
// answers as a battery.
//
// It exists for one question an operator cannot answer from the agent log: when
// a battery stops responding, has it moved to a new address or is it actually
// off the air? DHCP handing the battery a new lease looks exactly like the
// battery being dead — until something walks the subnet and finds it.
//
// The sweep is deliberately two-staged. Stage one is a plain TCP connect to the
// battery's port on every address, which is cheap and bounded. Stage two runs a
// real driver probe, but only against the addresses that answered: a driver
// probe carries the vendor's own retry policy (the Sonnen driver retries five
// times with linear backoff, up to the better part of a minute), so running one
// per address across a /24 would take hours. The port check reduces the set to
// the handful of hosts that are actually listening.
//
// Nothing here knows about any particular vendor. The caller supplies the probe,
// which keeps the driver registry — and the battery token the probe needs — out
// of this package and makes the sweep testable without a battery.
package netscan

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxAddresses caps how many addresses one sweep may cover. A /24 is the shape
// this is built for; the cap is what stops a mistyped prefix (a /8, say) from
// turning a diagnostic into an afternoon of traffic on someone's network.
const MaxAddresses = 1024

// Defaults applied by Scan when Options leaves a field at zero.
const (
	DefaultPort        = 80
	DefaultDialTimeout = 400 * time.Millisecond
	DefaultConcurrency = 64

	// DefaultProbeTimeout bounds one driver probe. It is generous because a
	// probe that reaches a real battery goes through the vendor's client, and a
	// loaded battery can take seconds to answer; it is finite because a host
	// that accepts the connection and then says nothing must not hold the sweep.
	DefaultProbeTimeout = 8 * time.Second
)

// Probe asks "is the device at this address our battery?". It is given the bare
// address (no port, no scheme) and returns a short human-readable detail on
// success — whatever identifies the device to an operator reading the result.
type Probe func(ctx context.Context, addr string) (detail string, err error)

// Result is what the sweep learned about one address.
type Result struct {
	Address string `json:"address"`

	// PortOpen reports whether the TCP connect in stage one succeeded. When it
	// is false nothing else was attempted, and the address is only reported at
	// all if the caller asked for the full list.
	PortOpen bool `json:"port_open"`

	// Battery is true when the probe recognised a battery here. This is the
	// field the whole sweep exists to produce.
	Battery bool `json:"battery"`

	// Detail is the probe's identification on success, or why it failed. For a
	// host that is listening but is not a battery this is the thing that tells
	// an operator what they found instead — an HTTP 404 from whatever picked up
	// the battery's old lease, say.
	Detail string `json:"detail,omitempty"`

	LatencyMs int64 `json:"latency_ms,omitempty"`
}

// Options configures one sweep.
type Options struct {
	// Addresses to examine, normally from ParseRange.
	Addresses []netip.Addr

	// Port is the TCP port the stage-one connect targets.
	Port int

	DialTimeout  time.Duration
	ProbeTimeout time.Duration
	Concurrency  int

	// Probe identifies a battery among the hosts that answered. A nil Probe
	// reduces the sweep to a port scan, which is still enough to tell "something
	// is at this address" from "nothing is".
	Probe Probe
}

// Summary reports a finished sweep.
type Summary struct {
	// Results holds every address that answered the port check, plus any address
	// whose probe said something worth reading. Addresses that answered nothing
	// are counted in Scanned rather than listed: on a /24 they are the other 250
	// entries and they carry no information.
	Results []Result `json:"results"`

	Scanned   int           `json:"scanned"`
	Responded int           `json:"responded"`
	Found     int           `json:"found"`
	Elapsed   time.Duration `json:"-"`
	ElapsedMs int64         `json:"elapsed_ms"`

	// Truncated is set when the sweep stopped early because its context ended.
	Truncated bool `json:"truncated,omitempty"`
}

// Scan sweeps the addresses in opts and reports what answered. It returns what
// it has when ctx ends rather than discarding the work, with Truncated set:
// a partial answer naming the address the battery moved to is the whole point,
// and it is worth having even if the sweep did not finish.
func Scan(ctx context.Context, opts Options) Summary {
	started := time.Now()

	port := opts.Port
	if port <= 0 {
		port = DefaultPort
	}
	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}
	probeTimeout := opts.ProbeTimeout
	if probeTimeout <= 0 {
		probeTimeout = DefaultProbeTimeout
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	if concurrency > len(opts.Addresses) && len(opts.Addresses) > 0 {
		concurrency = len(opts.Addresses)
	}

	// Stage one: who is listening. Every address is dialled, so this is where
	// the concurrency limit earns its keep — a /24 at 64 in flight is four
	// rounds of DialTimeout in the worst case.
	type stageOne struct {
		addr    netip.Addr
		open    bool
		latency time.Duration
		err     error
	}

	var (
		mu        sync.Mutex
		responded []stageOne
		scanned   int
		truncated bool
	)

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, addr := range opts.Addresses {
		if ctx.Err() != nil {
			truncated = true
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(addr netip.Addr) {
			defer wg.Done()
			defer func() { <-sem }()

			target := net.JoinHostPort(addr.String(), strconv.Itoa(port))
			t0 := time.Now()
			dialer := net.Dialer{Timeout: dialTimeout}
			conn, err := dialer.DialContext(ctx, "tcp", target)
			latency := time.Since(t0)
			if err == nil {
				_ = conn.Close()
			}

			mu.Lock()
			scanned++
			if err == nil {
				responded = append(responded, stageOne{addr: addr, open: true, latency: latency})
			}
			mu.Unlock()
		}(addr)
	}
	wg.Wait()

	// Stage two: of those listening, which one is the battery. The probe is the
	// expensive half, and it runs serially: there are only ever a few responders,
	// and a battery answering a status request while another probe hammers it is
	// how a scan turns into the outage it was meant to diagnose.
	results := make([]Result, 0, len(responded))
	found := 0
	for _, r := range responded {
		res := Result{
			Address:   r.addr.String(),
			PortOpen:  true,
			LatencyMs: r.latency.Milliseconds(),
		}

		if opts.Probe == nil {
			res.Detail = fmt.Sprintf("port %d open", port)
			results = append(results, res)
			continue
		}
		if ctx.Err() != nil {
			truncated = true
			res.Detail = "not probed: scan cancelled"
			results = append(results, res)
			continue
		}

		detail, err := runProbe(ctx, opts.Probe, r.addr.String(), probeTimeout)
		switch {
		case err != nil:
			res.Detail = err.Error()
		default:
			res.Battery = true
			res.Detail = detail
			found++
		}
		results = append(results, res)
	}

	sort.Slice(results, func(i, j int) bool {
		a, _ := netip.ParseAddr(results[i].Address)
		b, _ := netip.ParseAddr(results[j].Address)
		return a.Less(b)
	})

	elapsed := time.Since(started)
	return Summary{
		Results:   results,
		Scanned:   scanned,
		Responded: len(responded),
		Found:     found,
		Elapsed:   elapsed,
		ElapsedMs: elapsed.Milliseconds(),
		Truncated: truncated || ctx.Err() != nil,
	}
}

// runProbe bounds one probe by timeout. The driver interface takes no context,
// so a probe that ignores its deadline cannot be interrupted — it is abandoned
// instead, and finishes into a channel nobody reads. That is bounded waste (one
// goroutine per listening host, released when the vendor client gives up) and it
// keeps one unresponsive host from stalling the sweep.
func runProbe(ctx context.Context, probe Probe, addr string, timeout time.Duration) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type outcome struct {
		detail string
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		detail, err := probe(probeCtx, addr)
		done <- outcome{detail: detail, err: err}
	}()

	select {
	case res := <-done:
		return res.detail, res.err
	case <-probeCtx.Done():
		return "", fmt.Errorf("probe timed out after %s", timeout)
	}
}

// ParseRange turns an operator-typed range into addresses. It accepts, in any
// comma-separated combination:
//
//	192.168.68.0/24              a CIDR prefix (host addresses only)
//	192.168.68.100-150           a short range over the last octet
//	192.168.68.100-192.168.68.150  an explicit range
//	192.168.68.107               a single address
//
// Only IPv4 is accepted: the batteries this sweeps for are on IPv4 LANs, and an
// IPv6 prefix is large enough that walking it is never what the operator meant.
func ParseRange(spec string) ([]netip.Addr, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("empty address range")
	}

	seen := make(map[netip.Addr]struct{})
	var out []netip.Addr

	add := func(a netip.Addr) error {
		if _, dup := seen[a]; dup {
			return nil
		}
		if len(out) >= MaxAddresses {
			return fmt.Errorf("range covers more than %d addresses; narrow it", MaxAddresses)
		}
		seen[a] = struct{}{}
		out = append(out, a)
		return nil
	}

	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		addrs, err := parsePart(part)
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			if err := add(a); err != nil {
				return nil, err
			}
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("address range %q covers no addresses", spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out, nil
}

func parsePart(part string) ([]netip.Addr, error) {
	if strings.Contains(part, "/") {
		return parseCIDR(part)
	}
	if i := strings.Index(part, "-"); i >= 0 {
		return parseDash(part[:i], part[i+1:])
	}

	addr, err := parseIPv4(part)
	if err != nil {
		return nil, err
	}
	return []netip.Addr{addr}, nil
}

func parseCIDR(part string) ([]netip.Addr, error) {
	prefix, err := netip.ParsePrefix(part)
	if err != nil {
		return nil, fmt.Errorf("invalid prefix %q: %w", part, err)
	}
	if !prefix.Addr().Is4() {
		return nil, fmt.Errorf("prefix %q is not IPv4", part)
	}

	prefix = prefix.Masked()
	hostBits := 32 - prefix.Bits()
	if hostBits > 16 {
		return nil, fmt.Errorf("prefix %q covers more than %d addresses; narrow it", part, MaxAddresses)
	}

	var out []netip.Addr
	for a := prefix.Addr(); prefix.Contains(a); a = a.Next() {
		out = append(out, a)
	}

	// Drop the network and broadcast addresses: no host answers on either, and
	// including them only adds two guaranteed timeouts to every sweep. A /31 or
	// /32 has no such addresses to drop.
	if hostBits >= 2 && len(out) >= 2 {
		out = out[1 : len(out)-1]
	}
	if len(out) > MaxAddresses {
		return nil, fmt.Errorf("prefix %q covers more than %d addresses; narrow it", part, MaxAddresses)
	}
	return out, nil
}

func parseDash(lo, hi string) ([]netip.Addr, error) {
	lo, hi = strings.TrimSpace(lo), strings.TrimSpace(hi)

	start, err := parseIPv4(lo)
	if err != nil {
		return nil, err
	}

	var end netip.Addr
	if strings.Contains(hi, ".") {
		end, err = parseIPv4(hi)
		if err != nil {
			return nil, err
		}
	} else {
		// Short form: "192.168.68.100-150" means the last octet runs to 150.
		last, convErr := strconv.Atoi(hi)
		if convErr != nil || last < 0 || last > 255 {
			return nil, fmt.Errorf("invalid range end %q", hi)
		}
		octets := start.As4()
		octets[3] = byte(last)
		end = netip.AddrFrom4(octets)
	}

	if end.Less(start) {
		return nil, fmt.Errorf("range %s-%s ends before it starts", lo, hi)
	}

	var out []netip.Addr
	for a := start; ; a = a.Next() {
		out = append(out, a)
		if a == end {
			break
		}
		if len(out) > MaxAddresses {
			return nil, fmt.Errorf("range %s-%s covers more than %d addresses; narrow it", lo, hi, MaxAddresses)
		}
	}
	return out, nil
}

func parseIPv4(s string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid address %q: %w", s, err)
	}
	if !addr.Is4() {
		return netip.Addr{}, fmt.Errorf("address %q is not IPv4", s)
	}
	return addr, nil
}

// SubnetOf returns the /24 containing host, which is the range an operator means
// by "look for it where it used to be". A battery that got a new DHCP lease is
// almost always still on the same subnet; the exceptions are worth typing a
// range for by hand.
func SubnetOf(host string) (string, error) {
	addr, err := parseIPv4(host)
	if err != nil {
		return "", err
	}
	octets := addr.As4()
	octets[3] = 0
	return netip.PrefixFrom(netip.AddrFrom4(octets), 24).String(), nil
}
