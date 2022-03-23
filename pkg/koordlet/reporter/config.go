package reporter

import "flag"

type Config struct {
	ReportIntervalSeconds int
}

func NewDefaultConfig() *Config {
	return &Config{
		ReportIntervalSeconds: 60,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.IntVar(&c.ReportIntervalSeconds, "ReportIntervalSeconds", c.ReportIntervalSeconds, "Report interval by seconds")
}
