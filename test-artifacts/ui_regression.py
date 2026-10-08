"""End-to-end regression against an isolated real-question snapshot.
Usage: python3 test-artifacts/ui_regression.py http://127.0.0.1:18080 var/web-test/gk.sqlite
Never calls admin/test-llm or any distillation endpoint.
"""
import json,sqlite3,sys,time
from pathlib import Path
from playwright.sync_api import sync_playwright,expect
base=sys.argv[1].rstrip('/')
db=sqlite3.connect(f'file:{sys.argv[2]}?mode=ro',uri=True)
out=Path('test-artifacts');out.mkdir(exist_ok=True)
report={'checks':[],'console_errors':[],'screenshots':[]}
def check(name):report['checks'].append(name)

class BrowserReply:
 def __init__(self, value):self.value=value;self.ok=value['ok']
 def json(self):return json.loads(self.value['text'])
 def text(self):return self.value['text']

class BrowserAPI:
 """Use actual browser Origin and Secure-cookie rules, including localhost."""
 def __init__(self,page):self.page=page
 def request(self,url,method='GET',data=None):
  return BrowserReply(self.page.evaluate("""async ({url,method,data}) => {
   const options={method,credentials:'same-origin',headers:{'Content-Type':'application/json'}};
   if(data!==null) options.body=JSON.stringify(data);
   const response=await fetch(url,options);
   return {ok:response.ok,text:await response.text()};
  }""",{'url':url,'method':method,'data':data}))
 def get(self,url):return self.request(url)
 def post(self,url,data=None):return self.request(url,'POST',data)

with sync_playwright() as pw:
 browser=pw.chromium.launch(executable_path='/usr/bin/google-chrome',headless=True,args=['--no-sandbox'])
 ctx=browser.new_context(viewport={'width':1440,'height':1000},reduced_motion='reduce')
 page=ctx.new_page()
 browser_api=BrowserAPI(page)
 page.on('pageerror',lambda e:report['console_errors'].append(str(e)))
 page.goto(base+'/')
 expect(page).to_have_url(base+'/login?redirect=/questions')
 check('anonymous root redirects to user login')
 page.get_by_role('button',name='创建账户',exact=True).click()
 username='browser_'+str(int(time.time()))
 page.get_by_label('用户名',exact=True).fill(username)
 page.get_by_label('密码',exact=True).fill('browser-test-password')
 page.get_by_role('button',name='注册并登录',exact=True).click()
 expect(page.get_by_role('heading',name='从一道真题开始')).to_be_visible()
 expect(page.locator('.question-row')).to_have_count(20)
 check('registration/login and real question bank')
 page.screenshot(path=str(out/'questions-desktop.png'),full_page=True);report['screenshots'].append('questions-desktop.png')
 page.get_by_label('本次题量').select_option('50')
 page.get_by_role('button',name='开始专项练习',exact=True).click()
 expect(page.locator('.question-index button')).to_have_count(50)
 sid=page.url.split('session=')[1]
 s=browser_api.get(base+'/api/practice/sessions/'+sid).json()
 assert len(s['questions'])==50 and 'answer' not in s['questions'][0]
 def choose(q,answer):
  labels={o['label']:o['content'] for o in q['options']}
  for letter in answer:
   button=page.locator('.option').filter(has=page.locator('.option-letter',has_text=letter))
   button.click()
 for i,q in enumerate(s['questions']):
  answer=db.execute('select answer from question where id=?',(q['id'],)).fetchone()[0]
  choose(q,answer)
  if i==19:
   expect(page.get_by_text('作答已保存',exact=True)).to_be_visible(timeout=10000)
   page.reload();expect(page.locator('.question-index button.answered')).to_have_count(20)
   page.locator('.question-index button').nth(i).click()
   check('draft survives browser reload')
  if i<49:page.get_by_role('button',name='下一题',exact=True).click()
 page.get_by_role('button',name='提交练习',exact=True).first.click()
 expect(page.get_by_role('heading',name='本次正确率 100%')).to_be_visible(timeout=20000)
 check('50 consecutive real questions graded on server')
 page.screenshot(path=str(out/'practice-desktop.png'),full_page=True);report['screenshots'].append('practice-desktop.png')
 def session(qids):
  res=browser_api.post(base+'/api/practice/sessions',data={'kind':'single','spec':{'question_ids':qids}})
  assert res.ok,res.text();s=res.json();page.goto(base+f'/practice?session={s["session_id"]}');expect(page.locator('.options')).to_be_visible();return s
 s=session([12932]);assert len(s['questions'][0]['options'])==8
 choose(s['questions'][0],'H');page.get_by_role('button',name='提交练习',exact=True).first.click()
 expect(page.get_by_text('回答正确 · 你的答案 H · 正确答案 H',exact=True)).to_be_visible()
 check('real Shaanxi A–H question')
 s=session([10]);expect(page.locator('.option')).to_have_count(2);assert page.locator('.option').nth(0).inner_text().strip().endswith('正确')
 choose(s['questions'][0],'A');page.get_by_role('button',name='提交练习',exact=True).first.click();expect(page.locator('.result-status.err')).to_be_visible()
 check('judge correct/incorrect labels and wrong answer')
 page.goto(base+'/wrongbook');expect(page.locator('.question-row')).to_have_count(1)
 page.get_by_role('button',name='开始练习',exact=False).click();expect(page.locator('.options')).to_be_visible();choose({'options':[{'label':'B','content':'错误'}]},'B')
 page.get_by_role('button',name='提交练习',exact=True).first.click();expect(page.locator('.result-status.ok')).to_be_visible()
 page.goto(base+'/wrongbook');expect(page.get_by_role('heading',name='暂无待订正错题')).to_be_visible()
 check('wrongbook auto-insert and correct redo resolves')
 # Formula rendering: use a short restored fraction from the actual question bank.
 formula=db.execute("select id,answer from question where answer_type='single' and explanation_with_formula like '%$%frac%' order by length(explanation_with_formula) limit 1").fetchone()
 s=session([formula[0]]);choose(s['questions'][0],formula[1]);page.get_by_role('button',name='提交练习',exact=True).first.click();expect(page.locator('.analysis .katex').first).to_be_visible()
 check('restored LaTeX renders as KaTeX')
 # Real images remain in the original stem, not replaced by OCR text.
 picture=db.execute("select id from question where module='判断推理' and stem like '%IMG:%' and answer_type='single' limit 1").fetchone()[0]
 s=session([picture]);expect(page.locator('.question-stem img').first).to_be_visible();page.wait_for_function("Array.from(document.querySelectorAll('.question-stem img')).every(i=>i.complete&&i.naturalWidth>0)")
 check('question images delivered from original dataset')
 page.goto(base+'/concepts');expect(page.locator('.concept-card').first).to_be_visible();page.locator('.concept-card').first.click();expect(page.get_by_role('heading',name='从实例中理解')).to_be_visible()
 check('concept map, detail and sample questions')
 page.goto(base+'/papers');expect(page.locator('.paper-card')).to_have_count(12);page.locator('.paper-card').first.get_by_role('button',name='开始作答',exact=False).click();expect(page.locator('.question-index button').first).to_be_visible()
 check('paper session builds original question order')
 page.goto(base+'/records');expect(page.locator('.record-list article').first).to_be_visible();check('history and resume links')
 page.goto(base+'/questions');page.get_by_label('关键词',exact=True).fill('增长率');page.get_by_role('button',name='筛选 / 搜索',exact=True).click();expect(page.locator('.question-row').first).to_be_visible();check('Chinese keyword search')
 for path in ['/questions','/papers','/concepts','/wrongbook','/records','/account']:
  page.set_viewport_size({'width':375,'height':812});page.goto(base+path);page.wait_for_load_state('networkidle')
  assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth'),path+' overflows'
  assert not page.get_by_text('操作失败，请稍后重试',exact=True).count(),path
 page.goto(base+'/questions');page.wait_for_load_state('networkidle')
 page.screenshot(path=str(out/'questions-mobile.png'),full_page=True);report['screenshots'].append('questions-mobile.png')
 check('375px layouts without horizontal page overflow')
 s=session([12932]);assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth');page.screenshot(path=str(out/'practice-mobile.png'),full_page=True);report['screenshots'].append('practice-mobile.png')
 sid=s['session_id'];choose(s['questions'][0],'H')
 page.locator('.topbar').get_by_role('button',name='退出',exact=True).click();expect(page.get_by_role('heading',name='开始今天的研习')).to_be_visible()
 page.get_by_label('用户名',exact=True).fill(username);page.get_by_label('密码',exact=True).fill('browser-test-password');page.get_by_role('button',name='登录',exact=True).click();expect(page.get_by_role('heading',name='从一道真题开始')).to_be_visible()
 saved=browser_api.get(base+f'/api/practice/sessions/{sid}').json();assert saved['answers'][0]['answer']=='H'
 check('logout saves pending draft before revoking session')
 page.goto(base+'/admin');expect(page.get_by_role('heading',name='管理员登录')).to_be_visible();check('user account cannot enter admin')
 page.get_by_label('用户名',exact=True).fill('webtester');page.get_by_label('密码',exact=True).fill('ui-test-password');page.get_by_role('button',name='登录',exact=True).click();expect(page.get_by_role('heading',name='网站管理')).to_be_visible()
 page.wait_for_load_state('networkidle');assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth');check('independent admin login and 375px settings layout')
 page.get_by_label('当前密码',exact=True).fill('wrong-current-password')
 page.get_by_label('新密码',exact=True).fill('ui-test-password-next')
 page.get_by_role('button',name='修改并重新登录',exact=True).click()
 expect(page.get_by_text('账号或密码错误',exact=True)).to_be_visible()
 page.get_by_label('当前密码',exact=True).fill('ui-test-password')
 page.get_by_role('button',name='修改并重新登录',exact=True).click()
 expect(page.get_by_role('heading',name='管理员登录')).to_be_visible()
 page.get_by_label('用户名',exact=True).fill('webtester');page.get_by_label('密码',exact=True).fill('ui-test-password-next')
 page.get_by_role('button',name='登录',exact=True).click();expect(page.get_by_role('heading',name='网站管理')).to_be_visible()
 check('admin password rejects wrong current password, revokes session and accepts new password')
 page.goto(base+'/login');page.wait_for_load_state('networkidle');assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth')
 page.screenshot(path=str(out/'login-mobile.png'),full_page=True);report['screenshots'].append('login-mobile.png')
 browser.close()
assert not report['console_errors'],report['console_errors']
(out/'ui-results.json').write_text(json.dumps(report,ensure_ascii=False,indent=2))
print(json.dumps(report,ensure_ascii=False,indent=2))
