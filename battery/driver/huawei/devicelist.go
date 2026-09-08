package huawei

import (
	"strconv"
	"strings"
)

// Device is one entry of the device list returned by the vendor's device
// identification query (sections 4.3.6.2 and 4.3.6.3). Each device is described
// by an "attribute ID=value" string such as
//
//	1=LUNA2000-200KTL-H0;2=V800R021C10;3=P1.0-D5.0;4=123456789ABC;5=1;6=1.0;8=LUNA2000-P
//
// which is what tells you what actually answers on an endpoint: whether the
// Modbus server is the cabinet controller itself or a logger with the cabinet
// behind it, and what the unit IDs of the downstream devices are.
type Device struct {
	Model           string // attribute 1
	SoftwareVersion string // attribute 2
	ProtocolVersion string // attribute 3
	ESN             string // attribute 4
	DeviceID        int    // attribute 5; 0 is the device holding the Modbus card
	FeatureVersion  string // attribute 6
	DeviceType      string // attribute 8, e.g. LUNA2000-P or SUN2000

	// Attributes holds every field as returned, including any this struct does
	// not name, so an unrecognised attribute is visible rather than dropped.
	Attributes map[int]string
}

// IsHost reports whether this is the device the Modbus card sits in, which is
// the one answering on the unit the query was addressed to.
func (d Device) IsHost() bool { return d.DeviceID == 0 }

// String renders the device compactly for a probe listing.
func (d Device) String() string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(d.DeviceID))
	b.WriteString(": ")
	b.WriteString(orUnknown(d.Model))
	if d.DeviceType != "" {
		b.WriteString(" (" + d.DeviceType + ")")
	}
	if d.SoftwareVersion != "" {
		b.WriteString(" sw=" + d.SoftwareVersion)
	}
	if d.ESN != "" {
		b.WriteString(" esn=" + d.ESN)
	}

	return b.String()
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown model"
	}

	return s
}

// ParseDevice decodes one device description string. Unparsable attribute IDs
// and empty segments are skipped: the point of the probe is to report what a
// device said, not to reject it for a malformed field.
func ParseDevice(desc string) Device {
	d := Device{Attributes: map[int]string{}}

	for _, part := range strings.Split(desc, ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}

		id, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil {
			continue
		}

		value = strings.TrimSpace(value)
		d.Attributes[id] = value

		switch id {
		case 1:
			d.Model = value
		case 2:
			d.SoftwareVersion = value
		case 3:
			d.ProtocolVersion = value
		case 4:
			d.ESN = value
		case 5:
			d.DeviceID, _ = strconv.Atoi(value)
		case 6:
			d.FeatureVersion = value
		case 8:
			d.DeviceType = value
		}
	}

	return d
}

// ParseDeviceList turns the objects of a device-list response into devices,
// ordered by the object ID they arrived under. Object 0x87 carries the device
// count rather than a description and is returned separately; a count of -1
// means the device did not report one.
func ParseDeviceList(objects map[uint8]string) (count int, devices []Device) {
	count = -1
	if v, ok := objects[0x87]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			count = n
		}
	}

	ids := make([]int, 0, len(objects))
	for id := range objects {
		if id > 0x87 {
			ids = append(ids, int(id))
		}
	}
	// Sort without pulling in a comparator: the ID space is one byte.
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}

	for _, id := range ids {
		desc := objects[uint8(id)]
		if strings.TrimSpace(desc) == "" {
			continue
		}
		devices = append(devices, ParseDevice(desc))
	}

	return count, devices
}
