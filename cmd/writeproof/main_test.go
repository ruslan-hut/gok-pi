package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"gok-pi/battery/driver/huawei"
	"gok-pi/internal/modbus/modbussim"
)

func newLogger(t *testing.T, commTimeout uint16) *modbussim.Server {
	t.Helper()

	srv, err := modbussim.New(map[uint16]uint16{huawei.LoggerCommTimeout.Addr: commTimeout})
	if err != nil {
		t.Fatalf("start simulator: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	return srv
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		value      uint16
		deny       bool
		extra      []string
		wantErr    string
		wantOut    string
		wantWrites int
	}{
		{name: "dry run writes nothing", value: 300, wantOut: "dry run", wantWrites: 0},
		{name: "both function codes", value: 300, extra: []string{"-yes"}, wantOut: "write path proven", wantWrites: 2},
		{name: "out of documented range", value: 0, extra: []string{"-yes"}, wantErr: "outside its documented range"},
		{name: "never set", value: 0xFFFF, extra: []string{"-yes"}, wantErr: "not set"},
		{name: "not on the allowlist", value: 300, extra: []string{"-yes", "-reg", "40381"}, wantErr: "allowlist"},
		{name: "no permission", value: 300, deny: true, extra: []string{"-yes"}, wantErr: "whitelist", wantWrites: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newLogger(t, tt.value)
			srv.DenyWrites(tt.deny)
			var out bytes.Buffer

			err := run(context.Background(), append([]string{"-addr", srv.Addr()}, tt.extra...), &out)

			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("run() error = %v\n%s", err, out.String())
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("run() error = %v, want it to mention %q", err, tt.wantErr)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("run() output missing %q\n%s", tt.wantOut, out.String())
			}
			if got := srv.Writes(); got != tt.wantWrites {
				t.Errorf("simulator saw %d writes, want %d", got, tt.wantWrites)
			}
		})
	}
}

func TestRunDetectsChangedValue(t *testing.T) {
	// A device that accepts the write but stores something else must not be
	// reported as a proven no-op.
	srv := newLogger(t, 300)
	srv.OnWrite(func(_, _ uint16) uint16 { return 60 })
	var out bytes.Buffer

	err := run(context.Background(), []string{"-addr", srv.Addr(), "-yes"}, &out)

	if err == nil || !strings.Contains(err.Error(), "restore 300 s") {
		t.Errorf("run() error = %v, want a restore instruction", err)
	}
	if got := srv.Writes(); got != 1 {
		t.Errorf("simulator saw %d writes, want 1: the run must stop at the first mismatch", got)
	}
}

func TestAllowlistIsBounded(t *testing.T) {
	// checkWritable's range guard only applies to bounded registers, so an
	// unbounded entry would be written back without it.
	for addr, reg := range allowed {
		if addr != reg.Addr {
			t.Errorf("allowed[%d] holds register %d", addr, reg.Addr)
		}
		if !reg.Bounded || !reg.Access.Writable() {
			t.Errorf("allowed register %s must be writable and have a documented range", reg)
		}
	}
}
