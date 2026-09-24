package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"gok-pi/battery/driver/huawei"
)

// alarms reads the device's alarm words and lists what is raised. Run it
// before wiring alerting: a site in service usually has a nuisance bit or two
// set permanently, and those need to be known so the agent does not email about
// them every day.
func alarms(ctx context.Context, r *reader, d *device, out io.Writer) error {
	if len(d.alarmWords) == 0 {
		return fmt.Errorf("the %s has no alarm registers", d.name)
	}

	s, err := r.read(ctx, append([]huawei.Register(nil), d.alarmWords...))
	if err != nil {
		return err
	}

	words := d.alarmWordValues(s)
	active := d.activeAlarms(words)

	fmt.Fprintf(out, "%d of %d alarm words read at %s\n\n",
		len(words), len(d.alarmWords), s.At.Format("2006-01-02 15:04:05Z"))

	// A word with bits set that the table does not define is worth seeing: it
	// means this firmware reports something the document does not.
	for addr, word := range words {
		if word == 0 {
			continue
		}
		var documented uint16
		for _, a := range d.alarmsIn(addr, 0xFFFF) {
			documented |= 1 << a.Bit
		}
		if extra := word &^ documented; extra != 0 {
			fmt.Fprintf(out, "register %d has undocumented bits set: 0x%04X\n", addr, extra)
		}
	}

	if len(active) == 0 {
		fmt.Fprintln(out, "no alarms raised")

		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tREGISTER\tBIT\tALARM")

	reserved := 0
	for _, a := range active {
		if a.Reserved() {
			reserved++

			continue
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\n", a.Code(), a.Addr, a.Bit, a.Label())
	}

	if err := w.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(out, "\n%d alarm(s) raised", len(active)-reserved)
	if reserved > 0 {
		fmt.Fprintf(out, ", plus %d reserved bit(s) set", reserved)
	}
	fmt.Fprintln(out)

	return nil
}

// alarmWordValues pulls the device's alarm words out of a sample, keyed by
// register address.
func (d *device) alarmWordValues(s *sample) map[uint16]uint16 {
	words := map[uint16]uint16{}
	for _, reg := range d.alarmWords {
		if raw, ok := s.raw(reg); ok {
			words[reg.Addr] = uint16(raw)
		}
	}

	return words
}

// activeAlarms decodes a set of alarm words with the device's own table,
// ordered by alarm code.
func (d *device) activeAlarms(words map[uint16]uint16) []huawei.Alarm {
	if d.alarmsIn == nil {
		return nil
	}

	var out []huawei.Alarm
	for addr, word := range words {
		out = append(out, d.alarmsIn(addr, word)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}

		return out[i].SubID < out[j].SubID
	})

	return out
}
