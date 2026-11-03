package huawei

import (
	"context"
	"errors"
	"fmt"
	"gok-pi/battery/driver"
	"gok-pi/battery/entity"
	"gok-pi/internal/lib/sl"
	"gok-pi/internal/modbus"
	"log/slog"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

func init() {
	driver.Register("huawei", New)
}

const (
	defaultPort      = "502"
	defaultMeterUnit = 3
	loggerUnit       = 0
	maxUnit          = 247

	requestTimeout = 5 * time.Second
	statusTimeout  = 15 * time.Second
	// idleClose releases the logger's client slot once polling has stopped. The
	// driver interface has no Close, and a worker restart or a battery scan
	// simply drops the driver.
	idleClose = 30 * time.Second

	// activeThresholdW separates real charge or discharge from measurement noise
	// around zero.
	activeThresholdW = 100.0

	// opModeAuto is the mode SystemStatus uses for a battery that manages itself.
	// The SmartLogger always runs the array while this driver is read-only.
	opModeAuto = "2"
)

// ErrReadOnly is returned for any command that would change the battery's state.
var ErrReadOnly = errors.New("huawei driver is read-only")

// Driver reads a whole ESS array through its SmartLogger (unit 0) and grid meter.
// It never constructs a modbus.Writer, so it cannot alter the equipment.
type Driver struct {
	client    *modbus.Client
	meterUnit uint8
	log       *slog.Logger

	mu   sync.Mutex
	idle *time.Timer
}

// New creates a driver from a URL of the form modbus://host:port?meter_unit=3.
// The scheme, port and meter_unit are optional.
func New(cfg entity.BatteryConfig, log *slog.Logger) (driver.Driver, error) {
	addr, meterUnit, err := parseEndpoint(cfg.Url)
	if err != nil {
		return nil, err
	}

	log.With(
		slog.String("addr", addr),
		slog.Int("meter_unit", int(meterUnit)),
	).Info("creating huawei read-only client")

	return &Driver{
		client:    modbus.New(addr, requestTimeout),
		meterUnit: meterUnit,
		log:       log.With(sl.Module("huawei")),
	}, nil
}

func parseEndpoint(raw string) (string, uint8, error) {
	if !strings.Contains(raw, "://") {
		raw = "modbus://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", 0, fmt.Errorf("parsing url: %w", err)
	}
	if u.Scheme != "modbus" {
		return "", 0, fmt.Errorf("url scheme %q: want modbus", u.Scheme)
	}
	if u.Hostname() == "" {
		return "", 0, errors.New("url has no host")
	}

	port := u.Port()
	if port == "" {
		port = defaultPort
	}

	meterUnit := uint64(defaultMeterUnit)
	if v := u.Query().Get("meter_unit"); v != "" {
		meterUnit, err = strconv.ParseUint(v, 10, 8)
		if err != nil || meterUnit > maxUnit {
			return "", 0, fmt.Errorf("meter_unit %q: want 0..%d", v, maxUnit)
		}
	}

	return net.JoinHostPort(u.Hostname(), port), uint8(meterUnit), nil
}

// Status reads the array and the grid meter. A failure in either fails the whole
// reading, because consumption is derived from both.
func (d *Driver) Status() (*entity.SystemStatus, error) {
	d.stopIdleTimer()
	defer d.startIdleTimer()

	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()

	logger, err := d.read(ctx, loggerUnit, []Register{
		LoggerSOC, LoggerDischargeableCapacity, LoggerESSActivePower, LoggerPVActivePower,
	})
	if err != nil {
		return nil, fmt.Errorf("reading logger: %w", err)
	}

	meter, err := d.read(ctx, d.meterUnit, []Register{MeterActivePower})
	if err != nil {
		return nil, fmt.Errorf("reading meter: %w", err)
	}

	soc := logger[LoggerSOC.Addr]
	if soc < 0 || soc > 100 {
		return nil, fmt.Errorf("implausible soc %g", soc)
	}

	// 40392 matches Sonnen, negative while charging; its discharge direction is
	// inferred until observed on site. The meter is the opposite of its document:
	// on site, positive power accompanies a rising import counter (2026-11-03).
	essW := wholeWatts(logger[LoggerESSActivePower.Addr])
	pvW := wholeWatts(logger[LoggerPVActivePower.Addr])
	feedInW := 0 - wholeWatts(meter[MeterActivePower.Addr]) // not unary minus: an idle meter must not read -0

	return &entity.SystemStatus{
		USOC:                soc,
		RSOC:                soc,
		RemainingCapacityWh: wholeWatts(logger[LoggerDischargeableCapacity.Addr]),
		PacTotalW:           essW,
		ProductionW:         pvW,
		GridFeedInW:         feedInW,
		ConsumptionW:        max(pvW+essW-feedInW, 0),
		BatteryCharging:     essW < -activeThresholdW,
		BatteryDischarging:  essW > activeThresholdW,
		OperatingMode:       opModeAuto,
	}, nil
}

// wholeWatts converts kW or kWh to a whole number of W or Wh. The registers hold whole watts, and
// without the rounding the division by gain and this multiplication leave a binary fraction behind.
func wholeWatts(kilo float64) float64 {
	return math.Round(kilo * 1000)
}

// read fetches regs from one unit, one contiguous block at a time, so that a
// request never spans an undocumented address the device might refuse.
func (d *Driver) read(ctx context.Context, unit uint8, regs []Register) (map[uint16]float64, error) {
	out := make(map[uint16]float64, len(regs))

	for _, b := range Blocks(regs, 0) {
		words, err := d.client.ReadHoldingRegisters(ctx, unit, b.Addr, b.Count)
		if err != nil {
			return nil, fmt.Errorf("block %d+%d: %w", b.Addr, b.Count, err)
		}

		for _, r := range regs {
			w, ok := b.Extract(words, r)
			if !ok {
				continue
			}

			raw, err := r.DecodeRaw(w)
			if err != nil {
				return nil, err
			}

			// The type's maximum marks a value the device does not have.
			if _, hi := r.rawLimits(); raw == hi {
				return nil, fmt.Errorf("register %s has no value", r)
			}

			out[r.Addr] = float64(raw) / float64(r.gain())
		}
	}

	return out, nil
}

func (d *Driver) stopIdleTimer() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.idle != nil {
		d.idle.Stop()
	}
}

func (d *Driver) startIdleTimer() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.idle = time.AfterFunc(idleClose, func() {
		if err := d.client.Close(); err != nil {
			d.log.Debug("closing idle connection", sl.Err(err))
		}
	})
}

// StartDischarge refuses: this driver only reads.
func (d *Driver) StartDischarge(int) error { return ErrReadOnly }

// StartCharge refuses: this driver only reads.
func (d *Driver) StartCharge(int) error { return ErrReadOnly }

// SwitchOperatingModeToManual refuses: this driver only reads.
func (d *Driver) SwitchOperatingModeToManual(string) error { return ErrReadOnly }

// StopDischarge succeeds without writing: this driver never starts a discharge.
func (d *Driver) StopDischarge() error { return nil }

// StopCharge succeeds without writing: this driver never starts a charge.
func (d *Driver) StopCharge() error { return nil }

// SwitchOperatingModeToAuto succeeds without writing: the SmartLogger always
// manages the array.
func (d *Driver) SwitchOperatingModeToAuto(string) error { return nil }
