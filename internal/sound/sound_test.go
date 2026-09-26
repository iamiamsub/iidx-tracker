package sound

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"iidx-tracker/internal/eamuse"
)

type ev struct {
	cmd, key byte
	length   int16
}

// dot1 builds a .1 file: chart index -> events.
func dot1(charts map[int][]ev) []byte {
	out := make([]byte, 96)
	for chart, events := range charts {
		slot := chartSlot[chart]
		binary.LittleEndian.PutUint32(out[slot*8:], uint32(len(out)))
		start := len(out)
		for i, e := range events {
			b := make([]byte, 8)
			binary.LittleEndian.PutUint32(b, uint32(1000*(i+1)))
			b[4], b[5] = e.cmd, e.key
			binary.LittleEndian.PutUint16(b[6:], uint16(e.length))
			out = append(out, b...)
		}
		end := make([]byte, 8)
		binary.LittleEndian.PutUint32(end, 0x7FFFFFFF)
		out = append(out, end...)
		binary.LittleEndian.PutUint32(out[slot*8+4:], uint32(len(out)-start))
	}
	return out
}

// ifsFile builds an IFS: files (name -> data, nil = back reference to the super), optional super.
func ifsFile(t *testing.T, files map[string][]byte, super string, superMD5 []byte) []byte {
	t.Helper()
	root := &eamuse.Node{Name: "imgfs"}
	if super != "" {
		s := &eamuse.Node{Name: "_super_", Attrs: attrs("__type", "str"), Text: super}
		s.Children = []*eamuse.Node{{Name: "md5", Attrs: attrs("__type", "bin", "__size", "16"), Text: fmt.Sprintf("%x", superMD5)}}
		root.Children = append(root.Children, s)
	}
	var data []byte
	for _, name := range sortedKeys(files) {
		packed := strings.ReplaceAll(strings.ReplaceAll(name, "_", "__"), ".", "_E")
		if packed[0] >= '0' && packed[0] <= '9' {
			packed = "_" + packed
		}
		n := &eamuse.Node{Name: packed}
		if files[name] == nil {
			n.Children = []*eamuse.Node{{Name: "i", Attrs: attrs("__type", "u32"), Text: "1"}}
		} else {
			n.Attrs = attrs("__type", "3s32")
			n.Text = fmt.Sprintf("%d %d 0", len(data), len(files[name]))
			data = append(data, files[name]...)
		}
		root.Children = append(root.Children, n)
	}
	manifest, err := eamuse.EncodeBinary(root)
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 36)
	binary.BigEndian.PutUint32(head, ifsSignature)
	binary.BigEndian.PutUint16(head[4:], 3)
	binary.BigEndian.PutUint16(head[6:], 0xFFFF^3)
	binary.BigEndian.PutUint32(head[16:], uint32(36+len(manifest)))
	sum := md5.Sum(manifest)
	copy(head[20:], sum[:])
	return append(append(head, manifest...), data...)
}

func attrs(kv ...string) []eamuse.Attr {
	var out []eamuse.Attr
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, eamuse.Attr{Name: kv[i], Value: kv[i+1]})
	}
	return out
}

func sortedKeys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	for i := range out { // tiny insertion sort, enough for a test
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNoteCounts(t *testing.T) {
	// SPA: 2 notes + 1 charge note; DPA: notes on both sides; a BPM change is not a note
	data := dot1(map[int][]ev{
		3: {{0, 1, 0}, {0, 7, 0}, {0, 2, 480}, {4, 0, 150}},
		8: {{0, 1, 0}, {1, 3, 0}, {1, 7, 100}},
	})
	got, err := NoteCounts(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[int]int64{3: 4, 8: 4}) {
		t.Fatal(got)
	}
	if _, err := NoteCounts([]byte("short")); err == nil {
		t.Fatal("short file accepted")
	}
}

func TestScanLayers(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "data", "sound")
	chart := func(n int) []byte { // SPA with n notes
		var e []ev
		for i := 0; i < n; i++ {
			e = append(e, ev{0, 1, 0})
		}
		return dot1(map[int][]ev{3: e})
	}
	// 01000: plain IFS in data
	write(t, filepath.Join(base, "01000.ifs"), ifsFile(t, map[string][]byte{"01000.1": chart(10), "01000.2dx": {1}}, "", nil))
	// 01001: data has it, mod "b" replaces the whole IFS, mod "a" too - "a" wins (alphabetical)
	write(t, filepath.Join(base, "01001.ifs"), ifsFile(t, map[string][]byte{"01001.1": chart(11)}, "", nil))
	write(t, filepath.Join(root, "data_mods", "b", "sound", "01001.ifs"), ifsFile(t, map[string][]byte{"01001.1": chart(12)}, "", nil))
	write(t, filepath.Join(root, "data_mods", "a", "sound", "01001.ifs"), ifsFile(t, map[string][]byte{"01001.1": chart(13)}, "", nil))
	// 01002: only a loose folder in a mod
	write(t, filepath.Join(root, "data_mods", "b", "sound", "01002", "01002.1"), chart(14))
	// 01003: a -p0 patch whose base matches - the patch's chart is used
	baseIFS := ifsFile(t, map[string][]byte{"01003.1": chart(15)}, "", nil)
	write(t, filepath.Join(base, "01003.ifs"), baseIFS)
	baseMD5 := baseIFS[20:36]
	write(t, filepath.Join(base, "01003-p0.ifs"), ifsFile(t, map[string][]byte{"01003.1": chart(16)}, "01003.ifs", baseMD5))
	// 01004: a -p0 patch whose base changed - ignored, the base is used
	write(t, filepath.Join(base, "01004.ifs"), ifsFile(t, map[string][]byte{"01004.1": chart(17)}, "", nil))
	write(t, filepath.Join(base, "01004-p0.ifs"), ifsFile(t, map[string][]byte{"01004.1": chart(18)}, "01004.ifs", make([]byte, 16)))
	// 01005: a -p0 patch that keeps the chart in its base (back reference)
	baseIFS = ifsFile(t, map[string][]byte{"01005.1": chart(19)}, "", nil)
	write(t, filepath.Join(base, "01005.ifs"), baseIFS)
	write(t, filepath.Join(base, "01005-p0.ifs"), ifsFile(t, map[string][]byte{"01005.1": nil}, "01005.ifs", baseIFS[20:36]))
	// 01006: a mod replaces only the chart inside the data IFS (<name>_ifs)
	write(t, filepath.Join(base, "01006.ifs"), ifsFile(t, map[string][]byte{"01006.1": chart(20)}, "", nil))
	write(t, filepath.Join(root, "data_mods", "a", "sound", "01006_ifs", "01006.1"), chart(21))
	// 01007: an _ifs folder without any IFS creates nothing
	write(t, filepath.Join(root, "data_mods", "a", "sound", "01007_ifs", "01007.1"), chart(22))

	layers, err := Layers(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 || layers[0].Mod != "a" || layers[1].Mod != "b" || layers[2].Mod != "" {
		t.Fatal(layers)
	}
	want := map[int64]int64{1000: 10, 1001: 13, 1002: 14, 1003: 16, 1004: 17, 1005: 19, 1006: 21}
	got := map[int64]int64{}
	for _, s := range Scan(layers) {
		if s.Err != "" {
			t.Errorf("%d: %s", s.ID, s.Err)
		}
		got[s.ID] = s.Counts[3]
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}

	// data only (omnimix copied straight into data): mods ignored
	layers, _ = Layers(filepath.Join(root, "data"), []string{})
	got = map[int64]int64{}
	for _, s := range Scan(layers) {
		got[s.ID] = s.Counts[3]
	}
	if got[1001] != 11 || got[1006] != 20 || got[1002] != 0 {
		t.Fatal(got)
	}
}
