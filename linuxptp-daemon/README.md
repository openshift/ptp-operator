# Linuxptp Daemon for Kubernetes
## Table of Contents

- [linuxptp Daemon](#linuxptp-daemon)
- [Quick Start](#quick-start)

## Linuxptp Daemon
Linuxptp Daemon runs as Kubernetes DaemonSet and manages linuxptp processes (ptp4l, phc2sys, timemaster).
It mounts ptp configmap which contains aggregated ptp configurations and applies specific config for each node.
Both linuxptp daemon and ptp configmap are created in `ptp` namespace.

## Quick Start

### Create namespace

1. All ptp related resources run inside `ptp` namespace, including `linuxptp-daemon`, `ptp-configmap`.
```
$ kubectl create -f deploy/00-ns.yaml
```

### Generate ptp configmap data sources

1. ptp configmap contains data sources(linuxptp configuration) for all the nodes in cluster, each node has a data source entry in ptp configmap. The key name of data source is equal to node name, the value of data source is a string representing linuxptp configurations(ptp4lOpts, phc2sysOpts, Interface etc). Below shell script helps to generate data sources for all the nodes under `ptp-configmap` directory which will be used later to create configmap.
```
$ ./hack/gen-configmap-data-source.sh

```

For example, if the cluster only has one node called `node.example.com`, then generated file would be:
```
$ ls ./ptp-configmap
node.example.com
```

### Modify ptp configmap

1. The generated data sources is under `./ptp-configmap` directory, one file per node. It has hard-coded linuxptp configuration in each node file. Usually user will need to change the config according to their own environments, for example, change the `Interface` name `eth0` to the PTP capable device `ens786f1` in node specific file.

```
$ cat ptp-configmap/node.example.com
{"interface":"eth0", "ptp4lOpts":"-s -2", "phc2sysOpts":"-a -r"}

$ vim ptp-configmap/node.example.com
{"interface":"ens786f1", "ptp4lOpts":"-s -2", "phc2sysOpts":"-a -r"}
```

### Create ptp configmap

1. The name of ptp configmap is called `ptp-configmap`, this equals to the name of configmap mounted in linuxptp-daemon.yaml. `./ptp-configmap` is the directory where node specific data source is geneated.
```
$ kubectl create configmap ptp-configmap --from-file ./ptp-configmap
$ kubectl get configmap ptp-configmap -o yaml -n ptp
apiVersion: v1
data:
  node.example.com: |
    {"interface":"ens786f1", "ptp4lOpts":"-s -2", "phc2sysOpts":"-a -r"}
kind: ConfigMap
metadata:
  creationTimestamp: "2019-10-10T09:03:39Z"
  name: ptp-configmap
  namespace: ptp
  resourceVersion: "2323998"
  selfLink: /api/v1/namespaces/ptp/configmaps/ptp-configmap
  uid: 40c031f4-e09d-40a8-b081-92c3b8c0accb
```

### Create linuxptp daemon

1. Build linuxptp daemon image `openshift.io/linuxptp-daemon` used by linuxptp-daemon.yaml

```
$ make image
```

2. This launches linuxptp DaemonSet pod on each node, daemon mounts ptp configmap and configures linuxptp processes(ptp4l, phc2sys) according to data sources in configmap. daemon uses node specific data source from configmap.

```
$ kubectl create -f deploy/linuxptp-daemon.yaml

$ kubectl get pods -n ptp
NAME                    READY   STATUS    RESTARTS   AGE
linuxptp-daemon-txmpn   1/1     Running   0          105m

$ kubectl logs linuxptp-daemon-txmpn -n ptp
I1010 07:28:11.655009   48998 main.go:41] resync period set to: 30 [s]
I1010 07:28:11.655384   48998 main.go:42] linuxptp profile path set to: /etc/linuxptp
I1010 07:28:11.655845   48998 main.go:49] successfully get kubeconfig
I1010 07:28:11.682927   48998 utils.go:67] grabbing NIC timestamp capability for cni0
I1010 07:28:11.684469   48998 utils.go:67] grabbing NIC timestamp capability for docker0
I1010 07:28:11.685858   48998 utils.go:67] grabbing NIC timestamp capability for eno1
I1010 07:28:11.687590   48998 utils.go:67] grabbing NIC timestamp capability for eno2
I1010 07:28:11.689626   48998 utils.go:67] grabbing NIC timestamp capability for ens785f0
I1010 07:28:11.691474   48998 utils.go:67] grabbing NIC timestamp capability for ens785f0v1
I1010 07:28:11.693260   48998 utils.go:67] grabbing NIC timestamp capability for ens785f1
I1010 07:28:11.694836   48998 utils.go:67] grabbing NIC timestamp capability for ens785f1v0
I1010 07:28:11.696335   48998 utils.go:67] grabbing NIC timestamp capability for ens785f1v1
I1010 07:28:11.697712   48998 utils.go:67] grabbing NIC timestamp capability for ens786f0
I1010 07:28:11.699205   48998 utils.go:67] grabbing NIC timestamp capability for ens786f1
I1010 07:28:11.700644   48998 utils.go:67] grabbing NIC timestamp capability for veth0a8f8416
I1010 07:28:11.702098   48998 utils.go:67] grabbing NIC timestamp capability for veth2378b8d1
I1010 07:28:11.703581   48998 utils.go:67] grabbing NIC timestamp capability for veth79297004
I1010 07:28:11.705105   48998 utils.go:67] grabbing NIC timestamp capability for vethfed50896
I1010 07:28:11.706621   48998 main.go:61] PTP capable NICs: [eno1 eno2 ens785f0 ens785f1 ens786f0 ens786f1]
I1010 07:28:11.706945   48998 daemon.go:107] in applyNodePTPProfile
I1010 07:28:11.706970   48998 daemon.go:109] updating NodePTPProfile to:
I1010 07:28:11.706979   48998 daemon.go:110] ------------------------------------
I1010 07:28:11.706991   48998 daemon.go:102] Profile Name: profile1
I1010 07:28:11.707001   48998 daemon.go:102] Interface: ens786f1
I1010 07:28:11.707008   48998 daemon.go:102] Ptp4lOpts: -s -2
I1010 07:28:11.707016   48998 daemon.go:102] Phc2sysOpts: -a -r
I1010 07:28:11.707025   48998 daemon.go:116] ------------------------------------
I1010 07:28:12.707388   48998 daemon.go:186] Starting phc2sys...
I1010 07:28:12.707487   48998 daemon.go:187] phc2sys cmd: &{Path:/usr/sbin/phc2sys Args:[/usr/sbin/phc2sys -a -r] Env:[] Dir: Stdin:<nil> Stdout:<nil> Stderr:<nil> ExtraFiles:[] SysProcAttr:<nil> Process:<nil> ProcessState:<nil> ctx:<nil> lookPathErr:<nil> finished:false childFiles:[] closeAfterStart:[] closeAfterWait:[] goroutine:[] errch:<nil> waitDone:<nil>}
I1010 07:28:13.707667   48998 daemon.go:186] Starting ptp4l...
I1010 07:28:13.707709   48998 daemon.go:187] ptp4l cmd: &{Path:/usr/sbin/ptp4l Args:[/usr/sbin/ptp4l -m -f /etc/ptp4l.conf -i ens786f1 -s -2] Env:[] Dir: Stdin:<nil> Stdout:<nil> Stderr:<nil> ExtraFiles:[] SysProcAttr:<nil> Process:<nil> ProcessState:<nil> ctx:<nil> lookPathErr:<nil> finished:false childFiles:[] closeAfterStart:[] closeAfterWait:[] goroutine:[] errch:<nil> waitDone:<nil>}
ptp4l[1903447.096]: selected /dev/ptp3 as PTP clock
ptp4l[1903447.120]: driver rejected most general HWTSTAMP filter
ptp4l[1903447.120]: port 1: INITIALIZING to LISTENING on INIT_COMPLETE
ptp4l[1903447.120]: port 0: INITIALIZING to LISTENING on INIT_COMPLETE
ptp4l[1903447.120]: port 1: link down
ptp4l[1903447.121]: port 1: LISTENING to FAULTY on FAULT_DETECTED (FT_UNSPECIFIED)
ptp4l[1903447.145]: selected local clock 3cfdfe.fffe.b57f99 as best master
ptp4l[1903447.145]: assuming the grand master role
```
