package forwarder

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sync/errgroup"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	restclient "k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

var once sync.Once

// It is to forward port whith kubeconfig bytes.
func WithForwardersEmbedConfig(ctx context.Context, options []*Option, kubeconfigBytes []byte) (*Result, error) {
	kubeconfigGetter := func() (*clientcmdapi.Config, error) {
		config, err := shimLoadConfig(kubeconfigBytes)
		if err != nil {
			return nil, err
		}

		return config, nil
	}
	config, err := clientcmd.BuildConfigFromKubeconfigGetter("", kubeconfigGetter)
	if err != nil {
		return nil, err
	}

	return forwarders(ctx, options, config)
}

// It is to forward port for k8s cloud services.
func WithForwarders(ctx context.Context, options []*Option, kubeconfigPath string) (*Result, error) {
	if kubeconfigPath == "" {
		kubeconfigPath = "~/.kube/config"
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}

	return forwarders(ctx, options, config)
}

// It is to forward port for k8s cloud services.
func forwarders(ctx context.Context, options []*Option, config *restclient.Config) (*Result, error) {
	newOptions, err := parseOptions(options)
	if err != nil {
		return nil, err
	}

	podOptions, err := handleOptions(ctx, newOptions, config)
	if err != nil {
		return nil, err
	}

	stream := genericclioptions.IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	carries := make([]*carry, len(podOptions))

	var g errgroup.Group

	for index, option := range podOptions {
		index := index
		stopCh := make(chan struct{}, 1)
		readyCh := make(chan struct{})

		req := &portForwardAPodRequest{
			RestConfig: config,
			Pod:        option.Pod,
			LocalPort:  option.LocalPort,
			PodPort:    option.PodPort,
			Streams:    stream,
			StopCh:     stopCh,
			ReadyCh:    readyCh,
		}
		g.Go(func() error {
			pf, err := portForwardAPod(req)
			if err != nil {
				return err
			}
			carries[index] = &carry{StopCh: stopCh, ReadyCh: readyCh, PF: pf}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	ret := &Result{
		Close: func() {
			once.Do(func() {
				for _, c := range carries {
					close(c.StopCh)
				}
			})
		},
		Ready: func() ([][]portforward.ForwardedPort, error) {
			pfs := [][]portforward.ForwardedPort{}
			for _, c := range carries {
				<-c.ReadyCh
				ports, err := c.PF.GetPorts()
				if err != nil {
					return nil, err
				}
				pfs = append(pfs, ports)
			}
			return pfs, nil
		},
	}

	ret.Wait = func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		<-sigs
		fmt.Println("Bye...")
		ret.Close()
	}

	go func() {
		<-ctx.Done()
		ret.Close()
	}()

	return ret, nil
}

// It is to forward port, and return the forwarder.
func portForwardAPod(req *portForwardAPodRequest) (*portforward.PortForwarder, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward",
		req.Pod.Namespace, req.Pod.Name)
	hostIP := strings.TrimLeft(req.RestConfig.Host, "htps:/")

	transport, upgrader, err := spdy.RoundTripperFor(req.RestConfig)
	if err != nil {
		return nil, err
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, &url.URL{Scheme: "https", Path: path, Host: hostIP})
	fw, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", req.LocalPort, req.PodPort)}, req.StopCh, req.ReadyCh, req.Streams.Out, req.Streams.ErrOut)
	if err != nil {
		return nil, err
	}

	go func() {
		if err := fw.ForwardPorts(); err != nil {
			panic(err)
		}
	}()

	return fw, nil
}

// It is to transform kubeconfig bytes to clientcmdapi config.
func shimLoadConfig(kubeconfigBytes []byte) (*clientcmdapi.Config, error) {
	config, err := clientcmd.Load(kubeconfigBytes)
	if err != nil {
		return nil, err
	}

	// set LocationOfOrigin on every Cluster, User, and Context
	for key, obj := range config.AuthInfos {
		config.AuthInfos[key] = obj
	}
	for key, obj := range config.Clusters {
		config.Clusters[key] = obj
	}
	for key, obj := range config.Contexts {
		config.Contexts[key] = obj
	}

	if config.AuthInfos == nil {
		config.AuthInfos = map[string]*clientcmdapi.AuthInfo{}
	}
	if config.Clusters == nil {
		config.Clusters = map[string]*clientcmdapi.Cluster{}
	}
	if config.Contexts == nil {
		config.Contexts = map[string]*clientcmdapi.Context{}
	}

	return config, nil
}
