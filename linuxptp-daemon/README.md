# Linuxptp Daemon for Kubernetes
## Table of Contents

- [linuxptp Daemon](#linuxptp-daemon)
- [NodePTPDev](#nodeptpdev)
- [NodePTPCfg](#nodeptpcfg)
- [Quick Start](#quick-start)

## Linuxptp Daemon
Linuxptp Daemon runs as Kubernetes DaemonSet and manages linuxptp processes (ptp4l, phc2sys, timemaster). It offers `NodePTPCfg` CRD that can specify linuxptp config options and updates PTP capable devices via `NodePTPDev` CRD.


## NodePTPDev
Upon creation of Linuxptp Daemon, it automatically creates a custom resource of `NodePTPDev` kind per node and updates all PTP capable devices on the node to its status field. These device information is useful when creating `NodePTPCfg` custom resource which requires `Interface` name as a parameter for running ptp4l. The name of `NodePTPDev` custom resource is the same as node name.

`kubectl get nodeptpdevs.ptp.openshift.io -n ptp -o yaml`

```
apiVersion: v1
items:
- apiVersion: ptp.openshift.io/v1
  kind: NodePTPDev
  metadata:
    creationTimestamp: "2019-08-23T13:22:08Z"
    generation: 1
    name: worker-1
    namespace: ptp
    resourceVersion: "1516531"
    selfLink: /apis/ptp.openshift.io/v1/namespaces/ptp/nodeptpdevs/worker-1
    uid: 3cd38c94-d823-4849-ba16-9dcf929792b7
  spec: {}
  status:
    ptpDevices:
    - name: eno2
    - name: enp134s0f0
    - name: enp134s0f1
    - name: enp65s0f0
```
In above exmaple, `worker-1` is custom resource name and `status.ptpDevices` field lists all PTP capable devices on node `worker-1`.

## NodePTPCfg

`NodePTPCfg` CRD is used to configure linuxptp processes such as ptp4l, phc2sys and timemaster. The Spec of CR has two major sections. The first section `profile` contains ptp4l and phc2sys sysconfig options, the second `recommend` defines profile selection logic.

```
apiVersion: ptp.openshift.io/v1
kind: NodePTPCfg
metadata:
  name: example-nodeptpcfg
  namespace: ptp
spec:
  profile:
  - name: "profile1"
    interface: "enp134s0f0"
    ptp4lOpts: "-s -2"
    phc2sysOpts: "-a -r"
  recommend:
  - profile: "profile1"
    priority: 4
    match:
    - nodeLabel: "node-role.kubernetes.io/master="
      nodeName: "worker-1"
```

In above example, `example-nodeptpcfg` CR is created with `NodePTPCfg` kind. `spec.profile` defines profile named `profile1` which contains `interface (enp134s0f0)` to run ptp4l process on, `ptp4lOpts (-s -2)` sysconfig options to run ptp4l process with and `phc2sysOpts (-a -r)` to run phc2sys process with. `spec.recommend` defines `priority` (lower numbers mean higher priority, 0 is the highest priority) and `match` rules of profile `profile1`. `priority` is useful when there are multiple `NodePTPCfg` CRs defined, linuxptp daemon applies `match` rules against node labels and names from high priority to low priority in order. If any of `nodeLabel` or `nodeName` matches with the node label or name where daemon runs, it applies profile on that node.

Multiple `NodePTPCfg` CRs can co-exist `ptp` namespace. The existence of new CRs is detected by all daemon running on each node. All existing CRs are merged and appropriate objects are generated to update linuxptp processes on daemon node.

## Quck Start

Apply manifests at `deploy/` directory

```
$ kubectl create -f deploy/00-ns.yaml
$ kubectl create -f deploy/01-crd.yaml
$ kubectl create -f deploy/02-rbac.yaml
$ kubectl create -f deploy/ptp-daemon.yaml
$ kubectl create -f deploy/nodeptpcfg_cr.yaml

```
