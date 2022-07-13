package export

import "strings"

type Mac struct {
	Data string
}

type PCIAddress struct {
	Device, Function string
}

type Iface struct {
	IfName  string
	IfMac   Mac
	IfIndex int
	IfPci   PCIAddress
}

type Neighbors struct {
	Local  Iface
	Remote map[string]bool
}

func (mac Mac) String() string {
	return strings.ToUpper(string([]byte(mac.Data)[0:2]) + ":" +
		string([]byte(mac.Data)[2:4]) + ":" +
		string([]byte(mac.Data)[4:6]) + ":" +
		string([]byte(mac.Data)[6:8]) + ":" +
		string([]byte(mac.Data)[8:10]) + ":" +
		string([]byte(mac.Data)[10:12]))
}
