package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"gok-pi/battery/driver/huawei"
)

// dumpRow is one register as read, shaped for both the table and the JSON form.
type dumpRow struct {
	Addr   uint16   `json:"addr"`
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Access string   `json:"access"`
	Unit   string   `json:"unit,omitempty"`
	Raw    *int64   `json:"raw,omitempty"`
	Value  *float64 `json:"value,omitempty"`
	Note   string   `json:"note,omitempty"`
	Error  string   `json:"error,omitempty"`
}

// dump reads the point table once and prints every signal with its raw and
// decoded value, so the whole mapping can be eyeballed against the site in one
// pass. Anything implausible here is a mapping error worth chasing before the
// driver is written.
func dump(ctx context.Context, r *reader, out io.Writer, asJSON, all bool) error {
	regs := huawei.ObservationRegisters()
	if all {
		regs = huawei.AllRegisters()
	}

	s, err := r.read(ctx, regs)
	if err != nil {
		return err
	}

	sort.Slice(regs, func(i, j int) bool { return regs[i].Addr < regs[j].Addr })

	rows := make([]dumpRow, 0, len(regs))
	for _, reg := range regs {
		row := dumpRow{
			Addr:   reg.Addr,
			Name:   reg.Name,
			Type:   reg.Kind.String(),
			Access: reg.Access.String(),
			Unit:   reg.Unit,
		}

		if msg, bad := s.Failed[reg.Addr]; bad {
			row.Error = msg
			rows = append(rows, row)

			continue
		}

		if raw, ok := s.raw(reg); ok {
			row.Raw = &raw
		}
		if v, ok := s.value(reg); ok {
			row.Value = &v
		}
		row.Note = annotate(reg, s)

		rows = append(rows, row)
	}

	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")

		return enc.Encode(struct {
			At   string    `json:"at"`
			Addr string    `json:"endpoint"`
			Unit uint8     `json:"unit"`
			Rows []dumpRow `json:"registers"`
		}{s.At.Format("2006-01-02T15:04:05Z"), r.client.Addr(), r.unit, rows})
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ADDR\tNAME\tTYPE\tACC\tRAW\tVALUE\tUNIT\tNOTE")

	for _, row := range rows {
		switch {
		case row.Error != "":
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t\t\t\t%s\n", row.Addr, row.Name, row.Type, row.Access, row.Error)
		default:
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d\t%g\t%s\t%s\n",
				row.Addr, row.Name, row.Type, row.Access, deref(row.Raw), derefF(row.Value), row.Unit, row.Note)
		}
	}

	if err := w.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(out, "\n%d registers read, %d failed\n", len(s.Raw), len(s.Failed))

	return nil
}

// annotate adds the meaning behind a raw value where the point table defines
// one, so an enumeration does not have to be looked up by hand.
func annotate(reg huawei.Register, s *sample) string {
	raw, ok := s.raw(reg)
	if !ok {
		return ""
	}
	v := uint16(raw)

	switch reg.Addr {
	case huawei.WorkStatus.Addr:
		return huawei.WorkStatusName(v)
	case huawei.ChargingStatus.Addr:
		return [...]string{"idle", "recharge request", "recharging", "charging ends"}[min(int(v), 3)]
	case huawei.LTMSWorkingStatus.Addr:
		return [...]string{"off", "self-circulating", "refrigeration", "heating"}[min(int(v), 3)]
	case huawei.WorkingMode.Addr:
		if v == huawei.ModeVSG {
			return "VSG (grid-forming)"
		}

		return "PQ (grid-following)"
	case huawei.PowerOnOff.Addr:
		if v == huawei.PowerStateRun {
			return "run"
		}

		return "off"
	case huawei.ChargeDischargePower.Addr:
		return powerDirection(s)
	}

	if reg.Kind == huawei.Bits16 && v != 0 {
		names := huawei.AlarmsInWord(reg.Addr, v)
		if len(names) == 0 {
			return fmt.Sprintf("bits 0x%04X set, none documented", v)
		}
		out := ""
		for i, a := range names {
			if i > 0 {
				out += "; "
			}
			out += a.Name
		}

		return out
	}

	return ""
}

// powerDirection spells out what the sign of the charge/discharge power means,
// flagging that the polarity is an assumption until it has been confirmed
// against SOC movement on real hardware.
func powerDirection(s *sample) string {
	v, ok := s.value(huawei.ChargeDischargePower)
	if !ok || v == 0 {
		return "idle"
	}

	if v > 0 {
		return "positive (discharging, if the documented polarity holds)"
	}

	return "negative (charging, if the documented polarity holds)"
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}

	return *p
}

func derefF(p *float64) float64 {
	if p == nil {
		return 0
	}

	return *p
}
