# PTP Operator
## Table of Contents

- [PTP Operator](#ptp-operator)
- [PtpOperatorConfig](#ptpoperatorconfig)
- [PtpConfig](#ptpconfig)
- [Quick Start](#quick-start)

## PTP Operator
Ptp Operator, runs in `openshift-ptp` namespace, manages cluster wide PTP configuration. It offers `PtpOperatorConfig` and `PtpConfig` CRDs and creates `linuxptp daemon` to apply node specific PTP config.


## PtpOperatorConfig
Upon deployment of PTP Operator, it automatically creates a `default` custom resource of `PtpOperatorConfig` kind which contains a configurable option `daemonNodeSelector`, it is used to specify which nodes `linuxptp daemon` shall be created on. The `daemonNodeSelector` will be applied to `linuxptp daemon` DaemonSet `nodeSelector` field and trigger relaunching of `linuxptp daemon`. Ptp Operator only recognizes `default` `PtpOperatorConfig`, use `oc edit PtpOperatorConfig default -n openshift-ptp` to update the `daemonNodeSelector`.


```
$ oc get ptpoperatorconfigs.ptp.openshift.io default -n openshift-ptp -o yaml

apiVersion: v1
items:
- apiVersion: ptp.openshift.io/v1
  kind: PtpOperatorConfig
  metadata:
    creationTimestamp: "2019-10-28T06:53:09Z"
    generation: 3
    name: default
    namespace: openshift-ptp
    resourceVersion: "2356742"
    selfLink: /apis/ptp.openshift.io/v1/namespaces/openshift-ptp/ptpoperatorconfigs/default
    uid: d7286542-34bd-4c79-8533-d01e2b25953e
  spec:
    daemonNodeSelector: {}
kind: List
metadata:
  resourceVersion: ""
  selfLink: ""
```

Below is an example of updating `daemonNodeSelector` to select all worker nodes:

```
$ oc edit ptpoperatorconfigs.ptp.openshift.io default -n openshift-ptp

apiVersion: v1
items:
- apiVersion: ptp.openshift.io/v1
  kind: PtpOperatorConfig
  metadata:
    creationTimestamp: "2019-10-28T06:53:09Z"
    generation: 4
    name: default
    namespace: openshift-ptp
    resourceVersion: "2364095"
    selfLink: /apis/ptp.openshift.io/v1/namespaces/openshift-ptp/ptpoperatorconfigs/default
    uid: d7286542-34bd-4c79-8533-d01e2b25953e
  spec:
    daemonNodeSelector:
      node-role.kubernetes.io/worker: ""
kind: List
metadata:
  resourceVersion: ""
  selfLink: ""
```


## PtpConfig

`PtpConfig` CRD is used to define linuxptp configurations and to which node these linuxptp configurations shall be applied. The Spec of CR has two major sections. The first section `profile` contains `interface`, `ptp4lOpts` and `phc2sysOpts` options, the second `recommend` defines profile selection logic.

```
apiVersion: ptp.openshift.io/v1
kind: PtpConfig
metadata:
  name: example-ptpconfig
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
    - nodeLabel: "node-role.kubernetes.io/worker="
```

In above example, `profile1` will be applied by `linuxptp-daemon` to nodes labeled with `node-role.kubernetes.io/worker`.

`example-ptpconfig` CR is created with `PtpConfig` kind. `spec.profile` defines profile named `profile1` which contains `interface (enp134s0f0)` to run ptp4l process on, `ptp4lOpts (-s -2)` sysconfig options to run ptp4l process with and `phc2sysOpts (-a -r)` to run phc2sys process with. `spec.recommend` defines `priority` (lower numbers mean higher priority, 0 is the highest priority) and `match` rules of profile `profile1`. `priority` is useful when there are multiple `PtpConfig` CRs defined, linuxptp daemon applies `match` rules against node labels and names from high priority to low priority in order. If any of `nodeLabel` or `nodeName` on a specific node matches with the node label or name where daemon runs, it applies profile on that node.

## Quck Start

To install PTP Operator:
```
$ make deploy-setup
```

To un-install:
```
$ make undeploy
```

