package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
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
func dump(ctx context.Context, r *reader, d *device, out io.Writer, asJSON, all bool) error {
	regs := d.observation()
	if all {
		regs = d.all()
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
		row.Note = d.annotate(reg, s)

		rows = append(rows, row)
	}

	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")

		return enc.Encode(struct {
			At     string    `json:"at"`
			Addr   string    `json:"endpoint"`
			Unit   uint8     `json:"unit"`
			Device string    `json:"device"`
			Rows   []dumpRow `json:"registers"`
		}{s.At.Format("2006-01-02T15:04:05Z"), r.client.Addr(), r.unit, d.name, rows})
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
