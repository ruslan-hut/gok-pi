package huawei

import (
	"fmt"
	"math"
)

// Modbus carries multi-register values high word first, and each word high byte
// first (section 4.3.3.4). Everything below assumes words are already in that
// order, as returned by a Modbus client's ReadHoldingRegisters.

// Decode converts the raw words read for r into its engineering value, applying
// the sign of r.Kind and dividing by r.Gain. It errors if words does not hold
// exactly r.Words() entries, or if r is write-only.
func (r Register) Decode(words []uint16) (float64, error) {
	raw, err := r.DecodeRaw(words)
	if err != nil {
		return 0, err
	}

	return float64(raw) / float64(r.gain()), nil
}

// DecodeRaw converts the raw words read for r into the integer on the wire,
// before Gain is applied. Use it for enumerations and bitfields, where the
// engineering value is the integer itself.
func (r Register) DecodeRaw(words []uint16) (int64, error) {
	if !r.Access.Readable() {
		return 0, fmt.Errorf("register %s is write-only", r)
	}

	if n := int(r.Words()); len(words) != n {
		return 0, fmt.Errorf("register %s: got %d words, want %d", r, len(words), n)
	}

	var u uint64
	for _, w := range words {
		u = u<<16 | uint64(w)
	}

	if !r.Kind.Signed() {
		return int64(u), nil
	}

	// Sign-extend from the register's own width.
	shift := 64 - 16*uint(r.Words())

	return int64(u<<shift) >> shift, nil
}

// Encode converts an engineering value into the words to write to r. The value
// is scaled by r.Gain and rounded to the nearest integer, then checked against
// the register's documented range and the range its Kind can represent.
func (r Register) Encode(v float64) ([]uint16, error) {
	if !r.Access.Writable() {
		return nil, fmt.Errorf("register %s is read-only", r)
	}

	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, fmt.Errorf("register %s: value %v is not a number", r, v)
	}

	if r.Bounded && (v < r.Min || v > r.Max) {
		return nil, fmt.Errorf("register %s: value %g out of documented range [%g, %g]", r, v, r.Min, r.Max)
	}

	return r.EncodeRaw(int64(math.Round(v * float64(r.gain()))))
}

// EncodeRaw converts a wire-level integer into the words to write to r, without
// applying Gain or the documented range. Use it for enumerations and bitfields.
func (r Register) EncodeRaw(raw int64) ([]uint16, error) {
	if !r.Access.Writable() {
		return nil, fmt.Errorf("register %s is read-only", r)
	}

	lo, hi := r.rawLimits()
	if raw < lo || raw > hi {
		return nil, fmt.Errorf("register %s: raw value %d does not fit %s [%d, %d]", r, raw, r.Kind, lo, hi)
	}

	n := int(r.Words())
	words := make([]uint16, n)
	u := uint64(raw)
	for i := n - 1; i >= 0; i-- {
		words[i] = uint16(u)
		u >>= 16
	}

	return words, nil
}

// rawLimits is the inclusive range of wire values r.Kind can carry.
func (r Register) rawLimits() (int64, int64) {
	bits := 16 * uint(r.Words())
	if r.Kind.Signed() {
		return -1 << (bits - 1), 1<<(bits-1) - 1
	}

	return 0, 1<<bits - 1
}

// gain guards against a zero Gain on a hand-written Register, which would
// otherwise turn every decoded value into ±Inf or NaN.
func (r Register) gain() int {
	if r.Gain == 0 {
		return 1
	}

	return r.Gain
}

// Bit reports whether the given bit of a Bitfield16 word is set. Bit 0 is the
// least significant, matching the Bit column of the alarm definition table.
func Bit(word uint16, bit int) bool { return bit >= 0 && bit < 16 && word&(1<<uint(bit)) != 0 }
