// SPDX-License-Identifier: AGPL-3.0-or-later
"use strict";
(() => {
  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => [...r.querySelectorAll(s)];
  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);

  let S = null;                 // last state from the core
  let view = "hardware";
  const gang = new Set();       // ganged strip ids (UI only)
  let lastGangAnchor = null;
  const busy = new Set();       // strips being edited/dragged: don't re-render them
  const stripHTML = new Map();
  const local = new Map();      // optimistic fader values while dragging

  // ---------- core API ----------
  async function post(url, body) {
    const r = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    const j = await r.json().catch(() => ({ error: r.statusText }));
    if (j.error) throw new Error(j.error);
    return j;
  }
  async function op(o) {
    try {
      const j = await post("api/op", o);
      if (j.message) alertBox("21 Remote", j.message);
    } catch (e) { alertBox("Could not apply the change", e.message); }
  }

  function connect() {
    const es = new EventSource("api/events");
    es.onmessage = (ev) => { S = JSON.parse(ev.data); render(); };
    es.onerror = () => { $("#pollInfo").textContent = "Reconnecting to core…"; };
  }

  // ---------- dialogs ----------
  const dlg = $("#dlg");
  function dialogBox(html, buttons) {
    return new Promise((resolve) => {
      $("#dlgBody").innerHTML = html;
      const act = $("#dlgActions");
      act.innerHTML = "";
      buttons.forEach((b, i) => {
        const el = document.createElement("button");
        el.textContent = b.label;
        el.value = b.value ?? String(i);
        if (b.primary) el.className = "primary";
        act.appendChild(el);
      });
      dlg.onclose = () => resolve(dlg.returnValue);
      dlg.returnValue = "";
      dlg.showModal();
      const inp = $("input", dlg);
      if (inp) { inp.focus(); inp.select(); }
      else $$("button", act).pop()?.focus();
    });
  }
  const alertBox = (title, text) => dialogBox(`<h2>${esc(title)}</h2><p>${esc(text)}</p>`, [{ label: "OK", primary: true, value: "ok" }]);
  async function promptBox(title, value) {
    const r = await dialogBox(`<h2>${esc(title)}</h2><input type="text" id="dlgInput" value="${esc(value)}">`,
      [{ label: "Cancel", value: "" }, { label: "OK", primary: true, value: "ok" }]);
    return r === "ok" ? $("#dlgInput").value : null;
  }
  dlg.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && e.target.id === "dlgInput") { e.preventDefault(); dlg.close("ok"); }
  });

  // native file panels when running inside the macOS app, osascript otherwise
  const pending = new Map();
  let nativeSeq = 0;
  window.__nativeReply = (id, path) => { pending.get(id)?.(path || ""); pending.delete(id); };
  async function pickFile(kind) {
    const nh = window.webkit?.messageHandlers?.native;
    if (nh) {
      const id = ++nativeSeq;
      return new Promise((res) => { pending.set(id, res); nh.postMessage({ cmd: kind, id, name: "Session.xml" }); });
    }
    const j = await post("api/dialog", { kind });
    if (j.prompt) return (await promptBox(kind === "save" ? "Save session as (full path)" : "Open session (full path)", j.suggest || "")) || "";
    return j.path || "";
  }

  // before a new or another session: save the current one? "" = cancelled
  async function askSaveCurrent(title, goLabel) {
    const r = await dialogBox(`<h2>${esc(title)}</h2><p>Do you want to save the current session first?</p>`,
      [{ label: "Cancel", value: "" }, { label: goLabel, value: "go" }, { label: "Save…", primary: true, value: "save" }]);
    if (r === "save") return (await saveSnapshot()) ? "go" : "";
    return r;
  }
  async function openSnapshot(path) {
    if ((await askSaveCurrent("Open another session?", "Open Without Saving")) !== "go") return;
    path = path ?? (await pickFile("open"));
    if (!path) return;
    try {
      let j = await post("api/snapshot/open", { path, force: false });
      if (j.mismatches?.length) {
        const rows = j.mismatches.map((m) => `<li>Slot ${m.slot}: session has ${esc(m.snapshot)}, rack has ${esc(m.now)}</li>`).join("");
        const r = await dialogBox(`<h2>The session doesn’t match the rack</h2><ul>${rows}</ul><p>Cards that match can still be restored. The others are left as they are.</p>`,
          [{ label: "Cancel", value: "" }, { label: "Restore matching cards", primary: true, value: "ok" }]);
        if (r !== "ok") return;
        await post("api/snapshot/open", { path, force: true });
      }
    } catch (e) { alertBox("Could not open the session", e.message); }
  }
  async function saveSnapshot() {
    const path = await pickFile("save");
    if (!path) return false;
    try { await post("api/snapshot/save", { path }); return true; } catch (e) { alertBox("Could not save the session", e.message); return false; }
  }

  // ---------- views ----------
  function setView(v) {
    view = v;
    $$(".views button").forEach((b) => b.setAttribute("aria-selected", String(b.dataset.view === v)));
    $$(".view").forEach((s) => (s.hidden = s.id !== "view-" + v));
    if (v === "settings") fillSettings(true);
    render();
  }
  $$(".views button").forEach((b) => b.addEventListener("click", () => setView(b.dataset.view)));

  // ---------- render ----------
  function render() {
    if (!S) return;
    const r = S.rack;
    $("#rackName").textContent = r.label || "Rack";
    $("#railName").textContent = r.label || "";
    $("#moduleType").textContent = r.module;
    const w = $("#wellness");
    w.textContent = r.wellness;
    w.className = "health " + (r.wellness === "Healthy" ? "ok" : r.wellness === "Sick" ? "bad" : "");
    // the full message is shown under the rack while it is not connected
    const idleMsg = !r.connected && S.port !== "Demo";
    $("#rackMsg").textContent = idleMsg ? "" : r.message || "";
    $("#rackMsg").title = r.message || "";

    const hs = $("#hwStatus");
    hs.textContent = S.status || (S.portError ? S.portError : "");
    hs.className = "hwstatus " + (S.statusKind || (S.portError ? "error" : ""));
    $("#btnPolling").textContent = S.polling ? "Stop polling" : "Start polling";
    $("#pollInfo").textContent = S.pollBusy ? "Polling now…" : S.polling ? `Next poll in ${S.countdown} s` : "Polling stopped";
    syncRecent();

    renderPanel();
    if (view === "hardware") renderRack();
    if (view === "channels") { renderConsole(); markParams(); }
    $("#btnLearn").hidden = !S.midi?.available;
    const u = S.update;
    const ub = $("#btnUpdate");
    ub.hidden = !(u && u.available);
    if (u && u.available) ub.textContent = u.installing ? "Updating…" : `Update ${u.latest}`;
    $("#btnUpdToken").hidden = !(u && u.needsToken && !u.available);
    if (view === "settings") fillSettings(false);
  }

  function renderRack() {
    let h = "";
    for (let i = 0; i < 12; i++) {
      const s = S.slots[i];
      if (!s || s.type === 0) {
        // covered by the second half of a double-width card?
        const prev = S.slots[i - 1];
        if (prev && prev.type !== 0 && prev.width === 2) continue;
        h += `<div class="slot empty" title="Slot ${i + 1}: empty"><span class="num">${i + 1}</span></div>`;
        continue;
      }
      const cls = ["slot", "card", s.width === 2 ? "w2" : "", s.show ? "" : "hidden", s.enabled ? "" : "disabled", s.voltageOK ? "" : "volt"].join(" ");
      const badge = !s.enabled ? "paired" : !s.voltageOK ? "voltage!" : !s.show ? "hidden" : "";
      h += `<div class="${cls}" data-slot="${i}" title="${esc(s.tooltip)}${s.enabled ? "\n\nClick to " + (s.show ? "hide in" : "show in") + " Channels" : ""}">
        <img src="static/img/${s.image}.png" alt="${esc(s.desc)}" draggable="false">
        ${badge ? `<span class="badge">${badge}</span>` : ""}<span class="num">${i + 1}</span></div>`;
    }
    h += `<div class="psu" title="Power supply"></div>`;
    const slots = $("#slots");
    if (slots.dataset.h !== h) { slots.innerHTML = h; slots.dataset.h = h; }
    const idle = !S.rack.connected && S.port !== "Demo";
    $("#startBar").hidden = !idle;
    if (idle) $("#startNote").textContent = S.panel?.idle ? "No rack connected. Try the demo, or connect to your rack."
      : S.rack.message ? S.rack.message.replace(/\.*$/, ".") : "Not connected: the rack does not answer.";

    let l = "";
    for (const s of S.slots) {
      if (s.type === 0) continue;
      const flags = [s.ain || s.aout ? `${s.ain} in / ${s.aout} out` : "", s.i8s ? "I8S" : "", s.voltageOK ? "" : "voltage fail"].filter(Boolean).join(", ");
      l += `<div class="k">${s.acronym} ${s.slot + 1}</div><div class="v">${esc(s.desc)}</div><div class="x">${esc(flags)}</div>`;
    }
    if (!l && S.rack.connected) l = `<div class="k"></div><div class="v">No cards reported by the frame.</div><div></div>`;
    const cl = $("#cardList");
    if (cl.dataset.h !== l) { cl.innerHTML = l; cl.dataset.h = l; }
  }
  $("#slots").addEventListener("click", (e) => {
    const el = e.target.closest(".slot.card");
    if (!el || el.classList.contains("disabled")) return;
    const s = S.slots[+el.dataset.slot];
    op({ op: "show", slot: s.slot, value: !s.show });
  });

  // ---------- channel console ----------
  const L = () => S.limits;
  function faderRange(st) {
    return st.kind === "mic" ? [L().MicMin, L().MicMax, L().MicRange] : [L().DL5Min, L().DL5Max, L().DL5Range];
  }
  const textOn = (hex) => {
    const n = parseInt(hex.slice(1), 16);
    return ((n >> 16) & 255) + ((n >> 8) & 255) + (n & 255) < 0x180 ? "#fff" : "#111";
  };
  const fmtGain = (g) => (g > 0 ? "+" : "") + g;
  const frac = (v, min, max) => (max - v) / (max - min);

  function faderHTML(st, value) {
    const [min, max, lim] = faderRange(st);
    const pos = (v) => `calc(23px + (100% - 46px) * ${frac(v, min, max).toFixed(4)})`;
    let ticks = "";
    const step = Math.floor((max - min) / 10);
    for (let v = min; v <= max + ((max - min) % 10); v += step) {
      const vv = Math.min(v, max);
      ticks += `<span class="tick" style="top:${pos(vv)}">${fmtGain(vv)}</span>`;
      if (vv === max) break;
    }
    return `<div class="fader" data-param="${st.id}.gain" tabindex="0" role="slider" aria-label="Gain" aria-valuemin="${min}" aria-valuemax="${max}" aria-valuenow="${value}" data-min="${min}" data-max="${max}">
      <div class="zone mic" style="top:${pos(max)};height:calc((100% - 46px) * ${(frac(lim, min, max)).toFixed(4)})"><span class="zonelbl" style="top:30%">MIC</span></div>
      <div class="zone line" style="top:${pos(lim)};bottom:23px"><span class="zonelbl" style="top:20%">LINE</span></div>
      <div class="slit" style="top:23px;bottom:23px"></div>${ticks}
      <div class="cap" style="top:${pos(value)}"></div></div>`;
  }

  let cur = null; // strip being rendered (for data-param)
  const tog = (field, on, label, extra = "") => `<button class="tog ${extra}" data-f="${field}"${field === "link" ? "" : ` data-param="${cur.id}.${field}"`} aria-pressed="${!!on}">${label}</button>`;

  function stripBody(st) {
    cur = st;
    const off = !st.enabled;
    switch (st.kind) {
      case "mic": {
        const m = st.mic, g = local.has(st.id) ? local.get(st.id) : m.gain;
        return tog("phantom", m.phantom, "48 V", "ph48") + tog("hpf", m.hpf, "HPF") + tog("insert", m.insert, "Insert") +
          tog("softclip", m.softclip, "Soft clip") + tog("link", st.linked, "S. Link", "link") +
          `<div class="gainread">${fmtGain(g)} <small>dB</small></div>` + faderHTML(st, g) +
          `<button class="gang" aria-pressed="${gang.has(st.id) && !off}" title="Gang: click to gang this channel alone, ⌘-click to add or remove, ⇧-click for a range">Gang</button>`;
      }
      case "dl5": {
        const m = st.dl5, g = local.has(st.id) ? local.get(st.id) : m.gain;
        return tog("phantom", m.phantom, "48 V", "ph48") + tog("hpf", m.hpf, "HPF") + tog("dcs", m.dcs, "DC servo") + tog("cms", m.cms, "CM servo") +
          tog("link", st.linked, "S. Link", "link") +
          `<div class="gainread">${fmtGain(g)} <small>dB</small></div>` + faderHTML(st, g) +
          `<button class="gang" aria-pressed="${gang.has(st.id) && !off}" title="Gang: click to gang this channel alone, ⌘-click to add or remove, ⇧-click for a range">Gang</button>`;
      }
      case "aes": {
        const a = st.aes;
        let h = `<div class="grp">Lock</div><div class="leds">`;
        a.lock.forEach((on, i) => (h += `<span class="led ${on ? "on" : ""}"></span><span>in ${i + 1}</span>`));
        if (a.hasOut) h += `<span class="led ${a.extLock ? "on" : ""}"></span><span>ext.</span>`;
        h += `</div>`;
        if (a.hasSfcIn) {
          h += `<div class="grp">SFC</div><div class="checks">`;
          a.sfcIn.forEach((on, i) => (h += `<label><input type="checkbox" data-aes="sfcIn" data-i="${i}" data-param="${st.id}.sfcIn${i + 1}" ${on ? "checked" : ""}>in ${i + 1}</label>`));
          if (a.hasOut) a.sfcOut.forEach((on, i) => (h += `<label><input type="checkbox" data-aes="sfcOut" data-i="${i}" data-param="${st.id}.sfcOut${i + 1}" ${on ? "checked" : ""}>out ${i ? "5-8" : "1-4"}</label>`));
          h += `</div>`;
        }
        if (a.hasOut) {
          const left = h;
          let r = `<div class="grp">FS out</div><div class="radiogrid" style="grid-template-columns:26px repeat(4,1fr)"><span></span>${L().FsOut.map((n) => `<span class="h">${n}</span>`).join("")}`;
          a.fsOut.forEach((v, g) => {
            r += `<span class="r">${g ? "5-8" : "1-4"}</span>`;
            for (let k = 0; k < 4; k++) r += `<input type="radio" name="fs-${st.id}-${g}" data-aes="fsOut" data-i="${g}" data-param="${st.id}.fs${g + 1}" data-v="${k}" ${v === k ? "checked" : ""} aria-label="out ${g ? "5-8" : "1-4"} ${L().FsOut[k]}">`;
          });
          r += `</div><div class="grp">Out word length</div><div class="radiogrid" style="grid-template-columns:26px repeat(4,1fr)"><span></span>${L().Wordlength.map((n) => `<span class="h">${n.replace("-bit", "")}</span>`).join("")}`;
          a.wordlength.forEach((v, ch) => {
            r += `<span class="r">ch ${ch + 1}</span>`;
            for (let k = 0; k < 4; k++) r += `<input type="radio" name="wl-${st.id}-${ch}" data-aes="wordlength" data-i="${ch}" data-param="${st.id}.wl${ch + 1}" data-v="${k}" ${v === k ? "checked" : ""} aria-label="ch ${ch + 1} ${L().Wordlength[k]}">`;
          });
          r += `</div>`;
          return `<div class="aescols"><div>${left}</div><div>${r}</div></div>`;
        }
        return h;
      }
      case "adat": {
        const a = st.adat;
        let h = `<div class="grp">Lock</div><div class="leds"><span class="led ${a.lock[0] ? "on" : ""}"></span><span>ch 1-8</span>`;
        if (a.twoBanks) h += `<span class="led ${a.lock[1] ? "on" : ""}"></span><span>ch 9-16</span>`;
        return h + `</div>`;
      }
      case "gpio": {
        const g = st.gpio;
        let h = `<div class="grp">GPI</div><div class="gpgrid">`;
        g.gpi.forEach((on, i) => (h += (i === 8 ? `<span class="gap"></span>` : "") + `<span class="gpi ${on ? "on" : ""}" title="GPI ${i + 1} ${on ? "active" : "inactive"}">${i + 1}</span>`));
        h += `</div><div class="grp">GPO</div><div class="gpgrid">`;
        g.gpo.forEach((on, i) => (h += (i === 8 ? `<span class="gap"></span>` : "") + `<button class="gpo" data-gpo="${i}" data-param="${st.id}.gpo${i + 1}" aria-pressed="${on}" title="GPO ${i + 1}">${i + 1}</button>`));
        return h + `</div>`;
      }
      case "generic": {
        const g = st.generic;
        let h = `<div class="gen"><dl><dt>ID</dt><dd>${g.id}</dd><dt>V.</dt><dd>${g.voltage}</dd><dt>IN</dt><dd>${g.ain}</dd><dt>OUT</dt><dd>${g.aout}</dd><dt>CT</dt><dd>${g.ct}</dd><dt>Stat.</dt><dd>${g.status.length}</dd><dt>Cmd</dt><dd>${g.command.length}</dd></dl>`;
        const n = Math.max(g.command.length, g.status.length);
        if (n) h += `<div class="grp">Status, command</div>`;
        for (let i = 0; i < n; i++) {
          h += `<div class="hexrow"><span>${i + 1}</span>${i < g.status.length ? `<span class="st">${g.status[i]}</span>` : "<span></span>"}` +
            (i < g.command.length ? `<input maxlength="2" data-cmd="${i}" value="${g.command[i]}" aria-label="Command byte ${i + 1}">` : "<span></span>") + `</div>`;
        }
        return h + `</div>`;
      }
    }
    return "";
  }

  function stripInner(st) {
    const lbl = st.label ? esc(st.label) : `<span class="ph">${esc(st.fixed)}</span>`;
    return `<div class="shead" title="${esc(st.tooltip)}"><div class="fx">${esc(st.fixed)}</div><div class="ids">${esc(st.ids)}</div></div>
      <div class="sbody">${stripBody(st)}</div>
      <div class="scribble ${st.label.length > 10 ? "small" : ""}" draggable="true" title="Double-click to rename, drag to move">${lbl}</div>
      <div class="cmt" tabindex="0" title="Comment. Click to edit; the 21 Channel plug-in shows it too">${st.comment ? esc(st.comment) : `<span class="ph">Comment</span>`}</div>`;
  }

  function stripColors(el, st) {
    let c = st.color;
    if (!st.enabled) { // linked channels are darkened to 2/3
      const n = parseInt(c.slice(1), 16);
      c = "#" + [16, 8, 0].map((s) => Math.floor((((n >> s) & 255) / 3) * 2).toString(16).padStart(2, "0")).join("");
    }
    el.style.setProperty("--c", c);
    el.style.setProperty("--on", textOn(c));
  }

  function renderConsole() {
    const con = $("#console");
    const vis = S.strips.filter((s) => s.visible);
    $("#consoleEmpty").hidden = vis.length > 0;
    const seen = new Set();
    let prev = null;
    for (const st of vis) {
      seen.add(st.id);
      let el = con.querySelector(`[data-id="${st.id}"]`);
      if (!el) {
        el = document.createElement("div");
        el.dataset.id = st.id;
        stripHTML.delete(st.id);
      }
      el.className = `strip ${st.kind} ${st.enabled ? "" : "disabled"} ${st.kind === "aes" && st.aes.hasOut ? "wide" : ""}`;
      stripColors(el, st);
      if (!busy.has(st.id)) {
        const html = stripInner(st);
        if (stripHTML.get(st.id) !== html) {
          const focusedFader = document.activeElement?.closest?.(".strip") === el && document.activeElement.classList.contains("fader");
          el.innerHTML = html;
          stripHTML.set(st.id, html);
          if (focusedFader) $(".fader", el)?.focus({ preventScroll: true });
        }
      }
      const want = prev ? prev.nextSibling : con.firstChild;
      if (want !== el) con.insertBefore(el, want);
      prev = el;
    }
    $$(".strip", con).forEach((el) => { if (!seen.has(el.dataset.id)) { el.remove(); stripHTML.delete(el.dataset.id); } });
  }
  const strip = (id) => S.strips.find((s) => s.id === id);

  // controls
  const con = $("#console");

  // ---------- MIDI learn ----------
  let learnMode = false;
  function setLearn(on) {
    learnMode = on;
    document.body.classList.toggle("learning", on);
    $("#btnLearn").setAttribute("aria-pressed", String(on));
    if (!on && S?.midi?.learning) op({ op: "midiLearnCancel" });
    if (on && view !== "channels") setView("channels");
    markParams();
  }
  function markParams() {
    if (!S?.midi) return;
    const mapped = new Set(S.midi.mapped || []);
    $$("[data-param]", con).forEach((el) => {
      el.classList.toggle("mapped", mapped.has(el.dataset.param));
      el.classList.toggle("learn-target", learnMode && S.midi.learning === el.dataset.param);
    });
    const hint = $("#learnHint");
    if (!learnMode) hint.hidden = true;
    else {
      hint.hidden = false;
      hint.textContent = S.midi.learning
        ? "Now move a knob, fader or button on your MIDI controller (or send from your DAW)…"
        : "MIDI Learn: click a fader, button or switch, then move the control on your MIDI device. Press Esc when done.";
    }
  }
  function learnPick(e) {
    if (!learnMode) return false;
    const el = e.target.closest("[data-param]");
    e.preventDefault();
    e.stopPropagation();
    if (el) op({ op: "midiLearn", text: el.dataset.param });
    return true;
  }
  con.addEventListener("pointerdown", (e) => { if (learnMode) learnPick(e); }, true);
  con.addEventListener("click", (e) => { if (learnMode) { e.preventDefault(); e.stopPropagation(); } }, true);
  con.addEventListener("change", (e) => { if (learnMode) e.stopPropagation(); }, true);
  $("#btnLearn").onclick = () => setLearn(!learnMode);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && learnMode) setLearn(false); });
  con.addEventListener("click", (e) => {
    const el = e.target.closest(".strip");
    if (!el) return;
    const st = strip(el.dataset.id);
    const t = e.target;
    if (t.matches(".tog")) {
      const f = t.dataset.f, val = t.getAttribute("aria-pressed") !== "true";
      if (f === "link") op({ op: "link", strip: st.id, value: val });
      else op({ op: st.kind, strip: st.id, field: f, value: val });
    } else if (t.matches(".gang")) {
      gangClick(st, e);
      $(".fader", el)?.focus();
    } else if (t.matches(".gpo")) {
      op({ op: "gpo", slot: st.slot, index: +t.dataset.gpo, value: t.getAttribute("aria-pressed") !== "true" });
    }
  });
  con.addEventListener("change", (e) => {
    const t = e.target, el = t.closest(".strip");
    if (!el || !t.dataset.aes) return;
    const st = strip(el.dataset.id);
    const value = t.type === "radio" ? +t.dataset.v : t.checked;
    op({ op: "aes", slot: st.slot, field: t.dataset.aes, index: +t.dataset.i, value });
  });
  // generic command bytes (hex, like GenericComponent)
  con.addEventListener("focusin", (e) => { if (e.target.dataset?.cmd) { busy.add(e.target.closest(".strip").dataset.id); e.target.select(); } });
  con.addEventListener("focusout", (e) => {
    const t = e.target;
    if (!t.dataset?.cmd) return;
    const el = t.closest(".strip"), st = strip(el.dataset.id);
    busy.delete(st.id);
    const v = parseInt(t.value || "0", 16);
    op({ op: "cmdByte", slot: st.slot, index: +t.dataset.cmd, value: isNaN(v) ? 0 : v });
  });
  con.addEventListener("keydown", (e) => {
    const t = e.target;
    if (t.dataset?.cmd) {
      if (e.key === "Enter") {
        const ins = $$("input[data-cmd]", t.closest(".strip"));
        const nxt = ins[(ins.indexOf(t) + 1) % ins.length];
        nxt === t ? t.blur() : nxt.focus();
        e.preventDefault();
      } else if (e.key.length === 1 && !/[0-9a-f]/i.test(e.key) && !e.metaKey && !e.ctrlKey) e.preventDefault();
      return;
    }
    if (t.classList.contains("fader")) faderKey(e, t);
  });

  // ---------- faders ----------
  let sendTimer = null, sendQueue = new Map();
  function queueGain(id, kind, value) {
    sendQueue.set(id, { op: kind, strip: id, field: "gain", value });
    if (!sendTimer) sendTimer = setTimeout(() => {
      const ops = [...sendQueue.values()];
      sendQueue.clear(); sendTimer = null;
      op(ops.length === 1 ? ops[0] : { op: "batch", ops });
    }, 30);
  }
  function currentGain(st) { return local.has(st.id) ? local.get(st.id) : (st.mic || st.dl5).gain; }
  function setLocal(st, v) {
    const [min, max] = faderRange(st);
    v = Math.max(min, Math.min(max, Math.round(v)));
    local.set(st.id, v);
    const el = con.querySelector(`[data-id="${st.id}"]`);
    if (el) {
      const cap = $(".cap", el), rd = $(".gainread", el), f = $(".fader", el);
      if (cap) cap.style.top = `calc(23px + (100% - 46px) * ${frac(v, min, max).toFixed(4)})`;
      if (rd) rd.innerHTML = `${fmtGain(v)} <small>dB</small>`;
      if (f) f.setAttribute("aria-valuenow", v);
    }
    queueGain(st.id, st.kind, v);
    return v;
  }
  // move a fader; ganged channels follow by the same relative amount
  function moveFader(st, target) {
    const before = currentGain(st);
    const after = setLocal(st, target);
    const delta = after - before;
    if (!delta || !gang.has(st.id)) return;
    for (const id of gang) {
      if (id === st.id) continue;
      const o = strip(id);
      if (o && o.enabled && (o.mic || o.dl5)) setLocal(o, currentGain(o) + delta);
    }
  }
  function settle(ids) {
    setTimeout(() => { ids.forEach((id) => { local.delete(id); busy.delete(id); }); render(); }, 250);
  }
  function faderKey(e, f) {
    const st = strip(f.closest(".strip").dataset.id);
    const [min, max] = faderRange(st);
    const step = S.faderStep || 2, g = currentGain(st);
    let v = null;
    switch (e.key) {
      case "ArrowUp": v = g + step; break;
      case "ArrowDown": v = g - step; break;
      case "PageUp": v = g + step * 5; break;
      case "PageDown": v = g - step * 5; break;
      case "Home": v = max; break;
      case "End": v = min; break;
      case "ArrowLeft": case "ArrowRight": {
        const fs = $$(".strip:not(.disabled) .fader", con);
        const i = fs.indexOf(f) + (e.key === "ArrowLeft" ? -1 : 1);
        if (fs[i]) fs[i].focus();
        e.preventDefault();
        return;
      }
      default: return;
    }
    e.preventDefault();
    const ids = [st.id, ...gang];
    ids.forEach((id) => busy.add(id));
    moveFader(st, v);
    settle(ids);
  }
  con.addEventListener("pointerdown", (e) => {
    const f = e.target.closest(".fader");
    if (!f || e.button !== 0) return;
    const el = f.closest(".strip"), st = strip(el.dataset.id);
    if (!st.enabled) return;
    e.preventDefault();
    f.focus();
    const [min, max] = faderRange(st);
    const rect = f.getBoundingClientRect();
    const usable = rect.height - 46;
    const ids = [st.id, ...gang];
    ids.forEach((id) => busy.add(id));
    let startY = e.clientY, startV = currentGain(st);
    if (!e.target.classList.contains("cap")) {
      // click on the track jumps there
      startV = max - ((e.clientY - rect.top - 23) / usable) * (max - min);
      moveFader(st, startV);
      startV = currentGain(st);
    }
    f.classList.add("dragging");
    f.setPointerCapture(e.pointerId);
    const mv = (ev) => moveFader(st, startV + ((startY - ev.clientY) / usable) * (max - min));
    const up = () => {
      f.classList.remove("dragging");
      f.removeEventListener("pointermove", mv);
      f.removeEventListener("pointerup", up);
      f.removeEventListener("pointercancel", up);
      settle(ids);
    };
    f.addEventListener("pointermove", mv);
    f.addEventListener("pointerup", up);
    f.addEventListener("pointercancel", up);
  });
  con.addEventListener("wheel", (e) => {
    const f = e.target.closest(".fader");
    if (!f) return;
    const st = strip(f.closest(".strip").dataset.id);
    if (!st.enabled) return;
    e.preventDefault();
    const ids = [st.id, ...gang];
    ids.forEach((id) => busy.add(id));
    moveFader(st, currentGain(st) + (e.deltaY < 0 ? 1 : -1));
    settle(ids);
  }, { passive: false });

  function gangClick(st, e) {
    if (!st.enabled) return;
    const order = S.strips.filter((s) => s.visible && (s.mic || s.dl5) && s.enabled).map((s) => s.id);
    if (e.shiftKey && lastGangAnchor) {
      let a = order.indexOf(lastGangAnchor), b = order.indexOf(st.id);
      if (a > b) [a, b] = [b, a];
      for (let i = a; i <= b; i++) gang.add(order[i]);
    } else if (e.metaKey || e.ctrlKey) {
      gang.has(st.id) ? gang.delete(st.id) : gang.add(st.id);
      lastGangAnchor = st.id;
    } else {
      const only = gang.size === 1 && gang.has(st.id);
      gang.clear();
      if (!only) gang.add(st.id);
      lastGangAnchor = st.id;
    }
    stripHTML.clear();
    render();
  }

  // ---------- labels, colours, ordering ----------
  function editLabel(st) {
    const el = con.querySelector(`[data-id="${st.id}"] .scribble`);
    if (!el) return;
    busy.add(st.id);
    el.innerHTML = `<input type="text" value="${esc(st.label)}" aria-label="Channel label">`;
    const inp = $("input", el);
    inp.focus(); inp.select();
    let done = false;
    const finish = (commit) => {
      if (done) return;
      done = true;
      busy.delete(st.id);
      stripHTML.delete(st.id);
      if (commit && inp.value !== st.label) op({ op: "label", strip: st.id, text: inp.value });
      else render();
    };
    inp.addEventListener("keydown", (e) => {
      if (e.key === "Enter") finish(true);
      if (e.key === "Escape") finish(false);
      e.stopPropagation();
    });
    inp.addEventListener("blur", () => finish(true));
  }
  function editComment(st) {
    const el = con.querySelector(`[data-id="${st.id}"] .cmt`);
    if (!el || el.querySelector("textarea")) return;
    busy.add(st.id);
    el.innerHTML = `<textarea aria-label="Comment" placeholder="Comment">${esc(st.comment)}</textarea>`;
    const ta = $("textarea", el);
    ta.focus(); ta.setSelectionRange(ta.value.length, ta.value.length);
    let done = false;
    const finish = (commit) => {
      if (done) return;
      done = true;
      busy.delete(st.id);
      stripHTML.delete(st.id);
      if (commit && ta.value !== st.comment) op({ op: "comment", strip: st.id, text: ta.value });
      else render();
    };
    ta.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); finish(true); }
      if (e.key === "Escape") finish(false);
      e.stopPropagation();
    });
    ta.addEventListener("blur", () => finish(true));
  }
  con.addEventListener("click", (e) => {
    const c = e.target.closest(".cmt");
    if (c && !learnMode) editComment(strip(c.closest(".strip").dataset.id));
  });
  con.addEventListener("keydown", (e) => {
    if (e.target.classList?.contains("cmt") && e.key === "Enter") { e.preventDefault(); editComment(strip(e.target.closest(".strip").dataset.id)); }
  });
  con.addEventListener("dblclick", (e) => {
    const sc = e.target.closest(".scribble");
    if (sc && !sc.querySelector("input")) editLabel(strip(sc.closest(".strip").dataset.id));
  });
  // colour palette: 7 colours (columns) x 4 shades (rows)
  const hsl = (h, s, l) => {
    s /= 100; l /= 100;
    const f = (n) => { const k = (n + h / 30) % 12, a = s * Math.min(l, 1 - l); return Math.round(255 * (l - a * Math.max(-1, Math.min(k - 3, 9 - k, 1)))); };
    return "#" + [f(0), f(8), f(4)].map((x) => x.toString(16).padStart(2, "0")).join("");
  };
  const HUES = [["Red", 0, 72], ["Orange", 26, 85], ["Yellow", 50, 88], ["Green", 128, 50], ["Cyan", 188, 65], ["Blue", 218, 70], ["Purple", 275, 55]];
  const SHADES = [[78, "light"], [62, ""], [46, "dark"], [32, "deep"]];
  const PALETTE = SHADES.map(([l, n]) => HUES.map(([name, h, s]) => ({ hex: hsl(h, s, l), name: (n ? n + " " : "") + name.toLowerCase() })));
  const pal = $("#palette");
  function changeColor(st, x, y) {
    let h = `<div class="palgrid">`;
    for (const row of PALETTE) for (const c of row)
      h += `<button class="swatch ${c.hex === st.color ? "cur" : ""}" style="background:${c.hex}" data-c="${c.hex}" title="${c.name}" aria-label="${c.name}"></button>`;
    h += `</div><button class="paldefault" data-c="">Default colour</button>`;
    pal.innerHTML = h;
    pal.dataset.strip = st.id;
    pal.hidden = false;
    const w = pal.offsetWidth, hh = pal.offsetHeight;
    pal.style.left = Math.max(6, Math.min(x, innerWidth - w - 6)) + "px";
    pal.style.top = Math.max(6, Math.min(y, innerHeight - hh - 6)) + "px";
    (pal.querySelector(".swatch.cur") || pal.querySelector(".swatch")).focus();
  }
  pal.addEventListener("click", (e) => {
    const b = e.target.closest("[data-c]");
    if (!b) return;
    pal.hidden = true;
    op({ op: "color", strip: pal.dataset.strip, text: b.dataset.c });
  });
  pal.addEventListener("keydown", (e) => {
    const sw = $$(".swatch", pal), i = sw.indexOf(document.activeElement);
    const mv = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }[e.key];
    if (mv && i >= 0) { e.preventDefault(); sw[Math.max(0, Math.min(sw.length - 1, i + mv))].focus(); }
    if (e.key === "Escape") pal.hidden = true;
  });
  document.addEventListener("pointerdown", (e) => { if (!pal.hidden && !e.target.closest("#palette")) pal.hidden = true; });

  let dragId = null;
  con.addEventListener("dragstart", (e) => {
    const sc = e.target.closest?.(".scribble");
    if (!sc) return;
    dragId = sc.closest(".strip").dataset.id;
    e.dataTransfer.setData("text/plain", "Move_" + dragId);
    e.dataTransfer.effectAllowed = "move";
  });
  const clearDrag = () => $$(".dragover-l,.dragover-r", con).forEach((x) => x.classList.remove("dragover-l", "dragover-r"));
  con.addEventListener("dragover", (e) => {
    const el = e.target.closest(".strip");
    if (!el || !dragId) return;
    e.preventDefault();
    const r = el.getBoundingClientRect(), before = e.clientX < r.left + r.width / 2;
    clearDrag();
    if (el.dataset.id !== dragId) el.classList.add(before ? "dragover-l" : "dragover-r");
  });
  con.addEventListener("dragleave", (e) => { if (!con.contains(e.relatedTarget)) clearDrag(); });
  con.addEventListener("drop", (e) => {
    const el = e.target.closest(".strip");
    clearDrag();
    if (!el || !dragId) return;
    e.preventDefault();
    const r = el.getBoundingClientRect();
    if (el.dataset.id !== dragId) op({ op: "moveTo", strip: dragId, target: el.dataset.id, before: e.clientX < r.left + r.width / 2 });
    dragId = null;
  });
  con.addEventListener("dragend", () => { dragId = null; clearDrag(); });

  // ---------- context menu ----------
  const ctx = $("#ctxMenu");
  function showMenu(menu, x, y, items) {
    menu.innerHTML = "";
    for (const it of items) {
      if (it === "-") { const s = document.createElement("div"); s.className = "sep"; menu.appendChild(s); continue; }
      const b = document.createElement("button");
      b.textContent = it.label;
      b.disabled = !!it.disabled;
      b.setAttribute("role", "menuitem");
      b.onclick = () => { hideMenus(); it.run(); };
      menu.appendChild(b);
    }
    menu.hidden = false;
    if (menu === ctx) {
      const w = menu.offsetWidth, h = menu.offsetHeight;
      menu.style.left = Math.min(x, innerWidth - w - 6) + "px";
      menu.style.top = Math.min(y, innerHeight - h - 6) + "px";
    }
    menu.querySelector("button:not(:disabled)")?.focus();
  }
  function hideMenus() { ctx.hidden = true; }
  document.addEventListener("pointerdown", (e) => { if (!e.target.closest(".menu")) hideMenus(); });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") hideMenus(); });
  con.addEventListener("contextmenu", (e) => {
    const el = e.target.closest(".strip");
    if (!el) return;
    e.preventDefault();
    const st = strip(el.dataset.id);
    const items = [];
    if (st.kind === "mic" || st.kind === "dl5") items.push({ label: "Reset mic gain", disabled: !st.enabled, run: () => op({ op: "resetGain", strip: st.id }) }, "-");
    if (st.kind === "gpio") items.push(
      { label: "Set all GPOs", run: () => op({ op: "gpoAll", slot: st.slot, text: "set" }) },
      { label: "Clear all GPOs", run: () => op({ op: "gpoAll", slot: st.slot, text: "clear" }) },
      { label: "Invert all GPOs", run: () => op({ op: "gpoAll", slot: st.slot, text: "invert" }) }, "-");
    if (S.midi?.available && (st.kind === "mic" || st.kind === "dl5")) {
      const pid = st.id + ".gain";
      items.push({ label: "MIDI Learn gain…", disabled: !st.enabled, run: () => { setLearn(true); op({ op: "midiLearn", text: pid }); } });
      const mine = (S.midi.mappings || []).some((m) => m.param.startsWith(st.id + "."));
      items.push({ label: "Remove MIDI assignments", disabled: !mine, run: async () => {
        for (const f of ["gain", "phantom", "hpf", "insert", "softclip", "dcs", "cms"]) await op({ op: "midiDeleteParam", text: st.id + "." + f });
      } }, "-");
    }
    items.push(
      { label: "Edit label", run: () => editLabel(st) },
      { label: "Edit comment", run: () => editComment(st) },
      { label: "Change colour…", run: () => changeColor(st, e.clientX, e.clientY) }, "-",
      { label: "Move left", run: () => op({ op: "move", strip: st.id, delta: -1 }) },
      { label: "Move right", run: () => op({ op: "move", strip: st.id, delta: 1 }) }, "-",
      { label: "Hide card", run: () => op({ op: "hide", strip: st.id }) });
    showMenu(ctx, e.clientX, e.clientY, items);
  });

  // ---------- top bar & status bar ----------
  $("#rackName").addEventListener("click", async () => {
    const v = await promptBox("Rack name", S.rack.label);
    if (v !== null) op({ op: "rackLabel", text: v });
  });
  $("#btnOpen").onclick = () => openSnapshot();
  $("#btnSave").onclick = saveSnapshot;
  async function newSession() {
    const r = await dialogBox(`<h2>Start a new session?</h2><p>All microphone channels go to minimum gain with 48 V, HPF, insert and soft clip off. Channel names, comments and colours, the channel order, hidden cards, stereo links and the rack name go back to their defaults. You can undo this with ⌘Z.</p><p>Do you want to save the current session first?</p>`,
      [{ label: "Cancel", value: "" }, { label: "Don’t Save", value: "go" }, { label: "Save…", primary: true, value: "save" }]);
    if (!r) return;
    if (r === "save" && !(await saveSnapshot())) return;
    op({ op: "newSession" });
  }
  $("#btnNew").onclick = newSession;
  // recent sessions live in the File menu of the app (newest first)
  let recentSig = "";
  function syncRecent() {
    const nh = window.webkit?.messageHandlers?.native;
    const names = [...(S.recentNames || [])].reverse();
    const sig = names.join("\n") + "|" + (S.recent || []).join("\n");
    if (!nh || sig === recentSig) return;
    recentSig = sig;
    nh.postMessage({ cmd: "recent", names });
  }
  const openRecent = (i) => { const p = [...(S.recent || [])].reverse()[i]; if (p) openSnapshot(p); };
  $("#btnPollNow").onclick = () => op({ op: "pollNow" });
  $("#btnPolling").onclick = () => op({ op: "polling", value: !S.polling });

  // ---------- settings ----------
  const isNet = (p) => p.startsWith("tcp://") || p.startsWith("tcp-listen://");
  function modeOf(p) { return !p ? "serial" : p === "Demo" ? "demo" : p.startsWith("tcp-listen://") ? "listen" : p.startsWith("tcp://") ? "tcp" : "serial"; }
  function showMode(m) { $$(".mode").forEach((d) => (d.hidden = d.dataset.mode !== m)); }
  function fillSettings(force) {
    const port = $("#setPort"), poll = $("#setPoll"), step = $("#setStep");
    const form = $("#settingsForm");
    if (force || !form.dataset.dirty) {
      const serials = S.ports.filter((p) => p !== "Demo" && !isNet(p));
      const opts = serials.length
        ? serials.map((p) => `<option value="${esc(p)}">${esc(p.replace("/dev/", ""))}</option>`).join("")
        : `<option value="">No serial ports found</option>`;
      if (port.dataset.h !== opts) { port.innerHTML = opts; port.dataset.h = opts; }
      const mode = modeOf(S.port);
      $("#setMode").value = mode;
      showMode(mode);
      if (mode === "serial") port.value = S.port;
      if (mode === "tcp") {
        const hp = S.port.slice(6), i = hp.lastIndexOf(":");
        $("#setHost").value = hp.slice(0, i); $("#setTcpPort").value = hp.slice(i + 1);
      }
      if (mode === "listen") $("#setListenPort").value = S.port.slice(13).replace(/^.*:/, "");
      if (!poll.options.length) poll.innerHTML = S.intervals.map((v) => `<option value="${v}">${v < 60 ? v + " seconds" : v / 60 + (v === 60 ? " minute" : " minutes")}</option>`).join("");
      poll.value = S.pollInterval;
      if (!step.options.length) step.innerHTML = Array.from({ length: 10 }, (_, i) => `<option value="${i + 1}">${i + 1} dB</option>`).join("");
      step.value = S.faderStep;
    }
    $("#myIPs").textContent = S.localIPs?.length ? S.localIPs.join(" or ") : "this Mac’s address";
    const ps = $("#portStatus");
    ps.textContent = S.portStatus || "";
    ps.className = "note portstatus " + (S.portStatus?.startsWith("Connected") ? "ok" : S.portStatus ? "wait" : "");
    ps.hidden = $("#setMode").value !== modeOf(S.port);
    $("#setTrace").checked = S.trace;
    fillMIDI();
    fillUpdate();
  }
  function fillUpdate() {
    const u = S.update;
    if (!u) return;
    $("#updCurrent").textContent = `This is version ${u.current}.`;
    let st = "";
    if (u.checking) st = "Checking…";
    else if (u.error) st = u.error;
    else if (u.available) st = `Version ${u.latest} is available${u.published ? " (" + u.published + ")" : ""}.`;
    else if (u.latest) st = `Up to date (latest is ${u.latest}, checked ${u.checked}).`;
    else st = "Not checked yet.";
    const se = $("#updStatus");
    se.textContent = st;
    se.style.color = u.error ? "var(--red)" : u.available ? "var(--led)" : "";
    $("#updInstall").disabled = !u.canInstall || u.installing || u.checking;
    $("#updInstall").textContent = u.available ? `Update to ${u.latest}` : "Update now";
    $("#updCheck").disabled = u.checking || u.installing;
    $("#updAuto").checked = u.auto;
    $("#updProgress").textContent = u.progress || (u.available && !u.canInstall ? "Updates install from the 21 Remote app. Here, download the installer from GitHub instead." : "");
    $("#updRepo").textContent = u.repo;
    $("#updNotesBox").hidden = !(u.available && u.notes);
    $("#updNotes").textContent = u.notes || "";
    const tb = $("#updTokenBox");
    if (!tb.dataset.init) { tb.open = !!u.needsToken; tb.dataset.init = "1"; }
    $("#updToken").placeholder = u.hasToken ? "Token saved. Paste a new one, or save empty to remove it" : "Not needed for a public repository";
  }
  async function updPost(path, body) {
    try { await post("api/update/" + path, body || {}); } catch (e) { alertBox("Update", e.message); }
  }
  $("#updCheck").onclick = () => updPost("check");
  $("#updAuto").onchange = (e) => updPost("auto", { on: e.target.checked });
  $("#updTokenSave").onclick = async () => {
    const t = $("#updToken").value.trim();
    await updPost("token", { token: t });
    $("#updToken").value = "";
  };
  $("#updInstall").onclick = async () => {
    const u = S.update;
    const r = await dialogBox(`<h2>Update to ${esc(u.latest)}?</h2><p>21 Remote downloads the installer, macOS asks for an administrator password, and the app and the plug-ins are installed. Then the app restarts.${
      u.notes ? "</p><pre class=\"dlgnotes\">" + esc(u.notes) + "</pre><p>" : ""} Restart your DAW afterwards so it loads the new plug-ins.</p>`,
      [{ label: "Cancel", value: "" }, { label: "Update and restart", primary: true, value: "ok" }]);
    if (r === "ok") updPost("install");
  };
  $("#btnUpdToken").onclick = () => { setView("settings"); $("#updTokenBox").open = true; setTimeout(() => $("#updBox").scrollIntoView({ behavior: "smooth", block: "start" }), 50); };
  $("#btnUpdate").onclick = () => { setView("settings"); setTimeout(() => $("#updBox").scrollIntoView({ behavior: "smooth", block: "start" }), 50); };

  $("#permBox").addEventListener("click", async (e) => {
    const b = e.target.closest("button");
    if (!b) return;
    try {
      if (b.dataset.open) { await post("api/system", { action: "open", pane: b.dataset.open }); return; }
      b.disabled = true;
      const res = $(b.dataset.perm === "network" ? "#permNetRes" : "#permFilesRes");
      res.textContent = "Asking…";
      const j = await post("api/system", { action: b.dataset.perm });
      res.textContent = j.message || "";
    } catch (err) { alertBox("Permissions", err.message); } finally { b.disabled = false; }
  });

  const modeNames = { abs: "Absolute", rel: "Relative (encoder)", toggle: "Toggle on press", direct: "Follow (on while held / ≥64)", cycle: "Next value on press" };
  function fillMIDI() {
    const m = S.midi;
    if (!m) return;
    const st = $("#midiStatus");
    st.textContent = m.status;
    st.className = "note portstatus " + (m.available ? "ok" : "wait");
    $(".midi-on").hidden = !m.available;
    if (!m.available) return;
    $("#midiVirtual").checked = m.virtual;
    $("#midiFeedback").checked = m.feedback;
    const ins = (m.inputs || []).map((d) => d.name), outs = (m.outputs || []).map((d) => d.name);
    $("#midiDevices").textContent = ins.length || outs.length
      ? `Inputs: ${ins.join(", ") || "none"}. Outputs: ${outs.join(", ") || "none"}.`
      : "No MIDI devices found. Connect a controller or enable the IAC driver / network MIDI in Audio MIDI Setup.";
    let t = "";
    if (m.mappings?.length) {
      t = `<tr><th>Control</th><th>MIDI</th><th>From</th><th>Mode</th><th></th></tr>`;
      for (const x of m.mappings) {
        const opts = x.modes.map((md) => `<option value="${md}" ${md === x.mode ? "selected" : ""}>${modeNames[md] || md}</option>`).join("");
        t += `<tr><td class="${x.absent ? "absent" : ""}">${esc(x.label)}</td><td>${esc(x.msg)}</td><td>${esc(x.device)}</td>
          <td>${x.modes.length > 1 ? `<select data-map="${x.index}" aria-label="Mode">${opts}</select>` : esc(modeNames[x.mode] || x.mode)}</td>
          <td><button type="button" data-unmap="${x.index}" title="Remove this assignment">Remove</button></td></tr>`;
      }
      t += `<tr><td colspan="5"><button type="button" id="midiClear">Remove all assignments</button></td></tr>`;
    } else t = `<tr><td class="absent">No controls assigned yet. Use MIDI Learn: click a control in Channels, then move a knob on your controller.</td></tr>`;
    const mt = $("#mapTable");
    if (mt.dataset.h !== t && !mt.contains(document.activeElement)) { mt.innerHTML = t; mt.dataset.h = t; }
    const tt = $("#translatorTable");
    if (!tt.dataset.done) {
      tt.innerHTML = `<tr><th>Card</th><th>CC</th><th>Controls</th><th>Values</th></tr>` +
        m.translator.map((r) => `<tr><td>${esc(r.card)}</td><td>${esc(r.cc)}</td><td>${esc(r.what)}</td><td>${esc(r.value)}</td></tr>`).join("");
      tt.dataset.done = "1";
    }
  }
  $("#midiVirtual").onchange = (e) => op({ op: "midiVirtual", value: e.target.checked });
  $("#midiFeedback").onchange = (e) => op({ op: "midiFeedback", value: e.target.checked });
  $("#midiDump").onclick = () => op({ op: "midiDump" });
  $("#midiRescan").onclick = () => op({ op: "midiRescan" });
  $("#midiLearnBtn").onclick = () => setLearn(true);
  $("#mapTable").addEventListener("change", (e) => {
    if (e.target.dataset.map) op({ op: "midiMode", index: +e.target.dataset.map, text: e.target.value });
  });
  $("#mapTable").addEventListener("click", async (e) => {
    if (e.target.dataset.unmap) op({ op: "midiDelete", index: +e.target.dataset.unmap });
    if (e.target.id === "midiClear") {
      const r = await dialogBox("<h2>Remove all MIDI assignments?</h2><p>The “21 Remote” port map for DAWs is not affected.</p>", [{ label: "Cancel", value: "" }, { label: "Remove all", primary: true, value: "ok" }]);
      if (r === "ok") op({ op: "midiClear" });
    }
  });
  $("#setMode").addEventListener("change", (e) => {
    showMode(e.target.value);
    $("#portStatus").hidden = e.target.value !== modeOf(S.port);
    if (e.target.value === "tcp" && !$("#setTcpPort").value) $("#setTcpPort").value = 4001;
    if (e.target.value === "listen" && !$("#setListenPort").value) $("#setListenPort").value = 4001;
  });
  function chosenPort() {
    switch ($("#setMode").value) {
      case "demo": return "Demo";
      case "serial": return $("#setPort").value;
      case "tcp": {
        const h = $("#setHost").value.trim(), p = +$("#setTcpPort").value || 4001;
        if (!h) throw new Error("Enter the IP address of the device server.");
        return `tcp://${h.includes(":") && !h.startsWith("[") ? "[" + h + "]" : h}:${p}`;
      }
      case "listen": return `tcp-listen://:${+$("#setListenPort").value || 4001}`;
    }
  }
  $("#settingsForm").addEventListener("change", (e) => { if (e.target.id !== "setTrace" && !e.target.closest(".midibox")) e.currentTarget.dataset.dirty = "1"; });
  $("#settingsForm").addEventListener("input", (e) => { if (e.target.matches("input[type=text],input[type=number]")) e.currentTarget.dataset.dirty = "1"; });
  $("#settingsForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    let port;
    try { port = chosenPort(); } catch (err) { return alertBox("Check the connection settings", err.message); }
    if (!port) return alertBox("Check the connection settings", "No serial port is selected.");
    delete e.currentTarget.dataset.dirty;
    await op({ op: "settings", value: { port, pollInterval: +$("#setPoll").value, faderStep: +$("#setStep").value } });
  });
  $("#refreshPorts").onclick = () => { delete $("#settingsForm").dataset.dirty; fillSettings(true); };
  $("#setTrace").onchange = (e) => op({ op: "trace", value: e.target.checked });
  setInterval(async () => {
    if (view !== "settings") return;
    try {
      const log = await (await fetch("api/log")).json();
      const pre = $("#log");
      const atEnd = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 4;
      pre.innerHTML = (log || []).map((l) => `<span class="${l.dir}">${l.t}  ${l.dir.padEnd(4)} ${esc(l.text)}</span>`).join("\n") || "Nothing logged yet. Errors appear here; turn on recording to see every frame.";
      if (atEnd) pre.scrollTop = pre.scrollHeight;
      if (S?.midi?.available && $("#midiLog").closest("details").open) {
        const ml = await (await fetch("api/midilog")).json();
        const mp = $("#midiLog");
        const end2 = mp.scrollTop + mp.clientHeight >= mp.scrollHeight - 4;
        mp.innerHTML = (ml || []).map((l) => `<span class="${l.dir}">${l.t}  ${l.dir.padEnd(4)} ${esc(l.text)}</span>`).join("\n") || "No MIDI traffic yet.";
        if (end2) mp.scrollTop = mp.scrollHeight;
      }
    } catch { /* core restarting */ }
  }, 1000);

  // ---------- status and control panel ----------
  const textEditing = () => !!document.activeElement?.matches?.("textarea, input:not([type]), input[type=text], input[type=number], input[type=password]");
  function undoCmd(redo) {
    if (textEditing()) { document.execCommand(redo ? "redo" : "undo"); return; }
    if (dlg.open) return;
    op({ op: redo ? "redo" : "undo" });
  }
  function renderPanel() {
    const p = S.panel || {};
    // racks (one frame per connection)
    const r = S.rack;
    const dot = r.connected ? (r.healthy ? "ok" : "bad") : S.port && S.port !== "" ? "wait" : "";
    const state = r.connected ? (r.wellness || "Connected") : p.idle ? "Not connected" : "Offline";
    let mini = "";
    for (let i = 0; i < 12; i++) {
      const sl = S.slots[i];
      if (sl && sl.type === 0 && S.slots[i - 1]?.width === 2 && S.slots[i - 1].type !== 0) continue;
      mini += `<span class="${sl && sl.type ? "c" : ""} ${sl && sl.type && sl.width === 2 ? "w2" : ""}"></span>`;
    }
    mini += `<span class="psu"></span>`;
    const portName = S.port === "Demo" ? "Demo (simulated)" : S.port ? S.port.replace("/dev/", "").replace(/^tcp-listen:\/\/:?/, "network, port ").replace(/^tcp:\/\//, "network ") : "Choose a connection";
    const rl = `<button class="rackitem ${view === "hardware" ? "current" : ""}" data-rack="0" title="Show this rack">
      <span class="ri-name">${esc(r.label || "Rack")}</span>
      <span class="ri-sub"><span class="ri-dot ${dot}"></span>${esc(r.connected ? r.module : state)}${r.connected && r.wellness ? " · " + esc(r.wellness) : ""}</span>
      <span class="ri-port">${esc(portName)}</span>
      <span class="minirack">${mini}</span></button>`;
    const list = $("#rackList");
    if (list.dataset.h !== rl) { list.innerHTML = rl; list.dataset.h = rl; }

    const pb = $("#btnPhantomAll");
    pb.setAttribute("aria-pressed", String(!!p.phantomKilled));
    pb.querySelector(".big").textContent = p.phantomKilled ? "48 V RESTORE" : "48 V OFF";
    pb.querySelector("small").textContent = p.phantomKilled ? `switch on ${p.phantomOff} channel${p.phantomOff === 1 ? "" : "s"} again` : "all cards, all channels";
    pb.disabled = p.phantomKilled ? !p.phantomOff : !p.phantomOn;
    $("#phantomNote").textContent = p.phantomKilled
      ? `48 V was switched off here on ${p.phantomOff} channel${p.phantomOff === 1 ? "" : "s"}.`
      : p.phantomOn ? `48 V is on on ${p.phantomOn} channel${p.phantomOn === 1 ? "" : "s"}.` : S.strips?.some((x) => x.mic || x.dl5) ? "48 V is off on all channels." : "";

    $("#btnTakeNames").disabled = !p.dawNames;
    $("#btnResetNames").disabled = !p.named;
    $("#namesNote").textContent = p.dawNames
      ? `${p.dawNames} channel${p.dawNames === 1 ? " has a track name" : "s have track names"} from the DAW.`
      : "Put the 21 Channel plug-in on DAW tracks and pick a rack channel in each.";
    $("#btnResetOrder").disabled = !p.orderChanged;
    $("#btnUndo").disabled = !p.canUndo;
    $("#btnRedo").disabled = !p.canRedo;
    $("#btnUndo").title = p.canUndo ? `Undo ${p.undoText} (⌘Z)` : "Nothing to undo";
    $("#btnRedo").title = p.canRedo ? `Redo ${p.redoText} (⇧⌘Z)` : "Nothing to redo";
    $("#undoNote").textContent = p.canUndo ? `Last change: ${p.undoText}` : "";
  }
  $("#rackList").addEventListener("click", (e) => {
    if (!e.target.closest(".rackitem")) return;
    setView("hardware");
    const f = $(".rackframe");
    f.classList.remove("blink");
    void f.offsetWidth; // restart the animation
    f.classList.add("blink");
    f.scrollIntoView({ block: "nearest", behavior: "smooth" });
  });
  $(".rackframe").addEventListener("animationend", (e) => e.currentTarget.classList.remove("blink"));
  const chanList = (ids) => {
    const names = ids.map((id) => { const s = strip(id); return s ? (s.label ? `${s.fixed} (${s.label})` : s.fixed) : null; }).filter(Boolean);
    return names.length ? `<p class="chlist">${esc(names.slice(0, 24).join(", "))}${names.length > 24 ? ` and ${names.length - 24} more` : ""}</p>` : "";
  };
  $("#btnPhantomAll").onclick = async () => {
    const p = S.panel;
    if (p.phantomKilled) {
      const r = await dialogBox(`<h2>Switch 48 V on again?</h2><p>Phantom power goes back on on the ${p.phantomOff} channel${p.phantomOff === 1 ? "" : "s"} where this button switched it off, and nowhere else.</p>
        <p>Check that the microphones are connected and the monitors are turned down.</p>`,
        [{ label: "Cancel", value: "" }, { label: "Switch 48 V on", primary: true, value: "ok" }]);
      if (r === "ok") op({ op: "phantomAll" });
      return;
    }
    const on = S.strips.filter((x) => (x.mic && x.mic.phantom) || (x.dl5 && x.dl5.phantom)).map((x) => x.id);
    const r = await dialogBox(`<h2>Switch 48 V off on all channels?</h2><p>Phantom power is switched off on every card of the rack: ${on.length} channel${on.length === 1 ? " has" : "s have"} it on now.</p>${chanList(on)}
      <p>Press the button again later to switch 48 V back on on exactly these channels.</p>`,
      [{ label: "Cancel", value: "" }, { label: "Switch 48 V off", primary: true, value: "ok" }]);
    if (r === "ok") op({ op: "phantomAll" });
  };
  $("#btnTakeNames").onclick = () => op({ op: "takeNames" });
  $("#btnResetNames").onclick = async () => {
    const r = await dialogBox(`<h2>Reset all channel names?</h2><p>The names you gave the channels are removed; the fixed names like MIC 1/2 stay. Comments and colours are kept. You can undo this with ⌘Z.</p>`,
      [{ label: "Cancel", value: "" }, { label: "Reset names", primary: true, value: "ok" }]);
    if (r === "ok") op({ op: "resetNames" });
  };
  $("#btnResetOrder").onclick = () => op({ op: "resetOrder" });
  $("#btnUndo").onclick = () => op({ op: "undo" });
  $("#btnRedo").onclick = () => op({ op: "redo" });
  $("#btnDemo").onclick = () => op({ op: "settings", value: { port: "Demo" } });
  $("#btnConnect").onclick = () => {
    setView("settings");
    if ($("#setMode").value === "demo") { $("#setMode").value = "serial"; showMode("serial"); $("#settingsForm").dataset.dirty = "1"; }
    $("#setMode").focus();
  };

  // ---------- keyboard shortcuts & native menu bridge ----------
  async function about() {
    const v = await (await fetch("api/version")).json().catch(() => ({}));
    const repo = S?.update?.repo;
    const src = repo ? `https://github.com/${repo}` : "";
    dialogBox(`<div class="about"><h2>21 Remote</h2><p>Remote control for D21m frames on macOS, version ${esc(v.version || "")}.<br>Runs natively on Apple Silicon and Intel.</p>
      <p>Free software under the GNU Affero General Public License, version 3 or (at your option) any later version.${src ? ` Source code: <a href="${esc(src)}" target="_blank" rel="noopener">${esc(src)}</a>.` : ""} It comes with no warranty.</p>
      <p class="fine">An independent project, not affiliated with or endorsed by the maker of the D21m. D21m is a trademark of its owner. VST is a trademark of Steinberg Media Technologies GmbH.</p></div>`,
      [{ label: "Close", primary: true }]);
  }
  document.addEventListener("click", (e) => {
    const a = e.target.closest("a[href^='https://']");
    if (!a) return;
    e.preventDefault();
    post("api/system", { action: "url", url: a.href }).catch(() => window.open(a.href, "_blank"));
  });
  const commands = {
    open: () => openSnapshot(), save: saveSnapshot, newSession, clearRecent: () => op({ op: "clearRecent" }), hardware: () => setView("hardware"), channels: () => setView("channels"),
    settings: () => setView("settings"), pollNow: () => op({ op: "pollNow" }), togglePolling: () => op({ op: "polling", value: !S.polling }), about,
    undo: () => undoCmd(false), redo: () => undoCmd(true),
  };
  window.app = { menu: (c) => commands[c]?.(), openRecent };
  document.addEventListener("keydown", (e) => {
    if (!(e.metaKey || e.ctrlKey) || e.altKey) return;
    if (e.key.toLowerCase() === "z" && !window.webkit?.messageHandlers?.native) {
      if (textEditing() || dlg.open) return; // the browser undoes typing
      e.preventDefault();
      undoCmd(e.shiftKey);
      return;
    }
    const k = { n: "newSession", o: "open", s: "save", "1": "hardware", "2": "channels", "3": "settings", r: "pollNow" }[e.key.toLowerCase()];
    // inside the macOS app the native menu bar owns these shortcuts
    if (k && !window.webkit?.messageHandlers?.native) { e.preventDefault(); commands[k](); }
  });
  document.addEventListener("contextmenu", (e) => { if (!e.target.closest(".strip") && !e.target.closest("input")) e.preventDefault(); });

  connect();
})();
