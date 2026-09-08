package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"gok-pi/battery/driver/huawei"
)

type watchOptions struct {
	interval time.Duration
	duration time.Duration
	path     string
	log      io.Writer
}

// observation is one sample, written as a single JSON object per line so a long
// run can be tailed, split, or replayed into the simulator without parsing the
// whole file.
type observation struct {
	At        string             `json:"at"`
	Endpoint  string             `json:"endpoint"`
	Unit      uint8              `json:"unit"`
	Values    map[string]float64 `json:"values"`
	Raw       map[string]int64   `json:"raw"`
	Alarms    []string           `json:"alarms,omitempty"`
	Failed    map[string]string  `json:"failed,omitempty"`
	Setpoints map[string]float64 `json:"setpoints"`
}

// watch samples the observation set on an interval and appends one JSON object
// per sample. It is the tool for the two questions that cannot be answered from
// a single reading:
//
// The polarity of the charge/discharge power. Correlating its sign against the
// direction SOC moves settles it without writing anything, and the running
// summary on stderr does that correlation as it goes.
//
// Whether another master is dispatching the ESS. The active power setpoint and
// the cut-off SOC limits are writable but readable; nothing here writes them,
// so any change in one came from somewhere else on the network. Those changes
// are called out on stderr as they happen.
func watch(ctx context.Context, r *reader, opts watchOptions) error {
	if opts.interval < time.Second {
		return fmt.Errorf("interval %s is too short to be polite to a production endpoint", opts.interval)
	}

	sink := io.WriteCloser(nopCloser{os.Stdout})
	if opts.path != "" {
		f, err := os.OpenFile(opts.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("open %s: %w", opts.path, err)
		}
		sink = f
	}
	defer func() { _ = sink.Close() }()

	if opts.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.duration)
		defer cancel()
	}

	regs := huawei.ObservationRegisters()
	enc := json.NewEncoder(sink)
	tr := newTracker()

	fmt.Fprintf(opts.log, "watching %s unit %d every %s; %d registers in %d requests per sample\n",
		r.client.Addr(), r.unit, opts.interval, len(regs), len(huawei.Blocks(regs, huawei.DefaultGap)))
	if opts.path != "" {
		fmt.Fprintf(opts.log, "appending to %s\n", opts.path)
	}

	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()

	samples, failures := 0, 0
	for {
		s, err := r.read(ctx, regs)
		switch {
		case err != nil && ctx.Err() != nil:
			fmt.Fprintf(opts.log, "\nstopped after %d samples\n", samples)
			tr.summarise(opts.log)

			return nil
		case err != nil:
			failures++
			fmt.Fprintf(opts.log, "%s sample failed: %v\n", time.Now().Format(time.RFC3339), err)
		default:
			samples++
			obs := buildObservation(r, s)
			if err := enc.Encode(obs); err != nil {
				return fmt.Errorf("write sample: %w", err)
			}
			tr.observe(s, opts.log)
		}

		select {
		case <-ctx.Done():
			fmt.Fprintf(opts.log, "\nstopped after %d samples (%d failed)\n", samples, failures)
			tr.summarise(opts.log)

			return nil
		case <-ticker.C:
		}
	}
}

func buildObservation(r *reader, s *sample) observation {
	obs := observation{
		At:        s.At.Format(time.RFC3339),
		Endpoint:  r.client.Addr(),
		Unit:      r.unit,
		Values:    map[string]float64{},
		Raw:       map[string]int64{},
		Setpoints: map[string]float64{},
	}

	for _, reg := range huawei.TelemetryRegisters {
		if v, ok := s.value(reg); ok {
			obs.Values[reg.Name] = v
		}
		if raw, ok := s.raw(reg); ok {
			obs.Raw[reg.Name] = raw
		}
	}

	for _, reg := range huawei.SetpointRegisters {
		if v, ok := s.value(reg); ok {
			obs.Setpoints[reg.Name] = v
		}
	}

	words := map[uint16]uint16{}
	for _, reg := range huawei.AlarmWords {
		if raw, ok := s.raw(reg); ok {
			words[reg.Addr] = uint16(raw)
		}
	}
	for _, a := range huawei.ActiveAlarms(words) {
		if a.Reserved() {
			continue
		}
		obs.Alarms = append(obs.Alarms, fmt.Sprintf("%d %s", a.ID, a.Name))
	}

	if len(s.Failed) > 0 {
		obs.Failed = map[string]string{}
		for addr, msg := range s.Failed {
			obs.Failed[fmt.Sprint(addr)] = msg
		}
	}

	return obs
}

// tracker watches successive samples for the two things a passive observer is
// here to find: who else is writing, and which way the power sign runs.
type tracker struct {
	setpoints map[string]float64
	alarms    map[string]bool

	lastSOC   float64
	haveSOC   bool
	lastPower float64

	// agreement counts samples where SOC moved in the direction the documented
	// polarity predicts, against those where it moved the other way.
	agree, disagree int
	writers         map[string]int
}

func newTracker() *tracker {
	return &tracker{
		setpoints: map[string]float64{},
		alarms:    map[string]bool{},
		writers:   map[string]int{},
	}
}

func (t *tracker) observe(s *sample, w io.Writer) {
	stamp := s.At.Format(time.RFC3339)

	// A setpoint that changes was changed by something else: this probe cannot write.
	for _, reg := range huawei.SetpointRegisters {
		v, ok := s.value(reg)
		if !ok {
			continue
		}
		prev, seen := t.setpoints[reg.Name]
		if seen && prev != v {
			t.writers[reg.Name]++
			fmt.Fprintf(w, "%s ANOTHER MASTER WROTE %s: %g -> %g %s\n", stamp, reg.Name, prev, v, reg.Unit)
		}
		t.setpoints[reg.Name] = v
	}

	// Alarms appearing and clearing.
	words := map[uint16]uint16{}
	for _, reg := range huawei.AlarmWords {
		if raw, ok := s.raw(reg); ok {
			words[reg.Addr] = uint16(raw)
		}
	}
	now := map[string]bool{}
	for _, a := range huawei.ActiveAlarms(words) {
		if a.Reserved() {
			continue
		}
		key := fmt.Sprintf("%d %s", a.ID, a.Name)
		now[key] = true
		if !t.alarms[key] {
			fmt.Fprintf(w, "%s alarm raised: %s\n", stamp, key)
		}
	}
	for key := range t.alarms {
		if !now[key] {
			fmt.Fprintf(w, "%s alarm cleared: %s\n", stamp, key)
		}
	}
	t.alarms = now

	t.trackPolarity(s)
}

// trackPolarity accumulates evidence for the sign convention. The document does
// not say whether a positive charge/discharge power means charging or
// discharging, but SOC does: if power is positive while SOC falls, positive is
// discharge. Counting both ways over many samples settles it without writing.
func (t *tracker) trackPolarity(s *sample) {
	soc, okSOC := s.value(huawei.SOC)
	power, okPower := s.value(huawei.ChargeDischargePower)
	if !okSOC || !okPower {
		return
	}

	defer func() {
		t.lastSOC, t.haveSOC, t.lastPower = soc, true, power
	}()

	// Only samples where both the previous and current power agree in sign, and
	// SOC actually moved, carry information.
	if !t.haveSOC || soc == t.lastSOC || power == 0 || t.lastPower == 0 {
		return
	}
	if (power > 0) != (t.lastPower > 0) {
		return
	}

	rising := soc > t.lastSOC
	positive := power > 0

	// Documented reading: positive is discharge, so a positive power should
	// come with a falling SOC.
	if positive == rising {
		t.disagree++
	} else {
		t.agree++
	}
}

func (t *tracker) summarise(w io.Writer) {
	if len(t.writers) > 0 {
		fmt.Fprintln(w, "\nsetpoints changed by something other than this probe:")
		for name, n := range t.writers {
			fmt.Fprintf(w, "  %s: %d change(s)\n", name, n)
		}
		fmt.Fprintln(w, "  another master is dispatching this ESS; it will contend with any setpoint the agent writes")
	} else if len(t.setpoints) > 0 {
		fmt.Fprintln(w, "\nno setpoint changed during this run (no evidence of another master writing)")
	}

	total := t.agree + t.disagree
	if total == 0 {
		fmt.Fprintln(w, "\npolarity: not enough movement to tell; run while the battery is cycling")

		return
	}

	fmt.Fprintf(w, "\npolarity: %d of %d samples match 'positive means discharging'\n", t.agree, total)
	switch {
	case t.agree > 0 && t.disagree == 0:
		fmt.Fprintln(w, "  consistent with positive = discharge, negative = charge")
	case t.disagree > 0 && t.agree == 0:
		fmt.Fprintln(w, "  INVERTED: positive = charge, negative = discharge")
	default:
		fmt.Fprintln(w, "  contradictory; SOC may be moving for reasons other than the measured power")
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
