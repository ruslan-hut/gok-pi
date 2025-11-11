package config

import (
	"fmt"
	"github.com/ilyakaznacheev/cleanenv"
	"gok-pi/battery/entity"
	"log"
	"sync"
)

type Config struct {
	Env           string                 `yaml:"env" env-default:"local" env-required:"true"`
	Metrics       MetricsServer          `yaml:"metrics"`
	RemoteControl RemoteControl          `yaml:"remote_control"`
	Batteries     []entity.BatteryConfig `yaml:"batteries"`
	Schedules     []entity.Schedule      `yaml:"schedules"`
}

type MetricsServer struct {
	Enabled bool   `yaml:"enabled" env-default:"false"`
	Bind    string `yaml:"bind" env-default:"0.0.0.0"`
	Port    string `yaml:"port" env-default:"5001"`
}

type RemoteControl struct {
	Enabled      bool             `yaml:"enabled" env-default:"false"`
	ServerURL    string           `yaml:"server_url" env-default:"wss://localhost:8443/api/agent"`
	SharedSecret string           `yaml:"shared_secret" env-default:""`
	Reconnect    ReconnectBackoff `yaml:"reconnect"`
}

type ReconnectBackoff struct {
	InitialSeconds int `yaml:"initial_seconds" env-default:"5"`
	MaxSeconds     int `yaml:"max_seconds" env-default:"60"`
}

var instance *Config
var once sync.Once

func MustLoad(path string) *Config {
	var err error
	once.Do(func() {
		instance = &Config{}
		if err = cleanenv.ReadConfig(path, instance); err != nil {
			desc, _ := cleanenv.GetDescription(instance, nil)
			err = fmt.Errorf("%s; %s", err, desc)
			instance = nil
			log.Fatal(err)
		}
	})
	return instance
}
