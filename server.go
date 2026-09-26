package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"iidx-tracker/internal/eamuse"
	"iidx-tracker/internal/i18n"
	"iidx-tracker/internal/store"
)

// Headers that describe one hop and must not be copied across the proxy.
var hopHeaders = map[string]bool{"connection": true, "keep-alive": true, "proxy-connection": true,
	"transfer-encoding": true, "te": true, "trailer": true, "upgrade": true, "host": true, "content-length": true}

// Requests worth recording; everything else is only relayed. Responses are decoded only
// where the answer is what gets recorded.
var (
	recordedCalls = map[string]bool{"pc.get": true, "music.reg": true, "pc.save": true, "music.getrank": true}
	withResponse  = map[string]bool{"get": true, "getrank": true}
)

// Same rule as altfix's server guard: only literal private addresses count.
var privateNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"169.254.0.0/16", "0.0.0.0/8", "::1/128", "fe80::/10", "fc00::/7"} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

func isPrivateHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false // a name cannot be classified without resolving it
	}
	for _, n := range privateNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// serviceHost is the host:port to put into the rewritten service list. The game must reach us
// again there, and the address it just used certainly works: the local end of its connection is
// 127.0.0.1 when it runs on this PC and this PC's LAN address when it runs on another one.
// advertise_host in config.json ("name" or "name:port") overrides it, e.g. behind port forwarding.
func serviceHost(ip string, port int, override string) string {
	if override != "" {
		if strings.Contains(override, ":") {
			return override
		}
		return fmt.Sprintf("%s:%d", override, port)
	}
	ip = strings.TrimPrefix(ip, "::ffff:") // IPv4 client on a dual-stack socket
	if strings.Contains(ip, ":") {
		return fmt.Sprintf("[%s]:%d", ip, port)
	}
	return fmt.Sprintf("%s:%d", ip, port)
}

// localAddresses lists this PC's IPv4 addresses except loopback and link-local (what the game should use).
func localAddresses() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return []string{}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.To4() == nil || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		if s := ipn.IP.String(); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func isOmni(model string) bool {
	parts := strings.Split(model, ":")
	return len(parts) > 3 && parts[3] == "S"
}

func logf(format string, args ...any) { log.Printf(format, args...) }

type pending struct {
	call                *store.Call
	respBody            []byte
	respInfo, respCompr string
}

// App is the running tracker: configuration, database, the recording queue and counters.
type App struct {
	configPath, dbPath string
	store              *store.Store
	queue              chan pending
	client             *http.Client
	static             fs.FS

	mu     sync.Mutex
	config map[string]any
	stats  map[string]any
}

func newApp(configPath string, config map[string]any, appDir string, static fs.FS) (*App, error) {
	dbPath := str(config["db_path"])
	if !isAbs(dbPath) {
		dbPath = joinPath(appDir, dbPath)
	}
	if err := os.MkdirAll(dirOf(dbPath), 0o755); err != nil {
		return nil, err
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	app := &App{
		configPath: configPath, dbPath: dbPath, store: st, config: config, static: static,
		queue: make(chan pending, 1024),
		client: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
			Proxy:              nil,  // talk to the server directly, like the game would
			DisableCompression: true, // no gzip: the body must stay readable for recording
			TLSClientConfig:    &tls.Config{},
		}},
		stats: map[string]any{"started": time.Now().Unix(), "requests": 0, "recorded": 0, "refused": 0,
			"errors": 0, "last_request": nil, "last_error": nil},
	}
	go app.worker()
	return app, nil
}

func (a *App) bump(key string, set map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if key != "" {
		a.stats[key] = a.stats[key].(int) + 1
	}
	for k, v := range set {
		a.stats[k] = v
	}
}

func (a *App) conf(key string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return str(a.config[key])
}

func (a *App) saveConfig() error {
	a.mu.Lock()
	b, err := marshalIndent(a.config)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(a.configPath, b, 0o644)
}

// upstream returns the origin and base path of the configured server.
func (a *App) upstream() (origin, base string, ok bool) {
	u, err := url.Parse(a.conf("upstream"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "", false
	}
	return u.Scheme + "://" + u.Host, strings.TrimRight(u.EscapedPath(), "/"), true
}

func (a *App) worker() {
	for p := range a.queue {
		func() {
			defer func() {
				if r := recover(); r != nil {
					a.fail(p.call, fmt.Errorf("%v", r))
				}
			}()
			c := p.call
			if p.respBody != nil && withResponse[c.Method] {
				doc, _, err := eamuse.Decode(p.respBody, p.respInfo, p.respCompr)
				if err != nil {
					a.fail(c, err)
					return
				}
				if len(doc.Children) > 0 {
					c.Resp = doc.Children[0]
				}
			}
			if err := a.store.Ingest(c); err != nil {
				a.fail(c, err)
				return
			}
			a.bump("recorded", nil)
		}()
	}
}

func (a *App) fail(c *store.Call, err error) {
	a.bump("errors", map[string]any{"last_error": fmt.Sprintf("%s.%s: %v", c.Module, c.Method, err)})
	logf(i18n.L("記録に失敗: %s.%s: %v", "could not record %s.%s: %v"), c.Module, c.Method, err)
}

// ---- routing ----------------------------------------------------------------------

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", "iidx-tracker")
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/") && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		a.api(w, r)
	case r.Method == http.MethodPost:
		a.proxy(w, r)
	case r.Method != http.MethodGet:
		reply(w, http.StatusNotImplemented, []byte("unsupported method"), "text/plain")
	case p == "/" || p == "/ui":
		w.Header().Set("Location", "/ui/")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusFound)
	case strings.HasPrefix(p, "/ui/"):
		a.serveStatic(w, strings.TrimPrefix(p, "/ui/"))
	default:
		reply(w, http.StatusNotFound, []byte("not found"), "text/plain")
	}
}

func reply(w http.ResponseWriter, status int, body []byte, ctype string) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	w.Write(body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		status, b = 500, *bytes.NewBufferString(`{"error": "json"}`)
	}
	reply(w, status, bytes.TrimRight(b.Bytes(), "\n"), "application/json; charset=utf-8")
}

func (a *App) serveStatic(w http.ResponseWriter, name string) {
	if name == "" {
		name = "index.html"
	}
	body, err := fs.ReadFile(a.static, name)
	if err != nil || strings.Contains(name, "/") {
		reply(w, http.StatusNotFound, []byte("not found"), "text/plain")
		return
	}
	ctype := mime.TypeByExtension(path.Ext(name))
	switch path.Ext(name) {
	case ".js":
		ctype = "text/javascript"
	case ".html":
		ctype = "text/html"
	case ".css":
		ctype = "text/css"
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	if (strings.HasPrefix(ctype, "text/") || strings.HasSuffix(ctype, "javascript")) && !strings.Contains(ctype, "charset") {
		ctype += "; charset=utf-8"
	}
	w.Header().Set("Cache-Control", "no-cache")
	reply(w, http.StatusOK, body, ctype)
}

// ---- tracker_link.dll ---------------------------------------------------------------
//
// tracker_link.dll (a hook DLL built with altfix) adds a <tracker_link> element to two of the
// game's requests: services.get gets the music data file the game loads, music.reg the play's
// judgments by timing and by key. The proxy takes the element out, so the server upstream sees
// the request as the game would send it without the DLL.

// takeLink detaches <tracker_link> from the request (it sits under the method element).
func takeLink(nodes ...*eamuse.Node) *eamuse.Node {
	var link *eamuse.Node
	for _, n := range nodes {
		kept := n.Children[:0]
		for _, c := range n.Children {
			if c.Name != "tracker_link" {
				kept = append(kept, c)
			} else if link == nil {
				link = c
			}
		}
		n.Children = kept
	}
	return link
}

// machineReport records the music data file a cabinet's services.get reported; its plays then
// belong to the music database holding that file.
func (a *App) machineReport(r *http.Request, c *store.Call) {
	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	size, _ := strconv.ParseInt(c.Link.Get("size"), 10, 64)
	name := path.Base(c.Link.Get("name"))
	db, err := a.store.MachineBoot(c.PCBID, strings.ToLower(c.Link.Get("sha256")), name, size,
		"tracker_link "+c.Link.Get("ver"), c.Model, remote)
	if err != nil {
		logf(i18n.L("tracker_link: %s の報告を記録できません: %v", "tracker_link: cannot record the report of %s: %v"), c.PCBID, err)
		return
	}
	logf(i18n.L("tracker_link: %s は %s (曲DB %v)", "tracker_link: %s runs %s (music database %v)"), c.PCBID, name, orNone(db))
}

func orNone(v any) any {
	if v == nil {
		return i18n.L("未登録: 曲DB 画面でこのファイルを取り込むと紐付きます", "not registered: import this file on the Music DB page")
	}
	return v
}

// ---- proxy -------------------------------------------------------------------------

func localAddr(r *http.Request) (string, int) {
	if addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if tcp, ok := addr.(*net.TCPAddr); ok {
			return tcp.IP.String(), tcp.Port
		}
	}
	return "127.0.0.1", 0
}

// target is where a request goes: origin and path + query. /fwd/<id>/... names an origin
// registered from a services.get answer; everything else goes to the configured upstream.
func (a *App) target(r *http.Request) (origin, rest string, ok bool) {
	query := ""
	if r.URL.RawQuery != "" {
		query = "?" + r.URL.RawQuery
	}
	p := r.URL.EscapedPath()
	if strings.HasPrefix(p, "/fwd/") {
		oid, rest, _ := strings.Cut(p[5:], "/")
		id, err := strconv.ParseInt(oid, 10, 64)
		if err != nil || oid == "" || strings.ContainsAny(oid, "+-") {
			return "", "", false
		}
		o := a.store.Origin(id)
		return o, "/" + rest + query, o != ""
	}
	o, base, ok := a.upstream()
	return o, base + p + query, ok
}

func (a *App) proxy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		reply(w, http.StatusBadRequest, []byte("bad request"), "text/plain")
		return
	}
	a.bump("requests", map[string]any{"last_request": time.Now().Unix()})

	origin, rest, ok := a.target(r)
	if !ok {
		msg := i18n.L("不明な転送先です", "unknown destination")
		if _, _, set := a.upstream(); !set {
			msg = i18n.L("upstream が未設定です", "upstream is not set")
		}
		reply(w, http.StatusServiceUnavailable, []byte(msg), "text/plain; charset=utf-8")
		return
	}
	if a.pointsAtSelf(r, origin) {
		reply(w, http.StatusLoopDetected, []byte("upstream points back at the tracker"), "text/plain")
		return
	}

	info, compress := r.Header.Get("X-Eamuse-Info"), r.Header.Get("X-Compress")
	var call *store.Call
	var relayHeaders map[string]string
	if root, binary, err := eamuse.Decode(body, info, compress); err != nil || len(root.Children) == 0 {
		logf(i18n.L("リクエストを解読できません (そのまま転送): %v", "cannot decode the request (relayed as is): %v"),
			errOr(err, i18n.L("空のリクエスト", "empty request")))
	} else {
		m := root.Children[0]
		call = &store.Call{Model: root.Get("model"), PCBID: root.Get("srcid"), Module: m.Name,
			Method: m.Get("method"), Req: m, Link: takeLink(root, m), Upstream: origin, TS: time.Now().Unix()}
		if call.Link != nil {
			// relay the request as the game would send it without tracker_link.dll
			if out, headers, err := eamuse.Encode(root, binary, info); err != nil {
				logf(i18n.L("tracker_link の要素を取り除けません (そのまま転送): %v", "cannot take out the tracker_link element (relayed as is): %v"), err)
			} else {
				body, relayHeaders = out, headers
			}
			if call.Module == "services" && call.Method == "get" {
				a.machineReport(r, call)
			}
		}
	}

	// omnimix must never reach a public server - not even through the tracker.
	if call != nil && isOmni(call.Model) {
		if u, err := url.Parse(origin); err == nil && !isPrivateHost(u.Hostname()) {
			a.bump("refused", nil)
			logf(i18n.L("omnimix の公開サーバーへの転送を拒否: %s", "refused to relay omnimix to a public server: %s"), origin)
			reply(w, http.StatusForbidden, []byte("omnimix must not be relayed to a public server"), "text/plain")
			return
		}
	}

	resp, rbody, err := a.forward(r, origin, rest, body, relayHeaders)
	if err != nil {
		a.bump("errors", map[string]any{"last_error": "upstream: " + err.Error()})
		logf(i18n.L("upstream に接続できません: %s (%v)", "cannot reach upstream: %s (%v)"), origin, err)
		reply(w, http.StatusBadGateway, []byte("upstream error: "+err.Error()), "text/plain")
		return
	}

	out := rbody
	headers := http.Header{}
	for k, v := range resp.Header {
		if !hopHeaders[strings.ToLower(k)] {
			headers[k] = v
		}
	}
	if call != nil && call.Module == "services" && call.Method == "get" && resp.StatusCode == 200 {
		if rewritten, extra, err := a.rewriteServices(r, rbody, resp.Header); err != nil {
			logf(i18n.L("services.get を書き換えられません (以降は記録されません): %v", "cannot rewrite services.get (nothing after it will be recorded): %v"), err)
		} else {
			out = rewritten
			headers.Del("X-Compress")
			headers.Del("X-Eamuse-Info")
			for k, v := range extra {
				headers.Set(k, v)
			}
		}
	}
	for k, v := range headers {
		w.Header()[k] = v
	}
	if _, has := headers["Content-Type"]; !has {
		w.Header()["Content-Type"] = nil // no sniffed type: pass the answer on as it came
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(resp.StatusCode)
	w.Write(out)

	if call != nil && resp.StatusCode == 200 && strings.HasPrefix(call.Module, "IIDX") && len(call.Module) >= 6 &&
		recordedCalls[call.Module[6:]+"."+call.Method] {
		select {
		case a.queue <- pending{call, rbody, resp.Header.Get("X-Eamuse-Info"), resp.Header.Get("X-Compress")}:
		default:
			a.fail(call, errors.New(i18n.L("記録の待ち行列があふれました", "the recording queue is full")))
		}
	}
}

func errOr(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

func (a *App) forward(r *http.Request, origin, rest string, body []byte, override map[string]string) (*http.Response, []byte, error) {
	if rest == "" {
		rest = "/"
	}
	req, err := http.NewRequest(http.MethodPost, origin+rest, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	for k, v := range r.Header {
		// Accept-Encoding is dropped so the body stays readable for recording.
		if lk := strings.ToLower(k); !hopHeaders[lk] && lk != "accept-encoding" {
			req.Header[k] = v
		}
	}
	for k, v := range override {
		req.Header.Set(k, v)
	}
	if _, has := req.Header["User-Agent"]; !has {
		req.Header["User-Agent"] = []string{""} // do not announce Go
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	rbody, err := io.ReadAll(resp.Body)
	return resp, rbody, err
}

// pointsAtSelf: an upstream that is this tracker would relay every request back to itself.
func (a *App) pointsAtSelf(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	meIP, mePort := localAddr(r)
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}
	if port != mePort {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "::1" || host == meIP || host == strings.TrimPrefix(meIP, "::ffff:") ||
		strings.HasPrefix(host, "127.") {
		return true
	}
	for _, ip := range localAddresses() {
		if host == ip {
			return true
		}
	}
	return false
}

// rewriteServices points every service at the tracker (/fwd/<id>/...), keeping the path.
func (a *App) rewriteServices(r *http.Request, body []byte, h http.Header) ([]byte, map[string]string, error) {
	info := h.Get("X-Eamuse-Info")
	doc, binary, err := eamuse.Decode(body, info, h.Get("X-Compress"))
	if err != nil {
		return nil, nil, err
	}
	ip, port := localAddr(r)
	host := serviceHost(ip, port, a.conf("advertise_host"))
	for _, item := range doc.Iter("item") {
		u, err := url.Parse(item.Get("url"))
		name := item.Get("name")
		if err != nil || name == "ntp" || name == "keepalive" || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		if u.Host == host {
			continue
		}
		oid, err := a.store.OriginID(u.Scheme + "://" + u.Host)
		if err != nil {
			return nil, nil, err
		}
		query := ""
		if u.RawQuery != "" {
			query = "?" + u.RawQuery
		}
		item.Set("url", fmt.Sprintf("http://%s/fwd/%d%s%s", host, oid, u.EscapedPath(), query))
	}
	return eamuse.Encode(doc, binary, info)
}
