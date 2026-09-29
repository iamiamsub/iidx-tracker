// The web UI's header selection (player / music database / chart set) across page changes while the
// server's lists change: runs the real static/app.js in a fake page (elements, localStorage, location.hash,
// fetch answered by a fake server). Run by TestUIHeader, or: node testdata/ui_test.mjs static
import vm from "node:vm";
import fs from "node:fs";
import path from "node:path";

const dir = process.argv[2] || "static";
const APP = fs.readFileSync(path.join(dir, "app.js"), "utf8"), I18N = fs.readFileSync(path.join(dir, "i18n.js"), "utf8");

const unesc = (s) => s.replace(/&(amp|lt|gt|quot|#39);/g, (_, e) => ({amp: "&", lt: "<", gt: ">", quot: '"', "#39": "'"}[e]));

class El {
  constructor(select = false) {
    Object.assign(this, {select, listeners: {}, _html: "", _value: "", options: [], hidden: false, style: {},
      className: "", textContent: "", classList: {remove() {}, add() {}, toggle: () => false}});
  }
  set innerHTML(h) { // a <select> selects its first option when its options are replaced, like a browser
    this._html = h;
    this.options = [...h.matchAll(/<option value="([^"]*)"/g)].map((m) => unesc(m[1]));
    if (this.select) this._value = this.options[0] ?? "";
  }
  get innerHTML() { return this._html; }
  set value(v) { this._value = !this.select || this.options.includes(String(v)) ? String(v) : ""; } // no such option: none shown
  get value() { return this._value; }
  setAttribute() {}
  addEventListener(ev, f) { (this.listeners[ev] ||= []).push(f); }
  querySelector() { return null; }
  querySelectorAll() { return []; }
}

function makeServer() {
  const s = {players: [], dbs: [{id: 1, name: "db1"}, {id: 2, name: "db2"}], sets: [], context: {}, fail: new Set(), delay: {}, inflight: 0};
  s.fetch = (url) => {
    const u = new URL(url, "http://tracker");
    const [status, data] = s.fail.has(u.pathname) ? [500, {error: `${u.pathname} failed`}] : [200, {
      "/api/players": () => structuredClone(s.players),
      "/api/musicdbs": () => s.dbs.map((d) => ({...d})),
      "/api/chartsets": () => s.sets.map((name) => ({name})),
      "/api/context": () => ({db: s.context[u.searchParams.get("key")] ?? null}),
    }[u.pathname]?.() ?? {}]; // the views get nothing useful and show their error banner
    s.inflight++;
    const later = s.delay[u.pathname] ? (f) => setTimeout(f, s.delay[u.pathname]) : setImmediate; // timers are ~15 ms on Windows
    return new Promise((r) => later(() => {
      s.inflight--;
      r({ok: status < 400, status, json: async () => data});
    }));
  };
  return s;
}

function boot(server, storage = {}) {
  const els = {"#player": new El(true), "#db": new El(true), "#set": new El(true), "#lang": new El(true)};
  for (const id of ["#top", "#menu-btn", "#view", "#toast", "#set-pick"]) els[id] = new El();
  const listeners = {};
  const location = {
    _h: "#/songs",
    get hash() { return this._h; },
    set hash(v) {
      if (v === this._h) return;
      this._h = v;
      setImmediate(() => (listeners.hashchange || []).forEach((f) => f()));
    },
    reload() {},
  };
  const ls = new Map(Object.entries(storage));
  const ctx = vm.createContext({
    document: {documentElement: {}, querySelector: (sel) => els[sel] ?? null, querySelectorAll: () => []},
    window: {addEventListener: (ev, f) => (listeners[ev] ||= []).push(f), scrollTo() {}},
    location, navigator: {language: "ja"},
    localStorage: {getItem: (k) => (ls.has(k) ? ls.get(k) : null), setItem: (k, v) => ls.set(k, String(v)), removeItem: (k) => ls.delete(k)},
    fetch: server.fetch, setTimeout, clearTimeout, URL, URLSearchParams, console, confirm: () => true, alert() {},
  });
  vm.runInContext(I18N, ctx);
  vm.runInContext(APP, ctx);
  const b = {
    els, ls,
    get state() { return vm.runInContext("state", ctx); },
    async go(h) { location.hash = h; await settle(server); },
    async pick(id, v) { els[id].value = v; (els[id].listeners.change || []).forEach((f) => f({target: els[id]})); await settle(server); },
    player() { return b.state.player === b.els["#player"].value ? b.state.player : `${b.state.player} (shown ${b.els["#player"].value})`; },
  };
  return b;
}

async function settle(server) { // until no request is out for a few turns
  for (let idle = 0; idle < 8;) {
    await new Promise((r) => setImmediate(r));
    idle = server.inflight ? 0 : idle + 1;
  }
}

// /api/players as the tracker returns it: cards, newest login first, then plays recorded without a card
const card = (key, name, iidx, seen) => ({key, card_id: key, last_seen: seen, plays: 1, profile: {name, iidx_id: iidx}});
const A = "E004000000000001", B = "E004000000000002", ANON_B = "iidx:33334444";
const a = (seen) => card(A, "ALPHA", 11112222, seen), b = (seen) => card(B, "BRAVO", 33334444, seen);
const anonB = {key: ANON_B, iidx_id: 33334444, card_id: null, profile: null, plays: 1};

let failed = 0;
function expect(name, got, want) {
  if (JSON.stringify(got) === JSON.stringify(want)) return;
  failed++;
  console.log(`FAIL ${name}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
}

const tests = {
  async "someone else finishing a credit does not take the selection"() {
    const s = makeServer();
    s.players = [a(200), b(100)];
    const p = boot(s, {player: B, db: "2"});
    await settle(s);
    s.players = [a(300), b(100)];
    for (const h of ["#/", "#/player", "#/chart/1000/3", "#/tiers"]) {
      await p.go(h);
      expect(`${h} player`, [p.player(), p.ls.get("player")], [B, B]);
    }
    expect("db", [p.state.db, p.els["#db"].value], ["2", "2"]);
  },
  async "a new card logging in does not take the selection"() {
    const s = makeServer();
    s.players = [b(100)];
    const p = boot(s, {player: B});
    await settle(s);
    s.players = [a(300), b(100)];
    await p.go("#/player");
    expect("player", p.player(), B);
    expect("options", p.els["#player"].options, [A, B]);
  },
  async "a player picked in the header stays"() {
    const s = makeServer();
    s.players = [a(200), b(100)];
    s.context = {[B]: 2};
    const p = boot(s, {player: A, db: "1"});
    await settle(s);
    await p.pick("#player", B);
    expect("picked", [p.player(), p.state.db], [B, "2"]); // the picked player's default database
    s.players = [a(400), b(100)];
    await p.go("#/");
    expect("after a page change", [p.player(), p.state.db, p.ls.get("player")], [B, "2", B]);
  },
  async "the first player shows without reloading"() {
    const s = makeServer();
    const p = boot(s);
    await settle(s);
    expect("empty", p.player(), "");
    s.players = [b(100)];
    await p.go("#/chart/1000/3");
    expect("player", p.player(), B);
  },
  async "plays without a card stay with that person when the card logs in"() {
    for (const after of [[b(300), a(200)], [a(400), b(300)]]) { // with and without another login after it
      const s = makeServer();
      s.players = [a(200), anonB];
      const p = boot(s, {player: ANON_B});
      await settle(s);
      expect("before", p.player(), ANON_B);
      s.players = after;
      await p.go("#/player");
      expect(`after (${after.map((x) => x.profile.name)})`, [p.player(), p.ls.get("player")], [B, B]);
    }
  },
  async "music databases and chart sets coming and going"() {
    const s = makeServer();
    s.players = [a(200)];
    s.context = {[A]: 1};
    const p = boot(s, {player: A, db: "2"});
    await settle(s);
    expect("no sets: picker hidden", p.els["#set-pick"].hidden, true);
    s.sets = ["Kiraku"];
    s.dbs.push({id: 3, name: "db3"});
    await p.go("#/");
    expect("set picker", [p.els["#set-pick"].hidden, p.els["#set"].options], [false, ["", "Kiraku"]]);
    expect("db kept", [p.state.db, p.els["#db"].value, p.els["#db"].options], ["2", "2", ["1", "2", "3"]]);
    await p.pick("#set", "Kiraku");
    s.sets = ["Kiraku", "Kichiku"];
    await p.go("#/songs");
    expect("set kept", [p.state.set, p.els["#set"].value], ["Kiraku", "Kiraku"]);
    s.sets = ["Kichiku"];
    s.dbs = s.dbs.filter((d) => d.id !== 2);
    await p.go("#/player");
    expect("set gone", [p.state.set, p.ls.get("set")], ["", ""]);
    expect("db gone: the player's default", [p.state.db, p.els["#db"].value], ["1", "1"]);
  },
  async "a player from the URL"() {
    const s = makeServer();
    s.players = [a(200), b(100)];
    const p = boot(s, {player: A});
    await settle(s);
    await p.go(`#/player/${B}`);
    expect("URL", [p.player(), p.ls.get("player")], [B, B]);
    await p.go("#/songs");
    expect("next page", p.player(), B);
  },
  async "the player list failing"() {
    const s = makeServer();
    s.players = [a(200), b(100)];
    const p = boot(s, {player: B});
    await settle(s);
    s.fail.add("/api/players");
    await p.go("#/nowhere");
    expect("kept, page shown", [p.player(), p.els["#view"].innerHTML], [B, "<p>ページが見つかりません</p>"]);
    expect("toast", p.els["#toast"].textContent, "/api/players failed");
    s.fail.clear();
    await p.go("#/songs");
    expect("recovered", p.player(), B);
  },
  async "page changes while the list is slow"() {
    const s = makeServer();
    s.players = [a(200), b(100)];
    s.context = {[A]: 1, [B]: 2};
    const p = boot(s, {player: A, db: "1"});
    await settle(s);
    s.delay["/api/players"] = 40;
    p.go("#/player");
    await new Promise((r) => setTimeout(r, 5));
    s.players = [a(300), b(100)];
    await p.go("#/nowhere");
    expect("the last page", p.els["#view"].innerHTML, "<p>ページが見つかりません</p>");
    p.go("#/songs");
    await new Promise((r) => setTimeout(r, 5));
    await p.pick("#player", B); // picked while that page's list is on its way
    expect("the picked player", [p.player(), p.ls.get("player"), p.state.db], [B, B, "2"]);
  },
};

for (const [name, fn] of Object.entries(tests)) {
  const before = failed;
  try { await fn(); } catch (e) { failed++; console.log(`FAIL ${name}: ${e.stack}`); }
  if (failed > before) console.log(`  in: ${name}`);
}
console.log(failed ? `${failed} failed` : `ok (${Object.keys(tests).length} tests)`);
process.exit(failed ? 1 : 0); // not waiting for the page's timers (the toast's)
