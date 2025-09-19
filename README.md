# PTP Operator
## Table of Contents

- [PTP Operator](#ptp-operator)
- [PtpOperatorConfig](#ptpoperatorconfig)
- [PtpConfig](#ptpconfig)
- [Quick Start](#quick-start)

## PTP Operator
Ptp Operator, runs in `openshift-ptp` namespace, manages cluster wide PTP configuration. It offers `PtpOperatorConfig` and `PtpConfig` CRDs and creates `linuxptp daemon` to apply node-specific PTP config.

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
### Enable PTP events via fast event framework
PTP Operator supports fast event publisher for events such as PTP state change, os clock out of sync, clock class change and port failure.
Event publisher is enabled by deploying PTP operator with [cloud events framework](https://github.com/redhat-cne/cloud-event-proxy) (based on O-RAN API specifications).
The events are published via HTTP or AMQP transport and available for local subscribers.

#### Enabling fast events
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
    ptpEventConfig:
      enableEventPublisher: true
      transportHost: "http://ptp-event-publisher-service-NODE_NAME.openshift-ptp.svc.cluster.local:9043"
      storageType: local-sc
    daemonNodeSelector:
      node-role.kubernetes.io/worker: ""
kind: List
metadata:
  resourceVersion: ""
  selfLink: ""
```
## PtpConfig

`PtpConfig` CRD is used to define linuxptp configurations and to which node these 
linuxptp configurations shall be applied. 
The Spec of CR has two major sections. 
The first section `profile` contains `interface`, `ptp4lOpts`, `phc2sysOpts` and `ptp4lConf` options,
the second `recommend` defines profile selection logic.
```
 PTP operator supports T-BC and Ordinary clock which can be configured via ptpConfig
```
### ptpConfig to set up ordinary clock using single interface
``` 
NOTE: following ptp4l/phc2sys opts required when events are enabled 
    ptp4lOpts: "-2 -s --summary_interval -4" 
    phc2sysOpts: "-a -r -m -n 24 -N 8 -R 16"
```
```
apiVersion: ptp.openshift.io/v1
kind: PtpConfig
metadata:
  name: ordinary-clock-ptpconfig
  namespace: openshift-ptp
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
    - nodeLabel: "node-role.kubernetes.io/worker"
```
### ptpConfig to set up boundary clock using multiple interface
``` 
NOTE: following ptp4l/phc2sys opts required when events are enabled 
    ptp4lOpts: "-2 --summary_interval -4" 
    phc2sysOpts: "-a -r -m -n 24 -N 8 -R 16"
```
```
apiVersion: ptp.openshift.io/v1
kind: PtpConfig
metadata:
  name: boundary-clock-ptpconfig
  namespace: openshift-ptp
spec:
  profile:
  - name: "profile1"
    ptp4lOpts: "-s -2"
    phc2sysOpts: "-a -r"
    ptp4lConf: |
      [ens7f0]
      masterOnly 0
      [ens7f1]
      masterOnly 1
      [ens7f2]
      masterOnly 1
  recommend:
  - profile: "profile1"
    priority: 4
    match:
    - nodeLabel: "node-role.kubernetes.io/worker"
```
### ptpConfig to override offset threshold when events are enabled
```
apiVersion: ptp.openshift.io/v1
kind: PtpConfig
metadata:
  name: event-support-ptpconfig
  namespace: openshift-ptp
spec:
  profile:
  - name: "profile1"
    ...
    ...
    ......   
    ptpClockThreshold:
      holdOverTimeout: 24 # in secs
      maxOffsetThreshold: 100 #in nano secs
      minOffsetThreshold: 100 #in nano secs
  recommend:
  - profile: "profile1"
    priority: 4
    match:
    - nodeLabel: "node-role.kubernetes.io/worker"
    
```
### ptpConfig to filter 'master offset' and 'delay   filtered' logs
```
apiVersion: ptp.openshift.io/v1
kind: PtpConfig
metadata:
  name: suppress-logs-ptpconfig
  namespace: openshift-ptp
spec:
  profile:
  - name: "profile1"
    ...
    ...
    ......   
    ptpSettings:
      stdoutFilter: "^.*delay   filtered.*$"
      logReduce: "true"
  recommend:
  - profile: "profile1"
    priority: 4
    match:
    - nodeLabel: "node-role.kubernetes.io/worker"
    
```
### ptpConfig to filter 'master offset' logs and report periodic summary every 5 seconds
```
apiVersion: ptp.openshift.io/v1
kind: PtpConfig
metadata:
  name: suppress-logs-ptpconfig
  namespace: openshift-ptp
spec:
  profile:
  - name: "profile1"
    ...
    ...
    ......   
    ptpSettings:
      logReduce: "enhanced 5s 100"
  recommend:
  - profile: "profile1"
    priority: 4
    match:
    - nodeLabel: "node-role.kubernetes.io/worker"
    
```
### ptpConfig to configure as WPC NIC as GM
```
apiVersion: ptp.openshift.io/v1
kind: PtpConfig
metadata:
  name: ptpconfig-gm
  namespace: openshift-ptp
spec:
  profile:
  - name: "profile1"
    ...
    ...
    ......   
    plugins:
      e810:
        enableDefaultConfig: true
    ts2phcOpts: " "
    ts2phcConf: |
      [nmea]
      ts2phc.master 1
      [global]
      use_syslog  0
      verbose 1
      logging_level 7
      ts2phc.pulsewidth 100000000
      #GNSS module s /dev/ttyGNSS* -al use _0
      ts2phc.nmea_serialport  /dev/ttyGNSS_1700_0
      # The `leapfile` directive below can be omitted.
      # leapfile  /usr/share/zoneinfo/leap-seconds.list
      [ens2f0]
      ts2phc.extts_polarity rising
      ts2phc.extts_correction 0
    synce4lOpts: " "
    synce4lConf: |
     [global]
     logging_level 7
     use_syslog 0
     verbose 1
     message_tag [synce4l]

     [<synce1>]
     dnu_prio 0xFF
     network_option 2
     extended_tlv 1
     recover_time 60
     clock_id 
     module_name ice

     [enp59s0f0np0]
     tx_heartbeat_msec 1000
     rx_heartbeat_msec 500
     allowed_qls 0x4
     allowed_ext_qls 0xFF
     [{SMA1}]
     board_label SMA1
     input_QL 0x1
     input_ext_QL 0x20
  recommend:
  - profile: "profile1"
    priority: 4
    match:
    - nodeLabel: "node-role.kubernetes.io/worker"
    
```

In above examples, `profile1` will be applied by `linuxptp-daemon` to nodes labeled with `node-role.kubernetes.io/worker`.

`xxx-ptpconfig` CR is created with `PtpConfig` kind. `spec.profile` defines profile named `profile1` which contains `interface (enp134s0f0)` to run ptp4l process on, `ptp4lOpts (-s -2)` sysconfig options to run ptp4l process with and `phc2sysOpts (-a -r)` to run phc2sys process with. `spec.recommend` defines `priority` (lower numbers mean higher priority, 0 is the highest priority) and `match` rules of profile `profile1`. `priority` is useful when there are multiple `PtpConfig` CRs defined, linuxptp daemon applies `match` rules against node labels and names from high priority to low priority in order. If any of `nodeLabel` or `nodeName` on a specific node matches with the node label or name where daemon runs, it applies profile on that node.

#### Automatic leap second file management
The T-GM system depends on having the most recent leap second information. This data comes in a file that shows the difference in seconds between Coordinated Universal Time (UTC) and International Atomic Time (TAI). This file is regularly updated by the International Earth Rotation and Reference Systems Service (IERS). 
The latest leap seconds file can be downloaded from https://hpiers.obspm.fr/iers/bul/bulc/ntp/leap-seconds.list.
While the PTP operator container image includes the latest leap second information at build time, the system can automatically update the leap second file using announcements received through GPS to ensure it stays current.

##### How it works
The system initially uses leap second information included in the container during its creation. This information is stored in a resource called `leap-configmap` within the `openshift-ptp` namespace. This resource is mounted as a volume to the linuxptp-daemon pod. The file containing leap seconds is accessible by the `ts2phc` program.
Additionally, GPS satellites broadcast leap second updates. If this information differs from what's stored, the leap-configmap is automatically updated with the newer GPS data. The `ts2phc` program picks the changes automatically.

Automatic leapfile updates rely on WPC NIC NAV-TIMELS notifications. This notification can be enabled or disabled in the E810 plugin section.
Manual updates of the `leap-configmap` resource are not recommended.


### ptpConfig to enable High Availability for phc2sys by adding profiles of ptp4l enabled config's under `haProfiles`

```
apiVersion: ptp.openshift.io/v1
kind: PtpConfig
metadata:
name: suppress-logs-ptpconfig
namespace: openshift-ptp
spec:
phc2sysOpts: "-a -r"
ptp4lOpts: " "
profile:
- name: "enable-ha"
  ...
  ...
  ......
  ptpSettings:
    stdoutFilter: "^.*delay   filtered.*$"
    logReduce: "true"
    haProfiles: "profile1,profile2"
  recommend:
- profile: "enable-ha"
  priority: 4
  match:
    - nodeLabel: "node-role.kubernetes.io/worker"

```
Requirements:
Two ptp4l configurations must exist with the phc2sysOPts field set to an empty string.
The names of these ptp4l configurations will be used and listed under the ptpSettings/haProfiles key in the phc2sys-only enabled ptpConfig.


## Quick Start

To install PTP Operator:
```
$ make deploy
```

To un-install:
```
$ make undeploy
```
