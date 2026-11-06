#!/usr/bin/env python3
"""Capture the actual shore operator UI through its TLS gateway, without login secrets."""
import json
import os
from pathlib import Path

from playwright.sync_api import sync_playwright


def screenshot_paths(directory, root):
    result = []
    for path in sorted(directory.glob('*.png')):
        path = path.resolve()
        try:
            path = path.relative_to(root.resolve())
        except ValueError:
            pass
        result.append(str(path))
    return result


def main():
    root = Path(__file__).resolve().parents[2]
    report = root/'docs/reports/uds-validation'
    evidence = Path(os.environ.get('EVIDENCE_DIR', str(report/'step3-priority')))
    directory = Path(os.environ.get('SCREENSHOT_DIR', str(report/'img')))
    directory.mkdir(exist_ok=True)
    state = Path(os.environ.get('STATE', str(Path.home()/'.local/share/uds-two-cluster')))
    token = (state/'auth/operator-token').read_text().strip()
    records = json.loads((evidence/'backlog-arrival-order.json').read_text())
    identity = records[0]['id']
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.connect_over_cdp('http://localhost:29229')
            context = browser.new_context(viewport={'width': 1600, 'height': 1050})
            try:
                page = context.new_page()
                page.goto('https://ghost-seeker.uds.dev:9443', wait_until='domcontentloaded')
                page.locator('input[name=token]').fill(token)
                page.get_by_role('button', name='Sign in', exact=True).click()
                card = page.locator(f'#grid .card[data-id="item:{identity}"]')
                card.wait_for(state='visible', timeout=30000)
                page.wait_for_function('document.fonts.status === "loaded"')
                page.wait_for_function("[...document.querySelectorAll('#grid .card img')].every(i => i.complete)")
                detail = None
                if os.environ.get('SCREENSHOT_STATUS_ONLY') != '1':
                    if os.environ.get('SCREENSHOT_DETAIL_ONLY') != '1':
                        page.screenshot(path=str(directory/'shore-feed.png'))
                    card.click()
                    page.locator('#dClose').wait_for(state='visible')
                    page.wait_for_function("[...document.querySelectorAll('#dImg img')].every(i => i.complete && i.naturalWidth > 0)")
                    page.locator('#drawer').screenshot(path=str(directory/'shore-item-detail.png'))
                    detail = page.locator('.dhead').inner_text()
                    if os.environ.get('SCREENSHOT_DETAIL_ONLY') == '1':
                        metadata = json.loads((evidence/'visual-check.json').read_text())
                        metadata['detail_text'] = detail
                        (evidence/'visual-check.json').write_text(json.dumps(metadata, indent=2)+'\n')
                        print('Captured complete P1 detail drawer: '+str(directory/'shore-item-detail.png'))
                        return
                    page.locator('#dClose').click()
                page.goto('https://ghost-seeker.uds.dev:9443/#details', wait_until='domcontentloaded')
                page.get_by_role('heading', name='Pipeline counts').wait_for(state='visible')
                page.screenshot(path=str(directory/'shore-link-sync-status.png'))
                status = page.locator('#detailsBody').inner_text()
                (evidence/'visual-check.json').write_text(json.dumps(
                    {'url': page.url, 'item_id': identity, 'detail_text': detail, 'status_text': status,
                     'tls_verification_disabled': False, 'screenshots': screenshot_paths(directory, root)}, indent=2)+'\n')
                print('Captured shore feed, P1 item detail and pipeline status: '+str(directory))
            finally:
                context.close()
    except Exception as error:
        raise SystemExit(str(error).replace(token, '[REDACTED]')) from None


if __name__ == '__main__':
    main()
