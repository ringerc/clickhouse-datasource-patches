import { ConfigSubSection } from 'components/experimental/ConfigSection';
import allLabelsV2 from './labelsV2';
import sharedLabels from '../../labels';
import { selectors } from '../../selectors';
import React, { ChangeEvent, useMemo, useState } from 'react';
import {
  DataSourcePluginOptionsEditorProps,
  onUpdateDatasourceJsonDataOption,
  onUpdateDatasourceJsonDataOptionChecked,
} from '@grafana/data';
import {
  AliasTableEntry,
  CHConfig,
  CHCustomSetting,
  CHCustomSettingJWTVerify,
  CHCustomSettingOnMissing,
  CHCustomSettingSource,
  CHLogsConfig,
  CHSecureConfig,
  CHTracesConfig,
  defaultCHAdditionalSettingsConfig,
} from 'types/config';
import { AliasTableConfig } from 'components/configEditor/AliasTableConfig';
import { DefaultDatabaseTableConfig } from 'components/configEditor/DefaultDatabaseTableConfig';
import { LogsConfig } from 'components/configEditor/LogsConfig';
import { QuerySettingsConfig } from 'components/configEditor/QuerySettingsConfig';
import { TracesConfig } from 'components/configEditor/TracesConfig';
import { config } from '@grafana/runtime';
import { TimeUnit } from 'types/queryBuilder';
import { useConfigDefaults } from 'views/CHConfigEditorHooks';
import { isVersionGtOrEq as versionGte } from 'utils/version';
import {
  Field,
  Divider,
  Stack,
  Input,
  Button,
  Switch,
  Box,
  CollapsableSection,
  Text,
  Badge,
  useStyles2,
  Alert,
  Checkbox,
  IconButton,
  Select,
  Tooltip,
} from '@grafana/ui';
import { CONFIG_SECTION_HEADERS, CONTAINER_MIN_WIDTH } from './constants';
import {
  trackClickhouseConfigV2CustomSettingClicked,
  trackClickhouseConfigV2CustomSettingEnforcedToggle,
  trackClickhouseConfigV2CustomSettingRowExpanded,
  trackClickhouseConfigV2CustomSettingSourceChanged,
  trackClickhouseConfigV2DefaultDbInput,
  trackClickhouseConfigV2DefaultTableInput,
  trackClickhouseConfigV2EnableRowLimitToggle,
  trackClickhouseConfigV2EnforceReadOnlyToggle,
  trackClickhouseConfigV2LogsConfig,
  trackClickhouseConfigV2QuerySettings,
  trackClickhouseConfigV2TracesConfig,
} from './tracking';
import { css } from '@emotion/css';
import { isEqual } from 'lodash';

export interface Props extends DataSourcePluginOptionsEditorProps<CHConfig, CHSecureConfig> {}

export const AdditionalSettingsSection = (props: Props) => {
  const { options, onOptionsChange } = props;
  const { jsonData } = options;
  const labels = allLabelsV2.components.Config.ConfigEditor;
  const csLabels = sharedLabels.components.Config.ConfigEditor.customSettings;
  const styles = useStyles2(getStyles);
  const isSingleTableMode =
    (jsonData.configMode || (jsonData.signalType ? 'single-table' : 'classic')) === 'single-table';

  useConfigDefaults(options, onOptionsChange);

  const [customSettings, setCustomSettings] = useState(jsonData.customSettings || []);

  const anyEnforced = customSettings.some((s) => s.enforced);
  const anyHeaderSource = customSettings.some((s) => s.enforced && s.source === 'header');
  const anyJWTSource = customSettings.some((s) => s.enforced && s.source === 'jwt');

  const [expandedRows, setExpandedRows] = useState<Set<number>>(() => {
    const initial = new Set<number>();
    (jsonData.customSettings || []).forEach((s, i) => {
      if (s.enforced && s.source && s.source !== 'static') {
        initial.add(i);
      }
    });
    return initial;
  });

  const toggleRowExpanded = (i: number) => {
    setExpandedRows((prev) => {
      const next = new Set(prev);
      const nowExpanded = !next.has(i);
      if (nowExpanded) {
        next.add(i);
      } else {
        next.delete(i);
      }
      trackClickhouseConfigV2CustomSettingRowExpanded({ expanded: nowExpanded });
      return next;
    });
  };

  const onLogsConfigChange = (key: keyof CHLogsConfig, value: string | boolean | string[]) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...options.jsonData,
        logs: {
          ...(options.jsonData.logs || {}),
          [key]: value,
        },
      },
    });
  };

  const onUpdateLogsConfig = (key: keyof CHLogsConfig, value: string | boolean | string[]) => {
    trackClickhouseConfigV2LogsConfig({ [key]: value });
    onLogsConfigChange(key, value);
  };

  const onTracesConfigChange = (key: keyof CHTracesConfig, value: string | boolean) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...options.jsonData,
        traces: {
          ...options.jsonData.traces,
          durationUnit: options.jsonData.traces?.durationUnit || TimeUnit.Nanoseconds,
          [key]: value,
        },
      },
    });
  };

  const onUpdateTracesConfig = (key: keyof CHTracesConfig, value: string | boolean) => {
    trackClickhouseConfigV2TracesConfig({ [key]: value });
    onTracesConfigChange(key, value);
  };

  const onAliasTableConfigChange = (aliasTables: AliasTableEntry[]) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...options.jsonData,
        aliasTables,
      },
    });
  };

  const onCustomSettingsChange = (customSettings: CHCustomSetting[]) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...options.jsonData,
        customSettings: customSettings.filter(
          (s) =>
            !!s.setting &&
            (!!s.value ||
              (s.enforced && s.source === 'header' && !!s.headerName) ||
              (s.enforced &&
                s.source === 'jwt' &&
                Array.isArray(s.jwtClaimPath) &&
                s.jwtClaimPath.length > 0 &&
                !!s.jwtClaimPath[0]))
        ),
      },
    });
  };
  const shouldBeOpen = useMemo(() => {
    return (
      (!isSingleTableMode &&
        (() => {
          const defaultLogs = defaultCHAdditionalSettingsConfig.logs;
          const defaultTraces = defaultCHAdditionalSettingsConfig.traces;
          const logs = jsonData.logs ?? defaultLogs;
          const traces = jsonData.traces ?? defaultTraces;

          return (
            !!jsonData.defaultDatabase ||
            !!jsonData.defaultTable ||
            !!jsonData.connMaxLifetime ||
            !!jsonData.dialTimeout ||
            !!jsonData.maxIdleConns ||
            !!jsonData.maxOpenConns ||
            !!jsonData.queryTimeout ||
            !!jsonData.rowCapacityHint ||
            !!jsonData.validateSql ||
            jsonData.enableMapKeysDiscovery === false ||
            !isEqual(logs, defaultLogs) ||
            !isEqual(traces, defaultTraces)
          );
        })()) ||
      (jsonData.aliasTables?.length ?? 0) > 0 ||
      !!jsonData.enableRowLimit ||
      !!jsonData.enableSecureSocksProxy ||
      customSettings.length > 0
    );
  }, [jsonData, isSingleTableMode, customSettings]);

  return (
    <Box
      borderStyle="solid"
      borderColor="weak"
      padding={2}
      marginBottom={4}
      id={`${CONFIG_SECTION_HEADERS[4].id}`}
      minWidth={CONTAINER_MIN_WIDTH}
    >
      <CollapsableSection
        label={
          <>
            <Text variant="h3">{CONFIG_SECTION_HEADERS[4].label}</Text>
            <Badge text="optional" color="darkgrey" className={styles.badge} />
          </>
        }
        isOpen={!!shouldBeOpen}
      >
        {!isSingleTableMode && (
          <>
            <DefaultDatabaseTableConfig
              defaultDatabase={jsonData.defaultDatabase}
              defaultTable={jsonData.defaultTable}
              onDefaultDatabaseChange={(e) => {
                trackClickhouseConfigV2DefaultDbInput();
                onUpdateDatasourceJsonDataOption(props, 'defaultDatabase')(e);
              }}
              onDefaultTableChange={(e) => {
                trackClickhouseConfigV2DefaultTableInput();
                onUpdateDatasourceJsonDataOption(props, 'defaultTable')(e);
              }}
            />
            <Divider />
            <QuerySettingsConfig
              connMaxLifetime={jsonData.connMaxLifetime}
              dialTimeout={jsonData.dialTimeout}
              maxIdleConns={jsonData.maxIdleConns}
              maxOpenConns={jsonData.maxOpenConns}
              queryTimeout={jsonData.queryTimeout}
              rowCapacityHint={jsonData.rowCapacityHint}
              validateSql={jsonData.validateSql}
              enableMapKeysDiscovery={jsonData.enableMapKeysDiscovery}
              onDialTimeoutChange={(e) => {
                trackClickhouseConfigV2QuerySettings({ dialTimeout: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'dialTimeout')(e);
              }}
              onQueryTimeoutChange={(e) => {
                trackClickhouseConfigV2QuerySettings({ queryTimeout: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'queryTimeout')(e);
              }}
              onRowCapacityHintChange={(e) => {
                trackClickhouseConfigV2QuerySettings({ rowCapacityHint: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'rowCapacityHint')(e);
              }}
              onConnMaxLifetimeChange={(e) => {
                trackClickhouseConfigV2QuerySettings({ connMaxLifetime: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'connMaxLifetime')(e);
              }}
              onConnMaxIdleConnsChange={(e) => {
                trackClickhouseConfigV2QuerySettings({ maxIdleConns: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'maxIdleConns')(e);
              }}
              onConnMaxOpenConnsChange={(e) => {
                trackClickhouseConfigV2QuerySettings({ maxOpenConns: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'maxOpenConns')(e);
              }}
              onValidateSqlChange={(e) => {
                trackClickhouseConfigV2QuerySettings({ validateSql: e.currentTarget.checked });
                onUpdateDatasourceJsonDataOptionChecked(props, 'validateSql')(e);
              }}
              onEnableMapKeysDiscoveryChange={(e) => {
                trackClickhouseConfigV2QuerySettings({ enableMapKeysDiscovery: e.currentTarget.checked });
                onUpdateDatasourceJsonDataOptionChecked(props, 'enableMapKeysDiscovery')(e);
              }}
            />
            <Divider />
            <LogsConfig
              logsConfig={jsonData.logs}
              onDefaultDatabaseChange={(db) => onUpdateLogsConfig('defaultDatabase', db)}
              onDefaultTableChange={(table) => onUpdateLogsConfig('defaultTable', table)}
              onOtelEnabledChange={(v) => onUpdateLogsConfig('otelEnabled', v)}
              onOtelVersionChange={(v) => onUpdateLogsConfig('otelVersion', v)}
              onFilterTimeColumnChange={(c) => onUpdateLogsConfig('filterTimeColumn', c)}
              onTimeColumnChange={(c) => onUpdateLogsConfig('timeColumn', c)}
              onLevelColumnChange={(c) => onUpdateLogsConfig('levelColumn', c)}
              onMessageColumnChange={(c) => onUpdateLogsConfig('messageColumn', c)}
              onSelectContextColumnsChange={(c) => onUpdateLogsConfig('selectContextColumns', c)}
              onContextColumnsChange={(c) => onUpdateLogsConfig('contextColumns', c)}
              onShowLogLinksChange={(v) => onUpdateLogsConfig('showLogLinks', v)}
            />

            <Divider />
            <TracesConfig
              tracesConfig={jsonData.traces}
              onDefaultDatabaseChange={(db) => onUpdateTracesConfig('defaultDatabase', db)}
              onDefaultTableChange={(table) => onUpdateTracesConfig('defaultTable', table)}
              onOtelEnabledChange={(v) => onUpdateTracesConfig('otelEnabled', v)}
              onOtelVersionChange={(v) => onUpdateTracesConfig('otelVersion', v)}
              onTraceIdColumnChange={(c) => onUpdateTracesConfig('traceIdColumn', c)}
              onSpanIdColumnChange={(c) => onUpdateTracesConfig('spanIdColumn', c)}
              onOperationNameColumnChange={(c) => onUpdateTracesConfig('operationNameColumn', c)}
              onParentSpanIdColumnChange={(c) => onUpdateTracesConfig('parentSpanIdColumn', c)}
              onServiceNameColumnChange={(c) => onUpdateTracesConfig('serviceNameColumn', c)}
              onDurationColumnChange={(c) => onUpdateTracesConfig('durationColumn', c)}
              onDurationUnitChange={(c) => onUpdateTracesConfig('durationUnit', c)}
              onStartTimeColumnChange={(c) => onUpdateTracesConfig('startTimeColumn', c)}
              onTagsColumnChange={(c) => onUpdateTracesConfig('tagsColumn', c)}
              onServiceTagsColumnChange={(c) => onUpdateTracesConfig('serviceTagsColumn', c)}
              onKindColumnChange={(c) => onUpdateTracesConfig('kindColumn', c)}
              onStatusCodeColumnChange={(c) => onUpdateTracesConfig('statusCodeColumn', c)}
              onStatusMessageColumnChange={(c) => onUpdateTracesConfig('statusMessageColumn', c)}
              onStateColumnChange={(c) => onUpdateTracesConfig('stateColumn', c)}
              onInstrumentationLibraryNameColumnChange={(c) =>
                onUpdateTracesConfig('instrumentationLibraryNameColumn', c)
              }
              onInstrumentationLibraryVersionColumnChange={(c) =>
                onUpdateTracesConfig('instrumentationLibraryVersionColumn', c)
              }
              onFlattenNestedChange={(c) => onUpdateTracesConfig('flattenNested', c)}
              onEventsColumnPrefixChange={(c) => onUpdateTracesConfig('traceEventsColumnPrefix', c)}
              onLinksColumnPrefixChange={(c) => onUpdateTracesConfig('traceLinksColumnPrefix', c)}
              onShowTraceLinksChange={(v) => onUpdateTracesConfig('showTraceLinks', v)}
              onTraceTimestampTableSuffixChange={(c) => onUpdateTracesConfig('traceTimestampTableSuffix', c)}
            />
            <Divider />
          </>
        )}
        <AliasTableConfig aliasTables={jsonData.aliasTables} onAliasTablesChange={onAliasTableConfigChange} />
        <Divider />
        <Field label={labels.enableRowLimit.label} description={labels.enableRowLimit.tooltip}>
          <Switch
            value={jsonData.enableRowLimit || false}
            data-testid={labels.enableRowLimit.testid}
            onChange={(e) => {
              trackClickhouseConfigV2EnableRowLimitToggle({ rowLimitEnabled: e.currentTarget.checked });
              onUpdateDatasourceJsonDataOptionChecked(props, 'enableRowLimit')(e);
            }}
          />
        </Field>
        {config.secureSocksDSProxyEnabled && versionGte(config.buildInfo.version, '10.0.0') && (
          <Field label={labels.secureSocksProxy.label} description={labels.secureSocksProxy.tooltip}>
            <Switch
              value={jsonData.enableSecureSocksProxy || false}
              onChange={(e) => onUpdateDatasourceJsonDataOptionChecked(props, 'enableSecureSocksProxy')(e)}
            />
          </Field>
        )}
        <ConfigSubSection title="Custom Settings">
          {/*
           * Shared label strings live in ../../labels.ts (`csLabels`) and shared
           * `selectors.components.Config.CustomSettingsConfig.*` test IDs are reused
           * from the v1 editor so e2e selectors work across both editors.
           *
           * Row layout differs from v1: v2 renders a compact base row (Setting /
           * Value / Enforced / Source) plus an "Advanced" panel that reveals
           * header- and JWT-source fields on demand to fit v2's narrower container.
           *
           * `...customSettings[i]` spreads preserve every field on Setting/Value
           * edits, and `onCustomSettingsChange` mirrors the v1 save filter so
           * header/JWT rows (with empty `value`) survive persistence.
           */}
          <Alert title="" severity="info">
            <ul style={{ margin: 0, paddingLeft: '1.25em' }}>
              <li>Enabling read-only enforcement blocks INSERT and DDL queries from Grafana.</li>
              <li>
                Enforced settings <strong>must NOT</strong> be marked{' '}
                <code>CHANGEABLE_IN_READONLY</code> on the ClickHouse server — doing so would let users
                override them and break the enforcement guarantee.
              </li>
              <li>
                Use ClickHouse row policies with <code>{`getSetting('custom_x')`}</code> to gate data
                access on enforced settings.
              </li>
            </ul>
          </Alert>
          {anyHeaderSource && (
            <Alert
              title="Header-sourced enforced settings require a trusted upstream proxy."
              severity="warning"
              data-testid={selectors.components.Config.CustomSettingsConfig.headerWarningBanner}
            >
              The header value must be set by a trusted proxy on every request. If the proxy does not
              unconditionally overwrite the header, browser-supplied values will reach ClickHouse. Grafana
              must also be configured to forward this header to backend plugins.
            </Alert>
          )}
          {anyJWTSource && (
            <Alert
              title={csLabels.jwtInfoBanner.title}
              severity="info"
              data-testid={selectors.components.Config.CustomSettingsConfig.jwtInfoBanner}
            >
              {csLabels.jwtInfoBanner.message}
            </Alert>
          )}
          {customSettings.map((row, i) => {
            const {
              setting,
              value,
              enforced,
              source,
              headerName,
              onMissing,
              jwtHeaderName,
              jwtClaimPath,
              jwtClaimJoin,
              jwtVerify,
              jwtJwksUrl,
              jwtIssuer,
              jwtAudience,
            } = row;
            const effectiveSource: CHCustomSettingSource = source || 'static';
            const isHeader = !!enforced && effectiveSource === 'header';
            const isJWT = !!enforced && effectiveSource === 'jwt';
            const isDynamic = isHeader || isJWT;
            const effectiveJwtVerify: CHCustomSettingJWTVerify = jwtVerify || 'none';
            const isExpanded = expandedRows.has(i);
            const canExpand = !!enforced && isDynamic;
            const badgeText =
              enforced && source && source !== 'static' ? `enforced · ${source}` : enforced ? 'enforced' : '';

            return (
              <Box key={i} marginBottom={1}>
                <Stack direction="row" alignItems="flex-end">
                  <Field label="Setting" aria-label="Setting">
                    <Input
                      value={setting}
                      placeholder="Setting"
                      onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                        const newSettings = customSettings.concat();
                        newSettings[i] = { ...customSettings[i], setting: changeEvent.target.value };
                        setCustomSettings(newSettings);
                      }}
                      onBlur={() => onCustomSettingsChange(customSettings)}
                    />
                  </Field>
                  <Field label="Value" aria-label="Value">
                    <Input
                      value={value}
                      placeholder={isDynamic ? (isJWT ? '(from JWT claim)' : '(from header)') : 'Value'}
                      disabled={isDynamic}
                      onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                        const newSettings = customSettings.concat();
                        newSettings[i] = { ...customSettings[i], value: changeEvent.target.value };
                        setCustomSettings(newSettings);
                      }}
                      onBlur={() => onCustomSettingsChange(customSettings)}
                    />
                  </Field>
                  <Field
                    label={
                      <Tooltip content="Send with readonly=1 so the user's SQL cannot override it.">
                        <span>Enforced</span>
                      </Tooltip>
                    }
                    aria-label="Enforced"
                  >
                    <Checkbox
                      value={enforced || false}
                      data-testid={selectors.components.Config.CustomSettingsConfig.enforcedCheckbox}
                      onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                        const checked = changeEvent.target.checked;
                        const newSettings = customSettings.concat();
                        newSettings[i] = { ...customSettings[i], enforced: checked };
                        setCustomSettings(newSettings);
                        onCustomSettingsChange(newSettings);
                        trackClickhouseConfigV2CustomSettingEnforcedToggle({ enforced: checked });
                      }}
                    />
                  </Field>
                  {enforced && (
                    <Field
                      label={
                        <Tooltip content={csLabels.source.tooltip}>
                          <span>{csLabels.source.label}</span>
                        </Tooltip>
                      }
                      aria-label={csLabels.source.label}
                    >
                      <Select<CHCustomSettingSource>
                        options={[
                          { label: csLabels.source.staticOption, value: 'static' },
                          { label: csLabels.source.headerOption, value: 'header' },
                          { label: csLabels.source.jwtOption, value: 'jwt' },
                        ]}
                        value={effectiveSource}
                        data-testid={selectors.components.Config.CustomSettingsConfig.sourceSelect}
                        onChange={(selected) => {
                          const newSource = (selected.value as CHCustomSettingSource) || 'static';
                          const newSettings = customSettings.concat();
                          if (newSource === 'header') {
                            newSettings[i] = {
                              setting,
                              value: '',
                              enforced,
                              source: newSource,
                              headerName,
                              onMissing,
                            };
                          } else if (newSource === 'jwt') {
                            newSettings[i] = {
                              setting,
                              value: '',
                              enforced,
                              source: newSource,
                              onMissing,
                              jwtHeaderName,
                              jwtClaimPath,
                              jwtClaimJoin,
                              jwtVerify,
                              jwtJwksUrl,
                              jwtIssuer,
                              jwtAudience,
                            };
                          } else {
                            newSettings[i] = { setting, value, enforced, source: newSource };
                          }
                          setCustomSettings(newSettings);
                          onCustomSettingsChange(newSettings);
                          setExpandedRows((prev) => {
                            const next = new Set(prev);
                            if (newSource === 'static') {
                              next.delete(i);
                            } else {
                              next.add(i);
                            }
                            return next;
                          });
                          trackClickhouseConfigV2CustomSettingSourceChanged({ source: newSource });
                        }}
                        width={18}
                      />
                    </Field>
                  )}
                  {badgeText && (
                    <Field label=" " aria-label="Enforcement status">
                      <Badge text={badgeText} color="blue" />
                    </Field>
                  )}
                  {canExpand && (
                    <Field label=" " aria-label="Toggle advanced settings">
                      <IconButton
                        name={isExpanded ? 'angle-up' : 'angle-down'}
                        tooltip={isExpanded ? 'Hide advanced settings' : 'Show advanced settings'}
                        aria-label={isExpanded ? 'Hide advanced settings' : 'Show advanced settings'}
                        onClick={() => toggleRowExpanded(i)}
                      />
                    </Field>
                  )}
                </Stack>
                {canExpand && isExpanded && (
                  <Box paddingLeft={2} paddingTop={1}>
                    <Stack direction="row" alignItems="flex-end" wrap>
                      {isHeader && (
                        <>
                          <Field
                            label={
                              <Tooltip content={csLabels.headerName.tooltip}>
                                <span>{csLabels.headerName.label}</span>
                              </Tooltip>
                            }
                            aria-label={csLabels.headerName.label}
                          >
                            <Input
                              value={headerName || ''}
                              placeholder={csLabels.headerName.placeholder}
                              data-testid={selectors.components.Config.CustomSettingsConfig.headerNameInput}
                              onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                                const newSettings = customSettings.concat();
                                newSettings[i] = {
                                  ...customSettings[i],
                                  value: '',
                                  source: effectiveSource,
                                  headerName: changeEvent.target.value,
                                };
                                setCustomSettings(newSettings);
                              }}
                              onBlur={() => onCustomSettingsChange(customSettings)}
                            />
                          </Field>
                          <Field
                            label={
                              <Tooltip content={csLabels.onMissing.tooltip}>
                                <span>{csLabels.onMissing.label}</span>
                              </Tooltip>
                            }
                            aria-label={csLabels.onMissing.label}
                          >
                            <Select<CHCustomSettingOnMissing>
                              options={[
                                { label: csLabels.onMissing.rejectOption, value: 'reject' },
                                { label: csLabels.onMissing.emptyOption, value: 'empty' },
                              ]}
                              value={(onMissing as CHCustomSettingOnMissing) || 'reject'}
                              data-testid={selectors.components.Config.CustomSettingsConfig.onMissingSelect}
                              onChange={(selected) => {
                                const newSettings = customSettings.concat();
                                newSettings[i] = {
                                  ...customSettings[i],
                                  value: '',
                                  source: effectiveSource,
                                  onMissing: selected.value as CHCustomSettingOnMissing,
                                };
                                setCustomSettings(newSettings);
                                onCustomSettingsChange(newSettings);
                              }}
                              width={18}
                            />
                          </Field>
                        </>
                      )}
                      {isJWT && (
                        <>
                          <Field
                            label={
                              <Tooltip content={csLabels.jwtTokenHeaderInput.tooltip}>
                                <span>{csLabels.jwtTokenHeaderInput.label}</span>
                              </Tooltip>
                            }
                            aria-label={csLabels.jwtTokenHeaderInput.label}
                          >
                            <Input
                              value={jwtHeaderName || ''}
                              placeholder={csLabels.jwtTokenHeaderInput.placeholder}
                              data-testid={selectors.components.Config.CustomSettingsConfig.jwtTokenHeaderInput}
                              onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                                const newSettings = customSettings.concat();
                                newSettings[i] = {
                                  ...customSettings[i],
                                  value: '',
                                  source: effectiveSource,
                                  jwtHeaderName: changeEvent.target.value,
                                };
                                setCustomSettings(newSettings);
                              }}
                              onBlur={() => onCustomSettingsChange(customSettings)}
                            />
                          </Field>
                          <Field
                            label={
                              <Tooltip content={csLabels.jwtClaimPathInput.tooltip}>
                                <span>{csLabels.jwtClaimPathInput.label}</span>
                              </Tooltip>
                            }
                            aria-label={csLabels.jwtClaimPathInput.label}
                            description={
                              'Nested paths (for example ["realm_access","roles"]) can only be set via provisioning YAML.'
                            }
                          >
                            <Input
                              value={(jwtClaimPath && jwtClaimPath[0]) || ''}
                              placeholder={csLabels.jwtClaimPathInput.placeholder}
                              data-testid={selectors.components.Config.CustomSettingsConfig.jwtClaimPathInput}
                              onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                                const nextClaim = changeEvent.target.value
                                  ? [changeEvent.target.value]
                                  : undefined;
                                const newSettings = customSettings.concat();
                                newSettings[i] = {
                                  ...customSettings[i],
                                  value: '',
                                  source: effectiveSource,
                                  jwtClaimPath: nextClaim,
                                };
                                setCustomSettings(newSettings);
                              }}
                              onBlur={() => onCustomSettingsChange(customSettings)}
                            />
                          </Field>
                          <Field
                            label={
                              <Tooltip content={csLabels.jwtArrayJoinInput.tooltip}>
                                <span>{csLabels.jwtArrayJoinInput.label}</span>
                              </Tooltip>
                            }
                            aria-label={csLabels.jwtArrayJoinInput.label}
                          >
                            <Input
                              value={jwtClaimJoin || ''}
                              placeholder={csLabels.jwtArrayJoinInput.placeholder}
                              data-testid={selectors.components.Config.CustomSettingsConfig.jwtArrayJoinInput}
                              onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                                const newSettings = customSettings.concat();
                                newSettings[i] = {
                                  ...customSettings[i],
                                  value: '',
                                  source: effectiveSource,
                                  jwtClaimJoin: changeEvent.target.value,
                                };
                                setCustomSettings(newSettings);
                              }}
                              onBlur={() => onCustomSettingsChange(customSettings)}
                            />
                          </Field>
                          <Field
                            label={
                              <Tooltip content={csLabels.jwtVerifySelect.tooltip}>
                                <span>{csLabels.jwtVerifySelect.label}</span>
                              </Tooltip>
                            }
                            aria-label={csLabels.jwtVerifySelect.label}
                          >
                            <Select<CHCustomSettingJWTVerify>
                              options={[
                                { label: csLabels.jwtVerifySelect.noneOption, value: 'none' },
                                { label: csLabels.jwtVerifySelect.jwksOption, value: 'jwks' },
                              ]}
                              value={effectiveJwtVerify}
                              data-testid={selectors.components.Config.CustomSettingsConfig.jwtVerifySelect}
                              onChange={(selected) => {
                                const newSettings = customSettings.concat();
                                newSettings[i] = {
                                  ...customSettings[i],
                                  value: '',
                                  source: effectiveSource,
                                  jwtVerify: selected.value as CHCustomSettingJWTVerify,
                                };
                                setCustomSettings(newSettings);
                                onCustomSettingsChange(newSettings);
                              }}
                              width={22}
                            />
                          </Field>
                          {effectiveJwtVerify === 'jwks' && (
                            <>
                              <Field
                                label={
                                  <Tooltip content={csLabels.jwtJwksUrlInput.tooltip}>
                                    <span>{csLabels.jwtJwksUrlInput.label}</span>
                                  </Tooltip>
                                }
                                aria-label={csLabels.jwtJwksUrlInput.label}
                              >
                                <Input
                                  value={jwtJwksUrl || ''}
                                  placeholder={csLabels.jwtJwksUrlInput.placeholder}
                                  data-testid={selectors.components.Config.CustomSettingsConfig.jwtJwksUrlInput}
                                  onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                                    const newSettings = customSettings.concat();
                                    newSettings[i] = {
                                      ...customSettings[i],
                                      value: '',
                                      source: effectiveSource,
                                      jwtJwksUrl: changeEvent.target.value,
                                    };
                                    setCustomSettings(newSettings);
                                  }}
                                  onBlur={() => onCustomSettingsChange(customSettings)}
                                />
                              </Field>
                              <Field
                                label={
                                  <Tooltip content={csLabels.jwtIssuerInput.tooltip}>
                                    <span>{csLabels.jwtIssuerInput.label}</span>
                                  </Tooltip>
                                }
                                aria-label={csLabels.jwtIssuerInput.label}
                              >
                                <Input
                                  value={jwtIssuer || ''}
                                  placeholder={csLabels.jwtIssuerInput.placeholder}
                                  data-testid={selectors.components.Config.CustomSettingsConfig.jwtIssuerInput}
                                  onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                                    const newSettings = customSettings.concat();
                                    newSettings[i] = {
                                      ...customSettings[i],
                                      value: '',
                                      source: effectiveSource,
                                      jwtIssuer: changeEvent.target.value,
                                    };
                                    setCustomSettings(newSettings);
                                  }}
                                  onBlur={() => onCustomSettingsChange(customSettings)}
                                />
                              </Field>
                              <Field
                                label={
                                  <Tooltip content={csLabels.jwtAudienceInput.tooltip}>
                                    <span>{csLabels.jwtAudienceInput.label}</span>
                                  </Tooltip>
                                }
                                aria-label={csLabels.jwtAudienceInput.label}
                              >
                                <Input
                                  value={jwtAudience || ''}
                                  placeholder={csLabels.jwtAudienceInput.placeholder}
                                  data-testid={selectors.components.Config.CustomSettingsConfig.jwtAudienceInput}
                                  onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                                    const newSettings = customSettings.concat();
                                    newSettings[i] = {
                                      ...customSettings[i],
                                      value: '',
                                      source: effectiveSource,
                                      jwtAudience: changeEvent.target.value,
                                    };
                                    setCustomSettings(newSettings);
                                  }}
                                  onBlur={() => onCustomSettingsChange(customSettings)}
                                />
                              </Field>
                            </>
                          )}
                          <Field
                            label={
                              <Tooltip content={csLabels.onMissing.tooltip}>
                                <span>{csLabels.onMissing.label}</span>
                              </Tooltip>
                            }
                            aria-label={csLabels.onMissing.label}
                          >
                            <Select<CHCustomSettingOnMissing>
                              options={[
                                { label: csLabels.onMissing.rejectOption, value: 'reject' },
                                { label: csLabels.onMissing.emptyOption, value: 'empty' },
                              ]}
                              value={(onMissing as CHCustomSettingOnMissing) || 'reject'}
                              data-testid={selectors.components.Config.CustomSettingsConfig.onMissingSelect}
                              onChange={(selected) => {
                                const newSettings = customSettings.concat();
                                newSettings[i] = {
                                  ...customSettings[i],
                                  value: '',
                                  source: effectiveSource,
                                  onMissing: selected.value as CHCustomSettingOnMissing,
                                };
                                setCustomSettings(newSettings);
                                onCustomSettingsChange(newSettings);
                              }}
                              width={18}
                            />
                          </Field>
                        </>
                      )}
                    </Stack>
                  </Box>
                )}
              </Box>
            );
          })}
          <Button
            variant="secondary"
            icon="plus"
            type="button"
            onClick={() => {
              trackClickhouseConfigV2CustomSettingClicked();
              setCustomSettings([...customSettings, { setting: '', value: '' }]);
            }}
          >
            Add custom setting
          </Button>
        </ConfigSubSection>
        <Field
          label="Enforce read-only on all queries"
          description={
            anyEnforced
              ? 'Automatically enabled because at least one custom setting is marked Enforced.'
              : 'Forces readonly=1 on every query. Blocks INSERT/DDL from Grafana. Enable when you want read-only lockdown without enforced settings.'
          }
        >
          <Tooltip
            content={
              anyEnforced
                ? 'Disabled because enforceReadOnly is automatically on when any custom setting is marked Enforced.'
                : ''
            }
            placement="top"
          >
            <Switch
              value={anyEnforced || jsonData.enforceReadOnly || false}
              disabled={anyEnforced}
              onChange={(e) => {
                if (!anyEnforced) {
                  trackClickhouseConfigV2EnforceReadOnlyToggle({ enforceReadOnly: e.currentTarget.checked });
                  onUpdateDatasourceJsonDataOptionChecked(props, 'enforceReadOnly')(e);
                }
              }}
            />
          </Tooltip>
        </Field>
      </CollapsableSection>
    </Box>
  );
};

const getStyles = () => ({
  badge: css({
    marginLeft: 'auto',
  }),
});
