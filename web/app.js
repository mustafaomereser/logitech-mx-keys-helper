"use strict";

const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

let cfg = null;      // full config object
let st = null;       // last /api/state
let controls = [];   // MX keys from /api/controls
let selId = null;    // selected macro id
let draft = null;    // macro being edited (copy)
let dirty = false;

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
  toast._t = setTimeout(() => (t.hidden = true), err ? 5000 : 2200);
}

function uid() { return Math.random().toString(16).slice(2, 14); }
function clone(o) { return JSON.parse(JSON.stringify(o)); }

// ---------- nav ----------
$$("#nav button").forEach(b => b.onclick = () => {
  if (dirty && !confirm("Kaydedilmemiş makro değişiklikleri var. Yine de geçilsin mi?")) return;
  $$("#nav button").forEach(x => x.classList.toggle("active", x === b));
  $$(".tab").forEach(t => t.classList.toggle("active", t.id === "tab-" + b.dataset.tab));
  if (b.dataset.tab === "adv") loadRaw();
});

function seg(el, value, onPick) {
  $$("button", el).forEach(b => {
    b.classList.toggle("on", b.dataset.v === value);
    if (onPick) b.onclick = () => onPick(b.dataset.v);
  });
}

// ---------- state ----------
async function refreshState() {
  try { st = await api("GET", "/api/state"); } catch { return; }
  $("#ver").textContent = st.version;
  const on = st.connected;
  $("#devPill").classList.toggle("on", on);
  $("#devText").textContent = on ? (st.name || "MX Keys") : "Bağlı değil";
  const b = batt();
  $("#battMini").textContent = b.known ? `Pil %${b.pct}${b.chg ? " · şarjda" : ""}` : "";
  renderHomeState();
  renderLightState();
  renderMacroList();
}

function batt() {
  const b = st.battery || {};
  return { known: b.known, pct: b.percent ?? 0, chg: b.charging, full: b.full };
}

// ---------- home ----------
function renderHomeState() {
  $("#homeName").textContent = st.name || "MX Keys Mini";
  $("#homeConn").textContent = st.connected ? "Bluetooth ile bağlı" : "Bağlı değil — klavyeye dokununca bağlanır";
  const b = batt();
  $("#battFill").style.width = b.known ? b.pct + "%" : "0";
  $("#battFill").style.background = b.pct <= 15 ? "var(--bad)" : b.pct <= 30 ? "var(--warn)" : "var(--ok)";
  $("#battText").textContent = b.known ? `%${b.pct}${b.chg ? " · şarj oluyor" : b.full ? " · dolu" : ""}` : "—";
  const lv = st.repair.last_working_level;
  $("#repairLvl").textContent = lv ? `Seviye ${lv}` : "henüz yok";
  const s = st.repair.successes || {};
  $("#repairCnt").textContent = Object.values(s).reduce((a, x) => a + x, 0) || 0;
  $("#autostart").checked = st.autostart;
  $("#elevInfo").textContent = st.elevated ? "Şu an yönetici olarak çalışıyor." : "Şu an normal kullanıcı olarak çalışıyor; onarım yönetici izni isteyecek.";
  const w = $("#logiWarn");
  w.hidden = !st.logi_apps.length;
  w.textContent = st.logi_apps.length ? `Açık Logitech yazılımı: ${st.logi_apps.join(", ")}. Özel tuşlar ve ışık ayarları onlarla çakışabilir.` : "";
}

function renderHomeCfg() {
  seg($("#autoSeg"), cfg.repair.auto, v => { cfg.repair.auto = v; seg($("#autoSeg"), v); });
  $("#mediaN").value = cfg.repair.media_presses_to_suspect;
  $("#lowWarn").value = cfg.low_battery_warn_percent;
}

$("#repairBtn").onclick = async () => { await api("POST", "/api/repair"); toast("Onarım başlatıldı"); };
$("#saveHome").onclick = async () => {
  cfg.repair.media_presses_to_suspect = +$("#mediaN").value || 3;
  cfg.low_battery_warn_percent = +$("#lowWarn").value || 0;
  await saveCfg("Kaydedildi");
};
$("#resetRepair").onclick = async () => { await api("POST", "/api/repair/reset"); toast("Onarım geçmişi sıfırlandı"); refreshState(); };
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
    return true;
  } catch (e) { toast(e.message, true); return false; }
}

// ---------- triggers ----------
function ctlName(t) {
  const c = controls.find(x => x.cid === t);
  return c && c.name !== "?" ? c.name.replace("?", "") : "";
}

function triggerHTML(t) {
  t = (t || "").trim().toLowerCase();
  if (!t) return '<span class="muted">tetikleyici yok</span>';
  if (t.startsWith("mx:")) {
    const n = ctlName(t);
    return `<kbd>MX</kbd> <span class="mono">${esc(t.slice(3))}</span>${n ? ` <span class="muted small">${esc(n)}</span>` : ""}`;
  }
  const mouse = { mouse4: "Fare yan tuşu 4 (geri)", mouse5: "Fare yan tuşu 5 (ileri)", middle: "Fare orta tuş" };
  if (mouse[t]) return `<kbd>🖱</kbd> ${mouse[t]}`;
  return t.split("+").map(p => `<kbd>${esc(p.length === 1 ? p.toUpperCase() : p)}</kbd>`).join("<span class='muted'>+</span>");
}

async function capture(title) {
  const ov = $("#overlay");
  $("#ovTitle").textContent = title || "Bir tuşa bas";
  $("#ovText").innerHTML = "Klavye kombinasyonu (ör. <kbd>Ctrl</kbd>+<kbd>Alt</kbd>+<kbd>K</kbd>), fare yan tuşu veya MX özel tuşu. 15 saniye beklenir.";
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

// ---------- macros ----------
const modeName = { press: "bir kez", hold: "basılı tut", toggle: "aç/kapa" };

function renderMacroList() {
  if (!cfg) return;
  const list = $("#macroList");
  const running = new Set(st ? st.running : []);
  if (!cfg.macros.length) {
    list.innerHTML = '<div class="mlist-empty">Henüz makro yok.<br>Sağ üstten “Yeni makro”.</div>';
    return;
  }
  list.innerHTML = cfg.macros.map(m => `
    <div class="mitem ${m.id === selId ? "sel" : ""} ${m.enabled ? "" : "off"}" data-id="${esc(m.id)}">
      <div class="n">${esc(m.name || "Adsız")}</div>
      <label class="switch" onclick="event.stopPropagation()"><input type="checkbox" data-en="${esc(m.id)}" ${m.enabled ? "checked" : ""}><span></span></label>
      <div class="t">${triggerHTML(m.trigger)}
        <span class="chip mode-${m.mode}">${modeName[m.mode] || m.mode}</span>
        ${m.app ? `<span class="chip">${esc(m.app)}</span>` : ""}
        ${running.has(m.id) ? '<span class="chip run">çalışıyor</span>' : ""}
      </div>
    </div>`).join("");
  $$(".mitem", list).forEach(el => el.onclick = () => selectMacro(el.dataset.id));
  $$("input[data-en]", list).forEach(el => el.onchange = async () => {
    const m = cfg.macros.find(x => x.id === el.dataset.en);
    m.enabled = el.checked;
    if (draft && draft.id === m.id) { draft.enabled = el.checked; $("#mEnabled").checked = el.checked; }
    await saveCfg(el.checked ? "Etkinleştirildi" : "Devre dışı");
    renderMacroList();
  });
}

function selectMacro(id) {
  if (dirty && id !== selId && !confirm("Kaydedilmemiş değişiklikler kaybolsun mu?")) return;
  selId = id;
  const m = cfg.macros.find(x => x.id === id);
  draft = m ? clone(m) : null;
  dirty = false;
  renderEditor();
  renderMacroList();
}

function markDirty() { dirty = true; $("#saveMacro").textContent = "Kaydet •"; }

function renderEditor() {
  $("#editorEmpty").hidden = !!draft;
  $("#editorBody").hidden = !draft;
  if (!draft) return;
  $("#saveMacro").textContent = dirty ? "Kaydet •" : "Kaydet";
  $("#mName").value = draft.name || "";
  $("#mEnabled").checked = !!draft.enabled;
  $("#mTrigger").value = draft.trigger || "";
  $("#mTriggerView").innerHTML = triggerHTML(draft.trigger);
  seg($("#modeSeg"), draft.mode || "press", v => { draft.mode = v; dirty = true; renderEditor(); });
  $("#mInterval").value = draft.interval_ms || 50;
  $("#mRepeat").value = draft.repeat || 1;
  $("#fInterval").style.display = draft.mode === "press" && (draft.repeat || 1) <= 1 ? "none" : "";
  $("#fRepeat").style.display = draft.mode === "press" ? "" : "none";
  $("#mApp").value = draft.app || "";
  $("#mPass").checked = !!draft.passthrough;
  updateRate();
  renderActions();
}

function updateRate() {
  const iv = +$("#mInterval").value || 1;
  const hold = (draft.actions || []).reduce((a, x) => a + (x.hold_ms || 0) + (x.type === "delay" ? x.ms || 0 : 0), 0);
  const per = 1000 / Math.max(1, iv + hold);
  $("#rateHint").textContent = draft.mode === "press" ? "" : `≈ ${per >= 10 ? Math.round(per) : per.toFixed(1)} tekrar/sn`;
}

$("#mName").oninput = e => { draft.name = e.target.value; markDirty(); };
$("#mEnabled").onchange = e => { draft.enabled = e.target.checked; markDirty(); };
$("#mTrigger").oninput = e => { draft.trigger = e.target.value; $("#mTriggerView").innerHTML = triggerHTML(draft.trigger); markDirty(); };
$("#mInterval").oninput = e => { draft.interval_ms = +e.target.value; updateRate(); markDirty(); };
$("#mRepeat").oninput = e => { draft.repeat = +e.target.value; $("#fInterval").style.display = (+e.target.value || 1) > 1 ? "" : "none"; markDirty(); };
$("#mApp").oninput = e => { draft.app = e.target.value.trim(); markDirty(); };
$("#mPass").onchange = e => { draft.passthrough = e.target.checked; markDirty(); };
$("#captureBtn").onclick = async () => {
  const t = await capture("Tetikleyici tuşa bas");
  if (!t) return;
  draft.trigger = t; dirty = true; renderEditor();
};
$("#pickApp").onclick = async () => {
  toast("3 saniye içinde hedef programın penceresine geç…");
  const r = await api("GET", "/api/foreground?delay=3");
  if (r.app && !/msedge\.exe/i.test(r.app)) { draft.app = r.app; $("#mApp").value = r.app; markDirty(); toast("Seçildi: " + r.app); }
  else toast("Program seçilemedi, tekrar dene", true);
};

// ----- actions editor -----
const typeLabel = {
  click: "Fare tıkla", keys: "Tuşa bas", keydown: "Tuşu basılı tut", keyup: "Tuşu bırak",
  mousedown: "Fare bas", mouseup: "Fare bırak", delay: "Bekle", text: "Metin yaz", run: "Program aç",
  url: "Web adresi", shell: "Komut", http: "HTTP isteği", backlight: "Arka ışık", repair: "Klavyeyi onar",
};
const btnOpts = v => ["left", "right", "middle", "mouse4", "mouse5"].map(b =>
  `<option value="${b}" ${b === (v || "left") ? "selected" : ""}>${{ left: "Sol", right: "Sağ", middle: "Orta", mouse4: "Yan 4", mouse5: "Yan 5" }[b]}</option>`).join("");

function fieldsHTML(a, i) {
  const inp = (k, ph, cls = "w-m", type = "text") => `<input data-i="${i}" data-k="${k}" type="${type}" class="${cls} ${type === "text" ? "mono" : ""}" placeholder="${ph}" value="${esc(a[k] ?? "")}">`;
  switch (a.type) {
    case "click": return `<select data-i="${i}" data-k="button">${btnOpts(a.button)}</select> basılı ${inp("hold_ms", "0", "w-s", "number")} ms`;
    case "mousedown": case "mouseup": return `<select data-i="${i}" data-k="button">${btnOpts(a.button)}</select>`;
    case "keys": return `${inp("keys", "ctrl+shift+esc")} basılı ${inp("hold_ms", "0", "w-s", "number")} ms <button class="btn small" data-cap="${i}">Yakala</button>`;
    case "keydown": case "keyup": return `${inp("keys", "space")} <button class="btn small" data-cap="${i}">Yakala</button>`;
    case "delay": return `${inp("ms", "100", "w-s", "number")} ms`;
    case "text": return `<textarea data-i="${i}" data-k="text" placeholder="Yazılacak metin">${esc(a.text || "")}</textarea>`;
    case "run": return `${inp("cmd", "notepad.exe veya C:\\yol")} ${inp("args", "argümanlar")}`;
    case "url": return inp("url", "https://…", "grow");
    case "shell": return inp("cmd", "ör. shutdown /a", "grow");
    case "http": return `<select data-i="${i}" data-k="method">${["GET", "POST", "PUT", "DELETE"].map(m => `<option ${m === (a.method || "GET") ? "selected" : ""}>${m}</option>`).join("")}</select> ${inp("url", "https://…")} ${inp("body", "gövde (isteğe bağlı)")}`;
    case "backlight": return `<select data-i="${i}" data-k="value">${[["toggle", "Aç/kapa"], ["up", "Artır"], ["down", "Azalt"], ["auto", "Otomatik"], ["off", "Kapat"], ...[0, 1, 2, 3, 4, 5, 6, 7].map(n => [String(n), "Seviye " + n])].map(([v, l]) => `<option value="${v}" ${v === (a.value || "toggle") ? "selected" : ""}>${l}</option>`).join("")}</select>`;
    default: return "";
  }
}

function renderActions() {
  const box = $("#actions");
  const acts = draft.actions || (draft.actions = []);
  if (!acts.length) { box.innerHTML = '<div class="actions-empty">Eylem yok. Aşağıdan ekle veya ● Kayda başla ile tuşlarını kaydet.</div>'; return; }
  box.innerHTML = acts.map((a, i) => `
    <div class="act ${a.type === "delay" ? "delay" : ""}">
      <div class="idx">${i + 1}</div>
      <div class="lbl">${typeLabel[a.type] || esc(a.type)}</div>
      <div class="fields">${fieldsHTML(a, i)}</div>
      <div class="tools">
        <button data-up="${i}" title="Yukarı">↑</button><button data-down="${i}" title="Aşağı">↓</button><button data-del="${i}" title="Sil">✕</button>
      </div>
    </div>`).join("");
  $$("[data-k]", box).forEach(el => {
    const ev = el.tagName === "SELECT" ? "onchange" : "oninput";
    el[ev] = () => {
      const a = acts[+el.dataset.i];
      a[el.dataset.k] = el.type === "number" ? +el.value : el.value;
      markDirty(); updateRate();
    };
  });
  $$("[data-up]", box).forEach(b => b.onclick = () => move(+b.dataset.up, -1));
  $$("[data-down]", box).forEach(b => b.onclick = () => move(+b.dataset.down, 1));
  $$("[data-del]", box).forEach(b => b.onclick = () => { acts.splice(+b.dataset.del, 1); markDirty(); renderActions(); updateRate(); });
  $$("[data-cap]", box).forEach(b => b.onclick = async () => {
    const t = await capture("Gönderilecek tuşa bas");
    if (!t || t.startsWith("mx:") || t.startsWith("mouse") || t === "middle") return;
    acts[+b.dataset.cap].keys = t; markDirty(); renderActions();
  });
}

function move(i, d) {
  const a = draft.actions, j = i + d;
  if (j < 0 || j >= a.length) return;
  [a[i], a[j]] = [a[j], a[i]];
  markDirty(); renderActions();
}

const defaults = {
  click: { button: "left", hold_ms: 8 }, keys: { keys: "", hold_ms: 0 }, keydown: { keys: "" }, keyup: { keys: "" },
  mousedown: { button: "left" }, mouseup: { button: "left" }, delay: { ms: 100 }, text: { text: "" },
  run: { cmd: "" }, url: { url: "https://" }, shell: { cmd: "" }, http: { method: "POST", url: "https://" },
  backlight: { value: "toggle" }, repair: {},
};
$("#addAction").onclick = () => {
  const t = $("#addType").value;
  draft.actions.push({ type: t, ...clone(defaults[t] || {}) });
  markDirty(); renderActions(); updateRate();
};
$("#clearActions").onclick = () => { if (draft.actions.length && confirm("Tüm eylemler silinsin mi?")) { draft.actions = []; markDirty(); renderActions(); } };

// ----- recording -----
let recTimer = null;
$("#recBtn").onclick = async () => {
  await api("POST", "/api/record/start", { mouse: $("#recMouse").checked });
  const t0 = Date.now();
  $("#recbar").hidden = false;
  recTimer = setInterval(() => $("#recTime").textContent = ((Date.now() - t0) / 1000).toFixed(1) + " sn", 100);
};
$("#recStop").onclick = async () => {
  clearInterval(recTimer);
  $("#recbar").hidden = true;
  const r = await api("POST", "/api/record/stop", { delays: $("#recDelays").checked });
  const acts = r.actions || [];
  if (!acts.length) { toast("Hiçbir şey kaydedilmedi", true); return; }
  if (draft.actions.length && !confirm(`${acts.length} eylem kaydedildi. Mevcut eylemlerin sonuna eklensin mi? (İptal = üzerine yaz)`)) draft.actions = [];
  draft.actions.push(...acts);
  markDirty(); renderActions(); updateRate();
  toast(`${acts.length} eylem kaydedildi`);
};

// ----- new / save / delete -----
const templates = {
  turboClick: () => ({ name: "Turbo tık", mode: "hold", interval_ms: 30, trigger: "mouse5", actions: [{ type: "click", button: "left", hold_ms: 8 }] }),
  turboKey: () => ({ name: "Turbo tuş", mode: "hold", interval_ms: 30, trigger: "", actions: [{ type: "keys", keys: "space", hold_ms: 8 }] }),
  record: () => ({ name: "Kayıtlı makro", mode: "press", trigger: "", actions: [] }),
  text: () => ({ name: "Metin", mode: "press", trigger: "", actions: [{ type: "text", text: "" }] }),
  run: () => ({ name: "Program aç", mode: "press", trigger: "", actions: [{ type: "run", cmd: "" }] }),
  blank: () => ({ name: "Yeni makro", mode: "press", trigger: "", actions: [] }),
};
$("#newMacroBtn").onclick = e => { e.stopPropagation(); $("#newMenu").hidden = !$("#newMenu").hidden; };
document.addEventListener("click", () => ($("#newMenu").hidden = true));
$$("#newMenu button").forEach(b => b.onclick = () => {
  if (dirty && !confirm("Kaydedilmemiş değişiklikler kaybolsun mu?")) return;
  const m = { id: uid(), enabled: true, app: "", passthrough: false, repeat: 1, ...templates[b.dataset.t]() };
  cfg.macros.push(m);
  selId = m.id; draft = clone(m); dirty = true;
  renderMacroList(); renderEditor();
  if (b.dataset.t === "record") toast("Tetikleyiciyi yakala, sonra ● Kayda başla ile tuşlarını kaydet");
});

$("#saveMacro").onclick = async () => {
  if (draft.enabled && !draft.trigger) { toast("Önce bir tetikleyici seç (Yakala)", true); return; }
  const i = cfg.macros.findIndex(x => x.id === draft.id);
  const backup = clone(cfg.macros);
  if (i >= 0) cfg.macros[i] = clone(draft); else cfg.macros.push(clone(draft));
  if (await saveCfg("Makro kaydedildi")) { dirty = false; $("#saveMacro").textContent = "Kaydet"; }
  else cfg.macros = backup;
  renderMacroList();
};
$("#delMacro").onclick = async () => {
  if (!confirm(`“${draft.name}” silinsin mi?`)) return;
  cfg.macros = cfg.macros.filter(x => x.id !== draft.id);
  draft = null; selId = null; dirty = false;
  await saveCfg("Silindi");
  renderMacroList(); renderEditor();
};
$("#dupMacro").onclick = () => {
  const m = { ...clone(draft), id: uid(), name: draft.name + " (kopya)", enabled: false };
  cfg.macros.push(m); selId = m.id; draft = clone(m); dirty = true;
  renderMacroList(); renderEditor();
};
$("#testMacro").onclick = async () => {
  await api("POST", "/api/macro/test", draft);
  toast(draft.mode === "press" ? "2 sn sonra bir kez çalışacak — hedef pencereye geç" : "2 sn sonra 2 sn boyunca tekrarlayacak");
};
$("#stopAll").onclick = async () => { await api("POST", "/api/macro/stopall"); toast("Durduruldu"); };

// ---------- light ----------
function renderLightState() {
  const bl = st.backlight;
  const has = bl.known;
  $$("#blSeg button").forEach(b => (b.disabled = !has));
  $("#blLevel").disabled = !has;
  if (!has) { $("#blInfo").textContent = st.connected ? "Bu klavye arka ışık ayarı bildirmiyor." : "Klavye bağlı değil."; return; }
  const mode = !bl.enabled ? "off" : bl.mode === 1 ? "auto" : bl.mode === 3 ? "manual" : "auto";
  seg($("#blSeg"), mode, v => setBL(v, +$("#blLevel").value));
  const levels = bl.levels || 8;
  $("#blLevel").max = levels - 1;
  if (document.activeElement !== $("#blLevel")) $("#blLevel").value = bl.level;
  $("#blLvlText").textContent = `${bl.level} / ${levels - 1}`;
  $("#blInfo").textContent = st.backlight_override ? "Elle ayarlandı — bir sonraki kural değişimine kadar geçerli." : "Kurallar uygulanıyor.";
}
async function setBL(mode, level) {
  try { st = await api("POST", "/api/backlight", { mode, level }); renderLightState(); } catch (e) { toast(e.message, true); }
}
$("#blLevel").onchange = e => setBL("manual", +e.target.value);
$("#blLevel").oninput = e => ($("#blLvlText").textContent = e.target.value);
$("#blRules").onclick = () => setBL("rules", 0);

function renderLightCfg() {
  const b = cfg.backlight;
  b.rules = b.rules || [];
  $("#rulesBody").innerHTML = b.rules.map((r, i) => `
    <tr>
      <td><input type="time" data-r="${i}" data-k="from" value="${esc(r.from || "")}"></td>
      <td><input type="time" data-r="${i}" data-k="to" value="${esc(r.to || "")}"></td>
      <td><input class="mono" data-r="${i}" data-k="apps" placeholder="hepsi" value="${esc((r.apps || []).join(", "))}"></td>
      <td><select data-r="${i}" data-k="mode">${[["manual", "Manuel"], ["auto", "Otomatik"], ["off", "Kapalı"]].map(([v, l]) => `<option value="${v}" ${v === r.mode ? "selected" : ""}>${l}</option>`).join("")}</select></td>
      <td><input type="number" min="0" class="num" data-r="${i}" data-k="level" value="${r.level ?? 0}"></td>
      <td><button class="x" data-rx="${i}">✕</button></td>
    </tr>`).join("") || '<tr><td colspan="6" class="muted small">Kural yok.</td></tr>';
  $$("[data-r]").forEach(el => el.onchange = () => {
    const r = b.rules[+el.dataset.r], k = el.dataset.k;
    if (k === "apps") r.apps = el.value.split(",").map(s => s.trim()).filter(Boolean);
    else if (k === "level") r.level = +el.value;
    else r[k] = el.value;
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

// ---------- advanced ----------
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
async function loadRaw() { $("#raw").value = await api("GET", "/api/config/raw"); }
$("#rawLoad").onclick = loadRaw;
$("#rawSave").onclick = async () => {
  try { await api("PUT", "/api/config/raw", $("#raw").value, true); toast("config.json kaydedildi"); await loadAll(); loadRaw(); }
  catch (e) { toast(e.message, true); }
};
const showOut = async p => { $("#out").textContent = "Yükleniyor…"; $("#out").textContent = await api("GET", p) || "(boş)"; };
$("#diagBtn").onclick = () => showOut("/api/diag");
$("#logBtn").onclick = () => showOut("/api/log");
$("#rlogBtn").onclick = () => showOut("/api/log?f=repair");
$("#copyOut").onclick = async () => { await navigator.clipboard.writeText($("#out").textContent); toast("Kopyalandı"); };

// ---------- boot ----------
async function loadAll() {
  cfg = await api("GET", "/api/config");
  try { controls = await api("GET", "/api/controls") || []; } catch { controls = []; }
  renderHomeCfg(); renderLightCfg(); renderAdvCfg();
  if (selId && !dirty) { const m = cfg.macros.find(x => x.id === selId); draft = m ? clone(m) : null; renderEditor(); }
  renderMacroList();
}
window.addEventListener("beforeunload", e => { if (dirty) { e.preventDefault(); e.returnValue = ""; } });

(async () => {
  try { await loadAll(); } catch (e) { toast("Bağlantı hatası: " + e.message, true); }
  await refreshState();
  setInterval(refreshState, 2000);
  setInterval(async () => { if (!controls.length) { try { controls = await api("GET", "/api/controls") || []; } catch {} } }, 10000);
})();
