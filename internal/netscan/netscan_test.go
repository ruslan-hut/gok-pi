package netscan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func addrs(t *testing.T, spec string) []netip.Addr {
	t.Helper()
	got, err := ParseRange(spec)
	if err != nil {
		t.Fatalf("ParseRange(%q): %v", spec, err)
	}
	return got
}

func TestParseRangeCIDRDropsNetworkAndBroadcast(t *testing.T) {
	got := addrs(t, "192.168.68.0/24")

	if len(got) != 254 {
		t.Fatalf("expected 254 host addresses, got %d", len(got))
	}
	if first := got[0].String(); first != "192.168.68.1" {
		t.Errorf("first address = %s, want 192.168.68.1", first)
	}
	if last := got[len(got)-1].String(); last != "192.168.68.254" {
		t.Errorf("last address = %s, want 192.168.68.254", last)
	}
}

func TestParseRangeCIDRUnaligned(t *testing.T) {
	// A prefix written from a host address still means its whole subnet.
	got := addrs(t, "192.168.68.107/24")
	if len(got) != 254 || got[0].String() != "192.168.68.1" {
		t.Fatalf("got %d addresses starting at %s", len(got), got[0])
	}
}

func TestParseRangeSmallPrefixes(t *testing.T) {
	// A /32 and a /31 have no network/broadcast pair to drop.
	if got := addrs(t, "192.168.68.107/32"); len(got) != 1 {
		t.Errorf("/32 gave %d addresses, want 1", len(got))
	}
	if got := addrs(t, "192.168.68.106/31"); len(got) != 2 {
		t.Errorf("/31 gave %d addresses, want 2", len(got))
	}
}

func TestParseRangeForms(t *testing.T) {
	tests := []struct {
		spec  string
		count int
		first string
		last  string
	}{
		{"192.168.68.107", 1, "192.168.68.107", "192.168.68.107"},
		{"192.168.68.100-150", 51, "192.168.68.100", "192.168.68.150"},
		{"192.168.68.100-192.168.68.105", 6, "192.168.68.100", "192.168.68.105"},
		{"10.0.0.5-10.0.1.4", 256, "10.0.0.5", "10.0.1.4"},
		{" 192.168.68.1 , 192.168.68.3 ", 2, "192.168.68.1", "192.168.68.3"},
	}

	for _, tc := range tests {
		got := addrs(t, tc.spec)
		if len(got) != tc.count {
			t.Errorf("%q: got %d addresses, want %d", tc.spec, len(got), tc.count)
			continue
		}
		if got[0].String() != tc.first {
			t.Errorf("%q: first = %s, want %s", tc.spec, got[0], tc.first)
		}
		if got[len(got)-1].String() != tc.last {
			t.Errorf("%q: last = %s, want %s", tc.spec, got[len(got)-1], tc.last)
		}
	}
}

func TestParseRangeDeduplicatesAndSorts(t *testing.T) {
	got := addrs(t, "192.168.68.10,192.168.68.5,192.168.68.10,192.168.68.5-7")

	want := []string{"192.168.68.5", "192.168.68.6", "192.168.68.7", "192.168.68.10"}
	if len(got) != len(want) {
		t.Fatalf("got %d addresses (%v), want %d", len(got), got, len(want))
	}
	for i, w := range want {
		if got[i].String() != w {
			t.Errorf("address %d = %s, want %s", i, got[i], w)
		}
	}
}

func TestParseRangeRejects(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{"empty", "   "},
		{"garbage", "not-an-address"},
		{"bad octet", "192.168.68.999"},
		{"prefix too wide", "10.0.0.0/8"},
		{"reversed range", "192.168.68.150-100"},
		{"ipv6 address", "fd00::1"},
		{"ipv6 prefix", "fd00::/64"},
		{"range end out of bounds", "192.168.68.1-999"},
	}

	for _, tc := range tests {
		if _, err := ParseRange(tc.spec); err == nil {
			t.Errorf("%s: ParseRange(%q) accepted an invalid range", tc.name, tc.spec)
		}
	}
}

func TestParseRangeHonoursMaxAddresses(t *testing.T) {
	// Two /24s in one spec exceed the cap and must be refused rather than
	// silently truncated: a sweep that quietly skips half the subnet would
	// report "not found" about addresses it never looked at.
	if _, err := ParseRange("10.0.0.0/23,10.0.2.0/23,10.0.4.0/23"); err == nil {
		t.Fatal("expected an error for a spec over MaxAddresses")
	} else if !strings.Contains(err.Error(), "narrow it") {
		t.Errorf("error %q does not tell the operator what to do", err)
	}
}

func TestSubnetOf(t *testing.T) {
	got, err := SubnetOf("192.168.68.107")
	if err != nil {
		t.Fatalf("SubnetOf: %v", err)
	}
	if got != "192.168.68.0/24" {
		t.Errorf("SubnetOf = %s, want 192.168.68.0/24", got)
	}

	if _, err := SubnetOf("battery.local"); err == nil {
		t.Error("SubnetOf accepted a hostname")
	}
}

// listenLocal opens a listener on the loopback address and returns its port.
func listenLocal(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, ln.Addr().(*net.TCPAddr).Port
}

func TestScanFindsBatteryBehindOpenPort(t *testing.T) {
	_, port := listenLocal(t)

	var probed int32
	summary := Scan(context.Background(), Options{
		// 127.0.0.2 is on the loopback net but nothing listens there, so it
		// stands in for the 253 addresses that answer nothing.
		Addresses:   addrs(t, "127.0.0.1,127.0.0.2"),
		Port:        port,
		DialTimeout: 200 * time.Millisecond,
		Probe: func(_ context.Context, addr string) (string, error) {
			atomic.AddInt32(&probed, 1)
			return "sonnen, usoc 61%", nil
		},
	})

	if summary.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2", summary.Scanned)
	}
	if summary.Responded != 1 {
		t.Fatalf("Responded = %d, want 1", summary.Responded)
	}
	if summary.Found != 1 {
		t.Errorf("Found = %d, want 1", summary.Found)
	}
	if n := atomic.LoadInt32(&probed); n != 1 {
		t.Errorf("probe ran %d times; only listening hosts should be probed", n)
	}

	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	res := summary.Results[0]
	if res.Address != "127.0.0.1" || !res.PortOpen || !res.Battery {
		t.Errorf("unexpected result: %+v", res)
	}
	if res.Detail != "sonnen, usoc 61%" {
		t.Errorf("Detail = %q, want the probe's identification", res.Detail)
	}
}

func TestScanReportsListeningNonBattery(t *testing.T) {
	_, port := listenLocal(t)

	summary := Scan(context.Background(), Options{
		Addresses:   addrs(t, "127.0.0.1"),
		Port:        port,
		DialTimeout: 200 * time.Millisecond,
		Probe: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("received status code: 404")
		},
	})

	if summary.Found != 0 {
		t.Errorf("Found = %d, want 0", summary.Found)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected the listening host to be reported, got %d results", len(summary.Results))
	}
	res := summary.Results[0]
	if !res.PortOpen || res.Battery {
		t.Errorf("unexpected result: %+v", res)
	}
	// The whole point of listing a non-battery: it names what took the lease.
	if !strings.Contains(res.Detail, "404") {
		t.Errorf("Detail = %q, want the probe's error", res.Detail)
	}
}

func TestScanWithoutProbeIsPortScan(t *testing.T) {
	_, port := listenLocal(t)

	summary := Scan(context.Background(), Options{
		Addresses:   addrs(t, "127.0.0.1"),
		Port:        port,
		DialTimeout: 200 * time.Millisecond,
	})

	if summary.Responded != 1 || summary.Found != 0 {
		t.Fatalf("Responded = %d, Found = %d; want 1 and 0", summary.Responded, summary.Found)
	}
	if got := summary.Results[0].Detail; !strings.Contains(got, "open") {
		t.Errorf("Detail = %q, want it to say the port is open", got)
	}
}

func TestScanAbandonsHangingProbe(t *testing.T) {
	_, port := listenLocal(t)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	started := time.Now()
	summary := Scan(context.Background(), Options{
		Addresses:    addrs(t, "127.0.0.1"),
		Port:         port,
		DialTimeout:  200 * time.Millisecond,
		ProbeTimeout: 150 * time.Millisecond,
		Probe: func(_ context.Context, _ string) (string, error) {
			// Stands in for a vendor client that ignores its deadline and
			// retries for the better part of a minute.
			<-release
			return "too late", nil
		},
	})
	elapsed := time.Since(started)

	if elapsed > 3*time.Second {
		t.Fatalf("scan waited %s for a hanging probe", elapsed)
	}
	if summary.Found != 0 {
		t.Errorf("Found = %d; a probe that never answered is not a battery", summary.Found)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	if got := summary.Results[0].Detail; !strings.Contains(got, "timed out") {
		t.Errorf("Detail = %q, want a timeout explanation", got)
	}
}

func TestScanReportsPartialResultOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	summary := Scan(ctx, Options{
		Addresses:   addrs(t, "192.168.68.0/24"),
		Port:        80,
		DialTimeout: 50 * time.Millisecond,
	})

	if !summary.Truncated {
		t.Error("Truncated = false on a cancelled scan")
	}
	if summary.Scanned >= 254 {
		t.Errorf("Scanned = %d; a cancelled scan should stop early", summary.Scanned)
	}
}

func TestScanRecordsLatency(t *testing.T) {
	_, port := listenLocal(t)

	summary := Scan(context.Background(), Options{
		Addresses:   addrs(t, "127.0.0.1"),
		Port:        port,
		DialTimeout: time.Second,
		Probe: func(_ context.Context, addr string) (string, error) {
			return fmt.Sprintf("found at %s", addr), nil
		},
	})

	if summary.ElapsedMs < 0 {
		t.Errorf("ElapsedMs = %d", summary.ElapsedMs)
	}
	if summary.Results[0].LatencyMs < 0 {
		t.Errorf("LatencyMs = %d", summary.Results[0].LatencyMs)
	}
}
