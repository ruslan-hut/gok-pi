package huawei

import "testing"

func TestAlarmsWellFormed(t *testing.T) {
	seen := map[[2]uint16]Alarm{}
	ids := map[uint16]Alarm{}
	words := alarmWordSet()

	for i, a := range Alarms {
		if a.Name == "" {
			t.Errorf("alarm %d (%d): empty name", i, a.ID)
		}
		if a.Bit > 15 {
			t.Errorf("alarm %d: bit %d out of range", a.ID, a.Bit)
		}
		if !words[a.Addr] {
			t.Errorf("alarm %d: register %d is not one of the 52 AlarmWords", a.ID, a.Addr)
		}

		key := [2]uint16{a.Addr, uint16(a.Bit)}
		if prev, dup := seen[key]; dup {
			t.Errorf("register %d bit %d claimed by both %d and %d", a.Addr, a.Bit, prev.ID, a.ID)
		}
		seen[key] = a

		if prev, dup := ids[a.ID]; dup {
			t.Errorf("alarm ID %d used by both %q and %q", a.ID, prev.Name, a.Name)
		}
		ids[a.ID] = a

		if i > 0 && Alarms[i-1].ID >= a.ID {
			t.Errorf("Alarms not ordered by ID at index %d: %d then %d", i, Alarms[i-1].ID, a.ID)
		}
	}
}

func alarmWordSet() map[uint16]bool {
	m := make(map[uint16]bool, len(AlarmWords))
	for _, r := range AlarmWords {
		m[r.Addr] = true
	}

	return m
}

func TestAlarmByID(t *testing.T) {
	a, ok := AlarmByID(3100)
	if !ok {
		t.Fatal("AlarmByID(3100): not found")
	}
	if a.Addr != 32133 || a.Bit != 0 {
		t.Errorf("AlarmByID(3100) = %d bit %d, want 32133 bit 0", a.Addr, a.Bit)
	}
	if a.Reserved() {
		t.Error("AlarmByID(3100).Reserved() = true")
	}
	if _, ok := AlarmByID(9999); ok {
		t.Error("AlarmByID(9999): expected not found")
	}
}

func TestAlarmsInWord(t *testing.T) {
	// Table 3-2: register 32133 carries 3100 at bit 0, 3113 at bit 13.
	got := AlarmsInWord(32133, 1<<0|1<<13)
	if len(got) != 2 {
		t.Fatalf("AlarmsInWord() = %v, want 2 alarms", got)
	}
	if got[0].ID != 3100 || got[1].ID != 3113 {
		t.Errorf("AlarmsInWord() = %d, %d; want 3100, 3113", got[0].ID, got[1].ID)
	}

	if got := AlarmsInWord(32133, 0); got != nil {
		t.Errorf("AlarmsInWord() with no bits set = %v, want none", got)
	}
	// 30463 is an alarm word with no documented bits.
	if got := AlarmsInWord(30463, 0xFFFF); got != nil {
		t.Errorf("AlarmsInWord() on an undocumented word = %v, want none", got)
	}
	if got := AlarmsInWord(12345, 0xFFFF); got != nil {
		t.Errorf("AlarmsInWord() on an unknown register = %v, want none", got)
	}
}

func TestActiveAlarms(t *testing.T) {
	got := ActiveAlarms(map[uint16]uint16{
		32133: 1 << 0, // 3100
		30512: 1 << 1, // 3905
		30463: 0xFFFF, // no documented bits
		32134: 0,      // nothing raised
	})

	if len(got) != 2 {
		t.Fatalf("ActiveAlarms() = %v, want 2", got)
	}
	// Ordered by ID regardless of map iteration order.
	if got[0].ID != 3100 || got[1].ID != 3905 {
		t.Errorf("ActiveAlarms() = %d, %d; want 3100, 3905", got[0].ID, got[1].ID)
	}

	if got := ActiveAlarms(nil); got != nil {
		t.Errorf("ActiveAlarms(nil) = %v, want none", got)
	}
}

func TestActiveAlarmsDecodesEveryWord(t *testing.T) {
	// Setting every bit of every alarm word must raise exactly the whole table.
	words := make(map[uint16]uint16, len(AlarmWords))
	for _, r := range AlarmWords {
		words[r.Addr] = 0xFFFF
	}

	if got := len(ActiveAlarms(words)); got != len(Alarms) {
		t.Errorf("ActiveAlarms() raised %d alarms, want all %d", got, len(Alarms))
	}
}
