package sheet

import (
	"bytes"
	"reflect"
	"testing"
)

func TestXLSXRoundTrip(t *testing.T) {
	rows := [][]string{
		{"country_iso", "mcc", "mnc", "price"},
		{"PK", "410", "01", "0.0045"},
		{"AE", "424", "", "0.021"},
		{"note", "923001234567", "a & <b>", "Ünïcødé ✓"},
	}
	var buf bytes.Buffer
	if err := WriteXLSX(&buf, "Rates", rows); err != nil {
		t.Fatal(err)
	}
	if !IsXLSX(buf.Bytes()) {
		t.Fatal("not detected as xlsx")
	}
	got, err := Read(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, rows) {
		t.Fatalf("round trip:\n got %q\nwant %q", got, rows)
	}
}

func TestReadCSVVariants(t *testing.T) {
	for _, in := range []string{"\xef\xbb\xbfa,b\n1,2\n", "a;b\n1;2\n", "a\tb\n1\t2\n"} {
		got, err := Read([]byte(in))
		if err != nil || !reflect.DeepEqual(got, [][]string{{"a", "b"}, {"1", "2"}}) {
			t.Fatalf("%q: %q %v", in, got, err)
		}
	}
}

func TestColumns(t *testing.T) {
	for i, name := range map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 701: "ZZ", 702: "AAA"} {
		if columnName(i) != name || columnIndex(name+"7") != i {
			t.Fatalf("%d <-> %s", i, name)
		}
	}
}
