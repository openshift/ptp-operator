package event_test

import (
	"bufio"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"github.com/golang/glog"
	"github.com/openshift/linuxptp-daemon/pkg/event"
)

var (
	staleSocketTimeout = 100 * time.Millisecond
)

func TestEventHandler_ProcessEvents(t *testing.T) {
	eChannel := make(chan event.EventChannel, 10)
	closeChn := make(chan bool)
	go listenToEvents(closeChn)
	time.Sleep(2 * time.Second)
	eventMananger := event.Init(true, "/tmp/go.sock", eChannel, closeChn)
	eventMananger.MockEnable()
	go eventMananger.ProcessEvents()
	time.Sleep(2 * time.Second)
	closeChn <- true
}

func listenToEvents(closeChn chan bool) {
	l, sErr := Listen("/tmp/go.sock")
	if sErr != nil {
		glog.Infof("error setting up socket %s", sErr)
		return
	}
	glog.Infof("connection established successfully")

	for {
		select {
		case <-closeChn:
			return
		default:
			fd, err := l.Accept()
			if err != nil {
				glog.Infof("accept error: %s", err)
			} else {
				ProcessTestEvents(fd)
			}
		}
	}
}

// Listen ... listen to ptp daemon logs
func Listen(addr string) (l net.Listener, e error) {
	uAddr, err := net.ResolveUnixAddr("unix", addr)
	if err != nil {
		return nil, err
	}

	// Try to listen on the socket. If that fails we check to see if it's a stale
	// socket and remove it if it is. Then we try to listen one more time.
	l, err = net.ListenUnix("unix", uAddr)
	if err != nil {
		if err = removeIfStaleUnixSocket(addr); err != nil {
			return nil, err
		}
		if l, err = net.ListenUnix("unix", uAddr); err != nil {
			return nil, err
		}
	}
	return l, err
}

// removeIfStaleUnixSocket takes in a path and removes it iff it is a socket
// that is refusing connections
func removeIfStaleUnixSocket(socketPath string) error {
	// Ensure it's a socket; if not return without an error
	if st, err := os.Stat(socketPath); err != nil || st.Mode()&os.ModeType != os.ModeSocket {
		return nil
	}
	// Try to connect
	conn, err := net.DialTimeout("unix", socketPath, staleSocketTimeout)
	if err != nil { // =syscall.ECONNREFUSED {
		return os.Remove(socketPath)
	}
	return conn.Close()
}

func ProcessTestEvents(c net.Conn) {
	// echo received messages
	remoteAddr := c.RemoteAddr().String()
	log.Println("Client connected from", remoteAddr)
	scanner := bufio.NewScanner(c)
	for {
		ok := scanner.Scan()
		if !ok {
			break
		}
		msg := scanner.Text()
		glog.Infof("events received %s", msg)
	}
}
