package entity

type Schedule struct {
	Name        string `yaml:"name" json:"name" env-default:""`
	StartTime   string `yaml:"start_time" json:"start_time" env-default:"18:00"`
	StopTime    string `yaml:"stop_time" json:"stop_time" env-default:"22:00"`
	BatteryName string `yaml:"battery_name" json:"battery_name" env-required:"battery1"`
	Enabled     bool   `yaml:"enabled" json:"enabled" env-default:"false"`
	PowerLimit  int    `yaml:"power_limit" json:"power_limit" env-default:"1000"`
	SocLimit    int    `yaml:"soc_limit" json:"soc_limit" env-default:"50"`
}
