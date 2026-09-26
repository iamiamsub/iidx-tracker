package eamuse

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The .kbin files were made by Python kbinxml 2.1 (testdata/make_reference.py).
const testdata = "../../testdata"

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdata, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCrypt(t *testing.T) {
	got, err := Crypt("1-5f0b1c2d-a1b2", []byte("hello"))
	if err != nil || hex.EncodeToString(got) != "b23a80da21" { // Python eamuse.crypt
		t.Fatalf("crypt = %x, %v", got, err)
	}
	back, _ := Crypt("1-5f0b1c2d-a1b2", got)
	if string(back) != "hello" {
		t.Fatalf("round trip = %q", back)
	}
	if _, err := Crypt("nope", nil); err == nil {
		t.Fatal("bad info accepted")
	}
}

func lz77Literals(data []byte) []byte {
	var out []byte
	for i := 0; i < len(data); i += 8 {
		chunk := data[i:min(i+8, len(data))]
		out = append(out, byte(1<<len(chunk)-1))
		out = append(out, chunk...)
	}
	return append(out, 0, 0, 0)
}

func TestLZ77(t *testing.T) {
	// literals, then a back reference (distance 3, length 6), then the end marker
	if got := LZ77Decode([]byte("\x07abc\x00\x33\x00\x00")); string(got) != "abcabcabc" {
		t.Fatalf("got %q", got)
	}
	in := []byte("0123456789abcdefXYZ")
	if got := LZ77Decode(lz77Literals(in)); !bytes.Equal(got, in) {
		t.Fatalf("got %q", got)
	}
}

func TestEncodeMatchesKbinxml(t *testing.T) {
	names, _ := filepath.Glob(filepath.Join(testdata, "*.xml"))
	for _, path := range names {
		name := filepath.Base(path)
		if strings.HasPrefix(name, "raw_") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			root, err := ParseXML(read(t, name))
			if err != nil {
				t.Fatal(err)
			}
			got, err := EncodeBinary(root)
			if err != nil {
				t.Fatal(err)
			}
			want := read(t, strings.TrimSuffix(name, ".xml")+".kbin")
			if !bytes.Equal(got, want) {
				t.Fatalf("encoding differs from kbinxml\n got %x\nwant %x", got, want)
			}
			// decoding the reference and encoding it again gives the same bytes
			back, err := DecodeBinary(want)
			if err != nil {
				t.Fatal(err)
			}
			again, _ := EncodeBinary(back)
			if !bytes.Equal(again, want) {
				t.Fatalf("decode/encode round trip differs")
			}
		})
	}
}

func TestDecodeValues(t *testing.T) {
	root, err := DecodeBinary(read(t, "types.kbin"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"u8": "255", "s8": "-1", "u16": "65535", "s16": "-2", "u8b": "7", "s32": "-123456",
		"u32": "4000000000", "s64": "-9000000000", "u64": "18000000000000000000", "bool": "1",
		"ip": "192.168.1.5", "t": "1700000000", "f": "1.500000", "d": "-2.250000", "v2": "1 2",
		"v3": "-1 0 1", "v4": "1 2 3 4", "arr": "-1 2 -3", "arr8": "1 2 3 4 5", "b": "00ff10",
		"s": "シャッフル&<x>", "e": "", "plain": "text without type",
	}
	for name, v := range want {
		if got, ok := root.FindText(name); !ok || got != v {
			t.Errorf("%s = %q (found %v), want %q", name, got, ok, v)
		}
	}
	if root.Get("b") != "日本語" || root.Find("arr").Get("__count") != "3" || root.Find("b").Get("__size") != "3" {
		t.Errorf("attributes: %+v / %+v / %+v", root.Attrs, root.Find("arr").Attrs, root.Find("b").Attrs)
	}
	if root.Find("deep/inner").Get("x") != "1" || root.Find("void") == nil {
		t.Error("nested nodes lost")
	}
}

// The Python tracker stored the music.reg element as lxml text; the Go text must mean the same.
func TestRawXMLCompatible(t *testing.T) {
	doc, err := DecodeBinary(read(t, "musicreg_req.kbin"))
	if err != nil {
		t.Fatal(err)
	}
	mine, err := ParseXML(doc.Children[0].XML())
	if err != nil {
		t.Fatal(err)
	}
	python, err := ParseXML(read(t, "raw_musicreg.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mine, python) {
		t.Fatal("Go and Python raw XML differ")
	}
	if python.Find("music_play_log").Get("graph_type") == "" {
		t.Fatal("expected fields missing")
	}
}

func TestDecodeEncodeBody(t *testing.T) {
	root, _ := ParseXML(read(t, "services.xml"))
	body, headers, err := Encode(root, true, "1-12345678-abcd")
	if err != nil || headers["X-Compress"] != "none" || headers["X-Eamuse-Info"] != "1-12345678-abcd" {
		t.Fatal(err, headers)
	}
	back, binary, err := Decode(body, headers["X-Eamuse-Info"], headers["X-Compress"])
	if err != nil || !binary {
		t.Fatal(err)
	}
	urls := map[string]string{}
	for _, it := range back.Iter("item") {
		urls[it.Get("name")] = it.Get("url")
	}
	if urls["local"] != "http://10.0.0.5:8083/core" || urls["ntp"] != "ntp://pool.ntp.org/" {
		t.Fatal(urls)
	}
	// text XML bodies are kept as text
	text, _, _ := Encode(root, false, "")
	if again, binary, err := Decode(text, "", ""); err != nil || binary || again.Find("services").Get("expire") != "10800" {
		t.Fatal("text body", err, binary)
	}
}
