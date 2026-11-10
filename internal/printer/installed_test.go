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
