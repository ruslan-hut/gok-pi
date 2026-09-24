package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"gok-pi/battery/driver/huawei"
	"gok-pi/internal/modbus"
)

// identify reports what is on the other end of the endpoint and whether the
// point table fits it. The nameplate check is the useful part: each device has
// a register whose value is known from the installation (a cabinet's rated
// capacity, the logger's summed capacity, the meter's phase voltages), and a
// multi-word one with a gain confirms the address space, the word order and
// the gain in a single read. A wrong answer here means everything else read
// from this device is meaningless.
func identify(ctx context.Context, r *reader, d *device, out io.Writer) error {
	fmt.Fprintf(out, "endpoint %s, unit %d, read as %s\n\n", r.client.Addr(), r.unit, d.name)

	basic, err := r.client.ReadDeviceID(ctx, r.unit, modbus.ReadDevIDBasic, modbus.ObjectVendor)
	switch {
	case err != nil:
		fmt.Fprintf(out, "device identification: unavailable (%v)\n", err)
	default:
		fmt.Fprintln(out, "device identification:")
		fmt.Fprintf(out, "  vendor       %s\n", orDash(basic[modbus.ObjectVendor]))
		fmt.Fprintf(out, "  product code %s\n", orDash(basic[modbus.ObjectProductCode]))
		fmt.Fprintf(out, "  revision     %s\n", orDash(basic[modbus.ObjectRevision]))
	}

	list, err := r.client.ReadDeviceID(ctx, r.unit, modbus.ReadDevIDList, modbus.ObjectDeviceCount)
	switch {
	case err != nil:
		fmt.Fprintf(out, "\ndevice list: unavailable (%v)\n", err)
	default:
		count, devices := huawei.ParseDeviceList(list)
		fmt.Fprintf(out, "\ndevice list (%s):\n", countLabel(count))
		if len(devices) == 0 {
			fmt.Fprintln(out, "  none reported")
		}
		for _, dev := range devices {
			fmt.Fprintf(out, "  %s\n", dev)
			if dev.IsHost() {
				fmt.Fprintln(out, "      this is the device holding the Modbus card")
			}
		}
	}

	fmt.Fprintln(out, "\nnameplate check:")

	regs := make([]huawei.Register, 0, len(d.nameplate)+len(d.status))
	for _, c := range d.nameplate {
		regs = append(regs, c.reg)
	}
	regs = append(regs, d.status...)

	s, err := r.read(ctx, regs)
	if err != nil {
		return fmt.Errorf("the endpoint answered nothing readable: %w", err)
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, c := range d.nameplate {
		if msg, bad := s.Failed[c.reg.Addr]; bad {
			fmt.Fprintf(w, "  %s\t%d\tfailed: %s\n", c.label, c.reg.Addr, msg)

			continue
		}
		v, _ := s.value(c.reg)
		fmt.Fprintf(w, "  %s\t%d\t%g %s\t%s\n", c.label, c.reg.Addr, v, c.reg.Unit, c.verdict(v))
	}

	for _, reg := range d.status {
		if msg, bad := s.Failed[reg.Addr]; bad {
			fmt.Fprintf(w, "  %s\t%d\tfailed: %s\n", reg.Name, reg.Addr, msg)

			continue
		}
		v, _ := s.value(reg)
		fmt.Fprintf(w, "  %s\t%d\t%g %s\t%s\n", reg.Name, reg.Addr, v, reg.Unit, d.annotate(reg, s))
	}

	if err := w.Flush(); err != nil {
		return err
	}

	if len(s.Failed) > 0 {
		fmt.Fprintf(out, "\n%d of these registers could not be read; "+
			"if the device answered exception 0x02 the address space differs from the document, "+
			"or -device does not match what is at this unit\n", len(s.Failed))
	}

	if d.footer != "" {
		fmt.Fprintf(out, "\n%s\n", d.footer)
	}

	return nil
}

// plausibleRange reports whether a decoded value sits inside the range the
// signal can physically take. A value outside it is evidence of a mapping
// error - a wrong gain, or the wrong word order - not of a strange site.
func plausibleRange(lo, hi float64) func(float64) string {
	return func(v float64) string {
		if v < lo || v > hi {
			return fmt.Sprintf("IMPLAUSIBLE - outside [%g, %g]", lo, hi)
		}

		return "plausible"
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

func countLabel(count int) string {
	if count < 0 {
		return "count not reported"
	}

	return fmt.Sprintf("%d device(s) reported", count)
}
