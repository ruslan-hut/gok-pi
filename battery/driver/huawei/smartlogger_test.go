package huawei

import (
	"math"
	"testing"
)

func TestLoggerTableIntegrity(t *testing.T) {
	for name, regs := range map[string][]Register{
		"logger": LoggerAllRegisters(),
		"meter":  MeterRegisters,
	} {
		t.Run(name, func(t *testing.T) {
			// No two registers may share a word: an overlap means a transcription error.
			owner := map[uint16]string{}
			for _, r := range regs {
				if r.Gain <= 0 {
					t.Errorf("%s: gain %d", r, r.Gain)
				}
				if !r.Access.Readable() {
					t.Errorf("%s: write-only registers do not belong in a read table", r)
				}
				if r.Bounded && r.Min >= r.Max {
					t.Errorf("%s: empty range [%g, %g]", r, r.Min, r.Max)
				}
				for w := r.Addr; w < r.Addr+r.Words(); w++ {
					if prev, dup := owner[w]; dup {
						t.Errorf("word %d claimed by both %s and %s", w, prev, r)
					}
					owner[w] = r.String()
				}
			}
		})
	}

	for _, r := range LoggerSetpointRegisters {
		if r.Access != RW {
			t.Errorf("setpoint %s is %s, want RW", r, r.Access)
		}
	}
	for _, r := range LoggerTelemetryRegisters {
		if r.Access != RO {
			t.Errorf("telemetry %s is %s, want RO", r, r.Access)
		}
	}
	for i, r := range LoggerAlarmWords {
		if r.Addr != 50000+uint16(i) || r.Kind != Bits16 || r.Access != RO {
			t.Errorf("LoggerAlarmWords[%d] = %s %s %s", i, r, r.Kind, r.Access)
		}
	}
}

func TestLoggerDispatchRegisters(t *testing.T) {
	// The two registers a driver would write, pinned to the document's rows 39
	// and 40 so an accidental edit shows up here rather than on site.
	if r := LoggerESSActivePowerSetpoint; r.Addr != 40381 || r.Kind != I32 || r.Gain != 10 || r.Access != RW {
		t.Errorf("40381 = %+v", r)
	}
	if r := LoggerESSActivePowerPercent; r.Addr != 40383 || r.Kind != I16 || r.Gain != 10 || !r.Bounded || r.Min != -100 || r.Max != 100 {
		t.Errorf("40383 = %+v", r)
	}

	// -50 kW at gain 10 is -500 on the wire, high word first.
	words, err := LoggerESSActivePowerSetpoint.Encode(-50)
	if err != nil {
		t.Fatalf("Encode(-50) error = %v", err)
	}
	if len(words) != 2 || words[0] != 0xFFFF || words[1] != 0xFE0C {
		t.Errorf("Encode(-50) = %04X, want [FFFF FE0C]", words)
	}

	if _, err := LoggerArrayChargeEndSOC.Encode(89); err == nil {
		t.Error("42470 accepted 89, below its documented [90, 100]")
	}

	// The release value of 40430 must fit its own type.
	if _, err := LoggerHighestPriorityActivePower.EncodeRaw(ReleaseHighestPriority); err != nil {
		t.Errorf("EncodeRaw(0x7FFFFFFF) error = %v", err)
	}
}

func TestLoggerBlocksFitOneRequest(t *testing.T) {
	for _, regs := range [][]Register{LoggerAllRegisters(), LoggerObservationRegisters(), MeterRegisters} {
		for _, b := range Blocks(regs, DefaultGap) {
			if b.Count == 0 || b.Count > MaxReadCount {
				t.Errorf("block %d+%d exceeds one read", b.Addr, b.Count)
			}
		}
	}
}

func TestI64(t *testing.T) {
	// 1 234 567.89 kWh at gain 100.
	v, err := MeterTotalActiveEnergy.Decode([]uint16{0, 0, 0x075B, 0xCD15})
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if v != 1234567.89 {
		t.Errorf("Decode() = %v, want 1234567.89", v)
	}

	raw, err := MeterTotalActiveEnergy.DecodeRaw([]uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFE})
	if err != nil {
		t.Fatalf("DecodeRaw() error = %v", err)
	}
	if raw != -2 {
		t.Errorf("DecodeRaw() = %d, want -2", raw)
	}

	if _, err := MeterTotalActiveEnergy.Decode([]uint16{0, 1}); err == nil {
		t.Error("Decode() accepted 2 words for an I64")
	}

	rw := Register{Name: "test", Addr: 1, Kind: I64, Gain: 1, Access: RW}
	for _, want := range []int64{math.MinInt64, -1, 0, math.MaxInt64} {
		words, err := rw.EncodeRaw(want)
		if err != nil {
			t.Fatalf("EncodeRaw(%d) error = %v", want, err)
		}
		got, err := rw.DecodeRaw(words)
		if err != nil || got != want {
			t.Errorf("round trip of %d gave %d (%v)", want, got, err)
		}
	}
}

func TestControlModeName(t *testing.T) {
	if got := ControlModeName(ControlModeLimitedPower); got != "grid connection with limited power (kW)" {
		t.Errorf("ControlModeName(6) = %q", got)
	}
	if got := ControlModeName(ControlModeRemote); got != "remote communication scheduling" {
		t.Errorf("ControlModeName(4) = %q", got)
	}
	if got := ControlModeName(2); got != "unknown(2)" {
		t.Errorf("ControlModeName(2) = %q", got)
	}
}

func TestLoggerAlarmsWellFormed(t *testing.T) {
	words := map[uint16]bool{}
	for _, r := range LoggerAlarmWords {
		words[r.Addr] = true
	}

	// The document assigns these two bits twice; nothing else may collide.
	knownClash := map[[2]uint16]bool{{50005, 13}: true, {50005, 14}: true}

	bits := map[[2]uint16][]Alarm{}
	codes := map[string]bool{}
	for i, a := range LoggerAlarms {
		if a.Name == "" || a.SubID == 0 {
			t.Errorf("alarm %d: missing name or sub-ID: %+v", i, a)
		}
		if a.Bit > 15 || !words[a.Addr] {
			t.Errorf("alarm %s: register %d bit %d is not an alarm bit", a.Code(), a.Addr, a.Bit)
		}
		if codes[a.Code()] {
			t.Errorf("alarm %s listed twice", a.Code())
		}
		codes[a.Code()] = true

		key := [2]uint16{a.Addr, uint16(a.Bit)}
		bits[key] = append(bits[key], a)

		if i > 0 {
			p := LoggerAlarms[i-1]
			if p.ID > a.ID || (p.ID == a.ID && p.SubID >= a.SubID) {
				t.Errorf("LoggerAlarms not ordered at %d: %s then %s", i, p.Code(), a.Code())
			}
		}
	}

	for key, as := range bits {
		if len(as) > 1 && !knownClash[key] {
			t.Errorf("register %d bit %d claimed by %d alarms", key[0], key[1], len(as))
		}
	}
	for key := range knownClash {
		if len(bits[key]) != 2 {
			t.Errorf("register %d bit %d: want the documented double assignment, got %d", key[0], key[1], len(bits[key]))
		}
	}
}

func TestActiveLoggerAlarms(t *testing.T) {
	got := ActiveLoggerAlarms(map[uint16]uint16{
		50000: 1 << 4,  // 1100-5: no remote dispatch commands
		50005: 1 << 13, // 1141-2 and 1142-5 share this bit
		50007: 1 << 9,  // 1154-5: meter communication
	})

	want := []string{"1100-5", "1141-2", "1142-5", "1154-5"}
	if len(got) != len(want) {
		t.Fatalf("ActiveLoggerAlarms() = %v, want %v", got, want)
	}
	for i, a := range got {
		if a.Code() != want[i] {
			t.Errorf("alarm %d = %s, want %s", i, a.Code(), want[i])
		}
	}
	if l := got[3].Label(); l != "Abnormal Communication with Southbound Devices: meter" {
		t.Errorf("Label() = %q", l)
	}

	if len(LoggerAlarmsInWord(50000, 0)) != 0 {
		t.Error("a clear word raised alarms")
	}
}
