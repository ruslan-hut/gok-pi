package huawei

import (
	"errors"
	"gok-pi/battery/driver"
	"gok-pi/battery/entity"
	"gok-pi/internal/modbus/modbussim"
	"io"
	"log/slog"
	"math"
	"testing"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantAddr  string
		wantMeter uint8
		wantErr   bool
	}{
		{name: "full", raw: "modbus://10.0.0.1:1502?meter_unit=11", wantAddr: "10.0.0.1:1502", wantMeter: 11},
		{name: "defaults", raw: "modbus://10.0.0.1", wantAddr: "10.0.0.1:502", wantMeter: 3},
		{name: "no scheme", raw: "10.0.0.1:502", wantAddr: "10.0.0.1:502", wantMeter: 3},
		{name: "http refused", raw: "http://10.0.0.1/api", wantErr: true},
		{name: "no host", raw: "modbus://", wantErr: true},
		{name: "unit out of range", raw: "modbus://10.0.0.1?meter_unit=248", wantErr: true},
		{name: "unit not a number", raw: "modbus://10.0.0.1?meter_unit=x", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, meter, err := parseEndpoint(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if addr != tt.wantAddr || meter != tt.wantMeter {
				t.Errorf("got %q unit %d, want %q unit %d", addr, meter, tt.wantAddr, tt.wantMeter)
			}
		})
	}
}

func setU32(s *modbussim.Server, r Register, raw uint32) {
	s.SetWords(r.Addr, []uint16{uint16(raw >> 16), uint16(raw)})
}

func setI32(s *modbussim.Server, r Register, raw int32) { setU32(s, r, uint32(raw)) }

// newSite serves the logger and the meter on one unit: the simulator answers a
// single unit id, and the two register maps do not collide.
func newSite(t *testing.T) (*modbussim.Server, driver.Driver) {
	t.Helper()

	s, err := modbussim.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	s.Set(LoggerSOC.Addr, 785)
	setU32(s, LoggerDischargeableCapacity, 320500)
	setI32(s, LoggerESSActivePower, 0)
	setU32(s, LoggerPVActivePower, 0)
	setI32(s, MeterActivePower, 0)

	d, err := New(entity.BatteryConfig{Url: "modbus://" + s.Addr() + "?meter_unit=0"}, discardLog())
	if err != nil {
		t.Fatal(err)
	}

	return s, d
}

func TestStatus(t *testing.T) {
	tests := []struct {
		name            string
		essRaw          int32
		pvRaw           uint32
		meterRaw        int32
		wantPac         float64
		wantConsumption float64
		wantCharging    bool
		wantDischarging bool
	}{
		{
			name:   "pv charges the battery",
			essRaw: -39300, pvRaw: 40300, meterRaw: 100,
			wantPac: -39300, wantConsumption: 900, wantCharging: true,
		},
		{
			name:   "battery covers a charging session",
			essRaw: 100000, pvRaw: 5000, meterRaw: 0,
			wantPac: 100000, wantConsumption: 105000, wantDischarging: true,
		},
		{
			name:   "grid covers the load with the battery idle",
			essRaw: 0, pvRaw: 0, meterRaw: -80000,
			wantPac: 0, wantConsumption: 80000,
		},
		{
			name:   "noise around zero is idle and never negative",
			essRaw: -50, pvRaw: 0, meterRaw: 200,
			wantPac: -50, wantConsumption: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, d := newSite(t)
			setI32(s, LoggerESSActivePower, tt.essRaw)
			setU32(s, LoggerPVActivePower, tt.pvRaw)
			setI32(s, MeterActivePower, tt.meterRaw)

			got, err := d.Status()
			if err != nil {
				t.Fatal(err)
			}

			if got.USOC != 78.5 || got.RSOC != 78.5 {
				t.Errorf("soc = %g / %g, want 78.5", got.USOC, got.RSOC)
			}
			if got.RemainingCapacityWh != 320500 {
				t.Errorf("remaining = %g", got.RemainingCapacityWh)
			}
			if math.Abs(got.PacTotalW-tt.wantPac) > 1e-6 {
				t.Errorf("pac = %g, want %g", got.PacTotalW, tt.wantPac)
			}
			if math.Abs(got.ConsumptionW-tt.wantConsumption) > 1e-6 {
				t.Errorf("consumption = %g, want %g", got.ConsumptionW, tt.wantConsumption)
			}
			if math.Abs(got.ProductionW-float64(tt.pvRaw)) > 1e-6 {
				t.Errorf("production = %g", got.ProductionW)
			}
			if math.Abs(got.GridFeedInW-float64(tt.meterRaw)) > 1e-6 {
				t.Errorf("feed-in = %g", got.GridFeedInW)
			}
			if got.BatteryCharging != tt.wantCharging || got.BatteryDischarging != tt.wantDischarging {
				t.Errorf("charging %v discharging %v", got.BatteryCharging, got.BatteryDischarging)
			}
			if got.OperatingMode != opModeAuto {
				t.Errorf("mode = %q", got.OperatingMode)
			}
			if s.Writes() != 0 {
				t.Errorf("status wrote %d times", s.Writes())
			}
		})
	}
}

func TestStatusRejectsBadReadings(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*modbussim.Server)
	}{
		{name: "soc above 100", setup: func(s *modbussim.Server) { s.Set(LoggerSOC.Addr, 1001) }},
		{name: "soc has no value", setup: func(s *modbussim.Server) { s.Set(LoggerSOC.Addr, 0xFFFF) }},
		{name: "power has no value", setup: func(s *modbussim.Server) { setI32(s, LoggerESSActivePower, math.MaxInt32) }},
		{name: "meter has no value", setup: func(s *modbussim.Server) { setI32(s, MeterActivePower, math.MaxInt32) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, d := newSite(t)
			tt.setup(s)

			if got, err := d.Status(); err == nil {
				t.Fatalf("want error, got %+v", got)
			}
		})
	}
}

func TestStatusFailsWithoutMeter(t *testing.T) {
	s, err := modbussim.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Set(LoggerSOC.Addr, 500)
	setU32(s, LoggerDischargeableCapacity, 1000)
	setI32(s, LoggerESSActivePower, 0)
	setU32(s, LoggerPVActivePower, 0)

	d, err := New(entity.BatteryConfig{Url: "modbus://" + s.Addr() + "?meter_unit=0"}, discardLog())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := d.Status(); err == nil {
		t.Fatal("want error when the meter register is refused")
	}
}

func TestCommandsNeverWrite(t *testing.T) {
	s, d := newSite(t)

	refused := map[string]func() error{
		"StartDischarge": func() error { return d.StartDischarge(1000) },
		"StartCharge":    func() error { return d.StartCharge(1000) },
		"ToManual":       func() error { return d.SwitchOperatingModeToManual("2") },
	}
	for name, fn := range refused {
		if err := fn(); !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s: err = %v, want ErrReadOnly", name, err)
		}
	}

	harmless := map[string]func() error{
		"StopDischarge": d.StopDischarge,
		"StopCharge":    d.StopCharge,
		"ToAuto":        func() error { return d.SwitchOperatingModeToAuto("2") },
	}
	for name, fn := range harmless {
		if err := fn(); err != nil {
			t.Errorf("%s: err = %v, want nil", name, err)
		}
	}

	if s.Requests() != 0 {
		t.Errorf("commands sent %d requests", s.Requests())
	}
}

func TestRegisteredWithDriverFactory(t *testing.T) {
	d, err := driver.New("huawei", entity.BatteryConfig{Url: "modbus://127.0.0.1:1502"}, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.(*Driver); !ok {
		t.Fatalf("got %T", d)
	}
}
