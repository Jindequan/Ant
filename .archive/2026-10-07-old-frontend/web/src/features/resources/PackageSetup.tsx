import { useState } from 'react';
import type { SupplyDocument, SupplyPackage, SupplyResource } from '../../api/inventory';
import { exactSupplyDecimal } from '../../api/inventory';
import { t } from '../../i18n';

const providers = [ ['deepseek-standard', 'DeepSeek'], ['alibaba-dashscope', 'Alibaba Cloud'], ['volcengine-ark', 'Volcengine Ark'] ] as const;
const metrics = ['tokens', 'requests', 'money', 'storage', 'compute', 'concurrency', 'custom'] as const;
export const capabilities = ['ai.chat', 'email.send', 'storage', 'compute', 'custom.service'] as const;
const kinds = ['subscription', 'credit', 'free', 'payg'] as const;
function defaultUnit(metric: string) { return ({ tokens:'tokens', requests:'requests', money:'USD', storage:'GB', compute:'hours', concurrency:'slots' } as Record<string,string>)[metric] ?? ''; }
function validDate(value: string) { return !value || (/^\d{4}-\d{2}-\d{2}$/.test(value) && Number.isFinite(Date.parse(`${value}T00:00:00Z`)) && new Date(`${value}T00:00:00Z`).toISOString().slice(0,10) === value); }
function validZone(value: string) { try { new Intl.DateTimeFormat('en', { timeZone: value }); return !!value; } catch { return false; } }

type Row = { id: string; name: string; metric: string; unit: string; quantity: string; role: SupplyResource['role']; reset: string; hours: string; anchor: string; shared: boolean; persisted: boolean };
function newRow(kind: SupplyPackage['kind']): Row {
 const metric = kind === 'credit' || kind === 'payg' ? 'money' : 'tokens';
 return { id:crypto.randomUUID(), name:t(`supply.metric.${metric}`), metric, unit:defaultUnit(metric), quantity:'', role:kind==='payg'?'spending_limit':'allowance', reset:kind==='subscription' || kind==='payg'?'month':'none', hours:'5', anchor:'', shared:false, persisted:false };
}
function existingRow(r: SupplyResource): Row {
 return { id:r.id, name:r.name, metric:r.metric.startsWith('custom.')?'custom':r.metric, unit:r.unit, quantity:r.quantity, role:r.role, reset:r.reset.kind==='calendar'?r.reset.period! : r.reset.kind==='rolling'?'rolling':'none', hours:String((r.reset.seconds ?? 18000)/3600), anchor:r.reset.anchorOn ?? '', shared:!r.packageId, persisted:true };
}

export function PackageSetup({ document, existing, busy, onSave, onCancel }: {
 document: SupplyDocument; existing?: SupplyPackage; busy: boolean;
 onSave: (document: SupplyDocument) => Promise<void>; onCancel: () => void;
}) {
 const [step,setStep] = useState(existing?2:0);
 const [initialResourceIds] = useState(existing?.resourceRefs ?? []);
 const [packageId] = useState(existing?.id ?? crypto.randomUUID());
 const [newAccountId] = useState(crypto.randomUUID());
 const [accountId,setAccountId] = useState(existing?.accountId ?? document.accounts[0]?.id ?? 'new');
 const [provider,setProvider] = useState('deepseek-standard');
 const [customProvider,setCustomProvider] = useState('');
 const [accountName,setAccountName] = useState('');
 const [kind,setKind] = useState<SupplyPackage['kind']>(existing?.kind ?? 'subscription');
 const [name,setName] = useState(existing?.name ?? '');
 const [capability,setCapability] = useState(existing?.capability ?? 'ai.chat');
 const [zone,setZone] = useState(existing?.timeZone ?? Intl.DateTimeFormat().resolvedOptions().timeZone ?? 'UTC');
 const [starts,setStarts] = useState(existing?.startsOn ?? '');
 const [renews,setRenews] = useState(existing?.renewsOn ?? '');
 const [expires,setExpires] = useState(existing?.expiresOn ?? '');
 const [price,setPrice] = useState(existing?.price?.amount ?? '');
 const [currency,setCurrency] = useState(existing?.price?.currency ?? 'USD');
 const [cadence,setCadence] = useState(existing?.price?.cadence ?? 'month');
 const [basis,setBasis] = useState(existing?.price?.basis ?? '');
 const [rows,setRows] = useState<Row[]>(existing ? existing.resourceRefs.map(id=>document.resources.find(r=>r.id===id)!).filter(Boolean).map(existingRow) : [newRow('subscription')]);
 const [attempted,setAttempted] = useState(false);
 const [error,setError] = useState('');
 const selectedAccount = document.accounts.find(a=>a.id===accountId);
 const availableShared = document.resources.filter(r=>r.accountId===accountId && !r.packageId && !r.retired && !rows.some(row=>row.id===r.id));
 const shared=availableShared.map(r=>r.id);
 const retired=document.resources.filter(r=>r.retired && r.packageId===existing?.id);
 function updateRow(id:string, changes:Partial<Row>) { setRows(items=>items.map(r=>r.id===id?{...r,...changes}:r));setError(''); }
 function validate(which:number):boolean {
  let valid = true;
  if (which===0) valid = accountId!=='new' ? !!selectedAccount : !!accountName.trim() && accountName.trim().length<=200 && (provider!=='custom' || !!customProvider.trim());
  if (which===1) valid = !!name.trim() && name.trim().length<=200 && validZone(zone) && [starts,renews,expires].every(validDate) && (!starts || !expires || starts<=expires) && (!price || exactSupplyDecimal(price)!==null) && /^[A-Z]{3}$/.test(currency) && (!price || kind!=='subscription' || ['week','month','year'].includes(cadence)) && (!price || kind!=='payg' || cadence==='usage');
  if (which===2)setAttempted(true);
  if (which===2) valid = (rows.length+shared.length)>0 && (kind!=='payg' || rows.some(r=>r.role==='spending_limit') || availableShared.some(r=>shared.includes(r.id) && r.role==='spending_limit')) && rows.every(r=> {
   const amount=exactSupplyDecimal(r.quantity);
   return !!r.name.trim() && r.name.trim().length<=200 && !!r.unit.trim() && r.unit.trim().length<=32 && amount!==null && amount!=='0' && (r.metric!=='money' || /^[A-Z]{3}$/.test(r.unit)) && (r.role!=='spending_limit' || r.metric==='money') && (r.role!=='concurrency' || (r.metric==='concurrency' && !r.quantity.includes('.') && r.reset==='none')) && (r.role!=='rate' || r.reset!=='none') && validDate(r.anchor) && (r.reset!=='rolling' || (Number.isInteger(Number(r.hours)*3600) && Number(r.hours)>0 && Number(r.hours)*3600<=31536000));
  });
  setError(valid?'':t(`supply.invalid.${which}`));return valid;
 }
 async function submit() {
  if (![0,1,2].every(validate)) return;
  const next=structuredClone(document);
  const owner=accountId==='new'?newAccountId:accountId;
  if(accountId==='new') {
   const providerName=provider==='custom'?customProvider.trim():providers.find(p=>p[0]===provider)![1];
   const providerId=provider==='custom'?(next.accounts.find(a=>a.providerName===providerName)?.providerId ?? `custom.${newAccountId}`):provider;
   next.accounts.push({id:owner,providerId,providerName,name:accountName.trim()});
  }
  const refs:string[]=[...shared,...(existing?.resourceRefs.filter(id=>!initialResourceIds.includes(id)) ?? [])];
  // Retire removed declarations while retaining their durable identities.
  for(const old of next.resources) {
   if(initialResourceIds.includes(old.id) && !rows.some(r=>r.id===old.id) && !shared.includes(old.id))old.retired=true;
  }
  for(const row of rows) {
   const previous=next.resources.find(r=>r.id===row.id);
   const resource:SupplyResource={id:row.id,accountId:owner,...(!row.shared?{packageId}:{}),name:row.name.trim(),metric:previous?.metric ?? (row.metric==='custom'?`custom.${row.id}`:row.metric),unit:row.unit.trim(),quantity:exactSupplyDecimal(row.quantity)!,role:row.role,reset:row.reset==='none'?{kind:'none'}:row.reset==='rolling'?{kind:'rolling',seconds:Number(row.hours)*3600}:{kind:'calendar',period:row.reset as 'month',timeZone:zone,...(row.anchor?{anchorOn:row.anchor}:{})}};
   const index=next.resources.findIndex(r=>r.id===row.id);if(index<0) next.resources.push(resource);else next.resources[index]=resource;
   refs.push(row.id);
   if(row.shared)next.packages=next.packages.map(p=>p.accountId===owner && !p.resourceRefs.includes(row.id)?{...p,resourceRefs:[...p.resourceRefs,row.id]}:p);
  }
  const pack:SupplyPackage={id:packageId,accountId:owner,name:name.trim(),kind,capability,status:existing?.status ?? 'configured',timeZone:zone,resourceRefs:refs,...(starts?{startsOn:starts}:{}),...(kind==='subscription' && renews?{renewsOn:renews}:{}),...(expires?{expiresOn:expires}:{}),...(price?{price:{amount:exactSupplyDecimal(price)!,currency,cadence,basis}}:{})};
  const index=next.packages.findIndex(p=>p.id===packageId);if(index<0)next.packages.push(pack);else next.packages[index]=pack;
  // Changing declared capability removes incompatible proposed memberships.
  next.pools=next.pools.map(p=>({...p,packageRefs:p.packageRefs.filter(id=>id!==packageId || p.capability===capability)}));
  await onSave(next);
 }
 return <section className="config-state" aria-labelledby="package-setup-title">
  <h2 id="package-setup-title">{t(existing?'supply.edit':'supply.add')}</h2>
  {name && <p><strong>{name}</strong> · {selectedAccount?.name ?? accountName}</p>}
  <p className="config-note">{!existing && `${t('supply.step',{step:String(step+1)})} · `}{t(`supply.step.${step}`)}</p>
  <form onSubmit={e=>{e.preventDefault();if(step<2){if(validate(step))setStep(step+1);}else void submit();}}>
   <fieldset disabled={busy} className="supply-fieldset">
   {step===0 && <>
    <label>{t('supply.account')}<select value={accountId} disabled={!!existing} onChange={e=>{setAccountId(e.target.value);}}><option value="new">{t('supply.newAccount')}</option>{document.accounts.map(a=><option key={a.id} value={a.id}>{a.providerName} · {a.name}</option>)}</select></label>
    {accountId==='new' && <>
     <label>{t('supply.provider')}<select value={provider} onChange={e=>setProvider(e.target.value)}>{providers.map(([id,label])=><option key={id} value={id}>{label}</option>)}<option value="custom">{t('supply.customProvider')}</option></select></label>
     {provider==='custom' && <label>{t('supply.providerName')}<input maxLength={200} value={customProvider} onChange={e=>setCustomProvider(e.target.value)} /></label>}
     <label>{t('supply.accountName')}<input maxLength={200} value={accountName} onChange={e=>setAccountName(e.target.value)} placeholder={t('supply.accountPlaceholder')} /></label>
    </>}
    <p className="config-note">{t('supply.accountHelp')}</p>
   </>}
   {step===1 && <>
    <label>{t('supply.kind')}<select value={kind} onChange={e=>{const k=e.target.value as SupplyPackage['kind'];setKind(k);setCadence(k==='subscription'?'month':k==='payg'?'usage':'once');if(!existing)setRows([newRow(k)]);}}>{kinds.map(k=><option key={k} value={k}>{t(`supply.kind.${k}`)}</option>)}</select></label>
    <label>{t('supply.packageName')}<input maxLength={200} value={name} onChange={e=>setName(e.target.value)} placeholder={t('supply.packagePlaceholder')} /></label>
    <label>{t('supply.capability')}<select value={capability} onChange={e=>setCapability(e.target.value)}>{capabilities.map(c=><option key={c} value={c}>{t(`supply.capability.${c}`)}</option>)}</select></label>
    <div className="config-form-grid">
     {kind==='subscription' && <label>{t('supply.renews')}<input type="date" value={renews} onChange={e=>setRenews(e.target.value)} onInput={e=>setRenews(e.currentTarget.value)} /></label>}
     <label>{t('supply.expires')}<input type="date" value={expires} onChange={e=>setExpires(e.target.value)} onInput={e=>setExpires(e.currentTarget.value)} /></label>
    </div>
    <details><summary>{t('supply.commercial')}</summary><div className="config-form-grid">
     <label>{t('supply.price')}<input inputMode="decimal" value={price} onChange={e=>setPrice(e.target.value)} placeholder={t('supply.optional')} /></label>
     <label>{t('supply.currency')}<select value={currency} onChange={e=>setCurrency(e.target.value)}>{[...new Set(['USD','CNY','EUR',currency])].map(c=><option key={c}>{c}</option>)}</select></label>
     <label>{t('supply.cadence')}<select value={cadence} onChange={e=>setCadence(e.target.value as typeof cadence)}>{(kind==='subscription'?['week','month','year']:kind==='payg'?['usage']:['once']).map(c=><option key={c} value={c}>{t(`supply.cadence.${c}`)}</option>)}</select></label>
     {kind==='payg' && <label>{t('supply.priceBasis')}<input value={basis} maxLength={200} onChange={e=>setBasis(e.target.value)} /></label>}
     <label>{t('supply.starts')}<input type="date" value={starts} onChange={e=>setStarts(e.target.value)} onInput={e=>setStarts(e.currentTarget.value)} /></label>
     <label>{t('supply.timezone')}<input value={zone} onChange={e=>setZone(e.target.value)} /></label>
    </div></details>
   </>}
   {step===2 && <>
    <p>{t('supply.allowancesHelp')}</p>
    {rows.map((r,index)=><fieldset key={r.id} className="supply-resource"><legend>{t('supply.allowanceNumber',{number:String(index+1)})}</legend>
     <div className="config-form-grid">
      <label>{t('supply.metric')}{r.persisted?<span>{t(`supply.metric.${r.metric}`)}</span>:<select value={r.metric} onChange={e=>{const metric=e.target.value;updateRow(r.id,{metric,name:t(`supply.metric.${metric}`),unit:defaultUnit(metric),role:metric==='concurrency'?'concurrency':metric==='money' && kind==='payg'?'spending_limit':'allowance',reset:metric==='concurrency'?'none':r.reset});}}>{metrics.map(m=><option key={m} value={m}>{t(`supply.metric.${m}`)}</option>)}</select>}</label>
      <label>{t('supply.quantity')}<input inputMode="decimal" aria-label={t("supply.quantity")} aria-describedby={`quantity-help-${r.id}`} aria-invalid={attempted && (!exactSupplyDecimal(r.quantity) || exactSupplyDecimal(r.quantity)==='0')} value={r.quantity} onChange={e=>updateRow(r.id,{quantity:e.target.value})} placeholder={t('supply.quantityPlaceholder')} />{attempted && (!exactSupplyDecimal(r.quantity) || exactSupplyDecimal(r.quantity)==='0') && <small id={`quantity-help-${r.id}`}>{t('supply.quantityError')}</small>}</label>
      {r.metric==='money' || r.metric==='custom' || r.metric==='storage' || r.metric==='compute' ? <label>{t('supply.unit')}{r.metric==='custom'?<input value={r.unit} disabled={r.persisted} maxLength={32} onChange={e=>updateRow(r.id,{unit:e.target.value})} />:<select disabled={r.persisted} value={r.unit} onChange={e=>updateRow(r.id,{unit:e.target.value})}>{(r.metric==='money'?[...new Set(['USD','CNY','EUR',r.unit])]:r.metric==='storage'?['GB','bytes']:['hours','seconds']).map(u=><option key={u}>{u}</option>)}</select>}</label>:<p className="config-note">{t('supply.unit')}: {t(`supply.unit.${r.unit}`)}</p>}
      <label>{t('supply.reset')}<select value={r.reset} disabled={r.metric==='concurrency'} onChange={e=>updateRow(r.id,{reset:e.target.value})}>{['none','minute','hour','day','week','month','year','rolling'].map(v=><option key={v} value={v}>{t(`supply.reset.${v}`)}</option>)}</select></label>
      {r.reset==='rolling' && <label>{t('supply.rollingHours')}<input inputMode="decimal" value={r.hours} onChange={e=>updateRow(r.id,{hours:e.target.value})} /></label>}
     </div>
     <details><summary>{t('supply.advancedRules')}</summary><div className="config-form-grid">
      {r.persisted && <p className="config-note">{t('supply.typeFixed')}</p>}
      <label>{t('supply.resourceName')}<input value={r.name} maxLength={200} onChange={e=>updateRow(r.id,{name:e.target.value})} /></label>
      <label>{t('supply.resourceRole')}<select value={r.role} disabled={r.metric==='concurrency'} onChange={e=>updateRow(r.id,{role:e.target.value as Row['role']})}>{(r.metric==='concurrency'?['concurrency']:r.metric==='money'?['allowance','spending_limit','rate']:['allowance','rate']).map(role=><option key={role} value={role}>{t(`supply.role.${role}`)}</option>)}</select></label>
      {!['none','rolling'].includes(r.reset) && <label>{t('supply.resetAnchor')}<input type="date" value={r.anchor} onChange={e=>updateRow(r.id,{anchor:e.target.value})} onInput={e=>updateRow(r.id,{anchor:e.currentTarget.value})} /></label>}
      <label>{t('supply.scope')}<select value={r.shared?'account':'package'} disabled={r.persisted} onChange={e=>updateRow(r.id,{shared:e.target.value==='account'})}><option value="package">{t('supply.scope.package')}</option><option value="account">{t('supply.scope.account')}</option></select></label>
     </div></details>
     <button type="button" className="button-secondary" disabled={r.shared && r.persisted} onClick={()=>setRows(items=>items.filter(row=>row.id!==r.id))}>{t('supply.removeAllowance')}</button>
     {r.shared && <p className="config-note">{t('supply.sharedEditHelp')}</p>}
    </fieldset>)}
    <button type="button" className="button-secondary" onClick={()=>setRows(items=>[...items,newRow(kind)])}>{t('supply.addAllowance')}</button>
    {availableShared.length>0 && <fieldset className="supply-resource"><legend>{t('supply.shared')}</legend><p>{t('supply.sharedHelp')}</p>{availableShared.map(r=><p key={r.id}>{r.name} · {r.quantity} {r.unit}</p>)}</fieldset>}
    {retired.filter(r=>!rows.some(row=>row.id===r.id)).map(r=><div key={r.id} className="config-actions"><span>{r.name}</span><button type="button" className="button-secondary" onClick={()=>setRows(items=>[...items,existingRow(r)])}>{t('supply.restoreAllowance')}</button></div>)}
    <p className="config-note">{t('supply.review',{name:name || t(`supply.kind.${kind}`),account:selectedAccount?.name ?? accountName,count:String(rows.length+shared.length)})}</p>
   </>}
   </fieldset>
   {error && <p role="alert">{error}</p>}
   <div className="config-actions">
    <button className="button-primary" disabled={busy}>{t(busy?'supply.saving':step<2?'supply.next':'supply.savePackage')}</button>
    {step>0 && <button type="button" className="button-secondary" disabled={busy} onClick={()=>{setStep(step-1);setError('');}}>{t('supply.previous')}</button>}
    <button type="button" className="button-secondary" disabled={busy} onClick={onCancel}>{t('supply.cancel')}</button>
   </div>
  </form>
 </section>;
}
