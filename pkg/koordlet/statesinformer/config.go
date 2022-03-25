package statesinformer

import "flag"

type Config struct {
	KubeletIPAddr              string
	KubeletHTTPPort            int
	KubeletSyncIntervalSeconds int
	KubeletSyncTimeoutSeconds  int
}

func NewDefaultConfig() *Config {
	return &Config{
		KubeletIPAddr:              "localhost",
		KubeletHTTPPort:            10255,
		KubeletSyncIntervalSeconds: 1,
		KubeletSyncTimeoutSeconds:  3,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.KubeletIPAddr, "KubeletIPAddr", c.KubeletIPAddr, "Kubelet IP Address.")
	fs.IntVar(&c.KubeletHTTPPort, "KubeletHTTPPort", c.KubeletHTTPPort, "Kubelet HTTP httpPort.")
	fs.IntVar(&c.KubeletSyncIntervalSeconds, "KubeletSyncIntervalSeconds", c.KubeletSyncIntervalSeconds, "Kubelet sync interval by seconds.")
	fs.IntVar(&c.KubeletSyncTimeoutSeconds, "KubeletSyncTimeoutSeconds", c.KubeletSyncTimeoutSeconds, "Kubelet sync timeout by seconds.")
}
