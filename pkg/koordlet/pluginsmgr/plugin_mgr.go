package pluginsmgr

type PluginsManager interface {
	Run(stopCh <-chan struct{}) error
}
