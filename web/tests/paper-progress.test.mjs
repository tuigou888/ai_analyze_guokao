import { test } from 'node:test'
import assert from 'node:assert/strict'
import { paperAction, ownsPaperProgress } from '../src/papers/progress.js'

test('an unfinished paper resumes the saved session without constructing a new one',()=>{
 const action=paperAction(7,{paper_id:7,session_id:12,status:'in_progress',answered:3,total:20})
 assert.deepEqual(action,{kind:'resume',label:'继续作答',sessionId:12,answered:3,total:20})
})
test('a submitted paper links to its actual result',()=>{
 const action=paperAction(7,{paper_id:7,session_id:15,status:'completed',answered:18,total:20,correct:12})
 assert.equal(action.kind,'review');assert.equal(action.label,'查看复盘');assert.equal(action.sessionId,15)
})
test('a paper without an attempt retains the normal start action',()=>{
 assert.deepEqual(paperAction(7),{kind:'start',label:'开始作答'})
})
test('a response for a different paper never routes to that attempt',()=>{
 assert.deepEqual(paperAction(8,{paper_id:7,session_id:12,status:'in_progress'}),{kind:'start',label:'开始作答'})
})
test('missing or invalid session identifiers never create a resume link',()=>{
 for(const session_id of [undefined,0,-1,'bad',1.5])assert.equal(paperAction(7,{paper_id:7,session_id,status:'completed'}).kind,'start')
})

test('paper progress and late creation results are invalid after account change',()=>{
 assert.equal(ownsPaperProgress(7,7,false),true)
 assert.equal(ownsPaperProgress(7,7,true),false)
 assert.equal(ownsPaperProgress(7,8,false),false)
 assert.equal(ownsPaperProgress(7,null,false),false)
 assert.equal(ownsPaperProgress(undefined,undefined,false),false)
})
