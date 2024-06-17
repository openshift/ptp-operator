package event_test

import (
	"github.com/golang/glog"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/event"
	"github.com/stretchr/testify/assert"
	"testing"
)

// Subscriber ... event subscriber
type Subscriber struct {
	source     event.EventSource
	obj        *TestObject
	monitoring bool
	id         string
}

func (s Subscriber) Notify(source event.EventSource, state event.PTPState) {
	glog.Infof("%s notified", s.Topic())
}

func (s Subscriber) Topic() event.EventSource {
	return s.source
}

func (s Subscriber) Monitor() {
	glog.Infof("%s monitoring started", s.ID())
}

func (s Subscriber) ID() string {
	return s.id
}

type TestObject struct {
	name string
}

func TestNewStateNotifier(t *testing.T) {
	// register monitoring process to be called by event
	testObject := &TestObject{
		name: "test",
	}
	m := &Subscriber{
		source:     event.MONITORING,
		obj:        testObject,
		monitoring: false,
		id:         "test",
	}
	event.StateRegisterer = event.NewStateNotifier()
	event.StateRegisterer.Register(m)
	assert.Equal(t, 1, len(event.StateRegisterer.Subscribers))
	event.StateRegisterer.Unregister(m)
	assert.Equal(t, 0, len(event.StateRegisterer.Subscribers))
}
