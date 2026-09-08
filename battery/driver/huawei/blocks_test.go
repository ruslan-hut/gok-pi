package huawei

import "testing"

func TestBlocks(t *testing.T) {
	tests := []struct {
		name string
		regs []Register
		gap  uint16
		want []Block
	}{
		{
			name: "empty",
			regs: nil,
			gap:  DefaultGap,
			want: nil,
		},
		{
			name: "single u16",
			regs: []Register{SOC},
			gap:  DefaultGap,
			want: []Block{{30400, 1}},
		},
		{
			name: "single u32 spans two words",
			regs: []Register{RatedCapacity},
			gap:  DefaultGap,
			want: []Block{{30236, 2}},
		},
		{
			name: "adjacent registers merge",
			regs: []Register{SOH, SOE, DOD},
			gap:  0,
			want: []Block{{32041, 3}},
		},
		{
			name: "gap within tolerance merges",
			regs: []Register{RatedCapacity, RatedPower},
			gap:  DefaultGap,
			want: []Block{{30236, 4}},
		},
		{
			name: "gap beyond tolerance splits",
			regs: []Register{SOC, SOH},
			gap:  DefaultGap,
			want: []Block{{30400, 1}, {32041, 1}},
		},
		{
			name: "input order does not matter",
			regs: []Register{DOD, SOH, SOE},
			gap:  0,
			want: []Block{{32041, 3}},
		},
		{
			name: "duplicates are absorbed",
			regs: []Register{SOC, SOC, SOC},
			gap:  DefaultGap,
			want: []Block{{30400, 1}},
		},
		{
			name: "register inside a wider one is absorbed",
			regs: []Register{RatedCapacity, {Name: "inner", Addr: 30237, Kind: U16}},
			gap:  0,
			want: []Block{{30236, 2}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Blocks(tt.regs, tt.gap)
			if len(got) != len(tt.want) {
				t.Fatalf("Blocks() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Blocks()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestBlocksRespectMaxReadCount(t *testing.T) {
	// A run of adjacent registers longer than one request must be split.
	regs := make([]Register, 300)
	for i := range regs {
		regs[i] = Register{Name: "r", Addr: uint16(1000 + i), Kind: U16, Gain: 1}
	}

	blocks := Blocks(regs, DefaultGap)
	if len(blocks) < 3 {
		t.Fatalf("Blocks() = %d blocks, want at least 3", len(blocks))
	}

	var total uint16
	for _, b := range blocks {
		if b.Count > MaxReadCount {
			t.Errorf("block %v exceeds MaxReadCount", b)
		}
		total += b.Count
	}
	if total != 300 {
		t.Errorf("blocks cover %d registers, want 300", total)
	}
}

func TestBlocksCoverEveryRegister(t *testing.T) {
	for _, name := range []string{"observation", "all"} {
		regs := ObservationRegisters()
		if name == "all" {
			regs = AllRegisters()
		}

		blocks := Blocks(regs, DefaultGap)
		for _, r := range regs {
			covered := false
			for _, b := range blocks {
				if b.Contains(r) {
					covered = true

					break
				}
			}
			if !covered {
				t.Errorf("%s: %s is not covered by any block", name, r)
			}
		}

		for _, b := range blocks {
			if b.Count == 0 || b.Count > MaxReadCount {
				t.Errorf("%s: block %v has an unusable count", name, b)
			}
		}
		t.Logf("%s: %d registers in %d read requests", name, len(regs), len(blocks))
	}
}

func TestBlocksAreOrderedAndDisjoint(t *testing.T) {
	blocks := Blocks(AllRegisters(), DefaultGap)
	for i := 1; i < len(blocks); i++ {
		if blocks[i].Addr < blocks[i-1].End() {
			t.Errorf("blocks %v and %v overlap or are out of order", blocks[i-1], blocks[i])
		}
	}
}

func TestExtract(t *testing.T) {
	b := Block{Addr: 30236, Count: 4} // RatedCapacity then RatedPower

	words := []uint16{0x0003, 0x47D8, 0x0000, 0x0064}

	got, ok := b.Extract(words, RatedCapacity)
	if !ok {
		t.Fatal("Extract(RatedCapacity) reported false")
	}
	if len(got) != 2 || got[0] != 0x0003 || got[1] != 0x47D8 {
		t.Errorf("Extract(RatedCapacity) = %v", got)
	}
	if v, err := RatedCapacity.Decode(got); err != nil || v != 215 {
		t.Errorf("decoded %v (err %v), want 215", v, err)
	}

	got, ok = b.Extract(words, RatedPower)
	if !ok {
		t.Fatal("Extract(RatedPower) reported false")
	}
	if v, err := RatedPower.Decode(got); err != nil || v != 0.1 {
		t.Errorf("decoded %v (err %v), want 0.1", v, err)
	}

	if _, ok := b.Extract(words, SOC); ok {
		t.Error("Extract() of a register outside the block reported true")
	}
	if _, ok := b.Extract(words[:2], RatedCapacity); ok {
		t.Error("Extract() with a wrong-length read reported true")
	}
}

func TestNoWriteOnlyRegisterIsRead(t *testing.T) {
	for _, r := range AllRegisters() {
		if !r.Access.Readable() {
			t.Errorf("%s is write-only and must not appear in a read set", r)
		}
	}
}
