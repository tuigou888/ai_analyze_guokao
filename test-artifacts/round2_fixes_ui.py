"""Round-two real-browser regression. Use only an isolated database.
Usage: python3 test-artifacts/round2_fixes_ui.py http://127.0.0.1:18082 TEMP_DB
Does not call paid model or OCR endpoints.
"""
import json, sqlite3, sys, time
from pathlib import Path
from playwright.sync_api import sync_playwright, expect

base = sys.argv[1].rstrip('/')
db = sqlite3.connect(f'file:{sys.argv[2]}?mode=ro', uri=True)
output = Path('test-artifacts/round2-fixes')
output.mkdir(exist_ok=True)
report = {'checks': [], 'console_errors': []}

with sync_playwright() as pw:
    browser = pw.chromium.launch(executable_path='/usr/bin/google-chrome', headless=True, args=['--no-sandbox'])
    context = browser.new_context(viewport={'width': 1440, 'height': 1000}, reduced_motion='reduce')
    page = context.new_page()
    page.on('pageerror', lambda e: report['console_errors'].append(str(e)))
    page.goto(base + '/login')

    def api(path, method='GET', data=None):
        return page.evaluate('''async ({path,method,data}) => {
          const options={method,headers:{'Content-Type':'application/json'},credentials:'same-origin'};
          if(data!==null)options.body=JSON.stringify(data);
          const res=await fetch(path,options);return {status:res.status,body:await res.json()};
        }''', {'path': '/api' + path, 'method': method, 'data': data})

    username = 'round2_' + str(time.time_ns())
    assert api('/auth/register', 'POST', {'username': username, 'password': 'round2-test-password'})['status'] == 201
    assert api('/auth/login', 'POST', {'username': username, 'password': 'round2-test-password'})['status'] == 200

    def create(ids):
        result = api('/practice/sessions', 'POST', {'kind': 'single', 'spec': {'question_ids': ids}})
        assert result['status'] == 200, result
        return result['body']['session_id']

    def open_practice(tab, sid):
        tab.goto(base + f'/practice?session={sid}')
        expect(tab.locator('.options')).to_be_visible()

    def choose(tab, letter):
        tab.locator('.option').filter(has=tab.locator('.option-letter', has_text=letter)).click()

    def saved(tab):
        expect(tab.get_by_text('作答已保存', exact=True)).to_be_visible(timeout=10000)

    def late_ack(ids, first, second):
        sid = create(ids)
        open_practice(page, sid)
        pending = []
        pattern = f'**/api/practice/sessions/{sid}/answers'
        page.route(pattern, lambda route: pending.append(route))
        choose(page, first)
        expect(page.get_by_text('正在保存…', exact=True)).to_be_visible()
        assert len(pending) == 1
        choose(page, second)
        with page.expect_response(lambda r: r.url.endswith(f'/{sid}/answers') and r.status == 200):
            pending.pop(0).continue_()
        page.evaluate('() => new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))')
        assert not page.get_by_text('作答已保存', exact=True).count(), 'old save response marked newer answers as saved'
        assert page.evaluate('''() => { const e=new Event('beforeunload',{cancelable:true});
          window.dispatchEvent(e);return e.defaultPrevented }'''), 'unsaved answer change did not guard refresh'
        expect(page.get_by_text('正在保存…', exact=True)).to_be_visible()
        assert len(pending) == 1
        pending.pop().continue_()
        page.unroute(pattern)
        saved(page)
        return sid

    sid = late_ack([12932], 'A', 'B')
    assert api(f'/practice/sessions/{sid}')['body']['answers'][0]['answer'] == 'B'
    report['checks'].append('late save acknowledgement preserves dirty status and refresh guard')

    multi = db.execute("SELECT id FROM question WHERE answer_type='multi' AND option_count>1 LIMIT 1").fetchone()[0]
    sid = late_ack([multi], 'A', 'A')
    assert not api(f'/practice/sessions/{sid}')['body']['answers'][0]['answer']
    report['checks'].append('clearing the last multi-choice answer still guards unsaved changes')

    sid = create([12932, 10])
    open_practice(page, sid)
    other = context.new_page()
    other.on('pageerror', lambda e: report['console_errors'].append(str(e)))
    open_practice(other, sid)
    choose(page, 'H')
    saved(page)
    other.locator('.question-index button').nth(1).click()
    choose(other, 'B')
    expect(other.get_by_role('button', name='加载已保存草稿', exact=True)).to_be_visible()
    expect(other.locator('.option.selected .option-letter')).to_have_text('B')
    expect(other.get_by_role('button', name='提交练习', exact=True).first).to_be_disabled()
    assert api(f'/practice/sessions/{sid}')['body']['answers'][0]['answer'] == 'H'
    other.set_viewport_size({'width': 375, 'height': 812})
    assert other.evaluate('document.documentElement.scrollWidth<=window.innerWidth')
    other.screenshot(path=str(output / 'draft-conflict-mobile.png'), full_page=True)
    other.once('dialog', lambda dialog: dialog.dismiss())
    other.get_by_role('button', name='加载已保存草稿', exact=True).click()
    expect(other.locator('.option.selected .option-letter')).to_have_text('B')
    other.once('dialog', lambda dialog: dialog.accept())
    other.get_by_role('button', name='加载已保存草稿', exact=True).click()
    saved(other)
    expect(other.locator('.option.selected')).to_have_count(0)
    choose(other, 'B')
    saved(other)
    answers = {a['question_id']: a['answer'] for a in api(f'/practice/sessions/{sid}')['body']['answers']}
    assert answers == {12932: 'H', 10: 'B'}, answers
    report['checks'].append('two tabs reject stale writes, retain local choices, confirm reload and preserve newest draft')

    pending = []
    pattern = f'**/api/practice/sessions/{sid}/submit'
    other.route(pattern, lambda route: pending.append(route))
    with other.expect_request(lambda r: r.url.endswith(f'/{sid}/submit') and r.method == 'POST'):
        other.get_by_role('button', name='提交练习', exact=True).first.click()
    expect(other.get_by_text('正在判分…', exact=True)).to_be_visible()
    expect(other.locator('.question-index button').first).to_be_disabled()
    expect(other.locator('.practice-actions button').first).to_be_disabled()
    other.wait_for_timeout(50)  # Dispatch the intercepted request callback.
    assert pending, "submission request did not start"
    pending.pop().continue_()
    other.unroute(pattern)
    expect(other.get_by_role('heading', name='本次正确率 100%')).to_be_visible()
    report['checks'].append('submission freezes navigation and returns correct final result')

    # Same path + changed query uses Vue Router's update guard, not leave.
    first_sid, second_sid = create([12932]), create([10])
    open_practice(page, first_sid)
    choose(page, 'H')
    assert api(f'/practice/sessions/{first_sid}')['body']['answers'] == []
    page.evaluate("url => document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(url)",
                  f'/practice?session={second_sid}')
    expect(page).to_have_url(base + f'/practice?session={second_sid}')
    expect(page.locator('.options')).to_be_visible()
    answers = api(f'/practice/sessions/{first_sid}')['body']['answers']
    assert len(answers) == 1 and answers[0]['answer'] == 'H', 'query navigation discarded unsaved answers'
    report['checks'].append('same-route query navigation flushes the previous session before remounting')

    page.goto(base + '/account')
    page.get_by_role('tab', name='账户安全', exact=True).click()
    page.get_by_label('当前密码', exact=True).fill('wrong-current-password')
    page.get_by_label('新密码', exact=True).fill('new-round2-test-password')
    page.get_by_label('确认新密码', exact=True).fill('new-round2-test-password')
    page.get_by_role('button', name='修改密码', exact=True).click()
    expect(page.get_by_role('alert').filter(has_text='原密码错误')).to_be_visible()
    assert page.url == base + '/account'
    assert api('/auth/me')['status'] == 200
    report['checks'].append('wrong current password retains account page and authenticated session')
    browser.close()

assert not report['console_errors'], report['console_errors']
(output / 'ui-results.json').write_text(json.dumps(report, ensure_ascii=False, indent=2))
print(json.dumps(report, ensure_ascii=False, indent=2))
