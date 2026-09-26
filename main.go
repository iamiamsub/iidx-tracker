// IIDX score tracker.
//
// Sits between the game and whatever e-amusement server it uses, relays every request unchanged
// and records what it sees: plays (music.reg), profiles (pc.get), the cabinet type (pc.save) and
// the server's bests (music.getrank). The only thing it rewrites is the service list in
// services.get, so that the game keeps talking through the tracker.
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"iidx-tracker/internal/i18n"
	"iidx-tracker/internal/sound"
	"iidx-tracker/internal/store"
)

//go:embed static
var embedded embed.FS

// version is set by build.ps1 (-X main.version=...).
var version = "dev"

// Like asphyxia-core: listen on every interface, so the game can be on this PC or on the LAN.
// language is the console's: "ja", "en", or empty for the system's (the web page picks its own).
func defaultConfig() map[string]any {
	return map[string]any{"listen": "0.0.0.0:8084", "upstream": "", "db_path": "data/tracker.db", "language": ""}
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func isAbs(p string) bool         { return filepath.IsAbs(p) }
func joinPath(a, b string) string { return filepath.Join(a, b) }
func dirOf(p string) string       { return filepath.Dir(p) }

func marshalIndent(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n"), err
}

func loadConfig(path string) (map[string]any, error) {
	config := defaultConfig()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return nil, err
	}
	var saved map[string]any
	if err := json.Unmarshal(b, &saved); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	for k, v := range saved {
		config[k] = v
	}
	return config, nil
}

// appDir is where config.json and data/ live: next to the executable.
func appDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(timestamped{os.Stdout})

	i18n.Lang = i18n.Pick("")
	dir := appDir()
	configPath := flag.String("config", filepath.Join(dir, "config.json"), i18n.L(
		"設定ファイル (既定: 実行ファイルと同じフォルダの config.json)",
		"settings file (default: config.json next to the executable)"))
	listen := flag.String("listen", "", i18n.L(
		"待ち受けアドレス 例: 0.0.0.0:8084 (既定, LAN からも接続可) / 127.0.0.1:8084 (この PC のみ)",
		"address to listen on, e.g. 0.0.0.0:8084 (default, reachable from the LAN) / 127.0.0.1:8084 (this PC only)"))
	upstream := flag.String("upstream", "", i18n.L(
		"本来の接続先サーバー 例: http://10.0.0.5:8083",
		"the server the game connects to, e.g. http://10.0.0.5:8083"))
	dbPath := flag.String("db", "", i18n.L("データベースの保存先", "where to keep the database"))
	flag.Parse()

	_, statErr := os.Stat(*configPath)
	firstRun := errors.Is(statErr, fs.ErrNotExist)
	config, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf(i18n.L("設定ファイルを読めません: %v", "cannot read the settings file: %v"), err)
	}
	i18n.Lang = i18n.Pick(str(config["language"]))
	for key, v := range map[string]string{"listen": *listen, "upstream": *upstream, "db_path": *dbPath} {
		if v != "" {
			config[key] = v
		}
	}
	static, _ := fs.Sub(embedded, "static")
	app, err := newApp(*configPath, config, dir, static)
	if err != nil {
		log.Fatalf(i18n.L("起動できません: %v", "cannot start: %v"), err)
	}
	if firstRun {
		if err := app.saveConfig(); err != nil {
			log.Printf(i18n.L("設定ファイルを保存できません: %v", "cannot save the settings file: %v"), err)
		}
	}

	addr := str(config["listen"])
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		log.Fatalf(i18n.L("listen の値が不正です: %s", "invalid listen address: %s"), addr)
	}
	if host == "" {
		host = "0.0.0.0"
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort(host, port))
	if err != nil {
		log.Fatalf(i18n.L("%s で待ち受けできません: %v", "cannot listen on %s: %v"), addr, err)
	}
	everywhere := host == "0.0.0.0"
	shown := host
	if everywhere {
		shown = "127.0.0.1"
	}
	log.Printf("IIDX Tracker %s", version)
	log.Printf(i18n.L("Web UI        : http://%s:%s/ui/", "Web UI          : http://%s:%s/ui/"), shown, port)
	log.Printf(i18n.L("ゲームの接続先: <services>http://%s:%s</services>  (ea3-config / spice -url)",
		"Game's server   : <services>http://%s:%s</services>  (ea3-config / spice -url)"), shown, port)
	if everywhere {
		for _, ip := range localAddresses() {
			log.Printf(i18n.L("  別の PC から: http://%s:%s", "  from other PCs: http://%s:%s"), ip, port)
		}
	}
	up := str(config["upstream"])
	if up == "" {
		up = i18n.L("(未設定 - Web UI の設定で指定してください)", "(not set - set it in the Web UI settings)")
	}
	log.Printf(i18n.L("中継先        : %s", "Upstream        : %s"), up)
	log.Printf(i18n.L("データベース  : %s", "Database        : %s"), app.dbPath)
	srv := &http.Server{Handler: app, ReadHeaderTimeout: 30 * time.Second}
	log.Fatal(srv.Serve(ln))
}

// timestamped prefixes every log line with [HH:MM:SS] like the Python tracker.
type timestamped struct{ w io.Writer }

func (t timestamped) Write(p []byte) (int, error) {
	_, err := fmt.Fprintf(t.w, "[%s] %s", time.Now().Format("15:04:05"), p)
	return len(p), err
}

// ---- web API -----------------------------------------------------------------------

type apiFunc func(a *App, q url.Values, body []byte) (any, error)

var apiRoutes = map[string]apiFunc{
	"GET /api/status":     apiStatus,
	"POST /api/settings":  apiSettings,
	"GET /api/players":    func(a *App, _ url.Values, _ []byte) (any, error) { return a.store.Players() },
	"GET /api/player":     apiPlayer,
	"GET /api/songs":      apiSongs,
	"GET /api/chart":      apiChart,
	"GET /api/play":       apiPlay,
	"GET /api/recent":     apiRecent,
	"GET /api/categories": func(a *App, q url.Values, _ []byte) (any, error) { return a.store.Categories(dbParam(a, q)) },
	"GET /api/context":    apiContext,
	"GET /api/machines": func(a *App, _ url.Values, _ []byte) (any, error) {
		return a.store.Rows("SELECT m.pcbid, m.musicdb_id, d.name AS musicdb, m.filename, m.altfix, m.game, m.remote, m.booted_at" +
			" FROM machines m LEFT JOIN musicdbs d ON d.id = m.musicdb_id ORDER BY m.booted_at DESC")
	},
	"POST /api/categories/create": apiCategoryCreate,
	"POST /api/categories/update": apiCategoryUpdate,
	"GET /api/musicdbs":           apiMusicDBs,
	"POST /api/musicdb/upload":    apiMusicDBUpload,
	"POST /api/musicdb/update":    apiMusicDBUpdate,
	"POST /api/notes/upload":      apiNotesUpload,
	"GET /api/sound/inspect":      apiSoundInspect,
	"POST /api/sound/scan":        apiSoundScan,
}

// apiSoundInspect checks a game folder and lists its mods (data_mods, LayeredFS order).
func apiSoundInspect(_ *App, q url.Values, _ []byte) (any, error) {
	root, err := sound.Root(qget(q, "path"))
	if err != nil {
		return nil, userError(err)
	}
	mods := sound.Mods(root)
	if mods == nil {
		mods = []string{}
	}
	return map[string]any{"root": root, "mods": mods}, nil
}

// apiSoundScan reads every chart of a game folder and stores the note counts for one music
// database, which the request must name.
func apiSoundScan(a *App, _ url.Values, raw []byte) (any, error) {
	b, err := jsonBody(raw)
	if err != nil {
		return nil, err
	}
	if b["musicdb_id"] == nil {
		return nil, i18n.New("紐付ける曲DBを選んでください", "Choose the music database to link")
	}
	dbID, err := needInt(b["musicdb_id"], "musicdb_id")
	if err != nil {
		return nil, err
	}
	var mods []string // nil = every mod in data_mods
	if list, ok := b["mods"].([]any); ok {
		mods = []string{}
		for _, m := range list {
			mods = append(mods, str(m))
		}
	}
	root, err := sound.Root(str(b["path"]))
	if err != nil {
		return nil, userError(err)
	}
	layers, err := sound.Layers(root, mods)
	if err != nil {
		return nil, userError(err)
	}
	start := time.Now()
	songs := sound.Scan(layers)
	used := []string{}
	for _, l := range layers {
		if l.Mod != "" {
			used = append(used, l.Mod)
		}
	}
	out, err := a.store.ImportScan(dbID, root, used, songs)
	if err != nil {
		return nil, err
	}
	out["seconds"] = time.Since(start).Seconds()
	out["mods"] = used
	return out, nil
}

func (a *App) api(w http.ResponseWriter, r *http.Request) {
	route, ok := apiRoutes[r.Method+" "+r.URL.Path]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	var body []byte
	if r.Method == http.MethodPost {
		body, _ = io.ReadAll(r.Body)
	}
	out, err := route(a, r.URL.Query(), body)
	var userErr store.UserError
	lang := r.Header.Get("X-Lang") // the page's language
	if lang == "" {
		lang = i18n.Lang
	}
	switch {
	case errors.As(err, &userErr):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": i18n.Text(err, lang)})
	case err != nil:
		log.Printf(i18n.L("API エラー: %s: %v", "API error: %s: %v"), r.URL.Path, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, out)
	}
}

// userError answers err as a mistake in the request (400), in both languages when it has them.
func userError(err error) error {
	var m i18n.Msg
	if errors.As(err, &m) {
		return m
	}
	return i18n.New(err.Error(), err.Error())
}

func jsonBody(raw []byte) (map[string]any, error) {
	out := map[string]any{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, i18n.New("JSON として読めません", "Not valid JSON")
	}
	return out, nil
}

func needInt(v any, name string) (int64, error) {
	switch x := v.(type) {
	case float64:
		return int64(x), nil
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			return n, nil
		}
	}
	return 0, i18n.Errorf("%s が不正です", "Invalid %s", name)
}

// qget returns the last value of a query parameter, like the Python handler.
func qget(q url.Values, key string) string {
	if v := q[key]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

func apiStatus(a *App, _ url.Values, _ []byte) (any, error) {
	counts := map[string]any{}
	for _, t := range []string{"plays", "cards", "sessions", "chart_notes"} {
		r, err := a.store.Row("SELECT COUNT(*) AS n FROM " + t)
		if err != nil {
			return nil, err
		}
		counts[t] = r["n"]
	}
	r, err := a.store.Row("SELECT COUNT(DISTINCT music_id) AS n FROM songs")
	if err != nil {
		return nil, err
	}
	counts["songs"] = r["n"]
	_, _, upOK := a.upstream()
	a.mu.Lock()
	defer a.mu.Unlock()
	config, stats := map[string]any{}, map[string]any{}
	for k, v := range a.config {
		config[k] = v
	}
	for k, v := range a.stats {
		stats[k] = v
	}
	return map[string]any{"config": config, "db_path": a.dbPath, "config_path": a.configPath,
		"upstream_ok": upOK, "stats": stats, "addresses": localAddresses(), "counts": counts}, nil
}

func apiSettings(a *App, _ url.Values, raw []byte) (any, error) {
	b, err := jsonBody(raw)
	if err != nil {
		return nil, err
	}
	restart := false
	a.mu.Lock()
	if v, ok := b["upstream"]; ok {
		up := strings.TrimSpace(str(v))
		if u, err := url.Parse(up); up != "" && (err != nil || (u.Scheme != "http" && u.Scheme != "https")) {
			a.mu.Unlock()
			return nil, i18n.New("upstream は http:// で始まる URL を指定してください", "upstream must be a URL starting with http://")
		}
		a.config["upstream"] = up
	}
	for _, key := range []string{"listen", "db_path"} {
		if v, ok := b[key]; ok {
			if s := strings.TrimSpace(str(v)); s != "" && s != str(a.config[key]) {
				a.config[key] = s
				restart = true
			}
		}
	}
	a.mu.Unlock()
	if err := a.saveConfig(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]any{"ok": true, "restart": restart, "config": a.config}, nil
}

// dbParam is the music database a view is about: ?db=<id>, else the player's default.
func dbParam(a *App, q url.Values) any {
	if id, err := strconv.ParseInt(qget(q, "db"), 10, 64); err == nil {
		return id
	}
	return a.store.DefaultDB(qget(q, "key"))
}

// apiContext tells the UI which music database to show a player by default.
func apiContext(a *App, q url.Values, _ []byte) (any, error) {
	return map[string]any{"db": a.store.DefaultDB(qget(q, "key"))}, nil
}

func apiPlayer(a *App, q url.Values, _ []byte) (any, error) {
	key := qget(q, "key")
	db := dbParam(a, q)
	data, err := a.store.Player(key)
	if err != nil {
		return nil, err
	}
	data["db"] = db
	// level -> count per clear lamp (index = cflg, 0 = not played yet)
	lamps, djpoint := map[string]any{}, map[string]any{}
	for _, style := range []string{"SP", "DP"} {
		rows, err := a.store.ChartRows(key, style, 0, "", "", db)
		if err != nil {
			return nil, err
		}
		perLevel := map[int64][]int64{}
		for _, r := range rows {
			lv := r["level"].(int64)
			if perLevel[lv] == nil {
				perLevel[lv] = make([]int64, 8)
			}
			clear, _ := r["best_clear"].(int64)
			if clear >= 0 && clear < 8 {
				perLevel[lv][clear]++
			}
		}
		lamps[style] = perLevel
		djpoint[style] = store.DjPointTotal(rows)
	}
	data["lamps"], data["djpoint"] = lamps, djpoint
	if data["chatter"], err = a.store.ChatterSummary(key, 100); err != nil {
		return nil, err
	}
	return data, nil
}

func apiSongs(a *App, q url.Values, _ []byte) (any, error) {
	var level int64
	if l := qget(q, "level"); l != "" {
		var err error
		if level, err = needInt(l, "level"); err != nil {
			return nil, err
		}
	}
	return a.store.ChartRows(qget(q, "key"), qget(q, "style"), level, qget(q, "category"), qget(q, "q"), dbParam(a, q))
}

func apiChart(a *App, q url.Values, _ []byte) (any, error) {
	music, err := needInt(qget(q, "music"), "music")
	if err != nil {
		return nil, err
	}
	chart, err := needInt(qget(q, "chart"), "chart")
	if err != nil {
		return nil, err
	}
	return a.store.Chart(qget(q, "key"), dbParam(a, q), music, chart)
}

func apiPlay(a *App, q url.Values, _ []byte) (any, error) {
	id, err := needInt(qget(q, "id"), "id")
	if err != nil {
		return nil, err
	}
	return a.store.PlayGraphs(id)
}

func apiRecent(a *App, _ url.Values, _ []byte) (any, error) {
	return a.store.PlaysFor("1", nil, "ORDER BY played_at DESC LIMIT 30")
}

func apiCategoryCreate(a *App, _ url.Values, raw []byte) (any, error) {
	b, err := jsonBody(raw)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(str(b["name"]))
	if name == "" {
		return nil, i18n.New("カテゴリ名を入力してください", "Enter a category name")
	}
	color := str(b["color"])
	if color == "" {
		color = "#6aa9ff"
	}
	id, err := a.store.Exec("INSERT INTO categories (name, color, created_at) VALUES (?, ?, ?)", name, color, time.Now().Unix())
	return map[string]any{"id": id}, err
}

func apiCategoryUpdate(a *App, _ url.Values, raw []byte) (any, error) {
	b, err := jsonBody(raw)
	if err != nil {
		return nil, err
	}
	cid, err := needInt(b["id"], "id")
	if err != nil {
		return nil, err
	}
	if r, err := a.store.Row("SELECT 1 FROM categories WHERE id = ?", cid); err != nil || r == nil {
		if err != nil {
			return nil, err
		}
		return nil, i18n.New("カテゴリが存在しません", "No such category")
	}
	ok := map[string]any{"ok": true}
	if truthy(b["delete"]) {
		if _, err := a.store.Exec("DELETE FROM category_songs WHERE category_id = ?", cid); err != nil {
			return nil, err
		}
		_, err := a.store.Exec("DELETE FROM categories WHERE id = ?", cid)
		return ok, err
	}
	_, hasName := b["name"]
	_, hasColor := b["color"]
	if hasName || hasColor {
		var name, color any
		if n := strings.TrimSpace(str(b["name"])); n != "" {
			name = n
		}
		if c, isStr := b["color"].(string); isStr {
			color = c
		}
		if _, err := a.store.Exec("UPDATE categories SET name = COALESCE(?, name), color = COALESCE(?, color) WHERE id = ?",
			name, color, cid); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"add", "remove"} {
		list, _ := b[key].([]any)
		for _, m := range list {
			mid, err := needInt(m, "music")
			if err != nil {
				return nil, err
			}
			q := "INSERT OR IGNORE INTO category_songs VALUES (?, ?)"
			if key == "remove" {
				q = "DELETE FROM category_songs WHERE category_id = ? AND music_id = ?"
			}
			if _, err := a.store.Exec(q, cid, mid); err != nil {
				return nil, err
			}
		}
	}
	return ok, nil
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return true
}

func apiMusicDBs(a *App, _ url.Values, _ []byte) (any, error) {
	dbs, err := a.store.Rows("SELECT d.*, (SELECT COUNT(*) FROM songs WHERE musicdb_id = d.id AND removed = 0) " +
		"AS songs FROM musicdbs d ORDER BY d.id")
	if err != nil {
		return nil, err
	}
	for _, d := range dbs {
		if d["imports"], err = a.store.Rows("SELECT * FROM musicdb_imports WHERE musicdb_id = ? ORDER BY id DESC", d["id"]); err != nil {
			return nil, err
		}
		if d["scans"], err = a.store.Rows("SELECT id, root, mods, songs, charts, skipped, errors, scanned_at "+
			"FROM sound_scans WHERE musicdb_id = ? ORDER BY id DESC LIMIT 5", d["id"]); err != nil {
			return nil, err
		}
	}
	return dbs, nil
}

func apiMusicDBUpload(a *App, q url.Values, raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, i18n.New("ファイルが空です", "The file is empty")
	}
	name := qget(q, "filename")
	if name == "" {
		name = "music_data.bin"
	}
	target := qget(q, "target")
	if target == "" {
		target = "auto"
	}
	return a.store.ImportMusicDB(name, raw, target, qget(q, "name"))
}

func apiMusicDBUpdate(a *App, _ url.Values, raw []byte) (any, error) {
	b, err := jsonBody(raw)
	if err != nil {
		return nil, err
	}
	id, err := needInt(b["id"], "id")
	if err != nil {
		return nil, err
	}
	defer a.store.ForgetSongs()
	if truthy(b["delete"]) {
		for _, q := range []string{"DELETE FROM songs WHERE musicdb_id = ?", "DELETE FROM musicdb_imports WHERE musicdb_id = ?",
			"DELETE FROM chart_notes_db WHERE musicdb_id = ?", "DELETE FROM sound_scans WHERE musicdb_id = ?",
			"DELETE FROM musicdbs WHERE id = ?"} {
			if _, err := a.store.Exec(q, id); err != nil {
				return nil, err
			}
		}
		return map[string]any{"ok": true}, nil
	}
	if name := strings.TrimSpace(str(b["name"])); name != "" {
		if _, err := a.store.Exec("UPDATE musicdbs SET name = ? WHERE id = ?", name, id); err != nil {
			return nil, err
		}
	}
	if kind := str(b["kind"]); kind == "omni" || kind == "vanilla" {
		if _, err := a.store.Exec("UPDATE musicdbs SET kind = ? WHERE id = ?", kind, id); err != nil {
			return nil, err
		}
	}
	return map[string]any{"ok": true}, nil
}

func apiNotesUpload(a *App, q url.Values, raw []byte) (any, error) {
	db, err := needInt(qget(q, "db"), "db")
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")) // utf-8-sig
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, i18n.New("JSON として読めません", "Not valid JSON")
	}
	counts, ok := data.(map[string]any)
	if !ok {
		return nil, i18n.New(`{曲ID: {"SPA": ノーツ数, ...}} 形式の JSON を指定してください`,
			`Give JSON shaped like {music ID: {"SPA": note count, ...}}`)
	}
	n, err := a.store.ImportNotes(db, counts)
	return map[string]any{"imported": n}, err
}
