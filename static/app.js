"use strict";

// ---------------------------------------------------------------- constants

const CHARTS = ["SPB", "SPN", "SPH", "SPA", "SPL", "DPB", "DPN", "DPH", "DPA", "DPL"];
const LAMPS = ["NO PLAY", "FAILED", "ASSIST", "EASY", "CLEAR", "HARD", "EX HARD", "FULL COMBO"];
const LAMP_COLORS = ["#3a3f4b", "#8b2635", "#a970ff", "#8fe36b", "#46b3ff", "#ff5a5a", "#ffd23f", "url(#fcgrad)"];
const RAINBOW = ["#ff6fa8", "#ffd23f", "#7ee07e", "#5cc8ff", "#a970ff"];
// SVG fills can reference the gradient; CSS needs the gradient spelled out.
const cssColor = (c) => (c.startsWith("url(") ? `linear-gradient(90deg, ${RAINBOW.join(", ")})` : c);
const DAN = pick(["七級", "六級", "五級", "四級", "三級", "二級", "一級", "初段", "二段", "三段", "四段",
             "五段", "六段", "七段", "八段", "九段", "十段", "中伝", "皆伝"],
  ["7th kyu", "6th kyu", "5th kyu", "4th kyu", "3rd kyu", "2nd kyu", "1st kyu", "1st dan", "2nd dan", "3rd dan",
   "4th dan", "5th dan", "6th dan", "7th dan", "8th dan", "9th dan", "10th dan", "Chuden", "Kaiden"]);
const RADAR = ["NOTES", "CHORD", "PEAK", "CHARGE", "SCRATCH", "SOF-LAN"];
// pc.get radar_score comes in the game's attribute order NOTES, PEAK, SCRATCH, SOF-LAN, CHARGE, CHORD
// (bm2dx Radar_FilterValueToAttr); RADAR above is the in-game display order: RADAR[i] = value[RADAR_ATTR[i]].
const RADAR_ATTR = [0, 5, 1, 4, 2, 3];
// Target graph numbers whose target bm2dx builds itself (CTargetCommonScoreGraph::BuildTargetLine);
// the others take their target from the server (rivals, pacemakers, battles).
const GRAPHS = {20: "AAA", 21: "AA", 22: "A", 23: t("ペース"), 24: t("次の DJ LEVEL"), 25: t("自己ベスト+")};
const PREFS = pick(["", "北海道", "青森県", "岩手県", "宮城県", "秋田県", "山形県", "福島県", "茨城県", "栃木県",
  "群馬県", "埼玉県", "千葉県", "東京都", "神奈川県", "新潟県", "富山県", "石川県", "福井県", "山梨県",
  "長野県", "岐阜県", "静岡県", "愛知県", "三重県", "滋賀県", "京都府", "大阪府", "兵庫県", "奈良県",
  "和歌山県", "鳥取県", "島根県", "岡山県", "広島県", "山口県", "徳島県", "香川県", "愛媛県", "高知県",
  "福岡県", "佐賀県", "長崎県", "熊本県", "大分県", "宮崎県", "鹿児島県", "沖縄県", "香港", "韓国", "台湾",
  "タイ", "インドネシア", "シンガポール", "フィリピン", "マカオ", "アメリカ", "海外"],
  ["", "Hokkaido", "Aomori", "Iwate", "Miyagi", "Akita", "Yamagata", "Fukushima", "Ibaraki", "Tochigi", "Gunma",
   "Saitama", "Chiba", "Tokyo", "Kanagawa", "Niigata", "Toyama", "Ishikawa", "Fukui", "Yamanashi", "Nagano",
   "Gifu", "Shizuoka", "Aichi", "Mie", "Shiga", "Kyoto", "Osaka", "Hyogo", "Nara", "Wakayama", "Tottori",
   "Shimane", "Okayama", "Hiroshima", "Yamaguchi", "Tokushima", "Kagawa", "Ehime", "Kochi", "Fukuoka", "Saga",
   "Nagasaki", "Kumamoto", "Oita", "Miyazaki", "Kagoshima", "Okinawa", "Hong Kong", "Korea", "Taiwan",
   "Thailand", "Indonesia", "Singapore", "Philippines", "Macau", "USA", "Overseas"]);
const VERSIONS = {0: "1st style", 1: "substream", 2: "2nd style", 3: "3rd style", 4: "4th style",
  5: "5th style", 6: "6th style", 7: "7th style", 8: "8th style", 9: "9th style", 10: "10th style",
  11: "IIDX RED", 12: "HAPPY SKY", 13: "DistorteD", 14: "GOLD", 15: "DJ TROOPERS", 16: "EMPRESS",
  17: "SIRIUS", 18: "Resort Anthem", 19: "Lincle", 20: "tricoro", 21: "SPADA", 22: "PENDUAL",
  23: "copula", 24: "SINOBUZ", 25: "CANNON BALLERS", 26: "Rootage", 27: "HEROIC VERSE",
  28: "BISTROVER", 29: "CastHour", 30: "RESIDENT", 31: "EPOLIS", 32: "Pinky Crush",
  33: "Sparkle Shower", 80: "INFINITAS"};
const JUDGE_COLORS = {PG: "#5cc8ff", GR: "#ffd23f", GD: "#7ee07e", BD: "#a970ff", PR: "#ff5a5a"};

const state = {
  player: localStorage.getItem("player") || "",
  players: [],
  db: localStorage.getItem("db") || "", // music database being viewed (music IDs mean different songs per database)
  dbs: [],
  set: localStorage.getItem("set") || "", // 2dxtra chart set being viewed ("" = the game's charts)
  sets: [],
};

// ---------------------------------------------------------------- helpers

const $ = (sel, root = document) => root.querySelector(sel);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g,
  (c) => ({"&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"}[c]));

async function api(path, opts = {}) {
  const res = await fetch(path, {...opts, headers: {"X-Lang": LANG, ...opts.headers}}); // errors come back in LANG
  const data = await res.json().catch(() => ({error: `HTTP ${res.status}`}));
  if (!res.ok || (data && data.error)) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}
const post = (path, body) => api(path, {method: "POST", body: JSON.stringify(body)});

function toast(msg, ok = false) {
  const t = $("#toast");
  t.textContent = msg;
  t.className = ok ? "ok" : "";
  t.style.display = "block";
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => (t.style.display = "none"), 5000);
}

function fmtDate(ts, withTime = true) {
  if (!ts) return "-";
  const d = new Date(ts * 1000);
  const p = (n) => String(n).padStart(2, "0");
  const day = `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())}`;
  return withTime ? `${day} ${p(d.getHours())}:${p(d.getMinutes())}` : day;
}

const chartTag = (c) => `<span class="chart-tag c-${CHARTS[c][2]}">${CHARTS[c]}</span>`;
const lampBox = (c) => `<span class="lamp lamp-${c ?? 0}" title="${LAMPS[c ?? 0]}"></span>`;
const danName = (g) => (g == null || g < 0 ? "-" : DAN[g] ?? `#${g}`);
const prefName = (p) => PREFS[p] || (p ? t("地域{0}", p) : "-");
const mono = (s) => `<span class="mono">${s}</span>`;
// automatic categories of songs missing from another music DB come with that DB's name
const catName = (c) => (c.other ? t("{0} に無い曲", c.other) : c.name);
// A chart's page; set: a 2dxtra chart set (Kiraku, Kichiku, All-Scratch), whose plays are kept apart.
const chartHref = (mid, chart, set) => `#/chart/${mid}/${chart}${set ? `?set=${encodeURIComponent(set)}` : ""}`;
const setPill = (set) => (set ? ` <span class="pill set" title="${t("2dxtra の譜面")}">${esc(set)}</span>` : "");
const versionName = (v) => (v == null ? "-" : VERSIONS[v] ?? `ver ${v}`);
const arenaName = (c) => (c == null || c < 0 ? "-" : "ABCD"[Math.floor(c / 5)] + ((c % 5) + 1));

// Grade k/9 of the maximum: AAA 8/9, AA 7/9 ... E 2/9, F below.
function djLevel(ex, notes) {
  if (ex == null || ex < 0 || !notes) return null;
  const max = notes * 2;
  const grades = ["F", "E", "D", "C", "B", "A", "AA", "AAA"];
  const step = Math.max(1, Math.min(8, Math.floor((ex * 9) / max)));
  const nextStep = Math.max(2, step + 1);
  const next = step >= 8 ? {name: "MAX", at: max}
                         : {name: grades[nextStep - 1], at: Math.ceil((max * nextStep) / 9)};
  return {grade: grades[step - 1], rate: ex / max, diff: `${next.name}-${next.at - ex}`};
}

function djCell(ex, notes) {
  const d = djLevel(ex, notes);
  if (!d) return `<td></td><td></td>`;
  return `<td class="num">${(d.rate * 100).toFixed(2)}%</td>` +
         `<td><span class="dj dj-${d.grade}">${d.grade}</span> <span class="dim small">${d.diff}</span></td>`;
}

const optionText = (p) => (p.option1 == null ? "-" : p.options === "OFF" ? `<span class="dim">OFF</span>` : esc(p.options));
const djpText = (v) => (v == null ? "" : (v / 10000).toFixed(2));   // DJ POINT is kept x10000 like the game

// Difficulty-table ranks (built in): SP☆12 "normal" / "hard" reference tables (地力 / 個人差 F..S+),
// DP "dp" unofficial difficulty. Short form: the rank, * for 個人差.
const tierShort = (label) => (label ?? "").replace(/^地力/, "").replace(/^個人差(.+)$/, "$1*");
const tierText = (tier) => (!tier ? "" : tier.dp ? tier.dp.label : `${tierShort(tier.normal?.label) || "-"} / ${tierShort(tier.hard?.label) || "-"}`);
const tierSort = (tier) => (!tier ? null : tier.dp ? tier.dp.value : (tier.normal?.value ?? 0) + (tier.hard?.value ?? 0) / 100);
const tierDetail = (tier, sources = {}) => {
  const link = (kind, text) => (sources[kind]
    ? `<a href="${esc(sources[kind].source)}" target="_blank" rel="noopener">${esc(text)}</a> (${t("{0} 時点", esc(sources[kind].fetched))})` : esc(text));
  if (tier.dp) return link("dp", `${t("DP非公式難易度")} ${tier.dp.label}`);
  return link("normal", `${t("☆12参考表")} ${t("ノマゲ")} ${tier.normal?.label ?? "-"}${SEP}${t("ハード")} ${tier.hard?.label ?? "-"}`);
};

// music_play_log folder_type: the music select folder the song was picked from (bm2dx folder ids,
// analysis_0819 musicdata.md 4.4). In 段位認定 it is the course number instead.
function folderName(f, mode) {
  if (f == null || f < 0) return null;
  if (mode === 3) return t("段位 {0}", DAN[f] ?? f);
  const fixed = {0x12: "LEGGENDARIA", 0x35: "LIGHTNING MODEL", 0x36: "EPOLIS RESTORATION", 0x37: "EXTRA",
    0x58: "OMNIMIX (altfix)", 0x59: "RECOMMEND", 0x60: "COMPETITION", 0x69: "BEMANI", 0x6a: "ALL DIFFICULTY",
    0x6b: "ALL DIFFICULTY (CN)", 0x6c: "ALL VERSION", 0x6d: "ALL ALPHABET", 0x6e: "DAN SEARCH RESULT",
    0x77: "NO SCORE", 0x80: "ALMOST FULLCOMBO", 0x81: "MY BEST", 0x82: "ALL RIVAL PLAY", 0xc5: "RIVAL CHALLENGE",
    0xc6: "MASTERS", 0xc7: "MY BINGO CARD", 0xd7: "ARENA WIN PLAYLIST", 0xd8: "ARENA LOSE PLAYLIST",
    0xe3: t("DJ TRAINING 皆伝"), 0xe4: "DJ TRAINING", 0xe5: "EVENT"};
  if (fixed[f]) return fixed[f];
  if (f <= 0x05) return ["BEGINNER", "IIDX", "BEMANI", "ARTIST", "GENRE", "TENDENCY"][f] + " RECOMMEND";
  if (f <= 0x11) return `LEVEL ${f - 5}`;
  if (f <= 0x34) return versionName(f - 0x13);
  if (f <= 0x48) return "WORLD TOURISM";
  if (f <= 0x57) return null;
  if (f <= 0x5f) return ["NOTES", "CHORD", "PEAK", "CHARGE", "SCRATCH", "SOF-LAN"][f - 0x5a] + " RECOMMEND";
  if (f <= 0x68) return "NAME " + ["A-D", "E-H", "I-L", "M-P", "Q-T", "U-Z", "0-9", "OTHERS"][f - 0x61];
  if (f <= 0x76) return "DJ LEVEL " + ["AAA", "AA", "A", "B", "C", "D", "E", "F"][f - 0x6f];
  if (f <= 0x7f) return ["FULLCOMBO", "EX HARD", "HARD", "CLEAR", "EASY", "ASSIST", "FAILED", "NO PLAY"][f - 0x78];
  if (f <= 0xa0) return `RIVAL${(f - 0x83) % 6 + 1} ` +
    ["PLAYED", "SCORE WIN", "SCORE LOSE", "CLEAR WIN", "CLEAR LOSE"][Math.floor((f - 0x83) / 6)];
  if (f <= 0xa5) return "STEP UP";
  if (f <= 0xb1) return `STEP UP LEVEL ${f - 0xa5}`;
  if (f <= 0xc4) return `DAN PRACTICE ${DAN[f - 0xb2]}`;
  if (f <= 0xd1) return `MEMO ${f - 0xc7}`;
  if (f <= 0xd6) return `ORIGINAL ${f - 0xd1}`;
  if (f <= 0xe2) return `DJ TRAINING ${f <= 0xdd ? "SP" : "DP"}`;
  return null;
}

const playerName = (key) => {
  const p = state.players.find((x) => x.key === key);
  if (!p) return key;
  return p.profile ? `${p.profile.name} (${p.profile.iidx_id})` : t("IIDX ID {0} (カード不明)", p.iidx_id);
};

// ---------------------------------------------------------------- SVG charts

const NS = "http://www.w3.org/2000/svg";
function svgEl(tag, attrs = {}, parent = null, text = null) {
  const e = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  if (text != null) e.textContent = text;
  if (parent) parent.appendChild(e);
  return e;
}

function niceTicks(min, max, count = 5) {
  const span = max - min || 1;
  const step0 = span / count;
  const mag = 10 ** Math.floor(Math.log10(step0));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= step0);
  const ticks = [];
  for (let v = Math.ceil(min / step) * step; v <= max + 1e-9; v += step) ticks.push(+v.toFixed(6));
  return ticks;
}

// Hover tooltip for a chart box (.chart-box is position: relative).
function chartTip(box) {
  const el = document.createElement("div");
  el.className = "chart-tip";
  box.appendChild(el);
  return {
    show(html, e) {
      el.innerHTML = html;
      el.style.display = "block";
      const r = box.getBoundingClientRect();
      let left = e.clientX - r.left + 14;
      if (left + el.offsetWidth > r.width) left = Math.max(0, e.clientX - r.left - el.offsetWidth - 14);
      el.style.left = `${left}px`;
      el.style.top = `${e.clientY - r.top + 14}px`;
    },
    hide() { el.style.display = "none"; },
  };
}

const swatch = (color) => `<i style="background:${cssColor(color)}"></i>`;

// series: [{name, color, points: [{y, tip}], area}], all series share the x index.
// Hovering shows every series at that index: a point's tip, else "name value"; xTip(i) heads it.
function lineChart(box, series, {yMin, yMax, yFmt = (v) => v, height = 220, xLabels = [], xTip} = {}) {
  const W = 800, H = height, L = 52, R = 12, T = 12, B = 26;
  const all = series.flatMap((s) => s.points.map((p) => p.y)).filter((v) => v != null);
  if (!all.length) { box.innerHTML = `<p class="dim">${t("データがありません")}</p>`; return; }
  let lo = yMin ?? Math.min(...all), hi = yMax ?? Math.max(...all);
  if (lo === hi) { lo -= 1; hi += 1; }
  const n = Math.max(...series.map((s) => s.points.length));
  const x = (i) => L + (n <= 1 ? (W - L - R) / 2 : (i * (W - L - R)) / (n - 1));
  const y = (v) => T + (H - T - B) * (1 - (v - lo) / (hi - lo));
  const svg = svgEl("svg", {viewBox: `0 0 ${W} ${H}`});
  for (const t of niceTicks(lo, hi)) {
    svgEl("line", {x1: L, x2: W - R, y1: y(t), y2: y(t), stroke: "#2a2f3f"}, svg);
    svgEl("text", {x: L - 6, y: y(t) + 4, "text-anchor": "end", fill: "#8b91a5", "font-size": 11}, svg, yFmt(t));
  }
  xLabels.forEach(([i, label]) => svgEl("text", {x: x(i), y: H - 6, "text-anchor": i === 0 ? "start" : "end",
    fill: "#8b91a5", "font-size": 11}, svg, label));
  for (const s of series) {
    const pts = s.points.map((p, i) => [i, p]).filter(([, p]) => p.y != null);
    if (!pts.length) continue;
    const d = pts.map(([i, p], k) => `${k ? "L" : "M"}${x(i).toFixed(1)},${y(p.y).toFixed(1)}`).join("");
    if (s.area) svgEl("path", {d: `${d}L${x(pts[pts.length - 1][0])},${y(lo)}L${x(pts[0][0])},${y(lo)}Z`,
      fill: s.color, opacity: 0.15}, svg);
    svgEl("path", {d, fill: "none", stroke: s.color, "stroke-width": 2, "stroke-dasharray": s.dash || ""}, svg);
    if (pts.length <= 120 && !s.noDots) {
      for (const [i, p] of pts) {
        svgEl("circle", {cx: x(i), cy: y(p.y), r: 3.5, fill: s.color}, svg);
      }
    }
  }
  const guide = svgEl("line", {y1: T, y2: H - B, stroke: "#8b91a5", "stroke-dasharray": "3 3", visibility: "hidden"}, svg);
  const marks = series.map((s) => svgEl("circle", {r: 5, fill: s.color, stroke: "#11141c", "stroke-width": 2,
    visibility: "hidden"}, svg));
  box.innerHTML = "";
  box.appendChild(svg);
  const tip = chartTip(box);
  const hide = () => {
    tip.hide();
    for (const el of [guide, ...marks]) el.setAttribute("visibility", "hidden");
  };
  svg.addEventListener("mouseleave", hide);
  svg.addEventListener("mousemove", (e) => {
    const pt = svg.createSVGPoint();
    pt.x = e.clientX;
    pt.y = e.clientY;
    const vx = pt.matrixTransform(svg.getScreenCTM().inverse()).x;
    const i = n <= 1 ? 0 : Math.round(((vx - L) * (n - 1)) / (W - L - R));
    if (i < 0 || i >= n) return hide();
    const lines = [];
    series.forEach((s, k) => {
      const p = s.points[i];
      const on = p != null && p.y != null;
      marks[k].setAttribute("visibility", on ? "visible" : "hidden");
      if (!on) return;
      marks[k].setAttribute("cx", x(i));
      marks[k].setAttribute("cy", y(p.y));
      lines.push(swatch(s.color) + esc(p.tip ?? `${s.name ? `${s.name}  ` : ""}${yFmt(p.y)}`));
    });
    guide.setAttribute("x1", x(i));
    guide.setAttribute("x2", x(i));
    guide.setAttribute("visibility", "visible");
    const head = xTip?.(i);
    if (head == null && !lines.length) return hide();
    tip.show([head != null ? `<b>${esc(head)}</b>` : null, ...lines].filter((v) => v != null).join("<br>"), e);
  });
  if (series.length > 1 || series[0].name) {
    box.insertAdjacentHTML("beforeend", `<div class="legend">${series.filter((s) => s.name)
      .map((s) => `<span><i style="background:${s.color}"></i>${esc(s.name)}</span>`).join("")}</div>`);
  }
}

// bars: [{label, tip, parts: [{value, color, name}]}]
function barChart(box, bars, {height = 200, normalize = false, horizontal = false, legend = null} = {}) {
  if (!bars.length) { box.innerHTML = `<p class="dim">${t("データがありません")}</p>`; return; }
  const totals = bars.map((b) => b.parts.reduce((a, p) => a + p.value, 0));
  const max = normalize ? 1 : Math.max(1, ...totals);
  const svg = horizontal ? hBars(bars, totals, max, normalize) : vBars(bars, totals, max, normalize, height);
  box.innerHTML = "";
  box.appendChild(svg);
  // hover: the bar's tip (a horizontal bar: label and tip), then its parts when it is split into several
  const tip = chartTip(box);
  svg.addEventListener("mouseleave", () => tip.hide());
  svg.addEventListener("mousemove", (e) => {
    const g = e.target.closest("[data-bar]");
    if (!g) return tip.hide();
    const i = +g.dataset.bar, b = bars[i];
    const head = horizontal ? [b.label, b.tip ?? totals[i]].filter((v) => v != null).join("  ") : b.tip ?? b.label;
    const parts = b.parts.filter((p) => p.value > 0);
    const lines = b.parts.length > 1 ? parts.map((p) => swatch(p.color) + esc(`${p.name}  ${p.value}` +
      (normalize && totals[i] ? ` (${((p.value * 100) / totals[i]).toFixed(1)}%)` : ""))) : [];
    tip.show([head != null ? `<b>${esc(String(head))}</b>` : null, ...lines].filter((v) => v != null).join("<br>"), e);
  });
  if (legend) box.insertAdjacentHTML("beforeend", `<div class="legend">${legend
    .map(([name, color]) => `<span><i style="background:${cssColor(color)}"></i>${esc(name)}</span>`).join("")}</div>`);
}

function rainbowDefs(svg) {
  const g = svgEl("linearGradient", {id: "fcgrad", x1: 0, y1: 0, x2: 1, y2: 0}, svgEl("defs", {}, svg));
  RAINBOW.forEach((c, i) => svgEl("stop", {offset: `${(i * 100) / (RAINBOW.length - 1)}%`, "stop-color": c}, g));
}

function vBars(bars, totals, max, normalize, H) {
  const W = 800, L = 44, R = 8, T = 8, B = 22;
  const svg = svgEl("svg", {viewBox: `0 0 ${W} ${H}`});
  rainbowDefs(svg);
  const bw = (W - L - R) / bars.length;
  const y = (v) => T + (H - T - B) * (1 - v / max);
  for (const t of normalize ? [0, 0.5, 1] : niceTicks(0, max, 4)) {
    svgEl("line", {x1: L, x2: W - R, y1: y(t), y2: y(t), stroke: "#2a2f3f"}, svg);
    svgEl("text", {x: L - 6, y: y(t) + 4, "text-anchor": "end", fill: "#8b91a5", "font-size": 11}, svg,
      normalize ? `${t * 100}%` : t);
  }
  bars.forEach((b, i) => {
    let acc = 0;
    const g = svgEl("g", {"data-bar": i}, svg);
    svgEl("rect", {x: L + i * bw, width: bw, y: T, height: H - T - B, fill: "transparent"}, g); // hover target
    for (const p of b.parts) {
      const v = normalize ? p.value / (totals[i] || 1) : p.value;
      if (v <= 0) continue;
      svgEl("rect", {x: L + i * bw + bw * 0.12, width: Math.max(1, bw * 0.76), y: y(acc + v),
        height: y(acc) - y(acc + v), fill: p.color}, g);
      acc += v;
    }
    if (b.label != null) {
      const cx = L + i * bw + bw / 2;
      svgEl("text", {x: cx, y: H - 6, "text-anchor": cx > W - R - 30 ? "end" : "middle", fill: "#8b91a5",
        "font-size": 10}, svg, b.label);
    }
  });
  return svg;
}

function hBars(bars, totals, max, normalize) {
  const W = 800, L = 44, R = 60, rowH = 22, H = bars.length * rowH + 6;
  const svg = svgEl("svg", {viewBox: `0 0 ${W} ${H}`});
  rainbowDefs(svg);
  bars.forEach((b, i) => {
    const top = i * rowH + 3;
    svgEl("text", {x: L - 8, y: top + 15, "text-anchor": "end", fill: "#c9cedb", "font-size": 12}, svg, b.label);
    let acc = 0;
    const g = svgEl("g", {"data-bar": i}, svg);
    svgEl("rect", {x: 0, y: top - 2, width: W, height: rowH, fill: "transparent"}, g); // hover target
    for (const p of b.parts) {
      const v = normalize ? p.value / (totals[i] || 1) : p.value / max;
      if (v <= 0) continue;
      svgEl("rect", {x: L + acc * (W - L - R), y: top, width: v * (W - L - R), height: rowH - 5, fill: p.color}, g);
      acc += v;
    }
    svgEl("text", {x: W - R + 6, y: top + 15, fill: "#8b91a5", "font-size": 11}, svg, b.tip ?? totals[i]);
  });
  return svg;
}

// EX lost in each of the 64 ghost buckets (the notes split in judgment order, see db.ghost_bucket_sizes).
// sizes: notes per bucket; lost: EX lost per bucket (may be an average); reached: buckets played.
function sectionChart(box, sizes, lost, reached = 64) {
  const color = (rate) => (rate >= 8 / 9 ? "#ffd23f" : rate >= 7 / 9 ? "#8b91a5" : rate >= 6 / 9 ? "#7ee07e" : "#ff5a5a");
  let first = 1;
  const bars = sizes.map((s, i) => {
    const from = first;
    first += s;
    const range = s ? t("ノーツ {0}–{1}", from, from + s - 1) : t("ノーツなし");
    if (i >= reached) return {label: null, tip: `${range}: ${t("未到達")}`, parts: [{value: 2 * s, color: "#2a2f3f", name: t("未到達")}]};
    const rate = s ? (2 * s - lost[i]) / (2 * s) : 1;
    return {label: i % 16 === 0 ? `${Math.round((i * 100) / 64)}%` : null,
      tip: `${range}: ${lost[i] > 0 ? `-${+lost[i].toFixed(1)}` : "0"} (${(rate * 100).toFixed(1)}%)`,
      parts: [{value: lost[i], color: color(rate), name: t("取りこぼし")}]};
  });
  barChart(box, bars, {height: 160, legend: [[t("AAA 以上"), "#ffd23f"], ["AA", "#8b91a5"], ["A", "#7ee07e"], [t("A 未満"), "#ff5a5a"]]});
}

// music_play_log chattering_log: presses of the same key 1..8 frames after the previous press
// (bm2dx Judge_CountChatter, Lightning Model only). counts: 14 rows (1P keys 1-7, 2P keys 1-7) x 8.
function chatterTable(counts) {
  const sides = [0, 1].filter((s) => counts.slice(s * 7, s * 7 + 7).some((r) => r.some((v) => v)));
  if (!sides.length) return `<p class="dim">${t("検出なし")}</p>`;
  const max = Math.max(...counts.flat());
  const cell = (v) => `<td class="num"${v ? ` style="background:rgba(255,90,90,${(0.15 + (0.6 * v) / max).toFixed(2)})"` : ""}>${v || ""}</td>`;
  return `<div class="table-wrap"><table><thead><tr><th>${t("鍵盤")}</th>${[1, 2, 3, 4, 5, 6, 7, 8]
    .map((f) => `<th class="num">${f}F</th>`).join("")}<th class="num">${t("計")}</th></tr></thead><tbody>
    ${sides.flatMap((s) => counts.slice(s * 7, s * 7 + 7).map((r, k) => `<tr><td>${t("{0}P 鍵盤{1}", s + 1, k + 1)}</td>${r.map(cell).join("")}
      <td class="num">${r.reduce((a, b) => a + b, 0)}</td></tr>`)).join("")}</tbody></table></div>`;
}

function worstSections(sizes, lost, reached = 64, n = 3) {
  let first = 1;
  const rows = sizes.map((s, i) => { const r = {i, from: first, to: first + s - 1, lost: lost[i]}; first += s; return r; })
    .filter((r) => r.i < reached && r.lost > 0).sort((a, b) => b.lost - a.lost).slice(0, n);
  const total = sizes.reduce((a, b) => a + b, 0);
  return rows.map((r) => t("ノーツ {0}–{1} ({2}–{3}%) で -{4}", r.from, r.to, Math.round(((r.from - 1) * 100) / total),
    Math.round((r.to * 100) / total), +r.lost.toFixed(1))).join(SEP);
}

function radarChart(box, values, max = 200) {
  const S = 300, C = S / 2, Rr = 105;
  const svg = svgEl("svg", {viewBox: `0 0 ${S} ${S}`, style: "max-width:320px;margin:auto"});
  const pt = (i, r) => [C + r * Math.sin((i * Math.PI) / 3), C - r * Math.cos((i * Math.PI) / 3)];
  for (const f of [0.25, 0.5, 0.75, 1]) {
    svgEl("polygon", {points: RADAR.map((_, i) => pt(i, Rr * f).join(",")).join(" "), fill: "none",
      stroke: "#2a2f3f"}, svg);
  }
  RADAR.forEach((name, i) => {
    const [x, y] = pt(i, Rr + 22);
    svgEl("text", {x, y: y + 4, "text-anchor": "middle", fill: "#c9cedb", "font-size": 11}, svg,
      `${name} ${values[i].toFixed(2)}`);
  });
  svgEl("polygon", {points: values.map((v, i) => pt(i, Rr * Math.min(1, v / max)).join(",")).join(" "),
    fill: "rgba(92,200,255,.25)", stroke: "#5cc8ff", "stroke-width": 2}, svg);
  box.innerHTML = "";
  box.appendChild(svg);
}

// ---------------------------------------------------------------- routing

const routes = [
  [/^#?\/?$/, viewHome],
  [/^#\/songs/, viewSongs],
  [/^#\/player\/?(.*)$/, viewPlayer],
  [/^#\/chart\/(\d+)\/(\d+)/, viewChart],
  [/^#\/musicdb/, viewMusicDb],
  [/^#\/categories/, viewCategories],
  [/^#\/tiers/, viewTiers],
  [/^#\/settings/, viewSettings],
];

function hashParams() {
  const q = location.hash.split("?")[1] || "";
  return Object.fromEntries(new URLSearchParams(q));
}

async function render() {
  $("#top").classList.remove("open"); // the phone menu closes on every page change
  $("#menu-btn").setAttribute("aria-expanded", false);
  const hash = location.hash || "#/";
  const base = hash.split("?")[0];
  if (base !== render.last) window.scrollTo(0, 0);
  render.last = base;
  for (const a of document.querySelectorAll("#nav a")) {
    const target = a.getAttribute("href");
    a.classList.toggle("active", target === "#/" ? hash === "#/" || hash === "" : hash.startsWith(target));
  }
  const view = $("#view");
  for (const [re, fn] of routes) {
    const m = hash.split("?")[0].match(re);
    if (m) {
      try {
        await fn(view, ...m.slice(1));
      } catch (e) {
        view.innerHTML = `<div class="banner">${t("読み込みに失敗しました: {0}", esc(e.message))}</div>`;
      }
      return;
    }
  }
  view.innerHTML = `<p>${t("ページが見つかりません")}</p>`;
}

async function loadPlayers() {
  state.players = await api("/api/players");
  if (!state.players.some((p) => p.key === state.player)) state.player = state.players[0]?.key || "";
  const sel = $("#player");
  sel.innerHTML = state.players.length
    ? state.players.map((p) => `<option value="${esc(p.key)}">${esc(playerName(p.key))}</option>`).join("")
    : `<option value="">${t("(まだ記録がありません)")}</option>`;
  sel.value = state.player;
  await Promise.all([loadDbs(), loadSets()]);
}

// the 2dxtra chart sets (the picker shows only when there are some); keeps the chosen one if it still exists
async function loadSets() {
  state.sets = (await api("/api/chartsets")).map((s) => s.name);
  if (!state.sets.includes(state.set)) setSet("");
  $("#set").innerHTML = `<option value="">${t("通常の譜面")}</option>` +
    state.sets.map((n) => `<option value="${esc(n)}">${esc(n)}</option>`).join("");
  $("#set").value = state.set;
  $("#set-pick").hidden = !state.sets.length;
}

function setSet(name) {
  state.set = name;
  localStorage.setItem("set", name);
  $("#set").value = name;
}

// the music database list; keeps the chosen one, else the player's default (their latest login's)
async function loadDbs(reset = false) {
  state.dbs = await api("/api/musicdbs");
  if (reset || !state.dbs.some((d) => String(d.id) === state.db)) {
    const c = await api(`/api/context?key=${encodeURIComponent(state.player)}`);
    state.db = c.db == null ? "" : String(c.db);
  }
  localStorage.setItem("db", state.db);
  const sel = $("#db");
  sel.innerHTML = state.dbs.length
    ? state.dbs.map((d) => `<option value="${d.id}">${esc(d.name)}</option>`).join("")
    : `<option value="">${t("(未登録)")}</option>`;
  sel.value = state.db;
}

function setPlayer(key) {
  state.player = key;
  localStorage.setItem("player", key);
  $("#player").value = key;
}

// query parameters for views about the current player and music database
const ctx = (extra = {}) => new URLSearchParams({key: state.player, db: state.db, set: state.set, ...extra});

// ---------------------------------------------------------------- views

function playsTable(plays, {showSong = true, showPlayer = false} = {}) {
  if (!plays.length) return `<p class="dim">${t("まだプレイがありません")}</p>`;
  return `<div class="table-wrap"><table><thead><tr>
    <th>${t("日時")}</th>${showPlayer ? `<th>${t("プレイヤー")}</th>` : ""}${showSong ? `<th>${pick("曲", "Song")}</th>` : ""}<th>${t("譜面")}</th>
    <th></th><th class="num">EX</th><th class="num">BP</th><th>${t("オプション")}</th><th>${t("筐体")}</th></tr></thead><tbody>
    ${plays.map((p) => `<tr class="click" data-href="${chartHref(p.music_id, p.chart, p.chart_set)}">
      <td>${fmtDate(p.played_at)}${p.analyzed ? `<span class="pill analyze" title="${t("判定の詳細あり: 鍵盤ごとの判定と FAST/SLOW (譜面画面のプレイ履歴で行をクリック)")}">${t("アナライズ")}</span>` : ""}</td>
      ${showPlayer ? `<td>${esc(p.card_id ? playerName(p.card_id) : `IIDX ${p.iidx_id}`)}</td>` : ""}
      ${showSong ? `<td class="title">${esc(p.title ?? `#${p.music_id}`)}</td>` : ""}
      <td>${chartTag(p.chart)}${setPill(p.chart_set)} <span class="dim">${p.level ?? ""}</span></td>
      <td>${lampBox(p.clear)} <span class="small">${LAMPS[p.clear ?? 0]}</span></td>
      <td class="num">${p.ex_score ?? "-"}</td>
      <td class="num">${p.miss_count >= 0 ? p.miss_count : "-"}</td>
      <td class="small">${optionText(p)}</td>
      <td>${cabinetPills(p)}</td></tr>`).join("")}
    </tbody></table></div>`;
}

const cabinetPills = (p) => [
  p.cabinet ? `<span class="pill ${p.cabinet === "TDJ" ? "tdj" : ""}">${p.cabinet}</span>` : "",
  p.omni ? `<span class="pill omni">omni</span>` : "",
  p.dxtra ? `<span class="pill dxtra" title="${t("2dxtra を入れた状態のプレイ")}">2dxtra</span>` : "",
].join(" ");

function bindRowLinks(root) {
  root.querySelectorAll("tr[data-href]").forEach((tr) =>
    tr.addEventListener("click", (e) => {
      if (e.target.closest("input,button,a,select")) return;
      location.hash = tr.dataset.href;
    }));
}

async function viewHome(view) {
  const [status, recent, unassigned] = await Promise.all([api("/api/status"), api("/api/recent"), api("/api/musicdata/unassigned")]);
  const st = status.stats;
  const banner = !status.upstream_ok
    ? `<div class="banner">${t("中継先サーバーが未設定です。{0}で本来の接続先 (asphyxia など) を指定してください。", `<a href="#/settings">${t("設定")}</a>`)}</div>`
    : st.last_request
      ? `<div class="banner ok">${t("中継中: {0}", esc(status.config.upstream))}${SEP}${t("最終通信 {0}", fmtDate(st.last_request))}</div>`
      : `<div class="banner">${t("まだゲームからの通信がありません。ゲームの接続先を {0} にしてください (別の PC のゲームは{1}を参照)。", mono(esc(gameUrls(status)[0][1])), `<a href="#/settings">${t("設定")}</a>`)}</div>`;
  const waiting = unassigned.reduce((n, f) => n + f.plays, 0);
  view.innerHTML = `${banner}
    ${waiting ? `<div class="banner">${t("まだ取り込んでいない曲データでのプレイが {0} 件あります。{1}でその曲データを取り込んでください。", waiting, `<a href="#/musicdb">${t("曲DB 画面")}</a>`)}</div>` : ""}
    <div class="panel"><div class="stats">
      <div class="stat"><b>${status.counts.plays}</b><span>${t("プレイ")}</span></div>
      <div class="stat"><b>${status.counts.cards}</b><span>${t("カード")}</span></div>
      <div class="stat"><b>${status.counts.sessions}</b><span>${t("セッション")}</span></div>
      <div class="stat"><b>${status.counts.songs}</b><span>${t("曲DB登録")}</span></div>
      <div class="stat"><b>${st.requests}</b><span>${t("中継リクエスト (起動後)")}</span></div>
      ${st.refused ? `<div class="stat"><b style="color:var(--bad)">${st.refused}</b><span>${t("omni 転送拒否")}</span></div>` : ""}
      ${st.errors ? `<div class="stat" title="${esc(st.last_error)}"><b style="color:var(--warn)">${st.errors}</b><span>${t("記録エラー")}</span></div>` : ""}
    </div></div>
    <div class="panel"><h2>${pick("プレイヤー", "Players")}</h2><div class="cards">${state.players.map((p) => {
      const pr = p.profile;
      return `<div class="card ${p.key === state.player ? "current" : ""}" data-key="${esc(p.key)}">
        <div class="name">${esc(pr ? pr.name : `IIDX ID ${p.iidx_id}`)}</div>
        <div class="dim small">${pr ? `${esc(pr.iidx_id)}${SEP}${esc(prefName(pr.pid))}` : t("カード不明 (ログイン前から記録)")}</div>
        ${pr ? `<div class="small">SP ${danName(pr.sgid)}${SEP}DP ${danName(pr.dgid)}</div>` : ""}
        <div class="small dim">${t("{0} プレイ ・ LDJ {1} / TDJ {2} ・ 最終 {3}", p.plays, p.ldj || 0, p.tdj || 0, fmtDate(p.last_played, false))}</div>
        ${p.card_id ? `<div class="small dim mono">${esc(p.card_id)}</div>` : ""}
      </div>`;
    }).join("") || `<p class="dim">${t("まだ記録がありません")}</p>`}</div></div>
    <div class="panel"><h2>${t("最近のプレイ")}</h2>${playsTable(recent, {showPlayer: true})}</div>`;
  view.querySelectorAll(".card[data-key]").forEach((c) => c.addEventListener("click", () => {
    setPlayer(c.dataset.key);
    location.hash = `#/player/${encodeURIComponent(c.dataset.key)}`;
  }));
  bindRowLinks(view);
}

async function viewPlayer(view, keyPart) {
  const key = keyPart ? decodeURIComponent(keyPart) : state.player;
  if (!key) { view.innerHTML = `<p class="dim">${t("まだ記録がありません")}</p>`; return; }
  if (key !== state.player) setPlayer(key);
  const d = await api(`/api/player?${new URLSearchParams({key, db: state.db, set: state.set})}`);
  const cur = d.profiles[d.profiles.length - 1];
  const last = d.sessions[d.sessions.length - 1];
  // DJ POINT as the game computed it: pc.get at login, replaced by pc.save at card out (the game's charts only)
  const withDjp = state.set ? [] : d.sessions.filter((s) => s.djpoint_sp != null || s.djpoint_dp != null);
  const gameDjp = withDjp[withDjp.length - 1];
  view.innerHTML = `
    <h1>${esc(cur ? cur.name : `IIDX ID ${key.slice(5)}`)} <span class="dim small">${cur ? esc(cur.iidx_id) : ""}</span>${setPill(state.set)}</h1>
    <div class="grid">
      <div class="panel"><h2>${t("プロフィール")}</h2>
        ${cur ? `<table>
          <tr><td class="dim">${t("カードID")}</td><td class="mono">${esc(cur.card_id)}</td></tr>
          <tr><td class="dim">${t("地域")}</td><td>${esc(prefName(cur.pid))}</td></tr>
          <tr><td class="dim">${t("段位")}</td><td>SP ${danName(cur.sgid)}${SEP}DP ${danName(cur.dgid)}</td></tr>
          ${gameDjp ? `<tr><td class="dim">DJ POINT</td><td>SP ${gameDjp.djpoint_sp ?? "-"}${SEP}DP ${gameDjp.djpoint_dp ?? "-"}
            <span class="dim small">(${t("{0} にゲームが計算した値", fmtDate(gameDjp.saved_at || gameDjp.started_at))})</span></td></tr>` : ""}
          <tr><td class="dim">${gameDjp ? t("DJ POINT (記録から計算)") : "DJ POINT"}</td><td>SP ${d.djpoint.SP.total}${SEP}DP ${d.djpoint.DP.total}${
            d.djpoint.SP.unknown + d.djpoint.DP.unknown ? ` <span class="dim small" title="${t("曲DB画面でノーツ数を取り込むと計算に入ります")}">(${t("ノーツ数不明の {0} 譜面を除く", d.djpoint.SP.unknown + d.djpoint.DP.unknown)})</span>` : ""}</td></tr>
          ${last ? `<tr><td class="dim">${t("アリーナ")}</td><td>SP ${arenaName(last.arena_sp)}${SEP}DP ${arenaName(last.arena_dp)}</td></tr>
          <tr><td class="dim">${t("プレイ回数")}</td><td>SP ${last.sp_plays ?? "-"}${SEP}DP ${last.dp_plays ?? "-"}</td></tr>
          <tr><td class="dim">${t("最終ログイン")}</td><td>${fmtDate(last.started_at)} ${cabinetPills(last)}</td></tr>
          <tr><td class="dim">${t("サーバー")}</td><td class="mono small">${esc(last.upstream)}</td></tr>` : ""}
        </table>` : `<p class="dim">${t("このプレイヤーのログインはまだ記録されていません (トラッカー起動前にログインした)。")}</p>`}
      </div>
      <div class="panel"><h2>${t("ノーツレーダー")} <select id="radar-style"><option value="sp">SP</option><option value="dp">DP</option></select></h2>
        <p class="dim small">${state.set ? t("記録したプレイから、ゲームと同じ式で計算した値 (ゲームの値は 2dxtra を起動してからのプレイしか知らない)") : t("ゲームが計算した値。最後のプレイが 2dxtra の譜面だったクレジットの値は、その譜面セットのものなので除く")}</p>
        <div id="radar" class="chart-box"></div></div>
    </div>
    ${d.profiles.length > 1 ? `<div class="panel"><h2>${t("プロフィール履歴")}</h2><div class="table-wrap"><table>
      <thead><tr><th>${t("期間")}</th><th>${t("名前")}</th><th>IIDX ID</th><th>${t("地域")}</th><th>${t("SP段位")}</th><th>${t("DP段位")}</th><th>${t("サーバー")}</th></tr></thead>
      <tbody>${d.profiles.map((p) => `<tr><td>${t("{0} 〜 {1}", fmtDate(p.first_seen, false), fmtDate(p.last_seen, false))}</td>
        <td>${esc(p.name)}</td><td>${esc(p.iidx_id)}</td><td>${esc(prefName(p.pid))}</td>
        <td>${danName(p.sgid)}</td><td>${danName(p.dgid)}</td><td class="mono small">${esc(p.upstream)}</td></tr>`).join("")}
      </tbody></table></div></div>` : ""}
    <div class="panel"><h2>${t("クリアランプ分布")} <select id="lamp-style"><option>SP</option><option>DP</option></select></h2>
      <div id="lamps" class="chart-box"></div></div>
    <div class="grid">
      <div class="panel"><h2>${t("日別プレイ数 (180日)")}</h2><div id="per-day" class="chart-box"></div></div>
      ${state.set ? "" : `<div class="panel"><h2>${t("レーダー推移")} <span class="dim small">${t("合計値")}</span></h2><div id="radar-history" class="chart-box"></div></div>`}
    </div>
    ${withDjp.length ? `<div class="panel"><h2>${t("DJ POINT 推移")} <select id="djp-style"><option value="sp">SP</option><option value="dp">DP</option></select>
      <span class="dim small">${t("ゲームが計算した値 (ログインごと)")}</span></h2>
      <div id="djp-history" class="chart-box"></div></div>` : ""}
    ${d.chatter ? `<div class="panel"><h2>${t("鍵盤のチャタリング")} <span class="dim small">${t("直近 {0} プレイ", d.chatter.plays)}</span></h2>
      <p class="dim small">${t("同じ鍵盤を 1〜8 フレーム以内 (120fps で 1 フレーム ≒ 8.3ms) に押し直した回数。ゲームが LIGHTNING MODEL でだけ数えている値。人が同じ鍵盤を叩き直せる間隔より短いので、特定の鍵盤に偏って多いときはスイッチの劣化を疑う目安になる。")}</p>
      ${chatterTable(d.chatter.counts)}</div>` : ""}
    <div class="panel"><h2>${t("最近のプレイ")}</h2>${playsTable(d.recent)}</div>`;

  const radarOf = (s, style) => (s && s[`radar_${style}`] ? s[`radar_${style}`].split(/\s+/).map((v) => +v / 100) : null);
  const drawRadar = () => {
    const style = $("#radar-style").value;
    if (state.set) { // computed from the set's bests (the logins' values belong to whichever set a credit ended on)
      const vals = (d.set_radar || {})[style.toUpperCase()];
      if (vals) radarChart($("#radar"), RADAR_ATTR.map((a) => vals[a] / 100));
      else $("#radar").innerHTML = `<p class="dim">${d.set_charts ? t("データがありません") : t("曲DB 画面で 2dxtra.sqlite を取り込むと、この譜面セットのノーツレーダーを計算できます")}</p>`;
      return;
    }
    // the game's value, from the logins whose radar belongs to the game's charts
    const withRadar = d.sessions.filter((s) => radarOf(s, style) && s[`radar_${style}_set`] == null);
    const vals = radarOf(withRadar[withRadar.length - 1], style);
    if (vals) radarChart($("#radar"), RADAR_ATTR.map((a) => vals[a]));
    else $("#radar").innerHTML = `<p class="dim">${t("データがありません")}</p>`;
    lineChart($("#radar-history"), [{color: "#5cc8ff", points: withRadar.map((s) => {
      const total = radarOf(s, style).reduce((a, b) => a + b, 0);
      return {y: +total.toFixed(2), tip: `${fmtDate(s.started_at)}  ${total.toFixed(2)}`};
    })}], {yFmt: (v) => v.toFixed(0)});
  };
  const drawLamps = () => {
    const per = d.lamps[$("#lamp-style").value] || {};
    const levels = Object.keys(per).map(Number).filter((l) => l > 0).sort((a, b) => b - a);
    barChart($("#lamps"), levels.map((lv) => {
      const counts = per[lv];
      const total = counts.reduce((a, b) => a + b, 0);
      const cleared = counts.slice(2).reduce((a, b) => a + b, 0);
      return {label: `☆${lv}`, tip: `${cleared}/${total}`,
        parts: [7, 6, 5, 4, 3, 2, 1, 0].map((c) => ({value: counts[c], color: LAMP_COLORS[c], name: LAMPS[c]}))};
    }), {horizontal: true, normalize: true, legend: [7, 6, 5, 4, 3, 2, 1, 0].map((c) => [LAMPS[c], LAMP_COLORS[c]])});
  };
  const drawDjp = () => {
    const key = `djpoint_${$("#djp-style").value}`;
    lineChart($("#djp-history"), [{color: "#ffd35c", points: withDjp.filter((s) => s[key] != null).map((s) =>
      ({y: s[key], tip: `${fmtDate(s.saved_at || s.started_at)}  ${s[key]}`}))}], {yFmt: (v) => v.toFixed(0)});
  };
  if (withDjp.length) {
    $("#djp-style").addEventListener("change", drawDjp);
    drawDjp();
  }
  $("#radar-style").addEventListener("change", drawRadar);
  $("#lamp-style").addEventListener("change", drawLamps);
  drawRadar();
  drawLamps();

  const days = [];
  const byDay = Object.fromEntries(d.per_day.map((r) => [r.day, r.plays]));
  for (let i = 179; i >= 0; i--) {
    const day = new Date(Date.now() - i * 86400000);
    const k = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`;
    days.push({label: i % 30 === 0 ? k.slice(5) : null, tip: `${k}: ${byDay[k] || 0}`,
      parts: [{value: byDay[k] || 0, color: "#5cc8ff", name: t("プレイ")}]});
  }
  barChart($("#per-day"), days, {height: 180});
  bindRowLinks(view);
}

async function viewSongs(view) {
  const q = hashParams();
  const style = q.style || localStorage.getItem("songs.style") || "SP";
  const level = q.level ?? localStorage.getItem("songs.level") ?? "12";
  const category = q.category ?? "";
  const search = q.q ?? "";
  const set = state.set;
  const params = ctx({style, level, category, q: search});
  const songs = api(`/api/songs?${params}`);
  songs.catch(() => {}); // reported where it is awaited
  view.innerHTML = `<p class="dim">${t("読み込み中…")}</p>`;
  const cats = await api(`/api/categories?${ctx()}`);
  localStorage.setItem("songs.style", style);
  localStorage.setItem("songs.level", level);

  const catOptions = [
    `<option value="">${t("すべて")}</option>`,
    `<optgroup label="${t("バージョン")}">${cats.versions.map((c) => `<option value="${c.key}">${esc(c.name)}</option>`).join("")}</optgroup>`,
    cats.auto.length ? `<optgroup label="${t("曲DB")}">${cats.auto.map((c) => `<option value="${c.key}">${esc(catName(c))}</option>`).join("")}</optgroup>` : "",
    cats.manual.length ? `<optgroup label="${t("手動カテゴリ")}">${cats.manual.map((c) => `<option value="m:${c.id}">${esc(c.name)} (${c.songs})</option>`).join("")}</optgroup>` : "",
  ].join("");

  view.innerHTML = `
    <div class="panel"><div class="row">
      <select id="f-style"><option>SP</option><option>DP</option></select>
      <select id="f-level"><option value="">${t("全レベル")}</option>${[12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1]
        .map((l) => `<option value="${l}">☆${l}</option>`).join("")}</select>
      <select id="f-cat">${catOptions}</select>
      <input id="f-q" placeholder="${t("曲名・アーティスト・ID")}" value="${esc(search)}">
      <label class="dim"><input type="checkbox" id="f-played"> ${t("プレイ済みのみ")}</label>
      <label class="dim" title="${t("オフ: 曲ごとに 1 行 (譜面は行の中で切り替え)")}"><input type="checkbox" id="f-split"> ${t("難易度ごとに分ける")}</label>
      <span class="dim" id="count"></span>
    </div>
    <div class="row" style="margin-top:10px">
      <span class="dim small">${t("選択した曲を")}</span>
      <select id="bulk-cat">${cats.manual.map((c) => `<option value="${c.id}">${esc(c.name)}</option>`).join("") || `<option value="">${t("(カテゴリ未作成)")}</option>`}</select>
      <button id="bulk-add">${t("に追加")}</button><button id="bulk-remove">${t("から外す")}</button>
      <a href="#/categories" class="small">${t("カテゴリ管理")}</a>
    </div></div>
    <div class="panel"><div class="table-wrap"><table id="songs"><thead><tr>
      <th><input type="checkbox" id="sel-all"></th>
      <th class="sort" data-k="level">Lv</th><th class="sort" data-k="tier_sort" title="${t("難易度表のランク: SP☆12 はノマゲ / ハード参考表（* は個人差）、DP は非公式難易度")}">${t("難易度")}</th>
      <th class="sort" data-k="title">${t("タイトル")}</th><th>${t("譜面")}</th>
      <th class="sort" data-k="best_clear">${t("ランプ")}</th><th class="sort num" data-k="best_ex">EX</th>
      <th class="sort num" data-k="rate">${t("レート")}</th><th>DJ LEVEL</th><th class="sort num" data-k="best_miss">BP</th>
      <th class="sort num" data-k="djpoint" title="${t("DJ POINT (ベストEX・ベストランプ・DJ LEVEL から計算)")}">DJP</th>
      <th class="sort num" data-k="plays">${t("回数")}</th><th class="sort" data-k="version">${t("バージョン")}</th>
      <th class="sort" data-k="last_played">${t("最終プレイ")}</th></tr></thead>
      <tbody><tr><td colspan="13" class="dim">${t("読み込み中…")}</td></tr></tbody></table></div></div>`;

  $("#f-style").value = style;
  $("#f-level").value = level;
  $("#f-cat").value = category;
  const played = $("#f-played");
  played.checked = localStorage.getItem("songs.played") === "1";
  const split = $("#f-split");
  split.checked = localStorage.getItem("songs.split") === "1";
  // per-song rows show one chart; the user's pick per song, else ANOTHER > HYPER > NORMAL > BEGINNER > LEGGENDARIA
  const chosen = new Map();
  const PICK_ORDER = [3, 2, 1, 0, 4];
  const bySong = (list) => {
    const songs = new Map();
    for (const r of list) {
      if (!songs.has(r.music_id)) songs.set(r.music_id, []);
      songs.get(r.music_id).push(r);
    }
    return [...songs.values()].map((charts) => {
      charts.sort((a, b) => a.chart - b.chart);
      const want = chosen.get(charts[0].music_id);
      const shown = charts.find((c) => c.chart === want) ||
        PICK_ORDER.map((k) => charts.find((c) => c.chart % 5 === k)).find(Boolean);
      return {...shown, charts};
    });
  };
  const chips = (r) => r.charts.map((c) => `<button class="chart-chip c-${CHARTS[c.chart][2]}${c.chart === r.chart ? " on" : ""}"
    data-mid="${c.music_id}" data-chart="${c.chart}" title="${CHARTS[c.chart]} ☆${c.level}">${CHARTS[c.chart][2]}<small>${c.level}</small></button>`).join("");
  const go = () => {
    const p = new URLSearchParams({style: $("#f-style").value, level: $("#f-level").value,
      category: $("#f-cat").value, q: $("#f-q").value});
    location.hash = `#/songs?${p}`;
  };
  ["f-style", "f-level", "f-cat"].forEach((id) => $(`#${id}`).addEventListener("change", go));
  $("#f-q").addEventListener("keydown", (e) => e.key === "Enter" && go());

  const tbody = $("#songs tbody");
  const rows = await songs;
  if (!tbody.isConnected) return; // moved on meanwhile
  for (const r of rows) {
    r.rate = r.best_ex != null && r.notes ? r.best_ex / (r.notes * 2) : null;
    r.tier_sort = tierSort(r.tier);
  }
  const collator = new Intl.Collator("ja");
  const picked = new Set();
  let sortKey = localStorage.getItem("songs.sort") || "title", sortDir = 1, list = [], rowH = 0, shown = "";
  const rowHtml = (r) => `
      <tr class="click" data-href="${chartHref(r.music_id, r.chart, set)}">
        <td><input type="checkbox" class="sel" value="${r.music_id}"${picked.has(r.music_id) ? " checked" : ""}></td>
        <td class="num">${r.level}</td>
        <td class="small nowrap">${esc(tierText(r.tier))}</td>
        <td class="title">${esc(r.title ?? `#${r.music_id}`)}<div class="dim small">${esc(r.artist ?? "")}</div></td>
        <td>${r.charts ? chips(r) : chartTag(r.chart)}</td>
        <td>${lampBox(r.best_clear)}</td>
        <td class="num">${r.best_ex ?? ""}</td>
        ${djCell(r.best_ex, r.notes)}
        <td class="num">${r.best_miss ?? ""}</td>
        <td class="num">${djpText(r.djpoint)}</td>
        <td class="num">${r.plays ?? ""}</td>
        <td class="small">${esc(versionName(r.version))}</td>
        <td class="small">${r.last_played ? fmtDate(r.last_played, false) : ""}</td></tr>`;
  const pad = (n) => (n > 0 ? `<tr class="pad" style="height:${n * rowH}px"><td colspan="14"></td></tr>` : "");
  // Only the rows near the screen are in the page, the rest is blank space of the same height:
  // thousands of rows take the browser seconds to lay out.
  const paint = (force) => {
    if (!tbody.isConnected) return window.removeEventListener("scroll", onScroll);
    if (!rowH && list.length) {
      tbody.innerHTML = rowHtml(list[0]);
      rowH = tbody.firstElementChild.offsetHeight || 40;
    }
    const first = Math.max(0, Math.floor(-tbody.getBoundingClientRect().top / (rowH || 40)) - 30);
    const last = Math.min(list.length, first + Math.ceil(innerHeight / (rowH || 40)) + 60);
    if (!force && shown === `${first}:${last}`) return;
    shown = `${first}:${last}`;
    tbody.innerHTML = pad(first) + list.slice(first, last).map(rowHtml).join("") + pad(list.length - last);
  };
  const onScroll = () => paint(false);
  window.addEventListener("scroll", onScroll, {passive: true});
  const draw = () => {
    localStorage.setItem("songs.played", played.checked ? "1" : "0");
    localStorage.setItem("songs.split", split.checked ? "1" : "0");
    list = played.checked ? rows.filter((r) => r.plays) : rows.slice();
    if (!split.checked) list = bySong(list);
    list.sort((a, b) => {
      const x = a[sortKey], y = b[sortKey];
      if (x == null && y == null) return 0;
      if (x == null) return 1;
      if (y == null) return -1;
      return (typeof x === "string" ? collator.compare(x, y) : x - y) * sortDir;
    });
    $("#count").textContent = split.checked ? t("{0} 譜面", list.length) : t("{0} 曲", list.length);
    rowH = 0; // one song per row or one chart per row: measured again
    paint(true);
  };
  tbody.addEventListener("click", (e) => {
    const chip = e.target.closest(".chart-chip");
    if (chip) { // shows that chart in the row; the row itself links to the chart page
      chosen.set(+chip.dataset.mid, +chip.dataset.chart);
      return draw();
    }
    const tr = e.target.closest("tr[data-href]");
    if (tr && !e.target.closest("input,button,a,select")) location.hash = tr.dataset.href;
  });
  tbody.addEventListener("change", (e) => {
    if (e.target.classList.contains("sel")) picked[e.target.checked ? "add" : "delete"](+e.target.value);
  });
  played.addEventListener("change", draw);
  split.addEventListener("change", draw);
  view.querySelectorAll("th.sort").forEach((th) => th.addEventListener("click", () => {
    sortDir = sortKey === th.dataset.k ? -sortDir : (["title", "version"].includes(th.dataset.k) ? 1 : -1);
    sortKey = th.dataset.k;
    localStorage.setItem("songs.sort", sortKey);
    draw();
  }));
  $("#sel-all").addEventListener("change", (e) => {
    for (const r of list) picked[e.target.checked ? "add" : "delete"](r.music_id);
    paint(true);
  });
  const bulk = async (op) => {
    const id = $("#bulk-cat").value;
    const ids = [...picked];
    if (!id) return toast(t("先にカテゴリを作成してください"));
    if (!ids.length) return toast(t("曲を選択してください"));
    await post("/api/categories/update", {id: +id, [op]: ids});
    toast(op === "add" ? t("{0} 曲を追加しました", ids.length) : t("{0} 曲を除外しました", ids.length), true);
  };
  $("#bulk-add").onclick = () => bulk("add").catch((e) => toast(e.message));
  $("#bulk-remove").onclick = () => bulk("remove").catch((e) => toast(e.message));
  draw();
}

async function viewChart(view, mid, chart) {
  const set = hashParams().set || "";
  if (set !== state.set && (set === "" || state.sets.includes(set))) setSet(set);
  const d = await api(`/api/chart?${ctx({music: mid, chart, set})}`);
  const s = d.song;
  const plays = d.plays;
  const notes = d.notes;
  const best = {
    ex: Math.max(-1, ...plays.map((p) => p.ex_score ?? -1)),
    clear: Math.max(0, ...plays.map((p) => p.clear ?? 0)),
    miss: Math.min(Infinity, ...plays.map((p) => (p.miss_count >= 0 ? p.miss_count : Infinity))),
  };
  const dj = djLevel(best.ex, notes);
  view.innerHTML = `
    <div class="row" style="justify-content:space-between">
      <h1>${esc(s ? s.title : `#${mid}`)} ${chartTag(+chart)}${setPill(set)} <span class="dim">☆${s ? s.levels[chart] : plays[0]?.level ?? "?"}</span></h1>
      <button id="pick-game" title="${t("tracker_link.dll を入れたゲームが選曲画面にいるとき、この譜面にカーソルを合わせます (サブ画面の予約と同じ動き)")}">${t("ゲームでこの曲を選ぶ")}</button></div>
    <p class="dim">${esc(s ? `${s.artist}${SEP}${s.genre}${SEP}${versionName(s.version)}` : t("曲DB未登録"))}${SEP}ID ${mid}
     ${SEP}${t("ノーツ {0}", notes ?? t("不明"))}${d.notes_source === "observed" ? ` (${t("プレイから推定")})` : ""}${SEP}${esc(playerName(state.player))}</p>
    ${d.tier ? `<p class="dim small">${tierDetail(d.tier, d.tier_sources)}</p>` : ""}
    <div class="panel"><div class="stats">
      <div class="stat"><b>${lampBox(best.clear)} ${LAMPS[best.clear]}</b><span>${t("ベストランプ")}</span></div>
      <div class="stat"><b>${best.ex >= 0 ? best.ex : "-"}</b><span>${t("ベストEX")}</span></div>
      <div class="stat"><b>${dj ? `${(dj.rate * 100).toFixed(2)}%` : "-"}</b><span>${dj ? `${dj.grade} ${dj.diff}` : t("レート")}</span></div>
      <div class="stat"><b>${Number.isFinite(best.miss) ? best.miss : "-"}</b><span>${t("ベストBP")}</span></div>
      <div class="stat"><b>${d.djpoint != null ? djpText(d.djpoint) : "-"}</b><span>DJ POINT</span></div>
      <div class="stat"><b>${plays.length}</b><span>${t("プレイ回数")}</span></div>
    </div></div>
    ${plays.length ? `<div class="grid">
      <div class="panel"><h2>${t("EXスコア推移")}</h2><div id="g-ex" class="chart-box"></div></div>
      <div class="panel"><h2>${t("ミスカウント推移")}</h2><div id="g-bp" class="chart-box"></div></div>
    </div>
    <div class="grid">
      <div class="panel"><h2>${t("判定内訳")}</h2><div id="g-judge" class="chart-box"></div></div>
      <div class="panel"><h2>FAST / SLOW</h2><div id="g-fs" class="chart-box"></div></div>
    </div>` : ""}
    ${d.sections ? `<div class="panel"><h2>${t("区間ごとの取りこぼし")} <span class="dim small">${t("完走した直近 {0} プレイの平均 (ノーツ {1})", d.sections.plays, d.sections.notes)}</span></h2>
      <p class="dim small">${t("ノーツを判定順に 64 等分した区間ごとに、失った EX の平均 (ゲームのスコアグラフと同じ区切り)。いつも落としている場所が分かる。")}</p>
      <div id="g-sections" class="chart-box"></div>
      <p class="small">${t("多い区間: {0}", esc(worstSections(d.sections.sizes, d.sections.lost) || t("なし")))}</p></div>` : ""}
    <div class="panel"><h2>${t("プレイ履歴")} <span class="dim small">${t("行をクリックでゲージ・スコア推移")}</span></h2>
      <div class="table-wrap"><table id="hist"><thead><tr><th>${t("日時")}</th><th>${t("ランプ")}</th><th class="num">EX</th>
      <th class="num">${t("レート")}</th><th class="num">BP</th><th class="num">PG</th><th class="num">GR</th><th class="num">GD</th>
      <th class="num">BD</th><th class="num">PR</th><th class="num">CB</th><th class="num">F</th><th class="num">S</th>
      <th class="num">${t("進行")}</th><th class="num">DJP</th><th>${t("オプション")}</th><th>${t("ゲージ")}</th><th>${t("筐体")}</th></tr></thead><tbody>
      ${plays.slice().reverse().map((p) => `<tr class="click" data-id="${p.id}">
        <td>${fmtDate(p.played_at)}${p.analyzed ? `<span class="pill analyze" title="${t("判定の詳細あり: 鍵盤ごとの判定と FAST/SLOW (譜面画面のプレイ履歴で行をクリック)")}">${t("アナライズ")}</span>` : ""}</td>
        <td>${lampBox(p.clear)} <span class="small">${LAMPS[p.clear ?? 0]}</span></td>
        <td class="num">${p.ex_score ?? "-"}</td>
        <td class="num">${notes && p.ex_score != null ? ((p.ex_score / notes / 2) * 100).toFixed(2) + "%" : ""}</td>
        <td class="num">${p.miss_count >= 0 ? p.miss_count : "-"}</td>
        ${["pgreat", "great", "good", "bad", "poor", "combo_break", "fast", "slow"]
          .map((k) => `<td class="num">${p[k] ?? ""}</td>`).join("")}
        <td class="num">${p.progress != null ? `${(p.progress / 100).toFixed(p.progress % 100 ? 1 : 0)}%` : ""}</td>
        <td class="num">${djpText(p.djpoint)}</td>
        <td class="small">${optionText(p)}</td>
        <td class="small">${esc(tData(p.gauge) ?? "")}</td>
        <td>${cabinetPills(p)}</td></tr>`).join("")}
      </tbody></table></div></div>`;
  $("#pick-game").onclick = () => post("/api/pick", {music_id: +mid, chart: +chart})
    .then(() => toast(t("ゲームの選曲画面でこの譜面に合わせます"), true)).catch((e) => toast(e.message));

  if (!plays.length) return;
  const xLabels = [[0, fmtDate(plays[0].played_at, false)], [plays.length - 1, fmtDate(plays[plays.length - 1].played_at, false)]];
  const exSeries = [{name: "EX", color: "#5cc8ff", points: plays.map((p) => ({y: p.ex_score}))}];
  const playTip = (list) => (i) => `${fmtDate(list[i].played_at)}  ${LAMPS[list[i].clear ?? 0]}`;
  if (notes) {
    for (const [name, frac, color] of [["AAA", 8 / 9, "#ffd23f"], ["AA", 7 / 9, "#8b91a5"]]) {
      const v = Math.ceil(notes * 2 * frac);
      exSeries.push({name, color, dash: "4 4", noDots: true, points: plays.map(() => ({y: v}))});
    }
  }
  lineChart($("#g-ex"), exSeries, {xLabels, yMax: notes ? notes * 2 : undefined, xTip: playTip(plays)});
  lineChart($("#g-bp"), [{name: "BP", color: "#ff5a5a",
    points: plays.map((p) => ({y: p.miss_count >= 0 ? p.miss_count : null}))}],
    {xLabels, yMin: 0, xTip: playTip(plays)});
  const judged = plays.filter((p) => p.good != null);
  barChart($("#g-judge"), judged.map((p) => ({
    label: null, tip: fmtDate(p.played_at),
    parts: [["PG", p.pgreat], ["GR", p.great], ["GD", p.good], ["BD", p.bad], ["PR", p.poor]]
      .map(([n, v]) => ({name: n, value: v || 0, color: JUDGE_COLORS[n]})),
  })), {normalize: true, legend: Object.entries(JUDGE_COLORS)});
  lineChart($("#g-fs"), [
    {name: "FAST", color: "#5cc8ff", points: judged.map((p) => ({y: p.fast}))},
    {name: "SLOW", color: "#ff5ca8", points: judged.map((p) => ({y: p.slow}))},
  ], {yMin: 0, xTip: playTip(judged)});
  if (d.sections) sectionChart($("#g-sections"), d.sections.sizes, d.sections.lost);

  $("#hist").addEventListener("click", async (e) => {
    const tr = e.target.closest("tr[data-id]");
    if (!tr) return;
    const open = tr.nextElementSibling?.classList.contains("detail");
    view.querySelectorAll("tr.detail").forEach((r) => r.remove());
    if (open) return;
    const p = plays.find((x) => x.id === +tr.dataset.id);
    const g = await api(`/api/play?id=${p.id}`);
    // ghost buckets reached: all for a finished play, else up to the last one that earned EX
    const sizes = g.bucket_notes;
    const finished = p.progress === 10000 && !p.is_death;
    const reached = finished ? 64 : g.ghost.reduce((last, v, i) => (v ? i + 1 : last), 0);
    const lost = sizes ? sizes.map((s, i) => 2 * s - g.ghost[i]) : null;
    const target = g.target_score > 0 ? `${GRAPHS[g.graph_type] ?? `#${g.graph_type}`}${SEP}${t("目標 EX {0}", g.target_score)}` +
      (p.ex_score != null ? ` (${p.ex_score >= g.target_score ? "+" : ""}${p.ex_score - g.target_score})` : "") : null;
    const folder = folderName(g.folder_type, p.mode_type);
    const hasChatter = g.chatter && g.chatter.some((r) => r.some((v) => v));
    tr.insertAdjacentHTML("afterend", `<tr class="detail"><td colspan="18"><div class="grid">
      <div><h2>${t("ゲージ推移")}</h2><div class="chart-box" id="d-gauge"></div></div>
      <div><h2>${t("スコア推移 (累積EX)")}</h2><div class="chart-box" id="d-ghost"></div></div>
      ${lost ? `<div><h2>${t("区間ごとの取りこぼし")} <span class="dim small">${t("ノーツを判定順に 64 等分")}</span></h2>
        <div class="chart-box" id="d-sections"></div>
        <p class="small">${t("多い区間: {0}", esc(worstSections(sizes, lost, reached) || t("なし")))}</p></div>` : ""}
      <div><h2>${t("詳細")}</h2><table>
        <tr><td class="dim">${t("オプション")}</td><td>${optionText(p)}</td></tr>
        <tr><td class="dim">${t("ゲージ")}</td><td>${esc(tData(p.gauge) ?? "-")}</td></tr>
        <tr><td class="dim">${t("ハイスピード")}</td><td>${esc(p.hispeed ?? "-")}</td></tr>
        <tr><td class="dim" title="${t("RANDOM レーンチケットかレーンリトライを使ったときだけ、チケットと同じ 7 桁の並び")}">${t("レーンチケット")}</td>
          <td class="mono">${p.ran_arrange != null && p.ran_arrange >= 0 ? p.ran_arrange : "-"}</td></tr>
        <tr><td class="dim">${t("モード")}</td><td>${esc(tData(p.mode) ?? "-")}</td></tr>
        <tr><td class="dim">${t("ターゲット")}</td><td>${esc(target ?? "-")}</td></tr>
        <tr><td class="dim">${t("選曲フォルダ")}</td><td>${esc(folder ?? (g.folder_type != null ? `#${g.folder_type}` : "-"))}</td></tr>
        <tr><td class="dim">DJ POINT</td><td>${djpText(p.djpoint) || "-"}</td></tr>
        <tr><td class="dim">${t("生の値")}</td><td class="mono small">option1 0x${(p.option1 ?? 0).toString(16)}${SEP}option2 0x${(p.option2 ?? 0).toString(16)}${SEP}gauge_type ${p.gauge_type ?? "-"}</td></tr>
        <tr><td class="dim">${t("前回までのベスト")}</td><td>EX ${p.prev_best_score ?? "-"}${SEP}BP ${p.prev_best_miss >= 0 ? p.prev_best_miss : "-"}${SEP}${LAMPS[p.prev_best_clear ?? 0]}</td></tr>
        <tr><td class="dim">${t("プレイサイド")}</td><td>${p.play_side === 1 ? "2P" : "1P"}</td></tr>
        <tr><td class="dim">${t("ゲーム")}</td><td class="mono small">${esc(p.model)}</td></tr>
        <tr><td class="dim">${t("サーバー")}</td><td class="mono small">${esc(p.upstream)}</td></tr>
      </table></div>
      ${hasChatter ? `<div><h2>${t("鍵盤のチャタリング")} <span class="dim small">${t("1〜8 フレーム以内の押し直し")}</span></h2>${chatterTable(g.chatter)}</div>` : ""}
      ${g.judges ? judgePanel(g.judges, p) : ""}
      </div></td></tr>`);
    lineChart($("#d-gauge"), [{name: t("ゲージ"), color: "#7ee07e", area: true, noDots: true,
      points: g.gauge.map((v) => ({y: v}))}], {yMin: 0, yMax: 100, yFmt: (v) => `${+(+v).toFixed(1)}%`, height: 180,
      xTip: (i) => t("曲の {0}% 地点", Math.round((i * 100) / Math.max(1, g.gauge.length - 1)))});
    let acc = 0, notesSoFar = 0;
    const cum = g.ghost.map((v) => ({y: (acc += v)}));
    const series = [{name: t("このプレイ"), color: "#5cc8ff", noDots: true, points: cum}];
    const total = sizes ? g.notes : notes;
    if (total) series.push({name: t("AAA ペース"), color: "#ffd23f", dash: "4 4", noDots: true,
      points: cum.map((_, i) => {
        notesSoFar += sizes ? sizes[i] : total / cum.length;       // exact bucket sizes when known
        return {y: Math.ceil((notesSoFar * 2 * 8) / 9)};
      })});
    lineChart($("#d-ghost"), series, {yMin: 0, height: 180, xTip: (i) => sizes
      ? t("区間 {0}/64 (ノーツ {1} まで)", i + 1, sizes.slice(0, i + 1).reduce((a, b) => a + b, 0)) : t("区間 {0}/64", i + 1)});
    if (lost) sectionChart($("#d-sections"), sizes, lost, reached);
    if (g.judges?.measures?.length) lineChart($("#d-measures"), [{name: t("スコアレート"), color: "#ffd23f",
      points: g.judges.measures.map((v) => ({y: v}))}], {yMin: 0, yMax: 100, yFmt: (v) => `${v}%`, height: 180,
      xTip: (i) => t("{0} 小節目", i + 1) + (g.judges.measures[i] == null ? ` (${t("ノーツなし")})` : "")});
  });
}

// What tracker_link.dll added to the play's music.reg: judgments by timing (FAST/SLOW) and by key,
// and the score rate of each measure (drawn into #d-measures after the panel is placed).
// j.timing[lane side][11 display codes], j.lanes[lane side][8 lanes][PG, GR, GD, BD, POOR, empty POOR].
// An SP play uses its own lane side, a DP play both.
function judgePanel(j, p) {
  const sides = p.chart >= 5 ? [[0, t("左")], [1, t("右")]] : [[p.play_side === 1 ? 1 : 0, ""]];
  // display codes: FAST POOR, FAST BAD, FAST GOOD, FAST GREAT, PGREAT, SLOW GREAT, SLOW GOOD, SLOW BAD, SLOW POOR
  const tm = j.timing[0].map((_, c) => sides.reduce((sum, [s]) => sum + j.timing[s][c], 0));
  const rows = [["GREAT", 3, 5], ["GOOD", 2, 6], ["BAD", 1, 7], ["POOR", 0, 8]];
  const lanes = sides.flatMap(([s, name]) => j.lanes[s].map((k, lane) => ({name: name + (lane < 7 ? lane + 1 : t("皿")), k})));
  const laneRow = ({name, k}) => {
    const notes = k[0] + k[1] + k[2] + k[3] + k[4]; // empty POORs are no notes
    const lv = djLevel(2 * k[0] + k[1], notes);
    return `<tr><td class="dim">${name}</td>${k.map((v) => `<td class="num">${notes || v ? v : ""}</td>`).join("")}
      <td class="num">${lv ? `${(lv.rate * 100).toFixed(1)}%` : "-"}</td><td>${lv ? lv.grade : ""}</td></tr>`;
  };
  return `<div><h2>${t("判定の詳細")} <span class="dim small">${t("ゲームがサーバーに送らない値 (tracker_link.dll から)")}</span></h2>
    <table>
      <tr><td class="dim">PGREAT</td><td class="num" colspan="3">${tm[4]}</td></tr>
      <tr><td></td><td class="dim num">FAST</td><td class="dim num">SLOW</td><td class="dim num">${t("計")}</td></tr>
      ${rows.map(([n, f, s]) => `<tr><td class="dim" ${n === "POOR" ? `title="${t("空POOR を含む")}"` : ""}>${n}</td>
        <td class="num">${tm[f]}</td><td class="num">${tm[s]}</td><td class="num">${tm[f] + tm[s]}</td></tr>`).join("")}
    </table>
    <h2 style="margin-top:12px">${t("鍵盤ごとの判定")}</h2>
    <div class="table-wrap"><table><thead><tr><th>${t("鍵盤")}</th><th class="num">PG</th><th class="num">GR</th><th class="num">GD</th>
      <th class="num">BD</th><th class="num">PR</th><th class="num" title="${t("空POOR")}">${t("空")}</th><th class="num">${t("レート")}</th><th></th></tr></thead>
      <tbody>${lanes.map(laneRow).join("")}</tbody></table></div>
    ${j.measures?.length ? `<h2 style="margin-top:12px">${t("小節ごとのスコアレート")}</h2><div class="chart-box" id="d-measures"></div>` : ""}</div>`;
}

async function viewMusicDb(view) {
  const [dbs, machines, sets, unassigned] = await Promise.all([api("/api/musicdbs"), api("/api/machines"),
    api("/api/chartsets"), api("/api/musicdata/unassigned")]);
  view.innerHTML = `
    <h1>${t("曲DB")}</h1>
    ${unassigned.length ? `<div class="panel"><h2>${t("取り込み待ちの曲データ")}</h2>
      <p class="dim small">${t("ゲームが読み込んでいるのに、トラッカーにまだ取り込まれていない曲データです。これらのプレイは記録してありますが、取り込むまではどの曲DB にも入りません。ゲームの {0} (omnimix なら data_mods の中の同じ場所) にある同じファイルを取り込むと、プレイはその曲DB に入ります。", mono(esc("data\\info\\<n>\\")))}</p>
      ${unassigned.map(unassignedFile).join("")}</div>` : ""}
    <div class="panel"><h2>${t("music_data.bin / music_omni.bin を取り込む")}</h2>
      <p class="dim small">${t("曲名・レベル・バージョンの表示とカテゴリ分けに使います。取り込み先の曲DB を選んでください。新しい版を元の曲DB に取り込むと、追加・削除された曲が記録されます。新しい曲DB は、altfix 用の曲データかファイル名に「omni」があれば omni になります (あとで変えられます)。")}</p>
      <div class="row">
        <input type="file" id="mdb-file" accept=".bin">
        <select id="mdb-target"><option value="">${t("取り込み先を選んでください")}</option>
          ${dbs.map((d) => `<option value="${d.id}">${t("{0} に追加", esc(d.name))}</option>`).join("")}
          <option value="new">${t("新しい曲DBとして")}</option></select>
        <input id="mdb-name" placeholder="${t("新しい曲DBの名前 (省略可)")}" style="display:none">
        <button class="primary" id="mdb-go">${t("取り込む")}</button>
      </div></div>
    ${dbs.map((d) => `<div class="panel">
      <div class="row"><input value="${esc(d.name)}" data-rename="${d.id}" style="font-weight:700;min-width:240px">
        <select data-kind="${d.id}"><option value="omni">omni</option><option value="vanilla">${t("通常")}</option></select>
        <span class="dim">IIDX${d.game_version}${SEP}${t("{0} 曲", d.songs)}</span>
        <a href="#/songs?category=db:${d.id}&level=">${t("曲一覧")}</a>
        <button class="danger" data-del="${d.id}" style="margin-left:auto">${t("削除")}</button></div>
      <p class="dim small">${d.scans.length ? (({scanned_at, songs, charts, skipped, errors, mods}) =>
        `${t("譜面の解析: {0}", fmtDate(scanned_at))}${SEP}${t("{0} 曲 {1} 譜面", songs, charts)}${mods ? `${SEP}mod: ${esc(mods)}` : ""}${
          skipped ? `${SEP}${t("この曲DBに無い {0} 曲は除外", skipped)}` : ""}${errors ? `${SEP}${t("読めない {0} 曲", errors)}` : ""}`)(d.scans[0])
        : t("譜面の解析: まだ (下の「譜面データからノーツ数を取り込む」)")}</p>
      ${clearButtons(d)}
      <div class="table-wrap" style="margin-top:10px"><table><thead><tr><th>${t("取り込み日時")}</th><th>${t("ファイル")}</th>
        <th class="num">${t("曲数")}</th><th class="num">${pick("追加", "Added")}</th><th class="num">${pick("削除", "Removed")}</th><th></th></tr></thead><tbody>
        ${d.imports.map((i) => `<tr><td>${fmtDate(i.imported_at)}</td><td>${esc(i.filename)}</td>
          <td class="num">${i.song_count}</td><td class="num">+${i.added}</td><td class="num">-${i.removed}</td>
          <td><button class="danger" data-undo="${i.id}" data-db="${d.id}" data-label="${esc(`${i.filename} (${fmtDate(i.imported_at)})`)}">${t("削除")}</button></td></tr>`).join("")}
      </tbody></table></div></div>`).join("")}
    <div class="panel"><h2>${t("tracker_link.dll からの報告")}</h2>
      <p class="dim small">${t("tracker_link.dll (トラッカーに同梱) を入れたゲームは、ログインのたびに、読み込んだ曲データ (ファイル名・サイズ・SHA-256) と 2dxtra の有無をトラッカーに知らせます。その筐体 (PCBID) のプレイは、報告された曲データの曲DB に記録されます (omnimix かどうかも曲DB で決まります)。「未登録」の曲データは、同じファイルを取り込むか、「取り込み待ちの曲データ」で曲DB を選ぶと紐付きます。")}</p>
      ${machines.length ? `<div class="table-wrap"><table><thead><tr><th>${t("最終報告")}</th><th>PCBID</th><th>${t("曲DB")}</th>
        <th>${t("ファイル")}</th><th>2dxtra</th><th>${t("ゲーム")}</th><th>DLL</th><th>${t("接続元")}</th></tr></thead><tbody>
        ${machines.map((m) => `<tr><td>${fmtDate(m.booted_at)}</td><td class="mono">${esc(m.pcbid)}</td>
          <td>${m.musicdb_id == null ? `<span class="dim">${t("未登録")}</span>` : esc(m.musicdb)}</td><td>${esc(m.filename)}</td>
          <td>${m.dxtra == null ? "-" : m.dxtra ? t("あり") : t("なし")}</td>
          <td>${esc(m.game)}</td><td>${esc(m.altfix)}</td><td class="mono">${esc(m.remote)}</td></tr>`).join("")}
      </tbody></table></div>` : `<p class="dim small">${t("まだ報告がありません。")}</p>`}</div>
    <div class="panel"><h2>${t("譜面データからノーツ数を取り込む")}</h2>
      <p class="dim small">${t("スコアレート・DJ LEVEL・DJ POINT の計算に使います (未取り込みでも、完走したプレイの判定数から推定します)。ゲームのフォルダ ({0} と {1} があるフォルダ) を指定すると、全曲の譜面 (.1) を読んでノーツ数を数えます。パスはトラッカーを動かしている PC のものです。「このPCのフォルダを選ぶ」なら、このブラウザの PC にあるフォルダを読みます (トラッカーが LAN の別の PC でも使えます)。ブラウザが「アップロード」の確認を出しますが、送るのは譜面の場所を調べるための目録とノーツ数だけです。", mono("data"), mono("data_mods"))}</p>
      <p class="dim small">${t("omnimix を {0} に入れている (layeredfs) 場合は、ゲームと同じ順番で重ねて読みます (mod はフォルダ名の順に優先、{1} → {2} → フォルダの順)。omnimix を {3} に直接上書きしている場合は、mod のチェックをすべて外してください。取り込んだノーツ数は、選んだ曲DB に収録されている曲だけに紐付きます。", mono("data_mods"), mono("-p0.ifs"), mono(".ifs"), mono("data"))}</p>
      <div class="row"><input id="snd-path" placeholder="${t("例: {0}", "D:\\bemani\\iidx33")}" style="min-width:380px">
        <button id="snd-check">${t("フォルダを確認")}</button>
        <span class="dim small">${t("または")}</span>
        <button id="snd-pick">${t("このPCのフォルダを選ぶ")}</button><input type="file" id="snd-files" webkitdirectory hidden></div>
      <div id="snd-detail" style="margin-top:10px"></div></div>
    <div class="panel"><h2>${t("ノーツ数の JSON を取り込む")}</h2>
      <p class="dim small">${t("iidx-datatools の {0} が出力する JSON ({1}) を選んだ曲DB に取り込みます。譜面データの解析結果がある譜面は上書きしません。", mono("parse_chart_notecounts.py"), mono(`{"${t("曲ID")}": {"SPA": 1234, ...}}`))}</p>
      <div class="row"><input type="file" id="notes-file" accept=".json">
        <select id="notes-db"><option value="">${t("紐付ける曲DB を選んでください")}</option>
          ${dbs.map((d) => `<option value="${d.id}">${esc(d.name)} (IIDX${d.game_version}${SEP}${t("{0} 曲", d.songs)})</option>`).join("")}</select>
        <button id="notes-go">${t("取り込む")}</button></div></div>
    <div class="panel"><h2>${t("2dxtra の譜面セットを取り込む")}</h2>
      <p class="dim small">${t("2dxtra が作る譜面 (Kiraku・Kichiku・All-Scratch) の一覧とノーツ数を、2dxtra.dll と同じフォルダの {0} から読みます。パスはトラッカーを動かしている PC のものです。「このPCのファイルを選ぶ」なら、このブラウザの PC にあるファイルから譜面の一覧 (数 MB) だけを読んで送ります (譜面そのものは送りません)。どちらもゲームを終えてから取り込んでください。これらの譜面のプレイは tracker_link.dll がカードアウト時にまとめて送り、通常の譜面とは別に集計します (上の「譜面セット」で切り替えて見られます)。", mono("2dxtra.sqlite"))}</p>
      <div class="row"><input id="dx-path" placeholder="${t("例: {0}", "D:\\bemani\\iidx33\\2dxtra.sqlite")}" style="min-width:380px">
        <button id="dx-go">${t("取り込む")}</button>
        <span class="dim small">${t("または")}</span>
        <button id="dx-pick">${t("このPCのファイルを選ぶ")}</button><input type="file" id="dx-file" accept=".sqlite" hidden></div>
      <p class="dim small" id="dx-progress"></p>
      <div class="small">${sets.length ? sets.map((s) => `<div class="row" style="margin-top:6px">${t("{0}: {1} 譜面・{2} プレイ", esc(s.name), s.charts, s.plays)}${
        s.charts ? ` <button class="danger" data-delset="${esc(s.name)}">${t("削除")}</button>` : ""}</div>`).join("")
        : `<p class="dim">${t("まだありません")}</p>`}</div></div>`;

  const sndPath = $("#snd-path");
  try { sndPath.value = localStorage.getItem("sound.path") || ""; } catch {}
  // the mods to lay over data and the database to fill, then run: scan(mods, db, progress)
  const soundDetail = (root, mods, scan) => {
      $("#snd-detail").innerHTML = `
        <p class="small">${t("ゲームのフォルダ: {0}", mono(esc(root)))}</p>
        ${mods.length ? `<p class="small">${t("重ねる mod (data_mods、上ほど優先):")}</p>
          ${mods.map((m) => `<label class="small" style="display:block"><input type="checkbox" class="snd-mod" value="${esc(m)}" checked> ${esc(m)}</label>`).join("")}`
          : `<p class="dim small">${t("data_mods に sound を含む mod はありません (data だけを読みます)。")}</p>`}
        <div class="row" style="margin-top:8px">
          <select id="snd-db"><option value="">${t("紐付ける曲DB を選んでください")}</option>
            ${dbs.map((d) => `<option value="${d.id}">${esc(d.name)} (IIDX${d.game_version}${SEP}${t("{0} 曲", d.songs)})</option>`).join("")}</select>
          <button class="primary" id="snd-go">${t("解析して取り込む")}</button></div>
        <div id="snd-result"></div>`;
      $("#snd-go").onclick = async () => {
        const db = $("#snd-db").value;
        if (!db) return toast(t("紐付ける曲DB を選んでください"));
        const mods = [...view.querySelectorAll(".snd-mod:checked")].map((c) => c.value);
        $("#snd-go").disabled = true;
        const progress = (msg) => ($("#snd-result").innerHTML = `<p class="dim small">${esc(msg)}</p>`);
        progress(t("解析中…"));
        try {
          const s = await scan(mods, +db, progress);
          $("#snd-result").innerHTML = `<p class="small">${t("{0} 曲 {1} 譜面を取り込みました ({2} 秒)", s.songs, s.charts, s.seconds.toFixed(1))}${
            s.skipped ? `${SEP}${t("選んだ曲DB に無い {0} 曲は除外", s.skipped)}` : ""}</p>
            ${s.errors ? `<details class="small"><summary>${t("読めなかった {0} 曲", s.errors)}</summary>
              <table>${s.error_list.map((e) => `<tr><td>${e.music_id}</td><td class="mono">${esc(e.source)}</td><td>${esc(pick(e.error, e.error_en))}</td></tr>`).join("")}</table></details>` : ""}`;
          toast(t("{0} 曲のノーツ数を取り込みました", s.songs), true);
        } catch (e) {
          $("#snd-result").innerHTML = "";
          toast(e.message);
        } finally {
          $("#snd-go").disabled = false;
        }
      };
  };
  $("#snd-check").onclick = async () => {
    try {
      const r = await api(`/api/sound/inspect?${new URLSearchParams({path: sndPath.value})}`);
      try { localStorage.setItem("sound.path", sndPath.value); } catch {}
      soundDetail(r.root, r.mods, (mods, db) => post("/api/sound/scan", {path: sndPath.value, mods, musicdb_id: db}));
    } catch (e) { toast(e.message); }
  };
  $("#snd-pick").onclick = () => $("#snd-files").click();
  $("#snd-files").onchange = (e) => {
    const game = gameFolder(e.target.files);
    if (!game.files.size) return toast(t("選んだフォルダに data の sound フォルダがありません"));
    soundDetail(t("{0} (このPC)", game.name), game.mods, (mods, db, progress) => browserScan(game, mods, db, progress));
  };

  $("#mdb-target").addEventListener("change", (e) => ($("#mdb-name").style.display = e.target.value === "new" ? "" : "none"));
  $("#mdb-go").onclick = async () => {
    const f = $("#mdb-file").files[0];
    if (!f) return toast(t("ファイルを選択してください"));
    if (!$("#mdb-target").value) return toast(t("取り込み先の曲DB を選んでください"));
    const p = new URLSearchParams({filename: f.name, target: $("#mdb-target").value, name: $("#mdb-name").value});
    try {
      const r = await api(`/api/musicdb/upload?${p}`, {method: "POST", body: await f.arrayBuffer()});
      toast(r.duplicate ? t("同じファイルは取り込み済みです") :
        t("{0} 曲を取り込みました (追加 {1} ／ 削除 {2})", r.songs, r.added, r.removed), true);
      await loadDbs();
      render();
    } catch (e) { toast(e.message); }
  };
  view.querySelectorAll("[data-kind]").forEach((s) => {
    s.value = dbs.find((d) => d.id === +s.dataset.kind).kind || "vanilla";
    s.addEventListener("change", () => post("/api/musicdb/update", {id: +s.dataset.kind, kind: s.value})
      .then(() => toast(t("変更しました"), true)).catch((e) => toast(e.message)));
  });
  view.querySelectorAll("[data-rename]").forEach((i) => i.addEventListener("change", () =>
    post("/api/musicdb/update", {id: +i.dataset.rename, name: i.value})
      .then(() => { toast(t("名前を変更しました"), true); return loadDbs(); }).catch((e) => toast(e.message))));
  view.querySelectorAll("[data-del]").forEach((b) => b.addEventListener("click", async () => {
    if (!confirm(t("この曲DBを削除しますか？ (プレイ記録は消えません)"))) return;
    await post("/api/musicdb/update", {id: +b.dataset.del, delete: true}).catch((e) => toast(e.message));
    await loadDbs().catch((e) => toast(e.message));
    render();
  }));
  $("#notes-go").onclick = async () => {
    const f = $("#notes-file").files[0];
    if (!f) return toast(t("ファイルを選択してください"));
    try {
      const db = $("#notes-db").value;
      if (!db) return toast(t("紐付ける曲DB を選んでください"));
      const r = await api(`/api/notes/upload?db=${db}`, {method: "POST", body: await f.arrayBuffer()});
      toast(t("{0} 譜面のノーツ数を取り込みました", r.imported), true);
    } catch (e) { toast(e.message); }
  };
  view.querySelectorAll("[data-undo]").forEach((b) => b.addEventListener("click", async () => {
    if (!confirm(t("{0} の取り込みを削除しますか？ 曲名などは残りの取り込みから作り直します。プレイ記録は消えません。", b.dataset.label))) return;
    try {
      await post("/api/musicdb/update", {id: +b.dataset.db, delete_import: +b.dataset.undo});
      toast(t("削除しました"), true);
      await loadDbs();
      render();
    } catch (e) { toast(e.message); }
  }));
  view.querySelectorAll("[data-clear]").forEach((b) => b.addEventListener("click", async () => {
    if (!confirm(t("「{0}」を削除しますか？ (プレイ記録は消えません)", b.dataset.label))) return;
    try {
      await post("/api/musicdb/update", {id: +b.dataset.db, clear: b.dataset.clear});
      toast(t("削除しました"), true);
      await loadDbs();
      render();
    } catch (e) { toast(e.message); }
  }));
  view.querySelectorAll("[data-delset]").forEach((b) => b.addEventListener("click", async () => {
    if (!confirm(t("譜面セット「{0}」の譜面一覧を削除しますか？ (プレイ記録は消えません)", b.dataset.delset))) return;
    try {
      await post("/api/chartsets/delete", {name: b.dataset.delset});
      toast(t("削除しました"), true);
      await loadSets();
      render();
    } catch (e) { toast(e.message); }
  }));
  view.querySelectorAll("[data-pending]").forEach((box) => {
    const sel = box.querySelector("select"), name = box.querySelector("input[type=text]");
    sel.addEventListener("change", () => (name.style.display = sel.value === "new" ? "" : "none"));
    box.querySelector("button").addEventListener("click", async () => {
      const f = box.querySelector("input[type=file]").files[0];
      if (!f) return toast(t("ファイルを選択してください"));
      if (!sel.value) return toast(t("取り込み先の曲DB を選んでください"));
      // the tracker refuses a file other than the one the game reported
      const p = new URLSearchParams({filename: f.name, target: sel.value, name: name.value, expect: box.dataset.pending});
      try {
        const r = await api(`/api/musicdb/upload?${p}`, {method: "POST", body: await f.arrayBuffer()});
        toast(t("{0} 曲を取り込みました (追加 {1} ／ 削除 {2})", r.songs, r.added, r.removed), true);
        await loadDbs();
        render();
      } catch (e) { toast(e.message); }
    });
  });
  view.querySelectorAll("[data-assign]").forEach((b) => b.addEventListener("click", async () => {
    if (!confirm(t("この曲データのプレイを「{0}」に入れますか？", b.dataset.name))) return;
    try {
      await post("/api/musicdata/assign", {sha256: b.dataset.assign, musicdb_id: +b.dataset.db});
      toast(t("振り分けました"), true);
      render();
    } catch (e) { toast(e.message); }
  }));
  $("#dx-pick").onclick = () => $("#dx-file").click();
  $("#dx-file").onchange = async (e) => {
    const f = e.target.files[0];
    if (!f) return;
    const progress = (msg) => ($("#dx-progress").textContent = msg);
    try {
      progress(t("譜面の一覧を読んでいます… {0}", 0));
      const list = await read2dxtra(f, progress);
      progress(t("送信しています…"));
      const r = await post("/api/chartsets/upload", list);
      progress("");
      toast(t("{0} 譜面を取り込みました ({1} プレイをセットに振り分け)", r.charts, r.plays), true);
      await loadSets();
      render();
    } catch (err) {
      progress("");
      toast(err.message);
    }
  };
  const dxPath = $("#dx-path");
  try { dxPath.value = localStorage.getItem("dxtra.path") || ""; } catch {}
  $("#dx-go").onclick = async () => {
    try {
      const r = await post("/api/chartsets/import", {path: dxPath.value});
      try { localStorage.setItem("dxtra.path", dxPath.value); } catch {}
      toast(t("{0} 譜面を取り込みました ({1} プレイをセットに振り分け)", r.charts, r.plays), true);
      await loadSets();
      render();
    } catch (e) { toast(e.message); }
  };
}

// ---- counting the charts of a game folder on the browser's PC (the tracker may be on another) ----

// The sound folders of a game folder picked on this PC (the game folder, data or sound): the files a
// scan looks at, by path in the game folder ("data/sound/01000.ifs"), and the mods with a sound folder.
function gameFolder(list) {
  const files = new Map(), mods = new Set();
  let name = "";
  for (const f of list) {
    const rel = f.webkitRelativePath;
    name ||= rel.split("/")[0];
    let m = rel.match(/(?:^|\/)(data\/sound|data_mods\/([^/]+)\/sound)\/(.+)$/);
    if (!m && (m = rel.match(/^sound\/(.+)$/))) m = [, "data/sound", undefined, m[1]]; // the sound folder itself
    if (!m || !/^\d{5}(-p0)?\.ifs$|^\d{5}\/\d{5}\.1$|^\d{5}(-p0)?_ifs\//.test(m[3])) continue;
    files.set(`${m[1]}/${m[3]}`, f);
    if (m[2]) mods.add(m[2]);
  }
  return {name, files, mods: [...mods].sort()};
}

// Runs fn over items, n at a time.
async function pool(items, n, fn) {
  let next = 0;
  await Promise.all(Array.from({length: Math.min(n, items.length)}, async () => {
    while (next < items.length) await fn(items[next++]);
  }));
}

// The start of an IFS up to the end of its manifest: what the tracker needs to find a file in it.
async function ifsHead(file) {
  const head = await file.slice(0, 36).arrayBuffer();
  const v = new DataView(head);
  if (head.byteLength < 20 || v.getUint32(0) !== 0x6CAD8F89 || v.getUint32(16) > 64 << 20) return head; // the tracker says what is wrong
  return file.slice(0, v.getUint32(16)).arrayBuffer();
}

function base64(buf) {
  const b = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000));
  return btoa(s);
}

// The notes of every chart of a .1, counted like NoteCounts in internal/sound: chart index -> notes.
const CHART_SLOT = [3, 1, 0, 2, 4, 9, 7, 6, 8, 10];
function noteCounts(buf) {
  const v = new DataView(buf), len = buf.byteLength;
  if (len >= 4 && v.getUint32(0) === 0x6CAD8F89) {
    return {error: "譜面の代わりにダミーが置かれています (未収録曲)", error_en: "a dummy stands in for the chart (song not included)"};
  }
  if (len < 96) return {error: ".1 が短すぎます", error_en: "the .1 is too short"};
  const counts = {};
  for (let chart = 0; chart < CHART_SLOT.length; chart++) {
    const off = v.getUint32(CHART_SLOT[chart] * 8, true), size = v.getUint32(CHART_SLOT[chart] * 8 + 4, true);
    if (!off || !size) continue;
    if (off < 96 || off > len) {
      return {error: `譜面 ${chart} の位置 0x${off.toString(16)} がファイルの外です`, error_en: `chart ${chart} at 0x${off.toString(16)} lies outside the file`};
    }
    let n = 0;
    for (let p = off, end = Math.min(len, off + size); p + 8 <= end; p += 8) {
      if (v.getInt32(p, true) === 0x7FFFFFFF) break;
      const cmd = v.getUint8(p + 4);
      if (cmd === 0 || cmd === 1) n += v.getUint16(p + 6, true) ? 2 : 1; // a charge note: start and end are judged
    }
    counts[chart] = n;
  }
  return {counts};
}

// Counts the charts of a picked folder: the tracker finds each song's chart from the file list and
// the IFS manifests (a few hundred bytes each), the charts are read and counted here, and only the
// counts are sent.
async function browserScan(game, mods, db, progress) {
  const start = performance.now();
  const layers = ["data/sound/", ...mods.map((m) => `data_mods/${m}/sound/`)];
  const paths = [...game.files.keys()].filter((p) => layers.some((l) => p.startsWith(l)));
  const heads = {};
  let done = 0;
  await pool(paths, 16, async (p) => {
    heads[p] = p.endsWith(".ifs") ? base64(await ifsHead(game.files.get(p))) : "";
    if (++done % 200 === 0) progress(t("フォルダを読んでいます… {0}/{1}", done, paths.length));
  });
  progress(t("譜面の場所を調べています…"));
  const found = await post("/api/sound/locate", {files: heads, mods});
  const songs = [];
  done = 0;
  await pool(found.songs, 8, async (s) => {
    if (s.need) {
      const f = game.files.get(s.need.path);
      const part = s.need.size < 0 ? f : f.slice(s.need.offset, s.need.offset + s.need.size);
      s = {id: s.id, source: s.source, ...noteCounts(await part.arrayBuffer())};
    }
    songs.push(s);
    if (++done % 200 === 0) progress(t("譜面を数えています… {0}/{1}", done, found.songs.length));
  });
  const out = await post("/api/sound/import", {musicdb_id: db, root: game.name, mods: found.mods, songs});
  return {...out, seconds: (performance.now() - start) / 1000};
}

// ---- 2dxtra.sqlite on the browser's PC (the tracker may be on another) ----

// The chart list of a 2dxtra.sqlite, read here without sending the file - it is mostly the charts
// themselves, hundreds of MB. A minimal reader of the SQLite file format walks the tables chart_set and
// charts and keeps the columns the tracker needs, which sit at the start of each row. It does not read
// 2dxtra.sqlite-wal: what the game wrote while it runs may be missing until it ends.
// Returns {sets: {id: name}, charts: [[set id, music ID, chart, id, notes, radar x 6]]}.
async function read2dxtra(file, progress) {
  const head = new DataView(await file.slice(0, 100).arrayBuffer());
  if (head.byteLength < 100 || new TextDecoder().decode(new Uint8Array(head.buffer, 0, 15)) !== "SQLite format 3") {
    throw new Error(t("SQLite のファイルではありません"));
  }
  const pageSize = head.getUint16(16) === 1 ? 65536 : head.getUint16(16);
  const usable = pageSize - head.getUint8(20);
  const text = new TextDecoder();
  const varint = (v, p) => { // [value, the offset after it]
    let x = 0;
    for (let i = 0; i < 8; i++) {
      const b = v.getUint8(p + i);
      x = x * 128 + (b & 0x7f);
      if (!(b & 0x80)) return [x, p + i + 1];
    }
    return [x * 256 + v.getUint8(p + 8), p + 9];
  };
  // the columns of a record, as far as the cell's own page holds them (the big ones come last)
  const record = (v, p, end) => {
    let [size, q] = varint(v, p);
    const types = [];
    while (q < p + size) {
      let type;
      [type, q] = varint(v, q);
      types.push(type);
    }
    const cols = [];
    let at = p + size;
    for (const type of types) {
      const len = type >= 12 ? (type - 12) >> 1 : [0, 1, 2, 3, 4, 6, 8, 8, 0, 0][type];
      if (at + len > end) break; // continues on an overflow page: not needed
      if (type === 0 || type === 10 || type === 11) cols.push(null);
      else if (type === 8 || type === 9) cols.push(type - 8);
      else if (type <= 6) {
        let x = v.getInt8(at);
        for (let i = 1; i < len; i++) x = x * 256 + v.getUint8(at + i);
        cols.push(x);
      } else if (type === 7) cols.push(v.getFloat64(at));
      else if (type & 1) cols.push(text.decode(new Uint8Array(v.buffer, v.byteOffset + at, len)));
      else cols.push(null); // a blob
      at += len;
    }
    return cols;
  };
  // every row of the table whose b-tree starts at page root
  const rows = async (root, visit) => {
    const pending = [root];
    while (pending.length) {
      const batch = pending.splice(0, 64);
      const pages = await Promise.all(batch.map(async (n) =>
        new DataView(await file.slice((n - 1) * pageSize, n * pageSize).arrayBuffer())));
      pages.forEach((v, i) => {
        const base = batch[i] === 1 ? 100 : 0; // page 1 starts with the file header
        const type = v.getUint8(base), cells = v.getUint16(base + 3), ptrs = base + (type === 5 ? 12 : 8);
        for (let c = 0; c < cells; c++) {
          let p = v.getUint16(ptrs + c * 2);
          if (type === 5) { // interior: the child page
            pending.push(v.getUint32(p));
            continue;
          }
          if (type !== 13) throw new Error(t("2dxtra のデータベースとして読めません"));
          let size, rowid;
          [size, p] = varint(v, p);
          [rowid, p] = varint(v, p);
          const max = usable - 35, min = Math.floor((usable - 12) * 32 / 255) - 23, k = min + (size - min) % (usable - 4);
          visit(rowid, record(v, p, p + (size <= max ? size : k <= max ? k : min)));
        }
        if (type === 5) pending.push(v.getUint32(base + 8)); // the right-most child
      });
    }
  };
  const tables = {};
  await rows(1, (_, [type, name, , root, sql]) => { if (type === "table") tables[name] = {root, sql}; });
  if (!tables.chart_set || !tables.charts) throw new Error(t("2dxtra のデータベースとして読めません"));
  const sets = {};
  await rows(tables.chart_set.root, (id, [, name]) => { sets[id] = name; }); // id is the rowid
  const col = Object.fromEntries([...tables.charts.sql.matchAll(/(\w+)\s+(?:INTEGER|TEXT|BLOB)/g)].map((m, i) => [m[1], i]));
  const want = ["chart_set", "music_id", "difficulty", "hash", "notes",
    "radar_notes", "radar_peak", "radar_scratch", "radar_soflan", "radar_charge", "radar_chord"];
  if (want.some((w) => col[w] == null)) throw new Error(t("2dxtra のデータベースとして読めません"));
  const charts = [];
  await rows(tables.charts.root, (_, c) => {
    charts.push(want.map((w) => c[col[w]] ?? null));
    if (charts.length % 5000 === 0) progress?.(t("譜面の一覧を読んでいます… {0}", charts.length));
  });
  return {sets, charts};
}

// Buttons that delete one kind of note counts of a music database (the database and plays stay; the
// music data is taken back per import, in the table).
function clearButtons(d) {
  const items = [
    ["analysis", d.notes.analysis && t("譜面解析のノーツ数 ({0} 譜面)", d.notes.analysis)],
    ["import", d.notes.import && t("JSON のノーツ数 ({0} 譜面)", d.notes.import)],
    ["observed", d.notes.observed && t("プレイから推定したノーツ数 ({0} 譜面)", d.notes.observed)],
  ].filter(([, label]) => label);
  if (!items.length) return "";
  return `<div class="row small" style="margin-top:8px"><span class="dim">${t("取り込んだ情報を削除:")}</span>
    ${items.map(([what, label]) => `<button class="danger" data-clear="${what}" data-db="${d.id}" data-label="${esc(label)}">${esc(label)}</button>`).join("")}</div>`;
}

// One music data file the game loaded that was never imported: import it here (only that very file is
// taken), or put its plays in a database as is, comparing what each database calls their songs.
function unassignedFile(f) {
  return `<div style="margin-top:12px">
    <p><b>${esc(f.filename ?? "?")}</b> <span class="dim small">${f.size != null ? t("{0} バイト", f.size.toLocaleString()) + SEP : ""}${
      mono(esc(f.sha256.slice(0, 16)))}…${SEP}${t("{0} プレイ・{1} 曲", f.plays, f.songs)}${SEP}${fmtDate(f.first_played, false)} – ${fmtDate(f.last_played, false)}</span></p>
    <div class="row" data-pending="${f.sha256}">
      <input type="file" accept=".bin">
      <select><option value="">${t("取り込み先を選んでください")}</option>
        ${f.dbs.map((d) => `<option value="${d.id}">${t("{0} に追加", esc(d.name))}</option>`).join("")}
        <option value="new">${t("新しい曲DBとして")}</option></select>
      <input type="text" placeholder="${t("新しい曲DBの名前 (省略可)")}" style="display:none">
      <button class="primary">${t("取り込む")}</button></div>
    ${f.dbs.length ? `<details class="small" style="margin-top:8px"><summary>${t("取り込まずに、今ある曲DB に入れる (プレイの曲名で見比べる)")}</summary>
    <div class="table-wrap"><table><thead><tr><th>${t("日時")}</th><th>${t("譜面")}</th><th class="num">EX</th>
      ${f.dbs.map((d) => `<th>${esc(d.name)} <span class="dim small">${t("{0}/{1} 曲あり", d.has, f.songs)}</span><br>
        <button data-assign="${f.sha256}" data-db="${d.id}" data-name="${esc(d.name)}">${t("この曲DB にする")}</button></th>`).join("")}</tr></thead><tbody>
    ${f.samples.map((p) => `<tr><td>${fmtDate(p.played_at)}</td>
      <td>${chartTag(p.chart)}${setPill(p.chart_set)} <span class="dim">${p.level ?? ""}</span></td><td class="num">${p.ex_score ?? "-"}</td>
      ${f.dbs.map((d) => `<td class="title">${p.titles[d.id] != null ? esc(p.titles[d.id]) : `<span class="dim">${t("#{0} (無い曲)", p.music_id)}</span>`}</td>`).join("")}</tr>`).join("")}
    </tbody></table></div></details>` : ""}</div>`;
}

async function viewCategories(view) {
  const cats = await api(`/api/categories?${ctx()}`);
  view.innerHTML = `
    <h1>${t("カテゴリ")}</h1>
    <div class="panel"><h2>${t("手動カテゴリ")}</h2>
      <p class="dim small">${t("曲一覧でチェックした曲をまとめて追加できます。")}</p>
      <div class="row"><input id="c-name" placeholder="${t("新しいカテゴリ名")}"><input type="color" id="c-color" value="#6aa9ff">
        <button class="primary" id="c-add">${t("作成")}</button></div>
      <div class="table-wrap" style="margin-top:12px"><table><tbody>
        ${cats.manual.map((c) => `<tr>
          <td><input type="color" value="${esc(c.color || "#6aa9ff")}" data-color="${c.id}"></td>
          <td><input value="${esc(c.name)}" data-name="${c.id}"></td>
          <td class="num">${t("{0} 曲", c.songs)}</td>
          <td><a href="#/songs?category=m:${c.id}&level=">${t("曲一覧")}</a></td>
          <td><button class="danger" data-del="${c.id}">${t("削除")}</button></td></tr>`).join("") ||
          `<tr><td class="dim">${t("まだありません")}</td></tr>`}
      </tbody></table></div></div>
    <div class="grid">
      <div class="panel"><h2>${t("自動カテゴリ (曲DB)")}</h2>${cats.auto.map((c) =>
        `<div><a href="#/songs?category=${encodeURIComponent(c.key)}&level=">${esc(catName(c))}</a></div>`).join("") ||
        `<p class="dim">${t("曲DBを取り込むと表示されます")}</p>`}</div>
      <div class="panel"><h2>${t("バージョン")}</h2>${cats.versions.map((c) =>
        `<a class="pill" style="margin:2px" href="#/songs?category=${c.key}&level=">${esc(c.name)}</a>`).join("") ||
        `<p class="dim">${t("曲DBを取り込むと表示されます")}</p>`}</div>
    </div>`;
  $("#c-add").onclick = async () => {
    try {
      await post("/api/categories/create", {name: $("#c-name").value, color: $("#c-color").value});
      render();
    } catch (e) { toast(e.message); }
  };
  view.querySelectorAll("[data-name]").forEach((i) => i.addEventListener("change", () =>
    post("/api/categories/update", {id: +i.dataset.name, name: i.value}).then(() => toast(t("変更しました"), true))));
  view.querySelectorAll("[data-color]").forEach((i) => i.addEventListener("change", () =>
    post("/api/categories/update", {id: +i.dataset.color, color: i.value})));
  view.querySelectorAll("[data-del]").forEach((b) => b.addEventListener("click", async () => {
    if (!confirm(t("このカテゴリを削除しますか？"))) return;
    await post("/api/categories/update", {id: +b.dataset.del, delete: true});
    render();
  }));
}

// Difficulty tables: a table's charts in the music DB with the built-in snapshot's rank and the one
// in use; picking a rank changes it at once (kept over the snapshot until set back).
async function viewTiers(view) {
  const q = hashParams();
  const kind = ["normal", "hard", "dp"].includes(q.kind) ? q.kind : localStorage.getItem("tiers.kind") || "normal";
  const level = q.level || localStorage.getItem("tiers.level") || "12";
  localStorage.setItem("tiers.kind", kind);
  localStorage.setItem("tiers.level", level);
  const d = await api(`/api/tiers?${ctx({kind, level})}`);
  const RANKS = ["F", "E", "D", "C", "B", "B+", "A", "A+", "S", "S+"];
  const rankSelect = (cur) => `<select class="tier-edit">${[`<option value="">${t("なし")}</option>`,
    ...["地力", "個人差"].flatMap((k) => RANKS.map((r) => `<option${k + r === cur ? " selected" : ""}>${k + r}</option>`))].join("")}</select>`;
  const editor = (r) => (kind === "dp"
    ? `<input class="tier-edit" type="number" step="0.1" min="1" max="13" style="width:6em" value="${esc(r.current ?? "")}" placeholder="${t("なし")}">`
    : rankSelect(r.current));
  const src = d.source || {};
  view.innerHTML = `
    <h1>${t("難易度表")}</h1>
    <div class="panel">
      <p class="dim small">${t("曲一覧・譜面の画面に出すランク。組み込みの写し ({0}) をここで変えられる。変えたものは「写しに戻す」まで残り、写しに無い譜面 (新曲など) にも付けられる。",
        src.source ? `<a href="${esc(src.source)}" target="_blank" rel="noopener">${esc(src.name)}</a>, ${esc(src.fetched)}` : "-")}</p>
      <div class="row">
        <select id="tr-kind">
          <option value="normal"${kind === "normal" ? " selected" : ""}>${t("SP☆12 ノマゲ")}</option>
          <option value="hard"${kind === "hard" ? " selected" : ""}>${t("SP☆12 ハード")}</option>
          <option value="dp"${kind === "dp" ? " selected" : ""}>${t("DP 非公式難易度")}</option>
        </select>
        ${kind === "dp" ? `<select id="tr-level">${Array.from({length: 12}, (_, i) => 12 - i).map((l) => `<option value="${l}"${String(l) === level ? " selected" : ""}>☆${l}</option>`).join("")}</select>` : ""}
        <input id="tr-q" placeholder="${t("曲名で絞り込み")}">
        <label><input type="checkbox" id="tr-changed"> ${t("変えたものだけ")}</label>
        <label><input type="checkbox" id="tr-none"> ${t("ランクの無いものだけ")}</label>
        <span class="dim small" id="tr-count"></span>
      </div>
    </div>
    <div class="panel"><div class="table-wrap"><table><thead><tr>
      <th>${t("タイトル")}</th><th>${t("譜面")}</th><th>${t("写し")}</th><th>${t("ランク")}</th><th></th></tr></thead>
      <tbody id="tr-rows"></tbody></table></div></div>`;
  const collator = new Intl.Collator("ja");
  const rows = d.rows.sort((a, b) => collator.compare(a.title, b.title) || a.chart - b.chart);
  const draw = () => {
    const f = $("#tr-q").value.trim().toLowerCase();
    const shown = rows.filter((r) => (!f || r.title.toLowerCase().includes(f)) &&
      (!$("#tr-changed").checked || r.current !== r.snapshot) && (!$("#tr-none").checked || r.current == null));
    $("#tr-rows").innerHTML = shown.map((r) => `
      <tr data-mid="${r.music_id}" data-chart="${r.chart}">
        <td><a href="${chartHref(r.music_id, r.chart)}">${esc(r.title)}</a></td><td>${chartTag(r.chart)}</td>
        <td class="small">${esc(r.snapshot ?? "-")}</td><td>${editor(r)}</td>
        <td>${r.current !== r.snapshot ? `<button class="tier-reset">${t("写しに戻す")}</button>` : ""}</td></tr>`).join("");
    const changed = rows.filter((r) => r.current !== r.snapshot).length;
    $("#tr-count").textContent = t("{0} / {1} 譜面", shown.length, rows.length) + (changed ? `${SEP}${t("変えたもの {0}", changed)}` : "");
  };
  const save = async (tr, body) => {
    const r = rows.find((x) => x.music_id === +tr.dataset.mid && x.chart === +tr.dataset.chart);
    try {
      r.current = (await post("/api/tiers/set", {kind, music_id: r.music_id, chart: r.chart, ...body})).current;
      toast(t("変更しました"), true);
    } catch (e) { toast(e.message); }
    draw();
  };
  $("#tr-rows").addEventListener("change", (e) => {
    if (e.target.classList.contains("tier-edit")) save(e.target.closest("tr"), {label: e.target.value || null});
  });
  $("#tr-rows").addEventListener("click", (e) => {
    if (e.target.classList.contains("tier-reset")) save(e.target.closest("tr"), {reset: true});
  });
  const go = () => {
    location.hash = `#/tiers?${new URLSearchParams({kind: $("#tr-kind").value, level: $("#tr-level")?.value ?? level})}`;
  };
  $("#tr-kind").addEventListener("change", go);
  $("#tr-level")?.addEventListener("change", go);
  for (const id of ["#tr-q", "#tr-changed", "#tr-none"]) $(id).addEventListener("input", draw);
  draw();
}

// Where the game should connect: this PC, plus every LAN address when listening on all interfaces.
function gameUrls(s) {
  const listen = s.config.listen;
  const cut = listen.lastIndexOf(":");
  const host = listen.slice(0, cut), port = listen.slice(cut + 1);
  if (host === "" || host === "0.0.0.0") {
    return [[t("この PC のゲーム"), `http://127.0.0.1:${port}`],
            ...(s.addresses || []).map((ip) => [t("別の PC のゲーム"), `http://${ip}:${port}`])];
  }
  return [[t("ゲーム"), `http://${host}:${port}`]];
}

async function viewSettings(view) {
  const s = await api("/api/status");
  const listen = s.config.listen;
  const urls = gameUrls(s).map(([who, url]) =>
    `<tr><td class="dim">${esc(who)}</td><td class="mono" style="font-size:16px">${esc(url)}</td></tr>`).join("");
  view.innerHTML = `
    <h1>${t("設定")}</h1>
    <div class="panel"><h2>${t("中継先サーバー")}</h2>
      <p class="dim small">${t("本来ゲームが接続していたサーバー (asphyxia など) の URL。変更はすぐ反映されます。")}</p>
      <div class="row"><input id="s-up" value="${esc(s.config.upstream)}" placeholder="http://10.0.0.5:8083" style="min-width:320px">
        <button class="primary" id="s-up-save">${t("保存")}</button></div>
      <p class="dim small">${t("omnimix (リビジョン S) の通信は、私設アドレス (localhost / 10.x / 172.16-31.x / 192.168.x など) 以外へは中継しません。")}</p></div>
    <div class="panel"><h2>${t("ゲーム側の設定")}</h2>
      <p>${t("ea3-config.xml の {0} (または spice2x の {1}) をトラッカーに向けます:", mono("&lt;services&gt;"), mono("-url"))}</p>
      <table>${urls}</table>
      <p class="dim small">${t("トラッカーは受信した通信を中継先へ転送し、応答をそのまま返します。書き換えるのは services.get の接続先一覧 (以降の通信もトラッカー経由にするため) と、tracker_link.dll が付け足した記録用の要素 (中継先には送らない) だけです。")}</p></div>
    <div class="panel"><h2>${t("保存場所・待ち受け (再起動後に反映)")}</h2>
      <table>
        <tr><td class="dim">${t("データベース")}</td><td><input id="s-db" value="${esc(s.config.db_path)}" style="min-width:360px">
          <div class="dim small">${t("現在: {0} (相対パスは iidx-tracker.exe の場所から)", mono(esc(s.db_path)))}</div></td></tr>
        <tr><td class="dim">${t("待ち受け")}</td><td><input id="s-listen" value="${esc(listen)}">
          <div class="dim small">${t("0.0.0.0:8084 (既定) ならこの PC からも LAN の別 PC からも接続できます。この PC だけにするなら 127.0.0.1:8084")}</div></td></tr>
        <tr><td class="dim">${t("設定ファイル")}</td><td class="mono small">${esc(s.config_path)}</td></tr>
      </table>
      <button id="s-save" style="margin-top:10px">${t("保存")}</button></div>
    <div class="panel"><h2>${t("状態")}</h2><table>
      <tr><td class="dim">${t("起動")}</td><td>${fmtDate(s.stats.started)}</td></tr>
      <tr><td class="dim">${t("中継リクエスト")}</td><td>${s.stats.requests}</td></tr>
      <tr><td class="dim">${t("記録")}</td><td>${s.stats.recorded}</td></tr>
      <tr><td class="dim">${t("omni 転送拒否")}</td><td>${s.stats.refused}</td></tr>
      <tr><td class="dim">${t("エラー")}</td><td>${s.stats.errors} ${esc(s.stats.last_error || "")}</td></tr>
    </table></div>`;
  $("#s-up-save").onclick = () => post("/api/settings", {upstream: $("#s-up").value})
    .then(() => toast(t("保存しました"), true)).catch((e) => toast(e.message));
  $("#s-save").onclick = () => post("/api/settings", {db_path: $("#s-db").value, listen: $("#s-listen").value})
    .then((r) => toast(r.restart ? t("保存しました。トラッカーを再起動すると反映されます") : t("変更はありません"), true))
    .catch((e) => toast(e.message));
}

// ---------------------------------------------------------------- boot

$("#player").addEventListener("change", async (e) => { setPlayer(e.target.value); await loadDbs(true).catch((err) => toast(err.message)); render(); });
$("#db").addEventListener("change", (e) => { state.db = e.target.value; localStorage.setItem("db", state.db); render(); });
$("#set").addEventListener("change", (e) => {
  setSet(e.target.value);
  const [path] = location.hash.split("?");
  if (path.startsWith("#/chart/")) location.hash = chartHref(...path.slice(8).split("/"), state.set); // the same chart in that set
  else render();
});
$("#menu-btn").addEventListener("click", () => {
  const open = $("#top").classList.toggle("open");
  $("#menu-btn").setAttribute("aria-expanded", open);
});
// fixed text of index.html, and the language switch (the page reloads in the other language)
for (const el of document.querySelectorAll("[data-i18n]")) el.textContent = t(el.textContent.trim());
for (const el of document.querySelectorAll("[data-i18n-title]")) el.title = t(el.title);
$("#lang").value = LANG;
$("#lang").addEventListener("change", (e) => {
  try { localStorage.setItem("lang", e.target.value); } catch {}
  location.reload();
});
window.addEventListener("hashchange", render);
loadPlayers().catch((e) => toast(e.message)).finally(render);
