package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gok-pi/battery/driver/huawei"
	"gok-pi/internal/modbus"
	"gok-pi/internal/modbus/modbussim"
)

// fakeESS is a simulator seeded so every register the probe reads answers with
// a plausible value for a 215 kWh cabinet at 62% state of charge, discharging.
type fakeESS struct {
	*modbussim.Server
}

func newFakeESS(t *testing.T) *fakeESS {
	t.Helper()

	srv, err := modbussim.New(nil)
	if err != nil {
		t.Fatalf("start simulator: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	f := &fakeESS{Server: srv}

	// Answer every address the probe's batched reads touch, not just the
	// documented ones. Reads are packed into contiguous blocks that span the
	// gaps between registers, so a device that refused those gaps would fail
	// whole blocks — which is exactly what makes DefaultGap worth testing.
	seedBlocks(f, huawei.AllRegisters())
	seedBlocks(f, huawei.ObservationRegisters())

	f.set(t, huawei.RatedCapacity, 215)
	f.set(t, huawei.RatedPower, 107.5)
	f.set(t, huawei.MaxActivePower, 107.5)
	f.set(t, huawei.MaxReverseRectificationPower, -107.5)
	f.set(t, huawei.SOC, 62)
	f.set(t, huawei.ControlSOC, 62)
	f.set(t, huawei.SOH, 99)
	f.set(t, huawei.ChargeDischargePower, 50)
	f.set(t, huawei.ChargeableCapacity, 80)
	f.set(t, huawei.DischargeableCapacity, 130)
	f.set(t, huawei.GridFrequency, 50)
	f.set(t, huawei.RackVoltage, 780.5)
	f.set(t, huawei.RackCurrent, -64)
	f.set(t, huawei.ChargeCutOffSOC, 95)
	f.set(t, huawei.DischargeCutOffSOC, 10)
	f.set(t, huawei.ActivePowerSetpoint, 50)

	f.SetWords(huawei.WorkStatus.Addr, []uint16{huawei.WorkRunningPQ})
	f.SetWords(huawei.PowerOnOff.Addr, []uint16{huawei.PowerStateRun})
	f.SetWords(huawei.WorkingMode.Addr, []uint16{huawei.ModePQ})

	f.SetObject(modbus.ObjectVendor, "Huawei")
	f.SetObject(modbus.ObjectProductCode, "LUNA2000B")
	f.SetObject(modbus.ObjectRevision, "V200R024C00SPC310")
	f.SetObject(modbus.ObjectDeviceCount, "2")
	f.SetObject(0x88, "1=LUNA2000-215-2S11;2=V200R024C00SPC310;3=P1.0-D5.0;4=ESN0001;5=0;8=LUNA2000-P")
	f.SetObject(0x8A, "1=SUN2000-100KTL;2=V800R021C10;4=ESN0002;5=1;8=SUN2000")

	return f
}

// seedBlocks gives the simulator a value for every address covered by the read
// blocks of regs, mirroring a device that answers a contiguous range rather
// than only the addresses its documentation lists.
func seedBlocks(f *fakeESS, regs []huawei.Register) {
	for _, b := range huawei.Blocks(regs, huawei.DefaultGap) {
		f.SetWords(b.Addr, make([]uint16, b.Count))
	}
}

// set writes an engineering value through the register's own codec, so the
// simulator holds exactly the words a real device would.
func (f *fakeESS) set(t *testing.T, r huawei.Register, v float64) {
	t.Helper()

	raw := int64(v * float64(r.Gain))
	words := make([]uint16, r.Words())
	u := uint64(raw)
	for i := len(words) - 1; i >= 0; i-- {
		words[i] = uint16(u)
		u >>= 16
	}
	f.SetWords(r.Addr, words)
}

func newReader(f *fakeESS) *reader {
	return &reader{client: modbus.New(f.Addr(), 2*time.Second), retries: 1}
}

func TestIdentify(t *testing.T) {
	f := newFakeESS(t)
	var out bytes.Buffer

	if err := identify(context.Background(), newReader(f), &out); err != nil {
		t.Fatalf("identify() error = %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"Huawei", "LUNA2000B", "V200R024C00SPC310",
		"2 device(s) reported",
		"LUNA2000-215-2S11", "SUN2000-100KTL",
		"this is the device holding the Modbus card",
		"215", "Running: PQ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("identify() output missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "IMPLAUSIBLE") {
		t.Errorf("identify() flagged a plausible device as implausible\n%s", got)
	}
}

func TestIdentifyFlagsWrongMapping(t *testing.T) {
	f := newFakeESS(t)
	// A device whose rated capacity decodes to millions: the point table does
	// not describe this equipment, or the gain is wrong.
	f.SetWords(huawei.RatedCapacity.Addr, []uint16{0xFFFF, 0xFFFF})

	var out bytes.Buffer
	if err := identify(context.Background(), newReader(f), &out); err != nil {
		t.Fatalf("identify() error = %v", err)
	}

	if !strings.Contains(out.String(), "IMPLAUSIBLE") {
		t.Errorf("identify() did not flag an implausible nameplate\n%s", out.String())
	}
}

func TestDumpTable(t *testing.T) {
	f := newFakeESS(t)
	var out bytes.Buffer

	if err := dump(context.Background(), newReader(f), &out, false, true); err != nil {
		t.Fatalf("dump() error = %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"ADDR", "Rated capacity", "215", "Running: PQ",
		"positive (discharging", "0 failed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dump() output missing %q", want)
		}
	}
}

func TestDumpJSON(t *testing.T) {
	f := newFakeESS(t)
	var out bytes.Buffer

	if err := dump(context.Background(), newReader(f), &out, true, false); err != nil {
		t.Fatalf("dump() error = %v", err)
	}

	var doc struct {
		Endpoint  string `json:"endpoint"`
		Registers []struct {
			Addr  uint16   `json:"addr"`
			Name  string   `json:"name"`
			Value *float64 `json:"value"`
			Error string   `json:"error"`
		} `json:"registers"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("dump(-json) produced invalid JSON: %v", err)
	}

	if doc.Endpoint != f.Addr() {
		t.Errorf("endpoint = %q, want %q", doc.Endpoint, f.Addr())
	}

	found := false
	for _, r := range doc.Registers {
		if r.Addr != huawei.RatedCapacity.Addr {
			continue
		}
		found = true
		if r.Value == nil || *r.Value != 215 {
			t.Errorf("rated capacity = %v, want 215", r.Value)
		}
	}
	if !found {
		t.Error("rated capacity missing from the JSON dump")
	}
}

func TestDumpReportsUnreadableRegisters(t *testing.T) {
	srv, err := modbussim.New(map[uint16]uint16{huawei.SOC.Addr: 55})
	if err != nil {
		t.Fatalf("start simulator: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	var out bytes.Buffer
	r := &reader{client: modbus.New(srv.Addr(), time.Second), retries: 0}

	// Almost everything is missing, but the pass must still report what it got.
	if err := dump(context.Background(), r, &out, false, false); err != nil {
		t.Fatalf("dump() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "55") {
		t.Errorf("dump() lost the one readable register\n%s", got)
	}
	if !strings.Contains(got, "failed") {
		t.Error("dump() did not report the unreadable registers")
	}
}

func TestAlarms(t *testing.T) {
	f := newFakeESS(t)
	// 3100 is register 32133 bit 0; 3113 is bit 13 of the same word.
	f.Set(32133, 1<<0|1<<13)

	var out bytes.Buffer
	if err := alarms(context.Background(), newReader(f), &out); err != nil {
		t.Fatalf("alarms() error = %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "3100") || !strings.Contains(got, "3113") {
		t.Errorf("alarms() missed a raised alarm\n%s", got)
	}
	if !strings.Contains(got, "2 alarm(s) raised") {
		t.Errorf("alarms() miscounted\n%s", got)
	}
}

func TestAlarmsQuietSite(t *testing.T) {
	f := newFakeESS(t)
	var out bytes.Buffer

	if err := alarms(context.Background(), newReader(f), &out); err != nil {
		t.Fatalf("alarms() error = %v", err)
	}
	if !strings.Contains(out.String(), "no alarms raised") {
		t.Errorf("alarms() on a quiet site:\n%s", out.String())
	}
}

func TestAlarmsReportsUndocumentedBits(t *testing.T) {
	f := newFakeESS(t)
	// 30463 carries no documented bits in issue 01 of the point table.
	f.Set(30463, 0x0004)

	var out bytes.Buffer
	if err := alarms(context.Background(), newReader(f), &out); err != nil {
		t.Fatalf("alarms() error = %v", err)
	}
	if !strings.Contains(out.String(), "undocumented bits") {
		t.Errorf("alarms() did not report an undocumented bit\n%s", out.String())
	}
}

func TestWatchWritesSamples(t *testing.T) {
	f := newFakeESS(t)
	path := filepath.Join(t.TempDir(), "obs.jsonl")

	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
	defer cancel()

	err := watch(ctx, newReader(f), watchOptions{interval: time.Second, path: path, log: &log})
	if err != nil {
		t.Fatalf("watch() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read samples: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("got %d samples, want at least 2", len(lines))
	}

	var obs observation
	if err := json.Unmarshal([]byte(lines[0]), &obs); err != nil {
		t.Fatalf("sample is not valid JSON: %v", err)
	}
	if obs.Values["SOC"] != 62 {
		t.Errorf("sample SOC = %v, want 62", obs.Values["SOC"])
	}
	if obs.Setpoints["Active power (kW)"] != 50 {
		t.Errorf("sample setpoint = %v, want 50", obs.Setpoints["Active power (kW)"])
	}
}

func TestWatchRejectsImpoliteInterval(t *testing.T) {
	f := newFakeESS(t)

	err := watch(context.Background(), newReader(f), watchOptions{interval: time.Millisecond, log: &bytes.Buffer{}})
	if err == nil {
		t.Error("watch() accepted a sub-second interval")
	}
}

func TestTrackerDetectsAnotherMaster(t *testing.T) {
	tr := newTracker()
	var log bytes.Buffer

	s1 := newSample()
	s1.Values[huawei.ActivePowerSetpoint.Addr] = 50
	tr.observe(s1, &log)

	// Nothing here writes, so a changed setpoint came from somewhere else.
	s2 := newSample()
	s2.Values[huawei.ActivePowerSetpoint.Addr] = -30
	tr.observe(s2, &log)

	if !strings.Contains(log.String(), "ANOTHER MASTER WROTE") {
		t.Errorf("tracker missed a setpoint change\n%s", log.String())
	}

	var sum bytes.Buffer
	tr.summarise(&sum)
	if !strings.Contains(sum.String(), "another master is dispatching") {
		t.Errorf("summary did not report the competing master\n%s", sum.String())
	}
}

func TestTrackerQuietOnStableSetpoints(t *testing.T) {
	tr := newTracker()
	var log bytes.Buffer

	for range 3 {
		s := newSample()
		s.Values[huawei.ActivePowerSetpoint.Addr] = 50
		tr.observe(s, &log)
	}

	if strings.Contains(log.String(), "ANOTHER MASTER") {
		t.Errorf("tracker cried wolf on an unchanged setpoint\n%s", log.String())
	}

	var sum bytes.Buffer
	tr.summarise(&sum)
	if !strings.Contains(sum.String(), "no setpoint changed") {
		t.Errorf("summary:\n%s", sum.String())
	}
}

func TestTrackerPolarity(t *testing.T) {
	tests := []struct {
		name    string
		samples [][2]float64 // soc, power
		want    string
	}{
		{
			name:    "positive power with falling SOC matches the documented reading",
			samples: [][2]float64{{80, 50}, {79, 50}, {78, 50}, {77, 50}},
			want:    "consistent with positive = discharge",
		},
		{
			name:    "positive power with rising SOC means the polarity is inverted",
			samples: [][2]float64{{70, 50}, {71, 50}, {72, 50}, {73, 50}},
			want:    "INVERTED",
		},
		{
			name:    "an idle battery yields no verdict",
			samples: [][2]float64{{70, 0}, {70, 0}, {70, 0}},
			want:    "not enough movement",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTracker()
			var log bytes.Buffer

			for _, s := range tt.samples {
				smp := newSample()
				smp.Values[huawei.SOC.Addr] = s[0]
				smp.Values[huawei.ChargeDischargePower.Addr] = s[1]
				tr.observe(smp, &log)
			}

			var sum bytes.Buffer
			tr.summarise(&sum)
			if !strings.Contains(sum.String(), tt.want) {
				t.Errorf("summary missing %q\n%s", tt.want, sum.String())
			}
		})
	}
}

func TestTrackerReportsAlarmTransitions(t *testing.T) {
	tr := newTracker()
	var log bytes.Buffer

	quiet := newSample()
	quiet.Raw[32133] = 0
	tr.observe(quiet, &log)

	raised := newSample()
	raised.Raw[32133] = 1 << 0
	tr.observe(raised, &log)

	cleared := newSample()
	cleared.Raw[32133] = 0
	tr.observe(cleared, &log)

	got := log.String()
	if !strings.Contains(got, "alarm raised: 3100") {
		t.Errorf("tracker missed a raised alarm\n%s", got)
	}
	if !strings.Contains(got, "alarm cleared: 3100") {
		t.Errorf("tracker missed a cleared alarm\n%s", got)
	}
}

func TestReaderRetriesTransientExceptions(t *testing.T) {
	f := newFakeESS(t)
	f.FailEvery(2) // every other request answers "slave busy"

	r := newReader(f)
	s, err := r.read(context.Background(), []huawei.Register{huawei.SOC, huawei.RatedCapacity})
	if err != nil {
		t.Fatalf("read() error = %v", err)
	}
	if v, ok := s.value(huawei.SOC); !ok || v != 62 {
		t.Errorf("SOC = %v (ok=%v), want 62", v, ok)
	}
}

func TestReaderFailsWhenNothingIsReadable(t *testing.T) {
	srv, err := modbussim.New(nil)
	if err != nil {
		t.Fatalf("start simulator: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	r := &reader{client: modbus.New(srv.Addr(), time.Second), retries: 0}
	if _, err := r.read(context.Background(), []huawei.Register{huawei.SOC}); err == nil {
		t.Error("read() succeeded against a device that answers nothing")
	}
}
