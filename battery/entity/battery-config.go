package entity

type BatteryConfig struct {
	Name          string `yaml:"name" json:"name" env-default:"battery1"`
	Url           string `yaml:"url" json:"url" env-default:"https://example.battery/api"`
	Token         string `yaml:"token" json:"token" env-default:"auth-token"`
	Enabled       bool   `yaml:"enabled" json:"enabled" env-default:"true"`
	CapacityLimit int    `yaml:"capacity_limit" json:"capacity_limit" env-default:"20000"`
}
