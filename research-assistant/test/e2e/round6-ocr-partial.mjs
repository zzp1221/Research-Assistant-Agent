#!/usr/bin/env node
import { readFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
const BASE=(process.env.RA_API_URL||'http://backend:8080').replace(/\/$/,''); const TOKEN=(process.env.RA_API_TOKEN||'').trim();
const FIX=process.env.RA_FIXTURES_DIR||join(process.cwd(),'test','fixtures');
const USER='e2e-ocr-'+Math.random().toString(36).slice(2,10); const jar=new Map();
let failures=0; const check=(ok,msg)=>{console.log((ok?'ok   ':'FAIL ')+msg); if(!ok)failures++};
async function req(method,path,{form,body}={}){const h={Accept:'application/json','X-User-Id':USER};if(TOKEN)h.Authorization='Bearer '+TOKEN;if(jar.size)h.Cookie=[...jar].map(([k,v])=>k+'='+v).join('; ');let p;if(form)p=form;else if(body!==undefined){h['Content-Type']='application/json';p=JSON.stringify(body)}const r=await fetch(BASE+path,{method,headers:h,body:p});for(const raw of r.headers.getSetCookie?.()||[]){const pair=raw.split(';')[0];const i=pair.indexOf('=');if(i>0)jar.set(pair.slice(0,i),pair.slice(i+1))}const ct=r.headers.get('content-type')||'';const data=ct.includes('json')?await r.json():await r.text();return{status:r.status,data}}
const bytes=await readFile(join(FIX,'scanned-20pages.pdf'));const form=new FormData();form.append('file',new Blob([bytes],{type:'application/pdf'}),basename('scanned-20pages.pdf'));
const up=await req('POST','/api/uploads',{form});console.log('upload',up.status,JSON.stringify(up.data?.document?.ocr_stats||{}));
const doc=up.data?.document||{};const stats=doc.ocr_stats||{};
check(doc.status==='partial','partial: status is partial ('+doc.status+')');
check(stats.ocr_pages===5,'partial: ocr_pages=5 ('+stats.ocr_pages+')');
check(stats.skipped_pages===15,'partial: skipped_pages=15 ('+stats.skipped_pages+')');
check(String(doc.error||'').includes('15 page(s) skipped'),'partial: error mentions 15 skipped ('+doc.error+')');
const q=await req('GET','/api/uploads/'+up.data.id+'/document/quality');
const pages=q.data.pages||[];
check(pages.length===20,'partial: quality has 20 pages ('+pages.length+')');
check(pages.filter(p=>p.source==='ocr').length===5,'partial: 5 OCR pages ('+pages.filter(p=>p.source==='ocr').length+')');
check(pages.filter(p=>p.skipped===true).length===15,'partial: 15 skipped pages ('+pages.filter(p=>p.skipped===true).length+')');
if(up.data?.id)await req('DELETE','/api/uploads/'+up.data.id);
if(failures){console.log('\n'+failures+' failure(s)');process.exit(1)}console.log('\npartial OCR checks passed');
