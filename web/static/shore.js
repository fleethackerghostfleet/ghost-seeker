// Operator UI in live-shore mode: everything on screen comes from the shore
// ConsoleService through the Go bridge (/api/shore/*). No simulation here.
(() => {
  const $ = (s, r = document) => r.querySelector(s);
  let snap = null;
  let view = 'feed';
  const decided = new Map(JSON.parse(localStorage.getItem('shore.decided') || '[]'));
  const escalated = new Set(JSON.parse(localStorage.getItem('shore.escalated') || '[]'));
  const factCache = new Map();
  const pulling = new Set();

  const fmtBytes = n => n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1048576).toFixed(2)} MB`;
  const fmtRate = bps => bps >= 1e6 ? `${(bps / 1e6).toFixed(1)} Mbps` : `${(bps / 1000).toFixed(bps < 10000 ? 1 : 0)} kbps`;
  const presetName = bps => bps === 0 ? 'OFF' : bps >= 1e6 ? `${bps / 1e6} Mbps` : `${bps / 1000} kbps`;
  const ago = ms => { const s = Math.max(0, (Date.now() - ms) / 1000); return s < 60 ? `${s | 0}s ago` : s < 3600 ? `${(s / 60) | 0}m ago` : `${(s / 3600) | 0}h ago`; };
  const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  function toast(msg, bad) {
    const t = $('#toast'); t.textContent = msg; t.style.borderColor = bad ? 'var(--bad)' : 'var(--accent)';
    t.classList.add('show'); clearTimeout(toast.h); toast.h = setTimeout(() => t.classList.remove('show'), 2500);
  }
  async function post(url, body) {
    const r = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const text = await r.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch {}
    if (!r.ok) {
      if (data?.conflicts?.length) toast(window.gsPolicy.conflictToast(data.conflicts), true);
      throw new Error(data?.error || `${r.status} ${text}`);
    }
    return data;
  }

  // ---- status strip ----
  function renderStrip() {
    const { shore, edge, link, counts, policy } = snap;
    const policyInfo = snap.policyInfo || {};
    const c = $('#conn');
    c.textContent = shore.ok ? `shore ok · ${new Date(snap.ts).toLocaleTimeString()}` : `shore: ${shore.error || 'unreachable'}`;
    c.className = 'conn ' + (shore.ok ? 'ok' : 'bad');
    const kbps = link.controllable ? link.bps : (shore.estimateBps || 0);
    $('#kbps').textContent = fmtRate(kbps);
    const est = edge.ok ? edge.estimateBps : shore.estimateBps;
    const relearn = edge.ok && edge.consecutiveFailures > 0
      ? ` · re-learning: ${edge.consecutiveFailures} failed exchange${edge.consecutiveFailures === 1 ? '' : 's'}` : '';
    $('#link-src').textContent = link.controllable
      ? (est ? `edge estimates ${fmtRate(est)}${relearn}` : '')
      : '(edge estimate; control via degrade.sh)';
    const pr = $('#presets');
    if (!pr.childElementCount) {
      for (const p of link.presets) {
        const b = document.createElement('button'); b.textContent = presetName(p); b.dataset.bps = p;
        b.disabled = !link.controllable;
        b.onclick = async () => { try { await post('/api/shore/link', { bps: p }); toast(`link → ${presetName(p)}`); } catch (e) { toast(e.message, true); } };
        pr.appendChild(b);
      }
    }
    for (const b of pr.children) b.classList.toggle('on', link.controllable && +b.dataset.bps === link.bps);
    $('#items-shore').textContent = shore.ok ? shore.itemsTotal : '—';
    $('#items-edge').textContent = edge.ok ? edge.itemsTotal : (edge.error ? 'n/a' : '—');
    $('#edge-queue').textContent = edge.ok ? `prioritization: ${edge.prioritizer || 'edgeml'} · ${edge.queuedItems} queued, ${fmtBytes(edge.queuedBytes)} · ${edge.itemsEvicted || 0} delivered items evicted${edge.itemsDeduped ? ` · ${edge.itemsDeduped} duplicate frames folded on the edge` : ''}` : '';
    const sightingMode = counts.grouped > 0;
    $('#obj-img').textContent = sightingMode ? counts.sightingsWithImage : counts.objectsWithImage;
    $('#obj-all').textContent = sightingMode ? counts.sightings : counts.objects;
    $('#obj-note').textContent = sightingMode ? `sightings (by sighting_id)${counts.scenes ? ` · ${counts.scenes} camera heartbeats not counted` : ''}` : `(no sighting_ids yet: ${counts.items} items, 1 object each)`;
    $('#bytes').textContent = fmtBytes(link.controllable ? link.bytes : shore.bytes);
    $('#bytes-src').textContent = link.controllable ? `through the shaper · shore counted ${fmtBytes(shore.bytes)}` : 'received by shore';
    $('#policy').textContent = (policy ? `“${policy}”` : 'none set') + (!snap.policyInfo && snap.emcon ? ` · EMCON ${snap.emcon}` : '');
    const focusedMode = document.activeElement?.closest?.('#shore-emcon button')?.dataset.mode;
    const mode = String(policyInfo.emcon || snap.emcon || 'NORMAL').toUpperCase();
    const lockTitle = policyInfo.emconLocked ? `EMCON locked by ${policyInfo.emconLockedBy || policyInfo.emconSetBy || 'higher echelon'}` : '';
    $('#shore-emcon').innerHTML = `<div class="shore-emcon" role="group" aria-label="Go quiet (EMCON)" tabindex="-1" ${lockTitle ? `title="${esc(lockTitle)}"` : ''}><b>EMCON</b>${['NORMAL', 'BURST', 'SILENT'].map(value => `<button type="button" data-mode="${value}" aria-pressed="${mode === value}" class="${mode === value ? 'on' : ''}" ${policyInfo.emconLocked ? 'disabled' : ''} ${lockTitle ? `title="${esc(lockTitle)}"` : ''}>${value[0] + value.slice(1).toLowerCase()}</button>`).join('')}</div>`;
    if (focusedMode) {
      const button = $('#shore-emcon').querySelector(`[data-mode="${focusedMode}"]`);
      (button && !button.disabled ? button : $('#shore-emcon').querySelector('.shore-emcon'))?.focus({ preventScroll: true });
    }
    const locks = (policyInfo.locks || []).map(lock => `${esc(lock.lockedBy)}: ${esc((lock.labels || []).join(', '))}`).join(' · ');
    const conflicts = (policyInfo.conflicts || []).map(conflict => `${esc(conflict.ruleId)} — ${esc(conflict.reason)}`).join(' · ');
    $('#policy-info').innerHTML = [locks && `Locks: ${locks}`, conflicts && `Conflicts: ${conflicts}`].filter(Boolean).join(' | ');
    const review = snap.objects.filter(o => !decided.has(o.key) && !o.suppressed && !o.scene && (o.sightingId || !sightingMode));
    $('#review-n').textContent = review.length;
  }

  // ---- cards ----
  function formChips(card) {
    return ['fact', 'thumb', 'crop', 'full'].map(k => {
      const f = card.forms[k] || { state: 'none' };
      const title = f.state === 'received' ? `${k}: received (${fmtBytes(f.bytes)})`
        : f.state === 'partial' ? `${k}: ${fmtBytes(f.received)} of ${fmtBytes(f.bytes)}`
        : f.state === 'pending' ? `${k}: pending (${fmtBytes(f.bytes)})`
        : f.state === 'offered' ? `${k}: offered by edge, not requested` : `${k}: not available`;
      return `<span class="chip ${f.state}" title="${esc(title)}">${k}${f.state === 'partial' ? ` ${Math.round(100 * f.received / Math.max(1, f.bytes))}%` : ''}</span>`;
    }).join('');
  }
  function imageBlock(card, cls = 'img') {
    if (card.image) return `<div class="${cls}"><img src="${card.image}?v=${card.forms[card.imageForm]?.bytes || 0}" loading="lazy" alt=""><span class="form">${card.imageForm}</span></div>`;
    const fact = factCache.get(card.id);
    if (fact === undefined) loadFact(card);
    return `<div class="${cls} fact" title="no image form received yet">${fact ? esc(fact) : 'fact only — image pending'}</div>`;
  }
  async function loadFact(card) {
    factCache.set(card.id, null);
    try {
      const r = await fetch(card.factUrl);
      if (!r.ok) { // fact not on shore yet (409): retry on a later render
        setTimeout(() => factCache.delete(card.id), 5000);
        return;
      }
      const j = await r.json().catch(() => null);
      const text = j ? Object.entries(j).filter(([k]) => !/embedding|hash/i.test(k)).map(([k, v]) => `${k}: ${typeof v === 'object' ? JSON.stringify(v) : v}`).join('\n') : await r.text();
      factCache.set(card.id, text.slice(0, 400));
      render();
    } catch { setTimeout(() => factCache.delete(card.id), 5000); }
  }
  // Cameras strip: per-source heartbeat built from object-free "scene" items
  // (falling back to the latest contact's image), instead of scene tiles in the feed.
  function renderCameras(objs, sightingMode) {
    const cams = new Map();
    for (const o of objs) {
      const src = o.best.source || 'camera';
      const c = cams.get(src) || { src, lastMs: 0, scene: null, latest: null, contacts: 0 };
      c.lastMs = Math.max(c.lastMs, o.lastMs);
      if (o.scene) { if (!c.scene || o.lastMs > c.scene.lastMs) c.scene = o; }
      else {
        if (!c.latest || o.lastMs > c.latest.lastMs) c.latest = o;
        if (o.sightingId || !sightingMode) c.contacts++; // untagged per-frame items are not contacts in sightings mode
      }
      cams.set(src, c);
    }
    const el = $('#cameras');
    if (!cams.size) { el.innerHTML = ''; return 0; }
    el.innerHTML = [...cams.values()].sort((a, b) => a.src.localeCompare(b.src)).map(c => {
      const v = c.scene || c.latest; // prefer the object-free context frame; else the newest contact's image
      const b = v?.best;
      const img = b?.image ? `<img src="${b.image}?v=${b.forms[b.imageForm]?.bytes || 0}" alt="">` : '<div class="noimg">no view yet</div>';
      const full = b?.forms.full?.state;
      const pull = b ? `<button class="btn mini" data-act="pull" data-id="${esc(b.id)}" ${pulling.has(b.id) || full === 'received' ? 'disabled' : ''}>${full === 'received' ? 'full frame received' : full === 'partial' ? 'full frame…' : pulling.has(b.id) ? 'requested…' : 'Pull full frame'}</button>` : '';
      return `<div class="cam" title="${c.scene ? 'Latest object-free context frame from this camera (edge heartbeat, ~2 KB/min). Not a contact.' : 'Latest contact image from this camera (no heartbeat frame received yet).'}">
        ${img}
        <div class="meta"><div class="k">${esc(c.src)}</div><div class="u">seen ${ago(c.lastMs)} · ${c.contacts} contact${c.contacts === 1 ? '' : 's'}${v ? ` · view ${ago(v.lastMs)}` : ''}</div>${pull}</div>
      </div>`;
    }).join('');
    return cams.size;
  }
  function objectCard(o) {
    const b = o.best;
    // Zero-shot subtype is the operator-facing name; the detector class stays in the meta line.
    const label = b.override || o.subtype || o.label || 'unknown';
    const sub = o.subtype && o.label && o.label !== o.subtype ? ` · detector: ${esc(o.label)}` : '';
    const frames = !o.sightingId ? '1 frame' : o.framesKnown ? `${o.frames} frames seen` : `${o.reports} report${o.reports === 1 ? '' : 's'}`;
    const canPull = !pulling.has(b.id) && b.forms.full?.state !== 'received';
    return `<div class="card ${o.priority} ${o.suppressed ? 'suppressed' : ''}" data-key="${esc(o.key)}">
      ${imageBlock(b)}
      <div class="body">
        <div class="title"><span class="label">${esc(label)}${o.unknown ? ' <span class="muted">(unknown)</span>' : ''}</span><span class="tier ${o.priority}">${o.priority}${o.suppressed ? ' · suppressed' : ''}</span></div>
        <div class="meta">${b.conf ? `${Math.round(b.conf * 100)}% · ` : ''}${frames}${sub}${o.event ? ` · ${esc(o.event)}` : ''} · ${ago(o.lastMs)} · ${esc(b.source)}</div>
        <div class="meta">${o.sightingId ? `sighting ${esc(o.sightingId)}` : `item ${esc(b.id.slice(-8))}`}</div>
        <div class="forms">${formChips(b)}</div>
        <div class="actions">
          <button class="btn" data-act="pull" data-id="${esc(b.id)}" ${canPull ? '' : 'disabled'}>${b.forms.full?.state === 'received' ? 'full received' : b.forms.full?.state === 'pending' || b.forms.full?.state === 'partial' ? 'full pending…' : 'Pull full image'}</button>${b.forms.full ? `<button class="btn" data-act="fullsize" data-id="${esc(b.id)}" title="Operator override: send the original image at full size, no compression">Send full size</button>` : ''}
          <button class="btn" data-act="escalate" data-key="${esc(o.key)}">${escalated.has(o.key) ? 'Escalated ✓' : 'Escalate to CDR'}</button>
        </div>
      </div></div>`;
  }
  function reviewRow(o) {
    const b = o.best, d = decided.get(o.key);
    return `<div class="row ${o.priority} ${d ? 'done' : ''}" data-key="${esc(o.key)}">
      ${imageBlock(b)}
      <div>
        <div class="title"><span class="label">${esc(b.override || o.subtype || o.label || 'unknown')}</span> <span class="tier ${o.priority}">${o.priority}</span> ${d ? `<span class="muted">· ${esc(d)}</span>` : ''}</div>
        <div class="meta">${o.sightingId ? `sighting ${esc(o.sightingId)} · ${o.framesKnown ? `${o.frames} frames` : `${o.reports} reports`}` : `item ${esc(b.id)}`}${o.subtype ? ` · ${esc(o.subtype)}` : ''} · unknownness ${b.unknownness.toFixed(2)} · ${ago(o.lastMs)}</div>
        <div class="forms">${formChips(b)}</div>
        <div class="actions">
          <button class="btn primary" data-act="confirm" data-key="${esc(o.key)}">Confirm ${esc(b.override || o.subtype || o.label || '')}</button>
          <button class="btn" data-act="relabel" data-key="${esc(o.key)}" data-id="${esc(b.id)}">Relabel…</button>
          <button class="btn" data-act="pull" data-id="${esc(b.id)}" ${b.forms.full?.state === 'received' ? 'disabled' : ''}>Pull full image</button>${b.forms.full ? `<button class="btn" data-act="fullsize" data-id="${esc(b.id)}" title="Operator override: send the original image at full size, no compression">Send full size</button>` : ''}
          <button class="btn" data-act="escalate" data-key="${esc(o.key)}">Escalate to CDR</button>
          <button class="btn" data-act="dismiss" data-key="${esc(o.key)}">Dismiss</button>
        </div>
      </div></div>`;
  }

  function render() {
    if (!snap) return;
    renderStrip();
    const objs = snap.objects;
    if (view === 'feed') {
      const sightingMode = snap.counts.grouped > 0;
      const ncams = renderCameras(objs, sightingMode);
      let list = objs.filter(o => !o.scene); // scenes live in the cameras strip
      const onlySightings = $('#only-sightings').checked && sightingMode;
      if (onlySightings) list = list.filter(o => o.sightingId);
      if ($('#hide-suppressed').checked) list = list.filter(o => !o.suppressed);
      if ($('#only-image').checked) list = list.filter(o => o.hasImage);
      const hidden = objs.filter(o => !o.scene).length - list.length;
      $('#feed-note').textContent = `${list.length} objects · newest first${onlySightings && hidden ? ` · ${hidden} object-free frames hidden` : ''}`;
      const empty = ncams ? `No contacts yet. ${ncams} camera${ncams === 1 ? '' : 's'} reporting${hidden ? `; ${hidden} item${hidden === 1 ? '' : 's'} hidden by filters` : ''}.` : 'No items on shore yet. Start the edge and open the link.';
      $('#feed').innerHTML = list.length ? list.map(objectCard).join('') : `<div class="empty">${empty}</div>`;
    } else if (view === 'review') {
      const order = { P1: 0, P2: 1, P3: 2, P4: 3 };
      const list = objs.filter(o => !o.suppressed && !o.scene && (o.sightingId || snap.counts.grouped === 0)).sort((a, b) => (decided.has(a.key) - decided.has(b.key)) || (b.unknown - a.unknown) || (order[a.priority] - order[b.priority]) || (b.lastMs - a.lastMs));
      $('#review').innerHTML = list.length ? list.map(reviewRow).join('') : '<div class="empty">Queue empty.</div>';
    } else {
      const list = objs.filter(o => !o.scene && (escalated.has(o.key) || o.priority === 'P1')).sort((a, b) => escalated.has(b.key) - escalated.has(a.key) || b.lastMs - a.lastMs);
      $('#cdr').innerHTML = list.length ? list.map(objectCard).join('') : '<div class="empty">Nothing escalated. Use “Escalate to CDR” on a card.</div>';
    }
  }

  // ---- actions ----
  document.addEventListener('click', async e => {
    const b = e.target.closest('button[data-act]');
    if (!b) return;
    const { act, id, key } = b.dataset;
    try {
      if (act === 'pull') {
        pulling.add(id); b.disabled = true; b.textContent = 'requesting…';
        await post('/api/shore/pull', { itemId: id, form: 'full' });
        toast('full image requested from edge');
      } else if (act === 'fullsize') {
        const by = prompt('Operator override: send this image at FULL SIZE with NO compression.\nThis uses much more link bandwidth than the usual thumbnail/crop.\n\nEnter your name or operator ID to confirm:');
        if (by === null) return;
        if (!by.trim()) { toast('not sent: operator name is required', true); return; }
        await post('/api/shore/pull', { itemId: id, form: 'full', original: true, by: by.trim() });
        toast(`full-size original requested by ${by.trim()}`);
      } else if (act === 'escalate') {
        escalated.has(key) ? escalated.delete(key) : escalated.add(key);
        localStorage.setItem('shore.escalated', JSON.stringify([...escalated]));
      } else if (act === 'confirm' || act === 'dismiss') {
        decided.set(key, act === 'confirm' ? 'confirmed' : 'dismissed');
        localStorage.setItem('shore.decided', JSON.stringify([...decided]));
      } else if (act === 'relabel') {
        const label = prompt('Operator label for this object (sent to shore as an override):');
        if (!label) return;
        await post('/api/shore/override', { itemId: id, label });
        decided.set(key, `relabelled ${label}`);
        localStorage.setItem('shore.decided', JSON.stringify([...decided]));
        toast(`override sent: ${label}`);
      }
      render();
    } catch (err) {
      if (act === 'pull') pulling.delete(id);
      toast(err.message, true);
      render();
    }
  });
  $('#directive-form').addEventListener('submit', async e => {
    e.preventDefault();
    const text = $('#directive').value.trim();
    if (!text) return;
    try {
      const r = await post('/api/shore/policy', { text });
      const c = r.compiled;
      if (c) {
        $('#readback').textContent = `${c.readback} [${c.path}]${c.warnings.length ? ' — ' + c.warnings.join(' ') : ''}`;
        toast(`policy set (${c.path}): ${c.readback}`);
      } else {
        $('#readback').textContent = '';
        toast(`policy set${r.policyId ? ` (${r.policyId.slice(0, 8)})` : ''}`);
      }
      const conflicts = r.policy?.conflicts || r.conflicts;
      if (conflicts?.length) toast(window.gsPolicy.conflictToast(conflicts), true);
      $('#directive').value = '';
    } catch (err) { toast(err.message, true); }
  });
  $('#shore-emcon').addEventListener('click', async e => {
    const button = e.target.closest('button[data-mode]');
    if (!button || button.disabled) return;
    try {
      const response = await post('/api/shore/emcon', { mode: button.dataset.mode });
      if (response.conflicts?.length) toast(window.gsPolicy.conflictToast(response.conflicts), true);
      else toast(`EMCON set to ${button.dataset.mode}`);
    } catch (err) { toast(err.message, true); }
  });
  for (const t of document.querySelectorAll('.tab')) t.onclick = () => {
    view = t.dataset.view;
    document.querySelectorAll('.tab').forEach(x => x.classList.toggle('active', x === t));
    document.querySelectorAll('.view').forEach(v => v.classList.toggle('active', v.id === 'view-' + view));
    render();
  };
  $('#hide-suppressed').onchange = $('#only-image').onchange = $('#only-sightings').onchange = render;

  // ---- data ----
  function connect() {
    const es = new EventSource('/api/shore/stream');
    es.addEventListener('snapshot', ev => { snap = JSON.parse(ev.data); render(); });
    es.onerror = () => { $('#conn').textContent = 'bridge disconnected — retrying'; $('#conn').className = 'conn bad'; };
  }
  fetch('/api/shore/snapshot').then(r => r.json()).then(s => { snap = s; render(); }).catch(() => {});
  connect();
  setInterval(() => { if (snap) renderStrip(); }, 5000);
})();
