package config

import (
	"flag"
	"testing"
)

func TestConfiguration_InitFlags(t *testing.T) {
	// just ensure the function does not panic
	cfg := NewConfiguration()
	cfg.InitFlags(flag.CommandLine)
	flag.Parse()
}
