# PTP Simulation Infrastructure: netdevsim + gnss-sim

Hardware-free PTP Telecom Grandmaster (T-GM) simulation and testing
infrastructure. Enables end-to-end CI tests — including GNSS signal
loss/recovery, clock class transitions, and cascading holdover — without
physical GNSS receivers or Intel E810 NICs.

## Glossary

| Acronym | Definition |
|---------|------------|
| ACK | Acknowledgment (UBX response frame) |
| API | Application Programming Interface |
| BC | Boundary Clock (PTP node that acts as both master and slave) |
| CC | Clock Class (ITU-T quality level, e.g., CC6 = locked) |
| CFG | Configuration (UBX message class 0x06) |
| CI | Continuous Integration |
| DGPS | Differential GPS (enhanced accuracy GPS) |
| DPLL | Digital Phase-Locked Loop (hardware clock synchronization component) |
| DR | Dead Reckoning (position estimation without satellite fix) |
| EEC | Ethernet Equipment Clock (ITU-T G.8262 synchronization standard) |
| EXTTS | External Timestamp (PTP hardware clock input event) |
| FIFO | First In, First Out (kernel buffer queue) |
| GGA | GPS Fix Data (NMEA 0183 sentence type for position and fix quality) |
| GM | Grandmaster (PTP clock hierarchy root) |
| GNSS | Global Navigation Satellite System (GPS, GLONASS, Galileo, BeiDou) |
| GNGGA | Multi-constellation GGA sentence (GN = multi-GNSS talker ID) |
| GNRMC | Multi-constellation RMC sentence (Recommended Minimum data) |
| GPZDA | GPS Time and Date sentence |
| HTTP | Hypertext Transfer Protocol |
| IRQ | Interrupt Request |
| iTOW | GPS Time of Week in milliseconds (UBX NAV-CLOCK field) |
| MON | Monitor (UBX message class 0x0A) |
| NAV | Navigation (UBX message class 0x01) |
| NIC | Network Interface Card |
| NMEA | National Marine Electronics Association (text protocol for GPS data) |
| OC | Ordinary Clock (PTP endpoint, either master-only or slave-only) |
| OVS | Open vSwitch (software network switch) |
| PCI | Peripheral Component Interconnect (hardware bus standard) |
| PPS | Pulse Per Second (1 Hz timing signal from GNSS) |
| PTP | Precision Time Protocol (IEEE 1588) |
| PTY | Pseudo-Terminal (virtual serial port pair) |
| SyncE | Synchronous Ethernet (ITU-T G.8261, frequency sync over Ethernet) |
| tAcc | Time Accuracy estimate in nanoseconds (UBX NAV-CLOCK field) |
| TAI | International Atomic Time (monotonic timescale, no leap seconds) |
| T-GM | Telecom Grandmaster (ITU-T G.8275.1 PTP profile, GNSS-locked) |
| TGM | Telecom Grandmaster test mode (T-GM only) |
| TGMBC | Telecom Grandmaster + Boundary Clock test mode |
| TGMOC | Telecom Grandmaster + Ordinary Clock test mode |
| UBX | u-blox proprietary binary protocol (for GNSS receiver control) |
| VLAN | Virtual Local Area Network (IEEE 802.1Q) |
| WPC | Westport Channel (Intel E810 ICE NIC series with DPLL/GNSS support) |

---

## 1. Architecture

### 1.1 Kernel Simulation Layer

The netdevsim module creates all hardware-equivalent devices when a device
is registered with `wpc=1`:

```mermaid
flowchart LR
    nsim_bus["netdevsim bus<br/>wpc=1 flag"]

    subgraph dpll_devices [DPLL Devices]
        dpll_pps["PPS DPLL<br/>LOCKED_HO_ACQ"]
        dpll_eec["EEC DPLL<br/>LOCKED_HO_ACQ"]
    end

    subgraph dpll_pins [DPLL Pins]
        gnss_pin["GNSS-1PPS<br/>input, 1 Hz"]
        synce_pins["SyncE pins<br/>output, 10 MHz"]
    end

    subgraph char_devices [Character Devices]
        gnss_dev["/dev/gnssN<br/>GNSS char device"]
        ptp_mock["ptp_mock<br/>PTP clock + EXTTS"]
    end

    subgraph timers [Periodic Timers]
        ubx_timer["UBX 1 Hz<br/>NAV-STATUS + NAV-CLOCK"]
        ntf_timer["DPLL 1 Hz<br/>change notifications"]
    end

    nsim_bus --> dpll_devices & dpll_pins & char_devices & timers
```

### 1.2 Data Flow: NMEA and Clock Synchronization

gnss-sim (built from source, runs on the host) generates NMEA sentences
and writes them into `/dev/gnssN`. The kernel echoes them to readers and
parses GGA fix quality to drive UBX responses:

```mermaid
flowchart LR
    gnss_sim["gnss-sim<br/>NMEA generator<br/>+ HTTP API"]

    subgraph kernel [Kernel]
        gnss_dev["/dev/gnssN"]
        gga_parser["GGA parser"]
        ubx_timer["UBX timer<br/>NAV-STATUS"]
        ptp_mock["ptp_mock<br/>EXTTS 1PPS"]
        ntf_timer["DPLL ntf timer"]
    end

    subgraph daemon [linuxptp-daemon pod]
        ts2phc["ts2phc"]
        gpsd["gpsd"]
        ubxtool["ubxtool"]
        monitor["MonitorGNSSEvents<br/>WithUblox"]
        ptp4l["ptp4l"]
        phc2sys["phc2sys<br/>(baremetal only)"]
    end

    gnss_sim -->|"write NMEA"| gnss_dev
    gnss_dev -->|"read FIFO"| ts2phc
    gnss_dev -->|"read FIFO"| gpsd
    gnss_dev --> gga_parser -->|"gnss_gps_fix"| ubx_timer
    ubx_timer -->|"NAV-STATUS"| gnss_dev
    ptp_mock -->|"EXTTS 1PPS"| ts2phc
    ts2phc --> ptp4l
    ptp4l -.->|"omitted in Kind/netdevsim"| phc2sys
    gpsd --> ubxtool --> monitor
    ntf_timer -->|"dpll_pin_change_ntf"| daemon
```

### 1.3 Event Pipeline and Test Orchestration

GNSS state changes propagate through cloud events to a test consumer.
The Ginkgo test suite drives signal loss/recovery via the gnss-sim HTTP
API and observes results through metrics and the event consumer:

```mermaid
flowchart LR
    subgraph tests [Ginkgo test suite]
        tgm["TGM:<br/>signal loss/recovery"]
        tgmoc["TGMOC:<br/>GM + OC sync"]
        tgmbc["TGMBC:<br/>GM + BC holdover"]
    end

    gnss_sim["gnss-sim<br/>HTTP API"]

    subgraph daemon [linuxptp-daemon pod]
        monitor["MonitorGNSSEvents"]
        event_handler["EventHandler"]
        metrics["metrics endpoint"]
    end

    cloud_proxy["cloud-event-proxy"]
    consumer["event consumer"]

    tests -->|"POST /signal/loss<br/>POST /signal/restore"| gnss_sim
    tests -->|"curl metrics<br/>kubectl exec"| daemon
    tests -->|"subscribe"| consumer
    monitor --> event_handler --> cloud_proxy --> consumer
```

---

## 2. Kernel: netdevsim DPLL/GNSS/UBX

**File:** `drivers/net/netdevsim/dpll.c`

### 2.1 DPLL Emulation

A pair of DPLL devices is registered when a netdevsim device is created
with `wpc=1`:

```
echo "1 0001:1f:01.0 1 1 1 1" > /sys/bus/netdevsim/new_device
                                          ^-- wpc flag
```

```mermaid
flowchart LR
    subgraph dpll_pair [DPLL Device Pair]
        pps["PPS DPLL<br/>LOCKED_HO_ACQ<br/>mode: automatic"]
        eec["EEC DPLL<br/>LOCKED_HO_ACQ<br/>mode: automatic"]
    end

    subgraph pins [DPLL Pins]
        gnss["GNSS-1PPS<br/>type: gnss<br/>direction: input<br/>freq: 1 Hz<br/>phase-offset: 0"]
        synce1["ens1f0<br/>type: synce-eth-port<br/>direction: output<br/>freq: 10 MHz"]
        synce2["ens1f1<br/>type: synce-eth-port<br/>direction: output<br/>freq: 10 MHz"]
    end

    gnss -->|connected| pps
    synce1 -->|connected| pps
    synce2 -->|connected| pps
    pps -.->|"dpll_netdev_pin_set"| synce1 & synce2
```

**Pin operations:**

| Op | GNSS pin | SyncE pins |
|----|----------|------------|
| `frequency_get` | 1 Hz | 10 MHz |
| `direction_get` | input | output |
| `state_on_dpll_get` | connected | connected |
| `phase_offset_get` | 0 | 0 |

### 2.2 Virtual GNSS Device and UBX Protocol

A `gnss_device` is registered on the same PCI parent as the netdevsim NIC,
creating `/dev/gnssN`. The `write_raw` callback processes two protocols:

```mermaid
flowchart TD
    write["/dev/gnssN write"]
    write -->|"buf starts with '$'"| nmea_branch["NMEA sentence"]
    write -->|"buf starts with 0xB5 0x62"| ubx_branch["UBX frame"]
    write -->|"other"| discard["silently discard"]

    nmea_branch --> gga_check{"contains GGA?"}
    gga_check -->|yes| parse_fix["parse fix quality<br/>update gnss_gps_fix"]
    gga_check -->|no| echo_only["echo only"]
    parse_fix --> read_fifo["gnss_insert_raw"]
    echo_only --> read_fifo

    ubx_branch --> ubx_class{"class?"}
    ubx_class -->|"MON (0x0A)"| mon_ver["build MON-VER response<br/>PROTVER=29.20"]
    ubx_class -->|"CFG (0x06)"| cfg_msg["parse CFG-MSG"]
    ubx_class -->|"other"| ack_other["ACK-ACK"]

    cfg_msg -->|"NAV-STATUS or NAV-CLOCK"| enable_timer["enable 1 Hz ubx_timer<br/>+ ACK-ACK"]
    cfg_msg -->|"other"| ack_cfg["ACK-ACK"]

    mon_ver --> read_fifo
    enable_timer --> read_fifo
    ack_other --> read_fifo
    ack_cfg --> read_fifo
```

**UBX periodic timer (`ubx_timer`):**

Once enabled by a CFG-MSG write, a 1 Hz `hrtimer` fires and injects:

1. **NAV-STATUS** (class=0x01, id=0x03, 16-byte payload) — contains `gpsFix`,
   flags, time-to-first-fix
2. **NAV-CLOCK** (class=0x01, id=0x22, 20-byte payload) — contains iTOW,
   clock bias, drift, tAcc, fAcc

Both are inserted via `gnss_insert_raw()` under `gnss_lock` spinlock.

### 2.3 Dynamic gpsFix from NMEA GGA

The GGA parser (`nsim_parse_gga_fix`) scans the write buffer for any
`$??GGA` sentence and extracts fix quality from field 6. The buffer may
contain multiple concatenated NMEA sentences (e.g., GNRMC + GNGGA + GPZDA
in a single write), so the parser searches for `$` markers rather than
only checking the first byte.

**GGA-to-gpsFix mapping:**

| GGA Quality (field 6) | Meaning | UBX gpsFix | NAV-STATUS flags |
|------------------------|---------|------------|------------------|
| 0 | Invalid / no fix | 0x00 (NoFix) | 0x00 |
| 1 | GPS fix | 0x03 (3D) | 0x0D (gpsFixOk + wknSet + towSet) |
| 2 | DGPS fix | 0x03 (3D) | 0x0D |
| 6 | Estimated (DR) | 0x01 (Dead Reckoning) | 0x00 |
| other | — | 0x03 (3D) | 0x0D |

NAV-CLOCK `tAcc` reflects both GNSS fix state and DPLL lock status:

| Condition | `tAcc` (ns) |
|-----------|-------------|
| `gpsFix >= 2` (valid fix) | 10 |
| `gpsFix < 2`, DPLL in HOLDOVER | 10 |
| `gpsFix < 2`, DPLL in UNLOCKED (freerun) | 999,999 |

During HOLDOVER the oscillator is still in-spec, so `tAcc` stays low.
This prevents the daemon's `isOffsetInRange()` check from bypassing the
HOLDOVER state. Once the DPLL holdover timeout expires and transitions to
UNLOCKED, `tAcc` spikes to 999,999 ns, matching real hardware behavior
where the oscillator eventually drifts out of spec.

### 2.4 DPLL Notification Timer

A separate 1 Hz `hrtimer` defers `dpll_device_change_ntf` and
`dpll_pin_change_ntf` calls to a workqueue (`ntf_work`). This is necessary
because:

- The DPLL notification functions acquire a mutex (cannot be called from
  hard-IRQ context)
- Late-joining userspace consumers (linuxptp-daemon starting after device
  creation) need to receive initial pin state including phase offset

Notifications use `genlmsg_multicast_allns` to deliver to all network
namespaces, so daemons in Kind cluster worker containers receive them.

### 2.5 PTP Mock EXTTS

**File:** `drivers/ptp/ptp_mock.c`

The mock PTP clock provides external timestamp (EXTTS) capability for
ts2phc, which requires a hardware 1PPS input pin.

| Feature | Value |
|---------|-------|
| Pins | 2 (pin 0: "NONE", pin 1: "GNSS1PPS") |
| EXTTS channels | 1 |
| Initial time | `ktime_get_real_ns()` (wall clock) |
| EXTTS timer | 1 Hz hrtimer, TAI-aligned timestamps |

The EXTTS timer generates `PTP_CLOCK_EXTTS` events with timestamps aligned to
the TAI second boundary (`ktime_get_clocktai_ns()` rounded down). This gives
ts2phc an offset of approximately 0 ns.

### 2.6 Device Creation Flow

```mermaid
sequenceDiagram
    participant User as sysfs write
    participant Bus as nsim_bus
    participant Dev as nsim_dev
    participant DPLL as nsim_dpll
    participant GNSS as gnss_device
    participant PTP as ptp_mock

    User->>Bus: echo "id pci clk ports queues wpc" > new_device
    Bus->>Bus: store wpc flag
    Bus->>Dev: nsim_drv_probe()
    Dev->>Dev: create netdevs

    alt wpc == 1
        Dev->>DPLL: nsim_dpll_init()
        DPLL->>DPLL: register PPS + EEC DPLL devices
        DPLL->>DPLL: register GNSS pin + SyncE pins
        DPLL->>GNSS: gnss_allocate_device() + gnss_register_device()
        Note over GNSS: /dev/gnssN created
        DPLL->>DPLL: start ntf_timer (1 Hz)
        DPLL->>DPLL: init ubx_timer (started on CFG-MSG)
    end

    Dev->>PTP: mock_phc_create()
    Note over PTP: /dev/ptpN created with EXTTS support
```

### 2.7 Signal Loss/Recovery Data Flow

```mermaid
sequenceDiagram
    participant Test as CI Test
    participant Sim as gnss-sim
    participant Kernel as netdevsim /dev/gnss0
    participant Timer as UBX 1Hz timer
    participant UBX as ubxtool
    participant Daemon as linuxptp-daemon
    participant Events as cloud-event-proxy

    Note over Kernel: gnss_gps_fix = 3 (initial)
    Timer->>Kernel: NAV-STATUS gpsFix=3
    Kernel->>UBX: read gpsFix=3
    UBX->>Daemon: GNSS LOCKED

    Test->>Sim: POST /signal/loss
    Sim->>Kernel: $GNGGA,...,0,...  (fix quality = 0)
    Kernel->>Kernel: parse GGA -> gnss_gps_fix = 0
    Kernel->>Kernel: DPLL: LOCKED → HOLDOVER
    Timer->>Kernel: NAV-STATUS gpsFix=0, flags=0x00
    Timer->>Kernel: NAV-CLOCK tAcc=10 (holdover, still in-spec)
    Kernel->>UBX: read gpsFix=0
    UBX->>Daemon: GNSS HOLDOVER (CC7)
    Daemon->>Events: ptp-state-change (HOLDOVER)
    Note over Kernel: After holdover timeout (30s default)
    Kernel->>Kernel: DPLL: HOLDOVER → UNLOCKED
    Timer->>Kernel: NAV-CLOCK tAcc=999999 (out of spec)
    UBX->>Daemon: GNSS FREERUN (CC248)
    Daemon->>Events: ptp-state-change (FREERUN)

    Test->>Sim: POST /signal/restore
    Sim->>Kernel: $GNGGA,...,1,...  (fix quality = 1)
    Kernel->>Kernel: parse GGA -> gnss_gps_fix = 3
    Timer->>Kernel: NAV-STATUS gpsFix=3, flags=0x0D
    Timer->>Kernel: NAV-CLOCK tAcc=10
    Kernel->>UBX: read gpsFix=3
    UBX->>Daemon: GNSS LOCKED
    Daemon->>Events: gnss-state-change (SYNCHRONIZED)
    Events->>Test: event received, assertion passes
```

---

## 3. gnss-sim

**Source:** `ptp-tools/gnss-sim/`

A userspace Go tool that simulates a GNSS receiver, generating NMEA
sentences at 1 Hz and providing an HTTP API for test control.

### 3.1 Output Backends

```mermaid
flowchart TB
    subgraph gnss_sim [gnss-sim process]
        generator["NMEA Generator<br/>1 Hz: GNRMC, GNGGA, GPZDA"]
        api["HTTP API<br/>:9200"]
        dpll_sim["DPLL State Machine<br/>LOCKED / HOLDOVER / FREERUN"]
    end

    subgraph outputs [Output Backends]
        kernel_dev["/dev/gnss0<br/>--gnss-dev<br/>kernel GNSS char device"]
        pty1["/var/run/ptp/ttyGNSS_GNSS0<br/>--pty-links<br/>PTY pair (self-managed)"]
        file["stdout or file<br/>--outputs<br/>plain file I/O"]
    end

    generator --> kernel_dev
    generator --> pty1
    generator --> file
    api -->|"signal loss/restore"| generator
    api -->|"state queries"| dpll_sim
```

Each NMEA sentence is written individually (one `Write()` call per sentence)
to ensure the kernel's GGA parser sees each sentence at the start of a buffer.

**PTY self-management (`pty.go`):** Creates PTY pairs directly using kernel
ioctls (`TIOCGPTN`, `TIOCSPTLCK`) on `/dev/ptmx`, with symlinks like
`/var/run/ptp/ttyGNSS_GNSS0 -> /dev/pts/N`. Writes use `syscall.Write` with
`O_NONBLOCK` — `EAGAIN` and `EIO` are silently dropped so the simulator
loop never stalls when no reader is attached.

**Kernel GNSS device (`--gnss-dev`):** Opens the kernel GNSS character
device and writes NMEA sentences directly. The kernel's `gnss_insert_raw()`
relays them into the device's read FIFO, so ts2phc and gpsd reading the
same `/dev/gnss0` receive the stream natively. `openGNSSDev()` polls for
device appearance with a 30-second timeout to handle the race between
kernel module loading and gnss-sim startup.

### 3.2 Deployment

```mermaid
flowchart TD
    start["configGNSS.sh"]
    start --> require["require GNSS_SIM_IMAGE"]
    require --> cleanup["delete prior gnss-sim Deployment/pod"]
    cleanup --> apply["kubectl apply gnss-sim-deployment.yaml<br/>hostNetwork + /dev + /sys + /var/run/ptp"]
    apply --> ready["wait Deployment available"]
    ready --> detect{"pod: /dev/gnss* exists?"}

    detect -->|yes| hybrid["Hybrid mode in pod:<br/>--gnss-dev /dev/gnss0"]
    detect -->|no| pty_only["PTY-only mode in pod:<br/>--pty-links /var/run/ptp/ttyGNSS_*"]

    hybrid --> health["curl nodeIP:API_PORT/health"]
    pty_only --> health
```

`scripts/configGNSS.sh` requires `GNSS_SIM_IMAGE` and applies
`scripts/gnss-sim-deployment.yaml` as a Kubernetes Deployment in
`openshift-ptp` (hostNetwork, privileged, host `/dev`, `/sys`, and
`/var/run/ptp` mounts). The container auto-selects kernel GNSS or the
PTY fallback; PTYs are created inside the pod on the hostPath
`/var/run/ptp` so linuxptp-daemon pods that mount the same path can
read them.

---

## 4. Network Topology

```mermaid
flowchart TB
    subgraph worker1 ["kind-netdevsim-worker (Server 1)"]
        ens1f0_w["ens1f0<br/>WPC NIC (DPLL+GNSS)"]
        ens1f1_w["ens1f1<br/>WPC NIC (DPLL+GNSS)"]
        ens2f0_w["ens2f0"]
        ens2f1_w["ens2f1"]
    end

    subgraph worker2 ["kind-netdevsim-worker2 (Server 2)"]
        ens3f0["ens3f0"]
        ens3f1["ens3f1"]
        ens3f2["ens3f2"]
    end

    subgraph worker3 ["kind-netdevsim-worker3 (Server 3)"]
        ens4f0["ens4f0"]
        ens5f0["ens5f0"]
        ens6f0["ens6f0"]
    end

    subgraph switch1 ["switch1 (OVS container)"]
        br0["OVS bridge br0"]
        vlan1501["VLAN 1501"]
        vlan1500["VLAN 1500"]
        vlan1502["VLAN 1502"]
    end

    ens1f0_w ---|"VLAN 1501"| vlan1501
    ens4f0 ---|"VLAN 1501"| vlan1501

    ens1f1_w ---|"VLAN 1500"| vlan1500
    ens2f0_w ---|"VLAN 1500"| vlan1500
    ens3f0 ---|"VLAN 1500"| vlan1500
    ens3f1 ---|"VLAN 1500"| vlan1500

    ens2f1_w ---|"VLAN 1502"| vlan1502
    ens3f2 ---|"VLAN 1502"| vlan1502
    ens5f0 ---|"VLAN 1502"| vlan1502
    ens6f0 ---|"VLAN 1502"| vlan1502
```

Server 1 (worker) has the WPC NIC with `wpc=1` — DPLL + GNSS emulation is
active on ens1f0 and ens1f1. The `configpair.sh` script's 11th argument
(`[wpc]`) enables this on the side-1 NIC.

The PTP daemon DaemonSet mounts host `/dev` into the linuxptp container so
it can access `/dev/gnssN`. The E810 plugin bundle is always applied because
the kernel virtual GNSS device responds to the UBX protocol, so
gpsd/ubxtool initialization succeeds without real hardware.

---

## 5. Test Modes

| Mode | Topology |
|------|----------|
| `tgm` | WPC T-GM only (signal loss/recovery, clock class transitions, cloud events) |
| `tgmoc` | WPC T-GM + downstream OC (same LAN) |
| `tgmbc` | WPC T-GM + BC slave + BC master + optional downstream OC (cascading holdover) |

```mermaid
flowchart LR
    subgraph tgmoc_topo ["TGMOC Topology"]
        gm1["WPC T-GM<br/>ens1f0 (worker)"]
        oc1["OC Slave<br/>ens3f0 (worker2)"]
        gm1 -->|"PTP over VLAN 1500"| oc1
    end
```

```mermaid
flowchart LR
    subgraph tgmbc_topo ["TGMBC Topology"]
        gm2["WPC T-GM<br/>ens1f0 (worker)"]
        bc_slave["BC Slave<br/>ens3f0 (worker2)"]
        bc_master["BC Master<br/>ens3f1 (worker2)"]
        oc2["OC Slave<br/>ens4f0 (worker3)"]
        gm2 -->|"PTP"| bc_slave
        bc_slave ---|"same NIC"| bc_master
        bc_master -->|"PTP"| oc2
    end
```

**Test coverage:**

| Context | Test | What it verifies |
|---------|------|-----------------|
| TGM | Signal loss/recovery events | GNSS state change, clock class 6 -> 7 -> 248 -> 6, cloud events |
| TGM | Process status | ts2phc, ptp4l, phc2sys running; CLOCK_REALTIME LOCKED (gpsd omitted: gnss-sim owns /dev/gnssN) |
| TGM | Clock state via metrics | `openshift_ptp_clock_class` reaches 6 |
| TGM | DPLL state via gnss-sim API | DPLL reports LOCKED |
| TGMOC | GM process status + CC6 | ts2phc, ptp4l running; clock class reaches 6 |
| TGMOC | Downstream OC sync | OC slave synchronized to WPC T-GM |
| TGMBC | GM + BC reach Locked CC6 | Both GM and BC achieve clock class 6 |
| TGMBC | Cascading holdover on GNSS loss | Signal loss degrades GM CC, BC cascades; restore recovers both |

---

## 6. Kernel Selftests

### dpll.sh

| # | Test | Validates |
|---|------|-----------|
| 1 | module load | netdevsim loads cleanly |
| 2 | no DPLL before create | clean slate |
| 3 | device creation | `wpc=1` triggers DPLL init |
| 4 | DPLL enumeration | PPS + EEC devices exist |
| 5 | lock status | all `locked-ho-acq` |
| 6 | mode | all `automatic` |
| 7 | module-name | `netdevsim` |
| 8 | pin enumeration | GNSS + SyncE pins |
| 9 | GNSS pin props | `GNSS-1PPS`, input, connected |
| 10 | phase offset | 0 |
| 11 | frequency | 1 Hz |
| 12 | SyncE pin props | output, connected, 10 MHz |
| 13 | SyncE pin count | matches port count |
| 14 | GNSS char device | `/dev/gnssN` exists |
| 15 | GNSS sysfs discovery | netdev -> gnss path |
| 16 | NMEA loopback | write NMEA, read it back |
| 17 | UBX MON-VER | `PROTVER=29.20` response |
| 18 | binary discard | non-NMEA/non-UBX dropped |
| 19 | NMEA+UBX interleave | both protocols coexist |
| 20 | periodic NAV | CFG-MSG enables NAV-STATUS + NAV-CLOCK |
| 21 | default gpsFix | gpsFix=3, flags=0x0D before any NMEA |
| 22 | GGA fix loss | `$GNGGA,...,0,...` sets gpsFix=0, flags=0x00 |
| 23 | GGA fix restore | `$GNGGA,...,1,...` restores gpsFix=3, flags=0x0D |
| 24 | GGA batch write | batched GNRMC+GNGGA in single write still updates gpsFix |
| 25 | teardown | DPLL + GNSS cleaned up |
| 26 | stress | 3x create/destroy cycles |

Uses Python helpers `extract_gps_fix()` and `extract_nav_flags()` to parse
binary NAV-STATUS frames from capture files.

### ptp_mock.sh

PTP clock creation, dual-pin configuration, EXTTS event generation,
timestamp accuracy validation.
