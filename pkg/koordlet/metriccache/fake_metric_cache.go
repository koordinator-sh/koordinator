package metriccache

func NewCacheNotShareMetricCache(cfg *Config) (MetricCache, error) {
	database, err := NewCacheNotShareStorage()
	if err != nil {
		return nil, err
	}
	return &metricCache{
		config: cfg,
		db:     database,
	}, nil
}

func NewCacheNotShareStorage() (*storage, error) {
	return newStorage("file::memory:?mode=memory&loc=auto&_busy_timeout=5000")
}
