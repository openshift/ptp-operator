package client

import (
	"os"

	"github.com/golang/glog"

	discovery "k8s.io/client-go/discovery"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	networkv1client "k8s.io/client-go/kubernetes/typed/networking/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client defines the client set that will be used for testing
var Client *ClientSet

func init() {
	Client = New("")
}

// ClientSet provides the struct to talk with relevant API
type ClientSet struct {
	corev1client.CoreV1Interface
	networkv1client.NetworkingV1Client
	appsv1client.AppsV1Interface
	discovery.DiscoveryInterface
	Config *rest.Config
}

// New returns a *ClientBuilder with the given kubeconfig.
func New(kubeconfig string) *ClientSet {
	var config *rest.Config
	var err error

	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	if kubeconfig != "" {
		glog.V(4).Infof("Loading kube client config from path %q", kubeconfig)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		glog.V(4).Infof("Using in-cluster kube client config")
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		panic(err)
	}

	clientSet := &ClientSet{}
	clientSet.CoreV1Interface = corev1client.NewForConfigOrDie(config)
	clientSet.AppsV1Interface = appsv1client.NewForConfigOrDie(config)
	clientSet.DiscoveryInterface = discovery.NewDiscoveryClientForConfigOrDie(config)
	clientSet.NetworkingV1Client = *networkv1client.NewForConfigOrDie(config)
	clientSet.Config = config
	return clientSet
}
