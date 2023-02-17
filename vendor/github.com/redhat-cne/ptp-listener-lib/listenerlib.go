package listenerlib

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"

	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"log"
	"net"

	"github.com/anthhub/forwarder"

	exports "github.com/redhat-cne/ptp-listener-exports"
	"github.com/redhat-cne/sdk-go/pkg/event"
	ptpEvent "github.com/redhat-cne/sdk-go/pkg/event/ptp"
	"github.com/redhat-cne/sdk-go/pkg/pubsub"
	"github.com/sirupsen/logrus"

	chanpubsub "github.com/redhat-cne/channel-pubsub"
	"github.com/redhat-cne/sdk-go/pkg/types"
	api "github.com/redhat-cne/sdk-go/v1/pubsub"
)

const (
	SubscriptionPath  = "/api/ocloudNotifications/v1/subscriptions"
	EventsPath        = "/api/ocloudNotifications/v1"
	LocalEventPath    = "/event"
	LocalHealthPath   = "/health"
	LocalAckEventPath = "/ack/event"
	HTTP200           = 200
	attempts          = 20
	interval          = 5 * time.Second
)

var (
	Ps                           *chanpubsub.Pubsub
	CurrentSubscriptionIDPerType = map[string]string{}
	localListeningEndpoint       string
)

type ListenerConfig struct {
	ptpEventServiceLocalhostPort,
	ptpEventServiceRemotePort,
	localHTTPServerPort int
	ptpPodName,
	nodeName,
	ptpNs,
	kubeconfigPath,
	kubernetesHost string
}

var config ListenerConfig

func InitPubSub() {
	Ps = chanpubsub.NewPubsub()
}

func initResources(nodeName string) (resourceList []string) {
	// adding support for OsClockSyncState
	resourceList = append(resourceList, fmt.Sprintf(resourcePrefix, nodeName, string(ptpEvent.OsClockSyncState)))
	// add more events here
	return
}

var HTTPClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConnsPerHost: 20,
	},
	Timeout: 10 * time.Second,
}

// Consumer webserver
func server(localListeningEndpoint string) {
	http.HandleFunc(LocalEventPath, processEvent)
	http.HandleFunc(LocalHealthPath, health)
	http.HandleFunc(LocalAckEventPath, ackEvent)
	server := &http.Server{
		Addr:              localListeningEndpoint,
		ReadHeaderTimeout: 3 * time.Second,
	}
	err := server.ListenAndServe()
	if err != nil {
		logrus.Errorf("ListenAndServe returns with err= %s", err)
	}
}

func health(w http.ResponseWriter, req *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// net/http.Header ["User-Agent": ["Go-http-client/1.1"],
// "Ce-Subject": ["/cluster/node/master2/sync/ptp-status/ptp-clock-class-change"],
// "Content-Type": ["application/json"],
// "Accept-Encoding": ["gzip"],
// "Content-Length": ["138"],
// "Ce-Id": ["4eff05f8-493a-4382-8d89-209dc2179041"],
// "Ce-Source": ["/cluster/node/master2/sync/ptp-status/ptp-clock-class-change"],
// "Ce-Specversion": ["0.3"], "Ce-Time": ["2022-12-16T14:26:47.167232673Z"],
// "Ce-Type": ["event.sync.ptp-status.ptp-clock-class-change"], ]

func processEvent(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		logrus.Errorf("error reading message  %v", err)
		return
	}
	e := string(bodyBytes)
	logrus.Trace(e)
	if len(bodyBytes) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	logrus.Debugf("received event %s", string(bodyBytes))

	aEvent, aType, err := createStoredEvent(bodyBytes)
	if err != nil {
		logrus.Errorf("could not create event %s", err)
		return
	}
	logrus.Info(aEvent)
	Ps.Publish(aType, aEvent)
}

func ackEvent(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		logrus.Errorf("error reading acknowledgment  %v", err)
		return
	}
	e := string(bodyBytes)
	if e != "" {
		logrus.Infof("received ack %s", string(bodyBytes))
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

const (
	resourcePrefix string = "/cluster/node/%s%s"
	sleep5s               = 5 * time.Second
)

func SubscribeAnWaitForAllEvents(kubernetesHost, nodeName, apiAddr string, localHTTPServerPort int) (err error) {
	supportedResources := initResources(nodeName)
	localListeningEndpoint = fmt.Sprintf("%s:%d", GetOutboundIP(kubernetesHost).String(), localHTTPServerPort)
	go server(fmt.Sprintf(":%d", localHTTPServerPort)) // spin local api
	time.Sleep(sleep5s)
	for _, resource := range supportedResources {
		err := SubscribeAllEvents(resource, apiAddr, localListeningEndpoint)
		if err != nil {
			return fmt.Errorf("could not register resource=%s at api addr=%s with endpoint=%s , err=%s", resource, apiAddr, localListeningEndpoint, err)
		}
	}
	logrus.Info("waiting for events")
	return nil
}

func UnsubscribeAllEvents(nodeName string) {
	if localListeningEndpoint == "" {
		logrus.Error("local endpoint not available, cannot unsubscribe events")
		return
	}
	supportedResources := initResources(nodeName)
	for _, resource := range supportedResources {
		err := deleteSubscription(resource, config.kubernetesHost)
		if err != nil {
			logrus.Errorf("could not delete resource=%s at api addr=%s with endpoint=%s , err=%s", resource, config.kubernetesHost, localListeningEndpoint, err)
		}
	}
}

func SubscribeAllEvents(supportedResource, apiAddr, localAPIAddr string) (err error) {
	subURL := &types.URI{URL: url.URL{Scheme: "http",
		Host: apiAddr,
		Path: SubscriptionPath}}
	endpointURL := &types.URI{URL: url.URL{Scheme: "http",
		Host: localAPIAddr,
		Path: "event"}}
	sub := api.NewPubSub(
		endpointURL,
		supportedResource)

	data, err := json.Marshal(&sub)
	if err != nil {
		return fmt.Errorf("error marshalling, err=%s", err)
	}
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", subURL.String(), bytes.NewBuffer(data))
	if err != nil {
		logrus.Error(err)
		return fmt.Errorf("error building http request, err=%s", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		logrus.Error(err)
		return fmt.Errorf("error sending http request, err=%s", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading event %v", err)
	}
	logrus.Infof("%s", string(bodyBytes))
	respPubSub := pubsub.PubSub{}
	err = json.Unmarshal(bodyBytes, &respPubSub)
	if err != nil {
		return fmt.Errorf("error un-marshaling event %v", err)
	}
	logrus.Infof("Subscription to %s created successfully with UUID=%s  at URL=%s", supportedResource, respPubSub.ID, subURL)
	CurrentSubscriptionIDPerType[supportedResource] = respPubSub.ID
	return nil
}

func deleteSubscription(supportedResource, apiAddr string) (err error) {
	subURL := &types.URI{URL: url.URL{Scheme: "http",
		Host: apiAddr,
		Path: SubscriptionPath + "/" + CurrentSubscriptionIDPerType[supportedResource]}}

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "DELETE", subURL.String(), http.NoBody)
	if err != nil {
		logrus.Error(err)
		return fmt.Errorf("error building http request, err=%s", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("error sending http request, err=%s", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading event %v", err)
	}
	bodyString := string(bodyBytes)

	if resp.StatusCode != HTTP200 {
		return fmt.Errorf("failed deleting subscription ID=%s for type %s body=%s", CurrentSubscriptionIDPerType[supportedResource], supportedResource, bodyString)
	}

	logrus.Infof("subscription ID=%s for type %s deleted successfully at URL=%s", CurrentSubscriptionIDPerType[supportedResource], supportedResource, subURL)

	return nil
}

// Get preferred outbound ip of this machine
func GetOutboundIP(aURL string) net.IP {
	u, err := url.Parse(aURL)
	if err != nil {
		log.Fatalf("cannot parse k8s api url, would not receive events, stopping, err = %s", err)
	}

	conn, err := net.Dial("udp", u.Host)
	if err != nil {
		log.Fatalf("error dialing host or address (%s), err = %s", u.Host, err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	logrus.Infof("Outbound IP = %s to reach server: %s", localAddr.IP.String(), u.Host)
	return localAddr.IP
}

func StartListening(ptpEventServiceLocalhostPort,
	ptpEventServiceRemotePort,
	localHTTPServerPort int,
	ptpPodName,
	nodeName,
	ptpNs,
	kubeconfigPath,
	kubernetesHost string,
) (err error) {
	// Saving configuration
	config.ptpEventServiceLocalhostPort = ptpEventServiceLocalhostPort
	config.ptpEventServiceRemotePort = ptpEventServiceRemotePort
	config.localHTTPServerPort = localHTTPServerPort
	config.ptpPodName = ptpPodName
	config.nodeName = nodeName
	config.ptpNs = ptpNs
	config.kubeconfigPath = kubeconfigPath
	config.kubernetesHost = kubernetesHost

	logrus.Infof("Forwarding to pod: %s node: %s", ptpPodName, nodeName)
	var ret *forwarder.Result
	defer closeForwarder(ret)
	err = retry(attempts, interval, func() (err error) {
		options := []*forwarder.Option{
			{
				LocalPort:  ptpEventServiceLocalhostPort,
				RemotePort: ptpEventServiceRemotePort,
				Namespace:  ptpNs,
				PodName:    ptpPodName,
			},
		}
		logrus.Infof("Forwarding to pod: %s node: %s", ptpPodName, nodeName)
		ret, err = forwarder.WithForwarders(context.Background(), options, kubeconfigPath)
		if err != nil {
			return fmt.Errorf("could not initialize port forwarding, err=%s", err)
		}

		// wait forwarding ready
		// the remote and local ports are listed
		ports, err := ret.Ready()
		logrus.Infof("Ports: %+v", ports)
		if err != nil {
			return fmt.Errorf("could not initialize port forwarding, err=%s", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	err = SubscribeAnWaitForAllEvents(kubernetesHost, nodeName, "localhost:"+strconv.Itoa(ptpEventServiceLocalhostPort), localHTTPServerPort)
	if err != nil {
		return fmt.Errorf("failed to subscribe to events, err=%s", err)
	}
	return nil
}

func closeForwarder(ret *forwarder.Result) {
	if ret != nil {
		ret.Close()
	}
}

func retry(attempts int, sleep time.Duration, f func() error) (err error) {
	for i := 0; i < attempts; i++ {
		if i > 0 {
			log.Println("retrying after error:", err)
			time.Sleep(sleep)
			sleep *= 2
		}
		err = f()
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("after %d attempts, last error: %s", attempts, err)
}

func IsEventValid(aEvent *event.Event) bool {
	if aEvent.Time == nil || aEvent.Data == nil {
		return false
	}
	return true
}

func createStoredEvent(data []byte) (aStoredEvent exports.StoredEvent, aType string, err error) {
	var e event.Event
	err = json.Unmarshal(data, &e)
	if err != nil {
		return aStoredEvent, aType, err
	}

	if !IsEventValid(&e) {
		return aStoredEvent, aType, fmt.Errorf("parsed invalid event event=%+v", e)
	}
	logrus.Debug(e)

	// Note that there is no UnixMillis, so to get the
	// milliseconds since epoch you'll need to manually
	// divide from nanoseconds.
	latency := (time.Now().UnixNano() - e.Time.UnixNano()) / 1000000
	// set log to Info level for performance measurement
	logrus.Debugf("Latency for the event: %d ms\n", latency)

	values := exports.StoredEventValues{}
	for _, v := range e.Data.Values {
		dataType := string(v.DataType)
		values[dataType] = v.Value
	}
	return exports.StoredEvent{exports.EventTimeStamp: e.Time, exports.EventType: e.Type, exports.EventSource: e.Source, exports.EventValues: values}, e.Type, nil
}

func GetEvent(nodeName, apiAddr, resource string) (aEvent exports.StoredEvent, aType string, err error) {
	path := EventsPath + resource + `/CurrentState`
	subURL := &types.URI{URL: url.URL{Scheme: "http",
		Host: apiAddr,
		Path: path}}

	logrus.Infof("GET request with url=%s", subURL.String())
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	logrus.Infof("trying to get URL=%s", subURL.String())
	req, err := http.NewRequestWithContext(ctx, "GET", subURL.String(), http.NoBody)
	if err != nil {
		logrus.Error(err)
		return aEvent, aType, fmt.Errorf("error building http request, err=%s", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return aEvent, aType, fmt.Errorf("error sending http request, err=%s", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return aEvent, aType, fmt.Errorf("error reading event %v", err)
	}

	logrus.Debugf("received event %s", string(bodyBytes))

	aEvent, aType, err = createStoredEvent(bodyBytes)
	if err != nil {
		logrus.Errorf("could not create event %s", err)
		return aEvent, aType, err
	}
	logrus.Info(aEvent)
	return aEvent, aType, err
}

// sends a fake events to indicate the initial state at the time of registering events via channel-pubsub
func PushInitialEvent(resource string) {
	const getAPIWait = 30
	time.Sleep(time.Second * getAPIWait)
	localListeningEndpoint = fmt.Sprintf("%s:%d", "localhost", config.ptpEventServiceLocalhostPort)
	initialState, aType, err := GetEvent(config.nodeName, localListeningEndpoint, fmt.Sprintf(resourcePrefix, config.nodeName, resource))
	if err != nil {
		logrus.Errorf("could not get and push event resource=%s, err=%s", resource, err)
	}
	Ps.Publish(aType, initialState)
}
