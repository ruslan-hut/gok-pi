package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"gok-pi/battery/driver/huawei"
)

// alarms reads the 52 alarm words and lists what is raised. Run it before
// wiring alerting: a site in service usually has a nuisance bit or two set
// permanently, and those need to be known so the agent does not email about
// them every day.
func alarms(ctx context.Context, r *reader, out io.Writer) error {
	regs := make([]huawei.Register, 0, len(huawei.AlarmWords))
	regs = append(regs, huawei.AlarmWords[:]...)

	s, err := r.read(ctx, regs)
	if err != nil {
		return err
	}

	words := map[uint16]uint16{}
	for _, reg := range huawei.AlarmWords {
		if raw, ok := s.raw(reg); ok {
			words[reg.Addr] = uint16(raw)
		}
	}

	active := huawei.ActiveAlarms(words)

	fmt.Fprintf(out, "%d of %d alarm words read at %s\n\n",
		len(words), len(huawei.AlarmWords), s.At.Format("2006-01-02 15:04:05Z"))

	// A word with bits set that the table does not define is worth seeing: it
	// means this firmware reports something issue 01 of the document does not.
	for addr, word := range words {
		if word == 0 {
			continue
		}
		var documented uint16
		for _, a := range huawei.AlarmsInWord(addr, 0xFFFF) {
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
		fmt.Fprintf(w, "%d\t%d\t%d\t%s\n", a.ID, a.Addr, a.Bit, a.Name)
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
