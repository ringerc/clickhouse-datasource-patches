import { DataSourceJsonData, KeyValue } from '@grafana/data';
import otel, { defaultLogsTable, defaultTraceTable } from 'otel';
import { TimeUnit } from './queryBuilder';

export type SignalType = 'logs' | 'traces';

/**
 * Configuration mode controls the datasource UI layout:
 * - 'classic': Full access to all databases/tables.
 * - 'single-table': Focused on one table. The user picks a signal type
 *   and configures the schema inline.
 */
export type ConfigMode = 'classic' | 'single-table';

export interface CHConfig extends DataSourceJsonData {
  /**
   * The version of the plugin this config was saved with
   */
  version: string;

  host: string;
  port: number;
  protocol: Protocol;
  secure?: boolean;
  path?: string;

  tlsSkipVerify?: boolean;
  tlsAuth?: boolean;
  tlsAuthWithCACert?: boolean;

  username: string;

  defaultDatabase?: string;
  defaultTable?: string;

  connMaxLifetime?: string;
  dialTimeout?: string;
  maxIdleConns?: string;
  maxOpenConns?: string;
  queryTimeout?: string;
  validateSql?: boolean;

  logs?: CHLogsConfig;
  traces?: CHTracesConfig;

  aliasTables?: AliasTableEntry[];

  httpHeaders?: CHHttpHeader[];
  forwardGrafanaHeaders?: boolean;
  oauthPassThru?: boolean;
  oauthPassThruAllowFallback?: boolean;

  customSettings?: CHCustomSetting[];
  enableSecureSocksProxy?: boolean;
  enableRowLimit?: boolean;
  /** Forces readonly=1 on every query, blocking INSERT/DDL. Auto-enabled when any customSetting has enforced=true. */
  enforceReadOnly?: boolean;

  /**
   * Optional expected row count passed to sqlds as DriverSettings.RowCapacityHint.
   * sqlds pre-allocates each frame's fields to this value before scanning, avoiding
   * per-column slice growth on large results. Applied to every query, so leave
   * unset (0, disabled) unless queries reliably return a similar, large number
   * of rows. A value larger than the typical result wastes memory.
   */
  rowCapacityHint?: string;

  hideTableNameInAdhocFilters?: boolean;

  /**
   * Controls the Map-column key discovery probe that populates the filter-key
   * dropdown for `Map(...)` columns. The probe issues
   * `SELECT DISTINCT arrayJoin(mapKeys(col)) FROM db.table LIMIT 1000` and can be
   * expensive on large tables when the map has high key cardinality. Defaults
   * to true to preserve existing UX; operators on large OTel logs/traces tables
   * may want to disable it. See issue #1843.
   */
  enableMapKeysDiscovery?: boolean;

  pdcInjected?: boolean;

  /**
   * Configuration mode: 'classic' (all databases) or 'single-table' (focused).
   * Defaults to 'classic' when unset.
   */
  configMode?: ConfigMode;

  /**
   * Signal type for single-table mode. Declares what the configured table contains.
   */
  signalType?: SignalType;
}

interface CHSecureConfigProperties {
  password?: string;

  tlsCACert?: string;
  tlsClientCert?: string;
  tlsClientKey?: string;
}
export type CHSecureConfig = CHSecureConfigProperties | KeyValue<string>;

export interface CHHttpHeader {
  name: string;
  value: string;
  secure: boolean;
}

export interface CHCustomSetting {
  setting: string;
  value: string;
  /** When true, the setting is sent alongside readonly=1 on every query so the user's SQL cannot override it. */
  enforced?: boolean;
  /**
   * Where the effective value comes from at query time.
   *  - undefined / "static": use `value` verbatim (default; the only mode for non-enforced rows).
   *  - "header": read from the named HTTP header on each request; `headerName` is required and `value` must be empty.
   *  - "jwt": read a claim from a JWT in the named HTTP header on each request; `jwtClaimPath` is a JSON-key segment array and `value` must be empty.
   * Only meaningful when `enforced === true`.
   */
  source?: CHCustomSettingSource;
  /** Required when `source === "header"`. Canonicalised server-side. */
  headerName?: string;
  /** Behaviour when a dynamic source produces no value. Defaults to "reject". */
  onMissing?: CHCustomSettingOnMissing;

  // JWT-source fields — only meaningful when source === "jwt".
  /** HTTP header carrying the JWT. Defaults to "X-Grafana-Id". Canonicalised server-side. */
  jwtHeaderName?: string;
  /** Claim key path; each element is one literal JSON key, e.g. ["https://myapp.example.com/roles"] or ["realm_access", "roles"]. */
  jwtClaimPath?: string[];
  /** Separator used to join array claims into a single string. Defaults to ",". */
  jwtClaimJoin?: string;
  /** Signature verification mode. Defaults to "none" (trust forwarded token). */
  jwtVerify?: CHCustomSettingJWTVerify;
  /** JWKS endpoint URL (https only). Required when jwtVerify === "jwks". */
  jwtJwksUrl?: string;
  /** Optional expected issuer (iss claim). Only checked when jwtVerify === "jwks". */
  jwtIssuer?: string;
  /** Optional expected audience (aud claim). Only checked when jwtVerify === "jwks". */
  jwtAudience?: string;
}

export type CHCustomSettingSource = 'static' | 'header' | 'jwt';
export type CHCustomSettingOnMissing = 'reject' | 'empty';
export type CHCustomSettingJWTVerify = 'none' | 'jwks';

export interface CHLogsConfig {
  defaultDatabase?: string;
  defaultTable?: string;

  otelEnabled?: boolean;
  otelVersion?: string;

  filterTimeColumn?: string;
  timeColumn?: string;
  levelColumn?: string;
  messageColumn?: string;

  selectContextColumns?: boolean;
  contextColumns?: string[];
  showLogLinks?: boolean;
}

export interface CHTracesConfig {
  defaultDatabase?: string;
  defaultTable?: string;

  otelEnabled?: boolean;
  otelVersion?: string;

  traceIdColumn?: string;
  spanIdColumn?: string;
  operationNameColumn?: string;
  parentSpanIdColumn?: string;
  serviceNameColumn?: string;
  durationColumn?: string;
  durationUnit?: string;
  startTimeColumn?: string;
  tagsColumn?: string;
  serviceTagsColumn?: string;
  kindColumn?: string;
  statusCodeColumn?: string;
  statusMessageColumn?: string;
  stateColumn?: string;
  instrumentationLibraryNameColumn?: string;
  instrumentationLibraryVersionColumn?: string;

  flattenNested?: boolean;
  traceEventsColumnPrefix?: string;
  traceLinksColumnPrefix?: string;
  showTraceLinks?: boolean;

  /**
   * Suffix appended to the traces table name to locate a companion trace-timestamp
   * index table (e.g. `<table>_trace_id_ts`). When such a table exists, trace ID
   * queries run a two-step lookup that narrows the main query's time range,
   * avoiding a full scan. Defaults to `_trace_id_ts` (the OTel convention).
   */
  traceTimestampTableSuffix?: string;
}

export interface AliasTableEntry {
  targetDatabase: string;
  targetTable: string;
  aliasDatabase: string;
  aliasTable: string;
}

export enum Protocol {
  Native = 'native',
  Http = 'http',
}

export const defaultCHAdditionalSettingsConfig: Partial<CHConfig> = {
  logs: {
    defaultTable: defaultLogsTable,
    otelVersion: otel.getLatestVersion().version,
    selectContextColumns: true,
    contextColumns: [],
  },
  traces: {
    defaultTable: defaultTraceTable,
    otelVersion: otel.getLatestVersion().version,
    durationUnit: TimeUnit.Nanoseconds,
  },
};
