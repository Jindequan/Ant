import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { rememberedDraft, rememberDraft, forgetDraft } from '../../api/draft-pointer';
import { ApiError } from '../../api/client';
import {
  activateConfigDraft,
  createConfigDraft,
  emptyConfigDocument,
  newCommandId,
  readProviderCatalog,
  readProviderScopes,
  readActiveConfig,
  readConfigDraft,
  observeDeepSeekBalance,
  readDeepSeekBalanceObservation,
  rotateSecret,
  saveConfigDraft,
  validateConfigDraft,
  writeSecret,
  type ConfigDraftView,
  type ConfigProjectionView,
  type ProviderDescriptorView,
  type DeepSeekBalanceObservationView,
  type VerifiedResourceFactView,
} from '../../api/config';
import type { ValidationResult } from '../../api/validate';
import type { ConfigDocument, EntitlementGroup } from '../../api/types';
import { useSession } from '../../auth/SessionContext';
import { PageStateFrame } from '../../components/PageState';
import { getLocale, t } from '../../i18n';

const activeConfigKey = ['active-config'] as const;
const draftKey = (id: string) => ['config-draft', id] as const;
const collectionKeys = [
  ['accounts', 'accounts'],
  ['bindings', 'bindings'],
  ['resources', 'resources'],
  ['offers', 'offers'],
  ['pools', 'pools'],
  ['grants', 'grants'],
] as const;

type BusyAction = 'create' | 'save' | 'validate' | 'activate' | null;

function formatDocument(document: ConfigDocument): string {
  return JSON.stringify(document, null, 2);
}

function errorMessage(error: unknown): string {
  if (error instanceof ApiError && error.status === 409) return t('config.error.conflict');
  if (error instanceof ApiError && error.status === 403) return t('config.error.permission');
  if (error instanceof ApiError && error.status === 401) return t('config.error.session');
  if (error instanceof ApiError && error.status === 422) return t('config.error.notExecutable');
  if (error instanceof ApiError && error.status === 400) return t('config.error.invalid');
  if (error instanceof ApiError && error.status === 429) return t('config.error.rateLimited');
  return t('config.error.request');
}

function parseEditor(text: string): { document?: ConfigDocument; syntaxError?: true } {
  try {
    const value: unknown = JSON.parse(text);
    if (typeof value !== 'object' || value === null || Array.isArray(value)) return { syntaxError: true };
    return { document: value as ConfigDocument };
  } catch {
    return { syntaxError: true };
  }
}

function asDraft(value: ConfigDraftView): ConfigDraftView {
  return { ...value, fieldIssues: value.fieldIssues ?? [] };
}

type SchemaField = { name: string; type: string; enumValues: string[] };

function configFields(schema: unknown): SchemaField[] {
  if (typeof schema !== 'object' || schema === null || Array.isArray(schema)) return [];
  const properties = (schema as { properties?: unknown }).properties;
  if (typeof properties !== 'object' || properties === null || Array.isArray(properties)) return [];
  return Object.entries(properties).flatMap(([name, raw]) => {
    if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return [];
    const field = raw as { type?: unknown; enum?: unknown };
    if (typeof field.type !== 'string' || !['string', 'integer', 'number', 'boolean'].includes(field.type)) return [];
    return [{ name, type: field.type, enumValues: Array.isArray(field.enum) ? field.enum.filter((value): value is string => typeof value === 'string') : [] }];
  });
}

function providerLabel(provider: ProviderDescriptorView): string {
  return `${provider.typeId} · ${provider.supportedDomains.join(', ')}`;
}

function formatBalanceCents(currency: string, cents: string): string {
  if (!/^(0|[1-9][0-9]*)$/.test(cents)) return t('config.balance.invalidAmount');
  const value = BigInt(cents);
  const locale = getLocale() === 'zh' ? 'zh-CN' : 'en-US';
  const integer = new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(value / 100n);
  const decimal = new Intl.NumberFormat(locale).formatToParts(1.1).find((part) => part.type === 'decimal')?.value ?? '.';
  const fraction = String(value % 100n).padStart(2, '0');
  return `${currency} ${integer}${decimal}${fraction}`;
}

function formatObservationTime(value: number): string {
  return new Intl.DateTimeFormat(getLocale() === 'zh' ? 'zh-CN' : 'en-US', { dateStyle: 'medium', timeStyle: 'short' }).format(value);
}

function balanceErrorKey(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 401) return 'config.balance.error.session';
    if (error.status === 403) return 'config.balance.error.permission';
    if (error.status === 404) return 'config.balance.error.notFound';
    if (error.status === 409) return 'config.balance.error.stale';
    if (error.status === 502) return 'config.balance.error.provider';
    if (error.status === 429) return 'config.balance.error.busy';
  }
  return 'config.balance.error.unavailable';
}

export function ProvidersPage() {
  const navigate = useNavigate();
  const { session, isLoading: sessionLoading } = useSession();
  const csrfToken = session?.csrfToken ?? '';
  const queryClient = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const draftId = searchParams.get('draft') ?? '';
  const canRead = session?.permissions.some((permission) => permission === 'read' || permission === 'configure') ?? false;
  const canConfigure = session?.permissions.includes('configure') ?? false;
  const activeQuery = useQuery({
    queryKey: activeConfigKey,
    queryFn: readActiveConfig,
    enabled: canRead,
  });
  const catalogQuery = useQuery({
    queryKey: ['provider-catalog'],
    queryFn: readProviderCatalog,
    enabled: canRead,
    staleTime: 60_000,
  });
  const scopesQuery = useQuery({
    queryKey: ['provider-scopes'],
    queryFn: readProviderScopes,
    enabled: canRead,
    staleTime: 60_000,
  });
  const draftQuery = useQuery({
    queryKey: draftKey(draftId),
    queryFn: () => readConfigDraft(draftId),
    enabled: canRead && draftId.length > 0,
    retry: false,
  });
  const balanceCommand = searchParams.get('balanceCommand') ?? '';
  const [balancePendingCommand, setBalancePendingCommand] = useState('');
  const [balanceSubmitting, setBalanceSubmitting] = useState(false);
  const [balanceActionError, setBalanceActionError] = useState('');
  const [balanceBindingId, setBalanceBindingId] = useState(searchParams.get('balanceBinding') ?? '');
  const balanceCommandId = useRef('');
  const balanceAbort = useRef<AbortController | null>(null);
  const balanceQuery = useQuery({
    queryKey: ['deepseek-balance-observation', balanceCommand],
    queryFn: ({ signal }) => readDeepSeekBalanceObservation(balanceCommand, signal),
    enabled: canConfigure && balanceCommand.length > 0 && balancePendingCommand !== balanceCommand,
    retry: false,
  });
  const [editorText, setEditorText] = useState('');
  const [savedText, setSavedText] = useState('');
  const [busy, setBusy] = useState<BusyAction>(null);
  const [pageError, setPageError] = useState('');
  const [editorError, setEditorError] = useState('');
  const [showActivationConfirm, setShowActivationConfirm] = useState(false);
  const [showCloseConfirm, setShowCloseConfirm] = useState(false);
  const [editorOpen, setEditorOpen] = useState(true);
  const [localValidation, setLocalValidation] = useState<ValidationResult | null>(null);
  const [selectedProviderId, setSelectedProviderId] = useState('');
  const [accountName, setAccountName] = useState('');
  const [accountIdInput, setAccountIdInput] = useState('');
  const [accountScopeId, setAccountScopeId] = useState('');
  const [accountExtensionValues, setAccountExtensionValues] = useState<Record<string, string>>({});
  const [selectedAccountId, setSelectedAccountId] = useState('');
  const [bindingDomain, setBindingDomain] = useState('');
  const [bindingProfile, setBindingProfile] = useState('');
  const [bindingEndpoint, setBindingEndpoint] = useState('');
  const [credentialValue, setCredentialValue] = useState('');
  const [selectedBindingId, setSelectedBindingId] = useState('');
  const [rotationValue, setRotationValue] = useState('');
  const [rotationInfo, setRotationInfo] = useState('');
  const [resourceSelections, setResourceSelections] = useState<Record<string, string>>({});
  const [poolName, setPoolName] = useState('');
  const activateButton = useRef<HTMLButtonElement>(null);
  const editorToggleButton = useRef<HTMLButtonElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const keepCloseEditingButton = useRef<HTMLButtonElement>(null);
  const saveAndCloseButton = useRef<HTMLButtonElement>(null);
  const discardChangesButton = useRef<HTMLButtonElement>(null);
  const confirmButton = useRef<HTMLButtonElement>(null);
  const keepEditingButton = useRef<HTMLButtonElement>(null);
  const secretCommandId = useRef('');
  const dialogWasOpen = useRef(false);
  const closeDialogWasOpen = useRef(false);
  const editorWasOpen = useRef(false);
  const draft = draftQuery.data ? asDraft(draftQuery.data) : undefined;
  const parsed = useMemo(() => parseEditor(editorText), [editorText]);
  const dirty = draft !== undefined && editorText !== savedText;
  const active = activeQuery.data;
  const providers = catalogQuery.data ?? [];
  const catalogScopes = scopesQuery.data?.scopes ?? [];
  const fixtureScopes = scopesQuery.data?.fixture === true ? scopesQuery.data.scopes : [];
  const selectedProvider = providers.find((provider) => provider.typeId === selectedProviderId) ?? providers[0];
  const accountFields = selectedProvider ? configFields(selectedProvider.configSchema) : [];

  function reportError(error: unknown) {
    if (error instanceof ApiError && error.status === 401) queryClient.setQueryData(['session'], null);
    setPageError(errorMessage(error));
  }

  function selectBinding(bindingId: string) {
    setSelectedBindingId(bindingId);
  }

  function selectBalanceBinding(bindingId: string) {
    setBalanceBindingId(bindingId);
    setBalanceActionError('');
    balanceCommandId.current = '';
    setBalancePendingCommand('');
    const next = new URLSearchParams(searchParams);
    next.delete('balanceCommand');
    if (bindingId) next.set('balanceBinding', bindingId);
    else next.delete('balanceBinding');
    setSearchParams(next, { replace: true });
  }

  async function checkDeepSeekBalance() {
    if (!parsed.document || !canConfigure || selectedProvider?.typeId !== 'deepseek-standard' || balanceSubmitting) return;
    const binding = parsed.document.bindings.find((item) => item.bindingId === balanceBindingId);
    const account = binding && parsed.document.accounts.find((item) => item.accountId === binding.accountId);
    if (!binding || !account || account.providerTypeId !== 'deepseek-standard' || !binding.credentialRef || !binding.credentialRevision) return;
    const commandId = balanceActionError
      ? (balanceCommandId.current || balanceCommand || newCommandId())
      : newCommandId();
    balanceCommandId.current = commandId;
    setBalanceActionError('');
    setBalancePendingCommand(commandId);
    setBalanceSubmitting(true);
    const controller = new AbortController();
    balanceAbort.current = controller;
    const next = new URLSearchParams(searchParams);
    next.set('balanceCommand', commandId);
    next.set('balanceBinding', binding.bindingId);
    setSearchParams(next, { replace: true });
    try {
      await observeDeepSeekBalance({ commandId, credentialRef: binding.credentialRef, credentialRevision: binding.credentialRevision }, csrfToken, controller.signal);
      balanceCommandId.current = '';
    } catch (error) {
      setBalanceActionError(error instanceof DOMException && error.name === 'AbortError' ? 'config.balance.error.cancelled' : balanceErrorKey(error));
    } finally {
      balanceAbort.current = null;
      setBalanceSubmitting(false);
      setBalancePendingCommand('');
    }
  }

  function cancelDeepSeekBalance() {
    balanceAbort.current?.abort();
  }

  useEffect(() => {
    setEditorOpen(true);
  }, [draftId]);

  useEffect(() => {
    if (!draftQuery.data) return;
    const text = formatDocument(draftQuery.data.definition);
    setEditorText(text);
    setSavedText(text);
    setEditorError('');
  }, [draftQuery.data]);

  useEffect(() => {
    if (!selectedProvider && providers[0]) setSelectedProviderId(String(providers[0].typeId));
  }, [providers, selectedProvider]);

  useEffect(() => {
    for (const error of [activeQuery.error, draftQuery.error]) {
      if (error instanceof ApiError && error.status === 401) {
        queryClient.setQueryData(['session'], null);
        break;
      }
    }
  }, [activeQuery.error, draftQuery.error, queryClient]);

  useEffect(() => {
    setLocalValidation(null);
    if (parsed.document === undefined) return;
    let cancelled = false;
    const timeout = window.setTimeout(() => {
      void import('../../api/validate').then(({ validateConfigDocument }) => {
        if (!cancelled) setLocalValidation(validateConfigDocument(parsed.document));
      });
    }, 300);
    return () => {
      cancelled = true;
      window.clearTimeout(timeout);
    };
  }, [parsed.document]);

  useEffect(() => {
    if (!dirty) return;
    const beforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', beforeUnload);
    return () => window.removeEventListener('beforeunload', beforeUnload);
  }, [dirty]);

  useEffect(() => {
    if (showActivationConfirm) {
      dialogWasOpen.current = true;
      confirmButton.current?.focus();
      const onKeyDown = (event: KeyboardEvent) => {
        if (event.key === 'Escape') setShowActivationConfirm(false);
        if (event.key === 'Tab') {
          if (event.shiftKey && document.activeElement === confirmButton.current) {
            event.preventDefault();
            keepEditingButton.current?.focus();
          } else if (!event.shiftKey && document.activeElement === keepEditingButton.current) {
            event.preventDefault();
            confirmButton.current?.focus();
          }
        }
      };
      window.addEventListener('keydown', onKeyDown);
      return () => window.removeEventListener('keydown', onKeyDown);
    }
    if (dialogWasOpen.current) {
      dialogWasOpen.current = false;
      activateButton.current?.focus();
    }
    return undefined;
  }, [showActivationConfirm]);

  useEffect(() => {
    if (!showCloseConfirm) {
      if (closeDialogWasOpen.current) {
        closeDialogWasOpen.current = false;
        (editorOpen ? closeButton.current : editorToggleButton.current)?.focus();
      }
      return undefined;
    }
    closeDialogWasOpen.current = true;
    keepCloseEditingButton.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setShowCloseConfirm(false);
        return;
      }
      if (event.key !== 'Tab') return;
      const buttons = [keepCloseEditingButton.current, saveAndCloseButton.current, discardChangesButton.current].filter((button): button is HTMLButtonElement => button !== null);
      if (buttons.length === 0) return;
      const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
      const next = event.shiftKey
        ? (current <= 0 ? buttons.length - 1 : current - 1)
        : (current < 0 || current === buttons.length - 1 ? 0 : current + 1);
      event.preventDefault();
      buttons[next]?.focus();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [editorOpen, showCloseConfirm]);

  useEffect(() => {
    if (editorOpen) {
      editorWasOpen.current = true;
    } else if (editorWasOpen.current) {
      editorWasOpen.current = false;
      editorToggleButton.current?.focus();
    }
  }, [editorOpen]);

  async function startDraft() {
    if (!canConfigure) return;
    setBusy('create');
    setPageError('');
    try {
      const definition = (active as ConfigProjectionView | null)?.document ?? emptyConfigDocument();
      const created = await createConfigDraft(definition, csrfToken, active?.revision ?? '');
      queryClient.setQueryData(draftKey(created.draftId), created);
      setSearchParams({ draft: created.draftId, view: "advanced" });
    } catch (error) {
      reportError(error);
    } finally {
      setBusy(null);
    }
  }

  async function saveDraft(): Promise<ConfigDraftView | null> {
    if (!draft || !parsed.document || !canConfigure) {
      setEditorError(parsed.syntaxError ? t('config.editor.invalidJson') : t('config.error.permission'));
      return null;
    }
    setBusy('save');
    setPageError('');
    setEditorError('');
    try {
      const updated = await saveConfigDraft(draft, parsed.document, csrfToken);
      queryClient.setQueryData(draftKey(draft.draftId), updated);
      return updated;
    } catch (error) {
      reportError(error);
      return null;
    } finally {
      setBusy(null);
    }
  }

  function requestCloseEditor() {
    if (dirty) {
      setShowCloseConfirm(true);
      return;
    }
    setEditorOpen(false);
  }

  async function saveAndCloseEditor() {
    const saved = await saveDraft();
    if (!saved) return;
    const text = formatDocument(saved.definition);
    setEditorText(text);
    setSavedText(text);
    setShowCloseConfirm(false);
    setEditorOpen(false);
  }

  useEffect(() => {
    if (!session || !draft) return;
    if (draft.status === 'editing' || draft.status === 'validated') rememberDraft(session.namespace, session.principalId, draft.draftId);
    else if (rememberedDraft(session.namespace, session.principalId) === draft.draftId) forgetDraft(session.namespace, session.principalId);
  }, [session, draft]);

  function discardAndCloseEditor() {
    setEditorText(savedText);
    setEditorError('');
    setLocalValidation(null);
    setShowCloseConfirm(false);
    setEditorOpen(false);
  }

  function addAccount() {
    if (!parsed.document || !selectedProvider || !canConfigure) return;
    const isFixtureCatalog = scopesQuery.data?.fixture === true && fixtureScopes.some((item) => item.providerTypeId === selectedProvider.typeId);
    const scope = catalogScopes.find((item) => item.scopeId === accountScopeId && item.kind === 'account' && item.providerTypeId === selectedProvider.typeId);
    const accountId = scope?.scopeId ?? (isFixtureCatalog ? '' : accountIdInput.trim());
    if (!accountId || !/^[A-Za-z0-9._:-]{1,64}$/.test(accountId) || parsed.document.accounts.some((item) => item.accountId === accountId)) {
      setEditorError(isFixtureCatalog ? t('config.account.selectFixtureScope') : t('config.account.invalidId'));
      return;
    }
    const providerExtensions: Record<string, string | number | boolean> = {};
    for (const field of accountFields) {
      const value = accountExtensionValues[field.name] ?? '';
      if (value === '') continue;
      if (field.type === 'integer' || field.type === 'number') {
        const numeric = Number(value);
        if (!Number.isFinite(numeric) || (field.type === 'integer' && !Number.isInteger(numeric))) {
          setEditorError(t('config.account.invalidField', { field: field.name }));
          return;
        }
        providerExtensions[field.name] = numeric;
      } else if (field.type === 'boolean') {
        providerExtensions[field.name] = value === 'true';
      } else {
        providerExtensions[field.name] = value;
      }
    }
    const account = {
      accountId,
      providerTypeId: selectedProvider.typeId,
      displayName: accountName.trim() || `${selectedProvider.typeId} account`,
      enabled: true,
      ...(Object.keys(providerExtensions).length > 0 ? { providerExtensions } : {}),
    };
    const next = { ...parsed.document, accounts: [...parsed.document.accounts, account] };
    setEditorText(formatDocument(next));
    setAccountName('');
    setAccountIdInput('');
    setAccountScopeId('');
    setAccountExtensionValues({});
    setEditorError('');
  }

  async function addBinding() {
    if (!parsed.document || !draft || !canConfigure || !selectedProvider || !credentialValue) return;
    const account = parsed.document.accounts.find((item) => item.accountId === selectedAccountId);
    if (!account || account.providerTypeId !== selectedProvider.typeId) {
      setEditorError(t('config.binding.selectAccount'));
      return;
    }
    let endpoint: URL;
    try {
      endpoint = new URL(bindingEndpoint);
    } catch {
      setEditorError(t('config.binding.invalidEndpoint'));
      return;
    }
    if (!['http:', 'https:'].includes(endpoint.protocol) || endpoint.username || endpoint.password || endpoint.search || endpoint.hash || !selectedProvider.supportedDomains.includes(bindingDomain) || !selectedProvider.profiles.some((profile) => profile.profileId === bindingProfile && profile.domain === bindingDomain)) {
      setEditorError(t('config.binding.invalidEndpoint'));
      return;
    }
    setBusy('save');
    setPageError('');
    setEditorError('');
    try {
      secretCommandId.current ||= newCommandId();
      const secret = await writeSecret(credentialValue, csrfToken, secretCommandId.current);
      const binding = {
        bindingId: crypto.randomUUID(),
        accountId: account.accountId,
        domain: bindingDomain,
        endpoint: endpoint.toString(),
        credentialRef: secret.ref,
        credentialRevision: secret.revision,
        enabled: true,
        capabilityProfiles: [bindingProfile],
        resourceRefs: [],
      };
      const next = { ...parsed.document, bindings: [...parsed.document.bindings, binding] };
      const text = formatDocument(next);
      setEditorText(text);
      setCredentialValue('');
      secretCommandId.current = '';
      const saved = await saveConfigDraft(draft, next, csrfToken);
      queryClient.setQueryData(draftKey(saved.draftId), saved);
      setSavedText(formatDocument(saved.definition));
    } catch (error) {
      reportError(error);
    } finally {
      setBusy(null);
    }
  }

  async function rotateBindingCredential() {
    if (!parsed.document || !draft || !canConfigure || !selectedProvider || !rotationValue) return;
    const binding = parsed.document.bindings.find((item) => item.bindingId === selectedBindingId);
    if (!binding || !parsed.document.accounts.some((item) => item.accountId === binding.accountId && item.providerTypeId === selectedProvider.typeId)) {
      setEditorError(t('config.binding.selectBinding'));
      return;
    }
    setBusy('save');
    setPageError('');
    setEditorError('');
    try {
      secretCommandId.current ||= newCommandId();
      const secret = await rotateSecret(binding.credentialRef, binding.credentialRevision, rotationValue, csrfToken, secretCommandId.current);
      const next = {
        ...parsed.document,
        bindings: parsed.document.bindings.map((item) => item.bindingId === binding.bindingId
          ? { ...item, credentialRevision: secret.revision }
          : item),
      };
      const text = formatDocument(next);
      setEditorText(text);
      setRotationValue('');
      secretCommandId.current = '';
      const saved = await saveConfigDraft(draft, next, csrfToken);
      queryClient.setQueryData(draftKey(saved.draftId), saved);
      setSavedText(formatDocument(saved.definition));
      setRotationInfo(t('config.binding.rotated', { mask: secret.mask }));
    } catch (error) {
      reportError(error);
    } finally {
      setBusy(null);
    }
  }

  function factsForAccount(accountId: string): VerifiedResourceFactView[] {
    const alreadyUsed = new Set((parsed.document?.resources ?? [])
      .filter((resource) => resource.ownerKind === 'account' && resource.ownerId === accountId && resource.providerFactId)
      .map((resource) => resource.providerFactId));
    return fixtureScopes
      .filter((scope) => scope.providerTypeId === selectedProvider?.typeId && (scope.scopeId === accountId || (scope.kind === 'shared' && scope.aliases?.includes(accountId))))
      .flatMap((scope) => scope.facts)
      .filter((fact) => !alreadyUsed.has(fact.factId))
      .filter((fact, index, facts) => facts.findIndex((other) => other.physicalKey === fact.physicalKey) === index);
  }

  function factMatchesDimension(fact: VerifiedResourceFactView, dimension: { metricId: string; role: string }): boolean {
    const semantics = selectedProvider?.semantics.find((item) => item.semanticsRef === fact.semanticsRef && item.acceptedRevisions.includes(fact.semanticsRevision));
    if (!semantics || fact.metricId !== dimension.metricId) return false;
    if (dimension.role === 'entitlement') return semantics.commercialRole === 'entitlement';
    if (dimension.role === 'occupancy') return semantics.resourceKind === 'occupancy';
    return semantics.resourceKind !== 'occupancy' && semantics.commercialRole === 'limit';
  }

  async function addProfileResources() {
    if (!parsed.document || !draft || !canConfigure || scopesQuery.data?.fixture !== true) return;
    const binding = parsed.document.bindings.find((item) => item.bindingId === selectedBindingId);
    const account = binding && parsed.document.accounts.find((item) => item.accountId === binding.accountId);
    const profile = selectedProvider?.profiles.find((item) => item.profileId === binding?.capabilityProfiles[0] && item.domain === binding.domain);
    if (!binding || !account || !profile) return;
    const facts = factsForAccount(account.accountId);
    const choices = profile.requiredDimensions.map((dimension) => {
      const fact = facts.find((candidate) => candidate.physicalKey === resourceSelections[`${binding.bindingId}:${dimension.metricId}:${dimension.role}`] && factMatchesDimension(candidate, dimension));
      return { dimension, fact };
    });
    if (choices.some((choice) => !choice.fact)) {
      setEditorError(t('config.resource.selectFacts'));
      return;
    }
    const now = Date.now();
    const entitlementGroups: EntitlementGroup[] = parsed.document.entitlementGroups.map((group) => ({ ...group, order: [...group.order] as EntitlementGroup['order'] }));
    const entitlementGroupByDimension = new Map(entitlementGroups
      .filter((group) => group.scopeKind === 'account' && group.scopeId === account.accountId)
      .map((group) => [`${group.metricId}:${group.unit}`, group]));
    const additions = choices.map(({ fact }) => {
      const verified = fact!;
      const semantics = selectedProvider!.semantics.find((item) => item.semanticsRef === verified.semanticsRef)!;
      const resourceId = crypto.randomUUID();
      const evidenceId = crypto.randomUUID();
      const initialStateEvidenceId = semantics.resourceKind === 'counter' ? crypto.randomUUID() : undefined;
      let entitlementGroupId: string | undefined;
      if (semantics.commercialRole === 'entitlement') {
        const key = `${verified.metricId}:${verified.unit}`;
        let group = entitlementGroupByDimension.get(key);
        let providerOrder = 10;
        if (!group) {
          group = {
            groupId: crypto.randomUUID(), scopeKind: 'account', scopeId: account.accountId,
            metricId: verified.metricId, unit: verified.unit, order: [{ resourceId, providerOrder }],
          };
          entitlementGroups.push(group);
          entitlementGroupByDimension.set(key, group);
        } else {
          providerOrder = Math.max(0, ...group.order.map((step) => step.providerOrder)) + 10;
          group.order.push({ resourceId, providerOrder });
        }
        entitlementGroupId = group.groupId;
      }
      const resource = {
        resourceId, ownerKind: 'account' as const, ownerId: account.accountId,
        metricId: verified.metricId, unit: verified.unit, quantumExp: verified.quantumExp,
        kind: semantics.resourceKind, commercialRole: semantics.commercialRole,
        semanticsRef: verified.semanticsRef, semanticsRevision: verified.semanticsRevision,
        window: {
          kind: verified.windowKind,
          ...(verified.windowPeriod ? { period: verified.windowPeriod } : {}),
          ...(verified.windowTimeZone ? { timeZone: verified.windowTimeZone } : {}),
          ...(verified.windowAnchorISO ? { anchorISO: verified.windowAnchorISO } : {}),
          ...(verified.windowDurationMillis ? { durationMillis: verified.windowDurationMillis } : {}),
        },
        metered: verified.metered,
        ...(verified.currency ? { currency: verified.currency } : {}),
        providerFactId: verified.factId,
        configuredCapacity: verified.capacityBound,
        capacityEvidenceRef: evidenceId,
        ...(entitlementGroupId ? { entitlementGroupId } : {}),
        ...(initialStateEvidenceId ? { initialStateEvidenceRef: initialStateEvidenceId } : {}),
      };
      const evidence = {
        evidenceId, kind: 'bound' as const, subjectRef: account.accountId,
        sourceRef: `localtest-fixture:${verified.scopeId}:${verified.physicalKey}`,
        verifiedAtMillis: now, expiresAtMillis: now + 3_600_000,
        payload: {
          bound: verified.capacityBound, metricId: verified.metricId, unit: verified.unit,
          quantumExp: String(verified.quantumExp), windowKind: verified.windowKind,
          enforcement: 'localtest-fixture',
          ...(verified.currency ? { currency: verified.currency } : {}),
          ...(verified.windowDurationMillis ? { windowDurationMillis: String(verified.windowDurationMillis) } : {}),
          semanticsRevision: verified.semanticsRevision,
        },
      };
      const initialStateEvidence = initialStateEvidenceId ? {
        evidenceId: initialStateEvidenceId, kind: 'initial_state' as const, subjectRef: resourceId,
        sourceRef: `localtest-fixture:${verified.scopeId}:${verified.physicalKey}:initial-state`,
        verifiedAtMillis: now, expiresAtMillis: now + 3_600_000,
        payload: {
          remaining: verified.initialRemaining, metricId: verified.metricId, unit: verified.unit,
          quantumExp: String(verified.quantumExp), windowKind: verified.windowKind,
          ...(verified.currency ? { currency: verified.currency } : {}),
          ...(verified.windowDurationMillis ? { windowDurationMillis: String(verified.windowDurationMillis) } : {}),
          semanticsRevision: verified.semanticsRevision,
        },
      } : undefined;
      return { resource, evidence, initialStateEvidence };
    });
    const resources = additions.map((item) => item!.resource);
    const evidence = additions.flatMap((item) => item!.initialStateEvidence
      ? [item!.evidence, item!.initialStateEvidence]
      : [item!.evidence]);
    const next = {
      ...parsed.document,
      resources: [...parsed.document.resources, ...resources],
      entitlementGroups,
      evidence: [...parsed.document.evidence, ...evidence],
      bindings: parsed.document.bindings.map((item) => item.bindingId === binding.bindingId
        ? { ...item, resourceRefs: [...item.resourceRefs, ...resources.map((resource) => resource.resourceId)] }
        : item),
    };
    const text = formatDocument(next);
    setEditorText(text);
    setBusy('save');
    setEditorError('');
    try {
      const saved = await saveConfigDraft(draft, next, csrfToken);
      queryClient.setQueryData(draftKey(saved.draftId), saved);
      setSavedText(formatDocument(saved.definition));
      setResourceSelections({});
    } catch (error) {
      reportError(error);
    } finally {
      setBusy(null);
    }
  }

  async function addOfferAndPool() {
    if (!parsed.document || !draft || !canConfigure || !selectedProvider || !session) return;
    const binding = parsed.document.bindings.find((item) => item.bindingId === selectedBindingId);
    const account = binding && parsed.document.accounts.find((item) => item.accountId === binding.accountId);
    const profile = selectedProvider.profiles.find((item) => item.profileId === binding?.capabilityProfiles[0] && item.domain === binding.domain);
    const name = poolName.trim();
    if (!binding || !account || !profile || !name) return;
    const boundResources = parsed.document.resources.filter((resource) => binding.resourceRefs.includes(resource.resourceId));
    const covers = profile.requiredDimensions.every((dimension) => boundResources.some((resource) => {
      if (resource.metricId !== dimension.metricId) return false;
      if (dimension.role === 'occupancy') return resource.kind === 'occupancy';
      if (dimension.role === 'entitlement') return resource.commercialRole === 'entitlement';
      return resource.kind !== 'occupancy' && resource.commercialRole === 'limit';
    }));
    if (!covers || profile.requiredDimensions.length === 0 || boundResources.length === 0) {
      setEditorError(t('config.offer.incompleteResources'));
      return;
    }
    const resourceRefs = boundResources.map((resource) => resource.resourceId);
    const offerId = crypto.randomUUID();
    const poolId = crypto.randomUUID();
    const offer = {
      offerId,
      bindingId: binding.bindingId,
      domain: binding.domain,
      capabilityProfile: profile.profileId,
      enabled: true,
      resourceRefs: [resourceRefs[0]!, ...resourceRefs.slice(1)] as [string, ...string[]],
      requiredEvidenceRefs: [...new Set(boundResources.map((resource) => resource.capacityEvidenceRef))],
    };
    const pool = {
      poolId,
      namespace: session.namespace,
      name,
      enabled: true,
      members: [{ offerId, priority: 10, enabled: true }],
    };
    const next = { ...parsed.document, offers: [...parsed.document.offers, offer], pools: [...parsed.document.pools, pool] };
    setEditorText(formatDocument(next));
    setBusy('save');
    setPageError('');
    setEditorError('');
    try {
      const saved = await saveConfigDraft(draft, next, csrfToken);
      queryClient.setQueryData(draftKey(saved.draftId), saved);
      setSavedText(formatDocument(saved.definition));
      setPoolName('');
    } catch (error) {
      reportError(error);
    } finally {
      setBusy(null);
    }
  }

  async function validateDraft() {
    const stored = await saveDraft();
    if (!stored) return;
    setBusy('validate');
    try {
      const checked = await validateConfigDraft(stored, csrfToken);
      queryClient.setQueryData(draftKey(checked.draftId), checked);
      setSavedText(formatDocument(checked.definition));
    } catch (error) {
      reportError(error);
    } finally {
      setBusy(null);
    }
  }

  async function activateDraft() {
    if (!draft || draft.status !== 'validated' || dirty || !canConfigure) return;
    setBusy('activate');
    setPageError('');
    setShowActivationConfirm(false);
    try {
      await activateConfigDraft(draft, active?.revision ?? '', csrfToken);
      await Promise.all([activeQuery.refetch(), draftQuery.refetch()]);
      setEditorText('');
      setSavedText('');
      setSearchParams({ view: "advanced" });
    } catch (error) {
      // Activation may have committed before a network failure. Read the durable
      // draft and active revision before presenting a retry path.
      const [, refreshedDraft] = await Promise.all([activeQuery.refetch(), draftQuery.refetch()]);
      if (refreshedDraft.data?.status === 'activated') {
        setPageError(t('config.activation.confirmedAfterDisconnect'));
        setEditorOpen(false);
        setSearchParams({ view: "advanced" });
      } else {
        reportError(error);
      }
    } finally {
      setBusy(null);
    }
  }

  if (sessionLoading) {
    return <section className="page"><h1>{t('page.providers.title')}</h1><PageStateFrame phase="loading" /></section>;
  }
  if (!session) {
    return (
      <section className="page">
        <h1>{t('page.providers.title')}</h1>
        <PageStateFrame phase="empty">
          <p>{t('config.signInRequired')}</p>
          <Link className="button-primary" to="/login">{t('nav.login')}</Link>
        </PageStateFrame>
      </section>
    );
  }
  if (!canRead) {
    return <section className="page"><h1>{t('page.providers.title')}</h1><PageStateFrame phase="error" errorDetail={t('config.error.permission')} /></section>;
  }
  if (activeQuery.isPending || catalogQuery.isPending || scopesQuery.isPending || (draftId !== '' && draftQuery.isPending)) {
    return <section className="page"><h1>{t('page.providers.title')}</h1><PageStateFrame phase="loading" /></section>;
  }
  if (activeQuery.error) {
    return <section className="page"><h1>{t('page.providers.title')}</h1><PageStateFrame phase="error" errorDetail={errorMessage(activeQuery.error)} onRetry={() => void activeQuery.refetch()} /></section>;
  }
  if (catalogQuery.error) {
    return <section className="page"><h1>{t('page.providers.title')}</h1><PageStateFrame phase="error" errorDetail={errorMessage(catalogQuery.error)} onRetry={() => void catalogQuery.refetch()} /></section>;
  }
  if (scopesQuery.error) {
    return <section className="page"><h1>{t('page.providers.title')}</h1><PageStateFrame phase="error" errorDetail={errorMessage(scopesQuery.error)} onRetry={() => void scopesQuery.refetch()} /></section>;
  }
  if (draftId && draftQuery.error) {
    return (
      <section className="page">
        <h1>{t('page.providers.title')}</h1>
        <PageStateFrame phase="error" errorDetail={t('config.draft.unavailable')} onRetry={() => void draftQuery.refetch()} />
      </section>
    );
  }

  return (
    <section className="page">
      <header className="page-header">
        <div>
          <h1>{t('page.providers.title')}</h1>
          <p className="config-subtitle">{t('config.description')}</p>
        </div>
        {!draft || !editorOpen ? (
          <button ref={editorToggleButton} type="button" className="button-primary" disabled={busy !== null || (!draft && !canConfigure)} onClick={() => draft ? setEditorOpen(true) : void startDraft()}>
            {draft ? t('config.draft.resume') : busy === 'create' ? t('config.draft.creating') : active ? t('config.draft.editActive') : t('config.draft.start')}
          </button>
        ) : null}
      </header>

      {pageError ? <p className="config-alert" role="alert">{pageError}</p> : null}
      {draft && canConfigure && ['editing', 'validated'].includes(draft.status) ? <div className="config-actions">
        <button type="button" className="button-secondary" disabled={busy !== null || !parsed.document} onClick={async () => { const stored = await saveDraft(); if (stored) navigate(`/resources?draft=${encodeURIComponent(stored.draftId)}&view=legacy`); }}>{t('resourceDraft.continue')}</button>
      </div> : !draft && session && rememberedDraft(session.namespace, session.principalId) ? <p><Link className="button-secondary" to={`/resources?draft=${encodeURIComponent(rememberedDraft(session.namespace, session.principalId))}`}>{t('resourceDraft.resume')}</Link></p> : null}


      {!draft || !editorOpen ? (
        <>
          {!active ? (
            <div className="config-state">
              <h2>{t('config.empty.title')}</h2>
              <p>{t('config.empty.description')}</p>
              {!canConfigure ? <p>{t('config.error.permission')}</p> : null}
            </div>
          ) : (
            <div className="config-state">
              <div className="config-state-heading">
                <h2>{t('config.active.title')}</h2>
                <span className="config-status config-status-active">{t('config.status.active')}</span>
              </div>
              <p>{t('config.active.revision', { revision: active.revision.slice(0, 12), version: String(active.version) })}</p>
              <ul className="config-counts">
                {collectionKeys.map(([key, label]) => (
                  <li key={key}><span>{t(`config.count.${label}`)}</span><strong>{active.document[key].length}</strong></li>
                ))}
              </ul>
              {!canConfigure ? <p>{t('config.readOnly')}</p> : null}
            </div>
          )}
          {draft && !editorOpen ? (
            <div className="config-state">
              <div className="config-state-heading">
                <h2>{t('config.draft.savedTitle')}</h2>
                <span className={`config-status config-status-${draft.status}`}>{t(`config.status.${draft.status}`)}</span>
              </div>
              <p>{t('config.draft.savedSummary', { id: draft.draftId, version: String(draft.version) })}</p>
              <p>{t('config.draft.bookmarkHint')}</p>
            </div>
          ) : null}
          {scopesQuery.data?.fixture === true ? <p className="config-note">{t('config.localtestNotice')}</p> : null}
        </>
      ) : (
        <>
          <div className="config-workspace-meta">
            <span>{t('config.draft.id', { id: draft.draftId })}</span>
            <span>{t('config.draft.version', { version: String(draft.version) })}</span>
            <span className={`config-status config-status-${draft.status}`}>{t(`config.status.${draft.status}`)}</span>
            {dirty ? <span role="status">{t('config.draft.unsaved')}</span> : null}
          </div>
          {canConfigure && selectedProvider ? (
            <>
            {scopesQuery.data?.fixture === true ? <p className="config-note">{t('config.localtestNotice')}</p> : null}
            <section className="config-state" aria-labelledby="config-account-title">
              <h2 id="config-account-title">{t('config.account.title')}</h2>
              <p>{t('config.account.help')}</p>
              <div className="config-account-form">
                <label>
                  {t('config.account.provider')}
                  <select value={selectedProvider.typeId} onChange={(event) => { setSelectedProviderId(event.currentTarget.value); setAccountExtensionValues({}); setAccountScopeId(''); setAccountIdInput(''); }}>
                    {providers.map((provider) => <option key={provider.typeId} value={provider.typeId}>{providerLabel(provider)}</option>)}
                  </select>
                </label>
                <label>
                  {t('config.account.name')}
                  <input value={accountName} onChange={(event) => setAccountName(event.currentTarget.value)} maxLength={120} />
                </label>
                {scopesQuery.data?.fixture === true && fixtureScopes.some((item) => item.providerTypeId === selectedProvider.typeId) ? <label>
                  {t('config.account.fixtureScope')}
                  <select value={accountScopeId} onChange={(event) => setAccountScopeId(event.currentTarget.value)}>
                    <option value="">{t('config.account.optional')}</option>
                    {fixtureScopes.filter((scope) => scope.kind === 'account' && scope.providerTypeId === selectedProvider.typeId && !parsed.document?.accounts.some((account) => account.accountId === scope.scopeId)).map((scope) => <option key={scope.scopeId} value={scope.scopeId}>{scope.scopeId} · {t('config.fixtureLabel')}</option>)}
                  </select>
                </label> : <>
                  {catalogScopes.some((scope) => scope.kind === 'account' && scope.providerTypeId === selectedProvider.typeId) ? <label>
                    {t('config.account.catalogScope')}
                    <select value={accountScopeId} onChange={(event) => setAccountScopeId(event.currentTarget.value)}>
                      <option value="">{t('config.account.optional')}</option>
                      {catalogScopes.filter((scope) => scope.kind === 'account' && scope.providerTypeId === selectedProvider.typeId && !parsed.document?.accounts.some((account) => account.accountId === scope.scopeId)).map((scope) => <option key={scope.scopeId} value={scope.scopeId}>{scope.scopeId}</option>)}
                    </select>
                  </label> : null}
                  <label>
                    {t('config.account.id')}
                    <input value={accountIdInput} onChange={(event) => setAccountIdInput(event.currentTarget.value)} maxLength={64} autoComplete="off" />
                    <small>{t('config.account.idHelp')}</small>
                  </label>
                </>}
                {accountFields.map((field) => (
                  <label key={field.name}>
                    {field.name}
                    {field.enumValues.length > 0 ? (
                      <select value={accountExtensionValues[field.name] ?? ''} onChange={(event) => { const value = event.currentTarget.value; setAccountExtensionValues((current) => ({ ...current, [field.name]: value })); }}>
                        <option value="">{t('config.account.optional')}</option>
                        {field.enumValues.map((value) => <option key={value} value={value}>{value}</option>)}
                      </select>
                    ) : field.type === 'boolean' ? (
                      <select value={accountExtensionValues[field.name] ?? ''} onChange={(event) => { const value = event.currentTarget.value; setAccountExtensionValues((current) => ({ ...current, [field.name]: value })); }}>
                        <option value="">{t('config.account.optional')}</option><option value="true">true</option><option value="false">false</option>
                      </select>
                    ) : (
                      <input type={field.type === 'integer' || field.type === 'number' ? 'number' : 'text'} step={field.type === 'integer' ? 1 : 'any'} value={accountExtensionValues[field.name] ?? ''} onChange={(event) => { const value = event.currentTarget.value; setAccountExtensionValues((current) => ({ ...current, [field.name]: value })); }} />
                    )}
                  </label>
                ))}
                <button type="button" className="button-secondary" disabled={!canConfigure || !parsed.document || (scopesQuery.data?.fixture === true ? !accountScopeId : !accountScopeId && !accountIdInput.trim()) || busy !== null} onClick={addAccount}>{t('config.account.add')}</button>
              </div>
            </section>
            </>
          ) : null}
          {canConfigure && selectedProvider && parsed.document?.accounts.some((item) => item.providerTypeId === selectedProvider.typeId) ? (
            <section className="config-state" aria-labelledby="config-binding-title">
              <h2 id="config-binding-title">{t('config.binding.title')}</h2>
              <p>{t('config.binding.help')}</p>
              <div className="config-account-form">
                <label>
                  {t('config.binding.account')}
                  <select value={selectedAccountId} onChange={(event) => setSelectedAccountId(event.currentTarget.value)}>
                    <option value="">{t('config.account.optional')}</option>
                    {parsed.document.accounts.filter((item) => item.providerTypeId === selectedProvider.typeId).map((item) => <option key={item.accountId} value={item.accountId}>{item.displayName}</option>)}
                  </select>
                </label>
                <label>
                  {t('config.binding.domain')}
                  <select value={bindingDomain} onChange={(event) => { setBindingDomain(event.currentTarget.value); setBindingProfile(''); }}>
                    <option value="">{t('config.account.optional')}</option>
                    {selectedProvider.supportedDomains.map((domain) => <option key={domain} value={domain}>{domain}</option>)}
                  </select>
                </label>
                <label>
                  {t('config.binding.profile')}
                  <select value={bindingProfile} onChange={(event) => setBindingProfile(event.currentTarget.value)}>
                    <option value="">{t('config.account.optional')}</option>
                    {selectedProvider.profiles.filter((profile) => profile.domain === bindingDomain).map((profile) => <option key={profile.profileId} value={profile.profileId}>{profile.profileId}</option>)}
                  </select>
                </label>
                <label>
                  {t('config.binding.endpoint')}
                  <input type="url" value={bindingEndpoint} onChange={(event) => setBindingEndpoint(event.currentTarget.value)} placeholder="https://api.example.com" />
                </label>
                <label>
                  {t('config.binding.credential')}
                  <input type="password" autoComplete="new-password" value={credentialValue} onChange={(event) => { setCredentialValue(event.currentTarget.value); secretCommandId.current = ''; }} />
                </label>
                <button type="button" className="button-secondary" disabled={!selectedAccountId || !bindingDomain || !bindingProfile || !bindingEndpoint || !credentialValue || busy !== null || !parsed.document} onClick={() => void addBinding()}>
                  {busy === 'save' ? t('config.binding.saving') : t('config.binding.add')}
                </button>
              </div>
            </section>
          ) : null}
          {canConfigure && selectedProvider && parsed.document?.bindings.some((binding) => parsed.document?.accounts.some((item) => item.accountId === binding.accountId && item.providerTypeId === selectedProvider.typeId)) ? (
            <section className="config-state" aria-labelledby="config-rotation-title">
              <h2 id="config-rotation-title">{t('config.binding.rotateTitle')}</h2>
              <p>{t('config.binding.rotateHelp')}</p>
              <div className="config-account-form">
                <label>
                  {t('config.binding.selectBindingLabel')}
                  <select value={selectedBindingId} onChange={(event) => selectBinding(event.currentTarget.value)}>
                    <option value="">{t('config.account.optional')}</option>
                    {parsed.document.bindings.filter((binding) => parsed.document?.accounts.some((item) => item.accountId === binding.accountId && item.providerTypeId === selectedProvider.typeId)).map((binding) => (
                      <option key={binding.bindingId} value={binding.bindingId}>{binding.domain} · {binding.endpoint}</option>
                    ))}
                  </select>
                </label>
                <label>
                  {t('config.binding.newCredential')}
                  <input type="password" autoComplete="new-password" value={rotationValue} onChange={(event) => { setRotationValue(event.currentTarget.value); secretCommandId.current = ''; setRotationInfo(''); }} />
                </label>
                <button type="button" className="button-secondary" disabled={!selectedBindingId || !rotationValue || busy !== null} onClick={() => void rotateBindingCredential()}>
                  {busy === 'save' ? t('config.binding.rotating') : t('config.binding.rotate')}
                </button>
              </div>
              {rotationInfo ? <p className="config-success" role="status">{rotationInfo}</p> : null}
            </section>
          ) : null}
          {canConfigure && selectedProvider?.typeId === 'deepseek-standard' && parsed.document?.bindings.some((binding) => parsed.document?.accounts.some((item) => item.accountId === binding.accountId && item.providerTypeId === 'deepseek-standard')) ? (() => {
            const bindingOptions = parsed.document!.bindings.filter((binding) => parsed.document!.accounts.some((item) => item.accountId === binding.accountId && item.providerTypeId === 'deepseek-standard'));
            const selectedBinding = bindingOptions.find((binding) => binding.bindingId === balanceBindingId);
            const observation: DeepSeekBalanceObservationView | undefined = balanceQuery.data;
            return <section className="config-state" aria-labelledby="config-balance-title">
              <h2 id="config-balance-title">{t('config.balance.title')}</h2>
              <p>{t('config.balance.help')}</p>
              <div className="config-account-form">
                <label>
                  {t('config.balance.binding')}
                  <select value={balanceBindingId} onChange={(event) => selectBalanceBinding(event.currentTarget.value)}>
                    <option value="">{t('config.account.optional')}</option>
                    {bindingOptions.map((binding) => {
                      const account = parsed.document!.accounts.find((item) => item.accountId === binding.accountId);
                      return <option key={binding.bindingId} value={binding.bindingId}>{account?.displayName ?? binding.accountId} · {binding.domain}</option>;
                    })}
                  </select>
                </label>
                <button type="button" className="button-secondary" disabled={!selectedBinding || balanceSubmitting} onClick={() => void checkDeepSeekBalance()}>
                  {balanceSubmitting ? t('config.balance.checking') : observation ? t('config.balance.refresh') : t('config.balance.check')}
                </button>
                {balanceSubmitting ? <button type="button" className="button-secondary" onClick={cancelDeepSeekBalance}>{t('config.balance.cancel')}</button> : null}
              </div>
              {!selectedBinding ? <p className="config-help">{t('config.balance.selectFirst')}</p> : null}
              {balanceSubmitting || balanceQuery.isLoading ? <p className="config-note" role="status">{t('config.balance.loading')}</p> : null}
              {balanceActionError ? <p className="config-error" role="alert">{t(balanceActionError)}</p> : null}
              {balanceQuery.isError ? <div className="config-error" role="alert"><p>{t(balanceErrorKey(balanceQuery.error))}</p><button type="button" className="button-secondary" onClick={() => void balanceQuery.refetch()}>{t('config.balance.retryRead')}</button></div> : null}
              {observation ? <div className="config-balance-result" aria-live="polite">
                <p className="config-success" role="status">{t(observation.isAvailable ? 'config.balance.available' : 'config.balance.unavailable')}</p>
                <p className="config-note">{t('config.balance.observedAt', { time: formatObservationTime(observation.observedAt) })}</p>
                <dl>
                  {observation.balances.map((balance) => <div key={balance.currency}>
                    <dt>{balance.currency}</dt>
                    <dd>{t('config.balance.total', { amount: formatBalanceCents(balance.currency, balance.totalCents) })}</dd>
                    <dd>{t('config.balance.granted', { amount: formatBalanceCents(balance.currency, balance.grantedCents) })}</dd>
                    <dd>{t('config.balance.toppedUp', { amount: formatBalanceCents(balance.currency, balance.toppedUpCents) })}</dd>
                  </div>)}
                </dl>
                <p className="config-note">{t('config.balance.caveat')}</p>
              </div> : null}
            </section>;
          })() : null}
          {canConfigure && scopesQuery.data?.fixture === true && selectedProvider && parsed.document?.bindings.some((binding) => parsed.document?.accounts.some((item) => item.accountId === binding.accountId && item.providerTypeId === selectedProvider.typeId)) ? (
            <section className="config-state" aria-labelledby="config-resource-title">
              <h2 id="config-resource-title">{t('config.resource.title')}</h2>
              <p>{t('config.resource.help')}</p>
              <div className="config-account-form">
                <label>
                  {t('config.binding.selectBindingLabel')}
                  <select value={selectedBindingId} onChange={(event) => { selectBinding(event.currentTarget.value); setResourceSelections({}); }}>
                    <option value="">{t('config.account.optional')}</option>
                    {parsed.document.bindings.filter((binding) => parsed.document?.accounts.some((item) => item.accountId === binding.accountId && item.providerTypeId === selectedProvider.typeId)).map((binding) => <option key={binding.bindingId} value={binding.bindingId}>{binding.domain} · {binding.endpoint}</option>)}
                  </select>
                </label>
                {(() => {
                  const binding = parsed.document?.bindings.find((item) => item.bindingId === selectedBindingId);
                  const account = binding && parsed.document?.accounts.find((item) => item.accountId === binding.accountId);
                  const profile = selectedProvider.profiles.find((item) => item.profileId === binding?.capabilityProfiles[0] && item.domain === binding.domain);
                  const facts = account ? factsForAccount(account.accountId) : [];
                  return profile?.requiredDimensions.map((dimension) => {
                    const key = `${binding?.bindingId}:${dimension.metricId}:${dimension.role}`;
                    const options = facts.filter((fact) => factMatchesDimension(fact, dimension));
                    return <label key={key}>{`${dimension.role} · ${dimension.metricId}`}
                      <select value={resourceSelections[key] ?? ''} onChange={(event) => { const value = event.currentTarget.value; setResourceSelections((current) => ({ ...current, [key]: value })); }}>
                        <option value="">{t('config.account.optional')}</option>
                        {options.map((fact) => <option key={fact.physicalKey} value={fact.physicalKey}>{fact.factId} · {fact.capacityBound} {fact.unit} · {t('config.fixtureLabel')}</option>)}
                      </select>
                    </label>;
                  });
                })()}
                <button type="button" className="button-secondary" disabled={!selectedBindingId || busy !== null} onClick={() => void addProfileResources()}>{busy === 'save' ? t('config.action.saving') : t('config.resource.add')}</button>
              </div>
            </section>
          ) : null}
          {canConfigure && selectedProvider && parsed.document?.bindings.some((binding) => parsed.document?.accounts.some((item) => item.accountId === binding.accountId && item.providerTypeId === selectedProvider.typeId)) ? (
            <section className="config-state" aria-labelledby="config-offer-title">
              <h2 id="config-offer-title">{t('config.offer.title')}</h2>
              <p>{t('config.offer.help')}</p>
              <div className="config-account-form">
                <label>
                  {t('config.binding.selectBindingLabel')}
                  <select value={selectedBindingId} onChange={(event) => selectBinding(event.currentTarget.value)}>
                    <option value="">{t('config.account.optional')}</option>
                    {parsed.document.bindings.filter((binding) => parsed.document?.accounts.some((item) => item.accountId === binding.accountId && item.providerTypeId === selectedProvider.typeId)).map((binding) => <option key={binding.bindingId} value={binding.bindingId}>{binding.domain} · {binding.endpoint}</option>)}
                  </select>
                </label>
                <label>
                  {t('config.pool.name')}
                  <input value={poolName} onChange={(event) => setPoolName(event.currentTarget.value)} maxLength={128} />
                </label>
                <button type="button" className="button-secondary" disabled={!selectedBindingId || !poolName.trim() || busy !== null} onClick={() => void addOfferAndPool()}>{busy === 'save' ? t('config.action.saving') : t('config.offer.add')}</button>
              </div>
            </section>
          ) : null}
          <p className="config-note">{t('config.editor.help')}</p>
          <label className="config-editor-label" htmlFor="config-document">{t('config.editor.label')}</label>
          <textarea
            id="config-document"
            className="config-editor"
            spellCheck={false}
            value={editorText}
            onChange={(event) => {
              setEditorText(event.currentTarget.value);
              setEditorError('');
            }}
            disabled={!canConfigure || busy !== null}
            aria-describedby="config-editor-description"
          />
          <p id="config-editor-description" className="config-help">{t('config.editor.description')}</p>

          {parsed.syntaxError ? <p className="config-alert" role="alert">{t('config.editor.invalidJson')}</p> : null}
          {editorError ? <p className="config-alert" role="alert">{editorError}</p> : null}
          {localValidation && !localValidation.ok ? (
            <div className="config-issues" role="status">
              <h2>{t('config.validation.localTitle', { count: String(localValidation.issues.length) })}</h2>
              <p>{t('config.validation.localHelp')}</p>
              <ul>{localValidation.issues.slice(0, 20).map((issue, index) => <li key={`${issue.path}-${index}`}><code>{issue.path}</code> — {t('config.validation.issue')}</li>)}</ul>
            </div>
          ) : null}
          {draft.fieldIssues.length > 0 ? (
            <div className="config-issues" role="alert">
              <h2>{t('config.validation.serverTitle', { count: String(draft.fieldIssues.length) })}</h2>
              <p>{t('config.validation.serverHelp')}</p>
              <ul>{draft.fieldIssues.slice(0, 50).map((issue, index) => <li key={`${issue.path}-${index}`}><code>{issue.path}</code> — {t('config.validation.issue')}</li>)}</ul>
            </div>
          ) : draft.status === 'validated' ? <p className="config-success" role="status">{t('config.validation.passed')}</p> : null}

          <div className="config-actions">
            <button type="button" className="button-primary" disabled={!canConfigure || busy !== null || parsed.document === undefined} onClick={() => void saveDraft()}>
              {busy === 'save' ? t('config.action.saving') : t('config.action.save')}
            </button>
            <button type="button" className="button-primary" disabled={!canConfigure || busy !== null || parsed.document === undefined} onClick={() => void validateDraft()}>
              {busy === 'validate' || busy === 'save' ? t('config.action.checking') : t('config.action.validate')}
            </button>
            {draft.status === 'validated' ? (
              <button ref={activateButton} type="button" className="button-primary" disabled={!canConfigure || busy !== null || dirty} onClick={() => setShowActivationConfirm(true)}>
                {t('config.action.activate')}
              </button>
            ) : null}
            <button ref={closeButton} type="button" className="button-secondary" disabled={busy !== null} onClick={requestCloseEditor}>
              {t('config.action.close')}
            </button>
          </div>
          {!canConfigure ? <p>{t('config.readOnly')}</p> : null}
        </>
      )}

      {showActivationConfirm ? (
        <div className="config-confirm-backdrop">
          <section className="config-confirm" role="alertdialog" aria-modal="true" aria-labelledby="config-confirm-title" aria-describedby="config-confirm-description">
            <h2 id="config-confirm-title">{t('config.activation.confirmTitle')}</h2>
            <p id="config-confirm-description">{t('config.activation.confirmBody', { namespace: session.namespace })}</p>
            <div className="config-actions">
              <button ref={confirmButton} type="button" className="button-primary" disabled={busy !== null} onClick={() => void activateDraft()}>
                {busy === 'activate' ? t('config.activation.activating') : t('config.activation.confirm')}
              </button>
              <button ref={keepEditingButton} type="button" className="button-secondary" disabled={busy !== null} onClick={() => setShowActivationConfirm(false)}>
                {t('config.activation.keepEditing')}
              </button>
            </div>
          </section>
        </div>
      ) : null}
      {showCloseConfirm ? (
        <div className="config-confirm-backdrop">
          <section className="config-confirm" role="alertdialog" aria-modal="true" aria-labelledby="config-close-title" aria-describedby="config-close-description">
            <h2 id="config-close-title">{t('config.closeConfirm.title')}</h2>
            <p id="config-close-description">{t('config.closeConfirm.body')}</p>
            {pageError ? <p role="alert" className="config-alert">{pageError}</p> : null}
            {editorError ? <p role="alert" className="config-alert">{editorError}</p> : null}
            <div className="config-actions">
              <button ref={saveAndCloseButton} type="button" className="button-primary" disabled={busy !== null} onClick={() => void saveAndCloseEditor()}>
                {busy === 'save' ? t('config.action.saving') : t('config.closeConfirm.save')}
              </button>
              <button ref={discardChangesButton} type="button" className="button-secondary" disabled={busy !== null} onClick={discardAndCloseEditor}>
                {t('config.closeConfirm.discard')}
              </button>
              <button ref={keepCloseEditingButton} type="button" className="button-secondary" disabled={busy !== null} onClick={() => setShowCloseConfirm(false)}>
                {t('config.closeConfirm.keepEditing')}
              </button>
            </div>
          </section>
        </div>
      ) : null}
    </section>
  );
}
