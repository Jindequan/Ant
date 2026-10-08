import { useEffect, useState } from 'react';
import { Link, useBlocker, useSearchParams } from 'react-router-dom';
import type { InventoryView, SupplyPackage, SupplyResource } from '../../api/inventory';
import { PageStateFrame } from '../../components/PageState';
import { getLocale, t } from '../../i18n';
import { PackageSetup } from './PackageSetup';
import { useInventory } from './useInventory';

function number(value:string) {
 const [whole,fraction]=value.split('.');
 const locale=getLocale()==='zh'?'zh-CN':'en-US';
 const grouped=BigInt(whole!).toLocaleString(locale);
 const separator=new Intl.NumberFormat(locale).formatToParts(1.1).find(p=>p.type==='decimal')?.value ?? '.';
 return fraction ? grouped+separator+fraction : grouped;
}
function date(value:string) {return new Intl.DateTimeFormat(getLocale()==='zh'?'zh-CN':'en-US',{dateStyle:'medium',timeZone:'UTC'}).format(new Date(`${value}T00:00:00Z`));}
export function resourceSummary(r:SupplyResource):string {
 const unit=t(`supply.unit.${r.unit}`)===`supply.unit.${r.unit}`?r.unit:t(`supply.unit.${r.unit}`);
 const window = r.reset.kind==='rolling'?t('supply.rollingSummary',{hours:String((r.reset.seconds ?? 0)/3600)}):t(`supply.reset.${r.reset.kind==='none'?'none':r.reset.period}`);
 return `${number(r.quantity)} ${unit} · ${window}`;
}
export function InventoryAccess({inventory,title}:{inventory:ReturnType<typeof useInventory>;title:string}) {
 if(inventory.isLoading || (inventory.canRead && inventory.query.isPending))return <section className="page"><h1>{title}</h1><PageStateFrame phase="loading" /></section>;
 if(!inventory.session)return <section className="page"><h1>{title}</h1><PageStateFrame phase="empty"><p>{t('config.signInRequired')}</p><Link className="button-primary" to="/login">{t('nav.login')}</Link></PageStateFrame></section>;
 if(!inventory.canRead)return <section className="page"><h1>{title}</h1><PageStateFrame phase="error" errorDetail={t('config.error.permission')} /></section>;
 return <section className="page"><h1>{title}</h1><PageStateFrame phase="error" errorDetail={t('supply.requestError')} onRetry={()=>void inventory.query.refetch()} /></section>;
}
export function InventoryResourcesPage() {
 const inv=useInventory();const [params]=useSearchParams();
 const [editor,setEditor]=useState<{snapshot:InventoryView;packageId?:string}|null>(null);
 const [archive,setArchive]=useState<SupplyPackage|null>(null);
 const [showArchived,setShowArchived]=useState(false);
 const [reviewed,setReviewed]=useState(false);
 const blocker=useBlocker(editor!==null);
 useEffect(()=>{if(!editor)return;const warn=(e:BeforeUnloadEvent)=>{e.preventDefault();e.returnValue='';};window.addEventListener('beforeunload',warn);return()=>window.removeEventListener('beforeunload',warn);},[editor]);
 if(!inv.query.data || !inv.canRead || inv.isLoading)return <InventoryAccess inventory={inv} title={t('page.resources.title')} />;
 const {document,version}=inv.query.data;
 const packs=document.packages.filter(p=>showArchived || p.status==='configured');
 async function changeStatus(pack:SupplyPackage) {
  const next=structuredClone(document);const target=next.packages.find(p=>p.id===pack.id)!;
  target.status=pack.status==='archived'?'configured':'archived';
  if(target.status==='archived') next.pools=next.pools.map(p=>({...p,packageRefs:p.packageRefs.filter(id=>id!==pack.id)}));
  if(await inv.save(next,version))setArchive(null);
 }
 return <section className="page">
  <header className="page-header"><div><h1>{t('page.resources.title')}</h1><p className="config-subtitle">{t('supply.subtitle')}</p></div>
  {!editor && inv.writable && <button className="button-primary" onClick={()=>{inv.resetFeedback();setReviewed(false);setEditor({snapshot:structuredClone(inv.query.data!)});}}>{t('supply.add')}</button>}</header>
  {!inv.writable && <p className="config-note">{t('supply.readOnly')}</p>}
  {params.get('draft') && <p className="config-note">{t('supply.legacyPreserved')} <Link to={`/resources?view=legacy&draft=${encodeURIComponent(params.get('draft')!)}`}>{t('supply.legacy')}</Link></p>}
  {inv.error && <p role="alert">{inv.error}</p>}{inv.message && <p role="status">{inv.message}</p>}
  {inv.conflict && <button className="button-secondary" disabled={inv.busy} onClick={()=>void inv.reviewLatest().then(latest=>{if(latest && editor){setEditor({...editor,snapshot:latest});setReviewed(true);}})}>{t('supply.reviewLatestAction')}</button>}
  {editor ? <PackageSetup key={editor.packageId ?? 'new'} document={editor.snapshot.document} existing={editor.snapshot.document.packages.find(p=>p.id===editor.packageId)} busy={inv.busy} onSave={async next=>{if(await inv.save(next,editor.snapshot.version))setEditor(null);}} onCancel={()=>setEditor(null)} />:<>
  {document.packages.length===0?<PageStateFrame phase="empty"><h2>{t('supply.empty')}</h2><p>{t('supply.emptyHelp')}</p></PageStateFrame>:<>
   <p className="config-note">{t('supply.configuredNote',{version:String(version)})}</p>
   {document.packages.some(p=>p.status==='archived') && <label className="config-checkbox"><input type="checkbox" checked={showArchived} onChange={e=>setShowArchived(e.target.checked)} />{t('supply.showArchived')}</label>}
   {document.accounts.map(account=>{const owned=packs.filter(p=>p.accountId===account.id);if(!owned.length)return null;return <section key={account.id} className="config-state" aria-label={`${account.providerName} · ${account.name}`}><h2>{account.providerName} · {account.name}</h2>
    {owned.map(pack=><article key={pack.id} className="supply-package"><div className="page-header"><div><h3>{pack.name}</h3><p>{t(`supply.kind.${pack.kind}`)} · {t(`supply.capability.${pack.capability}`)} · {t(`supply.status.${pack.status}`)}</p></div>{inv.writable && <div className="config-actions"><button className="button-secondary" disabled={inv.busy} onClick={()=>{inv.resetFeedback();setReviewed(false);setEditor({snapshot:structuredClone(inv.query.data!),packageId:pack.id});}}>{t('supply.edit')}</button><button className="button-secondary" disabled={inv.busy} onClick={()=>pack.status==='archived'?void changeStatus(pack):setArchive(pack)}>{t(pack.status==='archived'?'supply.restore':'supply.archive')}</button></div>}</div>
     <ul className="supply-allowances">{pack.resourceRefs.map(id=>{const r=document.resources.find(row=>row.id===id)!;return <li key={id}><strong>{r.name}</strong><span>{resourceSummary(r)}</span><small>{t(`supply.role.${r.role}`)} · {t(r.packageId?'supply.scope.package':'supply.scope.account')}{r.reset.timeZone && ` · ${r.reset.timeZone}`}{r.reset.anchorOn && ` · ${t('supply.resetAnchor')}: ${r.reset.anchorOn}`}</small></li>;})}</ul>
     <dl className="supply-commercial"><div><dt>{t('supply.price')}</dt><dd>{pack.price?`${number(pack.price.amount)} ${pack.price.currency} · ${t(`supply.cadence.${pack.price.cadence}`)}${pack.price.basis?` · ${pack.price.basis}`:''}`:t('supply.unrecorded')}</dd></div>
      {pack.startsOn && <div><dt>{t('supply.starts')}</dt><dd>{date(pack.startsOn)}</dd></div>}{pack.renewsOn && <div><dt>{t('supply.renews')}</dt><dd>{date(pack.renewsOn)}</dd></div>}{pack.expiresOn && <div><dt>{t('supply.expires')}</dt><dd>{date(pack.expiresOn)}</dd></div>}
      <div><dt>{t('supply.timezone')}</dt><dd>{pack.timeZone}</dd></div></dl>
    </article>)}
   </section>;})}
   <Link className="button-secondary" to="/pools">{t('supply.toPools')}</Link>
  </>}
  </>}
  {editor && reviewed && <section className="config-state" aria-label={t('supply.latestSaved')}><h2>{t('supply.latestSaved')} · {version}</h2>{document.packages.map(p=><div key={p.id}><strong>{p.name}</strong><p>{t(`supply.kind.${p.kind}`)} · {p.price?`${p.price.amount} ${p.price.currency} · ${t(`supply.cadence.${p.price.cadence}`)}`:t("supply.unrecorded")} · {t(`supply.status.${p.status}`)}{p.renewsOn && ` · ${t("supply.renews")}: ${date(p.renewsOn)}`}{p.expiresOn && ` · ${t("supply.expires")}: ${date(p.expiresOn)}`}</p><ul>{p.resourceRefs.map(id=>{const r=document.resources.find(x=>x.id===id)!;return <li key={id}>{r.name}: {resourceSummary(r)}</li>;})}</ul></div>)}</section>}
  {archive && <div className="config-state" role="alertdialog" aria-labelledby="archive-package-title"><h2 id="archive-package-title">{t('supply.archive')}</h2><p>{t('supply.archiveHelp',{name:archive.name})}</p><div className="config-actions"><button className="button-primary" disabled={inv.busy} onClick={()=>void changeStatus(archive)}>{t('supply.archive')}</button><button className="button-secondary" disabled={inv.busy} onClick={()=>setArchive(null)}>{t('supply.cancel')}</button></div></div>}
  {blocker.state==='blocked' && <div className="config-state" role="alertdialog" aria-labelledby="leave-supply-title"><h2 id="leave-supply-title">{t('resourceDraft.navigationTitle')}</h2><p>{t('supply.leaveHelp')}</p><div className="config-actions"><button className="button-primary" onClick={()=>blocker.reset()}>{t('resourceDraft.keepEditing')}</button><button className="button-secondary" onClick={()=>{setEditor(null);blocker.proceed();}}>{t('resourceDraft.discardLeave')}</button></div></div>}
 </section>;
}
export function InventoryAccountsPage() {
 const inv=useInventory();
 if(!inv.query.data || !inv.canRead || inv.isLoading)return <InventoryAccess inventory={inv} title={t('page.providers.title')} />;
 const d=inv.query.data.document;
 return <section className="page"><header className="page-header"><div><h1>{t('page.providers.title')}</h1><p>{t('supply.providersHelp')}</p></div><Link className="button-primary" to="/resources">{t('supply.toResources')}</Link></header>
  {!inv.writable && <p className="config-note">{t('supply.readOnly')}</p>}
  {d.accounts.length===0?<PageStateFrame phase="empty"><h2>{t('supply.noAccounts')}</h2><p>{t('supply.emptyHelp')}</p></PageStateFrame>:d.accounts.map(a=><section className="config-state" key={a.id}><h2>{a.providerName} · {a.name}</h2><p>{t('supply.accountCount',{count:String(d.packages.filter(p=>p.accountId===a.id && p.status==='configured').length)})}</p><ul>{d.packages.filter(p=>p.accountId===a.id).map(p=><li key={p.id}>{p.name} · {t(`supply.kind.${p.kind}`)} · {t(`supply.status.${p.status}`)}</li>)}</ul></section>)}
  <details><summary>{t('supply.advanced')}</summary><p>{t('supply.advancedHelp')}</p><Link to="/providers?view=advanced">{t('supply.advanced')}</Link></details>
 </section>;
}
