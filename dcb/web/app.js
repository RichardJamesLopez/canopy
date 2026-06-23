"use strict";

// DCB web front-end — "Phosphor Terminal". All game logic lives in the Go/WASM
// engine; this file renders the JSON view-model and forwards control actions.
// 1 block = 1 MONTH (the on-chain tx cadence). Real-time: 1 week = 5s, so a
// month = 4 weeks = 20s at 1×. Weeks are a cosmetic sub-tick. You build by
// buying/selling discrete units directly; score = cumulative compute organized.

// Per-accelerator phosphor colours.
const ACCEL_COLORS = ["#2f86d6", "#ef8a3c", "#43b3d6", "#8a9a1e", "#a37acc"];
// Shared-infra colours: Power, Cooling, Land, Staff, Network.
const INFRA_COLORS = ["#fa8d3e", "#55b4d4", "#86b300", "#a37acc", "#e6618f"];
const SPEEDS = [1, 2, 4, 8];
const MS_PER_BLOCK = 5000; // 1 block = 1 week = 5s at 1×

let vm = null;
let tab = 0;
let started = false;    // the clock is held until the player presses Start
let playing = true;
let speedIdx = 0;
let cashHistory = [];
let recorded = false;
let acc = 0;            // real-ms accumulated toward the next week-block (0..MS_PER_BLOCK)
let lastT = 0;

// ---- boot ----
(async function boot() {
  const go = new Go();
  let result;
  const WASM = "dcb.wasm?v=23"; // bump with app.js to bust the CDN/browser cache
  try {
    result = await WebAssembly.instantiateStreaming(fetch(WASM), go.importObject);
  } catch (e) {
    const buf = await (await fetch(WASM)).arrayBuffer();
    result = await WebAssembly.instantiate(buf, go.importObject);
  }
  go.run(result.instance);
  const params = new URLSearchParams(location.search);
  window._dcbSeason = params.get("season") || "week-1";
  window._dcbName = params.get("name") || "you";
  window._dcbChain = params.get("chain") === "1";
  live.init(params);
  // A local snapshot seeds the start-screen chrome; in live mode it's a
  // placeholder that act.start()/liveStart() replaces with the on-chain state.
  vm = JSON.parse(dcbNew(window._dcbSeason, window._dcbName, 0, window._dcbChain && !live.on));
  lastT = performance.now();
  render();
  requestAnimationFrame(loop);
  setInterval(sample, 3000);
  if (live.on) setInterval(livePoll, 3000);
  window.addEventListener("keydown", onKey);
})();

// Read-only tabs are safe to fully repaint each frame. Interactive tabs (Cost)
// must NOT repaint mid-frame or button clicks get destroyed.
const LIVE_TABS = { 0: renderDash, 2: renderRevenue };

function loop(now) {
  requestAnimationFrame(loop);
  const dt = (now - lastT) * SPEEDS[speedIdx];
  lastT = now;
  if (!vm || !started) return; // hold everything until Start is pressed
  if (playing && !vm.gameOver && !live.on) { // live mode advances via livePoll, not the local engine
    acc += dt;
    let advanced = false;
    while (acc >= MS_PER_BLOCK) {
      acc -= MS_PER_BLOCK;
      vm = JSON.parse(dcbTick(1));
      advanced = true;
      if (vm.gameOver) break;
    }
    if (advanced && vm.gameOver && !recorded) { recordScore(); render(); return; }
  }
  // Refresh only the volatile clock each frame so the header tab buttons stay
  // stable and remain clickable; full repaint of the active read-only tab;
  // interactive tabs only get their cash strip patched.
  const clk = document.getElementById("clock");
  if (clk) clk.innerHTML = clockInner();
  else document.getElementById("statusbar").innerHTML = renderStatus();
  const fn = LIVE_TABS[tab];
  if (fn && !(vm.gameOver && tab !== 3 && tab !== 4)) {
    const strip = (tab !== 0 && !vm.gameOver) ? `<div id="cashstrip">${headerBar()}</div>` : "";
    document.getElementById("content").innerHTML = strip + fn();
  } else {
    const cs = document.getElementById("cashstrip");
    if (cs) cs.innerHTML = headerBar();
  }
}

function sample() {
  if (vm && !vm.gameOver) {
    cashHistory.push(vm.capital);
    if (cashHistory.length > 80) cashHistory.shift();
  }
}

// ---- live (on-chain) mode ----
// Gated behind ?chain=live&node=<rpc>. The wasm does the Go-only work (keygen,
// build+sign tx, decode state→view-model); this JS layer does the HTTP. The
// node JSON shapes below are per CANOPY.md and need validation against a live
// node (marked VERIFY).
const sleep = ms => new Promise(r => setTimeout(r, ms));

const live = {
  on: false, node: "", chainId: 1, networkId: 1, fee: 10000, faucet: "",
  key: null, addr: "", prevB64: "", sinceCp: 0,

  init(params) {
    this.on = params.get("chain") === "live";
    if (!this.on) return false;
    this.node = (params.get("node") || "http://localhost:50002").replace(/\/$/, "");
    this.chainId = +(params.get("chainId") || 1);
    this.networkId = +(params.get("networkId") || 1);
    this.fee = +(params.get("fee") || 10000);
    this.faucet = params.get("faucet") || "";
    let stored = null;
    try { stored = JSON.parse(localStorage.getItem("dcb.key") || "null"); } catch {}
    this.key = (stored && stored.hex) ? stored : JSON.parse(dcbChainKeyNew());
    try { localStorage.setItem("dcb.key", JSON.stringify(this.key)); } catch {}
    this.addr = this.key.address;
    return true;
  },

  // rpc POSTs with retry+backoff on transient gateway failures. The val-a
  // gateway intermittently 502s under the 3s poll; a 5xx or network error is
  // retried (up to 3 tries, 250ms→500ms→1s) so a blip doesn't blank the UI. A
  // 4xx is a real rejection (e.g. tx invalid) and is returned immediately.
  async rpc(path, body) {
    let lastErr;
    for (let attempt = 0; attempt < 3; attempt++) {
      if (attempt) await sleep(250 * (1 << (attempt - 1)));
      try {
        const r = await fetch(this.node + path, {
          method: "POST", headers: { "content-type": "application/json" },
          body: JSON.stringify(body || {}),
        });
        if (r.status >= 500) { lastErr = new Error("rpc " + r.status); continue; }
        const txt = await r.text();
        try { return JSON.parse(txt); } catch { return txt; }
      } catch (e) { lastErr = e; } // network/CORS error → retry
    }
    throw lastErr || new Error("rpc failed");
  },

  async height() {
    const h = await this.rpc("/v1/query/height", {});
    return (h && (h.height ?? h.Height)) || 0;
  },

  async submit(action, args) {
    const height = await this.height();
    const txjson = dcbChainBuildTx(this.key.hex, action, JSON.stringify(args || {}),
      this.fee, height, this.networkId, this.chainId, Date.now() * 1000 /* micros */);
    if (txjson[0] !== "{") throw new Error("buildTx: " + txjson);
    return this.rpc("/v1/tx", JSON.parse(txjson)); // VERIFY: node /v1/tx body shape
  },

  async snapshot() {
    const res = await this.rpc("/v1/query/events-by-address", { address: this.addr }); // VERIFY shape
    const b64 = extractStateB64(res);
    if (!b64) return null;
    const policy = JSON.stringify((vm && vm.policy) || { regionWeights: [100, 0, 0, 0, 0, 0], leverage: 0 });
    const out = dcbChainViewModel(b64, this.prevB64, policy, 1, 0, window._dcbName || "you");
    this.prevB64 = b64;
    return JSON.parse(out);
  },

  async faucetFund() {
    if (!this.faucet) return;
    try { await fetch(this.faucet, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ address: this.addr }) }); } catch {}
  },

  async begin() {
    await this.faucetFund();
    await this.submit("start_run", { name: window._dcbName || "you" });
    await sleep(1500);
    const s = await this.snapshot();
    if (s) vm = s;
  },

  // route a player action through the chain, then refresh.
  async action(action, args) {
    try { await this.submit(action, args); await sleep(800); const s = await this.snapshot(); if (s) vm = s; }
    catch (e) { console.warn("live action failed", action, e); }
    render();
  },
};

// extractStateB64 walks the events-by-address response for the latest dcb/state
// custom payload (Any.Value, base64 in protojson). Defensive across shapes.
function extractStateB64(res) {
  try {
    const events = res.events || res.Events || res.result || (Array.isArray(res) ? res : []);
    const arr = Array.isArray(events) ? events : [];
    for (let i = arr.length - 1; i >= 0; i--) {
      const e = arr[i] || {};
      const custom = (e.msg && (e.msg.custom || e.msg.Custom)) || e.custom || e.Custom;
      const any = custom && (custom.msg || custom.Msg || custom);
      const tu = any && (any.typeUrl || any.type_url || any.TypeUrl || "");
      const val = any && (any.value || any.Value);
      if (val && (!tu || String(tu).includes("dcb/state"))) return val;
    }
  } catch {}
  return null;
}

async function liveStart() {
  started = true; render(); // show chrome immediately
  try { await live.begin(); } catch (e) { console.warn("live begin failed", e); }
  playing = true; render();
}

async function livePoll() {
  if (!live.on || !started || !playing || !vm || vm.gameOver) return;
  if (++live.sinceCp >= 10) { live.sinceCp = 0; try { await live.submit("checkpoint", {}); await sleep(800); } catch {} }
  try { const s = await live.snapshot(); if (s) vm = s; } catch {}
}

// ---- actions ----
const clamp = (x, lo, hi) => Math.max(lo, Math.min(hi, x));
const money = n => (n < 0 ? "-$" : "$") + Math.abs(Math.round(n)).toLocaleString("en-US");
const signed = n => (n < 0 ? "-$" : "+$") + Math.abs(Math.round(n)).toLocaleString("en-US");
const comma = n => (n < 0 ? "-" : "") + Math.abs(Math.round(n)).toLocaleString("en-US");
const pct1 = x => (x >= 0 ? "+" : "") + x.toFixed(1) + "%";
const esc = s => String(s).replace(/[&<>]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));

// tier is the scaling buy/sell increment for a given current quantity.
function tier(qty) { return qty < 100 ? 10 : qty < 1000 ? 50 : qty < 10000 ? 250 : 1000; }
// staffStep is the tiered hire/fire increment: round up to 10, then +10 to 100,
// then +10% of current headcount (rounded up).
function staffStep(cur) { return cur < 10 ? 10 - cur : cur < 100 ? 10 : Math.max(10, Math.ceil(cur * 0.1)); }

function submitPolicy(mut) {
  const p = JSON.parse(JSON.stringify(vm.policy));
  mut(p);
  if (live.on) {
    const hexPol = dcbEncodePolicy(JSON.stringify(p));
    live.action("set_policy", { policy: hexPol });
    return;
  }
  vm = JSON.parse(dcbSubmit(JSON.stringify(p)));
  render();
}

const act = {
  start() { if (live.on) { liveStart(); return; } started = true; playing = true; acc = 0; lastT = performance.now(); render(); },
  tab(i) { tab = i; render(); },
  play() { playing = !playing; render(); },
  speed(d) { speedIdx = clamp(speedIdx + d, 0, SPEEDS.length - 1); render(); },
  ff() { if (live.on) return; if (!vm.gameOver) { vm = JSON.parse(dcbTick(6)); } render(); }, // ~half a year
  buy(kind, qty) { if (live.on) return live.action("buy", { kind, qty }); vm = JSON.parse(dcbBuy(kind, qty)); render(); },
  sell(kind, qty) { if (live.on) return live.action("sell", { kind, qty }); vm = JSON.parse(dcbSell(kind, qty)); render(); },
  hire(n) { if (live.on) return live.action("hire", { n }); vm = JSON.parse(dcbHire(n)); render(); },
  fire(n) { if (live.on) return live.action("fire", { n }); vm = JSON.parse(dcbFire(n)); render(); },
  infra(kind, qty) { if (live.on) return live.action("infra", { infra: kind, qty }); vm = JSON.parse(dcbBuyInfra(kind, qty)); render(); },
  region(i, d) { submitPolicy(p => { p.regionWeights[i] = clamp(p.regionWeights[i] + d * 5, 0, 100); }); },
  lev(d) { submitPolicy(p => { const L = [0, 15, 20]; p.leverage = L[((L.indexOf(p.leverage) + d) % 3 + 3) % 3]; }); },
  fund(dollars) { vm = JSON.parse(dcbFund(dollars)); render(); },
  repay(dollars) { vm = JSON.parse(dcbRepay(dollars)); render(); },
  endGame() { vm = JSON.parse(dcbEndGame()); recordScore(); render(); },
  newGame() {
    vm = JSON.parse(dcbNew(window._dcbSeason, window._dcbName, 0, window._dcbChain));
    cashHistory = []; recorded = false; tab = 0; started = true; playing = true; acc = 0; lastT = performance.now(); render();
  },
};
window.act = act;

function onKey(e) {
  const k = e.key;
  // Before the game starts, the only key that does anything is Start.
  if (!started && !(vm && vm.gameOver)) {
    if (k === " " || k === "Enter") { e.preventDefault(); act.start(); }
    return;
  }
  if (k >= "1" && k <= "5") act.tab(+k - 1);
  else if (k === " ") { e.preventDefault(); act.play(); }
  else if (k === "+" || k === "=") act.speed(1);
  else if (k === "-" || k === "_") act.speed(-1);
  else if (k === "f") act.ff();
  else if (k === "Tab") { e.preventDefault(); act.tab((tab + 1) % 5); }
}

// ---- leaderboard (localStorage stand-in for the on-chain boards) ----
function loadScores() {
  try { return JSON.parse(localStorage.getItem("dcb.scores") || "[]"); } catch { return []; }
}
function recordScore() {
  if (recorded || !vm) return;
  recorded = true;
  const scores = loadScores();
  // Score is cumulative compute organized (CU) — the ranked metric.
  scores.push({ name: window._dcbName || "you", score: vm.score, ts: Date.now() });
  try { localStorage.setItem("dcb.scores", JSON.stringify(scores)); } catch {}
}

// ---- helpers ----
function logo() {
  const led = c => `<span class="led" style="background:${c}"></span>`;
  return `<div class="logo">
    <div class="rack"><div class="u">${led(ACCEL_COLORS[0])}<span class="lead"></span></div>
    <div class="u">${led(ACCEL_COLORS[1])}<span class="lead"></span></div>
    <div class="u">${led(ACCEL_COLORS[4])}<span class="lead"></span></div></div>
    <div class="wordmark"><div class="name">DCB</div><div class="sub">DATA·CENTER·BUILDER · build 0623a</div></div>
  </div>`;
}

function chart() {
  const H = cashHistory;
  const w = 1000, h = 120, pad = 8;
  if (H.length < 2) {
    return `<svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="none" style="display:block;width:100%;height:110px">
      <text x="${w / 2}" y="${h / 2}" fill="#a7adb3" font-size="15" text-anchor="middle" font-family="inherit">collecting months…</text></svg>`;
  }
  const min = Math.min(...H), max = Math.max(...H), span = Math.max(max - min, 1);
  const n = H.length;
  const px = i => pad + (i / (n - 1)) * (w - pad * 2);
  const py = v => h - pad - ((v - min) / span) * (h - pad * 2);
  const pts = H.map((v, i) => [px(i), py(v)]);
  const d = pts.map((p, i) => (i ? "L" : "M") + p[0].toFixed(1) + " " + p[1].toFixed(1)).join(" ");
  const last = pts[n - 1];
  const area = `<path d="${d} L${last[0].toFixed(1)} ${h - pad} L${pts[0][0].toFixed(1)} ${h - pad} Z" fill="url(#cashFill)"/>`;
  const line = `<path d="${d}" fill="none" stroke="#86b300" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round"/>`;
  const dot = `<circle cx="${last[0]}" cy="${last[1]}" r="3.5" fill="#86b300"/>`;
  const grid = [0.25, 0.5, 0.75].map(f => `<line x1="${pad}" x2="${w - pad}" y1="${h * f}" y2="${h * f}" stroke="rgba(92,97,102,.1)" stroke-dasharray="3 7"/>`).join("");
  return `<svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="none" style="display:block;width:100%;height:110px">
    <defs><linearGradient id="cashFill" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%" stop-color="rgba(134,179,0,.18)"/><stop offset="100%" stop-color="rgba(134,179,0,0)"/></linearGradient></defs>
    ${grid}${area}${line}${dot}</svg>`;
}

// ---- render ----
function render() {
  if (!vm) return;
  document.getElementById("statusbar").innerHTML = renderStatus();
  // Until the player presses Start, hold on a clear start screen (no tabs, no
  // clock) so it's obvious how to begin.
  if (!started && !vm.gameOver) {
    document.getElementById("tabs").innerHTML = "";
    document.getElementById("content").innerHTML = renderStart();
    return;
  }
  document.getElementById("tabs").innerHTML = ""; // tabs now render in the header (renderStatus)
  let body;
  if (vm.gameOver && tab !== 3 && tab !== 4) {
    body = renderOverlay();
  } else {
    const inner = [renderDash, renderCost, renderRevenue, renderBoard, renderRules][tab]();
    const strip = (tab !== 0 && !vm.gameOver) ? `<div id="cashstrip">${headerBar()}</div>` : "";
    body = strip + inner;
  }
  document.getElementById("content").innerHTML = body;
}

// renderStart is the landing screen: title, a one-line goal, the rules in brief,
// and a single obvious Start button. The clock does not advance until Start.
function renderStart() {
  return `<div class="overlay">
    <div class="over-title good">DATA CENTER BUILDER</div>
    <div class="muted" style="max-width:640px;margin:12px auto 0;line-height:1.65;font-size:13.5px">
      Organize as much <b class="bright">compute</b> as you can. On the <b>Cost</b> tab you buy
      servers, power, cooling, land and staff — keep your inputs <b>balanced</b> or production stalls.
      Match your accelerator mix to <b>market demand</b> and watch earnings on the <b>Revenue</b> tab.
      Each block is one week (5s); your score is the total compute you organize.
    </div>
    <div style="margin-top:24px">
      <span class="ctl" style="font-size:15px;padding:11px 26px" onclick="act.start()">▶ Start building</span>
    </div>
    <div class="muted" style="margin-top:14px;font-size:11.5px">space pause · +/− speed · f fast-forward · 1–4 tabs</div>
  </div>`;
}

// tickBar is the "time is moving" indicator: a slim bar that fills over each
// 5s week, then snaps back when the week-block ticks.
function tickBar() {
  const pct = started && playing ? Math.min(100, (acc / MS_PER_BLOCK) * 100) : 0;
  return `<span class="tick-bar"><span class="tick-fill" style="width:${pct.toFixed(0)}%"></span></span>`;
}

function renderStatus() {
  const pres = vm.prestige > 0 ? `<span class="stat">★<b class="star">${vm.prestige}</b></span>` : "";
  // Tabs live in the header whitespace (right-aligned) once the run has started.
  const tabs = (started || vm.gameOver) ? `<span class="tabstrip">${renderTabs()}</span>` : "";
  return logo() +
    `<span class="stat" id="clock">${clockInner()}</span>` +
    pres + tabs;
}

// clockInner is the volatile part of the header (year/week + tick bar) refreshed
// every frame. Kept separate so the per-frame refresh doesn't recreate the tab
// buttons (which would swallow clicks landing across a re-render).
function clockInner() {
  return `Yr <b>${vm.year}</b> · Wk <b>${vm.weekOfYear}</b>${tickBar()}`;
}

function renderTabs() {
  // Only the three live tabs sit in the header (widened to fill it). Leaderboard
  // and Rules are reached from the dashboard nav buttons (and keys 4/5).
  return ["Dashboard", "Cost", "Revenue"].map((n, i) =>
    `<span class="tab ${i === tab ? "active" : ""}" onclick="act.tab(${i})">${i + 1} ${n}</span>`).join("");
}

// headerBar is the thin cash-focus strip (Claude Design "Cash Focus" handoff):
// CASH + net income/wk on the left, then the three key cards (Compute organized,
// Capacity, Operable) inline with vertical dividers. Same height as the old cash
// container, shown at the top of the Cost / Revenue / Leaderboard tabs.
function headerBar() {
  const v = vm;
  const up = v.netFlow >= 0;
  const operPct = Math.round((v.util || 0) * 100);
  const operColor = operPct >= 80 ? "#4ade80" : operPct >= 40 ? "#fbbf24" : "#f87171";
  const cell = (color, label, value, sub) => `<div class="cb-cell">
    <div class="cb-label"><span class="cb-dot" style="background:${color}"></span>${label}</div>
    <div class="cb-val">${value}</div>
    <div class="cb-sub" style="color:${color}">${sub}</div>
  </div>`;
  return `<div class="cashbar">
    <div class="cb-cash">
      <div class="cb-label" style="margin-bottom:5px">CASH</div>
      <div style="display:flex;align-items:baseline;gap:13px">
        <span class="cb-cashnum ${v.capital < 0 ? "bad" : ""}">${money(v.capital)}</span>
        <span style="display:flex;flex-direction:column;line-height:1.25">
          <span class="cb-mini">NET INCOME</span>
          <span class="${up ? "good" : "bad"}" style="font-weight:700;font-size:13px">${up ? "▲" : "▼"} ${signed(v.netFlow)}/wk</span>
        </span>
      </div>
    </div>
    <div class="cb-div"></div>
    ${cell("#86b300", "COMPUTE ORGANIZED", comma(v.score) + ' <span class="cb-unit">CU</span>', comma(v.ucd) + " CU/wk delivered")}
    <div class="cb-div"></div>
    ${cell("#2dd4bf", "CAPACITY", comma(v.capacity) + ' <span class="cb-unit">CU</span>', "bottleneck: " + esc(v.bottleneck || "—"))}
    <div class="cb-div"></div>
    ${cell(operColor, "OPERABLE", operPct + '<span class="cb-unit">%</span>', "input balance health")}
  </div>`;
}

// statTriple is the pinned row of the three key cards shown at the top of the
// first three tabs.
function statTriple() {
  const v = vm;
  const operPct = Math.round((v.util || 0) * 100);
  const operColor = operPct >= 80 ? "#4ade80" : operPct >= 40 ? "#fbbf24" : "#f87171";
  return `<div class="tiles-3">
    ${tile("Compute organized", comma(v.score) + " CU", comma(v.ucd) + " CU/wk delivered", "#86b300")}
    ${tile("Capacity", comma(v.capacity) + " CU", "bottleneck: " + esc(v.bottleneck || "—"), "#2dd4bf")}
    ${tile("Operable", operPct + "%", "input balance health", operColor)}
  </div>`;
}

function tile(name, value, sub, color) {
  return `<div class="tile">
    <div class="tile-head"><span class="led" style="display:inline-block;width:6px;height:6px;border-radius:50%;background:${color}"></span>${name.toUpperCase()}</div>
    <div class="tile-val">${value}</div>
    <div class="tile-sub" style="color:${color}">${sub}</div>
  </div>`;
}

function renderDash() {
  const v = vm;
  const up = v.netFlow >= 0;
  return `
  <div class="hero">
    <div style="display:flex;align-items:center;gap:28px;flex-wrap:wrap">
      <div style="flex-shrink:0">
        <div class="hero-label">CASH</div>
        <div class="hero-num ${v.capital < 0 ? "bad" : ""}">${money(v.capital)}</div>
        <div style="display:flex;align-items:baseline;gap:12px;margin-top:8px">
          <span class="${up ? "good" : "bad"}" style="font-weight:700">${up ? "▲ " : "▼ "}${signed(v.netFlow)}/wk</span>
        </div>
      </div>
      <div style="flex:1;min-width:260px">
        ${chart()}
        <div style="display:flex;justify-content:space-between;font-size:10.5px;color:#2f6b46;margin-top:4px">
          <span>earlier</span><span>now</span>
        </div>
      </div>
    </div>
  </div>
  ${statTriple()}
  <div class="tiles-3" style="margin-top:12px">
    ${tile("Revenue", signed(v.revenue) + "/wk", "compute sold", "#4db8ff")}
    ${tile("Costs+int", signed(-(v.costs + v.interest)) + "/wk", "opex + interest", "#fbbf24")}
    ${tile("Net worth", money(v.netWorth), `${pct1(v.startCash ? (v.netWorth - v.startCash) / v.startCash * 100 : 0)} vs start`, "#4ade80")}
  </div>
  <div class="row" style="margin-top:6px">
    <span class="muted">Demand ${comma(v.demand)} CU/wk · your supply ${comma(v.capacity)} CU · avg price $${(v.avgPrice || 0).toFixed(0)}/CU · 1 block = 1 week (5s)</span>
  </div>
  <div class="dash-nav">
    <button class="dash-navbtn" onclick="act.tab(3)"><span class="dn-t">Leaderboard</span><span class="dn-s">how you rank vs rivals · all-time scores</span></button>
    <button class="dash-navbtn" onclick="act.tab(4)"><span class="dn-t">Rules</span><span class="dn-s">how chips, regions &amp; demand work · strategy</span></button>
  </div>`;
}

// Compact single-row accelerator grid: TYPE (name + maker logo) · OWNED (count +
// −/+ steppers) · PRICE ($/unit, the live escalating buy price). The market
// demand signal lives on the Revenue tab, so it is not duplicated here.
const ACCEL_GRID = "display:grid;grid-template-columns:1fr auto auto;align-items:center;gap:12px;";

function accelStepper(call, label) {
  return `<button onclick="${call}" title="${label}" style="width:26px;height:26px;display:inline-flex;align-items:center;justify-content:center;border:1px solid #dcdce0;border-radius:6px;background:#fff;color:#5c6166;font-size:16px;line-height:1;cursor:pointer;font-family:inherit;flex-shrink:0">${label[0] === "s" ? "−" : "+"}</button>`;
}

function renderCost() {
  const v = vm;
  let h = `<div class="sec"><span class="h">COST</span><span class="note">[ the four levers below configure your operation — buy/sell to drive revenue ]</span></div>`;
  h += `<div class="cost-grid">`;

  // ---- Accelerators card (top-left) ----
  const accelTip = "Chips produce compute (CU). More chips → more compute → more revenue — as long as you can power, cool & staff them and the market wants that type. Types differ in output and power/cooling/land draw, and prices rise over time. See earnings per type on the Revenue tab.";
  h += `<div class="config-card"><div class="card-head"><span class="h" style="font-size:12px">CHIPS</span><span class="info" title="${accelTip}">?</span><span class="note">[ each type: own price + footprint ]</span></div>`;
  h += `<div style="${ACCEL_GRID}padding:8px 0 6px;font-size:10px;letter-spacing:.1em;color:#b6b7bd">
    <span>TYPE</span><span>OWNED</span><span style="text-align:right">PRICE</span></div>`;
  v.accelerators.forEach((a, i) => {
    const inc = tier(a.units);
    h += `<div style="${ACCEL_GRID}padding:6px 0;border-top:1px solid #f0f0f2">
      <div style="display:flex;align-items:center;gap:10px;min-width:0">
        <span style="font-size:15px;font-weight:600;color:${ACCEL_COLORS[i]};white-space:nowrap">${esc(a.name)}</span>
      </div>
      <div style="display:flex;align-items:center;gap:8px">
        <span style="font-size:14px;color:#5c6166;white-space:nowrap"><b style="color:#383a42;font-weight:700">${comma(a.units)}</b> units</span>
        ${accelStepper(`act.sell(${i},${inc})`, `sell ${inc}`)}
        ${accelStepper(`act.buy(${i},${inc})`, `buy ${inc}`)}
      </div>
      <div style="text-align:right;font-variant-numeric:tabular-nums;white-space:nowrap;line-height:1.3">
        <div style="font-size:14px;font-weight:600;color:#383a42">${money(a.costUnit)}<span style="color:#b6b7bd;font-weight:400">/unit</span></div>
        <div style="font-size:11.5px;color:#9b9ca3">resell ${money(a.resaleUnit)}</div>
      </div>
    </div>`;
  });
  h += `</div>`; // end accelerators card

  // ---- Shared infrastructure card (top-right) ----
  h += `<div class="config-card"><div class="card-head"><span class="h" style="font-size:12px">INFRASTRUCTURE</span><span class="note">[ all servers draw on these ]</span></div>`;
  const pInc = tier(v.powerPU), cInc = tier(v.coolingKU);
  const infraRows = [
    ["Power",   `${comma(v.powerPU)} PU`,     money(v.costPU)+"/PU",       INFRA_COLORS[0], `act.infra(0,${pInc})`, pInc, null],
    ["Cooling", `${comma(v.coolingKU)} KU`,   money(v.costKU)+"/KU",       INFRA_COLORS[1], `act.infra(1,${cInc})`, cInc, null],
    ["Land",    `${comma(v.landAcres)} acres`, money(v.costAcre)+"/acre",   INFRA_COLORS[2], "act.infra(2,1)",        1,    null],
    ["Staff",   `${comma(v.staffSU)} people`,  money(v.staffWageWk*52)+"/yr · "+money(v.costHire)+" to hire", INFRA_COLORS[3], `act.hire(${staffStep(v.staffSU)})`, staffStep(v.staffSU), `act.fire(${staffStep(v.staffSU)})`],
    ...(v.networkUnlocked ? [["Network", `${comma(v.networkGbps)} Gbps`, money(v.costGbps)+"/Gbps", INFRA_COLORS[4], "act.infra(3,10)", 10, null]] : []),
  ];
  const infraTableRows = infraRows.map(([label, qty, unit, color, addCall, inc, subCall]) => {
    const minusBtn = subCall
      ? `<button class="step" onclick="${subCall}">−</button>`
      : `<button class="step" disabled style="opacity:.3;cursor:default">−</button>`;
    // Match the accelerator PRICE styling: amount in #383a42, unit suffix muted.
    const slash = unit.indexOf("/");
    const amount = slash >= 0 ? unit.slice(0, slash) : unit;
    const suffix = slash >= 0 ? unit.slice(slash) : "";
    const cost = `<span style="color:#383a42;font-weight:600">${amount}</span><span style="color:#b6b7bd">${suffix}</span>`;
    return `<tr>
      <td style="color:${color}">${label}</td>
      <td style="white-space:nowrap"><b style="margin-right:9px">${qty}</b>${minusBtn} <button class="step" onclick="${addCall}">+</button></td>
      <td>${cost}</td>
    </tr>`;
  }).join("");
  h += `<table><tr><th>Resource</th><th>Owned</th><th>Cost</th></tr>${infraTableRows}</table>`;
  h += `</div>`; // end shared-infra card

  // ---- Funding card ----
  const canFund = vm.fundingCooldownWeeks === 0;
  const hasDebt = vm.debt > 0;
  const fundInfo = `<span class="muted" style="margin-left:6px;font-size:12.5px">Rate ${vm.fundingOfferPct.toFixed(2)}%/wk · Debt ${money(vm.debt)} · lands in your balance immediately</span>`;
  const takeRow = canFund
    ? `<div class="funding-btns" style="margin-top:6px">
        <b class="muted" style="min-width:80px;display:inline-block">Borrow</b>
        <span class="ctl" onclick="act.fund(500000)">+$500K</span>
        <span class="ctl" onclick="act.fund(2000000)">+$2M</span>
        ${fundInfo}
      </div>`
    : `<div class="funding-btns" style="margin-top:6px"><b class="muted" style="min-width:80px;display:inline-block">Borrow</b>
        <span class="muted">available in ${vm.fundingCooldownWeeks} wk (once per year)</span>${fundInfo}</div>`;
  const repayRow = hasDebt
    ? `<div class="funding-btns" style="margin-top:6px">
        <b class="muted" style="min-width:80px;display:inline-block">Repay debt</b>
        <span class="ctl" onclick="act.repay(500000)">−$500K</span>
        <span class="ctl" onclick="act.repay(2000000)">−$2M</span>
      </div>` : "";
  let levRow = "";
  if (v.leverageUnlocked) {
    const lv = v.policy.leverage === 15 ? "1.5x" : v.policy.leverage === 20 ? "2.0x" : "off";
    levRow = `<div class="funding-btns" style="margin-top:6px">
      <b class="muted" style="min-width:80px;display:inline-block">Leverage</b>
      <span class="bright" style="margin-right:8px">${lv}</span>
      <button class="step" onclick="act.lev(-1)">−</button><button class="step" onclick="act.lev(1)">+</button></div>`;
  }
  // ---- Funding card (bottom-left) ----
  h += `<div class="config-card"><div class="card-head"><span class="h" style="font-size:12px">FUNDING</span><span class="note">[ borrow cash now, repay with interest ]</span></div>
    ${takeRow}${repayRow}${levRow}
  </div>`;

  // ---- Region allocation card (bottom-right) ----
  h += `<div class="config-card">${regionTable(false)}</div>`;
  h += `</div>`; // end cost-grid
  return h;
}


function regionTable(spaced) {
  const v = vm;
  const lock = v.regionsUnlocked ? "" : `<div class="note-line">Locked — reach 500 CU capacity to split your build across regions.</div>`;
  let sumR = 0;
  v.regions.forEach(r => { if (r.weight > 0) sumR += r.weight; });
  const rshare = w => (sumR > 0 ? Math.round(w * 100 / sumR) : 0);
  const rows = v.regions.map((r, i) => {
    const riskCls = r.risk === "HIGH" ? "bad" : r.risk === "med" ? "" : "good";
    const stepBtns = `<span style="white-space:nowrap"><button class="step" onclick="act.region(${i},-1)">−</button> <button class="step" onclick="act.region(${i},1)">+</button></span>`;
    return `<tr><td>${esc(r.name)}</td><td>${rshare(r.weight)}% ${stepBtns}</td>
      <td>${r.power.toFixed(2)}x</td><td>${r.cool.toFixed(2)}x</td><td>${r.price.toFixed(2)}x</td>
      <td class="${riskCls}" style="${r.risk === "med" ? "color:var(--c-power)" : ""}">${r.risk}</td>
      <td class="muted">${r.servers > 0 ? comma(r.servers) : ""}</td></tr>`;
  }).join("");
  return `<div class="card-head"><span class="h" style="font-size:12px">REGIONS</span><span class="note">[ where new units are placed · shares ]</span></div>${lock}
    <table><tr><th>Region</th><th>Weight</th><th>Power</th><th>Cool</th><th>Price</th><th>Risk</th><th>Servers</th></tr>${rows}</table>`;
}

// renderRevenue mirrors the Cost tab: each accelerator's delivered CU × price =
// $/wk (bar sized by revenue share), plus a per-week income statement so the
// player sees how adjusting a Cost lever moves the bottom line.
function renderRevenue() {
  const v = vm;
  const totalRev = Math.max(1, v.accelerators.reduce((s, a) => s + a.revenue, 0));
  const ceil = v.priceCeil || 700;
  const accelRows = v.accelerators.map((a, i) => {
    // Bar = how lucrative this chip's market is right now: current sale price as
    // a fraction of the absolute price ceiling (independent of what you hold).
    const barPct = Math.min(100, Math.round((a.price / ceil) * 100));
    const sharePct = Math.round((a.revenue / totalRev) * 100);
    const fill = a.demandCU > 0 ? Math.min(100, Math.round(a.delivered / a.demandCU * 100)) : 0;
    return `<div style="margin:11px 0">
      <div style="display:flex;align-items:center;gap:9px;margin-bottom:5px">
        <span style="font-size:14px;font-weight:600;color:${ACCEL_COLORS[i]}">${esc(a.name)}</span>
        <span class="muted" style="font-size:12px">${comma(a.delivered)}/${comma(a.demandCU)} CU sold @ $${a.price.toFixed(0)}/CU · meeting ${fill}% of demand</span>
        <span class="bright" style="margin-left:auto;font-weight:700">${signed(a.revenue)}/wk <span class="muted" style="font-weight:400">${sharePct}%</span></span>
      </div>
      <div style="height:9px;border-radius:4px;background:#edeef0;overflow:hidden">
        <div style="height:100%;width:${barPct}%;background:${ACCEL_COLORS[i]};border-radius:4px"></div></div>
    </div>`;
  }).join("");

  const line = (label, val, cls, strong, top) =>
    `<div class="row" style="margin:6px 0;${top ? "padding-top:8px;border-top:1px solid var(--bd-22)" : ""}">
      <span class="k"${strong ? ' style="font-weight:700"' : ""}>${label}</span>
      <span class="${cls}"${strong ? ' style="font-weight:700"' : ""}>${val}</span></div>`;
  const incomeStmt =
    line("Gross revenue", signed(v.revenue) + "/wk", "good") +
    line("− Power", signed(-v.opexPower) + "/wk", "bad") +
    line("− Staff wages", signed(-v.opexStaff) + "/wk", "bad") +
    line("− Maintenance", signed(-v.opexMaint) + "/wk", "bad") +
    line("− Interest", signed(-v.interest) + "/wk", "bad") +
    line("Net income", signed(v.netFlow) + "/wk", v.netFlow >= 0 ? "good" : "bad", true, true);

  return `<div class="sec"><span class="h">REVENUE</span><span class="note">[ delivered compute × market price · prices reprice quarterly ]</span></div>
    <div class="grid">
      <div class="panel"><h2>Revenue by accelerator / week</h2>
        <div class="muted" style="font-size:11.5px;margin-bottom:4px">bar = current market price vs the $${comma(ceil)}/CU ceiling — how lucrative each chip is to make right now</div>${accelRows}</div>
      <div class="panel"><h2>Income statement / week</h2>${incomeStmt}
        <div class="muted" style="margin-top:10px;font-size:12px">Operable ${Math.round((v.util || 0) * 100)}% · bottleneck ${esc(v.bottleneck || "—")}. Raise revenue by buying the chips the market wants (Cost tab) and keeping power/cooling/staff ahead of your fleet.</div>
      </div>
    </div>`;
}

function renderBoard() {
  const scores = loadScores().slice().sort((a, b) => b.score - a.score);
  const weekAgo = Date.now() - 7 * 24 * 3600 * 1000;
  const weekly = scores.filter(s => s.ts >= weekAgo);
  const list = (arr) => arr.length
    ? `<table><tr><th>#</th><th>Operator</th><th>Compute (CU)</th></tr>` +
      arr.slice(0, 10).map((s, i) => `<tr><td>${i + 1}.</td><td>${esc(s.name)}</td><td>${comma(s.score)}</td></tr>`).join("") + `</table>`
    : `<div class="muted">No recorded games yet — end a run to post a score.</div>`;
  // Live in-game board (you vs the AI rivals) ranked by cumulative compute.
  const live = (vm.leaderboard || []).map((e, i) =>
    `<tr><td>${i + 1}.</td><td class="${e.you ? "bright" : ""}">${esc(e.name)}${e.you ? " (you)" : ""}</td><td>${comma(e.score)}</td></tr>`).join("");
  const footer = vm.gameOver
    ? `<div class="row" style="margin-top:14px"><span class="muted">Run ended — ${comma(vm.score)} CU organized, recorded.</span>
        <span class="ctl" onclick="act.newGame()">New game</span></div>`
    : `<div class="row" style="margin-top:14px"><span class="muted">Current run: <b class="bright">${comma(vm.score)} CU</b> organized</span>
        <span class="ctl" onclick="act.endGame()">End game &amp; record score</span></div>`;
  return `<div class="sec"><span class="h">LEADERBOARD</span><span class="note">[ cumulative compute organized — the ranked metric ]</span></div>
    <div class="grid">
      <div class="panel"><h2>This season (live)</h2><table><tr><th>#</th><th>Operator</th><th>CU</th></tr>${live}</table></div>
      <div class="panel"><h2>All-Time (your device)</h2>${list(scores)}</div>
      <div class="panel full"><h2>This Week</h2>${list(weekly)}</div>
    </div>
    ${footer}`;
}

// renderRules is the encyclopedia / glossary tab: defines every chip, region and
// infrastructure input from live tuning (so it never drifts from the engine),
// explains the interplay and the rules, and gives a newcomer strategy guide.
function renderRules() {
  const v = vm;
  const card = (title, note, body) =>
    `<div class="config-card"><div class="card-head"><span class="h" style="font-size:12px">${title}</span><span class="note">${note}</span></div>${body}</div>`;
  const term = (t, d) => `<div style="margin:7px 0"><b style="color:#383a42">${t}</b> — <span class="muted">${d}</span></div>`;
  const f1 = x => (Math.round(x * 100) / 100).toString();

  // Static identity blurbs (index order: GPU, TPU, Trainium, Maia, MTIA).
  const makers = ["NVIDIA", "Google", "AWS", "Microsoft", "Meta"];
  const identity = [
    "Flagship general-purpose accelerator. Highest output per unit, but the most power-, cooling-, land- and capital-hungry.",
    "Balanced 1-CU workhorse — moderate draw on every input.",
    "The lean efficiency play: cheapest to buy and the lightest power/cooling/land footprint, at 1 CU/unit.",
    "Balanced-efficient 1-CU type — a touch leaner than TPU.",
    "High-output flagship (2 CU/unit) like the GPU, but slightly leaner on power and land.",
  ];
  const chipRows = (v.accelerators || []).map((a, i) => `<tr>
      <td style="white-space:nowrap"><span style="color:${ACCEL_COLORS[i]};font-weight:600">${esc(a.name)}</span> <span class="muted">${makers[i] || ""}</span></td>
      <td><b>${a.cuPerUnit}</b> CU</td>
      <td>${money(a.costUnit)}</td>
      <td>${f1(a.powerPerUnit)} PU</td>
      <td>${f1(a.coolPerUnit)} KU</td>
      <td>1 / ${a.staffPerUnit > 0 ? Math.round(1 / a.staffPerUnit) : "∞"}</td>
      <td>${f1(a.acrePerUnit)} ac</td>
    </tr>`).join("");
  const chipNotes = (v.accelerators || []).map((a, i) =>
    `<div style="margin:6px 0"><b style="color:${ACCEL_COLORS[i]}">${esc(a.name)}</b> <span class="muted">(${makers[i] || ""}) — ${identity[i] || ""}</span></div>`).join("");

  const regionRows = (v.regions || []).map(r => `<tr>
      <td>${esc(r.name)}</td><td>${r.power.toFixed(2)}x</td><td>${r.cool.toFixed(2)}x</td>
      <td>${r.price.toFixed(2)}x</td><td class="${r.risk === "HIGH" ? "bad" : r.risk === "low" || r.risk === "good" ? "good" : ""}">${r.risk}</td>
    </tr>`).join("");

  let h = `<div class="sec"><span class="h">RULES</span><span class="note">[ how to organize the most compute — read top to bottom ]</span></div>`;

  h += card("THE GOAL", "[ what you're optimizing ]",
    `<div class="muted" style="line-height:1.7">Your score is <b class="bright">cumulative compute (CU) organized</b> — every unit of compute you actually <i>deliver to customers</i>, summed over the whole run. One block = one game-week. You lose the moment <b>cash goes negative</b>. So: build compute, sell it into demand, and stay solvent while costs climb.</div>`);

  h += card("THE PRODUCTION LOOP — RECIPROCITY", "[ the core rule ]",
    `<div class="muted" style="line-height:1.7">Chips don't run on their own. Every unit needs four inputs <b>in balance</b>: <span style="color:${INFRA_COLORS[0]}">Power</span>, <span style="color:${INFRA_COLORS[1]}">Cooling</span>, <span style="color:${INFRA_COLORS[2]}">Land</span> and <span style="color:${INFRA_COLORS[3]}">Staff</span>. Output is set by your <b>weakest</b> input — owning 1000 chips but enough power for 600 means you produce as if you had 600. Over-buying one input is wasted money; under-buying any one throttles the whole floor. Capacity also ramps <b>gradually</b> (it takes a few weeks for new hardware to reach full operation, and a starved input bites within a week or two).</div>`);

  h += card("CHIPS", "[ each type trades output against footprint ]",
    `<table><tr><th>Type</th><th>Output</th><th>Buy</th><th>Power</th><th>Cool</th><th>Staff</th><th>Land</th></tr>${chipRows}</table>
     <div style="margin-top:10px">${chipNotes}</div>
     <div class="muted" style="margin-top:8px;font-size:12px">"Staff 1 / N" = one person operates ~N of these units. "Buy" is the one-time price per unit; chips also have a separate market <b>sale price</b> ($/CU) that floats with demand — see Revenue.</div>`);

  h += card("REGIONS", "[ where you place hardware · multipliers vs Virginia baseline ]",
    `<table><tr><th>Region</th><th>Power cost</th><th>Cooling</th><th>Price</th><th>Risk</th></tr>${regionRows}</table>
     <div class="muted" style="margin-top:8px;line-height:1.7">You start in Virginia. Splitting across regions <b>unlocks at 500 CU</b> of capacity. Cheaper power/cooling regions cut opex; higher "Price" regions sell compute for more; higher risk means more frequent local disruptions (heatwaves, grid strain, geopolitics). New purchases are placed by your region weights on the Cost tab.</div>`);

  h += card("INFRASTRUCTURE", "[ the shared pools every chip draws on ]",
    term(`<span style="color:${INFRA_COLORS[0]}">Power (PU)</span> · ${money(v.costPU)}/PU`, "Electricity capacity. The #1 recurring bill — you pay for every PU you own each week, and it climbs over time. Buy roughly what your fleet needs, not far more.") +
    term(`<span style="color:${INFRA_COLORS[1]}">Cooling (KU)</span> · ${money(v.costKU)}/KU`, "Heat-removal capacity. A one-time buy (no weekly bill), but its price is erratic and hotter/denser regions burn more of it.") +
    term(`<span style="color:${INFRA_COLORS[2]}">Land (acres)</span> · ${money(v.costAcre)}/acre`, "Floor space. The fastest-rising cost over the years — secure it before it gets pricey.") +
    term(`<span style="color:${INFRA_COLORS[3]}">Staff (people)</span> · ${money(v.costHire)}/hire`, "People operate the chips. You start as a one-person shop and hire in growing batches (up to 10, then +10s to 100, then +10% of headcount). The hire price is one-time recruiting; the real cost is the weekly salary (see below).") +
    term(`<span style="color:${INFRA_COLORS[4]}">Network (Gbps)</span> · ${money(v.costGbps)}/Gbps`, "Unlocks late (at high net worth); caps how much compute you can actually serve. Buy enough or it bottlenecks delivery."));

  h += card("THE PEOPLE — AND WHY LABOR MATTERS", "[ answering: what's the human's role? ]",
    `<div class="muted" style="line-height:1.7">People are the team that runs the floor. A data center is <b>not</b> labor-intensive — a small crew operates thousands of chips (see "Staff 1 / N" above) — but they're <b>well paid</b>: a salary of <b class="bright">~$100k/year</b> (charged prorated each week) that <b>rises ~15%/year</b>. So labor is a large <b>fixed</b> cost from day one: your starting crew costs the same whether you run 5 chips or 5,000. That's the early-game pressure — you must deploy enough compute to cover the payroll. As you scale, power overtakes labor as the biggest bill. (Note: the "hire" price is one-time recruiting, separate from the recurring salary.)</div>`);

  h += card("DEMAND & WHY DIVERSIFYING HELPS", "[ the market rotates ]",
    `<div class="muted" style="line-height:1.7">Customers want a <b>changing mix</b> of chip types. Each quarter a different type is in favor (a full rotation takes about a year). You can only sell up to each type's <b>share of demand</b> — make 1000 CU of a type customers only want 300 of, and 700 sits idle while still costing power and staff. Matching your fleet to the rotating mix maximizes delivered CU (your score). <b>Diversifying</b> across types hedges the rotation: when one chip falls out of favor, another is rising, so your delivery stays steady. Prices follow supply vs demand — flood a type and its price craters; hold a scarce in-demand type and it spikes.</div>`);

  h += card("DIFFICULTY OVER TIME", "[ why set-and-forget fails ]",
    `<div class="muted" style="line-height:1.7">The world gets harder. Input prices and salaries climb every year and <b>step up every 5 years</b>; cooling prices swing erratically; the favored chip keeps rotating. A fleet that's profitable today bleeds out in a few years if you never touch it. Keep re-architecting: re-balance inputs, rotate your chip mix, and expand into cheaper regions.</div>`);

  h += card("STRATEGY — IF YOU'VE NEVER PLAYED THIS KIND OF GAME", "[ a starting playbook ]",
    `<ol class="muted" style="line-height:1.8;margin:0;padding-left:20px">
      <li><b>Deploy fast.</b> You begin with a tiny fleet and a full payroll, so you're losing money. On the <b>Cost</b> tab, buy chips plus the Power / Cooling / Land to run them — keep the four inputs balanced.</li>
      <li><b>Watch the Revenue tab.</b> It shows which chip the market wants now and what each earns. Lean toward the in-favor types, but hold a spread.</li>
      <li><b>Scale until you're cash-positive,</b> then reinvest profit into more balanced capacity. Don't overbuild past what demand can absorb.</li>
      <li><b>Use Funding</b> (Cost tab) to bridge growth if cash is tight — borrow, build, repay from profits.</li>
      <li><b>Re-architect each year.</b> As costs rise and the favored chip rotates, rebalance and expand to cheaper regions (unlocked at 500 CU). Survive the 5-year difficulty steps.</li>
    </ol>`);

  return h;
}

function renderOverlay() {
  const grew = vm.score > 0;
  return `<div class="overlay">
    <div class="over-title ${grew ? "good" : "bad"}">${vm.capital < 0 ? "INSOLVENT — GAME OVER" : "RUN ENDED"}</div>
    <div class="over-num">${comma(vm.score)} CU</div>
    <div class="muted">compute organized · ${vm.year}y ${vm.weekOfYear}wk · net worth ${money(vm.netWorth)}</div>
    <div style="margin-top:18px"><span class="ctl" onclick="act.newGame()">New game</span>
      <span class="ctl" onclick="act.tab(3)">View leaderboard</span></div>
  </div>`;
}
