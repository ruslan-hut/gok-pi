package discharger

import (
	"fmt"
	"gok-pi/battery/entity"
	"gok-pi/internal/lib/sl"
	"gok-pi/internal/lib/timer"
	"log/slog"
	"time"
)

type Client interface {
	Status() (*entity.BatteryInfo, error)
	StartDischarge() error
	StopDischarge() error
}

type Discharge struct {
	startTime    string
	stopTime     string
	batteryLimit float64
	client       Client
	log          *slog.Logger
}

func New(startTime, stopTime string, batteryLimit int, client Client, log *slog.Logger) (*Discharge, error) {
	return &Discharge{
		startTime:    startTime,
		stopTime:     stopTime,
		batteryLimit: float64(batteryLimit),
		client:       client,
		log:          log.With(sl.Module("battery.discharge")),
	}, nil
}

func (d *Discharge) Run() error {
	for {
		// Calculate the start and stop times for today
		startTime, err := timer.ParseTime(d.startTime)
		if err != nil {
			return fmt.Errorf("parsing start time: %w", err)
		}
		stopTime, err := timer.ParseTime(d.stopTime)
		if err != nil {
			return fmt.Errorf("parsing stop time: %w", err)
		}
		if startTime.After(stopTime) {
			stopTime = stopTime.Add(24 * time.Hour)
		}
		now := time.Now()
		d.log.With(
			slog.String("start_time", startTime.Format(time.DateTime)),
			slog.String("stop_time", stopTime.Format(time.DateTime)),
			slog.String("now", now.Format(time.DateTime)),
			slog.Float64("limit", d.batteryLimit),
		).Info("next cycle")

		// If start time has passed for today, schedule for the next day
		if now.After(stopTime) {
			startTime = startTime.Add(24 * time.Hour)
		}

		startTimer := time.NewTimer(startTime.Sub(now))

		d.log.With(slog.Time("start_time", startTime)).Info("waiting until start time")
		<-startTimer.C

		// Check the battery status
		d.log.With(slog.Float64("limit", d.batteryLimit)).Info("starting battery discharge process...")
		status, err := d.client.Status()
		if err != nil {
			d.log.With(sl.Err(err)).Error("checking battery status")
			continue
		}

		if status.UsableRemainingCapacity > d.batteryLimit {
			err = d.client.StartDischarge()
			if err != nil {
				d.log.With(sl.Err(err)).Error("starting discharge")
			}

			// Start monitoring battery status during discharge
			d.monitorState(stopTime)

		} else {
			d.log.Info("battery level is below the limit, no discharge needed.")
		}

		d.log.Info("waiting for the next cycle...")
		time.Sleep(24*time.Hour - time.Now().Sub(startTime))
	}
}

func (d *Discharge) monitorState(stopTime time.Time) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	stopTimer := time.NewTimer(stopTime.Sub(time.Now()))

	for {
		select {
		case <-ticker.C:
			status, err := d.client.Status()
			if err != nil {
				d.log.With(sl.Err(err)).Error("checking battery status")
				continue
			}

			if status.UsableRemainingCapacity <= d.batteryLimit {
				d.log.Info("battery level reached the limit, stopping discharge")
				err = d.client.StopDischarge()
				if err != nil {
					d.log.With(sl.Err(err)).Error("stopping discharge")
					continue
				}
				return
			}

		case <-stopTimer.C:
			d.log.Info("stop time reached, stopping discharge")
			err := d.client.StopDischarge()
			if err != nil {
				d.log.With(sl.Err(err)).Error("stopping discharge")
			}
			return
		}
	}
}
