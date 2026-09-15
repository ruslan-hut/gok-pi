package huawei

import "testing"

func TestParseDeviceListFromSmartLogger(t *testing.T) {
	count, devices := ParseDeviceList(map[uint8]string{
		0x87: "\x05",
		0x8B: "1=LUNA2000B-V2;2=V200R024C00SPC400;3=P1.0-D1.0;4=BT2610479858;5=0;6=1.0;8=LUNA2000B-V2;;9=0",
		0x88: "1=Smart Logger;2=V300R024C10SPC161;4=102597484606;5=0",
	})

	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
	if len(devices) != 2 || devices[0].Model != "Smart Logger" || devices[1].ESN != "BT2610479858" {
		t.Errorf("devices = %+v", devices)
	}
	if devices[1].DeviceType != "LUNA2000B-V2" {
		t.Errorf("DeviceType = %q", devices[1].DeviceType)
	}
}
