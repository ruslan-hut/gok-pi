package entity

type BatteryConfig struct {
	Name          string `yaml:"name" json:"name" env-default:"battery1"`
	Url           string `yaml:"url" json:"url" env-default:"https://example.battery/api"`
	Token         string `yaml:"token" json:"token" env-default:"auth-token"`
	Enabled       bool   `yaml:"enabled" json:"enabled" env-default:"true"`
	AutoSchedule  bool   `yaml:"auto_schedule" json:"auto_schedule"`
	CapacityLimit int    `yaml:"capacity_limit" json:"capacity_limit" env-default:"20000"`
	PowerLimit    int    `yaml:"power_limit" json:"power_limit" env-default:"0"` // Default power limit in W (0 means not set)
	SocLimit      int    `yaml:"soc_limit" json:"soc_limit" env-default:"0"`     // Default SoC limit in % (0 means not set)
}
