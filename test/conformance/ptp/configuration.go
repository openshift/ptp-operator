package ptp

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

type yamlTimeDur time.Duration

type MasterOffsetContinuous struct {
	Enable    bool  `yaml:"enable"`
	FailFast  bool  `yaml:"failfast"`
	MinOffset int   `yaml:"minoffset"`
	MaxOffset int   `yaml:"maxoffset"`
	Duration  int64 `yaml:"duration"`
}

type Configuration struct {
	MasterOffsetContinuousConfig MasterOffsetContinuous `yaml:"masteroffsetcontinuous"`
}

var configuration Configuration
var loaded bool

func loadConfiguration(filename string, conf *Configuration) {
	yamlFile, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Printf("yamlFile.Get err   #%v ", err)
	}
	err = yaml.Unmarshal(yamlFile, &conf)
	if err != nil {
		log.Fatalf("Unmarshal: %v", err)
	}
}

func getConfiguration() Configuration {
	if loaded {
		return configuration
	}
	loaded = true
	path, err := os.Getwd()
	if err != nil {
		logrus.Error("can't retrieve path, trying to continue")
		return configuration
	}
	loadConfiguration(fmt.Sprintf("%s/%s", path, "ptp/configuration.yml"), &configuration)
	logrus.Info("config=", configuration)
	return configuration
}
