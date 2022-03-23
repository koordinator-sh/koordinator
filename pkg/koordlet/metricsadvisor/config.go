package metricsadvisor

import "flag"

type Config struct {
	CollectResUsedIntervalSeconds     int
	CollectNodeCPUInfoIntervalSeconds int
}

func NewDefaultConfig() *Config {
	return &Config{
		CollectResUsedIntervalSeconds:     1,
		CollectNodeCPUInfoIntervalSeconds: 60,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.IntVar(&c.CollectResUsedIntervalSeconds, "CollectResUsedIntervalSeconds", c.CollectResUsedIntervalSeconds, "Collect node/pod resource usage interval by seconds")
	fs.IntVar(&c.CollectNodeCPUInfoIntervalSeconds, "CollectNodeCPUInfoIntervalSeconds", c.CollectNodeCPUInfoIntervalSeconds, "Collect node cpu info interval by seconds")
}
