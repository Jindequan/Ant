/** Type surface of scripts/contract-tools.mjs (test-side contract tooling). */
export declare function loadOpenApiDocument(): unknown;
export declare function loadOpenApiSchemas(): Record<string, object>;
export declare function canonicalQuantitySchema(): object;
export declare function parseJsonStrict(text: string): unknown;
export declare type Verdict = 'accept' | 'reject';
export declare function strictDecodeConfigDocument(text: string): Verdict;
export declare function canonicalJson(value: unknown): string;
