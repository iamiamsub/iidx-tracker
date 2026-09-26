// Package sound reads the charts (.1) of a game's sound folder and counts their notes, finding
// each song's chart the way the game and IFS LayeredFS would.
//
// Where the game looks (bm2dx SoundPath_Resolve 0x180ae5590): /data/sound/<id>-p0.ifs, then
// <id>.ifs, then the loose folder <id>/<id>.1. A -p0.ifs is a patch on top of <id>.ifs (its
// "super"); when the base no longer has the fingerprint the patch expects, the game ignores the
// patch ("broken super: missmatch fingerprint").
//
// The game folder is on this PC (Layers), or on the PC of a browser (Listing): the browser sends the
// file list and the IFS manifests, the scan tells it where each chart is (Song.Need), and the
// browser reads and counts the charts itself - only the counts come back.
//
// LayeredFS: every folder in data_mods is a mod laid over data. Mods are searched alphabetically
// and the first one that has a file wins, file by file. A file inside an IFS can be replaced
// with <name>_ifs/<file>, but an IFS cannot be created out of nothing.
//
// Chart layout (iidx-datatools parse_chart_notecounts.py, bm2dx Chart_FileSlotFromIndex): 12
// (offset, size) pairs, then 8-byte events (time i32, command u8, key u8, length i16) ending at
// time 0x7FFFFFFF. Commands 0 and 1 are the notes of each side; a note with a length is a
// charge note and counts twice, like the game's note count.
package sound

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing/fstest"

	"iidx-tracker/internal/i18n"

	"iidx-tracker/internal/eamuse"
)

// chartSlot maps chart index (SPB SPN SPH SPA SPL DPB DPN DPH DPA DPL) to its slot in the .1 header.
var chartSlot = [10]int{3, 1, 0, 2, 4, 9, 7, 6, 8, 10}

// NoteCounts counts the notes of every chart in a .1 file; charts the file lacks are absent.
func NoteCounts(data []byte) (map[int]int64, error) {
	if len(data) >= 4 && binary.BigEndian.Uint32(data) == ifsSignature {
		// unreleased songs ship 252-byte IFS stubs in place of every file of their folder
		return nil, i18n.New("譜面の代わりにダミーが置かれています (未収録曲)", "a dummy stands in for the chart (song not included)")
	}
	if len(data) < 96 {
		return nil, i18n.New(".1 が短すぎます", "the .1 is too short")
	}
	out := map[int]int64{}
	for chart, slot := range chartSlot {
		off := int(binary.LittleEndian.Uint32(data[slot*8:]))
		size := int(binary.LittleEndian.Uint32(data[slot*8+4:]))
		if off == 0 || size == 0 {
			continue
		}
		if off < 96 || off > len(data) {
			return nil, i18n.Errorf("譜面 %d の位置 %#x がファイルの外です", "chart %d at %#x lies outside the file", chart, off)
		}
		end := min(len(data), off+size)
		var n int64
		for p := off; p+8 <= end; p += 8 {
			if int32(binary.LittleEndian.Uint32(data[p:])) == 0x7FFFFFFF {
				break
			}
			if cmd := data[p+4]; cmd == 0 || cmd == 1 {
				n++
				if binary.LittleEndian.Uint16(data[p+6:]) != 0 {
					n++ // charge note: start and end are both judged
				}
			}
		}
		out[chart] = n
	}
	return out, nil
}

// ---- IFS ---------------------------------------------------------------------

const ifsSignature = 0x6CAD8F89

type ifsEntry struct {
	start, size int64
	backref     bool // stored in the super IFS
}

// IFS is an opened archive: its manifest and where its data starts.
type IFS struct {
	fsys      fs.FS
	deferred  bool // its files are located, not read (see NeedRead)
	path      string
	md5       []byte // fingerprint a patch checks its super against
	dataStart int64
	files     map[string]ifsEntry // path inside the archive, "/" separated
	superName string
	superMD5  []byte
}

// fixName undoes the manifest's name escaping ("_E" = ".", "__" = "_", "_" before a digit).
func fixName(n string) string {
	n = strings.ReplaceAll(n, "_E", ".")
	n = strings.ReplaceAll(n, "__", "_")
	if len(n) > 1 && n[0] == '_' && n[1] >= '0' && n[1] <= '9' {
		n = n[1:]
	}
	return n
}

// openIFS reads the header and manifest of the IFS file of a layer.
func openIFS(l Layer, name string) (*IFS, error) {
	f, err := l.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 36)
	if _, err := io.ReadFull(f, head[:20]); err != nil {
		return nil, fmt.Errorf("%s: %v", path.Base(name), err)
	}
	if binary.BigEndian.Uint32(head) != ifsSignature {
		return nil, i18n.Errorf("%s: IFS ではありません", "%s: not an IFS", path.Base(name))
	}
	version := binary.BigEndian.Uint16(head[4:])
	manifestEnd := int64(binary.BigEndian.Uint32(head[16:]))
	manifestStart := int64(20)
	fs := &IFS{fsys: l.fsys, deferred: l.deferred, path: name, dataStart: manifestEnd, files: map[string]ifsEntry{}}
	if version > 1 {
		if _, err := io.ReadFull(f, head[20:36]); err != nil {
			return nil, err
		}
		fs.md5 = append([]byte(nil), head[20:36]...)
		manifestStart = 36
	}
	if manifestEnd <= manifestStart || manifestEnd-manifestStart > 64<<20 {
		return nil, i18n.Errorf("%s: 目録の位置が不正です", "%s: invalid manifest position", path.Base(name))
	}
	manifest := make([]byte, manifestEnd-manifestStart)
	n, err := io.ReadFull(f, manifest)
	if err == io.ErrUnexpectedEOF {
		manifest = manifest[:n] // patch IFSs without data end before their padded manifest end
	} else if err != nil {
		return nil, err
	}
	root, err := eamuse.DecodeBinary(manifest)
	if err != nil {
		return nil, i18n.Errorf("%s: 目録を読めません: %v", "%s: cannot read the manifest: %v", path.Base(name), err)
	}
	fs.walk(root, "")
	return fs, nil
}

func (fs *IFS) walk(folder *eamuse.Node, prefix string) {
	for _, c := range folder.Children {
		name := fixName(c.Name)
		switch {
		case name == "_info_":
		case name == "_super_":
			fs.superName = strings.TrimSpace(c.Text)
			if m, ok := c.FindText("md5"); ok {
				fs.superMD5, _ = hex.DecodeString(strings.TrimSpace(m))
			}
		case len(c.Children) > 0 && c.Children[0].Name == "i":
			fs.files[prefix+name] = ifsEntry{backref: true}
		case len(c.Children) > 0:
			fs.walk(c, prefix+name+"/")
		default:
			v := strings.Fields(c.Text)
			if len(v) >= 2 {
				start, _ := strconv.ParseInt(v[0], 10, 64)
				size, _ := strconv.ParseInt(v[1], 10, 64)
				fs.files[prefix+name] = ifsEntry{start: start, size: size}
			}
		}
	}
}

// find returns the archive path of the chart of song id (<id>.1, else any .1).
func (fs *IFS) find(id string) string {
	var any string
	names := make([]string, 0, len(fs.files))
	for n := range fs.files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		base := n[strings.LastIndex(n, "/")+1:]
		if base == id+".1" {
			return n
		}
		if any == "" && strings.HasSuffix(base, ".1") {
			any = n
		}
	}
	return any
}

func (fs *IFS) read(name string, super *IFS) ([]byte, error) {
	e, ok := fs.files[name]
	if !ok {
		return nil, i18n.Errorf("%s に %s がありません", "%s has no %s", path.Base(fs.path), name)
	}
	if e.backref {
		if super == nil {
			return nil, i18n.Errorf("%s: %s は元の IFS にあります", "%s: %s is in the original IFS", path.Base(fs.path), name)
		}
		return super.read(name, nil)
	}
	if e.size < 0 || e.size > 256<<20 {
		return nil, i18n.Errorf("%s: %s の大きさが不正です", "%s: %s has an invalid size", path.Base(fs.path), name)
	}
	if fs.deferred {
		return nil, &NeedRead{fs.path, fs.dataStart + e.start, e.size}
	}
	f, err := fs.fsys.Open(fs.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	at, ok := f.(io.ReaderAt)
	if !ok {
		return nil, fmt.Errorf("%s: cannot read at an offset", path.Base(fs.path))
	}
	buf := make([]byte, e.size)
	_, err = at.ReadAt(buf, fs.dataStart+e.start)
	return buf, err
}

// ---- scanning a game folder ------------------------------------------------------

// Layer is one sound folder: a mod in data_mods, or data itself (Mod == "").
type Layer struct {
	Mod      string
	Dir      string // its sound folder in the game folder: data/sound, data_mods/<mod>/sound
	fsys     fs.FS  // the game folder
	deferred bool   // charts are located, not read (see NeedRead)
}

// NeedRead is a chart a scan of a Listing located but could not read: the browser reads it.
// Size -1 = the whole file.
type NeedRead struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
}

func (n *NeedRead) Error() string { return "not read here: " + n.Path }

// readFile reads a whole file of a layer (or locates it, see NeedRead).
func (l Layer) readFile(name string) ([]byte, error) {
	if l.deferred {
		return nil, &NeedRead{name, 0, -1}
	}
	return fs.ReadFile(l.fsys, name)
}

func (l Layer) isFile(name string) bool {
	st, err := fs.Stat(l.fsys, name)
	return err == nil && !st.IsDir()
}

// Root returns the game folder (the one holding data and data_mods) for a path the user gave:
// that folder, its data folder or data/sound.
func Root(path string) (string, error) {
	root := filepath.Clean(strings.Trim(strings.TrimSpace(path), `"`))
	switch strings.ToLower(filepath.Base(root)) {
	case "sound":
		root = filepath.Dir(filepath.Dir(root))
	case "data":
		root = filepath.Dir(root)
	}
	base := filepath.Join(root, "data", "sound")
	if st, err := os.Stat(base); err != nil || !st.IsDir() {
		return "", i18n.Errorf("%s が見つかりません (data フォルダがあるフォルダを指定してください)", "%s not found (choose the folder that holds the data folder)", base)
	}
	return root, nil
}

// Layers returns the sound folders of a game folder on this PC, mods first in the order LayeredFS
// searches them. mods selects which mods to use; nil means all, empty means none (omnimix copied
// into data).
func Layers(folder string, mods []string) ([]Layer, error) {
	root, err := Root(folder)
	if err != nil {
		return nil, err
	}
	return layersOf(os.DirFS(root), false, mods), nil
}

// Listing returns the sound folders of a game folder on the PC of a browser, from what it sent:
// every file of the sound folders by path in the game folder ("data/sound/01000.ifs"), with the
// start of each IFS up to the end of its manifest (other files: nothing). Its charts are located,
// not read (Song.Need).
func Listing(files map[string][]byte, mods []string) []Layer {
	game := fstest.MapFS{}
	for name, head := range files {
		if fs.ValidPath(name) {
			game[name] = &fstest.MapFile{Data: head}
		}
	}
	return layersOf(game, true, mods)
}

func layersOf(game fs.FS, deferred bool, mods []string) []Layer {
	var layers []Layer
	want := map[string]bool{}
	for _, m := range mods {
		want[m] = true
	}
	for _, m := range modsOf(game) {
		if mods == nil || want[m] {
			layers = append(layers, Layer{m, "data_mods/" + m + "/sound", game, deferred})
		}
	}
	return append(layers, Layer{"", "data/sound", game, deferred})
}

// Mods lists the mods in data_mods that contain a sound folder, alphabetically.
func Mods(root string) []string { return modsOf(os.DirFS(root)) }

func modsOf(game fs.FS) []string {
	entries, err := fs.ReadDir(game, "data_mods")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if st, err := fs.Stat(game, "data_mods/"+e.Name()+"/sound"); e.IsDir() && err == nil && st.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out) // LayeredFS search order
	return out
}

// Song is the result for one song.
type Song struct {
	ID     int64         `json:"id"`
	Source string        `json:"source"`           // where the chart came from, for the report
	Counts map[int]int64 `json:"counts,omitempty"` // chart index -> notes
	Err    string        `json:"error,omitempty"`  // why the chart could not be read, in Japanese
	ErrEn  string        `json:"error_en,omitempty"`
	Need   *NeedRead     `json:"need,omitempty"` // a Listing's chart, for the browser to read and count
}

var songName = regexp.MustCompile(`^(\d{5})(-p0)?(\.ifs|_ifs)?$`)

// Scan counts the notes of every song found in the layers.
func Scan(layers []Layer) []Song {
	ids := map[string]bool{}
	for _, l := range layers {
		entries, _ := fs.ReadDir(l.fsys, l.Dir)
		for _, e := range entries {
			if m := songName.FindStringSubmatch(e.Name()); m != nil && (m[3] != "" || e.IsDir()) {
				ids[m[1]] = true
			}
		}
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	var out []Song
	for _, id := range sorted {
		n, _ := strconv.ParseInt(id, 10, 64)
		s := Song{ID: n}
		data, source, err := chartOf(layers, id)
		s.Source = source
		if need := (*NeedRead)(nil); errors.As(err, &need) {
			s.Need = need
			out = append(out, s)
			continue
		}
		if err == nil && data == nil {
			continue // audio only (e.g. a preview) - no chart
		}
		if err == nil {
			s.Counts, err = NoteCounts(data)
		}
		if err != nil {
			s.Err, s.ErrEn = i18n.Text(err, "ja"), i18n.Text(err, "en")
		}
		out = append(out, s)
	}
	return out
}

// resolve finds a path relative to the sound folder in the first layer that has it: the layer,
// the path in the game folder and the layer's name ("" when none has it).
func resolve(layers []Layer, rel string) (Layer, string, string) {
	for _, l := range layers {
		if p := path.Join(l.Dir, rel); l.isFile(p) {
			name := l.Mod
			if name == "" {
				name = "data"
			}
			return l, p, name
		}
	}
	return Layer{}, "", ""
}

// ifsChart reads the chart out of an IFS, honouring <name>_ifs/<file> replacements in mods.
func ifsChart(layers []Layer, ifsName, id string, fs, super *IFS) ([]byte, string, error) {
	inner := fs.find(id)
	if inner == "" && super != nil {
		inner = super.find(id)
	}
	if inner == "" {
		return nil, "", nil
	}
	folder := strings.TrimSuffix(ifsName, ".ifs") + "_ifs/"
	for _, l := range layers {
		if l.Mod == "" {
			continue // replacements only come from mods
		}
		for _, rel := range []string{folder + inner, folder + inner[strings.LastIndex(inner, "/")+1:]} {
			if p := path.Join(l.Dir, rel); l.isFile(p) {
				b, err := l.readFile(p)
				return b, l.Mod + "/" + rel, err
			}
		}
	}
	if _, ok := fs.files[inner]; !ok && super != nil {
		b, err := super.read(inner, nil)
		return b, "", err
	}
	b, err := fs.read(inner, super)
	return b, "", err
}

func chartOf(layers []Layer, id string) ([]byte, string, error) {
	base, basePath, baseLayer := resolve(layers, id+".ifs")
	if p0l, p0Path, p0Layer := resolve(layers, id+"-p0.ifs"); p0Path != "" {
		p0, err := openIFS(p0l, p0Path)
		if err != nil {
			return nil, p0Layer + "/" + id + "-p0.ifs", err
		}
		var super *IFS
		usable := true
		if p0.superName != "" {
			if basePath != "" {
				super, err = openIFS(base, basePath)
			}
			// the game drops a patch whose base changed ("broken super: missmatch fingerprint")
			usable = super != nil && err == nil && (p0.superMD5 == nil || bytes.Equal(super.md5, p0.superMD5))
		}
		if usable {
			b, replaced, err := ifsChart(layers, id+"-p0.ifs", id, p0, super)
			if b != nil || err != nil {
				return b, sourceName(p0Layer+"/"+id+"-p0.ifs", replaced), err
			}
		}
	}
	if basePath != "" {
		fs, err := openIFS(base, basePath)
		if err != nil {
			return nil, baseLayer + "/" + id + ".ifs", err
		}
		b, replaced, err := ifsChart(layers, id+".ifs", id, fs, nil)
		if b != nil || err != nil {
			return b, sourceName(baseLayer+"/"+id+".ifs", replaced), err
		}
	}
	if l, p, layer := resolve(layers, id+"/"+id+".1"); p != "" {
		b, err := l.readFile(p)
		return b, layer + "/" + id + "/" + id + ".1", err
	}
	return nil, "", nil
}

func sourceName(ifs, replaced string) string {
	if replaced != "" {
		return ifs + " (" + replaced + ")" // a file replacing one inside the IFS
	}
	return ifs
}
