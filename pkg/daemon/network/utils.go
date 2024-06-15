package network

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/golang/glog"
	"github.com/jaypipes/ghw"
)

const (
	_ETHTOOL_HARDWARE_RECEIVE_CAP   = "hardware-receive"
	_ETHTOOL_HARDWARE_TRANSMIT_CAP  = "hardware-transmit"
	_ETHTOOL_HARDWARE_RAW_CLOCK_CAP = "hardware-raw-clock"
	_ETHTOOL_PTP_HARDWARE_CLOCK     = "PTP Hardware Clock:"
)

func ethtoolInstalled() bool {
	_, err := exec.LookPath("ethtool")
	return err == nil
}

func netParseEthtoolTimeStampFeature(cmdOut *bytes.Buffer) bool {
	var hardRxEnabled bool
	var hardTxEnabled bool
	var hardRawEnabled bool

	glog.V(2).Infof("cmd output for %v", cmdOut)
	scanner := bufio.NewScanner(cmdOut)
	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), "\t")
		parts := strings.Fields(line)
		if parts[0] == _ETHTOOL_HARDWARE_RECEIVE_CAP {
			hardRxEnabled = true
		}
		if parts[0] == _ETHTOOL_HARDWARE_TRANSMIT_CAP {
			hardTxEnabled = true
		}
		if parts[0] == _ETHTOOL_HARDWARE_RAW_CLOCK_CAP {
			hardRawEnabled = true
		}
	}
	return hardRxEnabled && hardTxEnabled && hardRawEnabled
}

func DiscoverPTPDevices() ([]string, error) {
	var out bytes.Buffer
	nics := make([]string, 0)

	if !ethtoolInstalled() {
		return nics, fmt.Errorf("discoverDevices(): ethtool not installed. Cannot grab NIC capabilities")
	}

	ethtoolPath, _ := exec.LookPath("ethtool")

	net, err := ghw.Network()
	if err != nil {
		return nics, fmt.Errorf("discoverDevices(): error getting network info: %v", err)
	}

	for _, dev := range net.NICs {
		glog.Infof("grabbing NIC timestamp capability for %v", dev.Name)
		cmd := exec.Command(ethtoolPath, "-T", dev.Name)
		cmd.Stdout = &out
		err := cmd.Run()
		if err != nil {
			glog.Infof("could not grab NIC timestamp capability for %v: %v", dev.Name, err)
		}

		if !netParseEthtoolTimeStampFeature(&out) {
			continue
		}

		link, err := os.Readlink(fmt.Sprintf("/sys/class/net/%s", dev.Name))
		if err != nil {
			glog.Infof("could not grab NIC PCI address for %v: %v", dev.Name, err)
			continue
		}

		var PCIAddr string
		pathSegments := strings.Split(link, "/")
		if len(pathSegments)-3 <= 0 {
			glog.Infof("unexpected sysfs address for %v: %v", dev.Name, out.String())
			continue
		}

		// sysfs address for N3000 looks like: ../../devices/pci0000:85/0000:85:00.0/0000:86:00.0/0000:87:10.0/0000:8a:00.1/net/eth1
		// sysfs address looks like: /sys/devices/pci0000:17/0000:17:02.0/0000:19:00.5/net/eno1
		PCIAddr = pathSegments[len(pathSegments)-3]

		if _, err := os.Stat(fmt.Sprintf("/sys/bus/pci/devices/%s", PCIAddr)); os.IsNotExist(err) {
			glog.Infof("unexpected device address for device name %s PCI %s: %v", dev.Name, PCIAddr, err)
			continue
		}

		// If the physfn doesn't exist this means the interface is not a virtual function so we ca add it to the list
		if _, err := os.Stat(fmt.Sprintf("/sys/bus/pci/devices/%s/physfn", PCIAddr)); os.IsNotExist(err) {
			nics = append(nics, dev.Name)
		}
	}
	return nics, nil
}

func GetPhcId(iface string) (phcId string) {
	var out bytes.Buffer

	if !ethtoolInstalled() {
		glog.Errorf("discoverDevices(): ethtool not installed. Cannot grab NIC capabilities")
		return
	}

	ethtoolPath, _ := exec.LookPath("ethtool")

	cmd := exec.Command(ethtoolPath, "-T", iface)
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		glog.Infof("GetPhcId: could not grab NIC timestamp capability for %s: %v", iface, err)
		return
	}

	phcId = parsePhcId(&out)
	if phcId != "" {
		return fmt.Sprintf("/dev/%s", phcId)
	}
	return
}

func parsePhcId(cmdOut *bytes.Buffer) string {
	scanner := bufio.NewScanner(cmdOut)
	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), "\t")
		if strings.Contains(line, _ETHTOOL_PTP_HARDWARE_CLOCK) {
			parts := strings.Fields(line)
			return parts[len(parts)-1]
		}
	}
	return ""
}
