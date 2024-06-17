package event

import (
	"fmt"
	"github.com/golang/glog"
	"sync"
)

type Subscriber interface {
	ID() string
	// Monitor method to start ay monitoring process
	Monitor()
	// Notify to call when event occurs
	Notify(source EventSource, state PTPState)

	Topic() EventSource
}

type Notifier interface {
	Register(o *Subscriber)
	Unregister(o *Subscriber)
}

type StateNotifier struct {
	sync.Mutex
	Subscribers map[string]Subscriber
}

func (n *StateNotifier) Register(s Subscriber) {
	id := fmt.Sprintf("%s_%s", s.Topic(), s.ID())
	glog.Infof("Registering  for monitoring with id %s -  topic %s", id, s.Topic())
	n.Lock()
	defer n.Unlock()
	n.Subscribers[id] = s
}

func (n *StateNotifier) Unregister(s Subscriber) {
	id := fmt.Sprintf("%s_%s", s.Topic(), s.ID())
	n.Lock()
	defer n.Unlock()
	if _, ok := n.Subscribers[id]; ok {
		delete(n.Subscribers, id)
	}
}

func (n *StateNotifier) monitor() {
	if len(n.Subscribers) == 0 {
		return
	}
	n.Lock()
	defer n.Unlock()
	for key, o := range n.Subscribers {
		if o.Topic() == MONITORING {
			o.Monitor()
			// monitoring is once time registering
			delete(n.Subscribers, key)
		}
	}
}

func (n *StateNotifier) notify(source EventSource, state PTPState) {
	n.Lock()
	defer n.Unlock()
	for _, o := range n.Subscribers {
		// for source dpll, topic is gnss
		if o.Topic() == source && o.Topic() != MONITORING {
			glog.Infof("notifying source %v with state %v", source, state)
			o.Notify(source, state)
		}
	}
}

func NewStateNotifier() *StateNotifier {
	return &StateNotifier{
		Subscribers: make(map[string]Subscriber),
	}

}
