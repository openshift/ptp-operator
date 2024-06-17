package event_test

import (
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/event"
	"github.com/stretchr/testify/assert"
	"testing"
)

type testDataSet struct {
	data        map[string][]*event.Data
	wantedState event.PTPState
	desc        string
}

func Test_updateStats(t *testing.T) {
	tests := []testDataSet{{
		data: map[string][]*event.Data{
			"a.0.config": {
				{
					ProcessName: "ts2phc",
					Details: []*event.DataDetails{
						{
							IFace:     "en01",
							State:     event.PTP_FREERUN,
							ClockType: "GM",
							Metrics:   nil,
						},
						{
							IFace:     "en201",
							State:     event.PTP_LOCKED,
							ClockType: "GM",
							Metrics:   nil,
						},
					},
					State: event.PTP_UNKNOWN,
				}}},
		wantedState: event.PTP_FREERUN,
		desc:        "0. GM is FREERUN and PPS is LOCKED ",
	}, {
		data: map[string][]*event.Data{
			"a.0.config": {
				{
					Details: []*event.DataDetails{
						{
							IFace:     "en01",
							State:     event.PTP_LOCKED,
							ClockType: "GM",
							Metrics:   nil,
						},
						{
							IFace:     "en201",
							State:     event.PTP_FREERUN,
							ClockType: "GM",
							Metrics:   nil,
						},
					},
					State: event.PTP_UNKNOWN,
				}}},
		wantedState: event.PTP_FREERUN,
		desc:        "1. GNSS LOCKED PPS is FREERUN",
	}, {
		data: map[string][]*event.Data{
			"a.0.config": {
				{
					ProcessName: "ts2phc",
					Details: []*event.DataDetails{
						{
							IFace:     "en01",
							State:     event.PTP_HOLDOVER,
							ClockType: "GM",
							Metrics:   nil,
						},
						{
							IFace:     "en201",
							State:     event.PTP_LOCKED,
							ClockType: "GM",
							Metrics:   nil,
						},
					},
					State: event.PTP_UNKNOWN,
				}}},
		wantedState: event.PTP_HOLDOVER,
		desc:        "2. GNSS is in HOLDOVER PPS is in LOCKED",
	}, {
		data: map[string][]*event.Data{
			"a.0.config": {
				{
					ProcessName: "ts2phc",
					Details: []*event.DataDetails{
						{
							IFace:   "en01",
							State:   event.PTP_HOLDOVER,
							Metrics: nil,
						},
						{
							IFace:     "en201",
							State:     event.PTP_FREERUN,
							ClockType: "GM",
							Metrics:   nil,
						},
					},
					State: event.PTP_UNKNOWN,
				}}},
		wantedState: event.PTP_HOLDOVER,
		desc:        "3. GNSS is in HOLDOVER, PPS is in FREERUN",
	}, {
		data: map[string][]*event.Data{
			"a.0.config": {
				{
					ProcessName: "ts2phc",
					Details: []*event.DataDetails{
						{
							IFace:   "en01",
							State:   event.PTP_LOCKED,
							Metrics: nil,
						},
						{
							IFace:     "en201",
							State:     event.PTP_LOCKED,
							ClockType: "GM",
							Metrics:   nil,
						},
					},
					State: event.PTP_UNKNOWN,
				}}},
		wantedState: event.PTP_LOCKED,
		desc:        "4. Both are in locked state",
	}}

	for _, test := range tests {
		for _, d := range test.data {
			for _, dd := range d {
				dd.UpdateState()
				assert.Equal(t, test.wantedState, dd.State, test.desc)
			}

		}

	}

}
