# PTP Operator
## Table of Contents

- [E810 plugin](#ptp-operator)
- [Quick Start](#quick-start)

## E810 plugin
Intel E810 plugin can be used to do hardware-specific configurations as required for e810 NICs, in order to use as a PTP grandmaster.



## Quick Start

#### Example config enable e810 plugin and doing configuration as GM.
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
        enableDefaultConfig: false
        settings:
          LocalMaxHoldoverOffSet: 1500
          LocalHoldoverTimeout: 14400
          MaxInSpecOffset: 100
        phaseOffsetPins:
          ens2f0:
            boardLabel: GNSS-1PPS
          ens7f0:
            boardLabel: SMA1
        pins:
          "ens2f0":
            "U.FL2": "0 2"
            "U.FL1": "0 1"
            "SMA2": "0 2"
            "SMA1": "0 1"
        ublxCmds:
          - args: #ubxtool -P 29.20 -z CFG-HW-ANT_CFG_VOLTCTRL,1
              - "-P"
              - "29.20"
              - "-z"
              - "CFG-HW-ANT_CFG_VOLTCTRL,1"
            reportOutput: false
          - args: #ubxtool -P 29.20 -e GPS
              - "-P"
              - "29.20"
              - "-e"
              - "GPS"
            reportOutput: false
          - args: #ubxtool -P 29.20 -d Galileo
              - "-P"
              - "29.20"
              - "-d"
              - "Galileo"
            reportOutput: false
          - args: #ubxtool -P 29.20 -d GLONASS
              - "-P"
              - "29.20"
              - "-d"
              - "GLONASS"
            reportOutput: false
          - args: #ubxtool -P 29.20 -d BeiDou
              - "-P"
              - "29.20"
              - "-d"
              - "BeiDou"
            reportOutput: false
          - args: #ubxtool -P 29.20 -d SBAS
              - "-P"
              - "29.20"
              - "-d"
              - "SBAS"
            reportOutput: false
          - args: #ubxtool -P 29.20 -t -w 5 -v 1 -e SURVEYIN,600,50000
              - "-P"
              - "29.20"
              - "-t"
              - "-w"
              - "5"
              - "-v"
              - "1"
              - "-e"
              - "SURVEYIN,600,50000"
            reportOutput: true  
          - args:
              - "-p"
              - "MON-HW"
            reportOutput: true
          - args:
              - "-p"
              - "CFG-MSG,1,38,300"
            reportOutput: true
    ts2phcOpts: " "
    ts2phcConf: |
      [nmea]
      ts2phc.master 1
      [global]
      use_syslog  0
      verbose 1
      logging_level 7
      ts2phc.pulsewidth 100000000
      ts2phc.nmea_serialport  /dev/gnss0
      [ens2f0]
      ts2phc.extts_polarity rising
      ts2phc.extts_correction 0
  recommend:
  - profile: "profile1"
    priority: 4
    match:
    - nodeLabel: "node-role.kubernetes.io/worker"
    
```
