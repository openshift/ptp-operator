package ptp

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadConfiguration(t *testing.T) {
	cfg := Configuration{}
	path, _ := os.Getwd()
	loadConfiguration(fmt.Sprintf("%s/%s", path, "testdata/cfg1.yml"), &cfg)
	assert.Equal(t, int64(5), cfg.MasterOffsetContinuousConfig.Duration)
	assert.Equal(t, 100, cfg.MasterOffsetContinuousConfig.MaxOffset)
	assert.Equal(t, -100, cfg.MasterOffsetContinuousConfig.MinOffset)
	assert.Equal(t, true, cfg.MasterOffsetContinuousConfig.Enable)
	assert.Equal(t, true, cfg.MasterOffsetContinuousConfig.FailFast)
	cfg = Configuration{}
	loadConfiguration(fmt.Sprintf("%s/%s", path, "testdata/cfg2.yml"), &cfg)
	assert.Equal(t, int64(9), cfg.MasterOffsetContinuousConfig.Duration)
	assert.Equal(t, 19, cfg.MasterOffsetContinuousConfig.MaxOffset)
	assert.Equal(t, -15, cfg.MasterOffsetContinuousConfig.MinOffset)
	assert.Equal(t, false, cfg.MasterOffsetContinuousConfig.Enable)
	assert.Equal(t, false, cfg.MasterOffsetContinuousConfig.FailFast)
}
