package observers

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var socGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "battery",
	Name:      "RSoC",
	Help:      "Relative state of charge in percent",
}, []string{"name"})

func UpdateSoC(name string, value float64) {
	socGauge.WithLabelValues(name).Set(value)
	updateSnapshot(name, func(snapshot *Snapshot) {
		snapshot.RSOC = value
	})
}

var uSocGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "battery",
	Name:      "USoC",
	Help:      "User state of charge in percent",
}, []string{"name"})

func UpdateUSoC(name string, value float64) {
	uSocGauge.WithLabelValues(name).Set(value)
	updateSnapshot(name, func(snapshot *Snapshot) {
		snapshot.USOC = value
	})
}

var capacityGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "battery",
	Name:      "RemainingCapacity_W",
	Help:      "Remaining capacity based on RSoC",
}, []string{"name"})

func UpdateCapacity(name string, value float64) {
	capacityGauge.WithLabelValues(name).Set(value)
	updateSnapshot(name, func(snapshot *Snapshot) {
		snapshot.RemainingCapacityWh = value
	})
}

var consumptionGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "battery",
	Name:      "Consumption_W",
	Help:      "House consumption in Watts, direct measurement",
}, []string{"name"})

func UpdateConsumption(name string, value float64) {
	consumptionGauge.WithLabelValues(name).Set(value)
	updateSnapshot(name, func(snapshot *Snapshot) {
		snapshot.ConsumptionW = value
	})
}

var pacGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "battery",
	Name:      "Pac_total_W",
	Help:      "AC Power: greater than zero - discharging, less than zero - charging in Watts",
}, []string{"name"})

func UpdatePac(name string, value float64) {
	pacGauge.WithLabelValues(name).Set(value)
	updateSnapshot(name, func(snapshot *Snapshot) {
		snapshot.PacTotalW = value
	})
}

var dischargeStateGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "battery",
	Name:      "BatteryDischarging",
	Help:      "Discharge status: 1 - discharging, 0 - not discharging",
}, []string{"name"})

func UpdateDischargeState(name string, state bool) {
	setBoolGauge(dischargeStateGauge, name, state)
	updateSnapshot(name, func(snapshot *Snapshot) {
		snapshot.BatteryDischarging = state
		snapshot.BatteryDischargingSet = true
	})
}

var chargeStateGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "battery",
	Name:      "BatteryCharging",
	Help:      "Charge status: 1 - charging, 0 - not charging",
}, []string{"name"})

func UpdateChargeState(name string, state bool) {
	setBoolGauge(chargeStateGauge, name, state)
	updateSnapshot(name, func(snapshot *Snapshot) {
		snapshot.BatteryCharging = state
		snapshot.BatteryChargingSet = true
	})
}

var opModeGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "battery",
	Name:      "BatteryOperatingMode",
	Help:      "Operating mode: 1 - manual, 2 - auto",
}, []string{"name"})

func UpdateOpMode(name string, value string) {
	state, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return
	}
	opModeGauge.WithLabelValues(name).Set(state)
	updateSnapshot(name, func(snapshot *Snapshot) {
		snapshot.OperatingMode = value
		snapshot.OperatingModeSet = true
	})
}

func UpdateStatus(name string, status string) {
	updateSnapshot(name, func(snapshot *Snapshot) {
		snapshot.Status = status
	})
}

// UpdateOverride records why a battery is running outside its schedules. An
// empty source clears the override, but only when it comes from the direction
// that set it: the charge and discharge controllers for one battery share this
// snapshot, and the idle one would otherwise erase the running one's state.
func UpdateOverride(name, direction, source string) {
	updateSnapshot(name, func(snapshot *Snapshot) {
		if source == "" {
			if snapshot.OverrideDirection != direction {
				return
			}
			snapshot.OverrideSource = ""
			snapshot.OverrideDirection = ""
			return
		}
		snapshot.OverrideSource = source
		snapshot.OverrideDirection = direction
	})
}

func setBoolGauge(gauge *prometheus.GaugeVec, name string, state bool) {
	if state {
		gauge.WithLabelValues(name).Set(1.0)
	} else {
		gauge.WithLabelValues(name).Set(0.0)
	}
}

// Reading is one complete battery status poll.
type Reading struct {
	RSOC                float64
	USOC                float64
	RemainingCapacityWh float64
	ConsumptionW        float64
	PacTotalW           float64
	BatteryDischarging  bool
	BatteryCharging     bool
	OperatingMode       string
	Status              string
}

// UpdateBattery records a complete reading: it updates every Prometheus gauge and
// emits exactly one snapshot notification. Each per-field Update* helper notifies
// listeners on its own, so a caller holding a whole reading must use this instead —
// otherwise a single poll produces one telemetry row per field, multiplying spool
// writes and uplink traffic by the number of fields.
func UpdateBattery(name string, r Reading) {
	socGauge.WithLabelValues(name).Set(r.RSOC)
	uSocGauge.WithLabelValues(name).Set(r.USOC)
	capacityGauge.WithLabelValues(name).Set(r.RemainingCapacityWh)
	consumptionGauge.WithLabelValues(name).Set(r.ConsumptionW)
	pacGauge.WithLabelValues(name).Set(r.PacTotalW)
	setBoolGauge(dischargeStateGauge, name, r.BatteryDischarging)
	setBoolGauge(chargeStateGauge, name, r.BatteryCharging)

	opMode, opModeErr := strconv.ParseFloat(r.OperatingMode, 64)
	if opModeErr == nil {
		opModeGauge.WithLabelValues(name).Set(opMode)
	}

	updateSnapshot(name, func(snapshot *Snapshot) {
		snapshot.RSOC = r.RSOC
		snapshot.USOC = r.USOC
		snapshot.RemainingCapacityWh = r.RemainingCapacityWh
		snapshot.ConsumptionW = r.ConsumptionW
		snapshot.PacTotalW = r.PacTotalW
		snapshot.BatteryDischarging = r.BatteryDischarging
		snapshot.BatteryDischargingSet = true
		snapshot.BatteryCharging = r.BatteryCharging
		snapshot.BatteryChargingSet = true
		if opModeErr == nil {
			snapshot.OperatingMode = r.OperatingMode
			snapshot.OperatingModeSet = true
		}
		if r.Status != "" {
			snapshot.Status = r.Status
		}
	})
}
