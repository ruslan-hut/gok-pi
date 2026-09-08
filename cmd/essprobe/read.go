package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gok-pi/battery/driver/huawei"
	"gok-pi/internal/modbus"
)

// reader fetches registers in batched 0x03 requests, retrying the transient
// exceptions the ESS is documented to raise.
type reader struct {
	client  *modbus.Client
	unit    uint8
	retries int
}

// sample is one pass over a set of registers. Registers the device refused are
// recorded in Failed rather than aborting the pass, so a single unsupported
// span does not cost the whole reading — an installation with fewer subsystems
// than the point table assumes will refuse some addresses every time.
type sample struct {
	At     time.Time
	Raw    map[uint16]int64
	Values map[uint16]float64
	Failed map[uint16]string
}

func newSample() *sample {
	return &sample{
		At:     time.Now().UTC(),
		Raw:    map[uint16]int64{},
		Values: map[uint16]float64{},
		Failed: map[uint16]string{},
	}
}

// read fetches every register in regs, batching them into as few requests as
// the point table allows.
func (r *reader) read(ctx context.Context, regs []huawei.Register) (*sample, error) {
	s := newSample()
	blocks := huawei.Blocks(regs, huawei.DefaultGap)

	byBlock := make(map[huawei.Block][]huawei.Register, len(blocks))
	for _, reg := range regs {
		for _, b := range blocks {
			if b.Contains(reg) {
				byBlock[b] = append(byBlock[b], reg)

				break
			}
		}
	}

	var firstErr error
	for _, b := range blocks {
		words, err := r.readBlock(ctx, b)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			for _, reg := range byBlock[b] {
				s.Failed[reg.Addr] = err.Error()
			}
			if firstErr == nil {
				firstErr = err
			}

			continue
		}

		for _, reg := range byBlock[b] {
			w, ok := b.Extract(words, reg)
			if !ok {
				s.Failed[reg.Addr] = "register fell outside its own read block"

				continue
			}

			raw, err := reg.DecodeRaw(w)
			if err != nil {
				s.Failed[reg.Addr] = err.Error()

				continue
			}
			s.Raw[reg.Addr] = raw

			if v, err := reg.Decode(w); err == nil {
				s.Values[reg.Addr] = v
			}
		}
	}

	// Every block failing means the endpoint is unusable, not that the point
	// table is wrong; report that rather than handing back an empty sample.
	if len(s.Raw) == 0 && firstErr != nil {
		return nil, firstErr
	}

	return s, nil
}

// readBlock issues one 0x03, retrying the exceptions the device says are
// transient and the transport errors that a reconnect may clear.
func (r *reader) readBlock(ctx context.Context, b huawei.Block) ([]uint16, error) {
	var err error

	for attempt := 0; attempt <= r.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}

		var words []uint16
		words, err = r.client.ReadHoldingRegisters(ctx, r.unit, b.Addr, b.Count)
		if err == nil {
			return words, nil
		}

		var exc modbus.Exception
		if errors.As(err, &exc) && !exc.Retryable() {
			// The device understood and refused: asking again will not help.
			return nil, err
		}
	}

	return nil, fmt.Errorf("read block %d+%d: %w", b.Addr, b.Count, err)
}

// value returns the decoded value of reg from the sample.
func (s *sample) value(reg huawei.Register) (float64, bool) {
	v, ok := s.Values[reg.Addr]

	return v, ok
}

// raw returns the wire-level integer of reg from the sample.
func (s *sample) raw(reg huawei.Register) (int64, bool) {
	v, ok := s.Raw[reg.Addr]

	return v, ok
}
