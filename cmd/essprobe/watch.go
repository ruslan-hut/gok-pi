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
	Device    string             `json:"device"`
	Values    map[string]float64 `json:"values"`
	Raw       map[string]int64   `json:"raw"`
	Alarms    []string           `json:"alarms,omitempty"`
	Failed    map[string]string  `json:"failed,omitempty"`
	Setpoints map[string]float64 `json:"setpoints"`
}

// watch samples the device's observation set on an interval and appends one
// JSON object per sample. It is the tool for the two questions that cannot be
// answered from a single reading:
//
// The polarity of each power reading. Correlating its sign against the
// direction SOC moves settles it without writing anything, and the running
// summary on stderr does that correlation as it goes.
//
// Whether another master is dispatching. The setpoint registers are writable
// but readable; nothing here writes them, so any change in one came from
// somewhere else on the network. Those changes are called out on stderr as
// they happen.
func watch(ctx context.Context, r *reader, d *device, opts watchOptions) error {
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

	regs := d.observation()
	enc := json.NewEncoder(sink)
	tr := newTracker(d)

	fmt.Fprintf(opts.log, "watching %s unit %d (%s) every %s; %d registers in %d requests per sample\n",
		r.client.Addr(), r.unit, d.name, opts.interval, len(regs), len(huawei.Blocks(regs, huawei.DefaultGap)))
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
			obs := buildObservation(r, d, s)
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

func buildObservation(r *reader, d *device, s *sample) observation {
	obs := observation{
		At:        s.At.Format(time.RFC3339),
		Endpoint:  r.client.Addr(),
		Unit:      r.unit,
		Device:    d.name,
		Values:    map[string]float64{},
		Raw:       map[string]int64{},
		Setpoints: map[string]float64{},
	}

	for _, reg := range d.telemetry {
		if v, ok := s.value(reg); ok {
			obs.Values[reg.Name] = v
		}
		if raw, ok := s.raw(reg); ok {
			obs.Raw[reg.Name] = raw
		}
	}

	for _, reg := range d.setpoints {
		if v, ok := s.value(reg); ok {
			obs.Setpoints[reg.Name] = v
		}
	}

	for _, a := range d.activeAlarms(d.alarmWordValues(s)) {
		if a.Reserved() {
			continue
		}
		obs.Alarms = append(obs.Alarms, a.Code()+" "+a.Label())
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
// here to find: who else is writing, and which way each power sign runs.
type tracker struct {
	dev *device

	setpoints map[string]float64
	alarms    map[string]bool
	writers   map[string]int

	lastSOC float64
	haveSOC bool

	// Per power register: its previous value, and how many samples moved SOC
	// the way "positive means charging" predicts (agree) or the other way.
	lastPower       map[uint16]float64
	agree, disagree map[uint16]int
}

func newTracker(d *device) *tracker {
	return &tracker{
		dev:       d,
		setpoints: map[string]float64{},
		alarms:    map[string]bool{},
		writers:   map[string]int{},
		lastPower: map[uint16]float64{},
		agree:     map[uint16]int{},
		disagree:  map[uint16]int{},
	}
}

func (t *tracker) observe(s *sample, w io.Writer) {
	stamp := s.At.Format(time.RFC3339)

	// A setpoint that changes was changed by something else: this probe cannot write.
	for _, reg := range t.dev.setpoints {
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
	now := map[string]bool{}
	for _, a := range t.dev.activeAlarms(t.dev.alarmWordValues(s)) {
		if a.Reserved() {
			continue
		}
		key := a.Code() + " " + a.Label()
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

// trackPolarity correlates the sign of each power register with the direction
// SOC moves. On a cabinet this re-checks what the site already settled (30417
// positive = charge, 32986 the other way); on the logger it is how the sign of
// 40507, 40392 and 30014 gets settled at all. The SOC registers used have 0.1%
// resolution, which moves every minute or so at typical power.
func (t *tracker) trackPolarity(s *sample) {
	if len(t.dev.polarity) == 0 {
		return
	}
	soc, ok := s.value(t.dev.soc)
	if !ok {
		return
	}

	moved := t.haveSOC && soc != t.lastSOC
	rising := soc > t.lastSOC
	t.lastSOC, t.haveSOC = soc, true

	for _, p := range t.dev.polarity {
		power, ok := s.value(p.reg)
		if !ok {
			continue
		}
		last, had := t.lastPower[p.reg.Addr]
		t.lastPower[p.reg.Addr] = power

		// Only samples where both the previous and current power agree in
		// sign, and SOC actually moved, carry information.
		if !had || !moved || power == 0 || last == 0 || (power > 0) != (last > 0) {
			continue
		}

		if (power > 0) == rising {
			t.agree[p.reg.Addr]++
		} else {
			t.disagree[p.reg.Addr]++
		}
	}
}

func (t *tracker) summarise(w io.Writer) {
	if len(t.writers) > 0 {
		fmt.Fprintln(w, "\nsetpoints changed by something other than this probe:")
		for name, n := range t.writers {
			fmt.Fprintf(w, "  %s: %d change(s)\n", name, n)
		}
		fmt.Fprintln(w, "  another master is dispatching; it will contend with any setpoint the agent writes")
	} else if len(t.setpoints) > 0 {
		fmt.Fprintln(w, "\nno setpoint changed during this run (no evidence of another master writing)")
	}

	for _, p := range t.dev.polarity {
		fmt.Fprintf(w, "\npolarity of %d %s: ", p.reg.Addr, p.reg.Name)

		agree, disagree := t.agree[p.reg.Addr], t.disagree[p.reg.Addr]
		total := agree + disagree
		if total == 0 {
			fmt.Fprintln(w, "not enough movement to tell; run while the battery is cycling")

			continue
		}
		fmt.Fprintf(w, "%d of %d samples match 'positive means charging'\n", agree, total)

		var found int
		switch {
		case agree > 0 && disagree == 0:
			found = +1
		case disagree > 0 && agree == 0:
			found = -1
		default:
			fmt.Fprintln(w, "  contradictory; SOC may be moving for reasons other than the measured power")

			continue
		}

		verdict := "positive = charge, negative = discharge"
		if found < 0 {
			verdict = "positive = discharge, negative = charge"
		}
		switch {
		case p.confirmed == 0:
			fmt.Fprintf(w, "  consistent with %s (not yet confirmed; note it in the integration doc)\n", verdict)
		case p.confirmed == found:
			fmt.Fprintf(w, "  consistent with %s, as confirmed on site\n", verdict)
		default:
			fmt.Fprintf(w, "  CONTRADICTS the confirmed convention: %s on this device\n", verdict)
		}
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
