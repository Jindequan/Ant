import { cleanup, fireEvent, render, screen, within, waitFor } from '@testing-library/react';
import { createMemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../src/app/App';
import { routes } from '../src/app/routes';
import { emptyInventory, type SupplyDocument } from '../src/api/inventory';

let stored:SupplyDocument,version:number,readonly:boolean,conflict:boolean,writes:Array<{body:any;command:string}>;
const response=(value:unknown,status=200)=>new Response(JSON.stringify(value),{status});
function mount(path='/resources'){const router=createMemoryRouter(routes,{initialEntries:[path]});render(<App router={router}/>);return router;}
function fill(label:string,value:string){fireEvent.change(screen.getByLabelText(label),{target:{value}});}
beforeEach(()=>{
 stored=emptyInventory();version=0;readonly=false;conflict=false;writes=[];
 vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo,init?:RequestInit)=>{
  const path=String(input);
  if(path==='/v1/session')return response({principalId:'admin',namespace:'demo',permissions:readonly?['read']:['read','configure'],csrfToken:'csrf-test',expiresAt:1800000000000});
  if(path==='/v1/inventory'){
   if(init?.method==='PUT'){
    const body=JSON.parse(String(init.body));expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-test');expect(new Headers(init.headers).get('If-Match')).toBe(`"${body.expectedVersion}"`);
    if(conflict || body.expectedVersion!==version)return response({error:{code:'Conflict',messageKey:'ant.error.conflict',action:'reload_current'},requestId:'test'},409);
    writes.push({body,command:body.commandId});stored=body.document;version++;
   }
   return response({version,document:stored});
  }
  throw new Error(`unexpected ${path}`);
 }));
});
afterEach(()=>{cleanup();vi.unstubAllGlobals();});
async function newPackage(name:string,kind='subscription',quantity='3000000'){
 fireEvent.click(await screen.findByRole('button',{name:'添加套餐或资源包'}));
 if(!stored.accounts.length)fill('账户名称','Team account');
 fireEvent.click(screen.getByRole('button',{name:'下一步'}));
 fill('资源包类型',kind);fill('套餐或资源包名称',name);
 if(kind==='credit')fill('到期日期（可选）','2027-01-01');
 fireEvent.click(screen.getByRole('button',{name:'下一步'}));
 fill('数量',quantity);
}
async function savePackage(name:string){fireEvent.click(screen.getByRole('button',{name:'保存资源包'}));await screen.findByRole('heading',{name});}
describe('commercial supply setup',()=>{
 it('saves independent packages on one account, then a priority pool, and archives/restores safely',async()=>{
  mount();await newPackage('Monthly plan','subscription','9007199254740993');await savePackage('Monthly plan');
  expect(stored.resources[0]?.quantity).toBe('9007199254740993');expect(stored.packages[0]?.resourceRefs).toHaveLength(1);
  expect(screen.queryByLabelText('资源标识')).not.toBeInTheDocument();expect(screen.queryByLabelText('账户编号')).not.toBeInTheDocument();
  await newPackage('Purchased credits','credit','25.01');await savePackage('Purchased credits');
  expect(stored.accounts).toHaveLength(1);expect(stored.packages).toHaveLength(2);expect(stored.packages[1]?.expiresOn).toBe('2027-01-01');expect(stored.resources[1]?.reset.kind).toBe('none');
  fireEvent.click(screen.getByRole('link',{name:'配置资源池顺序'}));
  fireEvent.click(await screen.findByRole('button',{name:'创建资源池'}));fill('资源池名称','AI supplies');
  fireEvent.click(screen.getByLabelText('Monthly plan · DeepSeek'));fireEvent.click(screen.getByLabelText('Purchased credits · DeepSeek'));
  fireEvent.click(screen.getByRole('button',{name:'上移「Purchased credits」'}));fireEvent.click(screen.getByRole('button',{name:'保存资源池'}));
  await screen.findByRole('heading',{name:'AI supplies'});expect(stored.pools[0]?.packageRefs).toEqual([stored.packages[1]!.id,stored.packages[0]!.id]);
  fireEvent.click(screen.getByRole('link',{name:'资源'}));await screen.findByRole('heading',{name:'Purchased credits'});
  const article=screen.getByRole('heading',{name:'Purchased credits'}).closest('article')!;
  fireEvent.click(within(article).getByRole('button',{name:'归档资源包'}));fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button',{name:'归档资源包'}));
  await screen.findByLabelText('显示已归档资源包');expect(stored.packages[1]?.status).toBe('archived');expect(stored.pools[0]?.packageRefs).toEqual([stored.packages[0]!.id]);
  fireEvent.click(screen.getByLabelText('显示已归档资源包'));fireEvent.click(screen.getByRole('button',{name:'恢复资源包'}));await waitFor(()=>expect(stored.packages[1]?.status).toBe('configured'));
  expect(stored.resources[1]?.quantity).toBe('25.01');
 });
 it('keeps invalid fields and stale edits, reviews latest version, and saves without an apply/draft step',async()=>{
  mount();await newPackage('Plan','subscription','3e6');fireEvent.click(screen.getByRole('button',{name:'保存资源包'}));expect(screen.getByRole('alert')).toHaveTextContent('输入内容已保留');expect(screen.getByLabelText('数量')).toHaveValue('3e6');expect(writes).toHaveLength(0);
  fill('数量','3000000');conflict=true;fireEvent.click(screen.getByRole('button',{name:'保存资源包'}));await screen.findByText(/配置已被其他操作修改/);expect(screen.getByLabelText('数量')).toHaveValue('3000000');
  conflict=false;version=1;fireEvent.click(screen.getByRole('button',{name:'查看最新保存版本'}));await screen.findByText(/已加载最新配置/);expect(screen.getByLabelText('数量')).toHaveValue('3000000');
  await savePackage('Plan');expect(writes[0]?.body.expectedVersion).toBe(1);expect(writes).toHaveLength(1);
 });
 it('supports independent windows and reuses a shared account limit rather than duplicating it',async()=>{
  mount();await newPackage('First');fireEvent.click(screen.getByRole('button',{name:'添加另一项资源'}));
  const fields=screen.getAllByRole('group',{name:/资源 [12]/});const second=within(fields[1]!);
  fireEvent.change(second.getByLabelText('资源类型'),{target:{value:'requests'}});fireEvent.change(second.getByLabelText('数量'),{target:{value:'120'}});fireEvent.change(second.getByLabelText('额度何时重置？'),{target:{value:'minute'}});fireEvent.change(second.getByLabelText('谁共享这项限制？'),{target:{value:'account'}});fireEvent.change(second.getByLabelText('资源限制方式'),{target:{value:'rate'}});
  await savePackage('First');expect(stored.resources[0]?.reset.period).toBe('month');expect(stored.resources[1]?.reset.period).toBe('minute');expect(stored.resources[1]?.packageId).toBeUndefined();
  await newPackage('Second','credit','10');expect(screen.getByText(/请求次数 · 120 requests/)).toBeInTheDocument();await savePackage('Second');
  expect(stored.resources).toHaveLength(3);expect(stored.packages[1]?.resourceRefs).toContain(stored.resources[1]!.id);
 });

 it('persists native date input events and retires/restores a removed allowance',async()=>{
  mount();await newPackage('Editable plan');
  fireEvent.click(screen.getByRole('button',{name:'上一步'}));
  fireEvent.input(screen.getByLabelText('下次续订日期（可选）'),{target:{value:'2026-11-07'}});
  fireEvent.click(screen.getByRole('button',{name:'下一步'}));
  fireEvent.click(screen.getByRole('button',{name:'添加另一项资源'}));
  fireEvent.change(screen.getAllByLabelText('数量')[1]!,{target:{value:'20'}});
  await savePackage('Editable plan');expect(stored.packages[0]?.renewsOn).toBe('2026-11-07');
  const removedId=stored.resources[1]!.id;
  fireEvent.click(screen.getByRole('button',{name:'编辑资源包'}));
  fireEvent.click(screen.getAllByRole('button',{name:'移除此资源'})[1]!);await savePackage('Editable plan');
  expect(stored.resources.find(r=>r.id===removedId)?.retired).toBe(true);expect(stored.packages[0]?.resourceRefs).not.toContain(removedId);
  fireEvent.click(screen.getByRole('button',{name:'编辑资源包'}));
  fireEvent.click(screen.getByRole('button',{name:'恢复已移除资源'}));await savePackage('Editable plan');
  expect(stored.packages[0]?.resourceRefs).toContain(removedId);expect(stored.resources.find(r=>r.id===removedId)?.retired).toBeUndefined();
 });
 it('read-only users see inventory but cannot configure accounts, packages or pools',async()=>{
  readonly=true;mount();await screen.findByRole('heading',{name:'尚未配置套餐或资源包'});expect(screen.queryByRole('button',{name:'添加套餐或资源包'})).not.toBeInTheDocument();expect(writes).toEqual([]);
 });
});
