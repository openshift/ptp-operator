# Conformance tests

## Running the tests

To run the conformance tests, first set the following environment variables:

- **KUBECONFIG**: this is the path to the openshift kubeconfig
- **PTP_TEST_MODE**: this is the desired mode to run the tests. Choose between: Discovery, OC and BC. See below for an
  explanation for each mode.
- **DISCOVERY_MODE**: This is a legacy option and is equivalent to setting "Discovery" option in the PTP_TEST_MODE
  environment variable
- **ENABLE_TEST_CASE**: This is an option to run the long running tests separated by comma. For example, to run reboot
  test, `ENABLE_TEST_CASE=reboot` shall be used.
- **SKIP_INTERFACES**: passes a list of interfaces to be skipped in the form "eno1,ens2f1"
  `SKIP_INTERFACES="eno1,ens2f1"`
- **KEEP_PTPCONFIG**: if set to true, the test script will not delete the ptpconfig it automatically created. If set to
  false, it will delete them. The ptpconfigs are deleted by default. For instance `KEEP_PTPCONFIG=true`

Then run the following command:

```bash
make functests
```

So for instance to run in discovery mode the command line could look like this:

```bash
KUBECONFIG="/home/user/.kube/config" PTP_TEST_MODE=Discovery make functests
```

To run all the tests

```bash
KUBECONFIG="/home/usr/.kube/config" ENABLE_TEST_CASE=reboot PTP_TEST_MODE=Discovery make functests
```

To run all the tests with a container:

```bash
docker run -e PTP_TEST_MODE=<TEST MODE> -e ENABLE_TEST_CASE=<EXTRA TEST CASES> -v <KUBECONFIG PATH>:/tmp/config:Z -v <OUTPUT DIRECTORY PATH>:/output:Z <IMAGE>
```

for example:

```bash
docker run -e PTP_TEST_MODE=OC -e ENABLE_TEST_CASE=reboot -v /home/usr/.kube/config.3nodes:/tmp/config:Z -v .:/output:Z quay.io/redhat-cne/ptp-operator-test:latest
```

## Labelling test nodes manually in discovery mode

In Discovery mode, the node holding the clock under test is indicated via a label
To indicate a node with a BC configuration, label it with

```bash
 oc label  node <nodename>    ptp/clock-under-test=""
```

In addition to the label, a valid user provided ptpconfig must be present corresponding to the labeled node

example BC ptp config:

```yaml
apiVersion: ptp.openshift.io/v1
kind: PtpConfig
metadata:
  managedFields:
  - apiVersion: ptp.openshift.io/v1
    fieldsType: FieldsV1
    fieldsV1:
      f:spec:
        .: {}
        f:profile: {}
        f:recommend: {}
    manager: __debug_bin
    operation: Update
    time: "2022-06-13T22:27:54Z"
  name: test-bc-master
  namespace: openshift-ptp
spec:
  profile:
  - name: test-bc-master
    phc2sysOpts: -a -r
    ptp4lConf: |-
      [global]
      ptp_dst_mac 01:1B:19:00:00:00
      p2p_dst_mac 01:80:C2:00:00:0E
      domainNumber 24
      logging_level 7
      boundary_clock_jbod 1
      [ens2f0]
      masterOnly 0
      [ens2f1]
      masterOnly 1
    ptp4lOpts: "-2"
    ptpSchedulingPolicy: SCHED_OTHER
    ptpSchedulingPriority: 65
  recommend:
  - match:
    - nodeLabel: ptp/clock-under-test
    priority: 5
    profile: test-bc-master

```

## PTP test modes

The type of clock to test is indicated by selecting a PTP test mode. The mode selection also determines which test are
executed.

### Discovery mode

This mode assumes that one or more valid ptpconfig objects are configured in the openshift-ptp namespace. The test
parses the ptpconfig objects and labels to automatically determines the type of clock to test. Currently able to detect
OC, BC configurations. GrandMasters or Boundary clock slaves are not detected.

### Auto-Configured modes in SNO and Multinode clusters

The following modes need a multinode or single node cluster to run:

- with a multi-node cluster, One node is selected to act as a Master. Another node is selected to act as a slave
  Ordinary clock. The test suite uses the L2 discovery mechanism to discover the cluster connectivity and create a
  graph of the network. The graph is resolved to find the best OC, or BC configuration.
- with a single node cluster, the L2 mechanism instead reports interfaces on which PTP ethernet frames are received.
  The PTP frames received are assumed to be from ax external Grand master. The test suite uses one of the interfaces
  identified as receiving ptp GM frames and create the ordinary or boundary clock with it.

#### OC

The OC configurations includes a grandmaster providing a clock signal to a slave ordinary clock. See diagram below:

![multi_oc](doc/multi_oc.svg)

If the OC configuration was detected from the discovery mode or in OC mode in single node cluster with an external
grandmaster, the following diagram applies:

![sno_oc](doc/sno_oc.svg)

#### BC

Several boundary clock scenarios are possible, dependent on minimum hardware configuration.
In the optimal scenario, the openshift cluster needs to be at least connected to 2 separate LANs. These LANs are
realized with the help of VLANs or other technologies. The following configuration is tested if 2 LANs are detected in
the cluster. Note that the cluster needs to have at least one NIC which ports are connected on the 2 separate LANs, as
shown below:

![multi_bc_with_slave](doc/multi_bc_with_slave.svg)

If only one LAN is available, the following configuration is tested. In this case, the synchronization of the slave
ordinary clocks cannot be tested

![multi_bc](doc/multi_bc.svg)

Finally, if the BC clock is discovered in "Discovery" mode or in BC mode but in single node cluster, then the following
 configuration can be tested. Note that in discovery mode, the conformance test suite does not modify the ptpconfigs
 provided by the user.

![sno_bc](doc/sno_bc.svg)

#### DualNICBC

Several dual NIC boundary clock scenarios are possible, dependent on minimum hardware configuration.
In the optimal scenario, the openshift cluster needs to be at least connected to 3 separate LANs. These LANs are
realized with the help of VLANs or other technologies. The following configuration is tested if 3 LANs are detected in
 the cluster.

![multi_dnbc_with_slaves](doc/multi_dnbc_with_slaves.svg)

If only one LAN is available, the following configuration is tested. In this case, the synchronization of the slave
ordinary clocks cannot be tested

![multi_dnbc](doc/multi_dnbc.svg)

Finally, if the BC clock is discovered in "Discovery" mode or in Dual NIC BC mode but in single node cluster, then the
following configuration can be tested. Note that in discovery mode, the conformance test suite does not modify the
ptpconfigs provided by the user.

![sno_dnbc](doc/sno_dnbc.svg)

## Test mode discovery workflow

The Test mode discovery workflow is as follows:

 ![discovery_workflow](doc/ptptestconfig.svg)

### States

- **Desired config**: the desired mode is selected at this point. The choices are Discovery, OC and BC.
- **PTP configuration**: if OC, BC modes are selected, a valid default configuration is configured automatically. The
  output of the configuration is 1 or 2 ptpconfig objects in the openshift-ptp. If Discovery mode is selected, this
  configuration step is skipped.
- **Discovery**: the ptpconfig installed in the openshift-ptp namespace are analysed to determine which type of clocks
  they represent, either OC, BC. This step is the same whether the configuration was configured by the test suite
  (OC, BC) or by the user (Discovery). If the ptpconfigs are valid and a type of clock can be determined successfully,
  then discovery is successful and the corresponding test are executed.

## Host based L2 discovery

The rapid discovery of L2 hosts is realized by probing the network with test Ethernet frames.
See <https://github.com/test-network-function/l2discovery>

![L2topology](doc/l2topology.svg)

Each openshift node reports L2 connectivity with a JSON log as shown below. The Json represent a map ordered by
ethertype -> local interface -> remote mac address. So for each local interface it lists the MAC addresses that are
received per ethertype. We are using the "experimental" ethertype to generate the probe packets (0x88b5):

```json
{
  "88b5": {
    "eno5np0": {
      "Local": {
        "IfName": "eno5np0",
        "IfMac": {
          "Data": "F4:03:43:D1:70:A0"
        },
        "IfIndex": 2
      },
      "Remote": {
        "F4:03:43:D1:40:F0": true,
        "F4:03:43:D1:65:D0": true
      }
    },
    "eno6np1": {
      "Local": {
        "IfName": "eno6np1",
        "IfMac": {
          "Data": "F4:03:43:D1:70:A8"
        },
        "IfIndex": 4
      },
      "Remote": {
        "48:DF:37:BC:F0:E1": true,
        "48:DF:37:BC:F4:41": true,
        "F4:03:43:D1:40:F8": true,
        "F4:03:43:D1:65:D8": true
      }
    },
    "ens3f0": {
      "Local": {
        "IfName": "ens3f0",
        "IfMac": {
          "Data": "48:DF:37:BC:F1:64"
        },
        "IfIndex": 6
      },
      "Remote": {
        "48:DF:37:BC:F0:E0": true,
        "48:DF:37:BC:F4:40": true
      }
    },
    "ens3f1": {
      "Local": {
        "IfName": "ens3f1",
        "IfMac": {
          "Data": "48:DF:37:BC:F1:65"
        },
        "IfIndex": 8
      },
      "Remote": {
        "48:DF:37:BC:F0:E1": true,
        "48:DF:37:BC:F4:41": true,
        "F4:03:43:D1:40:F8": true,
        "F4:03:43:D1:65:D8": true
      }
    }
  }
}
```

## Solving Clock configuration from discovered L2 topology

To solve the problem of finding an optimal ptp configuration, we are using a homemade solver based on the backtracting
algorithm modified to take into account multiple level of successive constraints

As an example, the representation of the algorithm to solve the BC configuration in a multinode scenario is the folowing:

![multi_bc](doc/multi_bc.svg)

Note the labels, p0, p1. These represent the interface number in the algorithm and also the step. in step 0, we can
 check constraints for interface p0 only. In step 1, we can check constraints for interfaces p0 and p1 if needed.
The representation in the code of the visual representation of the algorithm represented by the picture above is the
following:

```golang
BCAlgo := [][][]int{
{{int(StepNil), 0, 0}},            //step nil
{{int(StepSameNic), 2, 0, 1}},     //step0 checks that p0 and p1 are in the same NIC
{{int(StepSameIsland2), 2, 1, 2}}, //step1 checks that p1 and p2 are in the same island
}
```

each step in the algorithm means:

```golang
{<constraint to check>, <number of parameters to the function>, <interface0>, ..., interfaceN}}
```

note: parameter0 ... parameterN are integers representing an interface. 0 means p0, 1 means p1, etc...
