# Forwarder

> A forwarder for forwarding kubernetes cluster service and pod programmatically.


## Installation

```bash
go get github.com/anthhub/forwarder
```


## Usage

### forwarding by kubeconfig path

```go

	import (
		"github.com/anthhub/forwarder"
	)

	options := []*forwarder.Option{
		{
			// the local port for forwarding
			LocalPort: 	8080,
			// the k8s pod port
			RemotePort: 80,
			// the forwarding service name
			ServiceName: "my-nginx-svc",
			// the k8s source string, eg: svc/my-nginx-svc po/my-nginx-666
			// the Source field will be parsed and override ServiceName or RemotePort field
			Source: "svc/my-nginx-66b6c48dd5-ttdb2",
			// namespace default is "default"
			Namespace: "default"
		},
		{	
			// if local port isn't provided, forwarder will generate a random port number
			// LocalPort: 8081,
			// 
			// if target port isn't provided, forwarder find the first container port of the pod or service
			// RemotePort: 80,
			Source: "po/my-nginx-66b6c48dd5-ttdb2",
		},
	}

	// it's to create a forwarder, and you need provide a path of kubeconfig
	// the path of kubeconfig, default is "~/.kube/config"
	ret, err := forwarder.WithForwarders(context.Background(), options, "./kubecfg")
	if err != nil {
		panic(err)
	}
	// remember to close the forwarding
	defer ret.Close()
	// wait forwarding ready
	// the remote and local ports are listed
	ports, err := ret.Ready()
	if err != nil {
		panic(err)
	}
	// ...

	// if you want to block the goroutine and listen IOStreams close signal, you can do as following:
	ret.Wait()
```

### forwarding embed kubeconfig
```go
//go:embed kubeconfig
var kubeconfigBytes []byte

func main() {
	options := []*forwarder.Option{
		{
			// LocalPort: 8080,
			// RemotePort:  80,
			ServiceName: "my-nginx-svc",
		},
	}
	// use kubeconfig bytes to config forward
	ret, err := forwarder.WithForwardersEmbedConfig(context.Background(), options, kubeconfigBytes)
	if err != nil {
		panic(err)
	}
	// remember to close the forwarding
	defer ret.Close()
	// wait forwarding ready
	// the remote and local ports are listed
	ports, err := ret.Ready()
	if err != nil {
		panic(err)
	}
	fmt.Printf("ports: %+v\n", ports)
	// ...

	// if you want to block the goroutine and listen IOStreams close signal, you can do as following:
	ret.Wait()
}
```

> If you want to learn more about `forwarder`, you can read example, test cases and source code.
