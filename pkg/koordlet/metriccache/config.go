package metriccache

import "flag"

type Config struct {
	MetricGCIntervalSeconds int
	MetricExpireSeconds     int
}

func NewDefaultConfig() *Config {
	return &Config{
		MetricGCIntervalSeconds: 300,
		MetricExpireSeconds:     1800,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.IntVar(&c.MetricGCIntervalSeconds, "MetricGCIntervalSeconds", c.MetricGCIntervalSeconds, "Collect node metrics interval by seconds")
	fs.IntVar(&c.MetricExpireSeconds, "MetricExpireSeconds", c.MetricExpireSeconds, "Collect pod metrics interval by seconds")
}
