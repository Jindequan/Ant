import { useEffect, useState } from 'react';
import { Link, useBlocker } from 'react-router-dom';
import type { SupplyPool } from '../../api/inventory';
import { t } from '../../i18n';
import { InventoryAccess, resourceSummary } from '../resources/InventoryPage';
import { capabilities } from '../resources/PackageSetup';
import { useInventory } from '../resources/useInventory';
import { PageStateFrame } from '../../components/PageState';

export function InventoryPoolsPage() {
 const inv=useInventory();const [editing,setEditing]=useState<{pool:SupplyPool;version:number}|null>(null);const [fieldError,setFieldError]=useState('');
 const blocker=useBlocker(editing!==null);
 useEffect(()=>{if(!editing)return;const warn=(e:BeforeUnloadEvent)=>{e.preventDefault();e.returnValue='';};window.addEventListener('beforeunload',warn);return()=>window.removeEventListener('beforeunload',warn);},[editing]);
 if(!inv.query.data || !inv.canRead || inv.isLoading)return <InventoryAccess inventory={inv} title={t('page.pools.title')} />;
 const {document,version}=inv.query.data;
 const configured=document.packages.filter(p=>p.status==='configured');
 function update(change:Partial<SupplyPool>) {if(editing)setEditing({...editing,pool:{...editing.pool,...change}});setFieldError('');}
 function move(id:string,delta:number) {if(!editing)return;const ids=[...editing.pool.packageRefs];const at=ids.indexOf(id),to=at+delta;if(to<0 || to>=ids.length)return;[ids[at],ids[to]]=[ids[to]!,ids[at]!];update({packageRefs:ids});}
 async function submit() {
  if(!editing || !editing.pool.name.trim()){setFieldError(t('supply.pool.invalid'));return;}
  const next=structuredClone(document);const pool={...editing.pool,name:editing.pool.name.trim()};
  const at=next.pools.findIndex(p=>p.id===pool.id);if(at<0)next.pools.push(pool);else next.pools[at]=pool;
  if(await inv.save(next,editing.version))setEditing(null);
 }
 function renderPackage(id:string,index:number,controls:boolean) {
  const pack=document.packages.find(p=>p.id===id);if(!pack)return <li key={id}>{t('supply.pool.unavailable')}</li>;
  const account=document.accounts.find(a=>a.id===pack.accountId)!;
  return <li key={id} className="supply-pool-member"><div><strong>{index+1}. {pack.name}</strong><p>{account.providerName} · {account.name}</p><ul>{pack.resourceRefs.map(ref=>{const r=document.resources.find(x=>x.id===ref)!;return <li key={ref}>{r.name}: {resourceSummary(r)}{!r.packageId && ` · ${t('supply.scope.account')}`}</li>;})}</ul></div>
  {controls && <div className="config-actions"><button type="button" className="button-secondary" disabled={index===0 || inv.busy} aria-label={t('supply.pool.up',{name:pack.name})} onClick={()=>move(id,-1)}>{t('supply.pool.moveUp')}</button><button type="button" className="button-secondary" disabled={index===(editing?.pool.packageRefs.length ?? 0)-1 || inv.busy} aria-label={t('supply.pool.down',{name:pack.name})} onClick={()=>move(id,1)}>{t('supply.pool.moveDown')}</button></div>}</li>;
 }
 return <section className="page"><header className="page-header"><div><h1>{t('page.pools.title')}</h1><p>{t('supply.pool.help')}</p></div>{!editing && inv.writable && <button className="button-primary" disabled={!configured.length} onClick={()=>{inv.resetFeedback();setFieldError('');setEditing({pool:{id:crypto.randomUUID(),name:'',capability:configured[0]!.capability,packageRefs:[]},version});}}>{t('supply.pool.add')}</button>}</header>
  {!inv.writable && <p className="config-note">{t('supply.readOnly')}</p>}
  {!configured.length && <p>{t('supply.pool.prerequisite')} <Link to="/resources">{t('supply.toResources')}</Link></p>}
  {inv.error && <p role="alert">{inv.error}</p>}{inv.message && <p role="status">{inv.message}</p>}{fieldError && <p role="alert">{fieldError}</p>}
  {inv.conflict && <button className="button-secondary" disabled={inv.busy} onClick={()=>void inv.reviewLatest().then(latest=>{if(latest && editing)setEditing({...editing,version:latest.version,pool:{...editing.pool,packageRefs:editing.pool.packageRefs.filter(id=>latest.document.packages.some(p=>p.id===id && p.status==='configured' && p.capability===editing.pool.capability))}});})}>{t('supply.reviewLatestAction')}</button>}
  {editing?<form className="config-state" onSubmit={e=>{e.preventDefault();void submit();}}><h2>{t('supply.pool.configure')}</h2><fieldset disabled={inv.busy} className="supply-fieldset">
   <label>{t('supply.pool.name')}<input value={editing.pool.name} maxLength={200} onChange={e=>update({name:e.target.value})} /></label>
   <label>{t('supply.capability')}<select value={editing.pool.capability} onChange={e=>update({capability:e.target.value,packageRefs:[]})}>{capabilities.map(c=><option key={c} value={c}>{t(`supply.capability.${c}`)}</option>)}</select></label>
   <fieldset className="supply-resource"><legend>{t('supply.pool.members')}</legend>{configured.filter(p=>p.capability===editing.pool.capability).map(p=><label className="config-checkbox" key={p.id}><input type="checkbox" checked={editing.pool.packageRefs.includes(p.id)} onChange={e=>update({packageRefs:e.target.checked?[...editing.pool.packageRefs,p.id]:editing.pool.packageRefs.filter(id=>id!==p.id)})} />{p.name} · {document.accounts.find(a=>a.id===p.accountId)?.providerName}</label>)}{!configured.some(p=>p.capability===editing.pool.capability) && <p>{t('supply.pool.noCompatible')}</p>}</fieldset>
   <p>{t('supply.pool.priorityHelp')}</p><ol className="supply-pool-members">{editing.pool.packageRefs.map((id,index)=>renderPackage(id,index,true))}</ol>
  </fieldset><div className="config-actions"><button className="button-primary" disabled={inv.busy}>{t(inv.busy?'supply.saving':'supply.pool.save')}</button><button type="button" className="button-secondary" disabled={inv.busy} onClick={()=>{setEditing(null);setFieldError('');}}>{t('supply.cancel')}</button></div></form>:document.pools.length===0?<PageStateFrame phase="empty"><h2>{t('supply.pool.empty')}</h2><p>{t('supply.pool.priorityHelp')}</p></PageStateFrame>:document.pools.map(pool=><section key={pool.id} className="config-state"><div className="page-header"><div><h2>{pool.name}</h2><p>{t(`supply.capability.${pool.capability}`)} · {t('supply.pool.configured')}</p></div>{inv.writable && <button className="button-secondary" disabled={inv.busy} onClick={()=>{inv.resetFeedback();setFieldError('');setEditing({pool:structuredClone(pool),version});}}>{t('supply.pool.edit')}</button>}</div><ol className="supply-pool-members">{pool.packageRefs.map((id,index)=>renderPackage(id,index,false))}</ol>{!pool.packageRefs.length && <p>{t('supply.pool.noMembers')}</p>}</section>)}
  <details><summary>{t('supply.pool.active')}</summary><Link to="/pools?view=active">{t('supply.pool.active')}</Link></details>
  {blocker.state==='blocked' && <div className="config-state" role="alertdialog" aria-labelledby="leave-pool-title"><h2 id="leave-pool-title">{t('resourceDraft.navigationTitle')}</h2><p>{t('supply.leaveHelp')}</p><button className="button-primary" onClick={()=>blocker.reset()}>{t('resourceDraft.keepEditing')}</button><button className="button-secondary" onClick={()=>{setEditing(null);blocker.proceed();}}>{t('resourceDraft.discardLeave')}</button></div>}
 </section>;
}
