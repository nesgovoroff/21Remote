// SPDX-License-Identifier: AGPL-3.0-or-later
// What the WebKit of macOS 10.10-10.15 lacks: <dialog> (Safari 15.4),
// pointer events (Safari 13) and flex gap (Safari 14.1). Put in front of
// app.js by macos/legacyweb.py.
(function () {
  "use strict";

  // ---- flex gap: style.css has margin fallbacks under html.nogap
  var probe = document.createElement("div");
  probe.style.cssText = "display:flex;flex-direction:column;row-gap:1px;position:absolute;visibility:hidden";
  probe.appendChild(document.createElement("div"));
  probe.appendChild(document.createElement("div"));
  document.documentElement.appendChild(probe);
  if (probe.scrollHeight !== 1) document.documentElement.className += " nogap";
  probe.parentNode.removeChild(probe);

  // ---- pointer events from mouse events
  if (!window.PointerEvent) {
    var capture = null;
    var fire = function (type, src, target) {
      if (!target) return;
      var ev = new MouseEvent(type, {
        bubbles: true, cancelable: true, view: window, detail: src.detail,
        screenX: src.screenX, screenY: src.screenY, clientX: src.clientX, clientY: src.clientY,
        ctrlKey: src.ctrlKey, shiftKey: src.shiftKey, altKey: src.altKey, metaKey: src.metaKey,
        button: src.button, buttons: src.buttons, relatedTarget: null
      });
      ev.pointerId = 1;
      ev.pointerType = "mouse";
      ev.isPrimary = true;
      if (!target.dispatchEvent(ev)) src.preventDefault();
    };
    document.addEventListener("mousedown", function (e) { fire("pointerdown", e, e.target); }, true);
    document.addEventListener("mousemove", function (e) { fire("pointermove", e, capture || e.target); }, true);
    document.addEventListener("mouseup", function (e) {
      var t = capture || e.target;
      capture = null;
      fire("pointerup", e, t);
    }, true);
    Element.prototype.setPointerCapture = function () { capture = this; };
    Element.prototype.releasePointerCapture = function () { if (capture === this) capture = null; };
    Element.prototype.hasPointerCapture = function () { return capture === this; };
  }

  // ---- <dialog> with showModal(), close() and <form method="dialog">
  var proto = window.HTMLDialogElement && window.HTMLDialogElement.prototype;
  if (!proto || !proto.showModal) {
    var backdrop = null;
    var lastButton = null;
    var hasOnclose = "onclose" in HTMLElement.prototype;
    var setup = function (d) {
      d.returnValue = "";
      d.showModal = function () {
        if (!backdrop) {
          backdrop = document.createElement("div");
          backdrop.className = "poly-backdrop";
        }
        document.body.appendChild(backdrop);
        d.setAttribute("open", "");
      };
      d.show = d.showModal;
      d.close = function (value) {
        if (!d.hasAttribute("open")) return;
        if (value !== undefined) d.returnValue = String(value);
        d.removeAttribute("open");
        if (backdrop && backdrop.parentNode) backdrop.parentNode.removeChild(backdrop);
        var ev = document.createEvent("Event");
        ev.initEvent("close", false, false);
        d.dispatchEvent(ev);
        if (!hasOnclose && typeof d.onclose === "function") d.onclose(ev);
      };
      d.addEventListener("click", function (e) {
        var b = e.target.closest ? e.target.closest("button") : null;
        if (b && (b.getAttribute("type") || "submit") === "submit") lastButton = b;
      }, true);
      d.addEventListener("submit", function (e) {
        if ((e.target.getAttribute("method") || "").toLowerCase() !== "dialog") return;
        e.preventDefault();
        d.close(lastButton ? lastButton.value : "");
        lastButton = null;
      }, true);
      document.addEventListener("keydown", function (e) {
        if ((e.key === "Escape" || e.keyCode === 27) && d.hasAttribute("open")) {
          e.preventDefault();
          d.close("");
        }
      });
    };
    var dialogs = document.querySelectorAll("dialog");
    for (var i = 0; i < dialogs.length; i++) setup(dialogs[i]);
  }
})();
