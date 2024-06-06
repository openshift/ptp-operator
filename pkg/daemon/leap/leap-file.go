package leap

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/daemon/ublox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	defaultLeapFileName    = "leap-seconds.list"
	defaultLeapFilePath    = "/usr/share/zoneinfo"
	gpsToTaiDiff           = 19
	curreLsValidMask       = 0x1
	timeToLsEventValidMask = 0x2
	leapSourceGps          = 2
	leapConfigMapName      = "leap-configmap"
	MaintenancePeriod      = time.Minute * 1
)

type LeapManager struct {
	// Ublox GNSS leap time indications channel
	UbloxLsInd chan ublox.TimeLs
	// Close channel
	Close chan bool
	// ts2phc path of leap-seconds.list file
	LeapFilePath string
	// client
	client    *kubernetes.Clientset
	namespace string
	// Leap file structure
	leapFile LeapFile
	// Retry configmap update if failed
	retryUpdate bool
}

type LeapEvent struct {
	LeapTime string `json:"leapTime"`
	LeapSec  int    `json:"leapSec"`
	Comment  string `json:"comment"`
}
type LeapFile struct {
	ExpirationTime string      `json:"expirationTime"`
	UpdateTime     string      `json:"updateTime"`
	LeapEvents     []LeapEvent `json:"leapEvents"`
	Hash           string      `json:"hash"`
}

func New(kubeclient *kubernetes.Clientset, namespace string) (*LeapManager, error) {
	lm := &LeapManager{
		UbloxLsInd: make(chan ublox.TimeLs, 2),
		Close:      make(chan bool),
		client:     kubeclient,
		namespace:  namespace,
		leapFile:   LeapFile{},
	}
	err := lm.PopulateLeapData()
	if err != nil {
		return nil, err
	}
	return lm, nil
}

func ParseLeapFile(b []byte) (*LeapFile, error) {
	var l = LeapFile{}
	lines := strings.Split(string(b), "\n")
	for i := 0; i < len(lines); i++ {
		fields := strings.Fields(lines[i])
		if strings.HasPrefix(lines[i], "#$") {
			l.UpdateTime = fields[1]
		} else if strings.HasPrefix(lines[i], "#@") {
			l.ExpirationTime = fields[1]
		} else if strings.HasPrefix(lines[i], "#h") {
			l.Hash = strings.Join(fields[1:], " ")
		} else if !strings.HasPrefix(lines[i], "#") {
			if len(fields) < 2 {
				// empty line
				continue
			}
			sec, err := strconv.ParseInt(fields[1], 10, 8)
			if err != nil {
				return nil, fmt.Errorf("failed to parse Leap seconds %s value from file: %s, %v", fields[1], defaultLeapFileName, err)
			}
			ev := LeapEvent{
				LeapTime: fields[0],
				LeapSec:  int(sec),
				Comment:  strings.Join(fields[2:], " "),
			}
			l.LeapEvents = append(l.LeapEvents, ev)
		}
	}
	return &l, nil
}

func (l *LeapManager) RenderLeapData() (*bytes.Buffer, error) {
	templateStr := `# Do not edit
# This file is generated automatically by linuxptp-daemon
#$	{{ .UpdateTime }}
#@	{{ .ExpirationTime }}
{{ range .LeapEvents }}{{ .LeapTime }}     {{ .LeapSec }}    {{ .Comment }}
{{ end }}
#h	{{ .Hash }}`

	templ, err := template.New("leap").Parse(templateStr)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	bufWriter := bufio.NewWriter(&buf)

	err = templ.Execute(bufWriter, l.leapFile)
	if err != nil {
		return nil, err
	}
	bufWriter.Flush()
	return &buf, nil
}

func (l *LeapManager) PopulateLeapData() error {
	cm, err := l.client.CoreV1().ConfigMaps(l.namespace).Get(context.TODO(), leapConfigMapName, metav1.GetOptions{})
	nodeName := os.Getenv("NODE_NAME")
	if err != nil {
		return err
	}
	lf, found := cm.Data[nodeName]
	if !found {
		glog.Info("Populate leap data from file")
		b, err := os.ReadFile(filepath.Join(defaultLeapFilePath, defaultLeapFileName))
		if err != nil {
			return err
		}
		leapData, err := ParseLeapFile(b)
		if err != nil {
			return err
		}
		l.leapFile = *leapData
		// Set expiration time to 2036
		exp := time.Date(2036, time.January, 1, 0, 0, 0, 0, time.UTC)
		start := time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)
		expSec := int(exp.Sub(start).Seconds())
		l.leapFile.ExpirationTime = fmt.Sprint(expSec)
		data, err := l.RenderLeapData()
		if err != nil {
			return err
		}
		if len(cm.Data) == 0 {
			cm.Data = map[string]string{}
		}
		cm.Data[nodeName] = data.String()
		_, err = l.client.CoreV1().ConfigMaps(l.namespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	} else {
		glog.Info("Populate leap data from configmap")
		leapData, err := ParseLeapFile([]byte(lf))
		if err != nil {
			return err
		}
		l.leapFile = *leapData
	}
	return nil
}

func (l *LeapManager) SetLeapFile(leapFile string) {
	l.LeapFilePath = leapFile
	glog.Info("setting Leap file to ", leapFile)
}

func (l *LeapManager) Run() {
	glog.Info("starting Leap file manager")
	ticker := time.NewTicker(MaintenancePeriod)
	for {
		select {
		case v := <-l.UbloxLsInd:
			l.HandleLeapIndication(&v)
		case <-l.Close:
			return
		case <-ticker.C:
			if l.retryUpdate {
				l.UpdateLeapConfigmap()
			}
			// TODO: if current time is within -12h ... +60s from leap event:
			// Send PMC command
		}
	}
}

// AddLeapEvent appends a new leap event to the list of leap events
func (l *LeapManager) AddLeapEvent(leapTime time.Time,
	leapSec int, expirationTime time.Time, currentTime time.Time) {

	startTime := time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)
	leapTimeTai := int(leapTime.Sub(startTime).Seconds())

	ev := LeapEvent{
		LeapTime: fmt.Sprint(leapTimeTai),
		LeapSec:  leapSec,
		Comment: fmt.Sprintf("# %v %v %v",
			leapTime.Day(), leapTime.Month().String()[:3], leapTime.Year()),
	}
	l.leapFile.LeapEvents = append(l.leapFile.LeapEvents, ev)
	l.leapFile.UpdateTime = fmt.Sprint(int(currentTime.Sub(startTime).Seconds()))
	l.RehashLeapData()

}

func (l *LeapManager) RehashLeapData() {
	data := fmt.Sprint(l.leapFile.UpdateTime) + fmt.Sprint(l.leapFile.ExpirationTime)

	for _, ev := range l.leapFile.LeapEvents {
		data += fmt.Sprint(ev.LeapTime) + fmt.Sprint(ev.LeapSec)
	}
	// checksum
	hash := fmt.Sprintf("%x", sha1.Sum([]byte(data)))
	var groupedHash string
	// group checksum by 8 characters
	for i := 0; i < 5; i++ {
		if groupedHash != "" {
			groupedHash += " "
		}
		groupedHash += hash[i*8 : (i+1)*8]
	}
	l.leapFile.Hash = groupedHash
}

func (l *LeapManager) UpdateLeapConfigmap() {
	data, err := l.RenderLeapData()
	if err != nil {
		glog.Error("Leap: ", err)
		return
	}
	cm, err := l.client.CoreV1().ConfigMaps(l.namespace).Get(context.TODO(), leapConfigMapName, metav1.GetOptions{})

	if err != nil {
		l.retryUpdate = true
		glog.Info("failed to get leap configmap (will retry): ", err)
		return
	}
	nodeName := os.Getenv("NODE_NAME")
	if len(cm.Data) == 0 {
		cm.Data = map[string]string{}
	}
	cm.Data[nodeName] = data.String()
	_, err = l.client.CoreV1().ConfigMaps(l.namespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
	if err != nil {
		l.retryUpdate = true
		glog.Info("failed to update leap configmap (will retry): ", err)
		return
	}
	l.retryUpdate = false
}

// HandleLeapIndication handles NAV-TIMELS indication
// and updates the leapseconds.list file
// If leap event is closer than 12 hours in the future,
// GRANDMASTER_SETTINGS_NP dataset will be updated with
// the up to date leap second information
func (l *LeapManager) HandleLeapIndication(data *ublox.TimeLs) {
	glog.Infof("Leap indication: %+v", data)
	if data.SrcOfCurrLs != leapSourceGps {
		glog.Info("Discarding Leap event not originating from GPS")
		return
	}
	// Current leap seconds on file
	leapSecOnFile := l.leapFile.LeapEvents[len(l.leapFile.LeapEvents)-1].LeapSec

	validCurrLs := (data.Valid & curreLsValidMask) > 0
	validTimeToLsEvent := (data.Valid & timeToLsEventValidMask) > 0
	expirationTime := time.Date(2036, time.January, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Now().UTC()

	if validTimeToLsEvent && validCurrLs {
		leapSec := int(data.CurrLs) + gpsToTaiDiff + int(data.LsChange)
		if leapSec != leapSecOnFile {
			// File update is needed
			glog.Infof("Leap Seconds on file outdated: %d on file, %d + %d + %d in GNSS data",
				leapSecOnFile, int(data.CurrLs), gpsToTaiDiff, int(data.LsChange))
			startTime := time.Date(1980, time.January, 6, 0, 0, 0, 0, time.UTC)
			deltaHours, err := time.ParseDuration(fmt.Sprintf("%dh",
				data.DateOfLsGpsWn*7*24+uint(data.DateOfLsGpsDn)*24))
			if err != nil {
				glog.Error("Leap: ", err)
				return
			}
			leapTime := startTime.Add(deltaHours)
			// Add a new leap event
			l.AddLeapEvent(leapTime, leapSec, expirationTime, currentTime)
			l.UpdateLeapConfigmap()
		}
	}
}
