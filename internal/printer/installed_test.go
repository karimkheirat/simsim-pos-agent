package printer

import "testing"

func TestParseGetPrinterJSON_ArrayDropsVirtual(t *testing.T) {
	raw := []byte(`[{"Name":"XP-420B","DriverName":"XP-420B","PortName":"USB001"},{"Name":"AnyDesk Printer","DriverName":"x","PortName":"y"},{"Name":"Microsoft Print to PDF","DriverName":"x","PortName":"PORTPROMPT:"}]`)
	got, err := parseGetPrinterJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "XP-420B" || got[0].Port != "USB001" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseGetPrinterJSON_SingleObject(t *testing.T) {
	got, err := parseGetPrinterJSON([]byte(`{"Name":"EPSON TM-T20III","DriverName":"EPSON","PortName":"ESDPRT001"}`))
	if err != nil || len(got) != 1 || got[0].Name != "EPSON TM-T20III" {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestParseGetPrinterJSON_Empty(t *testing.T) {
	got, err := parseGetPrinterJSON([]byte("  "))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestMergeDirect(t *testing.T) {
	queues := []Installed{
		{Name: "XP-420B", Driver: "Seagull XP-420B", Port: "USB001"},
		{Name: "EPSON TM-T20", Driver: "EPSON", Port: "USB002"},
		{Name: "Xprinter XP-58", Driver: "Generic / Text Only", Port: "IP_192.168.1.9"},
	}
	devices := []Device{
		{ID: ParseDeviceID("MFG:Xprinter;MDL:XP-370B;CMD:TSPL,ESC/POS;"), PortNumber: 1},
		{ID: ParseDeviceID("MFG:Xprinter;MDL:XP-58;CMD:ESC/POS;"), PortNumber: 3},
		{ID: DeviceID{}, PortNumber: 4}, // could not be opened: nothing to show
	}
	got := mergeDirect(queues, devices)
	if len(got) != 4 {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
	if got[0].Name != "Xprinter XP-370B" || !got[0].Direct || got[0].Spec != "usb:Xprinter XP-370B" || got[0].Language != "escpos" || got[0].Port != "USB001" {
		t.Fatalf("direct first: %+v", got[0])
	}
	if got[1].Name != "Xprinter XP-58 (USB)" || got[1].Spec != "usb:Xprinter XP-58 (USB)" {
		t.Fatalf("a direct name equal to a queue name is told apart: %+v", got[1])
	}
	// The XP-420B queue sits on USB001, the XP-370B's port: same printer, dropped.
	if got[2].Name != "EPSON TM-T20" || got[3].Name != "Xprinter XP-58" {
		t.Fatalf("queues: %+v", got[2:])
	}
	if got[2].SpecOrName() != "EPSON TM-T20" || got[0].SpecOrName() != "usb:Xprinter XP-370B" {
		t.Fatal("spec")
	}
}
