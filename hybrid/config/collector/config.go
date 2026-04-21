/**
 * Licensed Materials - Property of gientech.com
 * (C) Copyright 2026 GienTech Technology Co., Ltd. All rights reserved.
 * 2026-01-30 @author yangwanjin
 */

package collector

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ExportModeLocal  = "local"
	ExportModeRemote = "remote"
)

const (
	// DefaultConfigurationName is the default name of configuration
	DefaultConfigurationName = "collector.yaml"
	// DefaultConfigurationPath the default location of the configuration file
	DefaultConfigurationPath = "/etc/hybrid/"
)

// Config Collector config
type Config struct {
	Upload     UploadConfig     `yaml:"upload"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
	Export     ExportConfig     `yaml:"export"`
	Queries    []QueryConfig    `yaml:"queries"`
}
type UploadConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}

// PrometheusConfig Prometheus config
type PrometheusConfig struct {
	URL     string        `yaml:"url"`
	Auth    AuthConfig    `yaml:"auth"`
	Timeout time.Duration `yaml:"timeout"`
}

type AuthConfig struct {
	Type     string `yaml:"type"`     // "basic", "bearer", "none"
	Username string `yaml:"username"` // Basic Auth username
	Password string `yaml:"password"` // Basic Auth password
	Token    string `yaml:"token"`    // Bearer Token
}

// ExportConfig export config
type ExportConfig struct {
	// Mode export mode, supported: "local" or "remote"
	Mode            string          `yaml:"mode"`
	LocalConfig     LocalConfig     `yaml:"localConfig"`
	WebSocketConfig WebSocketConfig `yaml:"remoteConfig"`
	Interval        time.Duration   `yaml:"interval"`
}

type LocalConfig struct {
	// OutputDir directory to store exported files
	OutputDir string `yaml:"outputDir"`
	// Format export format, supported: "timestamp" or "daily"
	Format string `yaml:"format"`
	// RetentionHours retention hours
	RetentionHours time.Duration `yaml:"retentionHours"`
}

type WebSocketConfig struct {
	URL               string            `yaml:"url"`
	ReconnectInterval time.Duration     `yaml:"reconnectInterval"`
	PingInterval      time.Duration     `yaml:"pingInterval"`
	WriteTimeout      time.Duration     `yaml:"writeTimeout"`
	ReadTimeout       time.Duration     `yaml:"readTimeout"`
	MaxMessageSize    int64             `yaml:"maxMessageSize"`
	Headers           map[string]string `yaml:"headers"` // 自定义请求头
}

// QueryConfig query config from prometheus
type QueryConfig struct {
	Name        string            `yaml:"name"`
	Query       string            `yaml:"query"`
	Range       string            `yaml:"range"`       // 指标时间范围, 如: "1h", "24h"
	Step        string            `yaml:"step"`        // 指标步长, 如: "1m", "5m"
	Labels      []string          `yaml:"labels"`      // 要保留的标签
	SheetName   string            `yaml:"sheetName"`   // Excel 工作表名称
	Description string            `yaml:"description"` // 查询描述
	ValueColumn string            `yaml:"valueColumn"` // 值列名称
	Metadata    map[string]string `yaml:"metadata"`    // 额外的元数据
}

func LoadConfig(path string) (*Config, error) {
	var cfg *Config
	var err error

	if path != "" {
		cfg, err = LoadFromFile(path)
	} else {
		cfg, err = InitConfig()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate configuration after loading
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate configuration: %w", err)
	}

	return cfg, nil
}

// LoadFromFile load config from specified file path
func LoadFromFile(filePath string) (*Config, error) {
	tempConf := Config{}
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(content, &tempConf)
	if err != nil {
		return nil, err
	}

	// set default values
	setDefaultConfig(&tempConf)
	return &tempConf, nil
}

// InitConfig initialize config from default location
func InitConfig() (*Config, error) {
	// load config from local file
	cfg, err := getFromLocal()
	if err != nil {
		return nil, err
	}
	// set default values
	setDefaultConfig(cfg)

	return cfg, nil
}

func getFromLocal() (*Config, error) {
	tempConf := Config{}
	if _, err := os.Stat(DefaultConfigurationPath + DefaultConfigurationName); err == nil {
		content, err := os.ReadFile(DefaultConfigurationPath + DefaultConfigurationName)
		if err != nil {
			return nil, err
		}
		err = yaml.Unmarshal(content, &tempConf)
		if err != nil {
			return nil, err
		}
		return &tempConf, nil
	}
	return nil, fmt.Errorf("local config file not found ,file_name: %s", DefaultConfigurationName)
}

func setDefaultConfig(cfg *Config) {
	if cfg.Prometheus.Timeout == 0 {
		cfg.Prometheus.Timeout = 30 * time.Second
	}

	if cfg.Export.Interval == 0 {
		cfg.Export.Interval = 2 * time.Hour
	}

	if cfg.Export.Mode == "" {
		cfg.Export.Mode = ExportModeLocal
	}

	if cfg.Export.Mode == ExportModeLocal && cfg.Export.LocalConfig.OutputDir == "" {
		cfg.Export.LocalConfig.OutputDir = "/data/hybrid-output"
	}

	// if export to excel
	if cfg.Export.LocalConfig.Format == "" {
		cfg.Export.LocalConfig.Format = "timestamp"
	}

	// set default values for each query, if export to excel
	for i := range cfg.Queries {
		if cfg.Queries[i].Range == "" {
			cfg.Queries[i].Range = "1h"
		}
		if cfg.Queries[i].Step == "" {
			cfg.Queries[i].Step = "3m"
		}
		if cfg.Queries[i].SheetName == "" {
			cfg.Queries[i].SheetName = cfg.Queries[i].Name
		}
		if cfg.Queries[i].ValueColumn == "" {
			cfg.Queries[i].ValueColumn = "value"
		}
	}
}

// Validate validate config
func (c *Config) Validate() error {
	// validate prometheus URL config
	if c.Prometheus.URL == "" {
		return fmt.Errorf("prometheus.url is required")
	}

	if c.Export.Mode == ExportModeRemote && c.Export.WebSocketConfig.URL == "" {
		return fmt.Errorf("webSocketConfig.url is required when exportMode is remote")
	}

	if len(c.Queries) == 0 {
		return fmt.Errorf("at least one query is required")
	}

	for i, q := range c.Queries {
		if q.Name == "" {
			return fmt.Errorf("queries[%d].name is required", i)
		}
		if q.Query == "" {
			return fmt.Errorf("queries[%d].query is required", i)
		}
	}
	return nil
}
