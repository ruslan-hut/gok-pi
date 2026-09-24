package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"gok-pi/battery/driver/huawei"
	"gok-pi/internal/modbus"
	"gok-pi/internal/modbus/modbussim"
)

// newFakeLogger is a simulator seeded as the Pedernoso SmartLogger looks today:
// two 215 kWh cabinets, Export Limitation active, the ESS charging from PV.
func newFakeLogger(t *testing.T) *fakeESS {
	t.Helper()

	srv, err := modbussim.New(nil)
	if err != nil {
		t.Fatalf("start simulator: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	f := &fakeESS{Server: srv}
	seedBlocks(f, huawei.LoggerAllRegisters())

	f.set(t, huawei.LoggerRatedESSCapacity, 430.08)
	f.set(t, huawei.LoggerRatedESSPower, 280.8)
	f.set(t, huawei.LoggerNumberOfESSs, 2)
	f.set(t, huawei.LoggerNumberOfPCSs, 2)
	f.set(t, huawei.LoggerRunningPCSs, 2)
	f.set(t, huawei.LoggerMaxActivePowerAdjustment, 330.8)
	f.set(t, huawei.LoggerMinActivePowerAdjustment, -280.8)
	f.set(t, huawei.LoggerSOC, 92.3)
	f.set(t, huawei.LoggerESSActivePower, -39.3)
	f.set(t, huawei.LoggerArrayChargeEndSOC, 100)
	f.set(t, huawei.LoggerArrayDischargeEndSOC, 5)
	f.set(t, huawei.LoggerTotalEnergyCharged, 123456.78)
	f.SetWords(huawei.LoggerActivePowerControlMode.Addr, []uint16{huawei.ControlModeLimitedPower})
	f.SetWords(huawei.LoggerActivePowerControlMethod.Addr, []uint16{huawei.ControlModeLimitedPower})
	f.SetWords(huawei.LoggerHighestPriorityActivePower.Addr, []uint16{0x7FFF, 0xFFFF})

	f.SetObject(modbus.ObjectVendor, "HUAWEI")
	f.SetObject(modbus.ObjectProductCode, "SmartLogger")
	f.SetObject(modbus.ObjectRevision, "V300R024C10SPC161")

	return f
}

func TestIdentifyLogger(t *testing.T) {
	f := newFakeLogger(t)
	var out bytes.Buffer

	if err := identify(context.Background(), newReader(f), loggerDevice, &out); err != nil {
		t.Fatalf("identify() error = %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"read as logger", "SmartLogger",
		"430.08", "sum of the cabinets' rated capacity", "280.8",
		"grid connection with limited power (kW)", "if it follows 40737",
		"no override", "40381/40383",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("identify() output missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "IMPLAUSIBLE") || strings.Contains(got, "failed") {
		t.Errorf("identify() flagged a healthy logger\n%s", got)
	}
}

func TestIdentifyWrongDevice(t *testing.T) {
	// Pointing the cabinet table at a logger is the mistake -device exists to
	// avoid; it must show up as unreadable registers, not as plausible values.
	f := newFakeLogger(t)
	var out bytes.Buffer

	err := identify(context.Background(), newReader(f), essDevice, &out)
	if err == nil && !strings.Contains(out.String(), "-device does not match") {
		t.Errorf("identify() with the wrong table did not say so\n%s", out.String())
	}
}

func TestDumpLoggerAll(t *testing.T) {
	f := newFakeLogger(t)
	var out bytes.Buffer

	if err := dump(context.Background(), newReader(f), loggerDevice, &out, false, true); err != nil {
		t.Fatalf("dump() error = %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"Rated ESS capacity", "430.08", "123456.78",
		"negative (convention unconfirmed)", "0 failed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dump() output missing %q\n%s", want, got)
		}
	}
}

func TestLoggerAlarms(t *testing.T) {
	f := newFakeLogger(t)
	f.Set(50000, 1<<4)  // 1100-5: dispatch commands missing
	f.Set(50007, 1<<13) // 1154-9: BMS communication

	var out bytes.Buffer
	if err := alarms(context.Background(), newReader(f), loggerDevice, &out); err != nil {
		t.Fatalf("alarms() error = %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"8 of 8 alarm words", "1100-5", "no or abnormal remote dispatch commands",
		"1154-9", "BMS", "2 alarm(s) raised",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("alarms() output missing %q\n%s", want, got)
		}
	}
}

func TestMeterHasNoAlarms(t *testing.T) {
	f := newFakeLogger(t)
	if err := alarms(context.Background(), newReader(f), meterDevice, &bytes.Buffer{}); err == nil {
		t.Error("alarms() on the meter did not refuse")
	}
}

func TestIdentifyMeter(t *testing.T) {
	srv, err := modbussim.New(nil)
	if err != nil {
		t.Fatalf("start simulator: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	f := &fakeESS{Server: srv}
	seedBlocks(f, huawei.MeterRegisters)

	f.set(t, huawei.MeterVoltageA, 231.2)
	f.set(t, huawei.MeterVoltageB, 230.4)
	f.set(t, huawei.MeterVoltageC, 229.9)
	f.set(t, huawei.MeterVoltageAB, 399.8)
	f.set(t, huawei.MeterActivePower, -12.5)

	var out bytes.Buffer
	if err := identify(context.Background(), newReader(f), meterDevice, &out); err != nil {
		t.Fatalf("identify() error = %v", err)
	}

	got := out.String()
	for _, want := range []string{"231.2", "399.8", "-12.5", "import from grid"} {
		if !strings.Contains(got, want) {
			t.Errorf("identify() output missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "IMPLAUSIBLE") {
		t.Errorf("identify() flagged a healthy meter\n%s", got)
	}
}

func TestReaderSplitsRefusedBlock(t *testing.T) {
	// 40484 (two words) and 40488 fall in one block across the undocumented
	// 40486-40487. A device that refuses the gap must not cost both readings.
	srv, err := modbussim.New(map[uint16]uint16{
		40484: 0x0006, 40485: 0x9000, // 430.08 kWh
		40488: 2,
	})
	if err != nil {
		t.Fatalf("start simulator: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	regs := []huawei.Register{huawei.LoggerRatedESSCapacity, huawei.LoggerNumberOfESSs}
	if n := len(huawei.Blocks(regs, huawei.DefaultGap)); n != 1 {
		t.Fatalf("test premise: want one block, got %d", n)
	}

	r := &reader{client: modbus.New(srv.Addr(), time.Second), retries: 0}
	s, err := r.read(context.Background(), regs)
	if err != nil {
		t.Fatalf("read() error = %v", err)
	}
	if len(s.Failed) != 0 {
		t.Errorf("read() failed registers: %v", s.Failed)
	}
	if v, _ := s.value(huawei.LoggerRatedESSCapacity); v != 430.08 {
		t.Errorf("rated ESS capacity = %v, want 430.08", v)
	}
	if v, _ := s.value(huawei.LoggerNumberOfESSs); v != 2 {
		t.Errorf("number of ESSs = %v, want 2", v)
	}
}

func TestTrackerSettlesLoggerPolarity(t *testing.T) {
	tr := newTracker(loggerDevice)
	var log bytes.Buffer

	// SOC rising while 40507 reads negative and 40392 positive: 40507 then
	// follows the AC-side convention, 40392 the battery-side one.
	for i, soc := range []float64{90.0, 90.2, 90.4, 90.6} {
		s := newSample()
		s.Values[huawei.LoggerSOC.Addr] = soc
		s.Values[huawei.LoggerESSChargeDischargePower.Addr] = -40 - float64(i)
		s.Values[huawei.LoggerESSActivePower.Addr] = 40
		tr.observe(s, &log)
	}

	var sum bytes.Buffer
	tr.summarise(&sum)
	got := sum.String()

	for _, want := range []string{
		"polarity of 40507", "0 of 3 samples match", "positive = discharge, negative = charge (not yet confirmed",
		"polarity of 40392", "3 of 3 samples match", "positive = charge, negative = discharge (not yet confirmed",
		"polarity of 30014", "not enough movement",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q\n%s", want, got)
		}
	}
}

func TestTrackerLoggerSetpointChange(t *testing.T) {
	tr := newTracker(loggerDevice)
	var log bytes.Buffer

	for _, v := range []float64{0, -50} {
		s := newSample()
		s.Values[huawei.LoggerESSActivePowerSetpoint.Addr] = v
		tr.observe(s, &log)
	}

	if !strings.Contains(log.String(), "ANOTHER MASTER WROTE ESS active power setpoint (kW): 0 -> -50") {
		t.Errorf("tracker missed a write to 40381\n%s", log.String())
	}
}
