package event

import (
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"strconv"
	"strings"
)

type DDetails []*DataDetails

// Data ...
type Data struct {
	ProcessName EventSource // ts2phc  // dppl
	Details     DDetails    // array of iface and  offset
	State       PTPState    // have the worst state here
	logData     string      // iface that is connected to GNSS
}

// DataMetrics ...
type DataMetric struct {
	isRegistered  bool
	GaugeMetric   *prometheus.GaugeVec
	CounterMetric *prometheus.Counter
	Name          string
	ValueType     prometheus.ValueType
	Labels        prometheus.Labels
	Value         float64
}

// DataDetails .. details for data
type DataDetails struct {
	IFace        string
	State        PTPState
	ClockType    ClockType
	Metrics      map[ValueType]DataMetric
	time         int64
	logData      string
	signalSource EventSource // GNSS PPS
	sourceLost   bool
}

// UpdateState .. update process state
func (d *Data) UpdateState() {
	state := PTP_UNKNOWN
	for _, detail := range d.Details { // 2 ts2phc or 2 dpll etc
		switch detail.State {
		case PTP_FREERUN: // if its free ru and main state is not holdover then this is the state
			if state != PTP_HOLDOVER {
				state = detail.State
			}
		case PTP_HOLDOVER: // if one of them is in holdover then this is the state
			state = detail.State
		case PTP_LOCKED: // if this is locked and none of them are in UNKNOWN or FREE run then this is the state
			if state != PTP_FREERUN && state != PTP_HOLDOVER { // previous state
				state = detail.State
			}
		}
	}
	d.State = state
	glog.Infof("state updated for %s =%s", d.ProcessName, d.State)
}

// GetDataDetails ...
func (d *Data) GetDataDetails(iface string) *DataDetails {
	for _, d := range d.Details {
		if d.IFace == iface {
			return d
		}
	}
	return nil
}

func (d *Data) AddEvent(event EventChannel) {
	for _, dd := range d.Details {
		if dd.IFace == event.IFace {
			if dd.time <= event.Time {
				if dd.State != event.State {
					if len(StateRegisterer.Subscribers) > 0 {
						go StateRegisterer.notify(event.ProcessName, event.State)
					}
				}
				dd.State = event.State
				dd.sourceLost = event.SourceLost
				dd.ClockType = event.ClockType
				dd.time = event.Time
				dd.logData = event.GetLogData()
			} else {
				glog.Infof("discarding stale event for process %s, last event @ %d, current event @ %d", event.ProcessName, dd.time, event.Time)
			}
			return
		}
	}

	details := &DataDetails{
		ClockType:  event.ClockType,
		Metrics:    map[ValueType]DataMetric{},
		IFace:      event.IFace,
		time:       event.Time,
		logData:    event.GetLogData(),
		State:      event.State,
		sourceLost: event.SourceLost,
	}
	d.logData = details.logData
	d.Details = append(d.Details, details)
	if len(StateRegisterer.Subscribers) > 0 {
		glog.Infof("notify state changed for %s", event.ProcessName)
		go StateRegisterer.notify(event.ProcessName, event.State)
	}
}

// ToString ... data
func (d *Data) toString() string {
	out := strings.Builder{}
	out.WriteString("  logData : " + d.logData)
	out.WriteString("  state: " + string(d.State))
	out.WriteString("   process name: " + string(d.ProcessName))
	out.WriteString("  Data Details: " + d.Details.toString())
	out.WriteString("-----\r\n")
	return out.String()
}

// toString ... data details
func (dd DDetails) toString() string {
	out := strings.Builder{}
	for _, d := range dd {
		out.WriteString("  Iface name: " + d.IFace)
		out.WriteString("  state: " + string(d.State))
		out.WriteString("  clock type: " + string(d.ClockType))
		out.WriteString(" signal source: " + string(d.signalSource))
		out.WriteString(" source lost: " + strconv.FormatBool(d.sourceLost))
		out.WriteString("-----\r\n")
	}
	return out.String()
}
