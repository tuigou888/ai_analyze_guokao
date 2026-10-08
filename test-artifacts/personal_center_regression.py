"""Real browser personal-center regression against an isolated service only.
Usage: python3 test-artifacts/personal_center_regression.py BASE [ISOLATED_DB] [--red|--profile-ordering|--cross-tab-account|--late-identity-conflict|--late-security-password|--late-security-revoke|--late-current-logout]
"""
import datetime, json, sqlite3, sys, time
from pathlib import Path
from playwright.sync_api import sync_playwright, expect

base = sys.argv[1].rstrip('/')
red = '--red' in sys.argv
profile_ordering_only = '--profile-ordering' in sys.argv
cross_tab_only = '--cross-tab-account' in sys.argv
late_identity = '--late-identity-conflict' in sys.argv
late_password = '--late-security-password' in sys.argv
late_revoke = '--late-security-revoke' in sys.argv
late_logout = '--late-current-logout' in sys.argv
known_logout = '--notified-only-logout' in sys.argv
output = Path('test-artifacts/personal-center')
output.mkdir(exist_ok=True)
report = {'checks': [], 'console_errors': []}
db = None
if len(sys.argv)>2 and sys.argv[2]!='--red':
    db_path=Path(sys.argv[2]).resolve()
    assert db_path.is_relative_to((output/'_work').resolve()), 'Only an isolated _work database may be used'
    db=sqlite3.connect(db_path)
if not red and not db:
    raise SystemExit('Pass the isolated test-artifacts/personal-center/_work database for the complete green regression')
with sync_playwright() as pw:
    browser = pw.chromium.launch(executable_path='/usr/bin/google-chrome', headless=True, args=['--no-sandbox'])
    context = browser.new_context(viewport={'width': 1440, 'height': 1000}, reduced_motion='reduce')
    page = context.new_page()
    page.on('pageerror', lambda e: report['console_errors'].append(str(e)))
    page.goto(base + '/login')
    def api(path, method='GET', data=None, tab=page):
        return tab.evaluate('''async ({path,method,data})=>{const o={method,credentials:'same-origin',headers:{'Content-Type':'application/json'}};
          if(data!==null)o.body=JSON.stringify(data);const r=await fetch('/api'+path,o);return {status:r.status,body:await r.json()}}''', {'path':path,'method':method,'data':data})
    username = 'personal_' + str(time.time_ns())
    password = 'personal-test-password'
    assert api('/auth/register','POST',{'username':username,'password':password})['status']==201
    assert api('/auth/login','POST',{'username':username,'password':password})['status']==200

    if known_logout:
        page.goto(base+'/account');page.get_by_role('tab',name='资料与偏好',exact=True).click()
        owner=api('/auth/me')['body']['id']
        page.get_by_label('昵称',exact=True).fill('A退出通知后保留输入')
        other=context.new_page();other.goto(base+'/account')
        other.get_by_role('button',name='退出',exact=True).click()
        expect(other).to_have_url(base+'/login')
        expect(page.get_by_role('alert').filter(has_text='登录账户已变化').first).to_be_visible()
        sent=[]
        page.on('request',lambda r:sent.append({'method':r.method,'url':r.url}) if '/api/account/profile' in r.url or '/api/account/export' in r.url else None)
        page.get_by_role('button',name='保存资料与偏好',exact=True).click()
        page.wait_for_timeout(100)
        evidence={'requests':sent,'url_after_save':page.url}
        (output/'final-fix-known-logout-results.json').write_text(json.dumps(evidence,ensure_ascii=False,indent=2));print(json.dumps(evidence,ensure_ascii=False,indent=2))
        assert not sent, 'known logout page still sent a profile request and could lose input via401'
        expect(page.get_by_label('昵称',exact=True)).to_have_value('A退出通知后保留输入')
        page.get_by_role('button',name='放弃修改并重载',exact=True).click()
        page.get_by_role('button',name='下载 CSV',exact=True).click()
        page.evaluate("owner=>window.dispatchEvent(new CustomEvent('session-expired',{detail:{kind:'user',expectedUser:owner}}))",owner)
        page.wait_for_timeout(100)
        assert not sent
        expect(page).to_have_url(base+'/account')
        expect(page.get_by_label('昵称',exact=True)).to_have_value('A退出通知后保留输入')
        report['checks'].append('real other-tab logout notification preserves old A input: save/reload/CSV send no requests; late background401 cannot redirect known-stale page')
        (output/'final-fix-known-logout-results.json').write_text(json.dumps({**report,'requests':sent,'url_after_save':page.url},ensure_ascii=False,indent=2));print(json.dumps(report,ensure_ascii=False,indent=2))
        browser.close();db.close();raise SystemExit(0)

    if late_identity or late_password or late_revoke or late_logout:
        other=context.new_page();other.goto(base+'/login')
        bob=username+'_b'
        assert api('/auth/register','POST',{'username':bob,'password':password},other)['status']==201
        page.goto(base+'/account')
        page.get_by_role('tab',name='账户安全' if late_password or late_revoke else '资料与偏好',exact=True).click()
        held=[]
        if late_password:
            def hold(route): held.append((route,route.fetch()))
            page.route('**/api/auth/password',hold)
            page.get_by_label('当前密码',exact=True).fill(password)
            page.get_by_label('新密码',exact=True).fill(password+'-next')
            page.get_by_label('确认新密码',exact=True).fill(password+'-next')
            page.get_by_role('button',name='修改密码',exact=True).click()
            for _ in range(100):
                if held: break
                page.wait_for_timeout(100)
            assert len(held)==1 and held[0][1].status==200
        if late_revoke:
            def hold(route): held.append((route,route.fetch()))
            page.route('**/api/account/sessions/revoke-others',hold)
            page.once('dialog',lambda dialog:dialog.accept())
            page.get_by_role('button',name='退出其他会话',exact=True).click()
            for _ in range(100):
                if held: break
                page.wait_for_timeout(100)
            assert len(held)==1 and held[0][1].status==200
        if late_logout:
            def hold(route): held.append((route,route.fetch()))
            page.route('**/api/auth/logout',hold)
            page.get_by_role('button',name='退出',exact=True).click()
            for _ in range(100):
                if held: break
                page.wait_for_timeout(100)
            assert len(held)==1 and held[0][1].status==200
        assert api('/auth/login','POST',{'username':bob,'password':password},other)['status']==200
        bp=api('/account/profile',tab=other)['body']
        keys=['nickname','bio','avatar_id','daily_questions','daily_minutes','exam_name','exam_date','default_limit','default_module','reading_size','revision']
        update={k:bp[k] for k in keys};update.update(nickname='B已确认资料',avatar_id=5,reading_size=20,default_limit=50)
        assert api('/account/profile','PUT',update,other)['status']==200
        if late_identity:
            def hold(route):
                if route.request.method=='PUT': held.append((route,route.fetch()))
                else: route.continue_()
            page.route('**/api/account/profile',hold)
            page.get_by_label('昵称',exact=True).fill('A迟到冲突')
            page.get_by_role('button',name='保存资料与偏好',exact=True).click()
            page.wait_for_timeout(100)
            assert len(held)==1 and held[0][1].status==409
        # Only navigation uses a fault-injected 401. The held 409 / password
        # success is a real completed HTTP request, not a mocked business result.
        page.route('**/api/account/export?days=30',lambda route:route.fulfill(status=401,content_type='application/json',body=json.dumps({'error':'验收：旧页面会话失效'})))
        if late_password or late_revoke or late_logout:
            page.evaluate("window.dispatchEvent(new CustomEvent('session-expired',{detail:'user'}))")
        else:
            page.get_by_role('button',name='下载 CSV',exact=True).click()
        expect(page).to_have_url(base+'/login?redirect=/account')
        page.unroute('**/api/account/export?days=30')
        page.get_by_label('用户名',exact=True).fill(bob);page.get_by_label('密码',exact=True).fill(password)
        page.get_by_role('button',name='登录',exact=True).click()
        expect(page.get_by_role('tab',name='学习总览',exact=True)).to_be_visible()
        expect(page.locator('.topbar-user .pc-avatar')).to_have_class('avatar pc-avatar pc-avatar-5')
        followups=[]
        page.route('**/api/account/sessions?*',lambda route:(followups.append(route.request.headers.get('x-gk-expected-user')),route.continue_()))
        route,response=held.pop();route.fulfill(response=response)
        page.wait_for_timeout(200)
        evidence={'held_status':response.status,'url_after_release':page.url,'identity_alerts':page.get_by_role('alert').filter(has_text='登录账户已变化').count(),'topbar':page.locator('.topbar-user').text_content() if page.locator('.topbar-user').count() else None,'session_followups':followups,'current_cookie_status':api('/auth/me',tab=other)['status']}
        name='final-fix-late-password' if late_password else 'final-fix-late-revoke' if late_revoke else 'final-fix-late-logout' if late_logout else 'final-fix-late-identity'
        (output/(name+'-results.json')).write_text(json.dumps(evidence,ensure_ascii=False,indent=2))
        print(json.dumps(evidence,ensure_ascii=False,indent=2))
        assert evidence['current_cookie_status']==200, 'late A logout response deleted valid B Cookie'
        expect(page).to_have_url(base+'/account')
        assert not followups, 'old security callback issued a session reload after B login'
        assert evidence['identity_alerts']==0, 'late A failure invalidated current B identity'
        expect(page.locator('.topbar-user')).to_contain_text('B已确认资料')
        expect(page.locator('.topbar-user .pc-avatar')).to_have_class('avatar pc-avatar pc-avatar-5')
        page.get_by_role('tab',name='资料与偏好',exact=True).click()
        expect(page.get_by_label('练习正文字号',exact=True)).to_have_value('20')
        expect(page.get_by_label('默认题量',exact=True)).to_have_value('50')
        report['checks'].append('real delayed A '+('password success' if late_password else 'revoke-others success' if late_revoke else 'logout success' if late_logout else 'account_changed 409')+' after B login cannot invalidate B identity/profile/preferences or redirect')
        (output/(name+'-results.json')).write_text(json.dumps({**report,**evidence},ensure_ascii=False,indent=2))
        print(json.dumps(report,ensure_ascii=False,indent=2))
        browser.close();db.close();raise SystemExit(0)

    if cross_tab_only:
        other=context.new_page()
        other.goto(base+'/login')
        report['shared_cookie_cases']=[]
        for notified in (False,True):
            bob=username+('_n' if notified else '_s')
            assert api('/auth/register','POST',{'username':bob,'password':password},other)['status']==201
            assert api('/auth/login','POST',{'username':username,'password':password},other)['status']==200
            page.goto(base+'/account')
            page.get_by_role('tab',name='资料与偏好',exact=True).click()
            page.get_by_label('昵称',exact=True).fill('A跨标签未保存输入')
            a=api('/account/profile')['body']
            owner=api('/auth/me')['body']['id']
            # Real shared browser Cookie switch. Silent case uses raw login so
            # there is no BroadcastChannel event or mocked response/preflight.
            assert api('/auth/logout','POST',tab=other)['status']==200
            if notified:
                other.goto(base+'/login')
                other.get_by_label('用户名',exact=True).fill(bob)
                other.get_by_label('密码',exact=True).fill(password)
                other.get_by_role('button',name='登录',exact=True).click()
                expect(other.locator('.topbar-user')).to_contain_text(bob)
                expect(page.get_by_role('alert').filter(has_text='登录账户已变化').first).to_be_visible()
            else:
                assert api('/auth/login','POST',{'username':bob,'password':password},other)['status']==200
                assert page.get_by_role('alert').filter(has_text='登录账户已变化').count()==0
            before=api('/account/profile',tab=other)['body']
            assert before['revision']==a['revision']==0
            issued=[]
            def capture(request):
                if request.url.endswith('/api/account/profile') and request.method=='PUT': issued.append(request)
            page.on('request',capture)
            response=None
            if notified:
                page.get_by_role('button',name='保存资料与偏好',exact=True).click()
                page.wait_for_timeout(100)
                assert not issued, 'known account change should refuse locally before PUT'
            else:
                with page.expect_response(lambda r:r.url.endswith('/api/account/profile') and r.request.method=='PUT') as pending:
                    page.get_by_role('button',name='保存资料与偏好',exact=True).click()
                response=pending.value
            page.remove_listener('request',capture)
            after=api('/account/profile',tab=other)['body']
            case={'notified':notified,'a_revision':a['revision'],'b_before':before,'b_after':after,'b_polluted':after!=before,'put_status':response.status if response else None,'expected_owner':response.request.headers.get('x-gk-expected-user') if response else None,'a_owner':owner,'local_refusal':notified}
            report['shared_cookie_cases'].append(case)
            (output/'final-fix-cross-tab-results.json').write_text(json.dumps(report,ensure_ascii=False,indent=2))
            print(json.dumps(case,ensure_ascii=False,indent=2))
            assert after==before, 'R1: old A page polluted B profile via shared Cookie and equal revision'
            if not notified:
                assert response.status==409 and case['expected_owner']==str(owner)
                assert response.json()['code']=='account_changed'
            expect(page.get_by_role('alert').filter(has_text='登录账户已变化').first).to_be_visible()
            expect(page.get_by_label('昵称',exact=True)).to_have_value('A跨标签未保存输入')
            assert api('/auth/me',tab=other)['body']['username']==bob
            page.get_by_role('button',name='退出',exact=True).click()
            expect(page.get_by_label('昵称',exact=True)).to_have_value('A跨标签未保存输入')
            assert api('/auth/me',tab=other)['status']==200
            export_requests=[]
            def export_capture(request):
                if '/api/account/export?' in request.url: export_requests.append(request)
            page.on('request',export_capture)
            page.get_by_role('button',name='下载 CSV',exact=True).click()
            page.wait_for_timeout(100)
            page.remove_listener('request',export_capture)
            assert not export_requests, 'known stale CSV should refuse locally'
            expect(page.locator('.pc-export').get_by_role('alert')).to_contain_text('登录账户已变化')
            report['checks'].append(('notified' if notified else 'silent')+' real same-context A/B equal revision: silent PUT owner/409 or notified local refusal, B unchanged, notice/input retained, known-stale logout/CSV refused with valid B Cookie')
        browser.close();db.close()
        assert not report['console_errors'], report['console_errors']
        (output/'final-fix-cross-tab-results.json').write_text(json.dumps(report,ensure_ascii=False,indent=2))
        print(json.dumps(report,ensure_ascii=False,indent=2))
        raise SystemExit(0)

    def profile_ordering_regression():
        held=[];calls=[0]
        def initial_get(route):
            calls[0]+=1
            if calls[0]==1:
                route.fulfill(status=500,content_type='application/json',body=json.dumps({'error':'验收：路由资料暂不可用'}))
            elif calls[0]==2:
                held.append((route,route.fetch()))
            else:
                route.continue_()
        page.route('**/api/account/profile',initial_get)
        page.goto(base+'/account')
        page.get_by_role('tab',name='资料与偏好',exact=True).click()
        expect(page.get_by_label('昵称',exact=True)).to_be_visible()
        assert len(held)==1
        old=held[0][1].json()
        page.get_by_label('昵称',exact=True).fill('已经确认的新资料')
        page.get_by_label('默认题量',exact=True).select_option('10')
        page.get_by_label('练习正文字号',exact=True).select_option('20')
        page.get_by_role('button',name='头像样式 3',exact=True).click()
        page.get_by_role('button',name='保存资料与偏好',exact=True).click()
        new=api('/account/profile')['body']
        assert new['revision']==old['revision']+1
        expect(page.get_by_role('status').filter(has_text=f'版本 {new["revision"]}')).to_be_visible()
        route,response=held.pop()
        with page.expect_response(lambda r:r.url.endswith('/api/account/profile') and r.request.method=='GET'):
            route.fulfill(response=response)
        page.unroute('**/api/account/profile')
        page.evaluate('()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))')
        expect(page.get_by_label('昵称',exact=True)).to_have_value('已经确认的新资料')
        expect(page.locator('.topbar-user')).to_contain_text('已经确认的新资料')
        expect(page.locator('.topbar-user .pc-avatar')).to_have_class('avatar pc-avatar pc-avatar-3')
        expect(page.get_by_label('默认题量',exact=True)).to_have_value('10')
        expect(page.get_by_label('练习正文字号',exact=True)).to_have_value('20')
        expect(page.locator('.pc-profile-form .pc-card-heading .pc-tag')).to_contain_text(f'版本 {new["revision"]}')
        expect(page.get_by_role('alert').filter(has_text='登录账户已变化')).to_have_count(0)
        page.get_by_label('昵称',exact=True).fill('后续保存仍然成功')
        page.get_by_role('button',name='保存资料与偏好',exact=True).click()
        expect(page.get_by_role('status').filter(has_text=f'版本 {new["revision"]+1}')).to_be_visible()
        assert api('/account/profile')['body']['revision']==new['revision']+1
        report['checks'].append('same-user old initial profile GET cannot revert successful PUT, form/topbar/preferences/revision; next PUT succeeds')

        def assert_profile(p):
            expect(page.get_by_label('昵称',exact=True)).to_have_value(p['nickname'])
            expect(page.locator('.topbar-user')).to_contain_text(p['nickname'])
            expect(page.locator('.topbar-user .pc-avatar')).to_have_class(f'avatar pc-avatar pc-avatar-{p["avatar_id"]}')
            expect(page.get_by_label('默认题量',exact=True)).to_have_value(str(p['default_limit']))
            expect(page.get_by_label('练习正文字号',exact=True)).to_have_value(str(p['reading_size']))
            expect(page.locator('.pc-profile-form .pc-card-heading .pc-tag')).to_contain_text(f'版本 {p["revision"]}')
            expect(page.get_by_role('alert').filter(has_text='登录账户已变化')).to_have_count(0)
        def save_as(name):
            page.get_by_label('昵称',exact=True).fill(name)
            previous=api('/account/profile')['body']['revision']
            page.get_by_role('button',name='保存资料与偏好',exact=True).click()
            expect(page.get_by_role('status').filter(has_text=f'版本 {previous+1}')).to_be_visible()
            p=api('/account/profile')['body']
            assert p['revision']==previous+1
            return p

        # A real old dashboard snapshot also carries a profile. Its other counts
        # remain useful, but its embedded older profile must not lower confirmed data.
        held_dash=[];calls=[0]
        def fail_first_get(route):
            calls[0]+=1
            if calls[0]==1:
                route.fulfill(status=500,content_type='application/json',body=json.dumps({'error':'验收：首次资料读取失败'}))
            else:
                route.continue_()
        page.route('**/api/account/profile',fail_first_get)
        page.route('**/api/account/dashboard?days=30',lambda route:held_dash.append((route,route.fetch())))
        page.goto(base+'/account');page.get_by_role('tab',name='资料与偏好',exact=True).click()
        expect(page.get_by_label('昵称',exact=True)).to_be_visible()
        assert len(held_dash)==1
        page.get_by_label('默认题量',exact=True).select_option('50')
        page.get_by_label('练习正文字号',exact=True).select_option('18')
        page.get_by_label('每日作答目标',exact=True).fill('35')
        page.get_by_label('每日分钟目标',exact=True).fill('45')
        page.get_by_role('button',name='头像样式 6',exact=True).click()
        newest=save_as('初始化看板之前的新保存')
        route,response=held_dash.pop()
        with page.expect_response(lambda r:'/api/account/dashboard?' in r.url):
            route.fulfill(response=response)
        page.unroute('**/api/account/dashboard?days=30');page.unroute('**/api/account/profile')
        assert_profile(newest)
        page.get_by_role('tab',name='学习总览',exact=True).click()
        expect(page.locator('.pc-goal-count').first).to_contain_text('/ 35 次')
        expect(page.locator('.pc-goal-count').nth(1)).to_contain_text('/ 45 分钟')
        page.get_by_role('tab',name='资料与偏好',exact=True).click()
        save_as('看板迟到后继续保存')
        report['checks'].append('same-user late dashboard profile keeps newest profile/goals and next PUT succeeds')

        # The route guard's auth.loadProfile GET reads N while the mounted form
        # completes N+1 before navigation. New question defaults must use N+1.
        held_auth=[];captured=[False]
        def late_auth_get(route):
            if route.request.method=='GET' and not captured[0]:
                captured[0]=True;held_auth.append((route,route.fetch()))
            else:
                route.continue_()
        page.route('**/api/account/profile',late_auth_get)
        page.locator('.sidebar nav').get_by_role('link',name='题库练习',exact=True).click()
        expect(page).to_have_url(base+'/account')
        page.wait_for_timeout(100)
        assert len(held_auth)==1
        page.get_by_label('默认题量',exact=True).select_option('10')
        page.get_by_label('练习正文字号',exact=True).select_option('20')
        page.get_by_role('button',name='头像样式 7',exact=True).click()
        newest=save_as('路由资料读取之后的新保存')
        route,response=held_auth.pop();route.fulfill(response=response)
        page.unroute('**/api/account/profile')
        expect(page).to_have_url(base+'/questions')
        expect(page.locator('.session-controls select')).to_have_value('10')
        expect(page.locator('.topbar-user')).to_contain_text(newest['nickname'])
        expect(page.locator('.topbar-user .pc-avatar')).to_have_class('avatar pc-avatar pc-avatar-7')
        page.locator('.topbar').get_by_role('link',name='打开个人中心',exact=True).click()
        page.get_by_role('tab',name='资料与偏好',exact=True).click()
        assert_profile(newest)
        save_as('路由迟到读取后继续保存')
        report['checks'].append('auth.loadProfile late GET returns confirmed newer cache, preserves topbar/defaults and next PUT succeeds')

        # A successful old PUT ACK arrives after an independent real PUT N+2 has
        # already been confirmed by the pending initialization GET.
        held_read=[];held_ack=[];calls=[0]
        def init_read(route):
            if route.request.method!='GET':
                route.continue_();return
            calls[0]+=1
            if calls[0]==1:
                route.fulfill(status=500,content_type='application/json',body=json.dumps({'error':'验收：首次资料读取失败'}))
            elif calls[0]==2:
                held_read.append(route)
            else:
                route.continue_()
        page.route('**/api/account/profile',init_read)
        page.goto(base+'/account');page.get_by_role('tab',name='资料与偏好',exact=True).click()
        expect(page.get_by_label('昵称',exact=True)).to_be_visible()
        assert len(held_read)==1
        def hold_put(route):
            if route.request.method=='PUT':
                held_ack.append((route,route.fetch()))
            else:
                route.fallback()
        page.route('**/api/account/profile',hold_put)
        page.get_by_label('昵称',exact=True).fill('这次保存的较旧ACK')
        page.get_by_label('默认题量',exact=True).select_option('20')
        page.get_by_label('练习正文字号',exact=True).select_option('16')
        page.get_by_role('button',name='头像样式 1',exact=True).click()
        page.get_by_role('button',name='保存资料与偏好',exact=True).click()
        expect(page.get_by_label('昵称',exact=True)).to_be_disabled()
        assert len(held_ack)==1 and held_ack[0][1].status==200
        ack=held_ack[0][1].json()
        # Stop intercepting PUT while preserving the held ACK and init GET.
        page.unroute('**/api/account/profile',hold_put)
        keys=['nickname','bio','avatar_id','daily_questions','daily_minutes','exam_name','exam_date','default_limit','default_module','reading_size','revision']
        update={key:ack[key] for key in keys}
        update.update(nickname='同账号另一窗口确认的较新版本',avatar_id=6,default_limit=50,reading_size=18)
        result=api('/account/profile','PUT',update)
        assert result['status']==200
        newest=result['body'];assert newest['revision']==ack['revision']+1
        held_read.pop().continue_()
        expect(page.locator('.topbar-user')).to_contain_text(newest['nickname'])
        expect(page.locator('.pc-profile-form .pc-card-heading .pc-tag')).to_contain_text(f'版本 {newest["revision"]}')
        # The pending submitted input remains protected until its ACK is delivered.
        expect(page.get_by_label('昵称',exact=True)).to_have_value('这次保存的较旧ACK')
        route,response=held_ack.pop();route.fulfill(response=response)
        page.unroute('**/api/account/profile')
        expect(page.get_by_role('status').filter(has_text=f'已同步较新资料（版本 {newest["revision"]}）')).to_be_visible()
        assert_profile(newest)
        save_as('较旧ACK之后继续保存')
        report['checks'].append('older successful PUT ACK cannot lower newer same-user GET confirmation; pending dirty input protected, newest form/cache retained and next PUT succeeds')

    if profile_ordering_only:
        profile_ordering_regression()
        browser.close();db.close()
        assert not report['console_errors'],report['console_errors']
        (output/'task3-fix1-browser-results.json').write_text(json.dumps(report,ensure_ascii=False,indent=2))
        print(json.dumps(report,ensure_ascii=False,indent=2));sys.exit(0)

    page.goto(base + '/account')
    if red:
        page.screenshot(path=str(output/'task3-red-old-account.png'), full_page=True)
    expect(page.get_by_role('tab', name='学习总览', exact=True)).to_be_visible(timeout=5000)
    if red:
        raise AssertionError('The page already has personal-center tabs; the old-page red check is no longer applicable')
    for name in ['复习助手','资料与偏好','账户安全']:
        expect(page.get_by_role('tab',name=name,exact=True)).to_be_visible()
    expect(page.locator('[data-chart="trend"]')).to_be_visible()
    expect(page.locator('[data-chart="modules"]')).to_be_visible()
    expect(page.locator('[data-chart="activity"]')).to_be_visible()
    expect(page.locator('[data-chart="goal"]')).to_have_count(2)
    page.get_by_role('tab',name='学习总览',exact=True).focus()
    page.get_by_role('tab',name='学习总览',exact=True).press('ArrowRight')
    expect(page.get_by_role('tab',name='复习助手',exact=True)).to_have_attribute('aria-selected','true')
    page.get_by_role('tab',name='复习助手',exact=True).press('End')
    expect(page.get_by_role('tab',name='账户安全',exact=True)).to_have_attribute('aria-selected','true')
    page.get_by_role('tab',name='账户安全',exact=True).press('Home')
    expect(page.get_by_role('tab',name='学习总览',exact=True)).to_have_attribute('aria-selected','true')
    report['checks'].append('four tabs and four types of charts use the real empty dashboard')
    page.screenshot(path=str(output/'account-desktop.png'),full_page=True)
    page.get_by_role('tab',name='资料与偏好',exact=True).click()
    page.get_by_label('昵称',exact=True).fill('学习工作台测试')
    page.get_by_label('简介',exact=True).fill('合成浏览器验收资料')
    page.get_by_label('每日作答目标',exact=True).fill('25')
    page.get_by_label('每日分钟目标',exact=True).fill('35')
    page.get_by_label('默认题量',exact=True).select_option('10')
    page.get_by_label('练习正文字号',exact=True).select_option('20')
    future_exam=(datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).date()+datetime.timedelta(days=30)).isoformat()
    page.get_by_label('考试名称',exact=True).fill('国考合成计划')
    page.get_by_label('考试日期',exact=True).fill(future_exam)
    expect(page.locator('.pc-avatar-options button')).to_have_count(8)
    page.get_by_role('button',name='头像样式 3',exact=True).click()
    page.get_by_role('button',name='保存资料与偏好',exact=True).click()
    expect(page.get_by_role('status').filter(has_text='资料与偏好已保存')).to_be_visible()
    stored=api('/account/profile')['body']
    assert stored['nickname']=='学习工作台测试' and stored['daily_questions']==25 and stored['avatar_id']==3 and stored['reading_size']==20 and stored['default_limit']==10
    expect(page.locator('.topbar-user .pc-avatar')).to_have_class('avatar pc-avatar pc-avatar-3')
    expect(page.locator('.topbar-user')).to_contain_text('学习工作台测试')
    expect(page.locator('.pc-exam strong')).to_contain_text('距离考试还有')
    assert stored['exam_date']==future_exam
    page.reload()
    page.get_by_role('tab',name='资料与偏好',exact=True).click()
    expect(page.get_by_label('昵称',exact=True)).to_have_value('学习工作台测试')
    report['checks'].append('full profile PUT persists across refresh and updates topbar')
    page.get_by_label('昵称',exact=True).fill('准备退出的资料')
    page.route('**/api/auth/logout',lambda route:route.fulfill(status=503,content_type='application/json',body=json.dumps({'error':'验收：退出暂时不可用'})))
    page.once('dialog',lambda dialog:dialog.accept());page.locator('.topbar').get_by_role('button',name='退出',exact=True).click()
    expect(page.get_by_role('alert').filter(has_text='退出暂时不可用')).to_be_visible()
    page.unroute('**/api/auth/logout')
    assert api('/auth/me')['status']==200
    page.get_by_label('昵称',exact=True).fill('退出失败后保护资料')
    page.once('dialog',lambda dialog:dialog.dismiss());page.get_by_role('tab',name='账户安全',exact=True).click()
    expect(page.get_by_label('昵称',exact=True)).to_have_value('退出失败后保护资料')
    page.once('dialog',lambda dialog:dialog.dismiss());page.locator('.sidebar nav').get_by_role('link',name='学习记录',exact=True).click()
    expect(page).to_have_url(base+'/account')
    page.once('dialog',lambda dialog:dialog.accept());page.get_by_role('button',name='放弃修改并重载',exact=True).click()
    expect(page.get_by_label('昵称',exact=True)).to_have_value('学习工作台测试')
    report['checks'].append('logout 503 keeps identity and subsequent newly edited profile still has tab/router protection')
    other=context.new_page();other.goto(base+'/account');other.get_by_role('tab',name='资料与偏好',exact=True).click()
    page.get_by_label('昵称',exact=True).fill('第一窗口')
    page.get_by_role('button',name='保存资料与偏好',exact=True).click()
    expect(page.get_by_role('status').filter(has_text='资料与偏好已保存')).to_be_visible()
    other.get_by_label('昵称',exact=True).fill('未覆盖的本地输入')
    other.get_by_role('button',name='保存资料与偏好',exact=True).click()
    expect(other.get_by_role('alert').filter(has_text='其他窗口')).to_be_visible()
    expect(other.get_by_label('昵称',exact=True)).to_have_value('未覆盖的本地输入')
    other.once('dialog',lambda d:d.dismiss());other.get_by_role('button',name='重新加载服务器资料',exact=True).click()
    expect(other.get_by_label('昵称',exact=True)).to_have_value('未覆盖的本地输入')
    other.once('dialog',lambda d:d.accept());other.get_by_role('button',name='重新加载服务器资料',exact=True).click()
    expect(other.get_by_label('昵称',exact=True)).to_have_value('第一窗口')
    other.close()
    page.get_by_label('昵称',exact=True).fill('保护未保存资料')
    page.once('dialog',lambda d:d.dismiss());page.get_by_role('tab',name='账户安全',exact=True).click()
    expect(page.get_by_label('昵称',exact=True)).to_have_value('保护未保存资料')
    page.once('dialog',lambda d:d.accept());page.get_by_role('tab',name='账户安全',exact=True).click()
    page.get_by_label('当前密码',exact=True).fill('wrong-current-password')
    page.get_by_label('新密码',exact=True).fill('new-personal-password')
    page.get_by_label('确认新密码',exact=True).fill('new-personal-password')
    page.get_by_role('button',name='修改密码',exact=True).click()
    expect(page.get_by_role('alert').filter(has_text='原密码错误')).to_be_visible()
    assert api('/auth/me')['status']==200
    report['checks'].append('profile CAS retains local input, confirmed reload, dirty tab guard, password 400 retains login')
    page.once('dialog',lambda d:d.accept())
    for name in ['学习总览','复习助手','资料与偏好','账户安全']:
        page.get_by_role('tab',name=name,exact=True).click()
        page.set_viewport_size({'width':375,'height':812})
        assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth'), name
        page.screenshot(path=str(output/('account-mobile-'+name+'.png')),full_page=True)
    report['checks'].append('all four tabs fit 375px without page overflow')

    # The allowed 40-character exam name must remain within the phone viewport.
    page.get_by_role('tab',name='资料与偏好',exact=True).click()
    page.get_by_label('考试名称',exact=True).fill('W'*40)
    page.get_by_role('button',name='保存资料与偏好',exact=True).click()
    expect(page.get_by_role('status').filter(has_text='资料与偏好已保存')).to_be_visible()
    assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth'), '40-character exam name overflow'
    page.screenshot(path=str(output/'account-exam-name-boundary-mobile.png'),full_page=True)
    page.get_by_label('考试名称',exact=True).fill('国考合成计划')
    page.get_by_role('button',name='保存资料与偏好',exact=True).click()
    expect(page.get_by_role('status').filter(has_text='资料与偏好已保存')).to_be_visible()

    # Everything below is driven by real HTTP writes in the isolated service.
    page.set_viewport_size({'width':1440,'height':1000})
    if db:
        qid,module,correct,concept_id=db.execute("SELECT q.id,q.module,q.answer,c.id FROM question q JOIN latest_label l ON l.question_id=q.id JOIN concept c ON c.module=l.subject AND c.name=l.tertiary WHERE q.answer_type='single' AND q.option_count>=2 LIMIT 1").fetchone()
        wrong=next(v[0] for v in db.execute('SELECT label FROM option WHERE question_id=? ORDER BY label',(qid,)) if v[0]!=correct)
    else:
        question=api('/questions?size=1')['body']['items'][0]
        qid,module,correct,concept_id=question['id'],question['module'],'A',None
        wrong='B'
    def create(ids):
        result=api('/practice/sessions','POST',{'kind':'single','spec':{'question_ids':ids}})
        assert result['status']==200,result
        return result['body']['session_id']
    for i in range(6):
        sid=create([qid])
        result=api(f'/practice/sessions/{sid}/submit','POST',{'answers':[{'question_id':qid,'answer':correct if i==0 else wrong,'duration_ms':60000}],'draft_revision':0})
        assert result['status']==200,result
    unfinished=create([qid])
    assert api(f'/practice/sessions/{unfinished}/answers','PUT',{'answers':[{'question_id':qid,'answer':wrong,'duration_ms':500}],'draft_revision':0})['status']==200
    assert api(f'/favorites/{qid}','POST',{'action':'add'})['status']==200
    d=api('/account/dashboard?days=30')['body']
    assert d['today']['answered']==6 and d['today']['duration_ms']==360000
    if db:
        assert d['today']['correct']==1 and abs(d['today']['accuracy']-100/6)<.001
        assert d['review']['weak_concepts'][0]['concept_id']==concept_id
    page.reload()
    expect(page.locator('.pc-goal-count').first).to_contain_text('6')
    expect(page.locator('.pc-goal-count').nth(1)).to_contain_text('6.0')
    expect(page.locator('.pc-achievement-unlocked')).to_contain_text('首次作答')
    expect(page.locator('.pc-day')).to_have_count(90)
    page.get_by_text('查看今日目标数据表',exact=True).click()
    expect(page.locator('.pc-today tbody tr')).to_have_count(2)
    page.locator('[data-chart="goal"] svg').first.focus()
    assert '6 / 25 次' in page.locator('[data-chart="goal"] svg').first.get_attribute('aria-label')
    row=page.locator('.pc-module-row').filter(has_text=module)
    row.focus();expect(page.locator('[data-chart="modules"] .pc-chart-value')).to_contain_text('1 / 6')
    day=page.locator('.pc-day-selected');day.focus();day.press('ArrowUp')
    selected_day=page.locator('.pc-day-selected').get_attribute('aria-label')
    assert selected_day.startswith(d['activity'][-2]['date']), {'expected':d['activity'][-2]['date'],'actual':selected_day}
    page.locator('.pc-day').first.hover()
    expect(page.locator('[data-chart="activity"] .pc-chart-value')).to_contain_text(d['activity'][0]['date'])
    # Hover previews a date without moving the keyboard's one tab stop.
    assert page.locator('.pc-day-selected').get_attribute('aria-label').startswith(d['activity'][-2]['date'])
    expect(page.locator('.pc-day[tabindex="0"]')).to_have_count(1)
    page.mouse.move(0,0)
    page.locator('.pc-trend-plot svg').first.locator('g').last.hover()
    expect(page.locator('[data-chart="trend"] .pc-chart-value')).to_contain_text(d['trend'][-1]['date'])
    page.mouse.move(0,0)
    page.locator('.pc-trend-plot').focus();page.locator('.pc-trend-plot').press('Home')
    expect(page.locator('[data-chart="trend"] .pc-chart-value')).to_contain_text(d['trend'][0]['date'])
    page.get_by_text('查看趋势数据表',exact=True).click()
    expect(page.locator('[data-chart="trend"] tbody tr')).to_have_count(30)
    report['checks'].append('real six submissions drive 16.7% accuracy, six minutes, achievements and keyboard chart values; no double-counted module duration')

    # Hold an actual response then return it after the newer 90-day response.
    pending=[]
    pattern='**/api/account/dashboard?days=7'
    page.route(pattern,lambda route:pending.append(route))
    page.get_by_role('button',name='7 天',exact=True).click()
    expect(page.get_by_text('正在更新…',exact=True)).to_be_visible()
    assert len(pending)==1
    page.get_by_role('button',name='90 天',exact=True).click()
    expect(page.locator('[data-chart="trend"] select option')).to_have_count(90)
    pending.pop().continue_();page.unroute(pattern)
    page.wait_for_timeout(200)
    expect(page.locator('[data-chart="trend"] select option')).to_have_count(90)
    page.route('**/api/account/dashboard?days=7',lambda route:route.fulfill(status=500,content_type='application/json',body=json.dumps({'error':'验收：临时读取失败'})))
    page.get_by_role('button',name='7 天',exact=True).click()
    expect(page.get_by_role('alert').filter(has_text='临时读取失败')).to_be_visible()
    page.unroute('**/api/account/dashboard?days=7')
    page.get_by_role('button',name='重试学习数据',exact=True).click()
    expect(page.locator('[data-chart="trend"] select option')).to_have_count(7)
    report['checks'].append('late real 7-day response cannot replace newer 90-day data; failed dashboard can retry')

    page.get_by_role('tab',name='复习助手',exact=True).click()
    page.get_by_role('link',name='继续练习',exact=True).first.click()
    expect(page).to_have_url(base+f'/practice?session={unfinished}')
    expect(page.locator('.option.selected .option-letter')).to_have_text(wrong)
    assert page.locator('.question-stem').evaluate('e=>getComputedStyle(e).fontSize')=='20px'
    assert page.locator('.options .rich-text').first.evaluate('e=>getComputedStyle(e).fontSize')=='20px'
    page.locator('.option').filter(has=page.locator('.option-letter',has_text=wrong)).click()
    expect(page.get_by_text('作答已保存',exact=True)).to_be_visible()
    page.get_by_role('button',name='提交练习',exact=True).first.click()
    expect(page.get_by_text('官方解析',exact=True)).to_be_visible()
    assert page.locator('.analysis .rich-text').first.evaluate('e=>getComputedStyle(e).fontSize')=='20px'
    def account_review():
        page.goto(base+'/account');page.get_by_role('tab',name='复习助手',exact=True).click()
    account_review()
    page.get_by_role('button',name='开始优先错题复练',exact=True).click()
    expect(page.locator('.options')).to_be_visible()
    result=api('/practice/sessions/'+page.url.split('session=')[1])['body']
    assert result['kind']=='wrongbook' and qid in result['question_ids']
    account_review()
    page.get_by_role('button',name='练习此模块',exact=True).first.click()
    expect(page.locator('.options')).to_be_visible()
    result=api('/practice/sessions/'+page.url.split('session=')[1])['body']
    assert result['kind']=='single' and result['spec']['module']==module
    if db:
        account_review();page.get_by_role('button',name='复练此考点',exact=True).first.click()
        expect(page.locator('.options')).to_be_visible()
        result=api('/practice/sessions/'+page.url.split('session=')[1])['body']
        assert result['kind']=='concept' and result['spec']['concept_id']==concept_id
    favorite_result=api('/practice/sessions','POST',{'kind':'favorite','spec':{'limit':10}})
    assert favorite_result['status']==200
    favorite_session=favorite_result['body']['session_id']
    account_review()
    favorite_item=page.locator('.pc-resume-list article').filter(has=page.get_by_role('heading',name='收藏练习 · 1 题',exact=True))
    expect(favorite_item).to_be_visible()
    favorite_item.get_by_role('link',name='继续练习',exact=True).click()
    expect(page).to_have_url(base+f'/practice?session={favorite_session}')
    report['checks'].append('resume preserves saved answers and favorite session type; real wrongbook/module/concept buttons create compatible sessions; saved reading size applies to stems/options/analysis')

    # Profile input survives API failure, saved preferences apply, explicit URL wins.
    page.goto(base+'/account');page.get_by_role('tab',name='资料与偏好',exact=True).click()
    page.get_by_label('默认模块',exact=True).select_option(module)
    page.get_by_label('昵称',exact=True).fill('400保留本地输入')
    page.route('**/api/account/profile',lambda route:route.fulfill(status=400,content_type='application/json',body=json.dumps({'error':'验收：字段校验失败'})) if route.request.method=='PUT' else route.continue_())
    page.get_by_role('button',name='保存资料与偏好',exact=True).click()
    expect(page.get_by_role('alert').filter(has_text='字段校验失败')).to_be_visible()
    expect(page.get_by_label('昵称',exact=True)).to_have_value('400保留本地输入')
    assert api('/auth/me')['status']==200
    page.unroute('**/api/account/profile')
    # Hold PUT: disable edits and repeat-submit and do not mark saved before ACK.
    puts=[]
    page.route('**/api/account/profile',lambda route:puts.append(route) if route.request.method=='PUT' else route.continue_())
    page.get_by_role('button',name='保存资料与偏好',exact=True).click()
    expect(page.get_by_label('昵称',exact=True)).to_be_disabled()
    expect(page.get_by_role('button',name='正在保存…',exact=True)).to_be_disabled()
    page.get_by_role('tab',name='学习总览',exact=True).click()
    expect(page.get_by_label('昵称',exact=True)).to_be_visible()
    assert len(puts)==1
    puts.pop().continue_();page.unroute('**/api/account/profile')
    expect(page.get_by_role('status').filter(has_text='资料与偏好已保存')).to_be_visible()
    page.get_by_label('昵称',exact=True).fill('路由与退出保护')
    assert page.evaluate('''()=>{const e=new Event('beforeunload',{cancelable:true});window.dispatchEvent(e);return e.defaultPrevented}''')
    page.once('dialog',lambda dialog:dialog.dismiss());page.locator('.sidebar nav').get_by_role('link',name='题库练习',exact=True).click()
    expect(page).to_have_url(base+'/account')
    page.once('dialog',lambda dialog:dialog.dismiss());page.locator('.topbar').get_by_role('button',name='退出',exact=True).click()
    assert api('/auth/me')['status']==200
    expect(page.get_by_label('昵称',exact=True)).to_have_value('路由与退出保护')
    page.once('dialog',lambda dialog:dialog.accept());page.locator('.sidebar nav').get_by_role('link',name='题库练习',exact=True).click()
    expect(page.locator('.session-controls select')).to_have_value('10')
    expect(page.get_by_role('combobox',name='模块',exact=True)).to_have_value(module)
    page.goto(base+'/questions?module=&limit=50')
    expect(page.get_by_role('combobox',name='模块',exact=True)).to_have_value('')
    expect(page.locator('.session-controls select')).to_have_value('50')
    page.goto(base+'/questions')
    expect(page.get_by_role('combobox',name='模块',exact=True)).to_have_value(module)
    page.get_by_role('combobox',name='模块',exact=True).select_option('')
    page.get_by_role('button',name='筛选 / 搜索',exact=True).click()
    page.wait_for_function("location.search.includes('page=1')")
    expect(page.get_by_role('combobox',name='模块',exact=True)).to_have_value('')
    assert page.evaluate("new URL(location.href).searchParams.has('module') && new URL(location.href).searchParams.get('module')===''"), 'All modules must remain an explicit URL override'
    page.reload();expect(page.get_by_role('combobox',name='模块',exact=True)).to_have_value('')
    report['checks'].append('400 preserves input/login, pending PUT freezes edits/tabs and prevents duplicates; refresh/router/logout guard dirty profile; saved defaults respect explicit URL')

    page.goto(base+'/account');page.get_by_role('tab',name='资料与偏好',exact=True).click()
    page.route('**/api/account/export?days=30',lambda route:route.fulfill(status=400,content_type='application/json',body=json.dumps({'error':'导出超过 5000 条，请缩小范围'})))
    page.get_by_role('button',name='下载 CSV',exact=True).click()
    expect(page.get_by_role('alert').filter(has_text='5000')).to_be_visible()
    page.unroute('**/api/account/export?days=30')
    with page.expect_download() as download_event:
        page.get_by_role('button',name='下载 CSV',exact=True).click()
    download=download_event.value
    assert download.suggested_filename=='行测研习-个人作答记录.csv'
    download.save_as(str(output/'task3-personal-records.csv'))
    assert (output/'task3-personal-records.csv').read_bytes().startswith(b'\xef\xbb\xbf')
    report['checks'].append('actual same-origin CSV download uses fixed client filename and BOM; over-limit API error visible')

    # New Cookie in a second browser context, never copy tokens into JS or URLs.
    second=browser.new_context(viewport={'width':1200,'height':900});second_page=second.new_page();second_page.goto(base+'/login')
    assert api('/auth/login','POST',{'username':username,'password':password},tab=second_page)['status']==200
    second_page.goto(base+'/account');second_page.get_by_role('tab',name='资料与偏好',exact=True).click()
    expect(second_page.get_by_label('练习正文字号',exact=True)).to_have_value('20')
    page.reload();page.get_by_role('tab',name='账户安全',exact=True).click()
    expect(page.get_by_role('button',name='撤销此会话',exact=True)).to_have_count(1)
    assert len(api('/account/sessions')['body']['items'])==2
    page.once('dialog',lambda dialog:dialog.dismiss());page.get_by_role('button',name='撤销此会话',exact=True).click()
    assert api('/auth/me',tab=second_page)['status']==200
    page.once('dialog',lambda dialog:dialog.accept());page.get_by_role('button',name='撤销此会话',exact=True).click()
    expect(page.get_by_role('button',name='撤销此会话',exact=True)).to_have_count(0)
    assert api('/auth/me',tab=second_page)['status']==401 and api('/auth/me')['status']==200
    assert api('/auth/login','POST',{'username':username,'password':password},tab=second_page)['status']==200
    page.reload();page.get_by_role('tab',name='账户安全',exact=True).click()
    page.once('dialog',lambda dialog:dialog.accept());page.get_by_role('button',name='退出其他会话',exact=True).click()
    expect(page.get_by_role('status').filter(has_text='当前会话已保留')).to_be_visible()
    assert api('/auth/me',tab=second_page)['status']==401 and api('/auth/me')['status']==200
    page.get_by_role('button',name='重新计算考点统计',exact=True).click()
    expect(page.get_by_role('status').filter(has_text='已重新计算')).to_be_visible()
    report['checks'].append('second device loads server preferences, single revoke requires confirmation, revoke-others preserves current Cookie; original statistics rebuild works')
    second.close()

    if db:
        uid=api('/auth/me')['body']['id']
        # Historical metadata and page 2 require >20 synthetic active sessions.
        future=(datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(days=2)).strftime('%Y-%m-%dT%H:%M:%SZ')
        for i in range(22):
            db.execute('INSERT INTO app_session(token,user_id,expires_at,public_id) VALUES(?,?,?,?)',('fixture-session-'+str(time.time_ns()),uid,future,format(time.time_ns(),'032x')))
        db.commit()
        page.reload();page.get_by_role('tab',name='账户安全',exact=True).click()
        expect(page.locator('.pc-session-list>li')).to_have_count(20)
        expect(page.get_by_role('heading',name='历史会话 / 信息未知',exact=True).first).to_be_visible()
        current_item=page.locator('.pc-session-list>li').filter(has_text='当前设备')
        expect(current_item.get_by_role('button',name='撤销此会话',exact=True)).to_have_count(0)
        payload=api('/account/sessions')['body']
        assert payload['total']==23 and all('token' not in row and 'hash' not in row for row in payload['items'])
        page.get_by_role('navigation',name='登录会话分页',exact=True).get_by_role('button',name='下一页',exact=True).click()
        expect(page.locator('.pc-session-list>li')).to_have_count(3)
        page.once('dialog',lambda dialog:dialog.accept());page.get_by_role('button',name='退出其他会话',exact=True).click()
        expect(page.locator('.pc-session-list>li')).to_have_count(1)
        assert api('/auth/me')['status']==200
        report['checks'].append('23 real isolated sessions paginate 20/3, legacy nullable metadata shows unknown; current session has no DELETE control and responses contain no token/hash')

        label_id,tertiary=db.execute('SELECT id,tertiary FROM latest_label WHERE question_id=?',(qid,)).fetchone()
        db.execute('UPDATE label SET tertiary=NULL WHERE id=?',(label_id,));db.commit()
        try:
            page.reload();page.get_by_role('tab',name='复习助手',exact=True).click()
            expect(page.get_by_text('先做模块练习。',exact=False)).to_be_visible()
            expect(page.get_by_role('button',name='复练此考点',exact=True)).to_have_count(0)
        finally:
            db.execute('UPDATE label SET tertiary=? WHERE id=?',(tertiary,label_id));db.commit()
        report['checks'].append('actual unmapped latest label reports sparse mapping and does not create invalid concept practice')

        # All tabs with populated data and local tables must also fit small phones.
        page.reload();page.set_viewport_size({'width':375,'height':812})
        for name in ['学习总览','复习助手','资料与偏好','账户安全']:
            page.get_by_role('tab',name=name,exact=True).click()
            assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth'), 'populated '+name
            page.screenshot(path=str(output/('account-populated-mobile-'+name+'.png')),full_page=True)
        page.set_viewport_size({'width':1440,'height':1000})
        page.get_by_role('tab',name='学习总览',exact=True).click()
        page.screenshot(path=str(output/'account-populated-desktop.png'),full_page=True)

        # Fixtures only touch the explicitly validated _work DB, never the real var DB.
        yesterday=(datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).date()-datetime.timedelta(days=1)).isoformat()
        db.execute('UPDATE user_profile SET exam_date=?,revision=revision+1 WHERE user_id=?',(yesterday,uid));db.commit()
        page.reload();expect(page.get_by_text('考试日期已过',exact=True)).to_be_visible()
        page.get_by_role('tab',name='资料与偏好',exact=True).click()
        page.get_by_label('昵称',exact=True).fill('保持本人到期日期')
        page.get_by_role('button',name='保存资料与偏好',exact=True).click()
        expect(page.get_by_role('status').filter(has_text='资料与偏好已保存')).to_be_visible()
        assert api('/account/profile')['body']['exam_date']==yesterday
        page.get_by_label('考试日期',exact=True).fill((datetime.date.fromisoformat(yesterday)-datetime.timedelta(days=1)).isoformat())
        page.get_by_role('button',name='保存资料与偏好',exact=True).click()
        expect(page.locator('#pc-date-hint')).to_contain_text('未来日期')
        page.once('dialog',lambda dialog:dialog.accept());page.get_by_role('button',name='放弃修改并重载',exact=True).click()
        expect(page.get_by_label('考试日期',exact=True)).to_have_value(yesterday)
        # A Beijing March 1 in a leap year is February 29 in UTC. Adding five
        # years must operate on the calendar date rather than the prior UTC day.
        page.evaluate("""()=>{window.__personalNativeDate=Date;window.Date=class extends window.__personalNativeDate {
          constructor(...args){super(...(args.length?args:['2028-03-01T00:00:00+08:00']))}
        }}""")
        page.get_by_label('考试日期',exact=True).fill('2033-03-02')
        page.get_by_role('button',name='保存资料与偏好',exact=True).click()
        expect(page.locator('#pc-date-hint')).to_have_text('新设置的日期需为北京时间未来日期，最多五年后')
        page.evaluate('()=>{window.Date=window.__personalNativeDate;delete window.__personalNativeDate}')
        page.once('dialog',lambda dialog:dialog.accept());page.get_by_role('button',name='放弃修改并重载',exact=True).click()
        expect(page.get_by_label('考试日期',exact=True)).to_have_value(yesterday)
        report['checks'].append('stored expired exam date can remain while editing nickname; new past dates and the Beijing leap-year five-year calendar boundary reject inline')

    # API blob error must keep the shared 401 flow, and a new user must not inherit cache.
    page.route('**/api/account/export?days=30',lambda route:route.fulfill(status=401,content_type='application/json',body=json.dumps({'error':'验收：会话失效'})))
    page.get_by_role('button',name='下载 CSV',exact=True).click()
    expect(page).to_have_url(base+'/login?redirect=/account')
    page.unroute('**/api/account/export?days=30')
    # Actual logout clears the old Cookie before switching identity.
    assert api('/auth/logout','POST')['status']==200
    bob='personal_bob_'+str(time.time_ns())
    assert api('/auth/register','POST',{'username':bob,'password':password})['status']==201
    page.get_by_label('用户名',exact=True).fill(bob);page.get_by_label('密码',exact=True).fill(password)
    page.get_by_role('button',name='登录',exact=True).click()
    expect(page.get_by_role('tab',name='学习总览',exact=True)).to_be_visible()
    page.get_by_role('tab',name='资料与偏好',exact=True).click()
    expect(page.get_by_label('练习正文字号',exact=True)).to_have_value('16')
    expect(page.get_by_label('默认题量',exact=True)).to_have_value('20')
    page.get_by_role('tab',name='账户安全',exact=True).click()
    page.get_by_role('button',name='退出当前设备',exact=True).click()
    expect(page).to_have_url(base+'/login')
    assert api('/auth/me')['status']==401
    report['checks'].append('blob 401 dispatches the shared session-expired flow; user switch restores own defaults; current logout uses existing auth API')

    # Login.vue assigns the new identity before the router guard. The preference
    # owner must therefore be independent of auth.user, including a failed GET.
    page.get_by_label('用户名',exact=True).fill(username);page.get_by_label('密码',exact=True).fill(password)
    page.get_by_role('button',name='登录',exact=True).click()
    expect(page.locator('.session-controls select')).to_have_value('10')
    page.evaluate("()=>document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/login')")
    expect(page.get_by_role('heading',name='开始今天的研习',exact=True)).to_be_visible()
    page.route('**/api/account/profile',lambda route:route.fulfill(status=500,content_type='application/json',body=json.dumps({'error':'验收：偏好暂不可用'})))
    page.get_by_label('用户名',exact=True).fill(bob);page.get_by_label('密码',exact=True).fill(password)
    page.get_by_role('button',name='登录',exact=True).click()
    expect(page.locator('.session-controls select')).to_have_value('20')
    expect(page.get_by_role('combobox',name='模块',exact=True)).to_have_value('')
    page.unroute('**/api/account/profile')
    page.locator('.topbar').get_by_role('link',name='打开个人中心',exact=True).click()
    page.get_by_role('tab',name='资料与偏好',exact=True).click()
    expect(page.get_by_label('练习正文字号',exact=True)).to_have_value('16')
    report['checks'].append('identity switch clears previous preference owner even when profile GET fails after Login.vue assigns the new user')

    # Delay real profile and dashboard reads from A, then log in as B before ACK.
    def login_as(name):
        page.evaluate("()=>document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/login')")
        expect(page.get_by_role('heading',name='开始今天的研习',exact=True)).to_be_visible()
        page.get_by_label('用户名',exact=True).fill(name);page.get_by_label('密码',exact=True).fill(password)
        page.get_by_role('button',name='登录',exact=True).click()
        expect(page.locator('.session-controls select')).to_be_visible()
    login_as(username)
    held_profiles=[];held_dashboards=[];profile_calls=[0]
    def delayed_profile(route):
        profile_calls[0]+=1
        if profile_calls[0]==1:
            route.fulfill(status=500,content_type='application/json',body=json.dumps({'error':'验收：首次偏好失败'}))
        elif profile_calls[0]==2:
            held_profiles.append((route,route.fetch()))
        else:
            route.continue_()
    page.route('**/api/account/profile',delayed_profile)
    page.route('**/api/account/dashboard?days=30',lambda route:held_dashboards.append((route,route.fetch())))
    page.goto(base+'/account')
    expect(page.get_by_role('tab',name='学习总览',exact=True)).to_be_visible()
    page.wait_for_timeout(200)
    assert len(held_profiles)==1 and len(held_dashboards)==1
    page.locator('.topbar').get_by_role('button',name='退出',exact=True).click()
    expect(page).to_have_url(base+'/login')
    page.get_by_label('用户名',exact=True).fill(bob);page.get_by_label('密码',exact=True).fill(password)
    page.get_by_role('button',name='登录',exact=True).click()
    expect(page.locator('.session-controls select')).to_have_value('20')
    route,response=held_profiles.pop();route.fulfill(response=response)
    route,response=held_dashboards.pop();route.fulfill(response=response)
    page.unroute('**/api/account/profile');page.unroute('**/api/account/dashboard?days=30')
    page.wait_for_timeout(200)
    expect(page.locator('.topbar-user')).to_contain_text(bob)
    expect(page.locator('.topbar-user .pc-avatar')).to_have_class('avatar pc-avatar pc-avatar-0')

    # A PUT really succeeds on the server, but its ACK arrives after 401 + B login.
    login_as(username)
    page.locator('.topbar').get_by_role('link',name='打开个人中心',exact=True).click()
    page.get_by_role('tab',name='资料与偏好',exact=True).click()
    page.get_by_label('昵称',exact=True).fill('A的延迟保存响应')
    held_put=[]
    page.route('**/api/account/profile',lambda route:held_put.append((route,route.fetch())) if route.request.method=='PUT' else route.continue_())
    page.get_by_role('button',name='保存资料与偏好',exact=True).click()
    expect(page.get_by_label('昵称',exact=True)).to_be_disabled()
    assert len(held_put)==1 and held_put[0][1].status==200
    page.route('**/api/account/export?days=30',lambda route:route.fulfill(status=401,content_type='application/json',body=json.dumps({'error':'验收：保存期间会话失效'})))
    page.get_by_role('button',name='下载 CSV',exact=True).click()
    expect(page).to_have_url(base+'/login?redirect=/account')
    page.unroute('**/api/account/export?days=30')
    page.get_by_label('用户名',exact=True).fill(bob);page.get_by_label('密码',exact=True).fill(password)
    page.get_by_role('button',name='登录',exact=True).click()
    expect(page.get_by_role('tab',name='学习总览',exact=True)).to_be_visible()
    route,response=held_put.pop();route.fulfill(response=response);page.unroute('**/api/account/profile')
    page.wait_for_timeout(200)
    expect(page.locator('.topbar-user')).to_contain_text(bob)
    expect(page.locator('.topbar-user .pc-avatar')).to_have_class('avatar pc-avatar pc-avatar-0')
    page.get_by_role('tab',name='资料与偏好',exact=True).click()
    expect(page.get_by_label('用户名',exact=True)).to_have_value(bob)
    expect(page.get_by_label('昵称',exact=True)).to_have_value('')
    expect(page.get_by_label('练习正文字号',exact=True)).to_have_value('16')
    report['checks'].append('delayed real A dashboard/profile reads and successful PUT ACK after logout/401 cannot replace B nickname/avatar/preferences')
    browser.close()
    db.close()
assert not report['console_errors'], report['console_errors']
(output/'task3-browser-results.json').write_text(json.dumps(report,ensure_ascii=False,indent=2))
print(json.dumps(report,ensure_ascii=False,indent=2))
