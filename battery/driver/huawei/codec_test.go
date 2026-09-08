package huawei

import (
	"math"
	"testing"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		name  string
		reg   Register
		words []uint16
		want  float64
	}{
		{"soc u16 gain 1", SOC, []uint16{0x0055}, 85},
		{"control soc u16 gain 10", ControlSOC, []uint16{0x0355}, 85.3},
		{"rated capacity u32 gain 1000", RatedCapacity, []uint16{0x0003, 0x47D8}, 215},
		{"discharge power i32 positive", ChargeDischargePower, []uint16{0x0001, 0x86A0}, 100},
		{"charge power i32 negative", ChargeDischargePower, []uint16{0xFFFE, 0x7960}, -100},
		{"rack current i16 negative", RackCurrent, []uint16{0xFFCE}, -5},
		{"cabin temperature i16 negative", CabinTemperature1, []uint16{0xFF06}, -25},
		{"pack temperature i16 gain 100", HighestPackTemperature, []uint16{0x0BB8}, 30},
		{"grid frequency u16 gain 100", GridFrequency, []uint16{0x1388}, 50},
		{"local time u32 epoch", LocalTime, []uint16{0x68BE, 0x0000}, 1757282304},
		{"i32 minimum", ChargeDischargePower, []uint16{0x8000, 0x0000}, -2147483.648},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.reg.Decode(tt.words)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if math.Abs(got-tt.want) > 1e-6 {
				t.Errorf("Decode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	if _, err := RatedCapacity.Decode([]uint16{1}); err == nil {
		t.Error("Decode() with too few words: expected error")
	}
	if _, err := SOC.Decode([]uint16{1, 2}); err == nil {
		t.Error("Decode() with too many words: expected error")
	}
	if _, err := ChargingConfirmation.Decode([]uint16{1}); err == nil {
		t.Error("Decode() of a write-only register: expected error")
	}
}

func TestEncode(t *testing.T) {
	tests := []struct {
		name string
		reg  Register
		val  float64
		want []uint16
	}{
		{"charge cut-off soc", ChargeCutOffSOC, 95, []uint16{95}},
		{"discharge cut-off soc", DischargeCutOffSOC, 10, []uint16{10}},
		{"setpoint discharge 50 kW", ActivePowerSetpoint, 50, []uint16{0x0000, 0xC350}},
		{"setpoint charge 50 kW", ActivePowerSetpoint, -50, []uint16{0xFFFF, 0x3CB0}},
		{"setpoint zero", ActivePowerSetpoint, 0, []uint16{0x0000, 0x0000}},
		{"active power percent", ActivePowerPercent, -12.5, []uint16{0xFB1E}},
		{"baseline u32", ActivePowerBaseline, 215, []uint16{0x0003, 0x47D8}},
		{"rounds to nearest", ActivePowerPercent, 33.333, []uint16{0x0D05}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.reg.Encode(tt.val)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Encode() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("Encode() = %#v, want %#v", got, tt.want)
				}
			}
		})
	}
}

func TestEncodeRejects(t *testing.T) {
	tests := []struct {
		name string
		reg  Register
		val  float64
	}{
		{"read-only register", SOC, 50},
		{"charge cut-off below documented range", ChargeCutOffSOC, 80},
		{"charge cut-off above documented range", ChargeCutOffSOC, 101},
		{"discharge cut-off above documented range", DischargeCutOffSOC, 20},
		{"percent beyond documented range", ActivePowerPercent, 150},
		{"unsigned register given a negative value", ActivePowerBaseline, -1},
		{"value too wide for the register", ActivePowerSetpoint, 1e9},
		{"not a number", ActivePowerSetpoint, math.NaN()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.reg.Encode(tt.val); err == nil {
				t.Error("Encode(): expected error")
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	regs := []Register{ActivePowerSetpoint, ActivePowerPercent, ChargeCutOffSOC, ActivePowerBaseline}
	values := []float64{0, 1, 12.34, -12.34, 95, 100}

	for _, r := range regs {
		for _, v := range values {
			if r.Bounded && (v < r.Min || v > r.Max) {
				continue
			}
			if !r.Kind.Signed() && v < 0 {
				continue
			}

			words, err := r.Encode(v)
			if err != nil {
				t.Fatalf("%s: Encode(%v) error = %v", r, v, err)
			}
			got, err := r.Decode(words)
			if err != nil {
				t.Fatalf("%s: Decode() error = %v", r, err)
			}
			// Round trip is exact only to the register's own resolution.
			if math.Abs(got-v) > 1/float64(r.Gain) {
				t.Errorf("%s: round trip of %v gave %v", r, v, got)
			}
		}
	}
}

func TestRawEnumerations(t *testing.T) {
	raw, err := WorkStatus.DecodeRaw([]uint16{0x0201})
	if err != nil {
		t.Fatalf("DecodeRaw() error = %v", err)
	}
	if uint16(raw) != WorkRunningVSG {
		t.Errorf("DecodeRaw() = %#x, want %#x", raw, WorkRunningVSG)
	}
	if !Running(uint16(raw)) {
		t.Error("Running() = false for a 0x02xx state")
	}
	if Running(WorkCommandShutdown) {
		t.Error("Running() = true for a shutdown state")
	}
	if got := WorkStatusName(0x0207); got != "Running: ESS derating" {
		t.Errorf("WorkStatusName() = %q", got)
	}
	if got := WorkStatusName(0x0999); got != "unknown(0x0999)" {
		t.Errorf("WorkStatusName() = %q", got)
	}

	words, err := PowerOnOff.EncodeRaw(int64(PowerStateOff))
	if err != nil {
		t.Fatalf("EncodeRaw() error = %v", err)
	}
	if len(words) != 1 || words[0] != 2 {
		t.Errorf("EncodeRaw() = %v, want [2]", words)
	}
}

func TestBit(t *testing.T) {
	// Alarm 3100 is register 32133 bit 0, alarm 3113 is bit 13 (table 3-2).
	const word = 1<<0 | 1<<13

	if !Bit(word, 0) || !Bit(word, 13) {
		t.Error("Bit(): set bits reported as clear")
	}
	if Bit(word, 1) || Bit(word, 15) {
		t.Error("Bit(): clear bits reported as set")
	}
	if Bit(word, -1) || Bit(word, 16) {
		t.Error("Bit(): out-of-range bit reported as set")
	}
}

func TestPackRegisters(t *testing.T) {
	// Rows 152-154 document 33662/33862/34062/34262 and the two rows above it.
	for i, want := range []uint16{33662, 33862, 34062, 34262} {
		if got := PackVoltage(i + 1).Addr; got != want {
			t.Errorf("PackVoltage(%d).Addr = %d, want %d", i+1, got, want)
		}
	}
	if got := PackSOC(4).Addr; got != 34263 {
		t.Errorf("PackSOC(4).Addr = %d, want 34263", got)
	}
	if got := PackSOH(1).Addr; got != 33664 {
		t.Errorf("PackSOH(1).Addr = %d, want 33664", got)
	}

	defer func() {
		if recover() == nil {
			t.Error("PackSOC(5): expected panic")
		}
	}()
	PackSOC(5)
}

func TestTableIntegrity(t *testing.T) {
	if got := len(AlarmWords); got != 52 {
		t.Errorf("AlarmWords: %d entries, want 52", got)
	}
	if got := len(GridCodeMasks); got != 32 {
		t.Errorf("GridCodeMasks: %d entries, want 32", got)
	}

	seen := map[uint16]string{}
	for _, r := range append(AlarmWords[:], GridCodeMasks[:]...) {
		if prev, dup := seen[r.Addr]; dup {
			t.Errorf("address %d used by both %q and %q", r.Addr, prev, r.Name)
		}
		seen[r.Addr] = r.Name
		if r.Kind != Bits16 || r.Access != RO {
			t.Errorf("%s: want a read-only Bitfield16", r)
		}
	}
}
