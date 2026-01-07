package entity

import (
	"fmt"
	"time"
)

type Schedule struct {
	Name            string     `yaml:"name" json:"name" env-default:""`
	Type            string     `yaml:"type" json:"type" env-default:"discharge"` // "charge" or "discharge"
	StartTime       string     `yaml:"start_time" json:"start_time" env-default:"18:00"`
	StopTime        string     `yaml:"stop_time" json:"stop_time" env-default:"22:00"`
	BatteryName     string     `yaml:"battery_name" json:"battery_name" env-required:"battery1"`
	Enabled         bool       `yaml:"enabled" json:"enabled" env-default:"false"`
	PowerLimit      int        `yaml:"power_limit" json:"power_limit" env-default:"1000"`
	SocLimit        int        `yaml:"soc_limit" json:"soc_limit" env-default:"50"`
	RunOnce         bool       `yaml:"run_once" json:"run_once" env-default:"false"`
	GoalReachedTime *time.Time `yaml:"goal_reached_time,omitempty" json:"goal_reached_time,omitempty"`
}

// Validate checks if the schedule configuration is valid.
func (s *Schedule) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("schedule name is required")
	}
	if s.Type != "" && s.Type != "discharge" && s.Type != "charge" {
		return fmt.Errorf("invalid schedule type %q: must be 'discharge' or 'charge'", s.Type)
	}
	if _, err := time.Parse("15:04", s.StartTime); err != nil {
		return fmt.Errorf("invalid start_time %q: expected format HH:MM", s.StartTime)
	}
	if _, err := time.Parse("15:04", s.StopTime); err != nil {
		return fmt.Errorf("invalid stop_time %q: expected format HH:MM", s.StopTime)
	}
	if s.PowerLimit < 0 {
		return fmt.Errorf("power_limit cannot be negative")
	}
	if s.SocLimit < 0 || s.SocLimit > 100 {
		return fmt.Errorf("soc_limit must be between 0 and 100")
	}
	return nil
}

// ValidateSchedules validates a collection of schedules, checking both individual
// schedule validity and ensuring schedule names are unique.
func ValidateSchedules(schedules []Schedule) error {
	names := make(map[string]bool)
	for i, s := range schedules {
		if err := s.Validate(); err != nil {
			return fmt.Errorf("schedule[%d] %q: %w", i, s.Name, err)
		}
		if s.Name != "" {
			if names[s.Name] {
				return fmt.Errorf("duplicate schedule name: %q", s.Name)
			}
			names[s.Name] = true
		}
	}
	return nil
}
