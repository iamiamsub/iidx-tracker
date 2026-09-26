package store

// Game rules and file formats taken from bm2dx 2026081900 (addresses are that build's).

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unicode/utf16"

	"iidx-tracker/internal/i18n"

	"golang.org/x/text/encoding/japanese"
)

// ---- formulas ----------------------------------------------------------------

// GhostBucketSizes returns the notes in each of the 64 ghost buckets. CGraphData::GetBucket
// (0x18081dc40) puts the k-th judged note (1-based) into ceil(k*64/notes + 0.0001) - 1,
// clamped to 0..63, in single precision; the ghost byte of a bucket is the EX earned there,
// so its maximum is 2 * size.
func GhostBucketSizes(notes int) []int {
	sizes := make([]int, 64)
	eps := float64(float32(0.0001))
	for k := 1; k <= notes; k++ {
		a := float64(float32(float64(k*64) / float64(notes)))
		b := int(math.Ceil(float64(float32(a+eps)))) - 1
		sizes[min(63, max(0, b))]++
	}
	return sizes
}

// DjPoint_Calc (0x18082d030): EX * (100 + lamp bonus + DJ LEVEL bonus), kept x10000.
var (
	lampBonus = [8]int64{0, 0, 0, 5, 10, 20, 25, 30} // NO PLAY, FAILED, ASSIST, EASY, CLEAR, HARD, EX-HARD, FC
	rankBonus = [8]int64{20, 15, 10, 0, 0, 0, 0, 0}  // AAA, AA, A, B ... F
)

// DjLevel is 0 = AAA ... 7 = F; the borders are ceil((8 - i) * 2 * notes / 9).
func DjLevel(ex, notes int64) int64 {
	for i := int64(0); i < 7; i++ {
		if ex*9 >= (8-i)*2*notes {
			return i
		}
	}
	return 7
}

// DjPoint is x10000 like the game; nil when a part is unknown.
func DjPoint(ex, clear, level *int64) *int64 {
	if ex == nil || clear == nil || level == nil || *clear < 0 || *clear >= 8 || *level < 0 || *level >= 8 {
		return nil
	}
	v := int64(0)
	if *ex > 0 {
		v = *ex * (100 + lampBonus[*clear] + rankBonus[*level])
	}
	return &v
}

// ---- options -----------------------------------------------------------------
//
// The play option bitfield (music.reg opt/opt2, music_play_log option1/option2, pc.get/pc.save
// sp_opt/dp_opt/dp_opt2). FUN_1805c3db0 packs the per-player COptionGameData: every enumerated
// setting sets exactly one bit and every flag is a single bit. For DP the first value is the
// left side and the second the right side; SP sends 0 as the second value.

var (
	classicHS = []string{"1.00", "1.50", "2.00", "2.25", "2.50", "2.75", "3.00", "3.25", "3.50", "3.75", "4.00"}
	gauges    = []struct {
		bit  int
		name string
	}{{35, "A-EASY"}, {36, "EASY"}, {37, "HARD"}, {38, "EX-HARD"}}
	randoms = []struct {
		bit  int
		name string
	}{{39, "RANDOM"}, {40, "R-RANDOM"}, {41, "S-RANDOM"}}
	shortRandom = map[string]string{"RANDOM": "RAN", "R-RANDOM": "R-RAN", "S-RANDOM": "S-RAN"}
	// music_play_log gauge_type: numbered like the clear lamp the gauge would earn
	gaugeTypes = map[int64]string{2: "A-EASY", 3: "EASY", 4: "NORMAL", 5: "HARD", 6: "EX-HARD"}
	// Modes names music_play_log mode_type
	Modes = map[int64]string{0: "STANDARD", 3: "段位認定", 5: "STEP UP", 6: "PREMIUM FREE", 7: "BATTLE", 8: "BATTLE"}
)

type options struct {
	hs, classicHS                                                                    int // -1 = not set
	hidden, hiddenPlus, sudden, suddenPlus, mirror, ascr, flip, battle, lift, legacy bool
	gauge, random                                                                    string
}

func unpackOptions(p *int64) options {
	var v uint64
	if p != nil {
		v = uint64(*p)
	}
	bit := func(n int) bool { return v>>n&1 != 0 }
	onehot := func(lo, hi int) int {
		for i := lo; i <= hi; i++ {
			if bit(i) {
				return i - lo
			}
		}
		return -1
	}
	o := options{hs: onehot(0, 19), classicHS: onehot(20, 30),
		hidden: bit(31), hiddenPlus: bit(32), sudden: bit(33), suddenPlus: bit(34),
		mirror: bit(42), ascr: bit(43), flip: bit(45), battle: bit(46), lift: bit(49), legacy: bit(50),
		gauge: "NORMAL"}
	for _, g := range gauges {
		if bit(g.bit) {
			o.gauge = g.name
			break
		}
	}
	for _, r := range randoms {
		if bit(r.bit) {
			o.random = r.name
			break
		}
	}
	return o
}

// laneCover follows the precedence of the game's option indicator (FUN_18080ee90).
func laneCover(o options) string {
	switch {
	case o.hiddenPlus && o.suddenPlus:
		return "SUD+ & HID+"
	case o.lift && o.suddenPlus:
		return "LIFT & SUD+"
	case o.hiddenPlus:
		return "HID+"
	case o.suddenPlus:
		return "SUD+"
	case o.lift:
		return "LIFT"
	case o.sudden && o.hidden:
		return "SUDDEN & HIDDEN"
	case o.sudden:
		return "SUDDEN"
	case o.hidden:
		return "HIDDEN"
	}
	return ""
}

// dpStyle: SYNC/SYMM-RANDOM are not bits of their own but BATTLE with RANDOM on both sides and
// MIRROR on both (SYMM) or on exactly one (SYNC). FUN_180897e60/f60.
func dpStyle(left, right options) string {
	if left.battle && left.random == "RANDOM" && right.random == "RANDOM" {
		if left.mirror && right.mirror {
			return "SYMM-RAN"
		}
		if left.mirror != right.mirror {
			return "SYNC-RAN"
		}
	}
	side := func(o options) string {
		if s := shortRandom[o.random]; s != "" {
			return s
		}
		if o.mirror {
			return "MIR"
		}
		return "OFF"
	}
	if text := side(left) + "/" + side(right); text != "OFF/OFF" {
		return text
	}
	return ""
}

// DescribeOptions summarises the options in the game's order: style, gauge, lane cover, assist.
func DescribeOptions(option1, option2, playStyle *int64) string {
	left := unpackOptions(option1)
	var parts []string
	if playStyle != nil && *playStyle == 1 {
		right := unpackOptions(option2)
		if left.battle {
			parts = append(parts, "BATTLE")
		}
		if s := dpStyle(left, right); s != "" {
			parts = append(parts, s)
		}
		if left.flip {
			parts = append(parts, "FLIP")
		}
	} else {
		if left.random != "" {
			parts = append(parts, left.random)
		}
		if left.mirror {
			parts = append(parts, "MIRROR")
		}
	}
	if left.gauge != "NORMAL" {
		parts = append(parts, left.gauge)
	}
	if c := laneCover(left); c != "" {
		parts = append(parts, c)
	}
	if left.ascr {
		parts = append(parts, "A-SCR")
	}
	if left.legacy {
		parts = append(parts, "LEGACY")
	}
	if len(parts) == 0 {
		return "OFF"
	}
	return strings.Join(parts, ", ")
}

// GaugeName names music_play_log gauge_type; nil when unknown.
func GaugeName(gaugeType, modeType *int64) any {
	if gaugeType == nil {
		return nil
	}
	if modeType != nil && *modeType == 3 && (*gaugeType == 0 || *gaugeType == 1) {
		if *gaugeType == 1 {
			return "段位 (EX-HARD)"
		}
		return "段位"
	}
	if s, ok := gaugeTypes[*gaugeType]; ok {
		return s
	}
	return fmt.Sprintf("gauge %d", *gaugeType)
}

// HiSpeed describes the hi-speed settings; nil when neither is set.
func HiSpeed(option1 *int64) any {
	o := unpackOptions(option1)
	var parts []string
	if o.hs >= 0 {
		parts = append(parts, fmt.Sprintf("HS %d", o.hs+1))
	}
	if o.classicHS >= 0 {
		parts = append(parts, "CLASSIC ×"+classicHS[o.classicHS])
	}
	if len(parts) == 0 {
		return nil
	}
	return strings.Join(parts, " / ")
}

// ---- music_data.bin ------------------------------------------------------------

// ChartNames indexes charts: 0..4 SP, 5..9 DP.
var ChartNames = []string{"SPB", "SPN", "SPH", "SPA", "SPL", "DPB", "DPN", "DPH", "DPA", "DPL"}

var VersionNames = map[int64]string{
	0: "1st style", 1: "substream", 2: "2nd style", 3: "3rd style", 4: "4th style",
	5: "5th style", 6: "6th style", 7: "7th style", 8: "8th style", 9: "9th style",
	10: "10th style", 11: "IIDX RED", 12: "HAPPY SKY", 13: "DistorteD", 14: "GOLD",
	15: "DJ TROOPERS", 16: "EMPRESS", 17: "SIRIUS", 18: "Resort Anthem", 19: "Lincle",
	20: "tricoro", 21: "SPADA", 22: "PENDUAL", 23: "copula", 24: "SINOBUZ",
	25: "CANNON BALLERS", 26: "Rootage", 27: "HEROIC VERSE", 28: "BISTROVER",
	29: "CastHour", 30: "RESIDENT", 31: "EPOLIS", 32: "Pinky Crush",
	33: "Sparkle Shower", 80: "INFINITAS",
}

type field struct {
	name      string
	off, size int
	utf16     bool
}

type layout struct {
	size                    int
	strings                 []field
	version, levels, songID int
}

var (
	wide = layout{0x7F8, []field{ // 32 and later: UTF-16 strings, subtitle added
		{"title", 0x000, 0x100, true}, {"title_ascii", 0x100, 0x40, false}, {"genre", 0x140, 0x80, true},
		{"artist", 0x1C0, 0x100, true}, {"subtitle", 0x2C0, 0x100, true}}, 0x3DC, 0x3EC, 0x67C}
	narrow = layout{0x52C, []field{ // 27 - 31: Shift-JIS strings
		{"title", 0x000, 0x40, false}, {"title_ascii", 0x040, 0x40, false},
		{"genre", 0x080, 0x40, false}, {"artist", 0x0C0, 0x40, false}}, 0x118, 0x120, 0x3B0}
)

// Song is one entry of music_data.bin.
type Song struct {
	ID, Version int64
	Text        map[string]string // title, title_ascii, genre, artist, subtitle
	Levels      []int64           // per chart, see ChartNames
}

// MusicData is a parsed music_data.bin. Sealed: altfix's music_omni.bin, whose header cannot be
// read (see findSongTable).
type MusicData struct {
	Version int64
	Sealed  bool
	Songs   []Song
}

func text(raw []byte, wide bool) string {
	var s string
	if wide {
		u := make([]uint16, len(raw)/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(raw[2*i:])
		}
		s = string(utf16.Decode(u))
	} else {
		b, _ := japanese.ShiftJIS.NewDecoder().Bytes(raw)
		s = string(b)
	}
	s, _, _ = strings.Cut(s, "\x00")
	return strings.TrimSpace(strings.ReplaceAll(s, "�", "")) // Python decodes with errors="ignore"
}

// ParseMusicData reads music_data.bin / music_omni.bin (IIDX 27 and later).
func ParseMusicData(data []byte) (*MusicData, error) {
	var version int64
	var count, start int
	var l layout
	sealed := len(data) < 16 || !bytes.Equal(data[:4], []byte("IIDX"))
	if sealed {
		var ok bool
		if count, start, ok = findSongTable(data); !ok {
			return nil, i18n.New("music_data.bin ではありません (IIDX ヘッダ無し)", "not a music_data.bin (no IIDX header)")
		}
		l = wide
	} else {
		version = int64(binary.LittleEndian.Uint32(data[4:]))
		if version < 27 || version == 80 {
			return nil, i18n.Errorf("データバージョン %d は未対応です (27 以降のみ)", "data version %d is not supported (27 and later only)", version)
		}
		var slots, indexSize int
		if version >= 32 {
			count, slots = int(binary.LittleEndian.Uint16(data[8:])), int(binary.LittleEndian.Uint32(data[12:]))
			l, indexSize = wide, 4
		} else {
			count, slots = int(binary.LittleEndian.Uint16(data[8:])), int(binary.LittleEndian.Uint32(data[10:]))
			l, indexSize = narrow, 2
		}
		start = 16 + slots*indexSize
		if start+count*l.size != len(data) {
			return nil, i18n.New("ファイルサイズが想定と合いません (破損または未知の形式)", "the file size does not match (broken or unknown format)")
		}
	}
	md := &MusicData{Version: version, Sealed: sealed}
	for n := 0; n < count; n++ {
		e := data[start+n*l.size : start+(n+1)*l.size]
		s := Song{
			ID:      int64(binary.LittleEndian.Uint32(e[l.songID:])),
			Version: int64(binary.LittleEndian.Uint16(e[l.version:])),
			Text:    map[string]string{"subtitle": ""},
		}
		for _, f := range l.strings {
			s.Text[f.name] = text(e[f.off:f.off+f.size], f.utf16)
		}
		for _, v := range e[l.levels : l.levels+10] {
			s.Levels = append(s.Levels, int64(v))
		}
		md.Songs = append(md.Songs, s)
		// without a header, the file is as new as its newest arcade song
		if sealed && s.Version < 80 && s.Version > md.Version {
			md.Version = s.Version
		}
	}
	if sealed && md.Version < 32 {
		return nil, i18n.New("music_data.bin ではありません (IIDX ヘッダ無し)", "not a music_data.bin (no IIDX header)")
	}
	return md, nil
}

// findSongTable finds the song table of a music data file whose header cannot be read (altfix's
// music_omni.bin; IIDX 32 and later), from the rest of the file: the entries fill its end, and the
// index before them holds, at each song ID, that song's entry number. Only the true entry count
// makes the two agree. It returns the entry count and where the entries start.
func findSongTable(data []byte) (count, start int, ok bool) {
	for c := 1; 16+c*wide.size <= len(data); c++ {
		start := len(data) - c*wide.size
		if (start-16)%4 != 0 {
			continue
		}
		slots := (start - 16) / 4
		match := true
		for n := 0; n < c && match; n++ {
			id := int(binary.LittleEndian.Uint32(data[start+n*wide.size+wide.songID:]))
			match = id < slots && int32(binary.LittleEndian.Uint32(data[16+id*4:])) == int32(n)
		}
		if match {
			return c, start, true
		}
	}
	return 0, 0, false
}
