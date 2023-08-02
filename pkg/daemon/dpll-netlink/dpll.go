// Description: DPLL subsystem.

package dpll_netlink

import (
	"errors"

	"github.com/mdlayher/genetlink"
	"github.com/mdlayher/netlink"
)

// A Conn is a connection to netlink family "dpll".
type Conn struct {
	c *genetlink.Conn
	f genetlink.Family
}

// GetGenetlinkConn exposes genetlink connection
func (c *Conn) GetGenetlinkConn() *genetlink.Conn {
	return c.c
}

// GetGenetlinkFamily exposes genetlink family
func (c *Conn) GetGenetlinkFamily() genetlink.Family {
	return c.f
}

// GetMcastGroupId finds the requested multicast group in the family and returns its ID
func (c *Conn) GetMcastGroupId(mcGroup string) (id uint32, found bool) {
	for _, group := range c.f.Groups {
		if group.Name == mcGroup {
			return group.ID, true
		}
	}
	return 0, false
}

// Dial opens a Conn for netlink family "dpll". Any options are passed directly
// to the underlying netlink package.
func Dial(cfg *netlink.Config) (*Conn, error) {
	c, err := genetlink.Dial(cfg)
	if err != nil {
		return nil, err
	}

	f, err := c.GetFamily("dpll")
	if err != nil {
		return nil, err
	}

	return &Conn{c: c, f: f}, nil
}

// Close closes the Conn's underlying netlink connection.
func (c *Conn) Close() error { return c.c.Close() }

// DoDeviceIdGet wraps the "device-id-get" operation:
// Get id of dpll device that matches given attributes
func (c *Conn) DoDeviceIdGet(req DoDeviceIdGetRequest) (*DoDeviceIdGetReply, error) {
	ae := netlink.NewAttributeEncoder()
	// TODO: field "req.ModuleName", type "string"
	if req.ClockId != 0 {
		ae.Uint64(DPLL_A_CLOCK_ID, req.ClockId)
	}
	if req.Type != 0 {
		ae.Uint8(DPLL_A_TYPE, req.Type)
	}

	b, err := ae.Encode()
	if err != nil {
		return nil, err
	}

	msg := genetlink.Message{
		Header: genetlink.Header{
			Command: DPLL_CMD_DEVICE_ID_GET,
			Version: c.f.Version,
		},
		Data: b,
	}

	msgs, err := c.c.Execute(msg, c.f.ID, netlink.Request)
	if err != nil {
		return nil, err
	}

	replies := make([]*DoDeviceIdGetReply, 0, len(msgs))
	for _, m := range msgs {
		ad, err := netlink.NewAttributeDecoder(m.Data)
		if err != nil {
			return nil, err
		}

		var reply DoDeviceIdGetReply
		for ad.Next() {
			switch ad.Type() {
			case DPLL_A_ID:
				reply.Id = ad.Uint32()
			}
		}

		if err := ad.Err(); err != nil {
			return nil, err
		}

		replies = append(replies, &reply)
	}

	if len(replies) != 1 {
		return nil, errors.New("dpll: expected exactly one DoDeviceIdGetReply")
	}

	return replies[0], nil
}

// DoDeviceIdGetRequest is used with the DoDeviceIdGet method.
type DoDeviceIdGetRequest struct {
	// TODO: field "ModuleName", type "string"
	ClockId uint64
	Type    uint8
}

// DoDeviceIdGetReply is used with the DoDeviceIdGet method.
type DoDeviceIdGetReply struct {
	Id uint32
}

func ParseDeviceReplies(msgs []genetlink.Message) ([]*DoDeviceGetReply, error) {
	replies := make([]*DoDeviceGetReply, 0, len(msgs))
	for _, m := range msgs {
		ad, err := netlink.NewAttributeDecoder(m.Data)
		if err != nil {
			return nil, err
		}

		var reply DoDeviceGetReply
		for ad.Next() {
			switch ad.Type() {
			case DPLL_A_ID:
				reply.Id = ad.Uint32()
			case DPLL_A_MODULE_NAME:
				reply.ModuleName = ad.String()
			case DPLL_A_MODE:
				reply.Mode = ad.Uint8()
			case DPLL_A_MODE_SUPPORTED:
				reply.ModeSupported = ad.Uint8()
			case DPLL_A_LOCK_STATUS:
				reply.LockStatus = ad.Uint8()
			case DPLL_A_TEMP:
				// TODO: field "reply.Temp", type "s32"
			case DPLL_A_CLOCK_ID:
				reply.ClockId = ad.Uint64()
			case DPLL_A_TYPE:
				reply.Type = ad.Uint8()
			}
		}

		if err := ad.Err(); err != nil {
			return nil, err
		}

		replies = append(replies, &reply)
	}
	return replies, nil
}

// DoDeviceGet wraps the "device-get" operation:
// Get list of DPLL devices (dump) or attributes of a single dpll device
func (c *Conn) DoDeviceGet(req DoDeviceGetRequest) (*DoDeviceGetReply, error) {
	ae := netlink.NewAttributeEncoder()
	if req.Id != 0 {
		ae.Uint32(DPLL_A_ID, req.Id)
	}
	// TODO: field "req.ModuleName", type "string"

	b, err := ae.Encode()
	if err != nil {
		return nil, err
	}

	msg := genetlink.Message{
		Header: genetlink.Header{
			Command: DPLL_CMD_DEVICE_GET,
			Version: c.f.Version,
		},
		Data: b,
	}

	msgs, err := c.c.Execute(msg, c.f.ID, netlink.Request)
	if err != nil {
		return nil, err
	}

	replies, err := ParseDeviceReplies(msgs)
	if err != nil {
		return nil, err
	}

	if len(replies) != 1 {
		return nil, errors.New("dpll: expected exactly one DoDeviceGetReply")
	}

	return replies[0], nil
}

// DumpDeviceGet wraps the "device-get" operation:
// Get list of DPLL devices (dump) or attributes of a single dpll device

func (c *Conn) DumpDeviceGet() ([]*DoDeviceGetReply, error) {
	// No attribute arguments.
	var b []byte

	msg := genetlink.Message{
		Header: genetlink.Header{
			Command: DPLL_CMD_DEVICE_GET,
			Version: c.f.Version,
		},
		Data: b,
	}

	msgs, err := c.c.Execute(msg, c.f.ID, netlink.Request|netlink.Dump)
	if err != nil {
		return nil, err
	}

	replies, err := ParseDeviceReplies(msgs)
	if err != nil {
		return nil, err
	}
	return replies, nil
}

// DoDeviceGetRequest is used with the DoDeviceGet method.
type DoDeviceGetRequest struct {
	Id uint32
	// TODO: field "ModuleName", type "string"
}

// DoDeviceGetReply is used with the DoDeviceGet method.
type DoDeviceGetReply struct {
	Id            uint32
	ModuleName    string
	Mode          uint8
	ModeSupported uint8
	LockStatus    uint8
	// TODO: field "Temp", type "s32"
	ClockId uint64
	Type    uint8
}
