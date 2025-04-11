package entity

type Schedule struct {
	StartTime   string `yaml:"start_time" env-default:"18:00"`
	StopTime    string `yaml:"stop_time" env-default:"22:00"`
	BatteryName string `yaml:"battery_name" env-required:"battery1"`
	Enabled     bool   `yaml:"enabled" env-default:"true"`
	PowerLimit  int    `yaml:"power_limit" env-default:"1000"`
	SocLimit    int    `yaml:"soc_limit" env-default:"50"`
}
