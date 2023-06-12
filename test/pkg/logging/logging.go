package logging

import (
	"fmt"
	"os"
	"path"
	"runtime"
	"strconv"
	"time"

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
	SetLogFormat()
}

// SetLogFormat sets the log format for logrus
func SetLogFormat() {
	customFormatter := new(logrus.TextFormatter)
	customFormatter.TimestampFormat = time.StampMilli
	customFormatter.PadLevelText = true
	customFormatter.FullTimestamp = true
	customFormatter.ForceColors = true
	logrus.SetReportCaller(true)
	customFormatter.CallerPrettyfier = func(f *runtime.Frame) (string, string) {
		_, filename := path.Split(f.File)
		return strconv.Itoa(f.Line) + "]", fmt.Sprintf("[%s:", filename)
	}
	logrus.SetFormatter(customFormatter)
}
