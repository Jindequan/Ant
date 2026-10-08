import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createMemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../src/app/App';
import { routes } from '../src/app/routes';
import { emptyConfigDocument } from '../src/api/config';
import type { ConfigDocument } from '../src/api/types';
import { rememberedDraft } from '../src/api/draft-pointer';

let stored: ConfigDocument;
let version: number;
let conflict: boolean;
let readonly: boolean;
let writes: string[];
function mount(path = '/resources?draft=draft-resources') {
  const router = createMemoryRouter(routes, { initialEntries: [`${path}${path.includes('?') ? '&' : '?'}view=legacy`] });
  render(<App router={router} />);
  return router;
}
function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}
function response(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status });
}
function draft() {
  return {
    draftId: 'draft-resources',
    baseRevision: '',
    version,
    status: 'editing',
    definition: stored,
    fieldIssues: [],
  };
}

beforeEach(() => {
  localStorage.clear();
  version = 1;
  conflict = false;
  readonly = false;
  writes = [];
  stored = emptyConfigDocument();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo, init?: RequestInit) => {
      const path = String(input);
      if (path === '/v1/session')
        return response({
          principalId: 'admin',
          namespace: 'demo',
          permissions: readonly ? ['read'] : ['read', 'configure'],
          csrfToken: 'csrf-test',
          expiresAt: 1_800_000_000_000,
        });
      if (path === '/v1/catalog/providers') return response([]);
      if (path === '/v1/config')
        return response(
          {
            error: { code: 'NotFound', messageKey: 'ant.error.notFound', retryable: false, action: 'login' },
            requestId: 'test',
          },
          404,
        );
      if (path === '/v1/pools') return response([]);
      if (path === '/v1/config/drafts' && init?.method === 'POST') {
        writes.push(path);
        return response(draft(), 201);
      }
      if (path === '/v1/config/drafts/draft-resources') {
        if (init?.method === 'PATCH') {
          writes.push(path);
          expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-test');
          expect(new Headers(init.headers).get('If-Match')).toBe(`"${version}"`);
          if (conflict)
            return response(
              {
                error: {
                  code: 'Conflict',
                  messageKey: 'ant.error.conflict',
                  retryable: false,
                  action: 'reload_current',
                },
                requestId: 'conflict-test',
              },
              409,
            );
          stored = JSON.parse(String(init.body)).definition as ConfigDocument;
          version++;
        }
        return response(draft());
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

async function account() {
  await screen.findByRole('heading', { name: '添加订阅账户' });
  fill('供应商类型标识', 'future-provider');
  fill('账户编号', 'subscription-1');
  fill('账户显示名称', 'Monthly subscription');
  fireEvent.click(screen.getByRole('button', { name: '添加到账户列表' }));
  fireEvent.click(screen.getByRole('button', { name: '添加资源规则' }));
  await screen.findByLabelText('资源标识');
}

describe('resource configuration first', () => {
  it('saves exact subscription rules without credentials or provider calls and resumes for editing/removal', async () => {
    mount();
    await account();
    fill('资源标识', 'monthly-tokens');
    fill('声明额度（最小单位）', '9007199254740993');
    fill('重置时区', 'Asia/Singapore');
    fill('到期时间（可选）', '2027-01-01T00:00:00+08:00');
    fireEvent.click(screen.getByRole('button', { name: '加入草稿' }));
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await screen.findByText(/资源配置已保存为草稿，版本 2/);
    expect(stored.accounts[0]).toMatchObject({
      providerTypeId: 'future-provider',
      displayName: 'Monthly subscription',
    });
    expect(stored.resources[0]).toMatchObject({
      resourceId: 'monthly-tokens',
      configuredCapacity: '9007199254740993',
      window: { kind: 'fixed', period: 'month', timeZone: 'Asia/Singapore' },
      capacityEvidenceRef: '',
    });
    expect(stored.bindings).toEqual([]);
    expect(stored.evidence).toEqual([]);
    expect(rememberedDraft('demo', 'admin')).toBe('draft-resources');
    expect(rememberedDraft('other', 'admin')).toBe('');
    cleanup();
    mount();
    fireEvent.click(await screen.findByRole('button', { name: '编辑规则' }));
    expect(screen.getByLabelText('声明额度（最小单位）')).toHaveValue('9007199254740993');
    fill('声明额度（最小单位）', '50000');
    fill('重置窗口', 'rolling');
    fill('滚动时长（毫秒）', '3600000');
    fireEvent.click(screen.getByRole('button', { name: '加入草稿' }));
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(stored.resources[0]!.window).toEqual({ kind: 'rolling', durationMillis: 3600000 }));
    expect(stored.resources[0]!.resourceId).toBe('monthly-tokens');
    fireEvent.click(screen.getByRole('button', { name: '移除资源' }));
    fireEvent.click(screen.getAllByRole('button', { name: '移除资源' }).at(-1)!);
    expect(stored.resources).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(stored.resources).toHaveLength(0));
    expect(writes).toHaveLength(3);
  });

  it('keeps invalid quantities, windows and unzoned dates in the form', async () => {
    mount();
    await account();
    fill('声明额度（最小单位）', '9223372036854775808');
    fireEvent.click(screen.getByRole('button', { name: '加入草稿' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('规则未加入草稿');
    expect(screen.getByLabelText('声明额度（最小单位）')).toHaveValue('9223372036854775808');
    fill('声明额度（最小单位）', '100');
    fill('重置时区', 'invalid/time-zone');
    fireEvent.click(screen.getByRole('button', { name: '加入草稿' }));
    expect(screen.getByRole('alert')).toHaveTextContent('规则未加入草稿');
    fill('重置时区', 'UTC');
    fill('到期时间（可选）', '2027-01-01');
    fireEvent.click(screen.getByRole('button', { name: '加入草稿' }));
    expect(screen.getByRole('alert')).toHaveTextContent('规则未加入草稿');
    fill('到期时间（可选）', '2027-02-30T00:00:00+08:00');
    fireEvent.click(screen.getByRole('button', { name: '加入草稿' }));
    expect(screen.getByRole('alert')).toHaveTextContent('规则未加入草稿');
    expect(stored.resources).toEqual([]);
    expect(writes).toEqual([]);
  });

  it('preserves local edits on a conflicting save and requires confirmation to discard', async () => {
    mount();
    await account();
    fill('声明额度（最小单位）', '10');
    fireEvent.click(screen.getByRole('button', { name: '加入草稿' }));
    conflict = true;
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('本地修改已保留');
    expect(screen.getByRole('button', { name: '编辑规则' })).toBeEnabled();
    expect(stored.resources).toEqual([]);
    fireEvent.click(screen.getByRole('button', { name: '重新载入已保存版本' }));
    const dialog = screen.getByRole('alertdialog');
    fireEvent.click(within(dialog).getByRole('button', { name: '取消' }));
    expect(screen.getByRole('button', { name: '编辑规则' })).toBeEnabled();
    fireEvent.click(screen.getByRole('link', { name: '查看已激活资源' }));
    expect(screen.getByRole('alertdialog')).toHaveTextContent('还有未保存的修改');
    fireEvent.click(screen.getByRole('button', { name: '继续编辑' }));
    expect(screen.getByRole('button', { name: '编辑规则' })).toBeEnabled();
  });

  it('saves the draft before continuing after an unsaved-navigation warning', async () => {
    const router = mount();
    await account();
    fill('声明额度（最小单位）', '100');
    fireEvent.click(screen.getByRole('button', { name: '加入草稿' }));
    fireEvent.click(screen.getByRole('link', { name: '查看已激活资源' }));
    expect(screen.getByRole('alertdialog')).toHaveTextContent('还有未保存的修改');
    fireEvent.click(screen.getByRole('button', { name: '保存并离开' }));
    await waitFor(() => expect(router.state.location.pathname).toBe('/resources'));
    expect(router.state.location.search).toBe('?view=active');
    expect(stored.resources[0]?.configuredCapacity).toBe('100');
    expect(writes).toHaveLength(1);
    expect(await screen.findByRole('link', { name: '继续资源草稿' })).toHaveAttribute(
      'href',
      '/resources?draft=draft-resources&view=legacy',
    );
  });

  it('blocks editing or removing linked resources and routes malformed drafts to repair', async () => {
    stored.accounts = [
      { accountId: 'subscription-1', providerTypeId: 'future-provider', displayName: 'Subscription', enabled: true },
    ];
    stored.resources = [
      {
        resourceId: 'linked-resource',
        ownerKind: 'account',
        ownerId: 'subscription-1',
        metricId: 'metric.tokens.total',
        unit: 'token',
        quantumExp: 0,
        kind: 'counter',
        commercialRole: 'limit',
        semanticsRef: 'sem.pending',
        semanticsRevision: 'pending',
        window: { kind: 'lifetime' },
        metered: false,
        configuredCapacity: '100',
        capacityEvidenceRef: '',
      },
    ];
    stored.bindings = [
      {
        bindingId: 'binding-1',
        accountId: 'subscription-1',
        domain: 'ai/text',
        endpoint: 'https://example.test',
        credentialRef: 'ref-1',
        credentialRevision: 'rev-1',
        enabled: true,
        capabilityProfiles: [],
        resourceRefs: ['linked-resource'],
      },
    ];
    mount();
    expect(await screen.findByRole('button', { name: '编辑规则' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '移除资源' })).toBeDisabled();
    expect(screen.getByText(/已被 binding-1 引用/)).toBeInTheDocument();
    expect(writes).toEqual([]);
    cleanup();
    stored.resources = [null] as unknown as ConfigDocument['resources'];
    mount();
    expect(await screen.findByRole('alert')).toHaveTextContent('需要修复文档结构');
    expect(screen.getByRole('link', { name: '返回供应商配置' })).toBeInTheDocument();
    expect(writes).toEqual([]);
  });

  it('offers configure-first from an empty installation and read-only sessions cannot write', async () => {
    const router = mount('/resources');
    fireEvent.click(await screen.findByRole('button', { name: '配置资源' }));
    await screen.findByRole('heading', { name: '添加订阅账户' });
    expect(router.state.location.search).toBe('?draft=draft-resources&view=legacy');
    cleanup();
    readonly = true;
    writes = [];
    mount();
    expect(await screen.findByText('此账户只有读取权限，不能创建、保存或激活配置。')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '保存草稿' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '添加订阅账户' })).not.toBeInTheDocument();
    expect(writes).toEqual([]);
  });
});
