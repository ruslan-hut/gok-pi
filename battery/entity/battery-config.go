package entity

type BatteryConfig struct {
	Name          string `yaml:"name" env-default:"battery1"`
	Url           string `yaml:"url" env-default:"https://example.battery/api"`
	Token         string `yaml:"token" env-default:"auth-token"`
	Enabled       bool   `yaml:"enabled" env-default:"true"`
	Discharge     bool   `yaml:"discharge" env-default:"false"`
	CapacityLimit int    `yaml:"capacity_limit" env-default:"20000"`
}
