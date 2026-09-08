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
// point table fits it. The nameplate check is the useful part: RatedCapacity is
// a U32 with a gain of 1000, so a LUNA2000-215 that decodes to 215.0 confirms
// the address space, the word order and the gain in a single read. A wrong
// answer here means everything else read from this device is meaningless.
func identify(ctx context.Context, r *reader, out io.Writer) error {
	fmt.Fprintf(out, "endpoint %s, unit %d\n\n", r.client.Addr(), r.unit)

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
		for _, d := range devices {
			fmt.Fprintf(out, "  %s\n", d)
			if d.IsHost() {
				fmt.Fprintln(out, "      this is the device holding the Modbus card")
			}
		}
	}

	fmt.Fprintln(out, "\nnameplate check:")

	s, err := r.read(ctx, []huawei.Register{
		huawei.RatedCapacity, huawei.RatedPower,
		huawei.MaxActivePower, huawei.MaxReverseRectificationPower,
		huawei.SOC, huawei.WorkStatus,
	})
	if err != nil {
		return fmt.Errorf("the endpoint answered nothing readable: %w", err)
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	report := func(label string, reg huawei.Register, verdict func(float64) string) {
		if msg, bad := s.Failed[reg.Addr]; bad {
			fmt.Fprintf(w, "  %s\t%d\tfailed: %s\n", label, reg.Addr, msg)

			return
		}
		v, _ := s.value(reg)
		fmt.Fprintf(w, "  %s\t%d\t%g %s\t%s\n", label, reg.Addr, v, reg.Unit, verdict(v))
	}

	report("rated capacity", huawei.RatedCapacity, func(v float64) string {
		if v <= 0 || v > 10000 {
			return "IMPLAUSIBLE - the point table does not match this device"
		}

		return "compare against the installed model's nameplate"
	})
	report("rated power", huawei.RatedPower, plausibleRange(0, 10000))
	report("max active power (Pmax)", huawei.MaxActivePower, plausibleRange(0, 10000))
	report("max reverse power (RPmax)", huawei.MaxReverseRectificationPower, plausibleRange(-10000, 10000))
	report("SOC", huawei.SOC, plausibleRange(0, 100))

	if raw, ok := s.raw(huawei.WorkStatus); ok {
		fmt.Fprintf(w, "  work status\t%d\t0x%04X\t%s\n",
			huawei.WorkStatus.Addr, uint16(raw), huawei.WorkStatusName(uint16(raw)))
	}

	if err := w.Flush(); err != nil {
		return err
	}

	if len(s.Failed) > 0 {
		fmt.Fprintf(out, "\n%d of the nameplate registers could not be read; "+
			"if the device answered exception 0x02 the address space differs from the document\n", len(s.Failed))
	}

	fmt.Fprintln(out, "\nActive power setpoint registers are readable and are polled by 'watch'.")
	fmt.Fprintln(out, "A setpoint that moves without you writing it means another master is dispatching this ESS.")

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
