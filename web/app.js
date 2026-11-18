"use strict";

const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const icon = (id, cls = "") => `<svg class="ic ${cls}"><use href="#${id}"/></svg>`;
const store = {
  get(k, d) { try { const v = localStorage.getItem("mxh." + k); return v == null ? d : JSON.parse(v); } catch { return d; } },
  set(k, v) { try { localStorage.setItem("mxh." + k, JSON.stringify(v)); } catch {} },
};

let cfg = null;        // full config
let st = null;         // last /api/state
let controls = [];     // MX keys from /api/controls
let profile = store.get("profile", "");
let extraProfiles = []; // profiles added this session that have no macros yet
let selId = null;      // selected macro id
let draft = null;      // macro being edited (copy)
let dirty = false;
let history = [];      // undo stack of drafts
let selAct = -1;       // selected action row (palette inserts after it)
let pickMode = false;  // next keyboard click sets the draft trigger
let mods = new Set();  // modifiers toggled on the assignment keyboard
let expandPaths = false;
let oemMap = store.get("oem", {}); // keyboard position -> key name learned by capture

// ---------- api ----------
async function api(method, path, body, raw) {
  const opt = { method, headers: {} };
  if (body !== undefined) {
    opt.body = raw ? body : JSON.stringify(body);
    opt.headers["Content-Type"] = raw ? "text/plain" : "application/json";
  }
  const r = await fetch(path, opt);
  const ct = r.headers.get("Content-Type") || "";
  const data = ct.includes("json") ? await r.json() : await r.text();
  if (!r.ok) throw new Error((data && data.error) || data || r.statusText);
  return data;
}

function toast(msg, err) {
  const t = $("#toast");
  t.textContent = msg;
  t.className = "toast" + (err ? " err" : "");
  t.hidden = false;
  clearTimeout(toast._t);
  toast._t = setTimeout(() => (t.hidden = true), err ? 5000 : 2400);
}

const uid = () => Math.random().toString(16).slice(2, 14);
const clone = o => JSON.parse(JSON.stringify(o));
const ieq = (a, b) => (a || "").toLowerCase() === (b || "").toLowerCase();

// ---------- modal ----------
// modal({title, body (html), ok, cancel, buttons:[{label,value,primary}], init(el)}) -> Promise<value|null>
function modal(o) {
  return new Promise(res => {
    const m = $("#modal");
    $("#mdTitle").textContent = o.title || "";
    $("#mdBody").innerHTML = o.body || "";
    const foot = $(".dialog-foot", m);
    const btns = o.buttons || [{ label: o.cancel || "Vazgeç", value: null }, { label: o.ok || "Tamam", value: true, primary: true }];
    foot.innerHTML = btns.map((b, i) => `<button class="btn ${b.primary ? "primary" : b.danger ? "ghost danger" : "ghost"}" data-i="${i}">${esc(b.label)}</button>`).join("");
    const done = v => { m.hidden = true; document.removeEventListener("keydown", onKey, true); res(v); };
    const collect = v => (v !== null && o.collect ? o.collect($("#mdBody")) : v);
    $$("button", foot).forEach(b => b.onclick = () => done(collect(btns[+b.dataset.i].value)));
    const onKey = e => {
      if (e.key === "Escape") { e.preventDefault(); done(null); }
      if (e.key === "Enter" && e.target.tagName !== "TEXTAREA") {
        const p = btns.findIndex(b => b.primary);
        if (p >= 0) { e.preventDefault(); done(collect(btns[p].value)); }
      }
    };
    document.addEventListener("keydown", onKey, true);
    m.hidden = false;
    if (o.init) o.init($("#mdBody"), done);
    const f = $("input, select, textarea", $("#mdBody"));
    if (f) setTimeout(() => f.focus(), 30);
  });
}
const ask = (title, text, ok = "Evet", danger) => modal({ title, body: `<p class="muted">${text}</p>`, buttons: [{ label: "Vazgeç", value: null }, { label: ok, value: true, primary: !danger, danger }] });
const promptNum = (title, label, value) => modal({
  title, body: `<div class="field"><label>${esc(label)}</label><input type="number" id="pn" value="${value}"></div>`,
  collect: b => { const v = parseFloat($("#pn", b).value); return isNaN(v) ? null : v; },
});

// process picker
async function pickApp(title) {
  let list = [];
  try { list = await api("GET", "/api/processes"); } catch {}
  return modal({
    title: title || "Program seç",
    body: `<div class="search">${icon("i-search", "sm")}<input id="pq" placeholder="Ara ya da yaz: oyun.exe"></div>
           <div class="plist" id="pl"></div>
           <div class="row between mt"><span class="muted small">Listede yoksa adını yazıp Tamam’a bas.</span>
           <button class="btn sm" id="pfg">Öndeki pencere (3 sn)</button></div>`,
    collect: b => { const s = b.querySelector(".plist .on"); const v = s ? s.dataset.v : $("#pq", b).value.trim(); return v || null; },
    init: (b, done) => {
      const draw = () => {
        const q = $("#pq", b).value.trim().toLowerCase();
        const items = list.filter(p => !q || p.toLowerCase().includes(q)).slice(0, 200);
        $("#pl", b).innerHTML = items.map(p => `<button data-v="${esc(p)}">${esc(p)}</button>`).join("") || '<div class="muted small" style="padding:8px">Eşleşen çalışan program yok.</div>';
        $$("#pl button", b).forEach(x => {
          x.onclick = () => { $$("#pl button", b).forEach(y => y.classList.remove("on")); x.classList.add("on"); };
          x.ondblclick = () => done(x.dataset.v);
        });
      };
      $("#pq", b).oninput = draw;
      $("#pfg", b).onclick = async () => {
        $("#pfg", b).textContent = "Hedef pencereye geç…";
        try {
          const r = await api("GET", "/api/foreground?delay=3");
          if (r.app && !/msedge\.exe/i.test(r.app)) done(r.app);
          else { $("#pfg", b).textContent = "Öndeki pencere (3 sn)"; toast("Program algılanamadı", true); }
        } catch { $("#pfg", b).textContent = "Öndeki pencere (3 sn)"; }
      };
      draw();
    },
  });
}

// ---------- trigger helpers ----------
const ALIAS = {
  control: "ctrl", lctrl: "ctrl", rctrl: "ctrl", lalt: "alt", ralt: "alt", altgr: "alt", lshift: "shift", rshift: "shift",
  lwin: "win", rwin: "win", return: "enter", escape: "esc", del: "delete", ins: "insert", pageup: "pgup", pagedown: "pgdn",
  prtsc: "printscreen", apps: "menu", x1: "mouse4", xbutton1: "mouse4", x2: "mouse5", xbutton2: "mouse5", mouse3: "middle", mbutton: "middle",
};
const MODS = ["ctrl", "alt", "shift", "win"];
function normTrig(t) {
  t = (t || "").trim().toLowerCase().replace(/\s+/g, "");
  if (!t) return "";
  if (t.startsWith("mx:")) {
    const n = parseInt(t.slice(3).replace(/^0x/, ""), 16);
    return isNaN(n) ? "" : "mx:0x" + n.toString(16).padStart(4, "0");
  }
  const parts = t.split("+").filter(Boolean).map(p => ALIAS[p] || p);
  if (parts.length === 1 && ["mouse4", "mouse5", "middle"].includes(parts[0])) return parts[0];
  const ms = MODS.filter(m => parts.includes(m));
  const main = parts.filter(p => !MODS.includes(p)).pop();
  return main ? [...ms, main].join("+") : "";
}
const trigMain = t => { t = normTrig(t); return t.startsWith("mx:") ? t : t.split("+").pop(); };
const trigMods = t => normTrig(t).split("+").slice(0, -1);

const KEYLABEL = { esc: "Esc", delete: "Del", backspace: "⌫", tab: "Tab", capslock: "Caps", enter: "Enter", space: "Boşluk", left: "←", right: "→", up: "↑", down: "↓", ctrl: "Ctrl", alt: "Alt", shift: "Shift", win: "Win", playpause: "⏯", next: "⏭", prev: "⏮", mute: "🔇", volup: "🔊", voldown: "🔉", pgup: "PgUp", pgdn: "PgDn", printscreen: "PrtSc", plus: "=", minus: "-", comma: ",", period: "." };
const keyLabel = p => KEYLABEL[p] || (p.length === 1 ? p.toUpperCase() : p.replace(/^f(\d+)$/, "F$1"));

function ctlName(cid) {
  const c = controls.find(x => x.cid === cid);
  return c && c.name !== "?" ? c.name.replace("?", "") : "";
}

function triggerHTML(t) {
  t = normTrig(t);
  if (!t) return '<span class="muted">Tetikleyici seçilmedi</span>';
  if (t.startsWith("mx:")) {
    const n = ctlName(t);
    const c = controls.find(x => x.cid === t);
    return `<kbd>MX</kbd> ${n ? esc(n) : `<span class="mono">${esc(t.slice(3))}</span>`}${c && c.pos ? ` <span class="muted small">(F${c.pos})</span>` : ""}`;
  }
  const mouse = { mouse4: "Fare geri (yan 4)", mouse5: "Fare ileri (yan 5)", middle: "Fare orta tuş" };
  if (mouse[t]) return `${icon("i-mouse", "sm")} ${mouse[t]}`;
  return t.split("+").map(p => `<kbd>${esc(keyLabel(p))}</kbd>`).join('<span class="plus">+</span>');
}

// ---------- keyboard model ----------
// Units: 15 wide, 5.75 tall (the function row is 0.75 high).
const KB = (() => {
  const keys = [];
  let y = 0;
  const row = (h, list) => {
    let x = 0;
    for (const k of list) {
      const [id, label, w = 1, extra = {}] = k;
      if (id) keys.push({ id, label, x, y, w, h, ...extra });
      x += w;
    }
    y += h;
  };
  const F = n => ["f" + n, "F" + n, 1, { fkey: n }];
  row(0.75, [["esc", "Esc", 1.5], ...[1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12].map(F), ["delete", "Del", 1.5]]);
  row(1, [["oem_tick", '"', 1, { oem: true }], ..."1234567890".split("").map(c => [c, c]), ["oem_minus", "*", 1, { oem: true }], ["oem_eq", "-", 1, { oem: true }], ["backspace", "⌫", 2]]);
  row(1, [["tab", "Tab", 1.5], ..."qwertyuiop".split("").map(c => [c, c.toUpperCase()]), ["oem_lb", "Ğ", 1, { oem: true }], ["oem_rb", "Ü", 1, { oem: true }], ["oem_bs", ",", 1.5, { oem: true }]]);
  row(1, [["capslock", "Caps", 1.75], ..."asdfghjkl".split("").map(c => [c, c.toUpperCase()]), ["oem_semi", "Ş", 1, { oem: true }], ["oem_quote", "İ", 1, { oem: true }], ["enter", "Enter", 2.25]]);
  row(1, [["lshift", "Shift", 2.25, { mod: "shift" }], ..."zxcvbnm".split("").map(c => [c, c.toUpperCase()]), ["oem_comma", "Ö", 1, { oem: true }], ["oem_period", "Ç", 1, { oem: true }], ["oem_slash", ".", 1, { oem: true }], ["rshift", "Shift", 2.75, { mod: "shift" }]]);
  const by = y;
  row(1, [["fn", "fn", 1, { dis: true }], ["lctrl", "Ctrl", 1, { mod: "ctrl" }], ["lwin", "Win", 1, { mod: "win" }], ["lalt", "Alt", 1, { mod: "alt" }], ["space", "", 6], ["ralt", "Alt Gr", 1, { mod: "alt" }], ["rctrl", "Ctrl", 1, { mod: "ctrl" }], ["left", "←", 1], [null, "", 1], ["right", "→", 1]]);
  keys.push({ id: "up", label: "↑", x: 13, y: by, w: 1, h: 0.5, half: true }, { id: "down", label: "↓", x: 13, y: by + 0.5, w: 1, h: 0.5, half: true });
  return keys;
})();
const KB_W = 15, KB_H = 5.75;
const keyName = k => k.oem ? oemMap[k.id] || "" : k.id;

function buildKB(el, interactive) {
  el.classList.toggle("interactive", !!interactive);
  el.innerHTML = '<div class="kb-inner">' + KB.map(k => {
    const g = k.half ? 0.3 : 0.5;
    const style = `left:${k.x / KB_W * 100}%;top:${k.y / KB_H * 100}%;width:calc(${k.w / KB_W * 100}% - ${g}cqw);height:calc(${k.h / KB_H * 100}% - ${g}cqw)`;
    const cls = ["key", k.fkey || k.id === "esc" || k.id === "delete" ? "fnrow" : "", k.half ? "half" : "", k.dis ? "dis" : ""].join(" ");
    return `<button class="${cls}" data-k="${k.id}" style="${style}" tabindex="-1"><span class="kl">${esc(k.label)}</span><span class="sub"></span></button>`;
  }).join("") + "</div>";
  if (interactive) $$(".key", el).forEach(b => b.onclick = e => onKeyClick(KB.find(k => k.id === b.dataset.k), b, e));
}

const fkeyControl = n => controls.find(c => c.pos === n && c.divertable);

function decorateFKeys(el) {
  KB.filter(k => k.fkey).forEach(k => {
    const b = $(`[data-k="${k.id}"]`, el);
    const c = fkeyControl(k.fkey);
    b.classList.toggle("mx", !!c);
    $(".sub", b).textContent = c ? ctlName(c.cid) : "";
    b.title = c ? `F${k.fkey} · MX: ${ctlName(c.cid) || c.cid}` : "";
  });
}

// ---------- nav ----------
// switching tabs keeps an unsaved draft; it is still there when you come back
function go(tab) {
  $$("#nav button").forEach(x => x.classList.toggle("active", x.dataset.tab === tab));
  $$(".tab").forEach(t => t.classList.toggle("active", t.id === "tab-" + tab));
  store.set("tab", tab);
  if (tab === "adv") loadRaw();
}
$$("#nav button").forEach(b => b.onclick = () => go(b.dataset.tab));
$$("[data-go]").forEach(b => b.onclick = () => go(b.dataset.go));

function seg(el, value, onPick) {
  $$("button", el).forEach(b => {
    b.classList.toggle("on", b.dataset.v === value);
    if (onPick) b.onclick = () => onPick(b.dataset.v);
  });
}

// ---------- state ----------
let lastRunning = "";
async function refreshState() {
  try { st = await api("GET", "/api/state"); } catch { return; }
  $("#ver").textContent = st.version;
  const on = st.connected;
  $("#devPill").classList.toggle("on", on);
  $("#devText").textContent = on ? (st.name || "MX Keys") : "Bağlı değil";
  const b = batt();
  $("#battIc").hidden = !b.known;
  $("#battIcFill").style.width = b.pct + "%";
  $("#battIcFill").style.background = battColor(b.pct);
  $("#battTop").textContent = b.known ? `%${b.pct}${b.chg ? " ⚡" : ""}` : "";
  $("#gmBtn").classList.toggle("on", !!st.game_mode);
  $("#stopAllTop").hidden = !(st.running || []).length;
  renderHomeState();
  renderLightState();
  renderGameState();
  renderUpdate(st.update);
  const run = (st.running || []).slice().sort().join(",");
  if (run !== lastRunning) { lastRunning = run; renderMacroList(); }
}

function batt() {
  const b = (st && st.battery) || {};
  return { known: !!b.known, pct: b.percent ?? 0, chg: b.charging, full: b.full };
}
const battColor = p => p <= 15 ? "var(--bad)" : p <= 30 ? "var(--warn)" : "var(--ok)";

// ---------- device page ----------
function litKB(el) {
  const bl = st && st.backlight;
  const lit = bl && bl.known && bl.enabled;
  el.classList.toggle("lit", !!lit);
  if (lit) el.style.setProperty("--lit", bl.mode === 1 ? .55 : (bl.level + 1) / Math.max(1, bl.levels || 8));
}

function renderHomeState() {
  $("#homeName").textContent = st.name || "MX Keys Mini";
  $("#homeConn").textContent = st.connected ? "Bluetooth ile bağlı" : "Bağlı değil — klavyeye dokununca bağlanır";
  $("#homeConn").classList.toggle("on", st.connected);
  litKB($("#kbHome"));
  const b = batt();
  $("#battArc").style.strokeDashoffset = b.known ? 97.4 * (1 - b.pct / 100) : 97.4;
  $("#battArc").style.stroke = battColor(b.pct);
  $("#battText").textContent = b.known ? `%${b.pct}` : "—";
  $("#battSub").textContent = !b.known ? "bilinmiyor" : b.chg ? "şarj oluyor" : b.full ? "dolu" : "pilde";
  const bl = st.backlight;
  $("#tileBL").textContent = !bl.known ? "—" : !bl.enabled ? "Kapalı" : bl.mode === 1 ? "Otomatik" : "Seviye " + bl.level;
  const lv = bl.levels || 8;
  $("#tileBLBars").innerHTML = bl.known ? Array.from({ length: lv - 1 }, (_, i) => `<i class="${bl.enabled && bl.level > i ? "on" : ""}" style="height:${30 + i * 70 / (lv - 1)}%"></i>`).join("") : "";
  $("#tileBLSub").textContent = st.backlight_override ? "elle ayarlandı" : "kurallar uygulanıyor";
  const s = st.repair.successes || {};
  $("#repairCnt").textContent = Object.values(s).reduce((a, x) => a + x, 0) || 0;
  const lvl = st.repair.last_working_level;
  $("#repairLvl").textContent = lvl ? `başarılı onarım · son işe yarayan: seviye ${lvl}` : "başarılı onarım";
  $("#autostart").checked = st.autostart;
  $("#elevInfo").textContent = st.elevated ? "Şu an yönetici olarak çalışıyor." : "Şu an normal kullanıcı olarak çalışıyor; onarım yönetici izni isteyecek.";
  const w = $("#logiWarn");
  w.hidden = !st.logi_apps.length;
  w.textContent = st.logi_apps.length ? `Açık Logitech yazılımı: ${st.logi_apps.join(", ")}. Özel tuşlar ve ışık ayarları onlarla çakışabilir.` : "";
}
function renderHomeCfg() {
  const n = cfg.macros.filter(m => m.enabled).length;
  $("#tileMacros").textContent = n;
  const profs = profiles().length - 1;
  $("#tileMacrosSub").textContent = `etkin makro · ${profs ? profs + " program profili" : "tek profil"}`;
  seg($("#autoSeg"), cfg.repair.auto, v => { cfg.repair.auto = v; seg($("#autoSeg"), v); });
  cfg.update = cfg.update || { mode: "ask" };
  seg($("#updSeg"), cfg.update.mode, async v => { cfg.update.mode = v; seg($("#updSeg"), v); await saveCfg(v === "off" ? "Otomatik güncelleme kapalı" : "Kaydedildi"); });
  $("#mediaN").value = cfg.repair.media_presses_to_suspect;
  $("#lowWarn").value = cfg.low_battery_warn_percent;
}

$("#repairBtn").onclick = async () => { await api("POST", "/api/repair"); toast("Onarım başlatıldı — birkaç saniye sürebilir"); };
$("#saveHome").onclick = async () => {
  cfg.repair.media_presses_to_suspect = +$("#mediaN").value || 3;
  cfg.low_battery_warn_percent = +$("#lowWarn").value || 0;
  await saveCfg("Kaydedildi");
};
$("#resetRepair").onclick = async () => {
  if (!(await ask("Onarım geçmişi", "İşe yarayan seviye ve sayaçlar sıfırlansın mı?", "Sıfırla", true))) return;
  await api("POST", "/api/repair/reset"); toast("Onarım geçmişi sıfırlandı"); refreshState();
};
$("#autostart").onchange = async e => {
  toast("Yönetici izni isteniyor…");
  const r = await api("POST", "/api/autostart", { on: e.target.checked });
  e.target.checked = r.on;
  toast(r.on ? "Açılışta yönetici olarak başlayacak" : "Otomatik başlatma kapalı");
};

async function saveCfg(msg) {
  try {
    cfg = await api("PUT", "/api/config", cfg);
    if (msg) toast(msg);
    renderHomeCfg();
    return true;
  } catch (e) { toast(e.message, true); return false; }
}

// ---------- capture ----------
async function capture(title, text) {
  const ov = $("#overlay");
  $("#ovTitle").textContent = title || "Bir tuşa bas";
  $("#ovText").innerHTML = text || "Klavye kombinasyonu (ör. <kbd>Ctrl</kbd>+<kbd>Alt</kbd>+<kbd>K</kbd>), fare yan tuşu veya MX özel tuşu. 15 saniye beklenir.";
  ov.hidden = false;
  try {
    const r = await api("POST", "/api/capture");
    return r.trigger || "";
  } catch (e) {
    if (!String(e.message).includes("aşımı")) toast(e.message, true);
    return "";
  } finally { ov.hidden = true; }
}
$("#ovCancel").onclick = () => api("POST", "/api/capture/cancel");

// ---------- profiles ----------
function profiles() {
  const out = [""];
  const add = a => { if (a && !out.some(x => ieq(x, a))) out.push(a); };
  cfg.macros.forEach(m => add(m.app));
  extraProfiles.forEach(add);
  return out;
}
const profLabel = p => p ? p.replace(/\.exe$/i, "") : "Varsayılan";
const inProfile = (m, p) => ieq(m.app, p);

function setProfile(p) {
  profile = p;
  store.set("profile", p);
  $("#profileName").textContent = p ? profLabel(p) + " profili" : "Varsayılan profil";
  if (draft && !inProfile(draft, p) && !dirty) { draft = null; selId = null; renderEditor(); }
  renderMacroList(); updateAssignKB();
}

function renderProfileMenu() {
  const m = $("#profileMenu");
  m.innerHTML = profiles().map(p => {
    const n = cfg.macros.filter(x => inProfile(x, p)).length;
    return `<button class="pitem ${ieq(p, profile) ? "on" : ""}" data-p="${esc(p)}">${icon(p ? "i-app" : "i-kb")}<span class="pn">${esc(p ? profLabel(p) : "Varsayılan")}</span><span class="pc">${n} makro</span>${p ? `<span class="pdel" data-del="${esc(p)}" title="Profili sil">${icon("i-trash", "sm")}</span>` : ""}</button>`;
  }).join("") + `<div class="sep"></div><button class="pitem padd" id="addProfile">${icon("i-plus")}<span class="pn">Program profili ekle…</span></button>`;
  $$(".pitem[data-p]", m).forEach(b => b.onclick = e => {
    if (e.target.closest("[data-del]")) return;
    m.hidden = true; setProfile(b.dataset.p);
  });
  $$("[data-del]", m).forEach(b => b.onclick = async e => {
    e.stopPropagation(); m.hidden = true;
    const p = b.dataset.del;
    const ms = cfg.macros.filter(x => inProfile(x, p));
    if (!ms.length) { extraProfiles = extraProfiles.filter(x => !ieq(x, p)); if (ieq(profile, p)) setProfile(""); return; }
    const v = await modal({
      title: `“${profLabel(p)}” profilini sil`,
      body: `<p class="muted">Bu profilde ${ms.length} makro var.</p>`,
      buttons: [{ label: "Vazgeç", value: null }, { label: "Makroları varsayılana taşı", value: "move" }, { label: "Makrolarla birlikte sil", value: "del", primary: true }],
    });
    if (!v) return;
    if (v === "move") ms.forEach(x => (x.app = "")); else cfg.macros = cfg.macros.filter(x => !inProfile(x, p));
    extraProfiles = extraProfiles.filter(x => !ieq(x, p));
    if (draft && ms.some(x => x.id === draft.id)) { draft = null; selId = null; dirty = false; renderEditor(); }
    await saveCfg("Profil silindi");
    setProfile("");
  });
  $("#addProfile").onclick = async () => {
    m.hidden = true;
    const a = await pickApp("Hangi program için profil?");
    if (!a) return;
    if (!profiles().some(x => ieq(x, a))) extraProfiles.push(a);
    setProfile(profiles().find(x => ieq(x, a)));
    go("macros");
    toast(`“${profLabel(a)}” profili: bu programdayken buradaki makrolar çalışır`);
  };
}
$("#profileBtn").onclick = e => { e.stopPropagation(); const m = $("#profileMenu"); if (m.hidden) renderProfileMenu(); m.hidden = !m.hidden; };

// ---------- assignment keyboard ----------
function boundMap() {
  // trigger -> "own" | "inh" for the current profile
  const map = {};
  for (const m of cfg.macros) {
    if (!m.enabled) continue;
    const t = normTrig(m.trigger);
    if (!t) continue;
    if (inProfile(m, profile)) map[t] = "own";
    else if (!m.app && !map[t]) map[t] = "inh";
  }
  return map;
}

function keyTrigger(k) {
  // trigger name for a key with current modifier toggles
  const name = keyName(k);
  return name ? normTrig([...mods, name].join("+")) : "";
}

function updateAssignKB() {
  if (!cfg) return;
  const el = $("#kbAssign");
  decorateFKeys(el);
  const bound = boundMap();
  const selTrig = draft ? normTrig(draft.trigger) : "";
  // keys whose main part (with the current modifiers) is bound
  const modsKey = MODS.filter(m => mods.has(m));
  const selMain = selTrig && !selTrig.startsWith("mx:") ? trigMain(selTrig) : "";
  const selMods = selTrig ? trigMods(selTrig) : [];
  for (const k of KB) {
    const b = $(`[data-k="${k.id}"]`, el);
    if (!b) continue;
    let state = "", sel = false;
    if (k.mod) {
      b.classList.toggle("mod-on", mods.has(k.mod));
      sel = !mods.size && selMods.includes(k.mod);
    } else {
      const name = keyName(k);
      const t = name ? normTrig([...modsKey, name].join("+")) : "";
      if (t) { state = bound[t] || ""; sel = name === selMain; }
      const c = k.fkey && fkeyControl(k.fkey);
      if (c && !modsKey.length) { state = state || bound[c.cid] || ""; sel = sel || c.cid === selTrig; }
      // a combo bound to this key with other modifiers: show it faintly
      if (!state && name) {
        const any = Object.keys(bound).some(x => !x.startsWith("mx:") && trigMain(x) === name);
        if (any) state = "inh";
      }
    }
    b.classList.toggle("own", state === "own");
    b.classList.toggle("inh", state === "inh");
    b.classList.toggle("sel", sel);
  }
  $$(".mbtn").forEach(b => {
    const t = b.dataset.trig;
    b.classList.toggle("own", bound[t] === "own");
    b.classList.toggle("inh", bound[t] === "inh");
    b.classList.toggle("sel", t === selTrig);
  });
  // MX keys without an F-key position
  const extra = controls.filter(c => c.divertable && !(c.pos >= 1 && c.pos <= 12));
  $("#mxExtra").hidden = !extra.length;
  $("#mxExtra").innerHTML = extra.length ? "<span>Diğer MX tuşları:</span>" + extra.map(c => `<button data-cid="${c.cid}" class="${bound[c.cid] === "own" ? "own" : ""}">${esc(ctlName(c.cid) || c.cid.slice(3))}</button>`).join("") : "";
  $$("#mxExtra button").forEach(b => b.onclick = () => useTrigger(b.dataset.cid));
  $("#kbAssign").classList.toggle("picking", pickMode);
  const hint = $("#kbHint");
  hint.classList.toggle("picking", pickMode || mods.size > 0);
  hint.innerHTML = pickMode ? "Tetikleyici seç: klavyede ya da farede bir tuşa tıkla. <a href='#' id='pickCancel'>Vazgeç</a>"
    : mods.size ? `Kombinasyon: <b>${[...MODS.filter(m => mods.has(m))].map(keyLabel).join(" + ")} + …</b> şimdi ana tuşa tıkla.`
      : "Bir tuşa tıkla: atanmışsa açılır, değilse yeni makro oluşur. Kombinasyon için önce <b>Ctrl / Alt / Shift / Win</b>’e tıkla.";
  const pc = $("#pickCancel");
  if (pc) pc.onclick = e => { e.preventDefault(); pickMode = false; updateAssignKB(); };
}

let keyMenuEl = null;
function closeKeyMenu() { if (keyMenuEl) { keyMenuEl.remove(); keyMenuEl = null; } }
function keyMenu(e, items) {
  closeKeyMenu();
  const m = document.createElement("div");
  m.className = "menu small";
  m.style.position = "fixed";
  m.innerHTML = items.map((it, i) => `<button data-i="${i}">${it.html}</button>`).join("");
  document.body.appendChild(m);
  const r = m.getBoundingClientRect();
  m.style.left = Math.min(e.clientX, innerWidth - r.width - 8) + "px";
  m.style.top = Math.min(e.clientY + 8, innerHeight - r.height - 8) + "px";
  $$("button", m).forEach(b => b.onclick = ev => { ev.stopPropagation(); closeKeyMenu(); items[+b.dataset.i].fn(); });
  keyMenuEl = m;
}

async function onKeyClick(k, btn, e) {
  if (!k || k.dis) return;
  e.stopPropagation();
  if (k.mod) {
    mods.has(k.mod) ? mods.delete(k.mod) : mods.add(k.mod);
    updateAssignKB();
    return;
  }
  let name = keyName(k);
  if (k.oem && !name) {
    const t = await capture(`Klavyende “${k.label}” tuşuna bas`, "Bu tuşun kodu klavye düzenine göre değişiyor; bir kez basman yeterli, sonra hatırlanır.");
    const main = trigMain(t);
    if (!main || main.startsWith("mx:") || ["mouse4", "mouse5", "middle"].includes(main)) return;
    oemMap[k.id] = main; store.set("oem", oemMap);
    name = main;
  }
  const c = k.fkey && fkeyControl(k.fkey);
  if (c && !mods.size) {
    keyMenu(e, [
      { html: `<b>MX işlevi</b> — ${esc(ctlName(c.cid) || c.cid)}`, fn: () => useTrigger(c.cid) },
      { html: `<b>F${k.fkey}</b> tuşu (Fn ile)`, fn: () => useTrigger(normTrig("f" + k.fkey)) },
    ]);
    return;
  }
  const t = keyTrigger(k);
  mods.clear();
  useTrigger(t);
  updateAssignKB();
}
$$(".mbtn").forEach(b => b.onclick = () => useTrigger(b.dataset.trig));

async function useTrigger(t) {
  t = normTrig(t);
  if (!t) return;
  if (pickMode && draft) {
    snapshot();
    draft.trigger = t; pickMode = false; markDirty(); renderEditor();
    return;
  }
  // existing macro in this profile (or inherited default)?
  const own = cfg.macros.find(m => inProfile(m, profile) && normTrig(m.trigger) === t);
  const inh = !own && profile && cfg.macros.find(m => !m.app && normTrig(m.trigger) === t);
  if (own) return selectMacro(own.id);
  if (inh) {
    const v = await modal({
      title: "Varsayılan profilde atanmış",
      body: `<p class="muted">Bu tuş varsayılan profilde “${esc(inh.name)}” makrosuna bağlı. ${esc(profLabel(profile))} içinde farklı bir şey yapmasını istiyorsan buraya yeni makro ekle.</p>`,
      buttons: [{ label: "Varsayılandakini aç", value: "open" }, { label: "Bu profilde yeni makro", value: "new", primary: true }],
    });
    if (v === "open") return selectMacro(inh.id);
    if (v !== "new") return;
  }
  if (!(await leaveDraft())) return;
  newMacro("blank", t);
}

// ---------- macro list ----------
const modeName = { press: "bir kez", hold: "basılı tut", toggle: "aç/kapa" };

function macroItem(m, running, inh) {
  const conflict = m.enabled && cfg.macros.some(o => o.id !== m.id && o.enabled && ieq(o.app, m.app) && normTrig(o.trigger) && normTrig(o.trigger) === normTrig(m.trigger));
  return `
    <div class="mitem ${m.id === selId ? "sel" : ""} ${m.enabled ? "" : "off"} ${inh ? "inh" : ""}" data-id="${esc(m.id)}">
      <div class="n">${esc(m.name || "Adsız")}</div>
      <label class="switch" title="Etkin"><input type="checkbox" data-en="${esc(m.id)}" ${m.enabled ? "checked" : ""}><span></span></label>
      <div class="t">${triggerHTML(m.trigger)}
        <span class="chip mode-${m.mode}">${modeName[m.mode] || m.mode}</span>
        ${running.has(m.id) ? '<span class="chip run">çalışıyor</span>' : ""}
        ${m === draft && dirty ? `<span class="chip">${cfg.macros.some(x => x.id === m.id) ? "değişti" : "yeni"}</span>` : ""}
        ${conflict ? '<span class="chip warn">çakışma</span>' : ""}
      </div>
    </div>`;
}

function renderMacroList() {
  if (!cfg) return;
  const list = $("#macroList");
  const running = new Set(st ? st.running : []);
  const q = $("#mSearch").value.trim().toLowerCase();
  const match = m => !q || (m.name || "").toLowerCase().includes(q) || normTrig(m.trigger).includes(q) || (m.actions || []).some(a => JSON.stringify(a).toLowerCase().includes(q));
  const own = cfg.macros.filter(m => inProfile(m, profile) && match(m)).map(m => (draft && draft.id === m.id ? draft : m));
  const isNew = draft && !cfg.macros.some(m => m.id === draft.id);
  if (isNew && inProfile(draft, profile)) own.unshift(draft);
  let html = "";
  if (profile) html += `<div class="mgroup">${esc(profLabel(profile).toUpperCase())}</div>`;
  html += own.map(m => macroItem(m, running)).join("") || `<div class="mlist-empty">${q ? "Eşleşen makro yok." : profile ? "Bu profilde makro yok.<br>Klavyede bir tuşa tıkla." : "Henüz makro yok.<br>Klavyede bir tuşa tıkla ya da + ile başla."}</div>`;
  if (profile) {
    const ownTrig = new Set(cfg.macros.filter(m => inProfile(m, profile) && m.enabled).map(m => normTrig(m.trigger)));
    const inh = cfg.macros.filter(m => !m.app && match(m) && !ownTrig.has(normTrig(m.trigger)));
    if (inh.length) html += `<div class="mgroup">VARSAYILANDAN GELENLER</div>` + inh.map(m => macroItem(m, running, true)).join("");
  }
  list.innerHTML = html;
  $$(".mitem", list).forEach(el => el.onclick = e => { if (!e.target.closest(".switch")) selectMacro(el.dataset.id); });
  $$("input[data-en]", list).forEach(el => el.onchange = async () => {
    const m = cfg.macros.find(x => x.id === el.dataset.en);
    if (el.checked && !normTrig(m.trigger)) { el.checked = false; toast("Önce bir tetikleyici seç", true); return; }
    m.enabled = el.checked;
    if (draft && draft.id === m.id) { draft.enabled = el.checked; $("#mEnabled").checked = el.checked; }
    await saveCfg(el.checked ? "Etkinleştirildi" : "Devre dışı");
    renderMacroList(); updateAssignKB();
  });
}
$("#mSearch").oninput = renderMacroList;

async function leaveDraft() {
  if (!dirty) return true;
  const v = await modal({
    title: "Kaydedilmemiş değişiklik",
    body: `<p class="muted">“${esc(draft.name || "Adsız")}” makrosunda kaydedilmemiş değişiklikler var.</p>`,
    buttons: [{ label: "Vazgeç", value: null }, { label: "Kaydetme", value: "drop", danger: true }, { label: "Kaydet", value: "save", primary: true }],
  });
  if (!v) return false;
  if (v === "save") return saveMacro();
  dirty = false;
  if (!cfg.macros.some(m => m.id === draft.id)) { draft = null; selId = null; renderEditor(); renderMacroList(); }
  return true;
}

async function selectMacro(id) {
  if (id === selId && draft) return;
  if (!(await leaveDraft())) return;
  selId = id;
  const m = cfg.macros.find(x => x.id === id);
  draft = m ? clone(m) : null;
  dirty = false; history = []; selAct = -1; pickMode = false; expandPaths = false;
  renderEditor(); renderMacroList(); updateAssignKB();
}

// ---------- editor ----------
function markDirty() {
  const was = dirty;
  dirty = true;
  if (!was) renderMacroList();
  $("#saveMacro").classList.add("dirty");
  $("#saveMacro").textContent = "Kaydet •";
  updateConflict();
}
function snapshot() {
  if (!draft) return;
  history.push(clone(draft));
  if (history.length > 60) history.shift();
  $("#undoBtn").disabled = false;
}
function undo() {
  if (!history.length) return;
  draft = history.pop();
  markDirty(); renderEditor();
}

function updateConflict() {
  if (!draft) return;
  const t = normTrig(draft.trigger);
  const other = t && cfg.macros.find(o => o.id !== draft.id && o.enabled && ieq(o.app, draft.app) && normTrig(o.trigger) === t);
  const c = $("#conflict");
  c.hidden = !other;
  if (other) c.textContent = `⚠ “${other.name}” de aynı tetikleyiciyi kullanıyor; ikisinden yalnızca biri çalışır.`;
}

function renderAppSelect() {
  const s = $("#mApp");
  const ps = profiles();
  if (draft.app && !ps.some(p => ieq(p, draft.app))) ps.push(draft.app);
  s.innerHTML = ps.map(p => `<option value="${esc(p)}" ${ieq(p, draft.app) ? "selected" : ""}>${esc(p ? profLabel(p) + " (" + p + ")" : "Varsayılan — tüm programlar")}</option>`).join("") + `<option value="__pick">Başka program…</option>`;
}

function renderEditor() {
  $("#editorEmpty").hidden = !!draft;
  $("#editorBody").hidden = !draft;
  updateAssignKB();
  if (!draft) return;
  $("#saveMacro").textContent = dirty ? "Kaydet •" : "Kaydet";
  $("#saveMacro").classList.toggle("dirty", dirty);
  $("#undoBtn").disabled = !history.length;
  $("#mName").value = draft.name || "";
  $("#mEnabled").checked = !!draft.enabled;
  $("#mTrigger").value = draft.trigger || "";
  $("#mTriggerView").innerHTML = triggerHTML(draft.trigger);
  $("#pickKeyBtn").classList.toggle("active", pickMode);
  renderAppSelect();
  seg($("#modeSeg"), draft.mode || "press", v => { snapshot(); draft.mode = v; markDirty(); renderEditor(); });
  $("#mInterval").value = draft.interval_ms || 50;
  $("#mRepeat").value = draft.repeat || 1;
  $("#fInterval").hidden = draft.mode === "press" && (draft.repeat || 1) <= 1;
  $("#fRepeat").hidden = draft.mode !== "press";
  $("#mPass").checked = !!draft.passthrough;
  updateRate(); updateConflict();
  renderActions();
}

function seqStats() {
  let ms = 0;
  for (const a of draft.actions || []) {
    if (a.type === "delay") ms += a.ms_max > a.ms ? (a.ms + a.ms_max) / 2 : a.ms || 0;
    ms += (a.hold_ms || 0) * (a.count || 1);
  }
  return ms;
}
function fmtMs(ms) { return ms >= 1000 ? (ms / 1000).toFixed(ms >= 10000 ? 0 : 1) + " sn" : Math.round(ms) + " ms"; }

function updateRate() {
  if (!draft) return;
  const iv = +$("#mInterval").value || 1;
  const per = 1000 / Math.max(1, iv + seqStats());
  $("#rateHint").textContent = draft.mode === "press" ? "" : `≈ ${per >= 10 ? Math.round(per) : per.toFixed(1)} tekrar/sn`;
  const n = (draft.actions || []).length;
  $("#actCount").textContent = n ? `(${n})` : "";
  const d = seqStats();
  $("#actDur").textContent = d ? "≈ " + fmtMs(d) + " sürer" : "";
}

$("#mName").oninput = e => { draft.name = e.target.value; markDirty(); const n = $(`.mitem[data-id="${draft.id}"] .n`); if (n) n.textContent = draft.name || "Adsız"; };
$("#mEnabled").onchange = e => { snapshot(); draft.enabled = e.target.checked; markDirty(); };
$("#mTrigger").onchange = e => { snapshot(); draft.trigger = normTrig(e.target.value) || e.target.value.trim(); markDirty(); renderEditor(); };
$("#mInterval").oninput = e => { draft.interval_ms = +e.target.value; updateRate(); markDirty(); };
$("#mRepeat").oninput = e => { draft.repeat = +e.target.value; $("#fInterval").hidden = (+e.target.value || 1) <= 1; markDirty(); };
$("#mPass").onchange = e => { snapshot(); draft.passthrough = e.target.checked; markDirty(); };
$("#mApp").onchange = async e => {
  let v = e.target.value;
  if (v === "__pick") {
    v = await pickApp("Bu makro hangi programda çalışsın?");
    if (!v) { renderAppSelect(); return; }
    if (!profiles().some(p => ieq(p, v))) extraProfiles.push(v);
  }
  snapshot(); draft.app = v; markDirty(); renderAppSelect();
};
$("#captureBtn").onclick = async () => {
  const t = await capture("Tetikleyici tuşa bas");
  if (!t) return;
  snapshot(); draft.trigger = t; markDirty(); renderEditor();
};
$("#pickKeyBtn").onclick = () => { pickMode = !pickMode; renderEditor(); if (pickMode) $(".kb-card").scrollIntoView({ behavior: "smooth", block: "nearest" }); };
$("#undoBtn").onclick = undo;
// one undo step per focused text field edit
$("#editorBody").addEventListener("focusin", e => { if (e.target.matches("input:not([type=checkbox]), textarea")) snapshot(); });

document.addEventListener("keydown", e => {
  if (!$("#modal").hidden || !$("#overlay").hidden) return;
  const inField = e.target.matches("input, textarea, select");
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s" && draft && $("#tab-macros").classList.contains("active")) { e.preventDefault(); saveMacro(); }
  else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "z" && !inField && draft) { e.preventDefault(); undo(); }
  else if (e.key === "Escape") { if (pickMode) { pickMode = false; renderEditor(); } else if (mods.size) { mods.clear(); updateAssignKB(); } closeKeyMenu(); }
  else if ((e.key === "Delete") && !inField && draft && selAct >= 0 && draft.actions[selAct]) { snapshot(); draft.actions.splice(selAct, 1); selAct = Math.min(selAct, draft.actions.length - 1); markDirty(); renderActions(); updateRate(); }
});

// ----- actions -----
const TYPES = {
  keys: { l: "Tuşa bas", ic: "i-kb", c: "k" }, keydown: { l: "Tuşu basılı tut", ic: "i-down", c: "k" }, keyup: { l: "Tuşu bırak", ic: "i-up", c: "k" },
  text: { l: "Metin yaz", ic: "i-text", c: "k" },
  click: { l: "Tıkla", ic: "i-mouse", c: "m" }, mousedown: { l: "Fare tuşu bas", ic: "i-down", c: "m" }, mouseup: { l: "Fare tuşu bırak", ic: "i-up", c: "m" },
  move: { l: "Fareyi taşı", ic: "i-move", c: "m" }, scroll: { l: "Tekerlek", ic: "i-scroll", c: "m" },
  delay: { l: "Bekle", ic: "i-clock", c: "t" },
  run: { l: "Program aç", ic: "i-app", c: "s" }, url: { l: "Web adresi aç", ic: "i-link", c: "s" }, shell: { l: "Komut çalıştır", ic: "i-term", c: "s" },
  http: { l: "HTTP isteği", ic: "i-link", c: "s" }, backlight: { l: "Arka ışık", ic: "i-sun", c: "s" }, gamemode: { l: "Oyun modu", ic: "i-game", c: "s" },
  repair: { l: "Klavyeyi onar", ic: "i-wrench", c: "s" },
};
const PALETTE = [
  ["KLAVYE", [["keys", "Tuş / kısayol"], ["text", "Metin yaz"], ["media", "Medya tuşu"], ["keydown", "Basılı tut"], ["keyup", "Bırak"]]],
  ["FARE", [["click", "Tıkla"], ["dblclick", "Çift tıkla"], ["move", "Fareyi taşı"], ["scroll", "Tekerlek"], ["mousedown", "Fare bas"], ["mouseup", "Fare bırak"]]],
  ["ZAMANLAMA", [["delay", "Bekle"], ["rdelay", "Rastgele bekle"]]],
  ["SİSTEM", [["run", "Program aç"], ["url", "Web adresi"], ["shell", "Komut"], ["http", "HTTP isteği"], ["backlight", "Arka ışık"], ["gamemode", "Oyun modu"], ["repair", "Klavyeyi onar"]]],
];
const PAL_IC = { media: "i-play", dblclick: "i-mouse", rdelay: "i-dice" };
const NEW_ACT = {
  keys: () => ({ type: "keys", keys: "", hold_ms: 0 }), keydown: () => ({ type: "keydown", keys: "" }), keyup: () => ({ type: "keyup", keys: "" }),
  text: () => ({ type: "text", text: "" }), media: () => ({ type: "keys", keys: "playpause" }),
  click: () => ({ type: "click", button: "left", hold_ms: 8 }), dblclick: () => ({ type: "click", button: "left", count: 2 }),
  mousedown: () => ({ type: "mousedown", button: "left" }), mouseup: () => ({ type: "mouseup", button: "left" }),
  move: () => ({ type: "move", x: 0, y: 0 }), scroll: () => ({ type: "scroll", amount: -3 }),
  delay: () => ({ type: "delay", ms: 100 }), rdelay: () => ({ type: "delay", ms: 80, ms_max: 160 }),
  run: () => ({ type: "run", cmd: "" }), url: () => ({ type: "url", url: "https://" }), shell: () => ({ type: "shell", cmd: "" }),
  http: () => ({ type: "http", method: "POST", url: "https://" }), backlight: () => ({ type: "backlight", value: "toggle" }),
  gamemode: () => ({ type: "gamemode", value: "toggle" }), repair: () => ({ type: "repair" }),
};
const PRESET_KEYS = [["", "hazır…"], ["playpause", "Oynat / duraklat"], ["next", "Sonraki parça"], ["prev", "Önceki parça"], ["mute", "Sesi kapat"], ["volup", "Ses +"], ["voldown", "Ses −"],
  ["ctrl+c", "Kopyala"], ["ctrl+v", "Yapıştır"], ["ctrl+x", "Kes"], ["ctrl+z", "Geri al"], ["ctrl+shift+esc", "Görev yöneticisi"], ["win+d", "Masaüstünü göster"], ["win+l", "Kilitle"],
  ["win+shift+s", "Ekran alıntısı"], ["alt+tab", "Pencere değiştir"], ["alt+f4", "Pencereyi kapat"], ["browserback", "Tarayıcı geri"], ["browserfwd", "Tarayıcı ileri"], ["calc", "Hesap makinesi"]];

$("#palette").innerHTML = PALETTE.map(([g, items]) => `<div><div class="pal-group-title">${g}</div><div class="pal-items">${items.map(([t, l]) => `<button data-add="${t}">${icon(PAL_IC[t] || TYPES[t].ic, "sm")}${l}</button>`).join("")}</div></div>`).join("");
$$("#palette [data-add]").forEach(b => b.onclick = () => { $("#palette").hidden = true; addAction(NEW_ACT[b.dataset.add]()); });
$("#palette").onclick = e => e.stopPropagation();
$("#addActBtn").onclick = e => {
  e.stopPropagation(); closeMenus("#palette");
  const p = $("#palette");
  p.hidden = !p.hidden;
  if (!p.hidden) {
    // open upwards when there is no room below
    const r = $("#addActBtn").getBoundingClientRect();
    p.style.top = r.bottom + p.offsetHeight + 12 > innerHeight ? "auto" : "";
    p.style.bottom = p.style.top === "auto" ? "calc(100% + 6px)" : "";
  }
};

function addAction(a) {
  snapshot();
  const at = selAct >= 0 && selAct < draft.actions.length ? selAct + 1 : draft.actions.length;
  draft.actions.splice(at, 0, a);
  selAct = at;
  markDirty(); renderActions(); updateRate();
  const row = $(`.act[data-i="${at}"]`);
  if (row) { row.scrollIntoView({ block: "nearest" }); const f = $("input:not([type=checkbox]), textarea", row); if (f) f.focus(); }
}

const btnOpts = v => [["left", "Sol"], ["right", "Sağ"], ["middle", "Orta"], ["mouse4", "Yan 4 (geri)"], ["mouse5", "Yan 5 (ileri)"]].map(([b, l]) => `<option value="${b}" ${b === (v || "left") ? "selected" : ""}>${l}</option>`).join("");

function fieldsHTML(a, i) {
  const inp = (k, ph, cls = "w-m", type = "text") => `<input data-i="${i}" data-k="${k}" type="${type}" class="${cls} ${type === "text" ? "mono" : ""}" placeholder="${ph}" value="${esc(a[k] ?? "")}">`;
  const chk = (k, l) => `<label class="check small"><input type="checkbox" data-i="${i}" data-k="${k}" ${a[k] ? "checked" : ""}> ${l}</label>`;
  const sel = (k, opts, def) => `<select data-i="${i}" data-k="${k}">${opts.map(([v, l]) => `<option value="${v}" ${String(a[k] ?? def) === v ? "selected" : ""}>${l}</option>`).join("")}</select>`;
  switch (a.type) {
    case "click": return `<select data-i="${i}" data-k="button">${btnOpts(a.button)}</select> ${sel("count", [["1", "tek"], ["2", "çift"], ["3", "üçlü"]], "1")} basılı ${inp("hold_ms", "0", "w-s", "number")} ms`;
    case "mousedown": case "mouseup": return `<select data-i="${i}" data-k="button">${btnOpts(a.button)}</select>`;
    case "keys": return `${inp("keys", "ctrl+shift+esc")} <button class="btn sm" data-cap="${i}">Yakala</button> <select data-preset="${i}" class="w-s">${PRESET_KEYS.map(([v, l]) => `<option value="${v}">${l}</option>`).join("")}</select> basılı ${inp("hold_ms", "0", "w-s", "number")} ms`;
    case "keydown": case "keyup": return `${inp("keys", "space")} <button class="btn sm" data-cap="${i}">Yakala</button>`;
    case "delay": return a.ms_max ? `${inp("ms", "80", "w-s", "number")} – ${inp("ms_max", "160", "w-s", "number")} ms arası rastgele` : `${inp("ms", "100", "w-s", "number")} ms <button class="btn sm ghost" data-rand="${i}" title="Aralıktan rastgele bekle">${icon("i-dice", "sm")}rastgele</button>`;
    case "move": return `X ${inp("x", "0", "w-s", "number")} Y ${inp("y", "0", "w-s", "number")} ${chk("rel", "göreli")} <button class="btn sm" data-pos="${i}" title="3 saniye içinde imleci hedefe götür">Konumu al</button>`;
    case "scroll": return `${inp("amount", "-3", "w-s", "number")} çentik ${sel("horiz", [["false", "dikey"], ["true", "yatay"]], "false")} <span class="small">(+ yukarı/sağ, − aşağı/sol)</span>`;
    case "text": return `<textarea data-i="${i}" data-k="text" placeholder="Yazılacak metin">${esc(a.text || "")}</textarea>`;
    case "run": return `${inp("cmd", "notepad.exe veya C:\\yol\\oyun.exe", "grow")} ${inp("args", "argümanlar")}`;
    case "url": return inp("url", "https://…", "grow");
    case "shell": return inp("cmd", "ör. shutdown /a", "grow");
    case "http": return `${sel("method", ["GET", "POST", "PUT", "DELETE"].map(m => [m, m]), "GET")} ${inp("url", "https://…", "grow")} ${inp("body", "gövde (isteğe bağlı)")}`;
    case "backlight": return sel("value", [["toggle", "Aç/kapa"], ["up", "Artır"], ["down", "Azalt"], ["auto", "Otomatik"], ["off", "Kapat"], ...[0, 1, 2, 3, 4, 5, 6, 7].map(n => [String(n), "Seviye " + n])], "toggle");
    case "gamemode": return sel("value", [["toggle", "Aç/kapa"], ["on", "Aç"], ["off", "Kapat"]], "toggle");
    case "repair": return '<span class="small">Bluetooth takılma onarımını başlatır</span>';
    default: return "";
  }
}

// consecutive move/delay runs longer than this are shown as one "mouse path" row
const PATH_MIN = 6;
function segments(acts) {
  const out = [];
  for (let i = 0; i < acts.length;) {
    if (!expandPaths && acts[i].type === "move") {
      let j = i, moves = 0;
      while (j < acts.length && (acts[j].type === "move" || (acts[j].type === "delay" && j + 1 < acts.length && acts[j + 1].type === "move"))) { if (acts[j].type === "move") moves++; j++; }
      if (moves >= PATH_MIN) { out.push({ path: true, from: i, to: j, moves }); i = j; continue; }
    }
    out.push({ i }); i++;
  }
  return out;
}

function renderActions() {
  const box = $("#actions");
  const acts = draft.actions || (draft.actions = []);
  if (!acts.length) {
    box.innerHTML = '<div class="actions-empty">Henüz eylem yok. Aşağıdan bir eylem seç ya da <b>● Kayıt al</b> ile yaptıklarını kaydet.</div>';
    updateRate(); return;
  }
  const hasPaths = acts.filter(a => a.type === "move").length >= PATH_MIN;
  box.innerHTML = segments(acts).map(s => {
    if (s.path) {
      const ms = acts.slice(s.from, s.to).reduce((x, a) => x + (a.type === "delay" ? a.ms || 0 : 0), 0);
      return `<div class="act thin" data-i="${s.from}">
        <span class="grip"></span><div class="idx">${s.from + 1}</div><div class="aic m">${icon("i-move", "sm")}</div><div class="lbl">Fare yolu</div>
        <div class="fields">${s.moves} nokta · ${fmtMs(ms)} <button class="btn sm ghost" data-expand="1">Aç</button></div>
        <div class="tools"><button class="del" data-delpath="${s.from}:${s.to}" title="Sil">${icon("i-trash", "sm")}</button></div></div>`;
    }
    const i = s.i, a = acts[i], t = TYPES[a.type] || { l: a.type, ic: "i-bolt", c: "" };
    const thin = a.type === "delay" || a.type === "keyup" || a.type === "mouseup";
    return `
    <div class="act ${thin ? "thin" : ""} ${i === selAct ? "selrow" : ""}" data-i="${i}">
      <span class="grip" title="Sürükle">${icon("i-grip", "sm")}</span>
      <div class="idx">${i + 1}</div>
      <div class="aic ${t.c}">${icon(t.ic, "sm")}</div>
      <div class="lbl">${t.l}</div>
      <div class="fields">${fieldsHTML(a, i)}</div>
      <div class="tools">
        <button data-dup="${i}" title="Çoğalt">${icon("i-copy", "sm")}</button>
        <button class="del" data-del="${i}" title="Sil (Del)">${icon("i-trash", "sm")}</button>
      </div>
    </div>`;
  }).join("") + (hasPaths && expandPaths ? `<button class="btn sm ghost" data-expand="0">Fare yollarını daralt</button>` : "");

  $$(".act", box).forEach(row => row.addEventListener("mousedown", e => {
    if (e.target.closest(".tools")) return;
    const i = +row.dataset.i;
    if (selAct !== i) { selAct = i; $$(".act", box).forEach(r => r.classList.toggle("selrow", +r.dataset.i === i)); }
  }));
  $$("[data-k]", box).forEach(el => {
    const ev = el.tagName === "SELECT" || el.type === "checkbox" ? "onchange" : "oninput";
    el[ev] = () => {
      const a = acts[+el.dataset.i], k = el.dataset.k;
      if (el.type === "checkbox") { snapshot(); a[k] = el.checked; }
      else if (k === "horiz") a[k] = el.value === "true";
      else if (k === "count") a[k] = +el.value > 1 ? +el.value : undefined;
      else a[k] = el.type === "number" ? +el.value : el.value;
      if (el.tagName === "SELECT" && k !== "horiz" && k !== "count") snapshot();
      markDirty(); updateRate();
    };
  });
  $$("[data-dup]", box).forEach(b => b.onclick = () => { snapshot(); const i = +b.dataset.dup; acts.splice(i + 1, 0, clone(acts[i])); selAct = i + 1; markDirty(); renderActions(); });
  $$("[data-del]", box).forEach(b => b.onclick = () => { snapshot(); acts.splice(+b.dataset.del, 1); selAct = -1; markDirty(); renderActions(); });
  $$("[data-delpath]", box).forEach(b => b.onclick = () => { const [f, t] = b.dataset.delpath.split(":").map(Number); snapshot(); acts.splice(f, t - f); markDirty(); renderActions(); });
  $$("[data-expand]", box).forEach(b => b.onclick = () => { expandPaths = b.dataset.expand === "1"; renderActions(); });
  $$("[data-rand]", box).forEach(b => b.onclick = () => { snapshot(); const a = acts[+b.dataset.rand]; a.ms_max = Math.round((a.ms || 100) * 1.6); markDirty(); renderActions(); });
  $$("[data-preset]", box).forEach(s => s.onchange = () => { if (!s.value) return; snapshot(); acts[+s.dataset.preset].keys = s.value; markDirty(); renderActions(); });
  $$("[data-cap]", box).forEach(b => b.onclick = async () => {
    const t = await capture("Gönderilecek tuşa bas", "Makronun basacağı tuş ya da kombinasyon.");
    if (!t || t.startsWith("mx:") || ["mouse4", "mouse5", "middle"].includes(t)) return;
    snapshot(); acts[+b.dataset.cap].keys = t; markDirty(); renderActions();
  });
  $$("[data-pos]", box).forEach(b => b.onclick = async () => {
    b.textContent = "3…";
    let n = 3; const tm = setInterval(() => { n--; b.textContent = n > 0 ? n + "…" : "…"; }, 1000);
    try {
      const r = await api("GET", "/api/cursor?delay=3");
      snapshot(); const a = acts[+b.dataset.pos]; a.x = r.x; a.y = r.y; a.rel = false; markDirty(); renderActions();
      toast(`Konum: ${r.x}, ${r.y}`);
    } finally { clearInterval(tm); }
  });
  // drag & drop reorder via the grip
  let from = -1;
  $$(".act", box).forEach(row => {
    const grip = $(".grip", row);
    if (grip) grip.onmousedown = () => { row.draggable = true; };
    row.addEventListener("dragstart", e => { from = +row.dataset.i; row.classList.add("dragging"); e.dataTransfer.effectAllowed = "move"; });
    row.addEventListener("dragend", () => { row.draggable = false; row.classList.remove("dragging"); $$(".act", box).forEach(r => r.classList.remove("drop-before", "drop-after")); });
    row.addEventListener("dragover", e => {
      if (from < 0) return;
      e.preventDefault();
      const r = row.getBoundingClientRect(), after = e.clientY > r.top + r.height / 2;
      $$(".act", box).forEach(x => x.classList.remove("drop-before", "drop-after"));
      row.classList.add(after ? "drop-after" : "drop-before");
    });
    row.addEventListener("drop", e => {
      e.preventDefault();
      const r = row.getBoundingClientRect(), after = e.clientY > r.top + r.height / 2;
      let to = +row.dataset.i + (after ? 1 : 0);
      if (from < 0 || to === from || to === from + 1) return;
      snapshot();
      const [m] = acts.splice(from, 1);
      if (to > from) to--;
      acts.splice(to, 0, m);
      selAct = to; from = -1;
      markDirty(); renderActions();
    });
  });
  updateRate();
}

$("#clearActions").onclick = async () => {
  if (!draft.actions.length || !(await ask("Eylemleri temizle", `${draft.actions.length} eylemin hepsi silinsin mi? (Geri al ile dönebilirsin.)`, "Temizle", true))) return;
  snapshot(); draft.actions = []; selAct = -1; markDirty(); renderActions();
};

// delay tools
$("#delayToolBtn").onclick = e => { e.stopPropagation(); closeMenus("#delayMenu"); $("#delayMenu").hidden = !$("#delayMenu").hidden; };
$$("#delayMenu button").forEach(b => b.onclick = async () => {
  $("#delayMenu").hidden = true;
  const ds = draft.actions.filter(a => a.type === "delay");
  const op = b.dataset.d;
  if (op === "moves") { snapshot(); draft.actions = draft.actions.filter((a, i, arr) => a.type !== "move" && !(a.type === "delay" && arr[i + 1] && arr[i + 1].type === "move")); }
  else if (!ds.length) { toast("Hiç bekleme yok", true); return; }
  else if (op === "remove") { snapshot(); draft.actions = draft.actions.filter(a => a.type !== "delay"); }
  else if (op === "set") { const v = await promptNum("Beklemeleri ayarla", "Tüm beklemeler (ms)", 50); if (v == null) return; snapshot(); ds.forEach(a => { a.ms = Math.max(0, Math.round(v)); delete a.ms_max; }); }
  else if (op === "scale") { const v = await promptNum("Hız", "Çarpan: 0.5 = iki kat hızlı, 2 = iki kat yavaş", 0.5); if (!v || v <= 0) return; snapshot(); ds.forEach(a => { a.ms = Math.round(a.ms * v); if (a.ms_max) a.ms_max = Math.round(a.ms_max * v); }); }
  else if (op === "jitter") { const v = await promptNum("Rastgelelik", "± yüzde (ör. 20)", 20); if (v == null) return; snapshot(); const p = Math.min(95, Math.abs(v)) / 100; ds.forEach(a => { const base = a.ms_max ? (a.ms + a.ms_max) / 2 : a.ms; a.ms = Math.round(base * (1 - p)); a.ms_max = Math.round(base * (1 + p)); if (a.ms_max <= a.ms) delete a.ms_max; }); }
  selAct = -1; markDirty(); renderActions();
});

// ----- recording -----
let recTimer = null;
$("#recOptBtn").onclick = e => { e.stopPropagation(); closeMenus("#recMenu"); $("#recMenu").hidden = !$("#recMenu").hidden; };
$("#recMenu").onclick = e => e.stopPropagation();
["recMouse", "recMoves", "recDelays"].forEach(id => { const v = store.get(id, null); if (v !== null) $("#" + id).checked = v; $("#" + id).onchange = e => store.set(id, e.target.checked); });
async function startRecording() {
  await api("POST", "/api/record/start", { mouse: $("#recMouse").checked, moves: $("#recMoves").checked });
  const t0 = Date.now();
  $("#recbar").hidden = false;
  recTimer = setInterval(() => $("#recTime").textContent = ((Date.now() - t0) / 1000).toFixed(1) + " sn", 100);
}
$("#recBtn").onclick = startRecording;
$("#recStop").onclick = async () => {
  clearInterval(recTimer);
  $("#recbar").hidden = true;
  const r = await api("POST", "/api/record/stop", { delays: $("#recDelays").checked });
  const acts = r.actions || [];
  if (!acts.length) { toast("Hiçbir şey kaydedilmedi", true); return; }
  let mode = "append";
  if (draft.actions.length) {
    mode = await modal({
      title: `${acts.length} eylem kaydedildi`, body: '<p class="muted">Mevcut eylemlerle ne yapalım?</p>',
      buttons: [{ label: "Vazgeç", value: null }, { label: "Üzerine yaz", value: "replace" }, { label: "Sona ekle", value: "append", primary: true }],
    });
    if (!mode) return;
  }
  snapshot();
  if (mode === "replace") draft.actions = [];
  draft.actions.push(...acts);
  selAct = -1; markDirty(); renderActions();
  toast(`${acts.length} eylem eklendi`);
};

// ----- new / save / delete -----
const TEMPLATES = {
  blank: () => ({ name: "", mode: "press", actions: [] }),
  record: () => ({ name: "Kayıtlı makro", mode: "press", actions: [] }),
  turboClick: () => ({ name: "Turbo tık", mode: "hold", interval_ms: 30, actions: [{ type: "click", button: "left", hold_ms: 8 }] }),
  turboKey: () => ({ name: "Turbo tuş", mode: "hold", interval_ms: 30, actions: [{ type: "keys", keys: "space", hold_ms: 8 }] }),
  shortcut: () => ({ name: "Kısayol", mode: "press", actions: [{ type: "keys", keys: "" }] }),
  text: () => ({ name: "Metin", mode: "press", actions: [{ type: "text", text: "" }] }),
  run: () => ({ name: "Program aç", mode: "press", actions: [{ type: "run", cmd: "" }] }),
  media: () => ({ name: "Oynat / duraklat", mode: "press", actions: [{ type: "keys", keys: "playpause" }] }),
};
function autoName(t) {
  t = normTrig(t);
  if (!t) return "Yeni makro";
  if (t.startsWith("mx:")) return (ctlName(t) || "MX tuşu") + " makrosu";
  return t.split("+").map(keyLabel).join("+") + " makrosu";
}
function newMacro(kind, trigger = "") {
  const tpl = TEMPLATES[kind]();
  const m = { id: uid(), enabled: true, app: profile, passthrough: false, repeat: 1, trigger, ...tpl };
  if (!m.name) m.name = autoName(trigger);
  // not added to cfg.macros until saved; the list shows the draft as "yeni"
  selId = m.id; draft = m; dirty = true; history = []; selAct = -1; expandPaths = false;
  pickMode = !trigger;
  renderEditor(); markDirty(); renderMacroList();
  $("#editor").scrollIntoView({ behavior: "smooth", block: "start" });
  if (!trigger) toast("Şimdi klavyede tetikleyici tuşa tıkla (ya da Yakala)");
  if (kind === "record") startRecording();
}
$("#newMacroBtn").onclick = e => { e.stopPropagation(); closeMenus("#newMenu"); $("#newMenu").hidden = !$("#newMenu").hidden; };
$$("#newMenu button").forEach(b => b.onclick = async () => {
  $("#newMenu").hidden = true;
  if (!(await leaveDraft())) return;
  newMacro(b.dataset.t);
});

async function saveMacro() {
  if (!draft) return false;
  if (draft.enabled && !normTrig(draft.trigger)) {
    toast("Önce bir tetikleyici seç: klavyede bir tuşa tıkla ya da Yakala", true);
    pickMode = true; renderEditor(); return false;
  }
  if (!draft.name.trim()) draft.name = autoName(draft.trigger);
  draft.trigger = normTrig(draft.trigger) || draft.trigger;
  const i = cfg.macros.findIndex(x => x.id === draft.id);
  const backup = clone(cfg.macros);
  if (i >= 0) cfg.macros[i] = clone(draft); else cfg.macros.push(clone(draft));
  if (await saveCfg("Makro kaydedildi")) {
    dirty = false; extraProfiles = extraProfiles.filter(p => !ieq(p, draft.app));
    renderEditor(); renderMacroList();
    return true;
  }
  cfg.macros = backup;
  renderMacroList();
  return false;
}
$("#saveMacro").onclick = saveMacro;
$("#delMacro").onclick = async () => {
  if (!(await ask("Makroyu sil", `“${esc(draft.name)}” silinsin mi?`, "Sil", true))) return;
  cfg.macros = cfg.macros.filter(x => x.id !== draft.id);
  draft = null; selId = null; dirty = false;
  await saveCfg("Silindi");
  renderMacroList(); renderEditor();
};
$("#dupMacro").onclick = async () => {
  if (!(await leaveDraft())) return;
  const m = { ...clone(draft), id: uid(), name: draft.name + " (kopya)", enabled: false };
  selId = m.id; draft = m; dirty = true; history = [];
  renderEditor(); markDirty(); renderMacroList();
  toast("Kopya devre dışı olarak oluşturuldu; tetikleyicisini değiştirip kaydet");
};
$("#testMacro").onclick = async () => {
  await api("POST", "/api/macro/test", draft);
  toast(draft.mode === "press" ? "2 sn sonra bir kez çalışacak — hedef pencereye geç" : "2 sn sonra 2 sn boyunca tekrarlayacak");
};
const stopAll = async () => { await api("POST", "/api/macro/stopall"); toast("Durduruldu"); };
$("#stopAllTop").onclick = stopAll;

// ----- import / export -----
$("#exportBtn").onclick = () => {
  const list = profile ? cfg.macros.filter(m => inProfile(m, profile)) : cfg.macros;
  if (!list.length) { toast("Dışa aktarılacak makro yok", true); return; }
  const blob = new Blob([JSON.stringify({ type: "mxhelper-macros", version: 1, macros: list }, null, 2)], { type: "application/json" });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = `mxhelper-makrolar${profile ? "-" + profLabel(profile) : ""}.json`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 2000);
  toast(`${list.length} makro dışa aktarıldı`);
};
$("#importBtn").onclick = () => $("#importFile").click();
$("#importFile").onchange = async e => {
  const f = e.target.files[0];
  e.target.value = "";
  if (!f) return;
  let list;
  try {
    const j = JSON.parse(await f.text());
    list = Array.isArray(j) ? j : j.macros;
    if (!Array.isArray(list)) throw new Error("makro listesi bulunamadı");
  } catch (err) { toast("Dosya okunamadı: " + err.message, true); return; }
  list = list.filter(m => m && Array.isArray(m.actions)).map(m => ({ mode: "press", ...m, id: uid() }));
  if (!list.length) { toast("Dosyada makro yok", true); return; }
  let clash = 0;
  for (const m of list) {
    if (m.enabled && cfg.macros.some(o => o.enabled && ieq(o.app, m.app) && normTrig(o.trigger) === normTrig(m.trigger))) { m.enabled = false; clash++; }
  }
  cfg.macros.push(...list);
  if (await saveCfg(`${list.length} makro içe aktarıldı${clash ? ` (${clash} tanesi tetikleyici çakıştığı için kapalı)` : ""}`)) renderMacroList();
};

// ---------- lighting ----------
function renderLightState() {
  const bl = st.backlight;
  const has = bl.known;
  litKB($("#kbLight"));
  $$("#blSeg button").forEach(b => (b.disabled = !has));
  const lv = bl.levels || 8;
  const pick = $("#blPicker");
  if (pick.children.length !== lv) {
    pick.innerHTML = Array.from({ length: lv }, (_, i) => `<button data-l="${i}" title="Seviye ${i}" style="height:${18 + i * 82 / (lv - 1)}%"></button>`).join("");
    $$("button", pick).forEach(b => b.onclick = () => pickLevel(+b.dataset.l));
  }
  pick.classList.toggle("dis", !has);
  // while the click animation runs (and the keyboard catches up) the bars belong to it
  if (Date.now() > blAnim.hold) animateLevel(has && bl.enabled ? bl.level : -1);
  if (!has) { $("#blInfo").textContent = st.connected ? "Bu klavye arka ışık ayarı bildirmiyor." : "Klavye bağlı değil."; $("#blLvlText").textContent = ""; return; }
  const mode = !bl.enabled ? "off" : bl.mode === 1 ? "auto" : "manual";
  seg($("#blSeg"), mode, v => setBL(v, bl.level));
  if (Date.now() > blAnim.hold) $("#blLvlText").textContent = `${bl.level} / ${lv - 1}`;
  $("#blInfo").textContent = st.backlight_override ? "Elle ayarlandı — bir sonraki kural değişimine kadar geçerli." : "Kurallar uygulanıyor.";
}
// level bars: light up / dim one step at a time towards the target, so a click
// visibly "travels" instead of jumping (and never looks stuck while the keyboard answers)
const blAnim = { shown: null, target: null, timer: 0, hold: 0 };
function paintLevel(n) {
  $$("#blPicker button").forEach(b => b.classList.toggle("on", +b.dataset.l <= n));
  blAnim.shown = n;
}
function animateLevel(target) {
  if (blAnim.shown === null) { paintLevel(target); return; }
  if (target === blAnim.target && blAnim.timer) return;
  blAnim.target = target;
  clearInterval(blAnim.timer);
  if (blAnim.shown === target) { blAnim.timer = 0; return; }
  blAnim.timer = setInterval(() => {
    const s = blAnim.shown;
    if (s === blAnim.target) { clearInterval(blAnim.timer); blAnim.timer = 0; return; }
    paintLevel(s + (blAnim.target > s ? 1 : -1));
  }, 45);
}
function pickLevel(l) {
  const lv = (st && st.backlight.levels) || 8;
  blAnim.hold = Date.now() + 2500;
  animateLevel(l);
  $("#blLvlText").textContent = `${l} / ${lv - 1}`;
  seg($("#blSeg"), "manual");
  setBL("manual", l);
}

async function setBL(mode, level) {
  try { st = await api("POST", "/api/backlight", { mode, level }); renderLightState(); renderHomeState(); } catch (e) { toast(e.message, true); }
}
$("#blRules").onclick = () => setBL("rules", 0);

function renderLightCfg() {
  const b = cfg.backlight;
  b.rules = b.rules || [];
  $("#rulesBody").innerHTML = (b.rules.length ? `<div class="rule-head"><span></span><span>BAŞLANGIÇ</span><span>BİTİŞ</span><span>PROGRAMLAR</span><span>MOD</span><span>SEVİYE</span><span></span></div>` : "") + b.rules.map((r, i) => `
    <div class="rule">
      <div class="ri">${i + 1}</div>
      <input type="time" data-r="${i}" data-k="from" value="${esc(r.from || "")}">
      <input type="time" data-r="${i}" data-k="to" value="${esc(r.to || "")}">
      <div class="app-field"><input class="mono" data-r="${i}" data-k="apps" placeholder="hepsi" value="${esc((r.apps || []).join(", "))}"><button class="btn sm icon-only" data-rapp="${i}" title="Program ekle">${icon("i-plus", "sm")}</button></div>
      <select data-r="${i}" data-k="mode">${[["manual", "Manuel"], ["auto", "Otomatik"], ["off", "Kapalı"]].map(([v, l]) => `<option value="${v}" ${v === r.mode ? "selected" : ""}>${l}</option>`).join("")}</select>
      <input type="number" min="0" class="num" data-r="${i}" data-k="level" value="${r.level ?? 0}">
      <button class="icon-btn danger" data-rx="${i}" title="Sil">${icon("i-x", "sm")}</button>
    </div>`).join("") || '<div class="muted small">Kural yok. Örneğin gece 23:00–07:00 arası seviye 1.</div>';
  $$("[data-r]").forEach(el => el.onchange = () => {
    const r = b.rules[+el.dataset.r], k = el.dataset.k;
    if (k === "apps") r.apps = el.value.split(",").map(s => s.trim()).filter(Boolean);
    else if (k === "level") r.level = +el.value;
    else r[k] = el.value;
  });
  $$("[data-rapp]").forEach(el => el.onclick = async () => {
    const a = await pickApp("Kural hangi programda geçerli olsun?");
    if (!a) return;
    const r = b.rules[+el.dataset.rapp];
    r.apps = [...(r.apps || []).filter(x => !ieq(x, a)), a];
    renderLightCfg();
  });
  $$("[data-rx]").forEach(el => el.onclick = () => { b.rules.splice(+el.dataset.rx, 1); renderLightCfg(); });
  $("#blDefault").value = b.default || "leave";
  $("#blDefaultLevel").value = b.default_level ?? 4;
  $("#blLowOff").value = b.low_battery_off_percent ?? 0;
  $("#dOut").value = b.hands_out_seconds || 0;
  $("#dIn").value = b.hands_in_seconds || 0;
  $("#dPow").value = b.powered_seconds || 0;
}
$("#addRule").onclick = () => { cfg.backlight.rules.push({ from: "22:00", to: "07:00", mode: "manual", level: 1 }); renderLightCfg(); };
$("#saveLight").onclick = async () => {
  const b = cfg.backlight;
  b.default = $("#blDefault").value;
  b.default_level = +$("#blDefaultLevel").value || 0;
  b.low_battery_off_percent = +$("#blLowOff").value || 0;
  b.hands_out_seconds = +$("#dOut").value || 0;
  b.hands_in_seconds = +$("#dIn").value || 0;
  b.powered_seconds = +$("#dPow").value || 0;
  await saveCfg("Işık ayarları kaydedildi");
};

// ---------- game mode ----------
function renderGameState() {
  const on = !!st.game_mode;
  $("#gameHero").classList.toggle("on", on);
  $("#gameState").textContent = on ? "Oyun modu açık" : "Oyun modu kapalı";
  $("#gameReason").textContent = on ? (st.game_mode_reason === "elle" ? "Elle açıldı." : `${st.game_mode_reason} önde olduğu için otomatik açıldı.`)
    : cfg && cfg.game_mode.apps.length ? "Listedeki bir program öne gelince açılır." : "Elle aç ya da otomatik açılacağı programları ekle.";
}
function renderGameCfg() {
  const g = cfg.game_mode;
  $("#gmManual").checked = g.enabled;
  $("#gmWin").checked = g.block_win;
  $("#gmAltTab").checked = g.block_alt_tab;
  $("#gmAltF4").checked = g.block_alt_f4;
  $("#gmApps").innerHTML = g.apps.map((a, i) => `<span class="chip big">${icon("i-app", "sm")}${esc(a)}<button data-gx="${i}" title="Kaldır">${icon("i-x", "sm")}</button></span>`).join("") || '<span class="muted small">Program yok.</span>';
  $$("[data-gx]").forEach(b => b.onclick = () => { g.apps.splice(+b.dataset.gx, 1); renderGameCfg(); });
}
async function setGame(on) {
  try { st = await api("POST", "/api/gamemode", { on }); cfg = await api("GET", "/api/config"); renderGameCfg(); refreshState(); toast(on ? "Oyun modu açık" : "Oyun modu kapalı"); }
  catch (e) { toast(e.message, true); }
}
$("#gmBtn").onclick = () => {
  if (st && st.game_mode && !cfg.game_mode.enabled) { go("game"); toast(`${st.game_mode_reason} önde olduğu için açık; listeden çıkarabilirsin`); return; }
  setGame(!cfg.game_mode.enabled);
};
$("#gmManual").onchange = e => setGame(e.target.checked);
$("#gmAddApp").onclick = async () => {
  const a = await pickApp("Hangi oyunda otomatik açılsın?");
  if (a && !cfg.game_mode.apps.some(x => ieq(x, a))) { cfg.game_mode.apps.push(a); renderGameCfg(); }
};
$("#saveGame").onclick = async () => {
  const g = cfg.game_mode;
  g.block_win = $("#gmWin").checked;
  g.block_alt_tab = $("#gmAltTab").checked;
  g.block_alt_f4 = $("#gmAltF4").checked;
  if (!g.block_win && !g.block_alt_tab && !g.block_alt_f4) toast("Hiçbir tuş seçili değil; oyun modu bir şey kilitlemez", true);
  if (await saveCfg("Oyun modu ayarları kaydedildi")) refreshState();
};

// ---------- updates ----------
function renderUpdate(u) {
  if (!u) return;
  const busy = u.state === "downloading" || u.state === "installing";
  $("#updCur").textContent = u.current + (u.dev_build ? " (geliştirme)" : "");
  $("#updLatest").textContent = u.latest || "—";
  $("#updState").textContent =
    u.state === "checking" ? "Kontrol ediliyor…" :
    u.state === "downloading" ? `İndiriliyor… %${u.progress}` :
    u.state === "installing" ? "Yükleniyor, uygulama yeniden başlayacak…" :
    u.state === "error" ? "Hata: " + u.error :
    u.available ? "Yeni sürüm hazır" :
    u.latest ? `En güncel sürümü kullanıyorsun${u.checked ? " · " + u.checked : ""}` :
    u.dev_build ? "Geliştirme sürümü kendini güncellemez" : "";
  $("#updApply").hidden = !u.available;
  $("#updApply").disabled = busy;
  $("#updApply").textContent = busy ? "Güncelleniyor…" : `${u.latest} sürümüne güncelle`;
  $("#updSide").hidden = !u.available;
  $("#updSideText").textContent = busy ? `Güncelleniyor %${u.progress}` : `Güncelleme var · ${u.latest}`;
}
$("#updSide").onclick = () => go("adv");
$("#updCheck").onclick = async () => {
  $("#updCheck").disabled = true;
  try { const u = await api("POST", "/api/update/check"); renderUpdate(u); if (!u.available && u.state !== "error") toast("En güncel sürümü kullanıyorsun"); }
  catch (e) { toast(e.message, true); }
  finally { $("#updCheck").disabled = false; }
};
$("#updApply").onclick = async () => {
  if (!(await ask("Güncelle", "Yeni sürüm indirilip yüklenecek; MX Helper birkaç saniyeliğine kapanıp yeniden açılacak.", "Güncelle"))) return;
  await api("POST", "/api/update/apply");
  toast("Güncelleme indiriliyor…");
};
$("#updNotes").onclick = () => api("POST", "/api/update/notes");

// ---------- settings ----------
function renderAdvCfg() {
  $("#aName").value = cfg.device_name_match || "";
  $("#aPid").value = cfg.device_pid || "";
  $("#aPoll").value = cfg.poll_seconds || 10;
  $("#aStop").value = cfg.stop_all_combo || "";
}
$("#aStopCap").onclick = async () => { const t = await capture("Durdurma kısayoluna bas"); if (t) $("#aStop").value = t; };
$("#saveAdv").onclick = async () => {
  cfg.device_name_match = $("#aName").value.trim();
  cfg.device_pid = $("#aPid").value.trim();
  cfg.poll_seconds = +$("#aPoll").value || 10;
  cfg.stop_all_combo = $("#aStop").value.trim();
  await saveCfg("Kaydedildi");
};
async function loadRaw() { try { $("#raw").value = await api("GET", "/api/config/raw"); } catch {} }
$("#rawLoad").onclick = loadRaw;
$("#rawSave").onclick = async () => {
  try { await api("PUT", "/api/config/raw", $("#raw").value, true); toast("config.json kaydedildi"); await loadAll(); loadRaw(); }
  catch (e) { toast(e.message, true); }
};
const showOut = async p => { $("#out").textContent = "Yükleniyor…"; try { $("#out").textContent = await api("GET", p) || "(boş)"; } catch (e) { $("#out").textContent = e.message; } };
$("#diagBtn").onclick = () => showOut("/api/diag");
$("#logBtn").onclick = () => showOut("/api/log");
$("#rlogBtn").onclick = () => showOut("/api/log?f=repair");
$("#copyOut").onclick = async () => { await navigator.clipboard.writeText($("#out").textContent); toast("Kopyalandı"); };

// ---------- menus ----------
function closeMenus(except) {
  ["#newMenu", "#delayMenu", "#recMenu", "#profileMenu", "#palette"].forEach(s => { if (s !== except) $(s).hidden = true; });
  closeKeyMenu();
}
document.addEventListener("click", () => closeMenus());

// ---------- boot ----------
async function loadAll() {
  cfg = await api("GET", "/api/config");
  try { controls = await api("GET", "/api/controls") || []; } catch { controls = []; }
  if (!profiles().some(p => ieq(p, profile))) profile = "";
  setProfile(profile);
  renderHomeCfg(); renderLightCfg(); renderAdvCfg(); renderGameCfg();
  if (selId && !dirty) { const m = cfg.macros.find(x => x.id === selId); draft = m ? clone(m) : null; }
  renderEditor(); renderMacroList(); updateAssignKB();
  decorateFKeys($("#kbHome")); decorateFKeys($("#kbLight"));
}
window.addEventListener("beforeunload", e => { if (dirty) { e.preventDefault(); e.returnValue = ""; } });

buildKB($("#kbHome"), false);
buildKB($("#kbAssign"), true);
buildKB($("#kbLight"), false);

(async () => {
  try { await loadAll(); } catch (e) { toast("Bağlantı hatası: " + e.message, true); }
  await refreshState();
  const t = store.get("tab", "home");
  if (t !== "home") go(t);
  setInterval(refreshState, 2000);
  setInterval(async () => {
    if (controls.length) return;
    try { controls = await api("GET", "/api/controls") || []; if (controls.length) { updateAssignKB(); decorateFKeys($("#kbHome")); decorateFKeys($("#kbLight")); } } catch {}
  }, 10000);
})();
