/**
 * PowerWing Web UI – app.js
 * Vanilla JS, no dependencies.  Communicates with the Go backend via WebSocket.
 */
'use strict';

// ─── WebSocket client ─────────────────────────────────────────────────────────

const WS_URL = `ws://${location.host}/ws`;
const RECONNECT_DELAY = 3000;

let ws = null;
let reconnectTimer = null;

function connectWS() {
  if (ws && ws.readyState === WebSocket.OPEN) return;
  ws = new WebSocket(WS_URL);
  ws.onopen    = () => { clearTimeout(reconnectTimer); console.log('[ws] connected'); };
  ws.onmessage = (e) => handleServerMsg(JSON.parse(e.data));
  ws.onclose   = () => { reconnectTimer = setTimeout(connectWS, RECONNECT_DELAY); };
  ws.onerror   = () => ws.close();
}

function sendCmd(deviceId, command, params) {
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({ type: 'cmd', device_id: deviceId, command, params }));
}

/**
 * Returns a debounced version of fn that fires only after `delay` ms of silence.
 * Each call with the same key cancels the previous pending call.
 */
const _debTimers = new Map();
function debounce(key, fn, delay = 100) {
  clearTimeout(_debTimers.get(key));
  _debTimers.set(key, setTimeout(() => { _debTimers.delete(key); fn(); }, delay));
}

/** Debounced sendCmd – coalesces rapid set-point changes into one WS message. */
function sendCmdDebounced(deviceId, command, params, delay = 30) {
  debounce(`cmd:${deviceId}:${command}`, () => sendCmd(deviceId, command, params), delay);
}

// ─── App state ────────────────────────────────────────────────────────────────

const state = {
  devices: {},     // id → { id, name, type, connected, state }
  activeTab: null, // device id or 'settings'
};

// ─── DOM helpers ──────────────────────────────────────────────────────────────

const $  = (sel, ctx = document) => ctx.querySelector(sel);
const $$ = (sel, ctx = document) => [...ctx.querySelectorAll(sel)];

function showPage(id) {
  $$('.page').forEach(p => p.classList.remove('active'));
  const page = $(`#page-${id}`) || $(`#page-${CSS.escape(id)}`);
  if (page) page.classList.add('active');
}

/** Show the devices grid; fall back to the empty state if no devices. */
function showDevices() {
  document.getElementById('page-settings').classList.remove('active');
  document.getElementById('page-empty')?.classList.remove('active');
  const dc = document.getElementById('devices-container');
  if (Object.keys(state.devices).length === 0) {
    dc.classList.add('hidden');
    document.getElementById('page-empty')?.classList.add('active');
  } else {
    dc.classList.remove('hidden');
  }
}

/** Return to the device overview (clicking the brand / leaving Settings). */
function goHome() {
  state.activeTab = null;
  $$('.device-tab').forEach(t => t.classList.remove('active'));
  showDevices();
  document.getElementById('devices-container')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  window.scrollTo({ top: 0, behavior: 'smooth' });
}

// ─── Tab management ───────────────────────────────────────────────────────────

function renderTabs() {
  const nav = $('#device-tabs');
  const devs = Object.values(state.devices);

  // Keep existing tabs, add/update them
  const existing = new Set();
  $$('.device-tab', nav).forEach(t => existing.add(t.dataset.id));

  devs.forEach(d => {
    let tab = $(`.device-tab[data-id="${d.id}"]`, nav);
    if (!tab) {
      tab = document.createElement('button');
      tab.className = 'device-tab';
      tab.dataset.id = d.id;
      tab.addEventListener('click', () => toggleDevicePanel(d.id));
      nav.appendChild(tab);
    }
    const dot = d.connected ? '<span class="tab-dot connected"></span>' : '<span class="tab-dot"></span>';
    tab.innerHTML = `${dot}${d.name}`;
    // Reflect hidden state on the tab
    const page = document.getElementById(`page-${d.id}`);
    tab.classList.toggle('panel-off', !!page?.classList.contains('panel-hidden'));
    existing.delete(d.id);
  });

  // Remove tabs for deleted devices
  existing.forEach(id => $(`.device-tab[data-id="${id}"]`, nav)?.remove());

  // On first render show all panels
  if (!state.activeTab && devs.length > 0) {
    state.activeTab = devs[0].id;
  }
}

/** Toggle a device panel visible/hidden; scroll into view when showing. */
function toggleDevicePanel(id) {
  const page = document.getElementById(`page-${id}`);
  const tab  = $(`.device-tab[data-id="${id}"]`);
  if (!page) return;
  const hiding = !page.classList.contains('panel-hidden');
  page.classList.toggle('panel-hidden', hiding);
  tab?.classList.toggle('panel-off', hiding);
  if (!hiding) {
    showDevices();
    page.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }
}

/** @deprecated kept for compatibility */
function scrollToDevice(id) { toggleDevicePanel(id); }
/** @deprecated kept for compatibility; use scrollToDevice */
function activateTab(id) { toggleDevicePanel(id); }

// ─── Page factory ─────────────────────────────────────────────────────────────

/** Returns true for any USB hub device type. */
function isHub(type) { return type === 'usbslim' || type === 'sangenxian' || type === 'usb_hub'; }
/** Returns true for alarm light device type. */
function isAlarm(type) { return type === 'usbalarm' || type === 'alarm_light'; }
/** Returns true for power monitor (F-11, POWER-Z) device types. */
function isMonitor(type) { return type === 'f11' || type === 'powerz' || type === 'power_monitor'; }

function ensureDevicePage(d) {
  if (document.getElementById(`page-${d.id}`)) return;

  let tplId = 'tpl-psu';
  if (isHub(d.type)) tplId = 'tpl-hub';
  else if (isAlarm(d.type)) tplId = 'tpl-alarm';
  else if (isMonitor(d.type)) tplId = 'tpl-monitor';

  const tpl = document.getElementById(tplId);
  if (!tpl) return;

  const clone = tpl.content.firstElementChild.cloneNode(true);
  clone.id = `page-${d.id}`;
  clone.dataset.deviceId = d.id;

  if (isHub(d.type)) {
    buildHubPage(clone, d);
  } else if (isAlarm(d.type)) {
    buildAlarmPage(clone, d);
  } else if (isMonitor(d.type)) {
    buildMonitorPage(clone, d);
  } else {
    buildPsuPage(clone, d);
  }

  document.getElementById('devices-container').appendChild(clone);
}

// ─── PSU page ─────────────────────────────────────────────────────────────────

function buildPsuPage(el, d) {
  // Wire up PSU output toggle switch
  const outSw = $('.toggle-sw[data-cmd="set_outp"]', el);
  if (outSw) {
    outSw.addEventListener('click', () => {
      const on = outSw.getAttribute('aria-checked') !== 'true';
      // Optimistic update – reflect click immediately; server confirms on next poll
      outSw.setAttribute('aria-checked', String(on));
      const stEl = $('.output-state', outSw.closest('.output-sw-wrap'));
      if (stEl) { stEl.textContent = on ? 'ON' : 'OFF'; stEl.classList.toggle('on', on); }
      sendCmd(d.id, 'set_outp', { on });
    });
  }

  // "Release panel": hand front-panel control back to the operator without
  // disconnecting, so measurements keep updating.  The next set command
  // re-takes control automatically.
  const relBtn = $('[data-cmd="release_control"]', el);
  if (relBtn) {
    relBtn.addEventListener('click', () => {
      relBtn.classList.add('hidden'); // reappears on the next poll if still locked
      sendCmd(d.id, 'release_control', {});
    });
  }

  // Optimistic meas-row update: reflect new voltage/current in the measurement
  // display immediately when the user moves a control, without waiting for the
  // next server push.  Only volt and curr have a corresponding meas display.
  function measOptimistic(cmd, value) {
    if (cmd === 'set_volt') {
      const vEl = $('.volt-value', el);
      if (vEl) vEl.textContent = value.toFixed(3);
      const voltMax = parseFloat($('.spinbox[data-cmd="set_volt"]', el)?.dataset.max) || 36;
      const vFill = $('.meas-volt .meas-bar-fill', el);
      if (vFill) vFill.style.width = Math.min(100, (value / voltMax) * 100).toFixed(1) + '%';
    } else if (cmd === 'set_curr') {
      const aEl = $('.curr-value', el);
      if (aEl) aEl.textContent = value.toFixed(3);
      const currMax = parseFloat($('.spinbox[data-cmd="set_curr"]', el)?.dataset.max) || 10;
      const aFill = $('.meas-curr .meas-bar-fill', el);
      if (aFill) aFill.style.width = Math.min(100, (value / currMax) * 100).toFixed(1) + '%';
    }
  }

  // Wire up spinboxes
  $$('.spinbox', el).forEach(box => {
    const cmd  = box.dataset.cmd;
    const step = parseFloat(box.dataset.step) || 0.1;
    const min  = parseFloat(box.dataset.min)  || 0;
    const max  = parseFloat(box.dataset.max)  || 99;
    const dec  = parseInt(box.dataset.decimals, 10) || 2;
    const inp  = $('input', box);

    const spinUp   = $('.spin-up',   box);
    const spinDown = $('.spin-down', box);
    if (spinUp) spinUp.addEventListener('click', () => {
      const v = Math.min(max, parseFloat(inp.value) + step);
      inp.value = v.toFixed(dec);
      const bar = box.closest('.set-card')?.querySelector('.set-bar');
      if (bar) { bar.value = v; updateBarFill(bar); }
      measOptimistic(cmd, v);
      sendCmd(d.id, cmd, { value: v });
    });
    if (spinDown) spinDown.addEventListener('click', () => {
      const v = Math.max(min, parseFloat(inp.value) - step);
      inp.value = v.toFixed(dec);
      const bar = box.closest('.set-card')?.querySelector('.set-bar');
      if (bar) { bar.value = v; updateBarFill(bar); }
      measOptimistic(cmd, v);
      sendCmd(d.id, cmd, { value: v });
    });
    inp.addEventListener('change', () => {
      let v = parseFloat(inp.value);
      if (isNaN(v)) v = 0;
      v = Math.max(min, Math.min(max, v));
      inp.value = v.toFixed(dec);
      measOptimistic(cmd, v);
      sendCmdDebounced(d.id, cmd, { value: v });
    });

    // Mouse-wheel support: update visuals on every tick, send only after scroll settles.
    inp.addEventListener('wheel', (e) => {
      e.preventDefault();
      const dir  = e.deltaY < 0 ? 1 : -1;
      const v    = Math.max(min, Math.min(max, parseFloat(inp.value) + dir * step));
      inp.value  = v.toFixed(dec);
      // Keep range bar in sync
      const bar = box.closest('.set-card')?.querySelector('.set-bar');
      if (bar) { bar.value = v; updateBarFill(bar); }
      debounce(`settle:${d.id}:${cmd}`, () => {
        const fv = parseFloat(inp.value);
        measOptimistic(cmd, fv);
        sendCmd(d.id, cmd, { value: fv });
      }, 200);
    }, { passive: false });
  });

  // Wire up range bars (scrollable set-point sliders)
  $$('.set-bar', el).forEach(bar => {
    const card = bar.closest('.set-card');
    if (!card) return;
    const box  = $('.spinbox', card);
    if (!box)  return;
    const inp  = $('input', box);
    const cmd  = box.dataset.cmd;
    const step = parseFloat(box.dataset.step) || 0.1;
    const min  = parseFloat(box.dataset.min)  || 0;
    const max  = parseFloat(box.dataset.max)  || 99;
    const dec  = parseInt(box.dataset.decimals, 10) || 2;
    // Colour-code bar fill to match the value badge colour
    if (cmd === 'set_volt' || cmd === 'set_ovp') bar.style.setProperty('--bar-color', '#4ade80');
    if (cmd === 'set_curr' || cmd === 'set_ocp') bar.style.setProperty('--bar-color', '#f87171');
    updateBarFill(bar);
    // Update visuals while dragging but only send command and meas on mouse-up.
    bar.addEventListener('input', () => {
      const v = parseFloat(bar.value);
      if (inp) inp.value = v.toFixed(dec);
      updateBarFill(bar);
    });
    bar.addEventListener('pointerup', () => {
      const v = parseFloat(bar.value);
      measOptimistic(cmd, v);
      sendCmd(d.id, cmd, { value: v });
    });
    bar.addEventListener('wheel', (e) => {
      e.preventDefault();
      const dir = e.deltaY < 0 ? 1 : -1;
      const v   = Math.max(min, Math.min(max, parseFloat(bar.value) + dir * step));
      bar.value = v;
      if (inp) inp.value = v.toFixed(dec);
      updateBarFill(bar);
      debounce(`settle:${d.id}:${cmd}`, () => {
        const fv = parseFloat(bar.value);
        measOptimistic(cmd, fv);
        sendCmd(d.id, cmd, { value: fv });
      }, 200);
    }, { passive: false });
  });

  buildPortPanel(el, d);
}

function updatePsuPage(el, s) {
  const fmt = (v, d) => v.toFixed(d);

  // Measurements
  const vEl = $('.volt-value', el);
  const aEl = $('.curr-value', el);
  const pEl = $('.pow-value',  el);
  if (vEl) vEl.textContent = fmt(s.volt_meas, 3);
  if (aEl) aEl.textContent = fmt(s.curr_meas, 3);
  if (pEl) pEl.textContent = fmt(s.power_meas, 2);

  // Measurement fill bars
  const voltMax = parseFloat($('.spinbox[data-cmd="set_volt"]', el)?.dataset.max) || 36;
  const currMax = parseFloat($('.spinbox[data-cmd="set_curr"]', el)?.dataset.max) || 10;
  const vFill = $('.meas-volt .meas-bar-fill', el);
  const aFill = $('.meas-curr .meas-bar-fill', el);
  const pFill = $('.meas-pow  .meas-bar-fill', el);
  if (vFill) vFill.style.width = Math.min(100, (s.volt_meas  / voltMax)               * 100).toFixed(1) + '%';
  if (aFill) aFill.style.width = Math.min(100, (s.curr_meas  / currMax)               * 100).toFixed(1) + '%';
  if (pFill) pFill.style.width = Math.min(100, (s.power_meas / (voltMax * currMax))   * 100).toFixed(1) + '%';

  // Output toggle switch
  const outSw = $('.toggle-sw[data-cmd="set_outp"]', el);
  if (outSw) {
    outSw.setAttribute('aria-checked', String(!!s.output_on));
    const stateEl = $('.output-state', outSw.closest('.output-sw-wrap'));
    if (stateEl) {
      stateEl.textContent = s.output_on ? 'ON' : 'OFF';
      stateEl.classList.toggle('on', !!s.output_on);
    }
  }

  // Mode badge
  const badge = $('.mode-badge', el);
  if (badge) {
    badge.textContent = s.cv_mode ? 'CV' : 'CC';
    badge.classList.toggle('cc', !s.cv_mode);
  }

  // "Release panel" is only offered while this PC holds front-panel control.
  const relBtn = $('[data-cmd="release_control"]', el);
  if (relBtn) relBtn.classList.toggle('hidden', !s.remote_control);

  // Temperature
  const tEl = $('.temp-value', el);
  if (tEl) tEl.textContent = s.temperature > 0 ? `${s.temperature.toFixed(1)}°C` : '—';

  // Set-point spinboxes (only update if user is not interacting)
  setSpinboxIfIdle($('.spinbox[data-cmd="set_volt"]', el), s.volt_set, 2);
  setSpinboxIfIdle($('.spinbox[data-cmd="set_curr"]', el), s.curr_set, 2);
  setSpinboxIfIdle($('.spinbox[data-cmd="set_ovp"]',  el), s.ovp_limit, 2);
  setSpinboxIfIdle($('.spinbox[data-cmd="set_ocp"]',  el), s.ocp_limit, 2);
}

/** Recalculate and push --fill-pct so the bar gradient shows the correct fill. */
function updateBarFill(bar) {
  const min = parseFloat(bar.min) || 0;
  const max = parseFloat(bar.max) || 100;
  const val = parseFloat(bar.value) || 0;
  const pct = ((val - min) / (max - min)) * 100;
  bar.style.setProperty('--fill-pct', pct.toFixed(2) + '%');
  // Update inline value badge (now lives in spinbox-row, not bar-wrap)
  const label = bar.closest('.set-card')?.querySelector('.bar-val');
  if (label) {
    const dec = parseInt(bar.closest('.set-card')?.querySelector('.spinbox')?.dataset.decimals, 10) || 2;
    label.textContent = val.toFixed(dec);
  }
}

function setSpinboxIfIdle(box, value, dec) {
  if (!box || value === undefined) return;
  const inp = $('input', box);
  if (inp && document.activeElement !== inp) {
    inp.value = (value || 0).toFixed(dec);
    // Keep the range bar in sync too
    const bar = box.closest('.set-card')?.querySelector('.set-bar');
    if (bar && document.activeElement !== bar) {
      bar.value = value || 0;
      updateBarFill(bar);
    }
  }
}

// ─── USB Hub page ──────────────────────────────────────────────────────────────

/** Number of switchable ports for a hub device. */
function hubPortCount(d) {
  const n = Number(d?.state?.port_count);
  if (Number.isFinite(n) && n > 0) return n;
  return d?.type === 'sangenxian' ? 12 : 4;
}

/** Whether the hub exposes lock / hardware-key / auto-save controls. */
function hubControlsSupported(d) {
  const s = d?.state;
  if (s) return !!(s.supports_lock || s.supports_hw_keys || s.supports_auto_save);
  return d?.type !== 'sangenxian';
}

function buildHubPage(el, d) {
  const portsRow = $('.ports-row', el);
  const portCount = hubPortCount(d);
  // 12-port hubs use a compact 2×6 grid; 4-port hubs keep their original layout.
  portsRow.classList.toggle('ports-row--12', portCount === 12);
  for (let i = 1; i <= portCount; i++) {
    const card = document.createElement('div');
    card.className = 'port-card';
    card.id = `${d.id}-port-${i}`;
    card.innerHTML = `
      <div class="port-label">PORT ${i}</div>
      <button class="toggle-sw" role="switch" aria-checked="false" data-port="${i}">
        <span class="toggle-knob"></span>
      </button>
      <div class="port-state">OFF</div>`;
    const sw = $('.toggle-sw', card);
    sw.addEventListener('click', () => {
      const on = sw.getAttribute('aria-checked') !== 'true';
      // Optimistic update
      sw.setAttribute('aria-checked', String(on));
      const stEl = $('.port-state', card);
      if (stEl) stEl.textContent = on ? 'ON' : 'OFF';
      card.classList.toggle('on', on);
      sendCmd(d.id, 'set_usb_port', { port: i, on });
    });
    portsRow.appendChild(card);
  }

  // Master switches: all ports ON / OFF.
  $$('.btn-all-ports', el).forEach(btn => {
    btn.addEventListener('click', () => {
      const on = btn.dataset.all === 'on';
      $$('.port-card', el).forEach(card => {
        card.classList.toggle('on', on);
        const sw = $('.toggle-sw', card);
        if (sw) sw.setAttribute('aria-checked', String(on));
        const stEl = $('.port-state', card);
        if (stEl) stEl.textContent = on ? 'ON' : 'OFF';
      });
      sendCmd(d.id, 'set_all_usb_ports', { on });
    });
  });

  // Control buttons
  $$('.btn-hub-ctrl', el).forEach(btn => {
    btn.addEventListener('click', () => {
      const cmd   = btn.dataset.cmd;
      const param = btn.dataset.param;
      const cur   = btn.dataset.state === 'true';
      const next  = !cur;
      btn.dataset.state = String(next);
      sendCmd(d.id, cmd, { [param]: next });
    });
  });

  // Hide controls the controller does not implement (e.g. SanGenXian).
  if (!hubControlsSupported(d)) {
    $('.hub-ctrl-row', el)?.classList.add('hidden');
  }

  buildPortPanel(el, d);
}

function updateHubPage(el, s, deviceId) {
  if (!s.ports) return;
  s.ports.forEach((on, i) => {
    const card = document.getElementById(`${deviceId}-port-${i + 1}`);
    if (!card) return;
    card.classList.toggle('on', on);
    const sw = $('.toggle-sw', card);
    if (sw) sw.setAttribute('aria-checked', String(on));
    const stateEl = $('.port-state', card);
    if (stateEl) stateEl.textContent = on ? 'ON' : 'OFF';
  });

  // Sync ctrl buttons
  const lockBtn = $('.btn-hub-ctrl[data-cmd="set_lock"]', el);
  if (lockBtn) {
    lockBtn.dataset.state = String(s.locked);
    lockBtn.textContent = s.locked ? '🔒 Lock ON' : '🔒 Lock OFF';
    lockBtn.classList.toggle('active', s.locked);
  }
  const hwBtn = $('.btn-hub-ctrl[data-cmd="set_hwkeys"]', el);
  if (hwBtn) {
    hwBtn.dataset.state = String(s.hw_keys_enabled);
    hwBtn.textContent = s.hw_keys_enabled ? '⌨ HW Keys ON' : '⌨ HW Keys OFF';
    hwBtn.classList.toggle('active', s.hw_keys_enabled);
  }
  const asBtn = $('.btn-hub-ctrl[data-cmd="set_autosave"]', el);
  if (asBtn) {
    asBtn.dataset.state = String(s.auto_save_enabled);
    asBtn.textContent = s.auto_save_enabled ? '💾 Auto-Save ON' : '💾 Auto-Save OFF';
    asBtn.classList.toggle('active', s.auto_save_enabled);
  }
}

// ─── Alarm light page ─────────────────────────────────────────────────────────

function buildAlarmPage(el, d) {
  // Color buttons
  $$('.alarm-color-btn', el).forEach(btn => {
    btn.addEventListener('click', () => {
      const color = btn.dataset.color;
      const modeBtn = $('.alarm-mode-btn.active', el);
      const mode = modeBtn ? modeBtn.dataset.mode : 'on';
      // Highlight selected color
      $$('.alarm-color-btn', el).forEach(b => b.classList.remove('selected'));
      btn.classList.add('selected');
      // Update indicator
      const indicator = $('#alarm-indicator', el);
      if (indicator) indicator.style.background = btn.style.background;
      const status = $('#alarm-status-text', el);
      if (status) { status.textContent = color.toUpperCase() + ' ' + mode.toUpperCase(); status.style.color = btn.style.background; }
      sendCmd(d.id, 'alarm_set_color', { color, mode });
    });
  });

  // Mode buttons
  $$('.alarm-mode-btn', el).forEach(btn => {
    btn.addEventListener('click', () => {
      $$('.alarm-mode-btn', el).forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
    });
  });

  // Action buttons (buzzer, off, rainbow, beep, pwm)
  $$('.btn-alarm-action', el).forEach(btn => {
    btn.addEventListener('click', () => {
      const cmd = btn.dataset.cmd;
      if (cmd === 'alarm_buzzer') {
        const cur = btn.dataset.state === 'true';
        const next = !cur;
        btn.dataset.state = String(next);
        btn.textContent = next ? '🔊 Buzzer ON' : '🔊 Buzzer';
        btn.classList.toggle('active', next);
        sendCmd(d.id, cmd, { on: next });
      } else if (cmd === 'alarm_off') {
        // Reset UI
        $$('.alarm-color-btn', el).forEach(b => b.classList.remove('selected'));
        const indicator = $('#alarm-indicator', el);
        if (indicator) indicator.style.background = '#333';
        const status = $('#alarm-status-text', el);
        if (status) { status.textContent = 'OFF'; status.style.color = '#888'; }
        // Reset buzzer button
        const buzBtn = $('.btn-alarm-action[data-cmd="alarm_buzzer"]', el);
        if (buzBtn) { buzBtn.dataset.state = 'false'; buzBtn.textContent = '🔊 Buzzer'; buzBtn.classList.remove('active'); }
        sendCmd(d.id, cmd, {});
      } else if (cmd === 'alarm_rainbow') {
        sendCmd(d.id, cmd, { duration: 2000 });
      } else if (cmd === 'alarm_beep') {
        const color = $('#alarm-beep-color', el)?.value || 'red';
        const ms = parseInt($('#alarm-beep-ms', el)?.value, 10) || 400;
        sendCmd(d.id, cmd, { color, ms });
      } else if (cmd === 'alarm_pwm') {
        const r = parseInt($('.alarm-pwm-inp[data-color="red"]', el)?.value, 10) || 0;
        const g = parseInt($('.alarm-pwm-inp[data-color="green"]', el)?.value, 10) || 0;
        const b = parseInt($('.alarm-pwm-inp[data-color="blue"]', el)?.value, 10) || 0;
        const flash = parseInt($('#alarm-pwm-flash', el)?.value, 10) || 0;
        const buzzer = $('#alarm-pwm-buzzer', el)?.checked || false;
        sendCmd(d.id, cmd, { red: r, green: g, blue: b, flash_interval: flash, buzzer });
      } else if (cmd === 'alarm_animate') {
        const colorFrom = $('#alarm-anim-from', el)?.value || 'red';
        const colorTo = $('#alarm-anim-to', el)?.value || 'yellow';
        const duration = parseInt($('#alarm-anim-dur', el)?.value, 10) || 2000;
        const loop = parseInt($('#alarm-anim-loop', el)?.value);
        const params = { color: colorFrom, color2: colorTo, mode: 'animation', duration };
        if (!isNaN(loop)) params.loop = loop;
        sendCmd(d.id, 'alarm_set_color', params);
      }
    });
  });

  buildPortPanel(el, d);
}

function updateAlarmPage(el, s, deviceId) {
  if (!s) return;
  // Update status text
  const status = $('#alarm-status-text', el);
  if (status) {
    if (s.color === 'off' || s.mode === 'off') {
      status.textContent = 'OFF';
      status.style.color = '#888';
    } else if (s.color === 'pwm') {
      status.textContent = `PWM R:${s.pwm_r} G:${s.pwm_g} B:${s.pwm_b}`;
      status.style.color = '#f0a500';
    } else {
      status.textContent = s.color.toUpperCase() + ' ' + (s.mode || 'on').toUpperCase();
      const colorMap = { red:'#ef4444', yellow:'#eab308', green:'#22c55e', white:'#e2e8f0', cyan:'#06b6d4', purple:'#a855f7', blue:'#3b82f6' };
      status.style.color = colorMap[s.color] || '#fff';
    }
  }

  // Update indicator
  const indicator = $('#alarm-indicator', el);
  if (indicator) {
    if (s.color === 'off' || s.mode === 'off') {
      indicator.style.background = '#333';
      indicator.classList.remove('blinking');
    } else {
      const colorMap = { red:'#ef4444', yellow:'#eab308', green:'#22c55e', white:'#e2e8f0', cyan:'#06b6d4', purple:'#a855f7', blue:'#3b82f6' };
      indicator.style.background = colorMap[s.color] || s.color || '#fff';
      indicator.classList.toggle('blinking', s.mode === 'blink_slow' || s.mode === 'blink_fast');
      if (s.mode === 'blink_fast') indicator.style.animationDuration = '0.3s';
      else if (s.mode === 'blink_slow') indicator.style.animationDuration = '0.8s';
    }
  }

  // Highlight active color button
  if (s.color && s.color !== 'off' && s.color !== 'pwm') {
    $$('.alarm-color-btn', el).forEach(b => {
      b.classList.toggle('selected', b.dataset.color === s.color);
    });
  }

  // Sync mode button
  $$('.alarm-mode-btn', el).forEach(b => {
    b.classList.toggle('active', b.dataset.mode === s.mode);
  });

  // Sync buzzer button
  const buzBtn = $('.btn-alarm-action[data-cmd="alarm_buzzer"]', el);
  if (buzBtn) {
    buzBtn.dataset.state = String(!!s.buzzer_on);
    buzBtn.textContent = s.buzzer_on ? '🔊 Buzzer ON' : '🔊 Buzzer';
    buzBtn.classList.toggle('active', !!s.buzzer_on);
  }
}

// ─── Power monitor (F-11) page ─────────────────────────────────────────────────

/** Interval (ms) requested when the panel is expanded / collapsed. */
const MONITOR_INTERVAL_FAST = 10;   // expanded: average/peak measurement mode
const MONITOR_INTERVAL_NORMAL = 500; // collapsed: simple meter mode

/** Per-device chart state: ring buffers of samples for the curve plot. */
const monitorCharts = new Map(); // deviceId → { volt:[], curr:[], voltMax, currMax, raf }

function buildMonitorPage(el, d) {
  monitorCharts.set(d.id, { volt: [], curr: [], voltMax: 1, currMax: 1 });

  // ── Expand / fold button ─────────────────────────────────────────────────
  const expBtn = $('.monitor-expand-btn', el);
  const expBody = $('.monitor-expand-body', el);
  if (expBtn) {
    expBtn.addEventListener('click', () => {
      const expanded = expBtn.getAttribute('aria-expanded') === 'true';
      const next = !expanded;
      expBtn.setAttribute('aria-expanded', String(next));
      expBtn.classList.toggle('expanded', next);
      expBody.classList.toggle('hidden', !next);
      if (next) {
        // Entering measurement mode: switch to 10ms sampling.
        sendCmd(d.id, 'monitor_set_interval', { interval_ms: MONITOR_INTERVAL_FAST });
      } else {
        // Folding back: return to normal 500ms meter sampling.
        sendCmd(d.id, 'monitor_set_interval', { interval_ms: MONITOR_INTERVAL_NORMAL });
      }
    });
  }

  // ── Start / Stop average-period control ──────────────────────────────────
  const startBtn = $('.btn-monitor-start', el);
  if (startBtn) {
    startBtn.addEventListener('click', () => {
      const running = startBtn.classList.contains('running');
      if (running) {
        sendCmd(d.id, 'monitor_stop', {});
        startBtn.classList.remove('running');
        startBtn.textContent = '▶ Start';
      } else {
        // Reset chart history when a new measurement period begins.
        const ch = monitorCharts.get(d.id);
        if (ch) { ch.volt = []; ch.curr = []; ch.voltMax = 1; ch.currMax = 1; }
        sendCmd(d.id, 'monitor_start', {});
        startBtn.classList.add('running');
        startBtn.textContent = '⏸ Stop';
      }
    });
  }

  // ── Curve visibility checkboxes (independent voltage / current) ──────────
  const voltCb = $('.chart-show-volt', el);
  const currCb = $('.chart-show-curr', el);
  if (voltCb) voltCb.addEventListener('change', () => drawMonitorChart(el, d.id));
  if (currCb) currCb.addEventListener('change', () => drawMonitorChart(el, d.id));

  // ── Canvas sizing (device-pixel-ratio aware) ─────────────────────────────
  const canvas = $('.monitor-canvas', el);
  if (canvas) {
    const resize = () => {
      const rect = canvas.getBoundingClientRect();
      if (rect.width === 0) return;
      const dpr = window.devicePixelRatio || 1;
      canvas.width  = Math.round(rect.width * dpr);
      canvas.height = Math.round(200 * dpr);
      drawMonitorChart(el, d.id);
    };
    window.addEventListener('resize', resize);
    // Defer initial sizing until the panel becomes visible.
    setTimeout(resize, 50);
    const exp = $('.monitor-expand-btn', el);
    if (exp) exp.addEventListener('click', () => setTimeout(resize, 50));
  }

  buildPortPanel(el, d);
}

function updateMonitorPage(el, s, deviceId) {
  if (!s) return;

  // Simple-mode measurement cards
  const vEl = $('.volt-value', el);
  const aEl = $('.curr-value', el);
  const pEl = $('.pow-value',  el);
  const volt = s.voltage || 0;
  const curr = s.current || 0;
  if (vEl) vEl.textContent = volt.toFixed(3);
  if (aEl) aEl.textContent = curr.toFixed(4);
  if (pEl) pEl.textContent = (volt * curr).toFixed(3);

  // Fill bars (relative to the running max seen on this page)
  const ch = monitorCharts.get(deviceId);
  if (ch) {
    ch.voltMax = Math.max(ch.voltMax, volt);
    ch.currMax = Math.max(ch.currMax, curr);
    const vFill = $('.meas-volt .meas-bar-fill', el);
    const aFill = $('.meas-curr .meas-bar-fill', el);
    const pFill = $('.meas-pow  .meas-bar-fill', el);
    if (vFill) vFill.style.width = Math.min(100, (volt / ch.voltMax) * 100).toFixed(1) + '%';
    if (aFill) aFill.style.width = Math.min(100, (curr / ch.currMax) * 100).toFixed(1) + '%';
    if (pFill) pFill.style.width = Math.min(100, (volt * curr) / (ch.voltMax * ch.currMax) * 100).toFixed(1) + '%';
  }

  // Interval badge & sample count
  const iEl = $('.interval-value', el);
  if (iEl && s.interval_ms) iEl.textContent = s.interval_ms + 'ms';
  const sEl = $('.samples-value', el);
  if (sEl) sEl.textContent = String(s.sample_count || 0);

  // Average / peak stats
  const av = $('.avg-volt',  el);
  const ac = $('.avg-curr',  el);
  const pv = $('.peak-volt', el);
  const pc = $('.peak-curr', el);
  if (av) av.innerHTML = (s.avg_voltage ?? 0).toFixed(3) + ' <small>V</small>';
  if (ac) ac.innerHTML = (s.avg_current ?? 0).toFixed(4) + ' <small>A</small>';
  if (pv) pv.innerHTML = (s.peak_voltage ?? 0).toFixed(3) + ' <small>V</small>';
  if (pc) pc.innerHTML = (s.peak_current ?? 0).toFixed(4) + ' <small>A</small>';
  const mc = $('.mon-sample-count', el);
  if (mc) mc.textContent = String(s.sample_count || 0);

  // Start/stop button state (only reflect server state when idle locally)
  const startBtn = $('.btn-monitor-start', el);
  if (startBtn) {
    startBtn.classList.toggle('running', !!s.monitoring);
    startBtn.textContent = s.monitoring ? '⏸ Stop' : '▶ Start';
  }

  // Append sample to chart history and redraw
  if (ch) {
    ch.volt.push(volt);
    ch.curr.push(curr);
    // Keep a bounded window: ~600 samples ≈ 6s at 10ms, 5min at 500ms.
    if (ch.volt.length > 600) ch.volt.shift();
    if (ch.curr.length > 600) ch.curr.shift();
    scheduleMonitorDraw(el, deviceId);
  }
}

/** rAF-throttled chart redraw so 100 Hz sample streams still render at 60fps max. */
function scheduleMonitorDraw(el, deviceId) {
  const ch = monitorCharts.get(deviceId);
  if (!ch || ch.raf) return;
  ch.raf = requestAnimationFrame(() => {
    ch.raf = 0;
    drawMonitorChart(el, deviceId);
  });
}

/** Draws the voltage/current curve chart onto the canvas. */
function drawMonitorChart(el, deviceId) {
  const canvas = $('.monitor-canvas', el);
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const ch = monitorCharts.get(deviceId);
  if (!ch) return;

  const showVolt = $('.chart-show-volt', el)?.checked !== false;
  const showCurr = $('.chart-show-curr', el)?.checked !== false;

  const W = canvas.width, H = canvas.height;
  ctx.clearRect(0, 0, W, H);

  // Background
  ctx.fillStyle = '#0b1120';
  ctx.fillRect(0, 0, W, H);

  // Grid lines
  ctx.strokeStyle = 'rgba(148,163,184,.12)';
  ctx.lineWidth = 1;
  for (let i = 1; i < 4; i++) {
    const y = (H / 4) * i;
    ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(W, y); ctx.stroke();
  }
  for (let i = 1; i < 8; i++) {
    const x = (W / 8) * i;
    ctx.beginPath(); ctx.moveTo(x, 0); ctx.lineTo(x, H); ctx.stroke();
  }

  const pad = 8;
  const plotW = W - pad * 2;
  const plotH = H - pad * 2;

  const series = [
    { data: ch.volt, max: Math.max(ch.voltMax, 0.001), color: '#4ade80', show: showVolt },
    { data: ch.curr, max: Math.max(ch.currMax, 0.0001), color: '#f87171', show: showCurr },
  ];

  for (const s of series) {
    if (!s.show || s.data.length < 2) continue;
    const n = s.data.length;
    const step = plotW / Math.max(1, s.data.length - 1);
    ctx.beginPath();
    for (let i = 0; i < n; i++) {
      const x = pad + i * step;
      const norm = Math.min(1, s.data[i] / s.max);
      const y = pad + plotH * (1 - norm);
      if (i === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
    }
    ctx.strokeStyle = s.color;
    ctx.lineWidth = 2 * (window.devicePixelRatio || 1);
    ctx.lineJoin = 'round';
    ctx.stroke();

    // Soft glow under the curve
    ctx.lineTo(pad + plotW, pad + plotH);
    ctx.lineTo(pad, pad + plotH);
    ctx.closePath();
    ctx.fillStyle = s.color + '22'; // ~13% alpha
    ctx.fill();
  }
}

// ─── Server message handler ────────────────────────────────────────────────────

function handleServerMsg(msg) {
  switch (msg.type) {
    case 'init':
      (msg.devices || []).forEach(d => {
        state.devices[d.id] = d;
        ensureDevicePage(d);
        if (d.state) {
          const page = document.getElementById(`page-${d.id}`);
          if (page) updatePage(page, d, d.state);
        }
      });
      renderTabs();
      renderSettingsDeviceList();
      // Hide empty state if we have devices
      if (Object.keys(state.devices).length > 0) {
        document.getElementById('page-empty')?.classList.remove('active');
        document.getElementById('devices-container').classList.remove('hidden');
      }
      break;

    case 'state': {
      const d = state.devices[msg.device_id];
      if (d) {
        d.connected = msg.state?.connected ?? d.connected;
        d.state = msg.state;
        const page = document.getElementById(`page-${msg.device_id}`);
        if (page) updatePage(page, d, msg.state);
        // update tab dot
        const tab = $(`.device-tab[data-id="${msg.device_id}"]`);
        if (tab) {
          const dot = $('.tab-dot', tab);
          if (dot) dot.classList.toggle('connected', !!d.connected);
        }
      }
      break;
    }

    case 'error':
      console.warn('[ws] error:', msg.message);
      break;
  }
}

function updatePage(page, d, s) {
  if (!s) return;
  // Power monitors stream at up to 100 Hz – skip the shared debounce so no
  // chart samples are dropped; drawing itself is rAF-throttled.
  if (isMonitor(d.type)) {
    updateMonitorPage(page, s, d.id);
    updatePortPanel(page, d.connected);
    return;
  }
  // Debounce UI redraws: coalesce bursts within 20ms
  debounce(`ui:${d.id}`, () => {
    if (isHub(d.type)) {
      updateHubPage(page, s, d.id);
    } else if (isAlarm(d.type)) {
      updateAlarmPage(page, s, d.id);
    } else {
      updatePsuPage(page, s);
    }
    updatePortPanel(page, d.connected);
  }, 20);
}

// ─── Port configuration panel ─────────────────────────────────────────────────

function buildPortPanel(el, d) {
  const portSel   = $('.port-sel',      el);
  const baudSel   = $('.baud-sel',      el);
  const btn       = $('.btn-conn',      el);
  const indicator = $('.conn-indicator', el);
  if (!portSel || !btn) return;

  // Editable device name label
  const nameLabel = $('.device-name-label', el);
  if (nameLabel) {
    nameLabel.textContent = d.name;
    nameLabel.addEventListener('click', () => {
      nameLabel.contentEditable = 'true';
      nameLabel.focus();
      const range = document.createRange();
      range.selectNodeContents(nameLabel);
      const sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
    });
    const doRename = async () => {
      nameLabel.contentEditable = 'false';
      const newName = nameLabel.textContent.trim();
      if (!newName || newName === d.name) {
        nameLabel.textContent = d.name; // revert if unchanged or empty
        return;
      }
      const resp = await fetch(`/api/device/${d.id}/rename`, {
        method:  'POST',
        headers: { 'Content-Type': 'application/json' },
        body:    JSON.stringify({ name: newName }),
      }).catch(() => null);
      if (resp && resp.ok) {
        d.name = newName;
        if (state.devices[d.id]) state.devices[d.id].name = newName;
        // Update all other name labels for this device (e.g. after reconnect).
        $$('.device-name-label', document.getElementById(`page-${d.id}`) || el)
          .forEach(l => { if (l !== nameLabel) l.textContent = newName; });
        renderTabs();
      } else {
        nameLabel.textContent = d.name; // revert on error
      }
    };
    nameLabel.addEventListener('blur', doRename);
    nameLabel.addEventListener('keydown', (e) => {
      if (e.key === 'Enter')  { e.preventDefault(); nameLabel.blur(); }
      if (e.key === 'Escape') { nameLabel.textContent = d.name; nameLabel.contentEditable = 'false'; }
    });
  }

  // Pre-fill port/baud from the device config.
  if (d.port) {
    if (![...portSel.options].some(o => o.value === d.port)) {
      portSel.appendChild(new Option(d.port, d.port));
    }
    portSel.value = d.port;
  }
  if (d.baud) baudSel.value = String(d.baud);
  else if (d.type === 'sangenxian') baudSel.value = '4800';

  // Refresh port list from the server when the dropdown is about to open.
  portSel.addEventListener('mousedown', async () => {
    try {
      const resp = await fetch('/api/serial-ports');
      if (!resp.ok) return;
      const ports = await resp.json();
      const cur = portSel.value;
      portSel.innerHTML = '<option value="">— select —</option>';
      (ports || []).forEach(p => portSel.appendChild(new Option(p, p)));
      // Restore previous selection if port is still listed.
      if (cur) portSel.value = cur;
    } catch (_) {}
  });

  // Connect / Disconnect button.
  btn.addEventListener('click', async () => {
    if (btn.dataset.state === 'connected') {
      // ── Disconnect ──────────────────────────────────────────────────────────
      btn.disabled = true;
      try {
        await fetch(`/api/device/${d.id}/disconnect`, { method: 'POST' });
        _setPortPanelState(indicator, btn, false);
      } finally {
        btn.disabled = false;
        refreshSerialLog();
      }
    } else {
      // ── Connect ─────────────────────────────────────────────────────────────
      const port = portSel.value;
      const baud = parseInt(baudSel.value, 10) || 115200;
      if (!port) { portSel.focus(); return; }
      btn.disabled = true;
      btn.textContent = '…';
      btn.className = 'btn-conn connecting';
      try {
        const resp = await fetch(`/api/device/${d.id}/connect`, {
          method:  'POST',
          headers: { 'Content-Type': 'application/json' },
          body:    JSON.stringify({ port, baud }),
        });
        if (resp.ok) {
          // Also update cached port/baud so the panel stays in sync.
          if (state.devices[d.id]) {
            state.devices[d.id].port = port;
            state.devices[d.id].baud = baud;
          }
          _setPortPanelState(indicator, btn, true);
        } else {
          const err = await resp.json().catch(() => ({}));
          alert('Connect failed: ' + (err.error || 'check serial port'));
          _setPortPanelState(indicator, btn, false);
        }
      } catch (_) {
        _setPortPanelState(indicator, btn, false);
      } finally {
        btn.disabled = false;
        refreshSerialLog();
      }
    }
  });

  // Set initial visual state.
  _setPortPanelState(indicator, btn, !!d.connected);
}

/** Sync indicator dot + button appearance to the connected state. */
function _setPortPanelState(indicator, btn, connected) {
  if (indicator) {
    indicator.classList.toggle('connected', connected);
    indicator.title = connected ? 'Connected' : 'Disconnected';
    // Drive the layout's data-connected attribute so CSS disables/enables controls
    const layout = indicator.closest('.psu-layout, .hub-layout');
    if (layout) layout.dataset.connected = String(connected);
  }
  if (btn) {
    btn.dataset.state = connected ? 'connected' : '';
    btn.textContent   = connected ? 'Disconnect' : 'Connect';
    btn.className     = connected ? 'btn-conn connected' : 'btn-conn';
  }
}

/** Called from updatePage to keep the port panel in sync with polled state. */
function updatePortPanel(el, connected) {
  const indicator = $('.conn-indicator', el);
  const btn       = $('.btn-conn',       el);
  // Don't override while a connect operation is in-flight.
  if (btn && btn.disabled) return;
  _setPortPanelState(indicator, btn, !!connected);
}

// ─── API bootstrap – load saved devices before WS connects ─────────────────────

async function loadFromAPI() {
  try {
    const resp = await fetch('/api/devices');
    if (!resp.ok) return;
    const devs = await resp.json();
    devs.forEach(d => {
      state.devices[d.id] = d;
      ensureDevicePage(d);
      if (d.state) {
        const page = document.getElementById(`page-${d.id}`);
        if (page) updatePage(page, d, d.state);
      }
    });
    renderTabs();
    renderSettingsDeviceList();
    if (Object.keys(state.devices).length > 0) {
      document.getElementById('page-empty')?.classList.remove('active');
      document.getElementById('devices-container').classList.remove('hidden');
    }
  } catch (e) {
    console.warn('[api] initial device load failed:', e);
  }
}

// ─── Settings page ────────────────────────────────────────────────────────────

function renderSettingsDeviceList() {
  const list = document.getElementById('settings-device-list');
  if (!list) return;
  list.innerHTML = '';
  Object.values(state.devices).forEach(d => {
    const entry = document.createElement('div');
    entry.className = 'device-entry';
    entry.innerHTML = `
      <span class="device-entry-name">${d.name}</span>
      <span class="device-entry-type">${d.type} · ${d.id}</span>
      <button class="btn-remove" data-id="${d.id}" title="Remove">✕</button>`;
    list.appendChild(entry);
  });
  $$('.btn-remove', list).forEach(b => {
    b.addEventListener('click', () => removeDevice(b.dataset.id));
  });
}

async function removeDevice(id) {
  if (!confirm(`Remove device "${id}"?`)) return;
  const resp = await fetch(`/api/config/device/${id}`, { method: 'DELETE' });
  if (resp.ok) {
    delete state.devices[id];
    document.getElementById(`page-${id}`)?.remove();
    $(`.device-tab[data-id="${id}"]`)?.remove();
    state.activeTab = null;
    renderTabs();
    renderSettingsDeviceList();
    if (Object.keys(state.devices).length === 0) {
      document.getElementById('devices-container').classList.add('hidden');
      showPage('empty');
    }
  }
}

async function addDevice(formData) {
  const dc = {
    id:      formData.id,
    name:    formData.name,
    type:    formData.type,
    port:    formData.port,
    baud:    parseInt(formData.baud, 10),
    enabled: true,
  };
  const resp = await fetch('/api/config/device', {
    method:  'POST',
    headers: { 'Content-Type': 'application/json' },
    body:    JSON.stringify(dc),
  });
  if (!resp.ok) {
    const err = await resp.json();
    alert('Error: ' + (err.error || 'unknown'));
    return;
  }
  // Optimistically add to local state; the WS 'init' or next poll will refresh
  state.devices[dc.id] = { ...dc, connected: false };
  ensureDevicePage(state.devices[dc.id]);
  document.getElementById('devices-container').classList.remove('hidden');
  document.getElementById('page-empty')?.classList.remove('active');
  renderTabs();
  renderSettingsDeviceList();
}

async function scanPorts() {
  const resp = await fetch('/api/serial-ports');
  if (!resp.ok) return;
  const ports = await resp.json();
  const cont = document.getElementById('port-suggestions');
  if (!cont) return;
  cont.innerHTML = '';
  (ports || []).forEach(p => {
    const chip = document.createElement('div');
    chip.className = 'port-chip';
    chip.textContent = p;
    chip.addEventListener('click', () => {
      document.getElementById('df-port').value = p;
      cont.classList.add('hidden');
    });
    cont.appendChild(chip);
  });
  cont.classList.toggle('hidden', !ports?.length);
}

// ─── Serial port debug log ─────────────────────────────────────────────────────

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

async function refreshSerialLog() {
  const box = document.getElementById('serial-log');
  if (!box) return;
  try {
    const resp = await fetch('/api/serial-log');
    if (!resp.ok) return;
    const data = await resp.json();
    const events = data.events || [];
    const count = document.getElementById('serial-log-count');
    if (count) count.textContent = `${events.length} event${events.length === 1 ? '' : 's'}`;
    if (events.length === 0) {
      box.innerHTML = '<div class="serial-log-empty">No serial-port activity recorded yet.</div>';
      return;
    }
    // Keep the view pinned to the newest entry unless the user scrolled up.
    const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 12;
    box.innerHTML = events.map(e => {
      const action = e.action || '';
      const label = action === 'open_failed' ? 'FAILED' : action.toUpperCase();
      const details = [
        e.owner  ? `<span class="serial-log-owner">${escapeHTML(e.owner)}</span>` : '',
        `<span>${escapeHTML(e.port || '')}${e.baud ? ' @ ' + e.baud : ''}</span>`,
        e.caller ? `<span class="serial-log-caller">${escapeHTML(e.caller)}</span>` : '',
        e.error  ? `<span class="serial-log-error">${escapeHTML(e.error)}</span>` : '',
      ].filter(Boolean).join(' · ');
      return `<div class="serial-log-row">
        <span class="serial-log-time">${escapeHTML(e.time || '')}</span>
        <span class="serial-log-action ${escapeHTML(action)}">${label}</span>
        <span class="serial-log-body">${details}</span>
      </div>`;
    }).join('');
    if (atBottom) box.scrollTop = box.scrollHeight;
  } catch (_) { /* ignore transient fetch errors */ }
}

async function clearSerialLog() {
  try { await fetch('/api/serial-log', { method: 'DELETE' }); } catch (_) {}
  refreshSerialLog();
}

/** Explicitly open every configured device.  Never called automatically. */
async function connectAll() {
  const btn = document.getElementById('btn-connect-all');
  if (btn) { btn.disabled = true; btn.textContent = 'Connecting…'; }
  const failures = [];
  for (const d of Object.values(state.devices)) {
    if (d.connected) continue;
    try {
      const resp = await fetch(`/api/device/${d.id}/connect`, {
        method:  'POST',
        headers: { 'Content-Type': 'application/json' },
        body:    JSON.stringify({ port: d.port, baud: d.baud }),
      });
      if (resp.ok) {
        d.connected = true;
      } else {
        const err = await resp.json().catch(() => ({}));
        failures.push(`${d.id} (${d.port}): ${err.error || 'connect failed'}`);
      }
    } catch (e) {
      failures.push(`${d.id}: ${e}`);
    }
  }
  if (btn) { btn.disabled = false; btn.textContent = 'Connect All'; }
  refreshSerialLog();
  if (failures.length) alert('Some devices failed to connect:\n' + failures.join('\n'));
}

let _serialLogTimer = null;
function startSerialLogAutoRefresh() {
  if (_serialLogTimer) return;
  _serialLogTimer = setInterval(() => {
    const auto = document.getElementById('serial-log-auto');
    const page = document.getElementById('page-settings');
    if (!auto || !auto.checked) return;
    if (!page || !page.classList.contains('active')) return;
    refreshSerialLog();
  }, 2000);
}

// ─── Wire settings form ────────────────────────────────────────────────────────

document.addEventListener('DOMContentLoaded', () => {
  // Brand / logo returns to the device overview.
  const brandHome = document.getElementById('brand-home');
  brandHome?.addEventListener('click', goHome);
  brandHome?.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      goHome();
    }
  });

  // Settings button
  document.getElementById('btn-settings').addEventListener('click', () => {
    state.activeTab = 'settings';
    $$('.device-tab').forEach(t => t.classList.remove('active'));
    document.getElementById('devices-container').classList.add('hidden');
    document.getElementById('page-empty')?.classList.remove('active');
    showPage('settings');
    refreshSerialLog();
  });

  // Add device button
  document.getElementById('btn-add-device')?.addEventListener('click', () => {
    const form = document.getElementById('device-form');
    if (form) form.parentElement.scrollIntoView({ behavior: 'smooth' });
  });

  // Connect all devices – explicit user action only.
  document.getElementById('btn-connect-all')?.addEventListener('click', connectAll);

  // Serial port debug panel
  document.getElementById('btn-serial-log-refresh')?.addEventListener('click', refreshSerialLog);
  document.getElementById('btn-serial-log-clear')?.addEventListener('click', clearSerialLog);
  document.getElementById('serial-log-auto')?.addEventListener('change', (e) => {
    if (e.target.checked) refreshSerialLog();
  });
  startSerialLogAutoRefresh();

  // Cancel form – reset and return to the devices view
  document.getElementById('btn-cancel-form')?.addEventListener('click', () => {
    document.getElementById('device-form')?.reset();
    // Navigate back: show devices grid if any devices exist, else empty state
    showPage('settings');  // hide settings
    document.getElementById('page-settings').classList.remove('active');
    if (Object.keys(state.devices).length > 0) {
      document.getElementById('devices-container').classList.remove('hidden');
    } else {
      showPage('empty');
    }
  });

  // Scan ports
  document.getElementById('btn-scan-ports')?.addEventListener('click', scanPorts);

  // Auto-select baud rate when device type changes
  document.getElementById('df-type')?.addEventListener('change', () => {
    const type = document.getElementById('df-type').value;
    const baud = document.getElementById('df-baud');
    if (type === 'usbalarm') baud.value = '4800';
    else if (type === 'spm3051') baud.value = '9600';
    else if (type === 'pd_pocket') baud.value = '115200';
    else if (type === 'usbslim') baud.value = '9600';
    else if (type === 'sangenxian') baud.value = '4800';
    else if (type === 'f11') baud.value = '38400';
    else if (type === 'powerz') baud.value = '115200';
  });

  // Submit device form
  document.getElementById('device-form')?.addEventListener('submit', async (e) => {
    e.preventDefault();
    await addDevice({
      id:   document.getElementById('df-id').value.trim(),
      name: document.getElementById('df-name').value.trim(),
      type: document.getElementById('df-type').value,
      port: document.getElementById('df-port').value.trim(),
      baud: document.getElementById('df-baud').value,
    });
    document.getElementById('device-form').reset();
  });

  // Load saved devices immediately from HTTP API, then open WebSocket for live updates
  loadFromAPI().then(() => connectWS());

  // Show app version badge next to the brand name
  fetch('/api/version')
    .then(r => r.json())
    .then(({ version }) => {
      if (version) document.getElementById('app-version').textContent = `v${version}`;
    })
    .catch(() => {});
});
