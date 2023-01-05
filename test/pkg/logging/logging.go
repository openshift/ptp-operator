package logging

import (
	"os"

	"github.com/sirupsen/logrus"
)

func InitLogLevel() {
	logLevelString, isSet := os.LookupEnv("PTP_LOG_LEVEL")
	var logLevel, err = logrus.ParseLevel(logLevelString)
	if err != nil {
		logrus.Error("PTP_LOG_LEVEL environment set with an invalid value, defaulting to INFO \n Valid values are:  trace, debug, info, warn, error, fatal, panic")
		logLevel = logrus.InfoLevel
	}
	if !isSet {
		logrus.Infof("PTP_LOG_LEVEL environment not set, defaulting to INFO \n Valid values are:  trace, debug, info, warn, error, fatal, panic")
		logLevel = logrus.InfoLevel
	}

	logrus.Info("Log level set to: ", logLevel)
	logrus.SetLevel(logLevel)
}
