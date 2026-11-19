/** Real API + real browser regression. Requires an isolated local API and test account.
 * UI_QA_URL=http://127.0.0.1:5173 UI_QA_CREDENTIALS=/tmp/.../credentials.json npm run test:ui
 * Credentials JSON: {username,password,admin,operator}; never commit it.
 */
import assert from 'node:assert/strict'
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { browser, pause } from './browser.mjs'

const origin = process.env.UI_QA_URL || 'http://127.0.0.1:5173'
if (!['localhost', '127.0.0.1', '[::1]'].includes(new URL(origin).hostname)) throw new Error('UI regression only runs against a local isolated service')
if (!process.env.UI_QA_CREDENTIALS) throw new Error('Set UI_QA_CREDENTIALS to the temporary test credentials JSON')
const credentials = JSON.parse(readFileSync(process.env.UI_QA_CREDENTIALS, 'utf8'))
const out = resolve(process.env.UI_QA_OUTPUT || '../design-review/after')
mkdirSync(out, { recursive: true })
const page = await browser()
const checks = [], viewports = []
async function check(name, run) { if(process.env.UI_QA_ONLY&&!name.includes(process.env.UI_QA_ONLY))return;await run(); checks.push(name); console.log('PASS '+name) }
async function goto(path, selector = 'h1') { await page.goto(origin+path); await page.waitFor(() => !!document.querySelector('h1')); if(selector) await page.waitFor(new Function(`return Boolean(document.querySelector(${JSON.stringify(selector)}))`)) }
async function click(selector) { await page.evaluate(selector => { const e=document.querySelector(selector);if(!e)throw Error('Missing control: '+selector);e.focus();e.click() }, selector); await pause(100) }
async function button(text, scope='main') { await page.evaluate(({text,scope}) => { const e=[...document.querySelectorAll(scope+' button')].find(e=>e.textContent.trim()===text);if(!e)throw Error('Missing button: '+text);e.focus();e.click() }, {text,scope});await pause(150) }
async function fill(selector, value, event='input') { await page.evaluate(({selector,value,event})=>{const e=document.querySelector(selector);e.value=value;e.dispatchEvent(new Event(event,{bubbles:true}))},{selector,value,event});await pause(50) }
async function request(path, method='GET', body) { return page.evaluate(async ({path,method,body})=>{const r=await fetch('/api'+path,{method,headers:{'Content-Type':'application/json'},...(body!==undefined&&{body:JSON.stringify(body)})});return {status:r.status,body:await r.json()}},{path,method,body}) }
async function login(admin=false,operator=false) { await page.goto(origin+(admin?'/admin/login':'/login'));const r=await request(admin?'/admin/login':'/auth/login','POST',{username:admin?(operator?credentials.operator:credentials.admin):credentials.username,password:credentials.password});assert.equal(r.status,200,'Local login succeeds') }
async function escape() { await page.command('Input.dispatchKeyEvent',{type:'keyDown',key:'Escape',code:'Escape',windowsVirtualKeyCode:27});await page.command('Input.dispatchKeyEvent',{type:'keyUp',key:'Escape',code:'Escape',windowsVirtualKeyCode:27});await pause(120) }
async function ready(){await page.waitFor(()=>!document.querySelector('main [aria-busy=true]')&&!document.querySelector('main .empty[role=status]'));await pause(100)}
try {
  await page.viewport(1440,900); await login()
  await check('题库查询区分草稿与已应用范围',async()=>{
    await goto('/questions','.question-row');await ready();const before=await page.evaluate(()=>document.querySelector('h1').nextElementSibling.textContent)
    await fill('.search-field input','不应自动应用的条件');assert.equal(await page.evaluate(()=>document.querySelector('h1').nextElementSibling.textContent),before)
    assert.ok(await page.evaluate(()=>!!document.querySelector('.filter-pending')))
    await fill('.search-field input','');await fill('.filter-primary select','判断推理','change');await button('搜索');await ready();assert.ok((await page.evaluate(()=>location.search)).includes('module='))
  })
  await check('无匹配结果与失败重试使用真实接口',async()=>{
    await goto('/questions?q=ui_qa_no_matching_stem_948275');await ready();assert.ok(await page.evaluate(()=>document.body.textContent.includes('没有找到对应题目')))
    await page.command('Network.enable');await page.command('Network.setBlockedURLs',{urls:['*api/questions*']});await goto('/questions');await page.waitFor(()=>!!document.querySelector('main [role=alert]'));await page.command('Network.setBlockedURLs',{urls:[]});await button('重试');await page.waitFor(()=>!!document.querySelector('.question-row'))
  })
  await check('短题干和题图始终可以读完整',async()=>{
    await page.viewport(360,800);await goto('/questions','.question-row');await ready()
    assert.equal(await page.evaluate(()=>[...document.querySelectorAll('.brief-text')].filter(e=>e.scrollHeight>e.clientHeight+1&&!e.closest('article').querySelector('.stem-toggle')).length),0)
    const hasToggle=await page.evaluate(()=>!!document.querySelector('.stem-toggle'));if(hasToggle){await click('.stem-toggle');assert.ok(await page.evaluate(()=>!!document.querySelector('.brief-text.expanded')))}
  })
  await check('移动导航与筛选抽屉、地区键盘和双层Escape',async()=>{
    await goto('/papers','.paper-card');await click('.mobile-nav-toggle');await page.waitFor(()=>!!document.querySelector('dialog[open]'));assert.ok(await page.evaluate(()=>!!document.querySelector('dialog[open] a[href="/account"]')));await escape();assert.equal(await page.evaluate(()=>document.activeElement.getAttribute('aria-label')),'展开导航')
    await click('.mobile-filter');await page.waitFor(()=>!!document.querySelector('dialog[open]'));await click('dialog[open] [role=combobox]');await fill('dialog[open] [role=combobox]','云南');
    await page.command('Input.dispatchKeyEvent',{type:'keyDown',key:'Enter',code:'Enter',windowsVirtualKeyCode:13});await pause(80);assert.equal(await page.evaluate(()=>document.querySelector('dialog[open] [role=combobox]').value),'云南')
    await click('dialog[open] [role=combobox]');await escape();assert.ok(await page.evaluate(()=>!!document.querySelector('dialog[open]')));await escape();assert.equal(await page.evaluate(()=>document.querySelectorAll('dialog[open]').length),0)
  })
  await check('真题地区查询、紧凑卡片及单页分页',async()=>{
    await page.viewport(1440,900);await goto('/papers','.paper-card');await ready();await click('[role=combobox]');await fill('[role=combobox]','云南');await pause(50);await click('[role=option]');await button('查询试卷');await ready();assert.ok((await page.evaluate(()=>location.search)).includes('region='))
    const heights=await page.evaluate(()=>[...document.querySelectorAll('.paper-card')].map(e=>e.getBoundingClientRect().height));assert.ok(heights.every(h=>h>=200&&h<=260))
  })
  await check('真题原卷组题、草稿保存和记录继续',async()=>{
    await goto('/papers?region='+encodeURIComponent('云南')+'&year=2026','.paper-card');await ready();const papers=(await request('/papers?region='+encodeURIComponent('云南')+'&year=2026')).body;await click('.paper-card button');await page.waitFor(()=>!!document.querySelector('.option'))
    const id=await page.evaluate(()=>Number(new URLSearchParams(location.search).get('session')));const session=(await request('/practice/sessions/'+id)).body;assert.equal(session.kind,'paperset');assert.equal(session.spec.paper_id,papers[0].paper_id);assert.equal(await page.evaluate(()=>document.querySelectorAll('.analysis').length),0);await click('.option');await pause(1000)
    await goto('/records','.record-list');await ready();await click(`a[href="/practice?session=${id}"]`);await page.waitFor(()=>!!document.querySelector('.option[aria-checked=true]'));assert.equal(await page.evaluate(()=>Number(new URLSearchParams(location.search).get('session'))),id)
  })
  await check('考点目录、详情与关联真题入口',async()=>{
    await goto('/concepts','.concept-row');await ready();await click('.concept-directory li a');await click('.concept-row a');await page.waitFor(()=>!!document.querySelector('a[href*="concept_id"]'));assert.ok(await page.evaluate(()=>!!document.querySelector('a[href*="concept_id"]')))
  })
  let submitted,unfinished
  await check('真实专项组题、保存、提交前答案锁定和记录恢复',async()=>{
    await goto('/questions?module='+encodeURIComponent('政治理论'),'.question-row');await ready();await click('.question-row .row-actions>button');await page.waitFor(()=>!!document.querySelector('.option'));assert.equal(await page.evaluate(()=>document.querySelectorAll('.analysis').length),0)
    unfinished=await page.evaluate(()=>Number(new URLSearchParams(location.search).get('session')));await click('.option');await pause(1000)
    const draft=await request('/practice/sessions/'+unfinished);assert.ok(draft.body.answers.some(a=>a.answer));assert.equal(draft.body.questions[0].answer,undefined)
    await button('提交练习');await page.waitFor(()=>!!document.querySelector('.analysis'));submitted=unfinished
    await goto('/records','.record-list');await ready();assert.ok(await page.evaluate(id=>!!document.querySelector(`a[href="/practice?session=${id}"]`),submitted));assert.equal(await page.evaluate(()=>document.querySelectorAll('.stats-grid>article').length),4)
  })
  await check('未完成交卷确认与未答题解析保持锁定',async()=>{
    const create=await request('/practice/sessions','POST',{kind:'single',spec:{module:'政治理论',limit:10}});assert.equal(create.status,200);unfinished=create.body.session_id
    await goto('/practice?session='+unfinished,'.option');await button('提交练习');await page.waitFor(()=>!!document.querySelector('dialog[open]'));await button('继续作答','dialog[open]');assert.equal(await page.evaluate(()=>document.querySelectorAll('.analysis').length),0)
    await button('提交练习');await page.waitFor(()=>!!document.querySelector('dialog[open]'));await button('仍然交卷','dialog[open]');await page.waitFor(()=>!!document.querySelector('.score-banner'));assert.equal(await page.evaluate(()=>document.querySelectorAll('.analysis').length),0)
  })
  await check('错题订正、恢复、收藏、移除确认与焦点',async()=>{
    await goto('/wrongbook','.question-row');await ready();const initial=(await request('/wrongbook?status=pending&page=1&size=20')).body.total
    await click('.action-menu>button');await button('标记已订正','[role=menu]');await ready();assert.equal((await request('/wrongbook?status=pending&page=1&size=20')).body.total,initial-1)
    await button('已订正');await ready();await click('.action-menu>button');await button('取消订正','[role=menu]');await ready();assert.equal((await request('/wrongbook?status=pending&page=1&size=20')).body.total,initial)
    await page.goto(origin+'/wrongbook');await page.waitFor(()=>!!document.querySelector('.question-row'));await ready();await button('收藏','.question-row');
    await click('.action-menu>button');await button('移除','[role=menu]');await page.waitFor(()=>!!document.querySelector('dialog[open]'));await button('取消','dialog[open]');assert.equal((await request('/wrongbook?status=pending&page=1&size=20')).body.total,initial)
    const recordsBefore=(await request('/practice/records?limit=20')).body.total;await click('.action-menu>button');await button('移除','[role=menu]');await page.waitFor(()=>!!document.querySelector('dialog[open]'));await button('移除错题','dialog[open]');await ready();assert.equal((await request('/wrongbook?status=pending&page=1&size=20')).body.total,initial-1);assert.equal((await request('/practice/records?limit=20')).body.total,recordsBefore);assert.notEqual(await page.evaluate(()=>document.activeElement.tagName),'BODY')
  })
  await check('收藏取消需确认并保留原作答记录',async()=>{
    await goto('/favorites','.question-row');await ready();const initial=(await request('/favorites?page=1&size=20')).body.total;await button('取消收藏','.question-row');await page.waitFor(()=>!!document.querySelector('dialog[open]'));await button('取消收藏','dialog[open]');await ready();assert.equal((await request('/favorites?page=1&size=20')).body.total,initial-1);assert.ok((await request('/practice/records?limit=20')).body.total>=2);assert.notEqual(await page.evaluate(()=>document.activeElement.tagName),'BODY')
  })
  await check('个人资料真实保存、统计范围与安全错误反馈',async()=>{
    await goto('/account?tab=profile','.pc-profile-form');await ready();const old=await page.evaluate(()=>document.querySelector('input[aria-label="昵称"]').value);await fill('input[aria-label="昵称"]','研习体验员');await button('保存资料与偏好');await pause(400);assert.equal((await request('/account/profile')).body.nickname,'研习体验员');if(old!=='研习体验员'){await fill('input[aria-label="昵称"]',old);await button('保存资料与偏好')}
    await goto('/account','.pc-goal');await button('7 天');await pause(300);assert.ok(await page.evaluate(()=>document.querySelector('.pc-metric-heading')?.textContent.includes('7')))
    await goto('/account?tab=security','.pc-password-form');await fill('.pc-password-form input[autocomplete=current-password]','intentionally-wrong');await fill('.pc-password-form input[autocomplete=new-password]',credentials.password);const inputs=await page.evaluate(()=>document.querySelectorAll('.pc-password-form input').length);assert.equal(inputs,3);await page.evaluate(value=>{const e=document.querySelectorAll('.pc-password-form input')[2];e.value=value;e.dispatchEvent(new Event('input',{bubbles:true}))},credentials.password);await button('修改密码');await page.waitFor(()=>!!document.querySelector('.pc-security [role=alert]'));await page.evaluate(()=>{window.confirm=()=>true})
  })
  await login(true)
  await check('系统配置真实保存、留空密钥、连接失败提示',async()=>{
    await goto('/admin?tab=settings','#settings-llm');await ready();await fill('#settings-llm input','');await fill('#settings-llm input[placeholder="填写服务商提供的模型名"]','');await button('保存配置');await pause(400);assert.ok(await page.evaluate(()=>document.querySelector('[role=status]')?.textContent.includes('已保存')));await button('测试连接');await page.waitFor(()=>document.body.textContent.includes('连接失败'));assert.ok(await page.evaluate(()=>document.body.textContent.includes('连接失败')))
  })
  await check('用户查询、会员调整、封禁解封与重置密码',async()=>{
    await goto('/admin?tab=users','.admin-table');await ready();await fill('.admin-search input',credentials.username);await button('查询');await ready()
    const open=async()=>{await click('.admin-actions button');await page.waitFor(()=>!!document.querySelector('dialog[open] .admin-operation-actions'))}
    await open();await button('设为会员','dialog[open]');await button('确定设为会员','dialog[open]');await ready();assert.ok(await page.evaluate(()=>document.querySelector('.admin-table tbody').textContent.includes('会员')))
    await page.evaluate(()=>{window.confirm=()=>true});await open();await button('取消会员','dialog[open]');await ready();await open();await button('封禁','dialog[open]');await ready();assert.ok(await page.evaluate(()=>document.querySelector('.admin-table tbody').textContent.includes('已封禁')))
    await open();await button('解封','dialog[open]');await ready();await open();await button('重置密码','dialog[open]');await fill('dialog[open] input[type=password]',credentials.password);await button('确定重置密码','dialog[open]');await ready();assert.ok(await page.evaluate(()=>document.body.textContent.includes('已重置')))
  })
  await check('管理员账号创建、重置口令与删除',async()=>{
    await goto('/admin?tab=admins','.admin-create-form');await ready();await page.evaluate(()=>{window.confirm=()=>true});const name='ui_qa_'+Date.now();await fill('.admin-create-form input[autocomplete=off]',name);await fill('.admin-create-form input[type=password]',credentials.password);await button('创建管理员');await ready()
    const selector=`button[aria-label="管理管理员 ${name}"]`;await page.waitFor(new Function(`return Boolean(document.querySelector(${JSON.stringify(selector)}))`));await click(selector);await button('重置口令','dialog[open]');await fill('dialog[open] input[type=password]',credentials.password);await button('确定重置口令','dialog[open]');await ready();await click(selector);await button('删除管理员','dialog[open]');await ready();assert.equal(await page.evaluate(selector=>!!document.querySelector(selector),selector),false)
  })
  await check('管理导航、数据维护和运营管理员权限',async()=>{
    await goto('/admin?tab=maintenance','.admin-narrow');await button('更新考点映射');await pause(400);assert.ok(await page.evaluate(()=>document.body.textContent.includes('已更新')))
    await login(true,true);await goto('/admin?tab=users','.admin-table');assert.equal(await page.evaluate(()=>!!document.querySelector('.sidebar a[href="/admin?tab=settings"]')),false);assert.equal((await request('/admin/settings')).status,403)
  })
  await login()
  await check('登录会话管理与本人密码修改后重新登录',async()=>{
    await goto('/account?tab=security','.pc-password-form');await page.evaluate(()=>{window.confirm=()=>true});await button('退出其他会话');await pause(300)
    const change=async(current,next)=>{await fill('.pc-password-form input[autocomplete=current-password]',current);await fill('.pc-password-form input[autocomplete=new-password]',next);await page.evaluate(value=>{const e=document.querySelectorAll('.pc-password-form input')[2];e.value=value;e.dispatchEvent(new Event('input',{bubbles:true}))},next);await button('修改密码');await page.waitFor(()=>location.pathname==='/login')}
    const temporary=credentials.password+'a';await change(credentials.password,temporary);assert.equal((await request('/auth/login','POST',{username:credentials.username,password:temporary})).status,200);await goto('/account?tab=security','.pc-password-form');await change(temporary,credentials.password);await login()
  })
  const latest=(await request('/practice/records?limit=20')).body.items.find(x=>!x.submitted_at)
  const conceptID=(await request('/concepts')).body[0]?.id
  const routes=[['questions','/questions'],['papers','/papers'],['concepts','/concepts'],...(conceptID?[['concept-detail','/concepts/'+conceptID]]:[]),['wrongbook','/wrongbook'],['favorites','/favorites'],['records','/records'],['account','/account'],['profile','/account?tab=profile'],['security','/account?tab=security'],['review','/account?tab=review'],['practice','/practice?session='+(latest?.session_id||submitted)]]
  await check('七个断点、全部学习页面无整页横向溢出',async()=>{
    for(const [name,path] of routes){await goto(path);await ready();for(const [w,h] of [[1920,1080],[1440,900],[1024,768],[768,1024],[390,844],[360,800],[320,800]]){await page.viewport(w,h);await pause(60);const m=await page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth}));assert.ok(m.scroll<=m.width+1,`${name} overflows at ${w}`);viewports.push({name,...m});if([1440,390,320].includes(w))await page.screenshot(out+'/'+name+'-'+w+'.png')}}
  })
  await login(true)
  await check('全部后台页面断点与表格局部滚动',async()=>{
    for(const tab of ['settings','users','admins','maintenance','account']){await goto('/admin?tab='+tab);await ready();for(const [w,h] of [[1920,1080],[1440,900],[1024,768],[768,1024],[390,844],[360,800],[320,800]]){await page.viewport(w,h);const m=await page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth}));assert.ok(m.scroll<=m.width+1,`admin ${tab} overflows at ${w}`);viewports.push({name:'admin-'+tab,...m});if([1440,390,320].includes(w))await page.screenshot(out+'/admin-'+tab+'-'+w+'.png')}}
  })
  assert.deepEqual(page.errors,[])
  console.log(`PASS ${checks.length} workflows; ${viewports.length} viewport checks; 0 browser exceptions`)
} finally {
  writeFileSync(out+'/regression-results.json',JSON.stringify({checks,viewports,consoleErrors:page.errors},null,2))
  await page.close()
}
