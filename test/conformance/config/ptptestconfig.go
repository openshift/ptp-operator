package test

import (
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

type yamlTimeDur time.Duration

type GlobalConfig struct {
	MinOffset int `yaml:"minoffset"`
	MaxOffset int `yaml:"maxoffset"`
}

type SoakTestConfig struct {
	EnableSoakTest     bool               `yaml:"enable"`
	MasterOffsetConfig MasterOffsetConfig `yaml:"masteroffset"`
	TestCaseAConfig    TestCaseA          `yaml:"testa"`
	TestCaseBConfig    TestCaseB          `yaml:"testb"`
}
type MasterOffsetConfig struct {
	Enable   bool  `yaml:"enable"`
	FailFast bool  `yaml:"failfast"`
	Duration int64 `yaml:"duration"`
}

type TestCaseA struct {
	Enable   bool  `yaml:"enable"`
	FailFast bool  `yaml:"failfast"`
	Duration int64 `yaml:"duration"`
}

type TestCaseB struct {
	Enable   bool  `yaml:"enable"`
	FailFast bool  `yaml:"failfast"`
	Duration int64 `yaml:"duration"`
}

type PtpTestConfig struct {
	GlobalConfig   GlobalConfig   `yaml:"global"`
	SoakTestConfig SoakTestConfig `yaml:"soaktest"`
}

var ptpTestConfig PtpTestConfig
var loaded bool

func (conf *PtpTestConfig) loadPtpTestConfig(filename string) {
	yamlFile, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Printf("yamlFile.Get err   #%v ", err)
	}
	err = yaml.Unmarshal(yamlFile, &conf)
	if err != nil {
		log.Fatalf("Unmarshal: %v", err)
	}
}

func GetPtpTestConfig() PtpTestConfig {
	if loaded {
		return ptpTestConfig
	}
	loaded = true

	// If config file path is provided, use that, otherwise continue with the provided config file
	path, ok := os.LookupEnv("PTP_TEST_CONFIG_FILE")
	if !ok {
		path = "../config/ptptestconfig.yaml"
	}

	ptpTestConfig.loadPtpTestConfig(path)
	logrus.Info("config=", ptpTestConfig)
	return ptpTestConfig
}
