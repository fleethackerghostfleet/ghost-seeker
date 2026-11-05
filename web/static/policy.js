(() => {
  const esc = value => String(value ?? '').replace(/[&<>"']/g, char => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[char]));

  function emconHTML(summary = {}) {
    const emcon = summary.emcon || {};
    const mode = String(emcon.mode || 'NORMAL').toUpperCase();
    const lockedBy = emcon.locked ? `EMCON locked by ${emcon.lockedBy || emcon.setBy || 'higher echelon'}` : '';
    const buttons = ['NORMAL', 'BURST', 'SILENT'].map(value =>
      `<button type="button" data-mode="${value}" class="${mode === value ? 'on' : ''}" aria-pressed="${mode === value}" ${emcon.locked ? 'disabled' : ''} ${lockedBy ? `title="${esc(lockedBy)}"` : ''}>${value[0] + value.slice(1).toLowerCase()}</button>`
    ).join('');
    return `<div class="seg emcon" id="emconSeg" role="group" aria-label="Go quiet (EMCON)" tabindex="-1" ${lockedBy ? `title="${esc(lockedBy)}"` : ''}><span class="emcon-label" title="EMCON: transmission control. In live operation, Silent stops ship-to-shore transfers; stored images remain on the ship. Normal resumes transfers; Burst limits them. The demonstration feed continues.">Go quiet</span>${buttons}</div>`;
  }

  function directiveBadge(directive = {}) {
    if (directive.source) {
      return directive.locked
        ? `<span class="policy-badge locked">🔒 Locked by ${esc(directive.lockedBy || directive.source)}</span>`
        : `<span class="policy-badge delegated">From ${esc(directive.source)} · delegated</span>`;
    }
    return directive.blocked
      ? `<span class="policy-badge blocked">Blocked — ${esc(directive.blocked)}</span>`
      : '';
  }

  function conflictsHTML(conflicts = []) {
    if (!conflicts.length) return '';
    return `<ul class="policy-conflicts">${conflicts.map(conflict => `<li><b>${esc(conflict.ruleId || 'policy')}</b> — ${esc(conflict.reason || 'Conflict')} <small>(${esc(conflict.blockedBy || 'unknown')})</small></li>`).join('')}</ul>`;
  }

  function conflictToast(conflicts = []) {
    const count = conflicts.length;
    if (!count) return '';
    const reason = conflicts[0]?.reason || 'policy conflict';
    return `${count} rule${count === 1 ? '' : 's'} dropped — ${reason}`;
  }

  function auditRowsHTML(entries = []) {
    return entries.map(entry => {
      const at = new Date(Number(entry.atUnixMs) || 0).toISOString().slice(11, 19) + 'Z';
      const result = String(entry.result || '');
      return `<tr><td>${esc(at)}</td><td>${esc(entry.who || '')}</td><td>${esc(entry.source || '')}</td><td>${esc(entry.version ?? '')}</td><td title="${esc(entry.text || '')}">${esc(String(entry.text || '').slice(0, 80))}</td><td class="audit-${esc(result)}">${esc(result)}</td><td>${esc(entry.reason || '')}</td></tr>`;
    }).join('');
  }

  function isPolicyHotkeyTarget(element) {
    return !!element?.closest?.('#emconSeg, #policyAdmin');
  }

  const helpers = { emconHTML, directiveBadge, conflictsHTML, conflictToast, auditRowsHTML, isPolicyHotkeyTarget };
  if (typeof module !== 'undefined') module.exports = helpers;
  if (typeof window !== 'undefined') window.gsPolicy = helpers;
})();
