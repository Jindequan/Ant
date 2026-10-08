import { apiJson } from './client';
import type { components } from './schema';
export type SupplyDocument = components['schemas']['SupplyDocument'];
export type SupplyPackage = components['schemas']['SupplyPackage'];
export type SupplyResource = components['schemas']['SupplyResource'];
export type SupplyPool = components['schemas']['SupplyPool'];
export type InventoryView = components['schemas']['InventoryView'];
export type InventoryInput = components['schemas']['InventoryInput'];
export const emptyInventory = (): SupplyDocument => ({ schemaVersion: 1, accounts: [], packages: [], resources: [], pools: [] });
export const inventoryKey = (namespace?: string, principal?: string) => ['supply-inventory', namespace, principal] as const;
export function readInventory(): Promise<InventoryView> { return apiJson<InventoryView>('/v1/inventory'); }
export function saveInventory(input: InventoryInput, csrfToken: string): Promise<InventoryView> {
  return apiJson<InventoryView>('/v1/inventory', { method: 'PUT', body: input, csrfToken, headers: { 'If-Match': `"${input.expectedVersion}"` } });
}
export function exactSupplyDecimal(value: string): string | null {
  if (!/^(0|[1-9][0-9]{0,37})(\.[0-9]{1,18})?$/.test(value)) return null;
  return value.includes('.') ? value.replace(/0+$/, '').replace(/\.$/, '') : value;
}
