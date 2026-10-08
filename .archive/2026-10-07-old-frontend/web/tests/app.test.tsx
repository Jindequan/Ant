/**
 * App shell smoke tests (R16): the four primary nav items render, `/`
 * redirects to the providers page with its honest empty state, disabled
 * controls expose their reason as visible text (not title-only), no
 * implementation jargon leaks into user-facing copy, and no React warnings
 * leak to console.error.
 */
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createMemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../src/app/App';
import { routes } from '../src/app/routes';
import type { ConfigDocument } from '../src/api/types';


/** Implementation terms banned from user-facing copy (R16). */
const IMPLEMENTATION_TERMS = /(T2|T2\.1|后端契约|后端接线|接线|server端)/i;

function renderAt(entry: string) {
  const legacy = entry.startsWith('/providers') ? 'advanced' : entry.startsWith('/pools') ? 'active' : entry.startsWith('/resources') ? 'legacy' : '';
  const path = legacy ? `${entry}${entry.includes('?') ? '&' : '?'}view=${legacy}` : entry;
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  render(<App router={router} />);
  return router;
}

afterEach(cleanup);
beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
    error: { code: 'Unauthenticated', messageKey: 'ant.error.unauthenticated', retryable: false, action: 'login' },
    requestId: 'test',
  }), { status: 401, headers: { 'Content-Type': 'application/json' } })));
});
afterEach(() => vi.unstubAllGlobals());

describe('app shell', () => {
  it('renders four nav items and the unauthenticated configuration entry at /', async () => {
    const consoleErrors: unknown[][] = [];
    const spy = vi.spyOn(console, 'error').mockImplementation((...args: unknown[]) => {
      consoleErrors.push(args);
    });
    try {
      renderAt('/');

      const nav = screen.getByRole('navigation', { name: '主导航' });
      expect(nav).toBeInTheDocument();
      expect(screen.getByRole('link', { name: '供应商' })).toHaveAttribute('href', '/providers');
      expect(screen.getByRole('link', { name: '资源' })).toHaveAttribute('href', '/resources');
      expect(screen.getByRole('link', { name: '资源池' })).toHaveAttribute('href', '/pools');
      expect(screen.getByRole('link', { name: '操作' })).toHaveAttribute('href', '/operations');

      expect(await screen.findByText('登录后可查看当前命名空间的配置。')).toBeInTheDocument();

      expect(consoleErrors).toEqual([]);
    } finally {
      spy.mockRestore();
    }
  });

  it('shows sign-in states for data pages and labels execution as unavailable', async () => {
    renderAt('/providers');
    expect(await screen.findByText('登录后可查看当前命名空间的配置。')).toBeInTheDocument();
    cleanup();
    for (const entry of ['/resources', '/pools']) {
      renderAt(entry);
      expect(await screen.findByText('登录后可查看当前命名空间的配置。')).toBeInTheDocument();
      expect(document.body.textContent).not.toContain('界面骨架');
      expect(document.body.textContent).not.toMatch(IMPLEMENTATION_TERMS);
      cleanup();
    }
    renderAt('/operations');
    expect(await screen.findByText('登录后可查看当前命名空间的配置。')).toBeInTheDocument();
    expect(document.body.textContent).not.toMatch(IMPLEMENTATION_TERMS);
    cleanup();
  });

  it('shows configured resource evidence and stable pool member priority from the active revision', async () => {
    const document: ConfigDocument = {
      schemaVersion: 1,
      accounts: [{ accountId: 'acct-demo-1', providerTypeId: 'provider-alpha', displayName: '演示账户', enabled: true }],
      bindings: [{ bindingId: 'bind-1', accountId: 'acct-demo-1', domain: 'ai/text', endpoint: 'https://api.example.test', credentialRef: 'secret-ref', credentialRevision: 'secret-rev-1', enabled: true, capabilityProfiles: ['profile.chat.basic'], resourceRefs: ['resource-1'] }],
      resources: [{ resourceId: 'resource-1', ownerKind: 'account', ownerId: 'acct-demo-1', metricId: 'metric.tokens.total', unit: 'token', quantumExp: 0, kind: 'counter', commercialRole: 'limit', semanticsRef: 'sem.fixed-window-counter', semanticsRevision: 'semrev-1', window: { kind: 'fixed', period: 'day', timeZone: 'Asia/Singapore' }, metered: false, providerFactId: 'alpha-daily-token-limit', configuredCapacity: '1000000', capacityEvidenceRef: 'evidence-1' }],
      entitlementGroups: [],
      offers: [{ offerId: 'offer-1', bindingId: 'bind-1', domain: 'ai/text', capabilityProfile: 'profile.chat.basic', enabled: true, resourceRefs: ['resource-1'], requiredEvidenceRefs: ['evidence-1'] }],
      pools: [{ poolId: 'pool-1', namespace: 'demo', name: '演示资源池', enabled: true, members: [{ offerId: 'offer-1', priority: 10, enabled: true }] }],
      grants: [],
      evidence: [{ evidenceId: 'evidence-1', kind: 'bound', subjectRef: 'acct-demo-1', sourceRef: 'localtest-fixture:acct-demo-1:physical-1', verifiedAtMillis: 1_800_000_000_000, expiresAtMillis: 1_800_003_600_000, payload: { bound: '1000000', metricId: 'metric.tokens.total', unit: 'token' } }],
      unsupportedDrafts: [],
    };
    vi.mocked(fetch).mockImplementation(async (input) => {
      if (String(input) === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read'], csrfToken: 'csrf', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (String(input) === '/v1/config') return new Response(JSON.stringify({ revision: 'revision-123456789', version: 1, document }), { status: 200 });
      if (String(input) === '/v1/pools') return new Response(JSON.stringify([]), { status: 200 });
      throw new Error(`Unexpected API request: ${String(input)}`);
    });

    renderAt('/resources');
    expect(await screen.findByText('1000000')).toBeInTheDocument();
    expect(screen.getByText('测试数据')).toBeInTheDocument();
    expect(screen.getByText('演示账户')).toBeInTheDocument();
    expect(screen.getAllByText('未知')).toHaveLength(3);
    expect(within(screen.getByRole('table')).getAllByRole('cell').map((cell) => cell.getAttribute('data-label'))).toEqual([
      '资源 / 指标', '所属账户', '类型与单位', '配置上限', '资源池 / 窗口', '实时余量', '已预留', '运行中', '计量窗口', '容量来源',
    ]);
    cleanup();

    const poolView = {
      pool: { poolId: 'pool-1', namespace: 'demo', name: '演示资源池', enabled: true, version: 1, members: [{ offerId: 'offer-1', priority: 10, enabled: true, version: 1 }] },
      groups: [{ domain: 'ai/text', capabilityProfile: 'profile.chat.basic', members: [{
        member: { offerId: 'offer-1', priority: 10, enabled: true, version: 1 },
        offer: { offerId: 'offer-1', bindingId: 'bind-1', domain: 'ai/text', capabilityProfile: 'profile.chat.basic', enabled: true, resourceRefs: ['resource-1'], version: 1 },
        status: 'available', reasons: [], resources: [{ resourceId: 'resource-1', windowKey: 'daily', metricId: 'metric.tokens.total', unit: 'token', available: '850000', reserved: '10000', active: '5000', uncertain: '0', evidenceStatus: 'verified', holdIds: [], encumbranceIds: [], observationRefreshNeeded: false }],
      }] }],
    };
    vi.mocked(fetch).mockImplementation(async (input) => {
      if (String(input) === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read'], csrfToken: 'csrf', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (String(input) === '/v1/config') return new Response(JSON.stringify({ revision: 'revision-123456789', version: 1, document }), { status: 200 });
      if (String(input) === '/v1/pools') return new Response(JSON.stringify([poolView]), { status: 200 });
      throw new Error(`Unexpected API request: ${String(input)}`);
    });
    renderAt('/pools');
    expect(await screen.findByRole('heading', { name: '演示资源池' })).toBeInTheDocument();
    expect(screen.getByText('ai/text · profile.chat.basic')).toBeInTheDocument();
    expect(screen.getByText(/metric\.tokens\.total: 850000 token/)).toBeInTheDocument();
    expect(within(screen.getByRole('listitem')).getByText('可用')).toBeInTheDocument();
    cleanup();

    renderAt('/resources');
    expect(await screen.findByRole('columnheader', { name: '实时余量' })).toBeInTheDocument();
    expect(screen.getByText('850000')).toBeInTheDocument();
    expect(screen.getByText('10000')).toBeInTheDocument();
    expect(screen.getByText('5000')).toBeInTheDocument();
    expect(screen.getByText('观测证据有效')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '记录余额观测' })).not.toBeInTheDocument();
  });

  it('lets a manager save a balance observation against the displayed ledger watermarks', async () => {
    const document: ConfigDocument = {
      schemaVersion: 1,
      accounts: [{ accountId: 'acct-1', providerTypeId: 'provider-alpha', displayName: '供应商账户', enabled: true }],
      bindings: [],
      resources: [{ resourceId: 'resource-counter', ownerKind: 'account', ownerId: 'acct-1', metricId: 'tokens', unit: 'token', quantumExp: 0, kind: 'counter', commercialRole: 'limit', semanticsRef: 'sem.counter', semanticsRevision: 'r1', window: { kind: 'fixed', period: 'day', timeZone: 'Asia/Singapore' }, metered: false, providerFactId: 'token-limit', configuredCapacity: '1000', capacityEvidenceRef: 'bound-evidence' }],
      entitlementGroups: [], offers: [], pools: [], grants: [],
      evidence: [{ evidenceId: 'bound-evidence', kind: 'bound', subjectRef: 'acct-1', sourceRef: 'provider-contract', verifiedAtMillis: 1_800_000_000_000, expiresAtMillis: 1_800_003_600_000, payload: { bound: '1000', metricId: 'tokens', unit: 'token' } }],
      unsupportedDrafts: [],
    };
    const poolView = {
      pool: { poolId: 'pool-1', namespace: 'demo', name: '资源池', enabled: true, version: 1, members: [] },
      groups: [{ domain: 'ai/text', capabilityProfile: 'profile.chat.basic', members: [{
        member: { offerId: 'offer-1', priority: 1, enabled: true, version: 1 },
        offer: { offerId: 'offer-1', bindingId: 'binding-1', domain: 'ai/text', capabilityProfile: 'profile.chat.basic', enabled: true, resourceRefs: ['resource-counter'], version: 1 },
        status: 'available', reasons: [], resources: [{ resourceId: 'resource-counter', windowKey: 'day-1', metricId: 'tokens', unit: 'token', committed: '35', adjustmentVersion: 2, available: '800', reserved: '5', active: '0', uncertain: '0', evidenceStatus: 'verified', holdIds: [], encumbranceIds: [], observationRefreshNeeded: true }],
      }] }],
    };
    const fetchMock = vi.mocked(fetch);
    let postCount = 0;
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'manager', namespace: 'demo', permissions: ['read', 'manage'], csrfToken: 'csrf-manager', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/config') return new Response(JSON.stringify({ revision: 'revision-1', version: 1, document }), { status: 200 });
      if (path === '/v1/pools') return new Response(JSON.stringify([poolView]), { status: 200 });
      if (path === '/v1/resources/resource-counter/observations' && init?.method === 'POST') {
        postCount += 1;
        expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-manager');
        const body = JSON.parse(String(init.body)) as { commandId: string; evidence: Record<string, unknown> };
        expect(body.commandId).toMatch(/^[0-9a-f-]{36}$/i);
        expect(body.evidence).toMatchObject({ sourceRef: 'invoice-2026-10', remaining: '900', committedAtStart: '35', adjustmentVersionAtStart: 2 });
        expect(body.evidence['observedAtMillis']).toEqual(expect.any(Number));
        expect(body.evidence['expiresAtMillis']).toEqual(expect.any(Number));
        return new Response(JSON.stringify({ status: 'stored', reasons: [] }), { status: 200 });
      }
      throw new Error(`Unexpected API request: ${path}`);
    });

    renderAt('/resources');
    fireEvent.click(await screen.findByText('记录余额观测'));
    await screen.findByLabelText('来源编号');
    fireEvent.change(screen.getByLabelText('来源编号'), { target: { value: 'invoice-2026-10' } });
    fireEvent.change(screen.getByLabelText(/供应商显示的剩余额度/), { target: { value: '900' } });
    const observedAt = new Date(Date.now() - 60_000);
    const expiryAt = new Date(Date.now() + 300_000);
    const localValue = (date: Date) => new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
    fireEvent.change(screen.getByLabelText('供应商数据读取时间'), { target: { value: localValue(observedAt) } });
    fireEvent.change(screen.getByLabelText('此观测的有效截止时间'), { target: { value: localValue(expiryAt) } });
    fireEvent.click(screen.getByRole('button', { name: '保存余额观测' }));

    expect(await screen.findByRole('status')).toHaveTextContent('观测已保存');
    expect(postCount).toBe(1);
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/providers/'))).toBe(false);
  });

  it('reorders the full live pool through the versioned order API', async () => {
    const makeMember = (offerId: string, priority: number) => ({
      member: { offerId, priority, enabled: true, version: 1 },
      offer: { offerId, bindingId: 'binding-1', domain: 'ai/text', capabilityProfile: 'profile.chat.basic', enabled: true, resourceRefs: ['resource-1'], version: 1 },
      status: 'available' as const, reasons: [], resources: [],
    });
    let poolView = {
      pool: { poolId: 'pool', namespace: 'demo', name: 'Pool', enabled: true, version: 4, members: [
        { offerId: 'offer-a', priority: 10, enabled: true, version: 1 },
        { offerId: 'offer-b', priority: 20, enabled: true, version: 1 },
      ] },
      groups: [{ domain: 'ai/text', capabilityProfile: 'profile.chat.basic', members: [makeMember('offer-a', 10), makeMember('offer-b', 20)] }],
    };
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read', 'configure'], csrfToken: 'csrf', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/config') return new Response(JSON.stringify({ revision: 'revision-order', version: 1, document: {} }), { status: 200 });
      if (path === '/v1/pools') return new Response(JSON.stringify([poolView]), { status: 200 });
      if (path === '/v1/pools/pool/order' && init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as { expectedPoolVersion: number; expectedConfigRevision: string; entries: Array<{ offerId: string; priority: number }> };
        expect(new Headers(init.headers).get('If-Match')).toBe('"4"');
        expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf');
        expect(body).toMatchObject({ expectedPoolVersion: 4, expectedConfigRevision: 'revision-order', entries: [
          { offerId: 'offer-b', priority: 10 }, { offerId: 'offer-a', priority: 20 },
        ] });
        poolView = { ...poolView, pool: { ...poolView.pool, version: 5, members: poolView.pool.members.map((member) => ({ ...member, priority: member.offerId === 'offer-b' ? 10 : 20 })) }, groups: [{ ...poolView.groups[0]!, members: [makeMember('offer-b', 10), makeMember('offer-a', 20)] }] };
        return new Response(JSON.stringify(poolView), { status: 200 });
      }
      throw new Error(`Unexpected API request: ${path}`);
    });

    renderAt('/pools');
    fireEvent.click(await screen.findByRole('button', { name: '调整优先级' }));
    fireEvent.click(screen.getByRole('button', { name: '将 offer-b 上移' }));
    fireEvent.click(screen.getByRole('button', { name: '保存优先顺序' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/v1/pools/pool/order', expect.objectContaining({ method: 'PUT' })));
    expect(await screen.findByText('20')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '保存优先顺序' })).not.toBeInTheDocument();
  });

  it('reads persisted operation summaries and details without submitting or replaying work', async () => {
    const operation = {
      operationId: 'op-42', poolId: 'pool-1', state: 'failed', cancelRequested: false,
      attempts: [{ attemptId: 'attempt-1', offerId: 'offer-1', bindingId: 'binding-1', accountId: 'acct-1', phase: 'terminal', outcome: 'known_failure', providerRequestId: 'provider-request-1' }],
      resources: [{ resourceId: 'resource-1', windowKey: 'daily', metricId: 'metric.tokens.total', unit: 'token', consumed: '10', consumptionBasis: 'provider_reported', occupancy: 'released', evidenceRefs: ['usage-response-1'] }],
      result: { operationId: 'op-42', state: 'failed', code: 'provider_rejected', reasons: null, retrySafety: 'unsafe', attempts: ['attempt-1'], resources: [{ resourceId: 'resource-1', windowKey: 'daily', metricId: 'metric.tokens.total', unit: 'token', consumed: '10', consumptionBasis: 'provider_reported', occupancy: 'released', evidenceRefs: null }], decisionRefs: [], outputAvailability: 'none', suggestedAction: 'check_provider_account' },
      reconciliations: null, updatedAt: 1_800_000_000_000,
    };
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read'], csrfToken: 'csrf', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/operations?limit=50&cursor=page-2') return new Response(JSON.stringify({ operations: [operation], nextCursor: 'page-3' }), { status: 200 });
      if (path === '/v1/operations/op-42') return new Response(JSON.stringify(operation), { status: 200 });
      if (path === '/v1/operations/op-42/decisions') return new Response(JSON.stringify([{ decisionId: 'decision-1', attemptId: 'attempt-1', poolId: 'pool-1', poolVersion: 7, offerId: 'offer-new', memberPriority: 10, configRevision: 'config-old', stage: 'prepare', candidates: [{ offerId: 'offer-old', priority: 5, reasons: [] }, { offerId: 'offer-new', priority: 10, reasons: [] }], traceTruncated: false, hookRegistryRevision: 'sha256:sealed-registry', plannerCandidateOrder: ['offer-old', 'offer-new'], hookCandidateOrder: ['offer-new', 'offer-old'], hookRegistryManifest: { apiVersion: 'v1' } }]), { status: 200 });
      if (path.startsWith('/v1/operations/op-42/output?')) return new Response(JSON.stringify({ chunks: [], complete: true, availability: 'none' }), { status: 200 });
      throw new Error(`Unexpected API request: ${path}`);
    });
    const router = renderAt('/operations?cursor=page-2');
    fireEvent.click(await screen.findByRole('link', { name: 'op-42' }));
    expect(router.state.location.search).toBe('?operation=op-42&returnCursor=page-2');
    expect(await screen.findByRole('heading', { name: '操作详情' })).toBeInTheDocument();
    expect(await screen.findByText('Hook 调整后的顺序')).toBeInTheDocument();
    expect(screen.getByText('sha256:sealed-registry')).toBeInTheDocument();
    expect(screen.getByText('config-old')).toBeInTheDocument();
    expect(screen.getByText('操作结果已保存。请先查看下方原因和用量，再决定是否继续。')).toBeInTheDocument();
    expect(screen.getByText('检查服务方账户状态和用量；确认请求是否已处理后再决定下一步。')).toBeInTheDocument();
    expect(screen.getByText('核算依据')).toBeInTheDocument();
    expect(screen.getByText('供应商实报')).toBeInTheDocument();
    expect(screen.getByText('证据 0 项')).toBeInTheDocument();
    expect(document.body.textContent).not.toContain('provider_rejected');
    expect(document.body.textContent).not.toContain('provider · rejected');
    expect(document.body.textContent).not.toContain('check_provider_account');
    expect(screen.getByText('供应商请求编号')).toBeInTheDocument();
    expect(screen.getByText('provider-request-1')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('link', { name: '返回操作列表' }));
    expect(await screen.findByRole('heading', { name: '操作' })).toBeInTheDocument();
    expect(router.state.location.search).toBe('?cursor=page-2');
    await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input) === '/v1/operations?limit=50&cursor=page-2')).toBe(true));
    expect(fetchMock).toHaveBeenCalledWith('/v1/operations/op-42', expect.objectContaining({ method: 'GET' }));
    expect(fetchMock.mock.calls.some(([input, init]) => String(input).startsWith('/v1/pools/') || init?.method === 'POST')).toBe(false);
  });

  it('surfaces saved result and unconfirmed resource occupancy in the operation list', async () => {
    const operation = {
      operationId: 'op-review', poolId: 'pool-1', requestDomain: 'ai/text', state: 'unknown', cancelRequested: false,
      attempts: [],
      resources: [{ resourceId: 'resource-1', windowKey: 'daily', metricId: 'metric.tokens.total', unit: 'token', consumed: null, consumptionBasis: 'unrepresentable', occupancy: 'uncertain', evidenceRefs: ['usage-1'] }],
      result: { operationId: 'op-review', state: 'unknown', code: 'UnknownOutcome', reasons: [], retrySafety: 'undetermined', attempts: [], resources: [], decisionRefs: [], outputAvailability: 'none', suggestedAction: 'check_provider_account' },
      reconciliations: [], updatedAt: 1_800_000_000_000,
    };
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read'], csrfToken: 'csrf', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/operations?limit=50') return new Response(JSON.stringify({ operations: [operation] }), { status: 200 });
      throw new Error(`Unexpected API request: ${path}`);
    });

    renderAt('/operations');

    expect(await screen.findByText('请求可能已到达服务方，但最终结果尚未确认。先核对服务方记录，再决定是否重试。')).toBeInTheDocument();
    expect(screen.getByText('文本 AI')).toBeInTheDocument();
    expect(screen.getByText('1 项资源待核实')).toBeInTheDocument();
    expect(screen.getByRole('table').querySelector('.config-status-unknown')).toHaveTextContent('结果待核实');
    expect(within(screen.getByRole('table')).getAllByRole('cell').map((cell) => cell.getAttribute('data-label'))).toEqual([
      '操作编号', '请求类型', '资源池', '状态', '结果摘要', '待核实占用', '尝试次数', '最后更新',
    ]);
  });

  it('lets managers reconcile a single unknown attempt with evidence and exact resource corrections', async () => {
    const operation = {
      operationId: 'op-review', poolId: 'pool-1', state: 'unknown', cancelRequested: false,
      attempts: [{ attemptId: 'attempt-1', offerId: 'offer-1', bindingId: 'binding-1', accountId: 'acct-1', phase: 'terminal', outcome: 'unknown', providerRequestId: 'request-1' }],
      resources: [],
      result: { operationId: 'op-review', state: 'unknown', code: 'UnknownOutcome', reasons: [], retrySafety: 'undetermined', attempts: ['attempt-1'], resources: [
        { resourceId: 'counter-1', windowKey: 'sr:lifetime', metricId: 'tokens', unit: 'token', consumed: null, consumptionBasis: 'unrepresentable', occupancy: 'not_applicable', evidenceRefs: [] },
        { resourceId: 'slot-1', windowKey: 'sr:lifetime', metricId: 'concurrency', unit: 'slot', consumed: null, consumptionBasis: 'not_applicable', occupancy: 'uncertain', evidenceRefs: [] },
      ], decisionRefs: [], outputAvailability: 'none', suggestedAction: 'review_usage' },
      reconciliations: [], updatedAt: 1_800_000_000_000,
    };
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'operator', namespace: 'demo', permissions: ['read', 'manage'], csrfToken: 'csrf', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/operations/op-review') return new Response(JSON.stringify(operation), { status: 200 });
      if (path === '/v1/operations/op-review/decisions') return new Response('[]', { status: 200 });
      if (path.startsWith('/v1/operations/op-review/output?')) return new Response(JSON.stringify({ chunks: [], complete: true, availability: 'none' }), { status: 200 });
      if (path === '/v1/attempts/attempt-1/reconciliations' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body));
        expect(body).toMatchObject({ commandId: expect.any(String), confirmedOutcome: 'known_success', retrySafety: 'unsafe', evidenceRefs: ['provider-ticket-7'] });
        expect(body.corrections).toEqual(expect.arrayContaining([
          expect.objectContaining({ resourceId: 'counter-1', consumed: '42', consumptionBasis: 'adjustment' }),
          expect.objectContaining({ resourceId: 'slot-1', consumed: null, occupancy: 'released' }),
        ]));
        return new Response(JSON.stringify({ reconciliationId: 'reconcile-1', confirmedOutcome: 'known_success', retrySafety: 'unsafe', evidenceRefs: ['provider-ticket-7'], corrections: [], createdAt: 1_800_000_000_100 }), { status: 201, headers: { 'Content-Type': 'application/json' } });
      }
      throw new Error(`Unexpected API request: ${path}`);
    });

    renderAt('/operations?operation=op-review');
    expect(await screen.findByRole('heading', { name: '核查执行结果' })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('核实结果'), { target: { value: 'known_success' } });
    fireEvent.change(screen.getByLabelText('重试安全性'), { target: { value: 'unsafe' } });
    fireEvent.change(screen.getByLabelText('tokens 实际用量（token）'), { target: { value: '42' } });
    fireEvent.change(screen.getByLabelText('证据编号'), { target: { value: 'provider-ticket-7' } });
    fireEvent.click(screen.getByRole('button', { name: '保存核查' }));
    expect(await screen.findByText('核查已追加到账本；原始结果记录保持不变。')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith('/v1/attempts/attempt-1/reconciliations', expect.objectContaining({ method: 'POST' }));
  });

  it('lets managers adjust only the server-listed reversible charge and clear eligible holds with evidence', async () => {
    const operation = {
      operationId: 'op-admin', poolId: 'pool-1', state: 'succeeded', cancelRequested: false,
      attempts: [], resources: [], reconciliations: [], updatedAt: 1_800_000_000_000,
      adjustableCharges: [{ eventId: 'event-charge', resourceId: 'resource-1', windowKey: 'daily', metricId: 'tokens', unit: 'token', remainingReversible: '5' }],
      managedHolds: [{ holdId: 'hold-1', resourceId: 'resource-1', scopeKind: 'resource', scopeId: 'resource-1', reason: 'manual_review', evidenceRefs: ['old-ticket'], canClear: true }],
    };
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'manager', namespace: 'demo', permissions: ['read', 'manage'], csrfToken: 'csrf-manager', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/operations/op-admin') return new Response(JSON.stringify(operation), { status: 200 });
      if (path === '/v1/operations/op-admin/decisions') return new Response('[]', { status: 200 });
      if (path.startsWith('/v1/operations/op-admin/output?')) return new Response(JSON.stringify({ chunks: [], complete: true, availability: 'none' }), { status: 200 });
      if (path === '/v1/resources/resource-1/adjustments' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body));
        expect(body).toMatchObject({ commandId: expect.any(String), windowKey: 'daily', delta: '-3', originalEventId: 'event-charge', evidenceRefs: ['ticket-adjust'] });
        expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-manager');
        return new Response(JSON.stringify({ commandId: body.commandId, status: 'applied' }), { status: 201, headers: { 'Content-Type': 'application/json' } });
      }
      if (path === '/v1/holds/hold-1/clear' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body));
        expect(body).toMatchObject({ commandId: expect.any(String), evidenceIds: ['ticket-clear'] });
        expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-manager');
        return new Response(JSON.stringify({ commandId: body.commandId, status: 'applied' }), { status: 201, headers: { 'Content-Type': 'application/json' } });
      }
      throw new Error(`Unexpected API request: ${path}`);
    });

    renderAt('/operations?operation=op-admin');
    expect(await screen.findByRole('heading', { name: '资源调账与 hold 处置' })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('原始扣费记录'), { target: { value: 'event-charge' } });
    fireEvent.change(screen.getByLabelText('调账数量（负数会减少用量）'), { target: { value: '-3' } });
    fireEvent.change(screen.getByLabelText('证据编号'), { target: { value: 'ticket-adjust' } });
    fireEvent.click(screen.getByRole('button', { name: '记录调账' }));
    expect(await screen.findByText('已保存。')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('清除依据'), { target: { value: 'ticket-clear' } });
    fireEvent.click(screen.getByRole('button', { name: '清除 hold' }));
    expect(await screen.findByText('已保存。')).toBeInTheDocument();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/v1/holds/hold-1/clear', expect.objectContaining({ method: 'POST' })));
    expect(fetchMock).toHaveBeenCalledWith('/v1/resources/resource-1/adjustments', expect.objectContaining({ method: 'POST' }));
    expect(fetchMock).toHaveBeenCalledWith('/v1/holds/hold-1/clear', expect.objectContaining({ method: 'POST' }));
  });

  it('filters operation history on the server and preserves the filter through pagination and detail return', async () => {
    const operation = {
      operationId: 'op-review', poolId: 'pool-1', requestDomain: 'ai/text', state: 'unknown', cancelRequested: false,
      attempts: [], resources: [],
      result: { operationId: 'op-review', state: 'unknown', code: 'UnknownOutcome', reasons: [], retrySafety: 'undetermined', attempts: [], resources: [], decisionRefs: [], outputAvailability: 'none', suggestedAction: 'check_provider_account' },
      reconciliations: [], updatedAt: 1_800_000_000_000,
    };
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read'], csrfToken: 'csrf', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/operations?limit=50') return new Response(JSON.stringify({ operations: [] }), { status: 200 });
      if (path === '/v1/operations?limit=50&state=unknown') return new Response(JSON.stringify({ operations: [operation], nextCursor: 'cursor-2' }), { status: 200 });
      if (path === '/v1/operations?limit=50&cursor=cursor-2&state=unknown') return new Response(JSON.stringify({ operations: [operation] }), { status: 200 });
      if (path === '/v1/operations?limit=50&state=failed') return new Response(JSON.stringify({ operations: [] }), { status: 200 });
      if (path === '/v1/operations/op-review') return new Response(JSON.stringify(operation), { status: 200 });
      if (path === '/v1/operations/op-review/decisions') return new Response('[]', { status: 200 });
      if (path.startsWith('/v1/operations/op-review/output?')) return new Response(JSON.stringify({ chunks: [], complete: true, availability: 'none' }), { status: 200 });
      throw new Error(`Unexpected API request: ${path}`);
    });
    const router = renderAt('/operations');

    fireEvent.change(await screen.findByLabelText('按状态筛选'), { target: { value: 'unknown' } });
    expect(await screen.findByRole('link', { name: 'op-review' })).toBeInTheDocument();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/v1/operations?limit=50&state=unknown', expect.objectContaining({ method: 'GET' })));
    fireEvent.click(screen.getByRole('button', { name: '读取下一页' }));
    expect(await screen.findByRole('link', { name: 'op-review' })).toBeInTheDocument();
    expect(router.state.location.search).toBe('?cursor=cursor-2&state=unknown');
    fireEvent.click(screen.getByRole('link', { name: 'op-review' }));
    expect(await screen.findByRole('heading', { name: '操作详情' })).toBeInTheDocument();
    expect(router.state.location.search).toBe('?operation=op-review&returnCursor=cursor-2&returnState=unknown');
    fireEvent.click(screen.getByRole('link', { name: '返回操作列表' }));
    expect(await screen.findByRole('heading', { name: '操作' })).toBeInTheDocument();
    expect(router.state.location.search).toBe('?cursor=cursor-2&state=unknown');
    expect(fetchMock).toHaveBeenCalledWith('/v1/operations?limit=50&cursor=cursor-2&state=unknown', expect.objectContaining({ method: 'GET' }));
    fireEvent.change(screen.getByLabelText('按状态筛选'), { target: { value: 'failed' } });
    expect(await screen.findByText('此状态下没有操作')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '清除筛选' }));
    expect(await screen.findByText('当前命名空间还没有操作记录')).toBeInTheDocument();
    expect(router.state.location.search).toBe('');
  });

  it('renders an empty operation list when the server serializes an absent list as null', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read', 'execute'], csrfToken: 'csrf', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/operations?limit=50') return new Response(JSON.stringify({ operations: null }), { status: 200 });
      if (path === '/v1/pools') return new Response(JSON.stringify([]), { status: 200 });
      throw new Error(`Unexpected API request: ${path}`);
    });

    renderAt('/operations');
    expect(await screen.findByRole('heading', { name: '当前命名空间还没有操作记录' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '提交文本请求' })).toBeInTheDocument();
  });

  it('submits the bounded text request once with CSRF and the explicit metered choice', async () => {
    const poolView = {
      pool: { poolId: 'pool-local', namespace: 'demo', name: 'Local test pool', enabled: true, version: 1, members: [{ offerId: 'offer-local', priority: 10, enabled: true, version: 1 }] },
      groups: [{ domain: 'ai/text', capabilityProfile: 'profile.chat.basic', members: [{
        member: { offerId: 'offer-local', priority: 10, enabled: true, version: 1 },
        offer: { offerId: 'offer-local', bindingId: 'binding-local', domain: 'ai/text', capabilityProfile: 'profile.chat.basic', enabled: true, resourceRefs: ['resource-local'], version: 1 },
        status: 'available', reasons: [], resources: [],
      }] }],
    };
    const operation = {
      operationId: 'op-created', poolId: 'pool-local', state: 'running', cancelRequested: false,
      attempts: [], resources: [], reconciliations: [], updatedAt: 1_800_000_000_000,
    };
    const fetchMock = vi.mocked(fetch);
    let firstSubmitted: unknown;
    let submitAttempts = 0;
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read', 'execute'], csrfToken: 'csrf-submit', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/pools') return new Response(JSON.stringify([poolView]), { status: 200 });
      if (path === '/v1/operations?limit=50') return new Response(JSON.stringify({ operations: [] }), { status: 200 });
      if (path === '/v1/pools/pool-local/operations' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body)) as { operationId: string; poolId: string; request: { domain: string; capabilityProfile: string; payload: { model: string; messages: Array<{ role: string; content: string }>; max_tokens: number } }; deadlineAt: number; maxAttempts: number; allowMetered: boolean };
        expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-submit');
        expect(body).toMatchObject({ poolId: 'pool-local', request: { domain: 'ai/text', capabilityProfile: 'profile.chat.basic', payload: { model: 'model-x', messages: [{ role: 'user', content: 'hello ant' }], max_tokens: 64 } }, maxAttempts: 1, allowMetered: true });
        expect(body.operationId).toMatch(/^[0-9a-f-]{36}$/i);
        expect(body.deadlineAt).toBeGreaterThan(Date.now());
        submitAttempts += 1;
        if (submitAttempts === 1) {
          firstSubmitted = body;
          throw new TypeError('simulated response loss');
        }
        expect(body).toEqual(firstSubmitted);
        return new Response(JSON.stringify({ kind: 'accepted', state: 'running', operationId: 'op-created', operationUrl: '/v1/operations/op-created', eventsUrl: '/v1/operations/op-created/events' }), { status: 202 });
      }
      if (path === '/v1/operations/op-created') return new Response(JSON.stringify(operation), { status: 200 });
      if (path === '/v1/operations/op-created/decisions') return new Response('[]', { status: 200 });
      if (path.startsWith('/v1/operations/op-created/output?')) return new Response(JSON.stringify({ chunks: [], complete: false, availability: 'none' }), { status: 200 });
      throw new Error(`Unexpected API request: ${path}`);
    });

    renderAt('/operations');
    fireEvent.change(await screen.findByLabelText('用户消息'), { target: { value: 'hello ant' } });
    fireEvent.change(screen.getByLabelText('最大输出 token 数'), { target: { value: '64' } });
    fireEvent.click(screen.getByLabelText('允许使用计量型资源'));
    fireEvent.click(screen.getByRole('button', { name: '提交请求' }));
    expect(await screen.findByText(/请求结果未确认/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '提交请求' }));
    expect(await screen.findByRole('heading', { name: '操作详情' })).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([input, init]) => String(input) === '/v1/pools/pool-local/operations' && init?.method === 'POST')).toHaveLength(2);
  });

  it('records an authorized cancel intent with CSRF and keeps the operation nonterminal', async () => {
    const operation = {
      operationId: 'op-live', poolId: 'pool-1', state: 'running', cancelRequested: false,
      attempts: [], resources: [], reconciliations: [], updatedAt: 1_800_000_000_000,
    };
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read', 'cancel'], csrfToken: 'csrf-cancel', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/operations/op-live/cancel' && init?.method === 'POST') {
        operation.cancelRequested = true;
        return new Response(JSON.stringify({ status: 'recorded' }), { status: 200 });
      }
      if (path === '/v1/operations/op-live') return new Response(JSON.stringify(operation), { status: 200 });
      if (path === '/v1/operations/op-live/decisions') return new Response('[]', { status: 200 });
      if (path.startsWith('/v1/operations/op-live/output?')) return new Response(JSON.stringify({ chunks: [], complete: false, availability: 'none' }), { status: 200 });
      throw new Error(`Unexpected API request: ${path}`);
    });
    renderAt('/operations?operation=op-live');
    fireEvent.click(await screen.findByRole('button', { name: '请求取消' }));
    expect(await screen.findByText('服务器已记录取消意图，正在等待操作结果确认。')).toBeInTheDocument();
    const cancelCall = fetchMock.mock.calls.find(([input, init]) => String(input) === '/v1/operations/op-live/cancel' && init?.method === 'POST');
    expect(cancelCall).toBeDefined();
    expect(new Headers(cancelCall?.[1]?.headers).get('X-CSRF-Token')).toBe('csrf-cancel');
    expect(JSON.parse(String(cancelCall?.[1]?.body))).toMatchObject({ commandId: expect.any(String) });
    expect(screen.getByText('运行中')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '请求取消' })).toBeDisabled();
  });

  it('subscribes to persisted operation events and refreshes the saved projection', async () => {
    const operation = {
      operationId: 'op-stream', poolId: 'pool-1', state: 'running', cancelRequested: false,
      attempts: [], resources: [], reconciliations: [], updatedAt: 1_800_000_000_000,
    };
    const listeners = new Map<string, EventListener>();
    const stream = {
      onopen: null as ((event: Event) => void) | null,
      onerror: null as ((event: Event) => void) | null,
      addEventListener: vi.fn((type: string, listener: EventListener) => listeners.set(type, listener)),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
    const eventSource = vi.fn();
    class FakeEventSource {
      constructor(url: string, options?: EventSourceInit) {
        eventSource(url, options);
        return stream as unknown as FakeEventSource;
      }
    }
    vi.stubGlobal('EventSource', FakeEventSource);
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read'], csrfToken: 'csrf', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/operations/op-stream') return new Response(JSON.stringify(operation), { status: 200 });
      if (path === '/v1/operations/op-stream/decisions') return new Response('[]', { status: 200 });
      if (path.startsWith('/v1/operations/op-stream/output?')) return new Response(JSON.stringify({ chunks: [{ sequence: 1, data: 'aGVsbG8=' }], complete: false, availability: 'available' }), { status: 200 });
      throw new Error(`Unexpected API request: ${path}`);
    });
    renderAt('/operations?operation=op-stream');
    await waitFor(() => expect(eventSource).toHaveBeenCalledWith('/v1/operations/op-stream/events', { withCredentials: true }));
    expect(await screen.findByText('hello')).toBeInTheDocument();
    stream.onopen?.(new Event('open'));
    expect(await screen.findByText(/实时状态已连接/)).toBeInTheDocument();
    listeners.get('attempt_prepared')?.(new Event('attempt_prepared'));
    await waitFor(() => expect(fetchMock.mock.calls.filter(([input]) => String(input) === '/v1/operations/op-stream').length).toBeGreaterThanOrEqual(2));
    operation.state = 'succeeded';
    listeners.get('operation_terminal')?.(new Event('operation_terminal'));
    expect(await screen.findByText(/操作已结束/)).toBeInTheDocument();
    expect(stream.close).toHaveBeenCalled();
  });

  it('exchanges the one-time token for a browser session and advances', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input) => {
      if (String(input) === '/v1/session') {
        return new Response(JSON.stringify({ error: { code: 'Unauthenticated', messageKey: 'ant.error.unauthenticated', retryable: false, action: 'login' }, requestId: 'test' }), { status: 401 });
      }
      return new Response(JSON.stringify({
        principalId: 'admin', namespace: 'demo', permissions: ['read'], csrfToken: 'csrf-token', expiresAt: 1_800_000_000_000,
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    });
    renderAt('/login');
    const tokenInput = await screen.findByLabelText('管理员令牌');
    fireEvent.change(tokenInput, { target: { value: 'a'.repeat(32) } });
    fireEvent.click(screen.getByRole('button', { name: '登录' }));
    await waitFor(() => expect(screen.getByRole('heading', { name: '供应商' })).toBeInTheDocument());
    expect(fetchMock).toHaveBeenCalledWith('/v1/sessions', expect.objectContaining({
      method: 'POST', credentials: 'include', body: JSON.stringify({ token: 'a'.repeat(32) }),
    }));
    expect(screen.getByText('demo')).toBeInTheDocument();
  });

  it('creates, saves, validates, and explicitly activates a server draft', async () => {
    const blankDocument = {
      schemaVersion: 1,
      accounts: [], bindings: [], resources: [], entitlementGroups: [], offers: [], pools: [], grants: [], evidence: [], unsupportedDrafts: [],
    };
    const draft = (version: number, status: string, fieldIssues: unknown[] = []) => ({
      draftId: 'draft-1', baseRevision: '', version, status, definition: blankDocument, fieldIssues,
    });
    let active = false;
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/v1/session') {
        return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read', 'configure'], csrfToken: 'csrf-value', expiresAt: 1_800_000_000_000 }), { status: 200 });
      }
      if (path === '/v1/config') {
        return active
          ? new Response(JSON.stringify({ revision: 'revision-1', version: 2, document: blankDocument }), { status: 200 })
          : new Response(JSON.stringify({ error: { code: 'NotFound', messageKey: 'ant.error.notFound', retryable: false, action: 'login' }, requestId: 'test' }), { status: 404 });
      }
      if (path === '/v1/catalog/providers') {
        return new Response(JSON.stringify([{
          typeId: 'provider-alpha', adapterRevision: 'adapter-alpha-v1', supportedDomains: ['ai/text'],
          configSchema: { type: 'object', properties: { region: { type: 'string', enum: ['test'] } } },
          resourceSchema: {}, capabilitySchema: {}, extensionSchema: {}, entitlementFacts: [], semantics: [], profiles: [],
        }]), { status: 200 });
      }
      if (path === '/v1/catalog/scopes') return new Response(JSON.stringify({ fixture: true, scopes: [] }), { status: 200 });
      if (path === '/v1/config/drafts' && init?.method === 'POST') {
        return new Response(JSON.stringify(draft(1, 'editing')), { status: 201 });
      }
      if (path === '/v1/config/drafts/draft-1' && init?.method === 'PATCH') {
        return new Response(JSON.stringify(draft(2, 'editing')), { status: 200 });
      }
      if (path === '/v1/config/drafts/draft-1/validate') {
        return new Response(JSON.stringify(draft(3, 'validated')), { status: 200 });
      }
      if (path === '/v1/config/drafts/draft-1/activate') {
        active = true;
        return new Response(JSON.stringify({ configRevision: 'revision-1', controlVersion: 1 }), { status: 200 });
      }
      if (path === '/v1/config/drafts/draft-1') {
        return new Response(JSON.stringify(draft(4, 'activated')), { status: 200 });
      }
      throw new Error(`Unexpected API request: ${path}`);
    });

    renderAt('/providers');
    fireEvent.click(await screen.findByRole('button', { name: '开始配置' }));
    const editor = await screen.findByLabelText('配置文档');
    await waitFor(() => expect(editor).toHaveValue(JSON.stringify(blankDocument, null, 2)));
    fireEvent.click(screen.getByRole('button', { name: '保存并校验' }));
    expect(await screen.findByText('服务端校验通过。激活前仍会再次检查最新版本和引用。')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '激活配置' }));
    expect(screen.getByRole('alertdialog')).toHaveTextContent('确认激活此配置？');
    fireEvent.click(screen.getByRole('button', { name: '确认激活' }));
    expect(await screen.findByText('版本 2 · 修订 revision-1…')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith('/v1/config/drafts', expect.objectContaining({
      method: 'POST', headers: expect.any(Headers),
    }));
    const mutations = fetchMock.mock.calls.filter(([, init]) => init?.method !== undefined && init.method !== 'GET');
    expect(mutations.length).toBe(4);
    for (const [, init] of mutations) {
      expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('csrf-value');
    }
  });

  it('asks before closing unsaved edits and restores the last saved draft after discard', async () => {
    const blankDocument: ConfigDocument = {
      schemaVersion: 1,
      accounts: [], bindings: [], resources: [], entitlementGroups: [], offers: [], pools: [], grants: [], evidence: [], unsupportedDrafts: [],
    };
    let savedDocument = blankDocument;
    let version = 1;
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read', 'configure'], csrfToken: 'csrf-value', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/config') return new Response(JSON.stringify({ error: { code: 'NotFound', messageKey: 'ant.error.notFound', retryable: false, action: 'login' }, requestId: 'test' }), { status: 404 });
      if (path === '/v1/catalog/providers') return new Response('[]', { status: 200 });
      if (path === '/v1/catalog/scopes') return new Response(JSON.stringify({ fixture: false, scopes: [] }), { status: 200 });
      if (path === '/v1/config/drafts' && init?.method === 'POST') return new Response(JSON.stringify({ draftId: 'draft-close', baseRevision: '', version, status: 'editing', definition: savedDocument, fieldIssues: [] }), { status: 201 });
      if (path === '/v1/config/drafts/draft-close' && init?.method === 'PATCH') {
        savedDocument = JSON.parse(String(init.body)).definition as ConfigDocument;
        version++;
        return new Response(JSON.stringify({ draftId: 'draft-close', baseRevision: '', version, status: 'editing', definition: savedDocument, fieldIssues: [] }), { status: 200 });
      }
      if (path === '/v1/config/drafts/draft-close') return new Response(JSON.stringify({ draftId: 'draft-close', baseRevision: '', version, status: 'editing', definition: savedDocument, fieldIssues: [] }), { status: 200 });
      throw new Error(`Unexpected API request: ${path}`);
    });

    renderAt('/providers');
    fireEvent.click(await screen.findByRole('button', { name: '开始配置' }));
    const editor = await screen.findByLabelText('配置文档');
    const firstEdit: ConfigDocument = { ...blankDocument, accounts: [{ accountId: 'acct-1', providerTypeId: 'provider-alpha', displayName: '已保存账户', enabled: true }] };
    fireEvent.change(editor, { target: { value: JSON.stringify(firstEdit, null, 2) } });
    fireEvent.click(screen.getByRole('button', { name: '关闭草稿' }));
    expect(screen.getByRole('alertdialog')).toHaveTextContent('还有未保存的修改');
    fireEvent.click(screen.getByRole('button', { name: '继续编辑' }));
    expect(screen.getByLabelText('配置文档')).toHaveValue(JSON.stringify(firstEdit, null, 2));

    fireEvent.click(screen.getByRole('button', { name: '关闭草稿' }));
    fireEvent.click(screen.getByRole('button', { name: '保存并关闭' }));
    expect(await screen.findByText('草稿已保存在服务器')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '继续编辑草稿' }));
    const reopened = await screen.findByLabelText('配置文档');
    await waitFor(() => expect(reopened).toHaveValue(JSON.stringify(firstEdit, null, 2)));

    const secondEdit = JSON.stringify({ ...firstEdit, accounts: [{ ...firstEdit.accounts[0]!, displayName: '未保存账户' }] }, null, 2);
    fireEvent.change(reopened, { target: { value: secondEdit } });
    fireEvent.click(screen.getByRole('button', { name: '关闭草稿' }));
    fireEvent.click(screen.getByRole('button', { name: '放弃修改' }));
    expect(await screen.findByText('草稿已保存在服务器')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '继续编辑草稿' }));
    expect(await screen.findByLabelText('配置文档')).toHaveValue(JSON.stringify(firstEdit, null, 2));
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'PATCH')).toHaveLength(1);
  });

  it('creates a registered account and saves a credential-backed binding without putting the credential in the draft', async () => {
    const blankDocument: ConfigDocument = {
      schemaVersion: 1,
      accounts: [], bindings: [], resources: [], entitlementGroups: [], offers: [], pools: [], grants: [], evidence: [], unsupportedDrafts: [],
    };
    const draft = (version: number, definition: ConfigDocument) => ({
      draftId: 'draft-guided', baseRevision: '', version, status: 'editing', definition, fieldIssues: [],
    });
    let storedDefinition = blankDocument;
    let draftVersion = 1;
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/v1/session') {
        return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read', 'configure'], csrfToken: 'csrf-value', expiresAt: 1_800_000_000_000 }), { status: 200 });
      }
      if (path === '/v1/config') {
        return new Response(JSON.stringify({ error: { code: 'NotFound', messageKey: 'ant.error.notFound', retryable: false, action: 'login' }, requestId: 'test' }), { status: 404 });
      }
      if (path === '/v1/catalog/providers') {
        return new Response(JSON.stringify([{
          typeId: 'provider-alpha', adapterRevision: 'adapter-alpha-v1', supportedDomains: ['ai/text'],
          configSchema: { type: 'object', properties: { region: { type: 'string', enum: ['test'] } } },
          resourceSchema: {}, capabilitySchema: {}, extensionSchema: {},
          entitlementFacts: [{ factId: 'alpha-usd-grant-promo', metricId: 'metric.usd.cents', unit: 'usd_cents', description: 'fixture' }],
          semantics: [
            { semanticsRef: 'sem.fixed-window-counter', acceptedRevisions: ['semrev-1'], resourceKind: 'counter', commercialRole: 'limit' },
            { semanticsRef: 'sem.entitlement-pool', acceptedRevisions: ['semrev-1'], resourceKind: 'counter', commercialRole: 'entitlement' },
            { semanticsRef: 'sem.occupancy-slot', acceptedRevisions: ['semrev-1'], resourceKind: 'occupancy', commercialRole: 'limit' },
          ],
          profiles: [{ profileId: 'profile.chat.basic', domain: 'ai/text', requiredDimensions: [
            { metricId: 'metric.tokens.total', role: 'limit' },
            { metricId: 'metric.usd.cents', role: 'entitlement' },
            { metricId: 'metric.concurrency.request', role: 'occupancy' },
          ] }],
        }]), { status: 200 });
      }
      if (path === '/v1/catalog/scopes') return new Response(JSON.stringify({ fixture: true, scopes: [
        { scopeId: 'acct-demo-1', kind: 'account', providerTypeId: 'provider-alpha', facts: [
          { physicalKey: 'phys-alpha:acct-demo-1:daily-token', factId: 'alpha-daily-token-limit', scopeId: 'acct-demo-1', providerTypeId: 'provider-alpha', metricId: 'metric.tokens.total', unit: 'token', quantumExp: 0, metered: false, semanticsRef: 'sem.fixed-window-counter', semanticsRevision: 'semrev-1', windowKind: 'fixed', windowPeriod: 'day', windowTimeZone: 'Asia/Singapore', capacityBound: '1000000', initialRemaining: '1000000' },
          { physicalKey: 'phys-alpha:acct-demo-1:usd-grant-promo', factId: 'alpha-usd-grant-promo', scopeId: 'acct-demo-1', providerTypeId: 'provider-alpha', metricId: 'metric.usd.cents', unit: 'usd_cents', quantumExp: 0, currency: 'USD', metered: true, semanticsRef: 'sem.entitlement-pool', semanticsRevision: 'semrev-1', windowKind: 'lifetime', capacityBound: '500', initialRemaining: '500' },
          { physicalKey: 'phys-alpha:acct-demo-1:concurrency', factId: 'alpha-concurrency-limit', scopeId: 'acct-demo-1', providerTypeId: 'provider-alpha', metricId: 'metric.concurrency.request', unit: 'slot', quantumExp: 0, metered: false, semanticsRef: 'sem.occupancy-slot', semanticsRevision: 'semrev-1', windowKind: 'lifetime', capacityBound: '1', initialRemaining: '1' },
        ] },
      ] }), { status: 200 });
      if (path === '/v1/config/drafts' && init?.method === 'POST') {
        return new Response(JSON.stringify(draft(1, blankDocument)), { status: 201 });
      }
      if (path === '/v1/secrets' && init?.method === 'POST') {
        expect(JSON.parse(String(init.body)).value).toBe('local-fixture-key-123456');
        return new Response(JSON.stringify({ ref: 'secret-alpha', revision: 'secret-rev-1', mask: '…3456' }), { status: 201 });
      }
      if (path === '/v1/secrets/secret-alpha/rotate' && init?.method === 'POST') {
        expect(JSON.parse(String(init.body))).toMatchObject({ expectedRevision: 'secret-rev-1', value: 'local-fixture-key-rotated-654321' });
        return new Response(JSON.stringify({ ref: 'secret-alpha', revision: 'secret-rev-2', mask: '…4321' }), { status: 200 });
      }
      if (path === '/v1/config/drafts/draft-guided' && init?.method === 'PATCH') {
        storedDefinition = JSON.parse(String(init.body)).definition as ConfigDocument;
        draftVersion += 1;
        return new Response(JSON.stringify(draft(draftVersion, storedDefinition)), { status: 200 });
      }
      throw new Error(`Unexpected API request: ${path}`);
    });

    renderAt('/providers');
    fireEvent.click(await screen.findByRole('button', { name: '开始配置' }));
    fireEvent.change(await screen.findByLabelText('本地测试账户范围'), { target: { value: 'acct-demo-1' } });
    fireEvent.change(await screen.findByLabelText('账户显示名称'), { target: { value: '本地测试账户' } });
    fireEvent.change(screen.getByLabelText('region'), { target: { value: 'test' } });
    fireEvent.click(screen.getByRole('button', { name: '添加到账户列表' }));
    const editor = await screen.findByLabelText('配置文档');
    await waitFor(() => expect((editor as HTMLTextAreaElement).value).toContain('provider-alpha'));
    const accountId = JSON.parse((editor as HTMLTextAreaElement).value).accounts[0].accountId as string;
    expect(accountId).toBe('acct-demo-1');

    fireEvent.change(screen.getByLabelText('所属账户'), { target: { value: accountId } });
    fireEvent.change(screen.getByLabelText('服务领域'), { target: { value: 'ai/text' } });
    fireEvent.change(screen.getByLabelText('能力配置'), { target: { value: 'profile.chat.basic' } });
    fireEvent.change(screen.getByLabelText('服务端点'), { target: { value: 'https://provider.example.test/v1' } });
    fireEvent.change(screen.getByLabelText('访问凭证'), { target: { value: 'local-fixture-key-123456' } });
    fireEvent.click(screen.getByRole('button', { name: '加密保存凭证并添加连接' }));

    await waitFor(() => expect(storedDefinition.bindings).toHaveLength(1));
    expect(storedDefinition.bindings[0]).toMatchObject({
      accountId,
      domain: 'ai/text', credentialRef: 'secret-alpha', credentialRevision: 'secret-rev-1',
      capabilityProfiles: ['profile.chat.basic'],
    });
    expect(JSON.stringify(storedDefinition)).not.toContain('local-fixture-key-123456');
    expect((editor as HTMLTextAreaElement).value).toContain('secret-alpha');

    const resourceBindingSelect = screen.getAllByLabelText('要更新的连接')[1];
    fireEvent.change(resourceBindingSelect!, { target: { value: storedDefinition.bindings[0]?.bindingId } });
    fireEvent.change(screen.getByLabelText('limit · metric.tokens.total'), { target: { value: 'phys-alpha:acct-demo-1:daily-token' } });
    fireEvent.change(screen.getByLabelText('entitlement · metric.usd.cents'), { target: { value: 'phys-alpha:acct-demo-1:usd-grant-promo' } });
    fireEvent.change(screen.getByLabelText('occupancy · metric.concurrency.request'), { target: { value: 'phys-alpha:acct-demo-1:concurrency' } });
    fireEvent.click(screen.getByRole('button', { name: '创建资源并保存草稿' }));
    await waitFor(() => expect(storedDefinition.resources).toHaveLength(3));
    expect(storedDefinition.resources.map((resource) => resource.configuredCapacity)).toEqual(['1000000', '500', '1']);
    expect(storedDefinition.resources.every((resource) => resource.ownerId === 'acct-demo-1' && resource.ownerKind === 'account')).toBe(true);
    const counterResources = storedDefinition.resources.filter((resource) => resource.kind === 'counter');
    expect(counterResources).toHaveLength(2);
    expect(counterResources.every((resource) => {
      const opening = storedDefinition.evidence.find((item) => item.evidenceId === resource.initialStateEvidenceRef);
      return opening?.kind === 'initial_state' && opening.subjectRef === resource.resourceId && opening.payload.remaining !== undefined;
    })).toBe(true);
    expect(storedDefinition.resources.find((resource) => resource.kind === 'occupancy')?.initialStateEvidenceRef).toBeUndefined();
    const entitlement = storedDefinition.resources.find((resource) => resource.commercialRole === 'entitlement');
    const group = storedDefinition.entitlementGroups.find((item) => item.groupId === entitlement?.entitlementGroupId);
    expect(group).toMatchObject({ scopeKind: 'account', scopeId: accountId, metricId: 'metric.usd.cents', unit: 'usd_cents' });
    expect(group?.order).toHaveLength(1);
    expect(group?.order[0]?.resourceId).toBe(entitlement?.resourceId);
    expect(storedDefinition.evidence).toHaveLength(5);
    expect(storedDefinition.evidence.filter((item) => item.kind === 'bound').every((item) => item.payload.enforcement === 'localtest-fixture')).toBe(true);
    expect(storedDefinition.bindings[0]?.resourceRefs).toHaveLength(3);
    expect(JSON.stringify(storedDefinition)).toContain('localtest-fixture:acct-demo-1');

    fireEvent.change(screen.getByLabelText('资源池名称'), { target: { value: '演示池' } });
    fireEvent.click(screen.getByRole('button', { name: '创建调用路径和资源池' }));
    await waitFor(() => expect(storedDefinition.offers).toHaveLength(1));
    expect(storedDefinition.offers[0]).toMatchObject({
      bindingId: storedDefinition.bindings[0]?.bindingId,
      capabilityProfile: 'profile.chat.basic',
      enabled: true,
      resourceRefs: storedDefinition.bindings[0]?.resourceRefs,
    });
    expect(storedDefinition.offers[0]?.requiredEvidenceRefs).toHaveLength(3);
    expect(storedDefinition.pools[0]).toMatchObject({ namespace: 'demo', name: '演示池', enabled: true, members: [{ priority: 10, enabled: true }] });

    fireEvent.change(screen.getAllByLabelText('要更新的连接')[0]!, { target: { value: storedDefinition.bindings[0]?.bindingId } });
    fireEvent.change(screen.getByLabelText('新访问凭证'), { target: { value: 'local-fixture-key-rotated-654321' } });
    fireEvent.click(screen.getByRole('button', { name: '加密并更新凭证' }));
    await waitFor(() => expect(storedDefinition.bindings[0]?.credentialRevision).toBe('secret-rev-2'));
    expect(JSON.stringify(storedDefinition)).not.toContain('local-fixture-key-rotated-654321');
    expect(screen.getByRole('status')).toHaveTextContent('凭证已加密并保存');
  });

  it('allows a non-fixture provider account to be added to a draft without inventing verified capacity', async () => {
    const blankDocument: ConfigDocument = {
      schemaVersion: 1,
      accounts: [], bindings: [], resources: [], entitlementGroups: [], offers: [], pools: [], grants: [], evidence: [], unsupportedDrafts: [],
    };
    let storedDefinition = blankDocument;
    const draft = (definition: ConfigDocument) => ({
      draftId: 'draft-account', baseRevision: '', version: 1, status: 'editing', definition, fieldIssues: [],
    });
    vi.mocked(fetch).mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read', 'configure'], csrfToken: 'csrf', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/config') return new Response(JSON.stringify({ error: { code: 'NotFound', messageKey: 'ant.error.notFound', retryable: false, action: 'login' }, requestId: 'test' }), { status: 404 });
      if (path === '/v1/catalog/providers') return new Response(JSON.stringify([{
        typeId: 'provider-live', adapterRevision: 'adapter-live-v1', supportedDomains: ['ai/text'],
        configSchema: { type: 'object', properties: {} }, resourceSchema: {}, capabilitySchema: {}, extensionSchema: {},
        entitlementFacts: [], semantics: [], profiles: [],
      }]), { status: 200 });
      if (path === '/v1/catalog/scopes') return new Response(JSON.stringify({ fixture: false, scopes: [] }), { status: 200 });
      if (path === '/v1/config/drafts' && init?.method === 'POST') return new Response(JSON.stringify(draft(blankDocument)), { status: 201 });
      if (path === '/v1/config/drafts/draft-account' && init?.method === 'PATCH') {
        storedDefinition = JSON.parse(String(init.body)).definition as ConfigDocument;
        return new Response(JSON.stringify(draft(storedDefinition)), { status: 200 });
      }
      throw new Error(`Unexpected API request: ${path}`);
    });

    renderAt('/providers');
    fireEvent.click(await screen.findByRole('button', { name: '开始配置' }));
    expect(screen.queryByText('此服务器当前使用本地测试目录；目录中的供应商数据不是可用的真实服务。')).not.toBeInTheDocument();
    fireEvent.change(await screen.findByLabelText(/账户编号/), { target: { value: 'account-live-01' } });
    fireEvent.change(screen.getByLabelText('账户显示名称'), { target: { value: '正式账户草稿' } });
    fireEvent.click(screen.getByRole('button', { name: '添加到账户列表' }));
    const editor = screen.getByLabelText('配置文档');
    await waitFor(() => expect(JSON.parse((editor as HTMLTextAreaElement).value).accounts).toHaveLength(1));
    expect(JSON.parse((editor as HTMLTextAreaElement).value).resources).toEqual([]);
    expect(JSON.parse((editor as HTMLTextAreaElement).value).evidence).toEqual([]);
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(storedDefinition.accounts).toEqual([{
      accountId: 'account-live-01', providerTypeId: 'provider-live', displayName: '正式账户草稿', enabled: true,
    }]));

    fireEvent.change(screen.getByLabelText(/账户编号/), { target: { value: 'account-live-01' } });
    fireEvent.click(screen.getByRole('button', { name: '添加到账户列表' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('账户编号不能为空或重复');
  });

  it('checks and reloads a saved DeepSeek balance observation without sending the credential to the browser', async () => {
    const definition: ConfigDocument = {
      schemaVersion: 1,
      accounts: [{ accountId: 'account-ds', providerTypeId: 'deepseek-standard', displayName: '我的账户', enabled: true }],
      bindings: [{ bindingId: 'binding-ds', accountId: 'account-ds', domain: 'ai/text', endpoint: 'https://api.deepseek.com', credentialRef: 'secret-ds', credentialRevision: 'revision-ds-1', enabled: true, capabilityProfiles: ['profile.chat.basic'], resourceRefs: [] }],
      resources: [], entitlementGroups: [], offers: [], pools: [], grants: [], evidence: [], unsupportedDrafts: [],
    };
    const savedObservation = {
      isAvailable: true,
      observedAt: 1_800_000_000_000,
      balances: [{ currency: 'USD', totalCents: '1250', grantedCents: '250', toppedUpCents: '1000' }],
    };
    const draft = { draftId: 'draft-deepseek', baseRevision: '', version: 1, status: 'editing', definition, fieldIssues: [] };
    const fetchMock = vi.mocked(fetch);
    let postCount = 0;
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === '/v1/session') return new Response(JSON.stringify({ principalId: 'admin', namespace: 'demo', permissions: ['read', 'configure'], csrfToken: 'csrf-balance', expiresAt: 1_800_000_000_000 }), { status: 200 });
      if (path === '/v1/config') return new Response(JSON.stringify({ error: { code: 'NotFound', messageKey: 'ant.error.notFound', retryable: false, action: 'login' }, requestId: 'test' }), { status: 404 });
      if (path === '/v1/catalog/providers') return new Response(JSON.stringify([{
        typeId: 'deepseek-standard', adapterRevision: 'deepseek-chat-v1', supportedDomains: ['ai/text'],
        configSchema: { type: 'object', properties: {} }, resourceSchema: {}, capabilitySchema: {}, extensionSchema: {},
        entitlementFacts: [], semantics: [], profiles: [{ profileId: 'profile.chat.basic', domain: 'ai/text', requiredDimensions: [] }],
      }]), { status: 200 });
      if (path === '/v1/catalog/scopes') return new Response(JSON.stringify({ fixture: false, scopes: [] }), { status: 200 });
      if (path === '/v1/config/drafts/draft-deepseek') return new Response(JSON.stringify(draft), { status: 200 });
      if (path === '/v1/providers/deepseek/balance-observations' && init?.method === 'POST') {
        postCount++;
        expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-balance');
        const body = JSON.parse(String(init.body)) as Record<string, unknown>;
        expect(body).toEqual({ commandId: expect.any(String), credentialRef: 'secret-ds', credentialRevision: 'revision-ds-1' });
        expect(JSON.stringify(body)).not.toContain('sk-');
        return new Response(JSON.stringify(savedObservation), { status: 200 });
      }
      if (path === '/v1/providers/deepseek/balance-observations/balance-cmd' || /^\/v1\/providers\/deepseek\/balance-observations\/[\da-f-]+$/i.test(path)) {
        return new Response(JSON.stringify(savedObservation), { status: 200 });
      }
      throw new Error(`Unexpected API request: ${path}`);
    });

    const router = renderAt('/providers?draft=draft-deepseek');
    fireEvent.change(await screen.findByLabelText('要查询的连接'), { target: { value: 'binding-ds' } });
    expect(screen.getAllByLabelText('要更新的连接').map((select) => (select as HTMLSelectElement).value)).toEqual(['', '']);
    fireEvent.click(screen.getByRole('button', { name: '查询余额' }));
    expect(await screen.findByText('总额：USD 12.50')).toBeInTheDocument();
    expect(screen.getByText('赠余额：USD 2.50')).toBeInTheDocument();
    expect(postCount).toBe(1);
    const post = fetchMock.mock.calls.find(([input, init]) => String(input) === '/v1/providers/deepseek/balance-observations' && init?.method === 'POST');
    expect(post).toBeDefined();
    const commandId = JSON.parse(String(post?.[1]?.body)).commandId as string;
    await waitFor(() => {
      expect(router.state.location.search).toContain(`balanceCommand=${commandId}`);
      expect(router.state.location.search).toContain('balanceBinding=binding-ds');
    });
    cleanup();

    renderAt(`/providers?draft=draft-deepseek&balanceBinding=binding-ds&balanceCommand=${commandId}`);
    expect(await screen.findByText('总额：USD 12.50')).toBeInTheDocument();
    expect(postCount).toBe(1);
    expect(fetchMock.mock.calls.some(([input]) => String(input) === `/v1/providers/deepseek/balance-observations/${commandId}`)).toBe(true);
  });

  it('renders the 404 page for unknown routes', () => {
    renderAt('/no-such-page');
    expect(screen.getByRole('heading', { name: '页面不存在' })).toBeInTheDocument();
  });
});
