// Package sysinfo reads the health of the board the agent runs on: SoC
// temperature and the Raspberry Pi firmware's throttling flags. Both come from
// sysfs, need no root and no vcgencmd, and are simply absent on other machines,
// so every field is optional and a missing file is not an error.
package sysinfo

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Throttling flags as reported by the Pi firmware (the same bitmask as
// `vcgencmd get_throttled`). The low bits are the current state; the same flags
// shifted by 16 record whether the condition has occurred since boot.
const (
	flagUnderVoltage = 1 << 0
	flagFreqCapped   = 1 << 1
	flagThrottled    = 1 << 2
	flagSoftTemp     = 1 << 3
	sinceBootShift   = 16
)

// Board is one reading of the host's health. A nil pointer field means the
// reading is not available on this machine.
type Board struct {
	TempC     *float64   `json:"temp_c,omitempty"`
	Throttled *Throttled `json:"throttled,omitempty"`
}

// Throttled is the decoded firmware bitmask. Raw is kept so a flag this decoder
// does not know about is still visible.
type Throttled struct {
	Raw                   uint32 `json:"raw"`
	UnderVoltage          bool   `json:"under_voltage"`
	FreqCapped            bool   `json:"freq_capped"`
	Throttled             bool   `json:"throttled"`
	SoftTempLimit         bool   `json:"soft_temp_limit"`
	UnderVoltageOccurred  bool   `json:"under_voltage_occurred"`
	FreqCappedOccurred    bool   `json:"freq_capped_occurred"`
	ThrottledOccurred     bool   `json:"throttled_occurred"`
	SoftTempLimitOccurred bool   `json:"soft_temp_limit_occurred"`
}

// Reader reads board health beneath a sysfs root. The root is a field so tests
// can point it at a fake tree.
type Reader struct {
	Root string
}

// Read returns the board health of the current host, or nil off a Pi.
func Read() *Board {
	return Reader{Root: "/sys"}.Read()
}

// Read returns whatever readings are available. It returns nil when there is
// nothing at all, so a caller can omit the whole block on non-Pi hosts.
func (r Reader) Read() *Board {
	b := Board{
		TempC:     r.temperature(),
		Throttled: r.throttled(),
	}
	if b.TempC == nil && b.Throttled == nil {
		return nil
	}
	return &b
}

func (r Reader) temperature() *float64 {
	raw, ok := readTrimmed(filepath.Join(r.Root, "class/thermal/thermal_zone0/temp"))
	if !ok {
		return nil
	}
	milli, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	c := float64(milli) / 1000
	return &c
}

// throttled finds the firmware attribute by glob: its parent directory is named
// after the SoC node, which differs between Pi models (soc on Pi 4, soc@... on
// Pi 5).
func (r Reader) throttled() *Throttled {
	matches, _ := filepath.Glob(filepath.Join(r.Root, "devices/platform/soc*/*firmware/get_throttled"))
	for _, path := range matches {
		raw, ok := readTrimmed(path)
		if !ok {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimPrefix(raw, "0x"), 16, 32)
		if err != nil {
			continue
		}
		t := decodeThrottled(uint32(v))
		return &t
	}
	return nil
}

func decodeThrottled(v uint32) Throttled {
	return Throttled{
		Raw:                   v,
		UnderVoltage:          v&flagUnderVoltage != 0,
		FreqCapped:            v&flagFreqCapped != 0,
		Throttled:             v&flagThrottled != 0,
		SoftTempLimit:         v&flagSoftTemp != 0,
		UnderVoltageOccurred:  v&(flagUnderVoltage<<sinceBootShift) != 0,
		FreqCappedOccurred:    v&(flagFreqCapped<<sinceBootShift) != 0,
		ThrottledOccurred:     v&(flagThrottled<<sinceBootShift) != 0,
		SoftTempLimitOccurred: v&(flagSoftTemp<<sinceBootShift) != 0,
	}
}

func readTrimmed(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(data))
	return s, s != ""
}
