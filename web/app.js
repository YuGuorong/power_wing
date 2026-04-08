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
function isHub(type) { return type === 'usbslim' || type === 'usb_hub'; }

function ensureDevicePage(d) {
  if (document.getElementById(`page-${d.id}`)) return;

  const tplId = isHub(d.type) ? 'tpl-hub' : 'tpl-psu';
  const tpl = document.getElementById(tplId);
  if (!tpl) return;

  const clone = tpl.content.firstElementChild.cloneNode(true);
  clone.id = `page-${d.id}`;
  clone.dataset.deviceId = d.id;

  if (isHub(d.type)) {
    buildHubPage(clone, d);
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

function buildHubPage(el, d) {
  const portsRow = $('.ports-row', el);
  for (let i = 1; i <= 4; i++) {
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
  // Debounce UI redraws: coalesce bursts within 20ms
  debounce(`ui:${d.id}`, () => {
    if (isHub(d.type)) {
      updateHubPage(page, s, d.id);
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

// ─── Wire settings form ────────────────────────────────────────────────────────

document.addEventListener('DOMContentLoaded', () => {
  // Settings button
  document.getElementById('btn-settings').addEventListener('click', () => {
    state.activeTab = 'settings';
    $$('.device-tab').forEach(t => t.classList.remove('active'));
    document.getElementById('devices-container').classList.add('hidden');
    document.getElementById('page-empty')?.classList.remove('active');
    showPage('settings');
  });

  // Add device button
  document.getElementById('btn-add-device')?.addEventListener('click', () => {
    const form = document.getElementById('device-form');
    if (form) form.parentElement.scrollIntoView({ behavior: 'smooth' });
  });

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
});
