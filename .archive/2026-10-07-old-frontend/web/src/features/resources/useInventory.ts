import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError } from '../../api/client';
import { inventoryKey, readInventory, saveInventory, type InventoryInput, type SupplyDocument } from '../../api/inventory';
import { useSession } from '../../auth/SessionContext';
import { t } from '../../i18n';

export function useInventory() {
 const { session,isLoading }=useSession(); const client=useQueryClient();
 const canRead=!!session?.permissions.some(p=>p==='read' || p==='configure');
 const writable=!!session?.permissions.includes('configure') && !!session.csrfToken;
 const key=inventoryKey(session?.namespace,session?.principalId);
 const query=useQuery({queryKey:key,queryFn:readInventory,enabled:canRead,retry:false});
 const [busy,setBusy]=useState(false); const [error,setError]=useState(''); const [message,setMessage]=useState(''); const [conflict,setConflict]=useState(false);
 const pending=useRef<{identity:string; input:InventoryInput}|null>(null);
 useEffect(()=>{if(query.error instanceof ApiError && query.error.status===401)client.setQueryData(['session'],null);},[query.error,client]);
 async function save(document:SupplyDocument, expectedVersion:number):Promise<boolean> {
  if(!writable || !session?.csrfToken)return false;
  setBusy(true);setError('');setMessage('');setConflict(false);
  const identity=JSON.stringify({document,expectedVersion});
  if(pending.current?.identity!==identity)pending.current={identity,input:{commandId:crypto.randomUUID(),expectedVersion,document}};
  let committed=false;
  try {
   await saveInventory(pending.current.input,session.csrfToken); committed=true;
   const actual=await readInventory();client.setQueryData(key,actual);
   pending.current=null;setMessage(t('supply.saved'));return true;
  }catch(failure){
   if(failure instanceof ApiError && failure.status===409){setConflict(true);setError(t('supply.conflict'));}
   else setError(t(committed?'supply.readbackError':failure instanceof ApiError && failure.status===403?'config.error.permission':failure instanceof ApiError && failure.status===401?'config.error.session':failure instanceof ApiError && failure.status===400?'supply.serverInvalid':'supply.requestError'));
   if(failure instanceof ApiError && failure.status===401)client.setQueryData(['session'],null);
   return false;
  }finally{setBusy(false);}
 }
 async function reviewLatest() {
  setBusy(true);
  try { const actual=await readInventory();client.setQueryData(key,actual);setConflict(false);setError('');setMessage(t('supply.reviewLatest'));return actual; }
  catch { setError(t('supply.requestError'));return null; }
  finally { setBusy(false); }
 }
 return {session,isLoading,canRead,writable,query,busy,error,message,conflict,save,reviewLatest,resetFeedback:()=>{setError('');setMessage('');setConflict(false);}};
}
