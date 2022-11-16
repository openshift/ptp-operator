package test

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadConfiguration(t *testing.T) {
	cfg := PtpTestConfig{}
	path, _ := os.Getwd()
	cfg.loadPtpTestConfig(fmt.Sprintf("%s/%s", path, "testdata/cfg1.yaml"))
	assert.Equal(t, int64(5), cfg.SoakTestConfig.MasterOffsetConfig.Duration)
	assert.Equal(t, 100, cfg.GlobalConfig.MaxOffset)
	assert.Equal(t, -100, cfg.GlobalConfig.MinOffset)
	assert.Equal(t, true, cfg.SoakTestConfig.MasterOffsetConfig.Enable)
	assert.Equal(t, true, cfg.SoakTestConfig.MasterOffsetConfig.FailFast)
	cfg = PtpTestConfig{}
	cfg.loadPtpTestConfig(fmt.Sprintf("%s/%s", path, "testdata/cfg2.yaml"))
	assert.Equal(t, int64(9), cfg.SoakTestConfig.MasterOffsetConfig.Duration)
	assert.Equal(t, 19, cfg.GlobalConfig.MaxOffset)
	assert.Equal(t, -15, cfg.GlobalConfig.MinOffset)
	assert.Equal(t, false, cfg.SoakTestConfig.MasterOffsetConfig.Enable)
	assert.Equal(t, false, cfg.SoakTestConfig.MasterOffsetConfig.FailFast)
}
