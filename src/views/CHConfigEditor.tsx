import React, { ChangeEvent, useEffect, useMemo, useState } from 'react';
import {
  DataSourcePluginOptionsEditorProps,
  onUpdateDatasourceJsonDataOption,
  onUpdateDatasourceSecureJsonDataOption,
} from '@grafana/data';
import {
  RadioButtonGroup,
  Switch,
  Input,
  SecretInput,
  Button,
  Field,
  Alert,
  Stack,
  TextLink,
  Tooltip,
  Icon,
  Checkbox,
  Select,
} from '@grafana/ui';
import { CertificationKey } from '../components/ui/CertificationKey';
import {
  CHConfig,
  CHCustomSetting,
  CHCustomSettingSource,
  CHCustomSettingOnMissing,
  CHCustomSettingJWTVerify,
  CHSecureConfig,
  CHLogsConfig,
  Protocol,
  CHTracesConfig,
  AliasTableEntry,
  ConfigMode,
  SignalType,
} from 'types/config';
import { isVersionGtOrEq as versionGte } from 'utils/version';
import { ConfigSection, ConfigSubSection, DataSourceDescription } from 'components/experimental/ConfigSection';
import { config } from '@grafana/runtime';
import { Divider } from 'components/Divider';
import { TimeUnit } from 'types/queryBuilder';
import { DefaultDatabaseTableConfig } from 'components/configEditor/DefaultDatabaseTableConfig';
import { QuerySettingsConfig } from 'components/configEditor/QuerySettingsConfig';
import { LogsConfig } from 'components/configEditor/LogsConfig';
import { TracesConfig } from 'components/configEditor/TracesConfig';
import { HttpHeadersConfig } from 'components/configEditor/HttpHeadersConfig';
import allLabels from '../labels';
import { selectors } from '../selectors';
import { createValidationAPI, onHttpHeadersChange, useConfigDefaults } from './CHConfigEditorHooks';
import { AliasTableConfig } from '../components/configEditor/AliasTableConfig';
import * as trackingV1 from './trackingV1';

export interface ConfigEditorProps extends DataSourcePluginOptionsEditorProps<CHConfig, CHSecureConfig> {}

export const ConfigEditor: React.FC<ConfigEditorProps> = (props) => {
  const { options, onOptionsChange } = props;
  const { jsonData, secureJsonFields } = options;

  const validationEnabled = (config.featureToggles as Record<string, boolean | undefined> | undefined)?.[
    'clickHouseConfigValidation'
  ];
  const validation = useMemo(
    () => (validationEnabled ? (props.validation ?? createValidationAPI()) : undefined),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [props.validation]
  );
  const labels = allLabels.components.Config.ConfigEditor;
  const secureJsonData = (options.secureJsonData || {}) as CHSecureConfig;
  const hasTLSCACert = secureJsonFields && secureJsonFields.tlsCACert;
  const hasTLSClientCert = secureJsonFields && secureJsonFields.tlsClientCert;
  const hasTLSClientKey = secureJsonFields && secureJsonFields.tlsClientKey;
  const protocolOptions = [
    { label: 'Native', value: Protocol.Native },
    { label: 'HTTP', value: Protocol.Http },
  ];

  useConfigDefaults(options, onOptionsChange);

  // Register a validator for required fields. The validator runs when
  // validation.validate() is called by Grafana before saving — if it returns
  // false, the save and backend health check are both blocked.
  //
  // We also eagerly call clearError in the effect body so that inline errors
  // disappear as soon as the user fills in a field, rather than waiting for
  // the next save attempt.
  useEffect(() => {
    if (!validation) {
      return;
    }
    if (jsonData.host) {
      validation.clearError('host');
    }
    if (jsonData.port) {
      validation.clearError('port');
    }

    return validation.registerValidation(() => {
      let valid = true;
      if (!jsonData.host) {
        validation.setError('host', labels.serverAddress.error);
        valid = false;
      } else {
        validation.clearError('host');
      }
      if (!jsonData.port) {
        validation.setError('port', labels.serverPort.error);
        valid = false;
      } else {
        validation.clearError('port');
      }
      return valid;
    });
  }, [jsonData.host, jsonData.port, validation, labels.serverAddress.error, labels.serverPort.error]);

  const fieldErrors = validation?.getErrors() ?? {};

  // The structural empty check must run regardless of the validation plumbing:
  // with the clickHouseConfigValidation toggle off `validation` is undefined,
  // and with it on the local ValidationAPI only gains errors once Grafana
  // calls validate() (13.1+). Either way an empty required host/port field
  // must surface an inline error immediately, as it did in v4.17.0, with any
  // API-driven error message taking precedence when present.
  const hostInvalid = Boolean(fieldErrors.host) || !jsonData.host;
  const hostError = fieldErrors.host || (jsonData.host ? undefined : labels.serverAddress.error);
  const portInvalid = Boolean(fieldErrors.port) || !jsonData.port;
  const portError = fieldErrors.port || (jsonData.port ? undefined : labels.serverPort.error);

  const onPortChange = (port: string) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...options.jsonData,
        port: +port,
      },
    });
  };
  const onTLSSettingsChange = (
    key: keyof Pick<CHConfig, 'tlsSkipVerify' | 'tlsAuth' | 'tlsAuthWithCACert'>,
    value: boolean
  ) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...options.jsonData,
        [key]: value,
      },
    });
  };
  const onSwitchToggle = (
    key: keyof Pick<
      CHConfig,
      | 'secure'
      | 'validateSql'
      | 'enableMapKeysDiscovery'
      | 'enableSecureSocksProxy'
      | 'forwardGrafanaHeaders'
      | 'enableRowLimit'
      | 'enforceReadOnly'
      | 'hideTableNameInAdhocFilters'
    >,
    value: boolean
  ) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...options.jsonData,
        [key]: value,
      },
    });
  };

  const onProtocolToggle = (protocol: Protocol) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...options.jsonData,
        protocol: protocol,
      },
    });
  };

  const onCertificateChangeFactory = (key: keyof Omit<CHSecureConfig, 'password'>, value: string) => {
    onOptionsChange({
      ...options,
      secureJsonData: {
        ...secureJsonData,
        [key]: value,
      },
    });
  };
  const onResetClickFactory = (key: keyof Omit<CHSecureConfig, 'password'>) => {
    onOptionsChange({
      ...options,
      secureJsonFields: {
        ...secureJsonFields,
        [key]: false,
      },
      secureJsonData: {
        ...secureJsonData,
        [key]: '',
      },
    });
  };
  const onResetPassword = () => {
    onOptionsChange({
      ...options,
      secureJsonFields: {
        ...options.secureJsonFields,
        password: false,
      },
      secureJsonData: {
        ...options.secureJsonData,
        password: '',
      },
    });
  };
  const onCustomSettingsChange = (customSettings: CHCustomSetting[]) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...options.jsonData,
        customSettings: customSettings.filter(
          (s) => !!s.setting && (!!s.value || (s.enforced && s.source === 'header' && !!s.headerName) || (s.enforced && s.source === 'jwt' && !!s.jwtClaim))
        ),
      },
    });
  };
  const onLogsConfigChange = (key: keyof CHLogsConfig, value: string | boolean | string[]) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...options.jsonData,
        logs: {
          ...options.jsonData.logs,
          [key]: value,
        },
      },
    });
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
  const onAliasTableConfigChange = (aliasTables: AliasTableEntry[]) => {
    // track events when both a target table and alias table has a value
    if (aliasTables.length > 0 && aliasTables[0].targetTable && aliasTables[0].aliasTable) {
      trackingV1.trackClickhouseConfigV1ColumnAliasTableAdded();
    }
    onOptionsChange({
      ...options,
      jsonData: {
        ...options.jsonData,
        aliasTables,
      },
    });
  };

  const [customSettings, setCustomSettings] = useState(jsonData.customSettings || []);

  // True when at least one custom setting row has enforced=true; drives the enforceReadOnly toggle state.
  const anyEnforced = customSettings.some((s) => s.enforced);
  // True when any enforced row uses source=header; drives the warning banner.
  const anyHeaderSource = customSettings.some((s) => s.enforced && s.source === 'header');
  // True when any enforced row uses source=jwt; drives the info banner.
  const anyJWTSource = customSettings.some((s) => s.enforced && s.source === 'jwt');

  const hasAdditionalSettings = Boolean(
    window.location.hash || // if trying to link to section on page, open all settings (React breaks this?)
    options.jsonData.defaultDatabase ||
    options.jsonData.defaultTable ||
    options.jsonData.dialTimeout ||
    options.jsonData.queryTimeout ||
    options.jsonData.validateSql ||
    options.jsonData.enableSecureSocksProxy ||
    options.jsonData.customSettings ||
    options.jsonData.logs ||
    options.jsonData.traces
  );
  const configMode = jsonData.configMode || (jsonData.signalType ? 'single-table' : 'classic');
  const isSingleTableMode = configMode === 'single-table';
  const selectedSignalType = isSingleTableMode ? jsonData.signalType || 'logs' : undefined;

  const defaultPort = jsonData.secure
    ? jsonData.protocol === Protocol.Native
      ? labels.serverPort.secureNativePort
      : labels.serverPort.secureHttpPort
    : jsonData.protocol === Protocol.Native
      ? labels.serverPort.insecureNativePort
      : labels.serverPort.insecureHttpPort;
  const portDescription = `${labels.serverPort.tooltip} (default for ${jsonData.secure ? 'secure' : ''} ${jsonData.protocol}: ${defaultPort})`;

  const uidWarning = !options.uid && (
    <Alert title="" severity="warning" buttonContent="Close">
      <Stack>
        <div>
          {'This datasource is missing the'}
          <code>uid</code>
          {'field in its configuration. If your datasource is '}
          <a
            style={{ textDecoration: 'underline' }}
            href="https://grafana.com/docs/grafana/latest/administration/provisioning/#data-sources"
            target="_blank"
            rel="noreferrer"
          >
            provisioned via YAML
          </a>
          {', please verify the UID is set. This is required to enable data linking between logs and traces.'}
        </div>
      </Stack>
    </Alert>
  );

  return (
    <>
      {uidWarning}
      <DataSourceDescription
        dataSourceName="Clickhouse"
        docsLink="https://grafana.com/grafana/plugins/grafana-clickhouse-datasource/"
        hasRequiredFields
      />
      <Divider />
      <ConfigSection title="Server">
        <Field
          required
          label={labels.serverAddress.label}
          description={labels.serverAddress.tooltip}
          invalid={hostInvalid}
          error={hostError}
        >
          <Input
            name="host"
            width={80}
            value={jsonData.host || ''}
            onChange={(e) => onUpdateDatasourceJsonDataOption(props, 'host')(e)}
            label={labels.serverAddress.label}
            aria-label={labels.serverAddress.label}
            placeholder={labels.serverAddress.placeholder}
            onBlur={trackingV1.trackClickhouseConfigV1HostInput}
          />
        </Field>
        <Field
          required
          label={labels.serverPort.label}
          description={portDescription}
          invalid={portInvalid}
          error={portError}
        >
          <Input
            name="port"
            width={40}
            type="number"
            value={jsonData.port || ''}
            onChange={(e) => onPortChange(e.currentTarget.value)}
            label={labels.serverPort.label}
            aria-label={labels.serverPort.label}
            placeholder={defaultPort}
            onBlur={(e) => trackingV1.trackClickhouseConfigV1PortInput({ port: e.currentTarget.value })}
          />
        </Field>

        <Field label={labels.protocol.label} description={labels.protocol.tooltip}>
          <RadioButtonGroup<Protocol>
            options={protocolOptions}
            disabledOptions={[]}
            value={jsonData.protocol || Protocol.Native}
            onChange={(e) => {
              trackingV1.trackClickhouseConfigV1NativeHttpToggleClicked({ nativeHttpToggle: e });
              onProtocolToggle(e!);
            }}
          />
        </Field>
        <Field label={labels.secure.label} description={labels.secure.tooltip}>
          <Switch
            id="secure"
            value={jsonData.secure || false}
            onChange={(e) => {
              trackingV1.trackClickhouseConfigV1SecureConnectionToggleClicked({
                secureConnection: e.currentTarget.checked,
              });
              onSwitchToggle('secure', e.currentTarget.checked);
            }}
          />
        </Field>

        {jsonData.protocol === Protocol.Http && (
          <Field label={labels.path.label} description={labels.path.tooltip}>
            <Input
              value={jsonData.path || ''}
              name="path"
              width={80}
              onChange={onUpdateDatasourceJsonDataOption(props, 'path')}
              label={labels.path.label}
              aria-label={labels.path.label}
              placeholder={labels.path.placeholder}
            />
          </Field>
        )}
      </ConfigSection>

      {jsonData.protocol === Protocol.Http && (
        <HttpHeadersConfig
          headers={options.jsonData.httpHeaders}
          forwardGrafanaHeaders={options.jsonData.forwardGrafanaHeaders}
          secureFields={options.secureJsonFields}
          onHttpHeadersChange={(headers) => onHttpHeadersChange(headers, options, onOptionsChange)}
          onForwardGrafanaHeadersChange={(forwardGrafanaHeaders) =>
            onSwitchToggle('forwardGrafanaHeaders', forwardGrafanaHeaders)
          }
        />
      )}

      <Divider />
      <ConfigSection title="TLS / SSL Settings">
        <Field label={labels.tlsSkipVerify.label} description={labels.tlsSkipVerify.tooltip}>
          <Switch
            value={jsonData.tlsSkipVerify || false}
            onChange={(e) => {
              trackingV1.trackClickhouseConfigV1SkipTLSVerifyToggleClicked({
                skipTlsVerifyToggle: e.currentTarget.checked,
              });
              onTLSSettingsChange('tlsSkipVerify', e.currentTarget.checked);
            }}
          />
        </Field>
        <Field label={labels.tlsClientAuth.label} description={labels.tlsClientAuth.tooltip}>
          <Switch
            value={jsonData.tlsAuth || false}
            onChange={(e) => {
              trackingV1.trackClickhouseConfigV1TLSClientAuthToggleClicked({
                clientAuthToggle: e.currentTarget.checked,
              });
              onTLSSettingsChange('tlsAuth', e.currentTarget.checked);
            }}
          />
        </Field>
        <Field label={labels.tlsAuthWithCACert.label} description={labels.tlsAuthWithCACert.tooltip}>
          <Switch
            value={jsonData.tlsAuthWithCACert || false}
            onChange={(e) => {
              trackingV1.trackClickhouseConfigV1WithCACertToggleClicked({ caCertToggle: e.currentTarget.checked });
              onTLSSettingsChange('tlsAuthWithCACert', e.currentTarget.checked);
            }}
          />
        </Field>
        {jsonData.tlsAuthWithCACert && (
          <CertificationKey
            hasCert={!!hasTLSCACert}
            onChange={(e) => onCertificateChangeFactory('tlsCACert', e.currentTarget.value)}
            placeholder={labels.tlsCACert.placeholder}
            label={labels.tlsCACert.label}
            onClick={() => onResetClickFactory('tlsCACert')}
          />
        )}
        {jsonData.tlsAuth && (
          <>
            <CertificationKey
              hasCert={!!hasTLSClientCert}
              onChange={(e) => onCertificateChangeFactory('tlsClientCert', e.currentTarget.value)}
              placeholder={labels.tlsClientCert.placeholder}
              label={labels.tlsClientCert.label}
              onClick={() => onResetClickFactory('tlsClientCert')}
            />
            <CertificationKey
              hasCert={!!hasTLSClientKey}
              placeholder={labels.tlsClientKey.placeholder}
              label={labels.tlsClientKey.label}
              onChange={(e) => onCertificateChangeFactory('tlsClientKey', e.currentTarget.value)}
              onClick={() => onResetClickFactory('tlsClientKey')}
            />
          </>
        )}
      </ConfigSection>

      <Divider />
      <ConfigSection title="Credentials">
        <Field label={labels.username.label} description={labels.username.tooltip}>
          <Input
            name="user"
            width={40}
            value={jsonData.username || ''}
            onChange={onUpdateDatasourceJsonDataOption(props, 'username')}
            label={labels.username.label}
            aria-label={labels.username.label}
            placeholder={labels.username.placeholder}
          />
        </Field>
        <Field label={labels.password.label} description={labels.password.tooltip}>
          <SecretInput
            name="pwd"
            width={40}
            label={labels.password.label}
            aria-label={labels.password.label}
            placeholder={labels.password.placeholder}
            value={secureJsonData.password || ''}
            isConfigured={(secureJsonFields && secureJsonFields.password) as boolean}
            onReset={onResetPassword}
            onChange={onUpdateDatasourceSecureJsonDataOption(props, 'password')}
          />
        </Field>
        <Field label={labels.oauthPassThru.label} description={
          <>
            {'Authenticate using '}
            <TextLink
              variant="bodySmall"
              href="https://clickhouse.com/docs/en/operations/external-authenticators/jwt"
              external
            >
              JWT
            </TextLink>
            {' '}
            <Tooltip content="ClickHouse Cloud only" placement="top">
              <Icon name="info-circle" size="sm" />
            </Tooltip>
            {'. When enabled, credentials are only used for health checks.'}
          </>
        }>
          <Switch
            id="oauthPassThru"
            className="gf-form"
            value={jsonData.oauthPassThru || false}
            onChange={(e) => {
              const checked = e.currentTarget.checked;
              onOptionsChange({
                ...options,
                jsonData: {
                  ...jsonData,
                  oauthPassThru: checked,
                },
              });
            }}
          />
        </Field>
        {jsonData.oauthPassThru && (
          <Field
            label={labels.oauthPassThruAllowFallback.label}
            description={labels.oauthPassThruAllowFallback.tooltip}
          >
            <Switch
              id="oauthPassThruAllowFallback"
              className="gf-form"
              value={jsonData.oauthPassThruAllowFallback || false}
              onChange={(e) => {
                const checked = e.currentTarget.checked;
                onOptionsChange({
                  ...options,
                  jsonData: {
                    ...jsonData,
                    oauthPassThruAllowFallback: checked,
                  },
                });
              }}
            />
          </Field>
        )}
      </ConfigSection>

      <Divider />
      <ConfigSection
        title="Configuration Mode"
        description="Choose how this datasource is used. 'Single table' provides a focused, compact query editor for one table. 'All databases' gives full access to explore any database and table."
      >
        <Field label="Mode">
          <RadioButtonGroup<ConfigMode>
            options={[
              { label: 'All databases', value: 'classic' },
              { label: 'Single table', value: 'single-table' },
            ]}
            value={configMode}
            onChange={(v) => {
              const newJsonData = { ...options.jsonData, configMode: v };
              if (v === 'classic') {
                newJsonData.signalType = undefined;
              } else if (!newJsonData.signalType) {
                newJsonData.signalType = 'logs';
              }
              onOptionsChange({ ...options, jsonData: newJsonData });
            }}
          />
        </Field>
        {isSingleTableMode && (
          <Field label="Signal type" description="What kind of data does this table contain?">
            <RadioButtonGroup<SignalType>
              options={[
                { label: 'Logs', value: 'logs', description: 'Log search with severity, message, and attributes' },
                { label: 'Traces', value: 'traces', description: 'Distributed tracing with spans and service maps' },
              ]}
              value={selectedSignalType}
              onChange={(v) => {
                onOptionsChange({
                  ...options,
                  jsonData: {
                    ...options.jsonData,
                    configMode: 'single-table',
                    signalType: v,
                  },
                });
              }}
            />
          </Field>
        )}
      </ConfigSection>

      {isSingleTableMode && selectedSignalType && (
        <>
          {selectedSignalType === 'logs' && (
            <>
              <Divider />
              <LogsConfig
                variant="single-table"
                logsConfig={jsonData.logs}
                onDefaultDatabaseChange={(db) => onLogsConfigChange('defaultDatabase', db)}
                onDefaultTableChange={(table) => onLogsConfigChange('defaultTable', table)}
                onOtelEnabledChange={(v) => onLogsConfigChange('otelEnabled', v)}
                onOtelVersionChange={(v) => onLogsConfigChange('otelVersion', v)}
                onFilterTimeColumnChange={(c) => onLogsConfigChange('filterTimeColumn', c)}
                onTimeColumnChange={(c) => onLogsConfigChange('timeColumn', c)}
                onLevelColumnChange={(c) => onLogsConfigChange('levelColumn', c)}
                onMessageColumnChange={(c) => onLogsConfigChange('messageColumn', c)}
                onSelectContextColumnsChange={(c) => onLogsConfigChange('selectContextColumns', c)}
                onContextColumnsChange={(c) => onLogsConfigChange('contextColumns', c)}
                onShowLogLinksChange={(v) => onLogsConfigChange('showLogLinks', v)}
              />
            </>
          )}
          {selectedSignalType === 'traces' && (
            <>
              <Divider />
              <TracesConfig
                variant="single-table"
                tracesConfig={jsonData.traces}
                onDefaultDatabaseChange={(db) => onTracesConfigChange('defaultDatabase', db)}
                onDefaultTableChange={(table) => onTracesConfigChange('defaultTable', table)}
                onOtelEnabledChange={(v) => onTracesConfigChange('otelEnabled', v)}
                onOtelVersionChange={(v) => onTracesConfigChange('otelVersion', v)}
                onTraceIdColumnChange={(c) => onTracesConfigChange('traceIdColumn', c)}
                onSpanIdColumnChange={(c) => onTracesConfigChange('spanIdColumn', c)}
                onOperationNameColumnChange={(c) => onTracesConfigChange('operationNameColumn', c)}
                onParentSpanIdColumnChange={(c) => onTracesConfigChange('parentSpanIdColumn', c)}
                onServiceNameColumnChange={(c) => onTracesConfigChange('serviceNameColumn', c)}
                onDurationColumnChange={(c) => onTracesConfigChange('durationColumn', c)}
                onDurationUnitChange={(c) => onTracesConfigChange('durationUnit', c)}
                onStartTimeColumnChange={(c) => onTracesConfigChange('startTimeColumn', c)}
                onTagsColumnChange={(c) => onTracesConfigChange('tagsColumn', c)}
                onServiceTagsColumnChange={(c) => onTracesConfigChange('serviceTagsColumn', c)}
                onKindColumnChange={(c) => onTracesConfigChange('kindColumn', c)}
                onStatusCodeColumnChange={(c) => onTracesConfigChange('statusCodeColumn', c)}
                onStatusMessageColumnChange={(c) => onTracesConfigChange('statusMessageColumn', c)}
                onStateColumnChange={(c) => onTracesConfigChange('stateColumn', c)}
                onInstrumentationLibraryNameColumnChange={(c) =>
                  onTracesConfigChange('instrumentationLibraryNameColumn', c)
                }
                onInstrumentationLibraryVersionColumnChange={(c) =>
                  onTracesConfigChange('instrumentationLibraryVersionColumn', c)
                }
                onFlattenNestedChange={(c) => onTracesConfigChange('flattenNested', c)}
                onEventsColumnPrefixChange={(c) => onTracesConfigChange('traceEventsColumnPrefix', c)}
                onLinksColumnPrefixChange={(c) => onTracesConfigChange('traceLinksColumnPrefix', c)}
                onShowTraceLinksChange={(v) => onTracesConfigChange('showTraceLinks', v)}
                onTraceTimestampTableSuffixChange={(v) => onTracesConfigChange('traceTimestampTableSuffix', v)}
              />
            </>
          )}
          <Divider />
          <ConfigSection title="Query settings" isCollapsible isInitiallyOpen={false}>
            <QuerySettingsConfig
              dialTimeout={jsonData.dialTimeout}
              queryTimeout={jsonData.queryTimeout}
              connMaxLifetime={jsonData.connMaxLifetime}
              maxIdleConns={jsonData.maxIdleConns}
              maxOpenConns={jsonData.maxOpenConns}
              rowCapacityHint={jsonData.rowCapacityHint}
              validateSql={jsonData.validateSql}
              enableMapKeysDiscovery={jsonData.enableMapKeysDiscovery}
              onDialTimeoutChange={(e) => onUpdateDatasourceJsonDataOption(props, 'dialTimeout')(e)}
              onQueryTimeoutChange={(e) => onUpdateDatasourceJsonDataOption(props, 'queryTimeout')(e)}
              onConnMaxLifetimeChange={(e) => onUpdateDatasourceJsonDataOption(props, 'connMaxLifetime')(e)}
              onConnMaxIdleConnsChange={(e) => onUpdateDatasourceJsonDataOption(props, 'maxIdleConns')(e)}
              onConnMaxOpenConnsChange={(e) => onUpdateDatasourceJsonDataOption(props, 'maxOpenConns')(e)}
              onRowCapacityHintChange={(e) => onUpdateDatasourceJsonDataOption(props, 'rowCapacityHint')(e)}
              onValidateSqlChange={(e) => onSwitchToggle('validateSql', e.currentTarget.checked)}
              onEnableMapKeysDiscoveryChange={(e) => onSwitchToggle('enableMapKeysDiscovery', e.currentTarget.checked)}
            />
          </ConfigSection>
        </>
      )}

      {!isSingleTableMode && (
        <>
          <Divider />
          <ConfigSection
            title="Additional settings"
            description="Additional settings are optional settings that can be configured for more control over your data source. This includes the default database, dial and query timeouts, SQL validation, and custom ClickHouse settings."
            isCollapsible
            isInitiallyOpen={hasAdditionalSettings}
          >
            <Divider />
            <DefaultDatabaseTableConfig
              defaultDatabase={jsonData.defaultDatabase}
              defaultTable={jsonData.defaultTable}
              onDefaultDatabaseChange={(e) => {
                trackingV1.trackClickhouseConfigV1DefaultDbInput();
                onUpdateDatasourceJsonDataOption(props, 'defaultDatabase')(e);
              }}
              onDefaultTableChange={(e) => {
                trackingV1.trackClickhouseConfigV1DefaultTableInput();
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
                trackingV1.trackClickhouseConfigV1QuerySettings({ dialTimeout: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'dialTimeout')(e);
              }}
              onQueryTimeoutChange={(e) => {
                trackingV1.trackClickhouseConfigV1QuerySettings({ queryTimeout: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'queryTimeout')(e);
              }}
              onRowCapacityHintChange={(e) => {
                trackingV1.trackClickhouseConfigV1QuerySettings({ rowCapacityHint: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'rowCapacityHint')(e);
              }}
              onConnMaxLifetimeChange={(e) => {
                trackingV1.trackClickhouseConfigV1QuerySettings({ connMaxLifetime: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'connMaxLifetime')(e);
              }}
              onConnMaxIdleConnsChange={(e) => {
                trackingV1.trackClickhouseConfigV1QuerySettings({ maxIdleConns: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'maxIdleConns')(e);
              }}
              onConnMaxOpenConnsChange={(e) => {
                trackingV1.trackClickhouseConfigV1QuerySettings({ maxOpenConns: Number(e.currentTarget.value) });
                onUpdateDatasourceJsonDataOption(props, 'maxOpenConns')(e);
              }}
              onValidateSqlChange={(e) => {
                trackingV1.trackClickhouseConfigV1QuerySettings({ validateSql: e.currentTarget.checked });
                onSwitchToggle('validateSql', e.currentTarget.checked);
              }}
              onEnableMapKeysDiscoveryChange={(e) => {
                trackingV1.trackClickhouseConfigV1QuerySettings({ enableMapKeysDiscovery: e.currentTarget.checked });
                onSwitchToggle('enableMapKeysDiscovery', e.currentTarget.checked);
              }}
            />

            <Divider />
            <LogsConfig
              logsConfig={jsonData.logs}
              onDefaultDatabaseChange={(db) => {
                trackingV1.trackClickhouseConfigV1LogsConfig({ defaultDatabase: db });
                onLogsConfigChange('defaultDatabase', db);
              }}
              onDefaultTableChange={(table) => {
                trackingV1.trackClickhouseConfigV1LogsConfig({ defaultTable: table });
                onLogsConfigChange('defaultTable', table);
              }}
              onOtelEnabledChange={(v) => {
                trackingV1.trackClickhouseConfigV1LogsConfig({ otelEnabled: v });
                onLogsConfigChange('otelEnabled', v);
              }}
              onOtelVersionChange={(v) => {
                trackingV1.trackClickhouseConfigV1LogsConfig({ version: v });
                onLogsConfigChange('otelVersion', v);
              }}
              onFilterTimeColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1LogsConfig({ filterTimeColumn: c });
                onLogsConfigChange('filterTimeColumn', c);
              }}
              onTimeColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1LogsConfig({ timeColumn: c });
                onLogsConfigChange('timeColumn', c);
              }}
              onLevelColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1LogsConfig({ levelColumn: c });
                onLogsConfigChange('levelColumn', c);
              }}
              onMessageColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1LogsConfig({ messageColumn: c });
                onLogsConfigChange('messageColumn', c);
              }}
              onSelectContextColumnsChange={(c) => {
                trackingV1.trackClickhouseConfigV1LogsConfig({ selectContextColumns: c });
                onLogsConfigChange('selectContextColumns', c);
              }}
              onContextColumnsChange={(c) => {
                trackingV1.trackClickhouseConfigV1LogsConfig({ contextColumns: c });
                onLogsConfigChange('contextColumns', c);
              }}
              onShowLogLinksChange={(v) => {
                onLogsConfigChange('showLogLinks', v);
              }}
            />

            <Divider />
            <TracesConfig
              tracesConfig={jsonData.traces}
              onDefaultDatabaseChange={(db) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ defaultDatabase: db });
                onTracesConfigChange('defaultDatabase', db);
              }}
              onDefaultTableChange={(table) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ defaultTable: table });
                onTracesConfigChange('defaultTable', table);
              }}
              onOtelEnabledChange={(v) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ otelEnabled: v });
                onTracesConfigChange('otelEnabled', v);
              }}
              onOtelVersionChange={(v) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ version: v });
                onTracesConfigChange('otelVersion', v);
              }}
              onTraceIdColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ traceIdColumn: c });
                onTracesConfigChange('traceIdColumn', c);
              }}
              onSpanIdColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ spanIdColumn: c });
                onTracesConfigChange('spanIdColumn', c);
              }}
              onOperationNameColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ operationNameColumn: c });
                onTracesConfigChange('operationNameColumn', c);
              }}
              onParentSpanIdColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ parentSpanIdColumn: c });
                onTracesConfigChange('parentSpanIdColumn', c);
              }}
              onServiceNameColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ serviceNameColumn: c });
                onTracesConfigChange('serviceNameColumn', c);
              }}
              onDurationColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ durationColumn: c });
                onTracesConfigChange('durationColumn', c);
              }}
              onDurationUnitChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ durationUnit: c });
                onTracesConfigChange('durationUnit', c);
              }}
              onStartTimeColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ startTimeColumn: c });
                onTracesConfigChange('startTimeColumn', c);
              }}
              onTagsColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ tagsColumn: c });
                onTracesConfigChange('tagsColumn', c);
              }}
              onServiceTagsColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ serviceTagsColumn: c });
                onTracesConfigChange('serviceTagsColumn', c);
              }}
              onKindColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ kindColumn: c });
                onTracesConfigChange('kindColumn', c);
              }}
              onStatusCodeColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ statusCodeColumn: c });
                onTracesConfigChange('statusCodeColumn', c);
              }}
              onStatusMessageColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ statusMessageColumn: c });
                onTracesConfigChange('statusMessageColumn', c);
              }}
              onStateColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ stateColumn: c });
                onTracesConfigChange('stateColumn', c);
              }}
              onInstrumentationLibraryNameColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ instrumentationLibraryNameColumn: c });
                onTracesConfigChange('instrumentationLibraryNameColumn', c);
              }}
              onInstrumentationLibraryVersionColumnChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ instrumentationLibraryVersionColumn: c });
                onTracesConfigChange('instrumentationLibraryVersionColumn', c);
              }}
              onFlattenNestedChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ flattenNested: c });
                onTracesConfigChange('flattenNested', c);
              }}
              onEventsColumnPrefixChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ traceEventsColumnPrefix: c });
                onTracesConfigChange('traceEventsColumnPrefix', c);
              }}
              onLinksColumnPrefixChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ traceLinksColumnPrefix: c });
                onTracesConfigChange('traceLinksColumnPrefix', c);
              }}
              onShowTraceLinksChange={(v) => {
                onTracesConfigChange('showTraceLinks', v);
              }}
              onTraceTimestampTableSuffixChange={(c) => {
                trackingV1.trackClickhouseConfigV1TracesConfig({ traceTimestampTableSuffix: c });
                onTracesConfigChange('traceTimestampTableSuffix', c);
              }}
            />

            <Divider />
            <AliasTableConfig aliasTables={jsonData.aliasTables} onAliasTablesChange={onAliasTableConfigChange} />
            <Divider />
            <Field label={labels.enableRowLimit.label} description={labels.enableRowLimit.tooltip}>
              <Switch
                value={jsonData.enableRowLimit || false}
                data-testid={labels.enableRowLimit.testid}
                onChange={(e) => {
                  trackingV1.trackClickhouseConfigV1EnableRowLimitToggle({ rowLimitEnabled: e.currentTarget.checked });
                  onSwitchToggle('enableRowLimit', e.currentTarget.checked);
                }}
              />
            </Field>
            <Field
              label={labels.hideTableNameInAdhocFilters.label}
              description={labels.hideTableNameInAdhocFilters.tooltip}
            >
              <Switch
                value={jsonData.hideTableNameInAdhocFilters || false}
                data-testid={labels.hideTableNameInAdhocFilters.testid}
                onChange={(e) => {
                  onSwitchToggle('hideTableNameInAdhocFilters', e.currentTarget.checked);
                }}
              />
            </Field>
            {config.secureSocksDSProxyEnabled && versionGte(config.buildInfo.version, '10.0.0') && (
              <Field label={labels.secureSocksProxy.label} description={labels.secureSocksProxy.tooltip}>
                <Switch
                  value={jsonData.enableSecureSocksProxy || false}
                  onChange={(e) => onSwitchToggle('enableSecureSocksProxy', e.currentTarget.checked)}
                />
              </Field>
            )}
            <ConfigSubSection title="Custom Settings">
              <Alert title="" severity="info">
                <ul style={{ margin: 0, paddingLeft: '1.25em' }}>
                  <li>Enabling read-only enforcement blocks INSERT and DDL queries from Grafana.</li>
                  <li>
                    Enforced settings <strong>must NOT</strong> be marked{' '}
                    <code>CHANGEABLE_IN_READONLY</code> on the ClickHouse server — doing so would let
                    users override them and break the enforcement guarantee.
                  </li>
                  <li>
                    Use ClickHouse row policies with{' '}
                    <code>{`getSetting('custom_x')`}</code> to gate data access on enforced settings.
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
                  title={labels.customSettings.jwtInfoBanner.title}
                  severity="info"
                  data-testid={selectors.components.Config.CustomSettingsConfig.jwtInfoBanner}
                >
                  {labels.customSettings.jwtInfoBanner.message}
                </Alert>
              )}
              {customSettings.map(({ setting, value, enforced, source, headerName, onMissing, jwtHeaderName, jwtClaim, jwtClaimJoin, jwtVerify, jwtJwksUrl, jwtIssuer, jwtAudience }, i) => {
                const effectiveSource: CHCustomSettingSource = source || 'static';
                const isHeader = enforced && effectiveSource === 'header';
                const isJWT = enforced && effectiveSource === 'jwt';
                const effectiveJwtVerify: CHCustomSettingJWTVerify = jwtVerify || 'none';
                const csLabels = labels.customSettings;
                return (
                  <Stack key={i} direction="row" alignItems="flex-end" wrap>
                    <Field label={`Setting`} aria-label={`Setting`}>
                      <Input
                        value={setting}
                        placeholder={'Setting'}
                        onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                          let newSettings = customSettings.concat();
                          newSettings[i] = { setting: changeEvent.target.value, value, enforced, source, headerName, onMissing, jwtHeaderName, jwtClaim, jwtClaimJoin, jwtVerify, jwtJwksUrl, jwtIssuer, jwtAudience };
                          setCustomSettings(newSettings);
                        }}
                        onBlur={() => {
                          trackingV1.trackClickhouseConfigV1CustomSettingAdded();
                          onCustomSettingsChange(customSettings);
                        }}
                      ></Input>
                    </Field>
                    {(isHeader || isJWT) ? (
                      <Field label={'Value'} aria-label={`Value`}>
                        <Input
                          value=""
                          placeholder={isJWT ? '(from JWT claim)' : '(from header)'}
                          disabled
                        ></Input>
                      </Field>
                    ) : (
                      <Field label={'Value'} aria-label={`Value`}>
                        <Input
                          value={value}
                          placeholder={'Value'}
                          onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                            let newSettings = customSettings.concat();
                            newSettings[i] = { setting, value: changeEvent.target.value, enforced, source, headerName, onMissing };
                            setCustomSettings(newSettings);
                          }}
                          onBlur={() => {
                            onCustomSettingsChange(customSettings);
                          }}
                        ></Input>
                      </Field>
                    )}
                    <Field
                      label={
                        <Tooltip content="Send with readonly=1 so the user's SQL cannot override it.">
                          <span>Enforced</span>
                        </Tooltip>
                      }
                      aria-label={`Enforced`}
                    >
                      <Checkbox
                        value={enforced || false}
                        onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                          let newSettings = customSettings.concat();
                          newSettings[i] = { setting, value, enforced: changeEvent.target.checked, source, headerName, onMissing };
                          setCustomSettings(newSettings);
                          onCustomSettingsChange(newSettings);
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
                            const newSource = selected.value as CHCustomSettingSource;
                            let newSettings = customSettings.concat();
                            if (newSource === 'header') {
                              // switching to header: clear value and jwt fields
                              newSettings[i] = { setting, value: '', enforced, source: newSource, headerName, onMissing };
                            } else if (newSource === 'jwt') {
                              // switching to jwt: clear value and headerName
                              newSettings[i] = { setting, value: '', enforced, source: newSource, onMissing, jwtHeaderName, jwtClaim, jwtClaimJoin, jwtVerify, jwtJwksUrl, jwtIssuer, jwtAudience };
                            } else {
                              // switching to static: clear headerName, onMissing, and all jwt fields
                              newSettings[i] = { setting, value, enforced, source: newSource };
                            }
                            setCustomSettings(newSettings);
                            onCustomSettingsChange(newSettings);
                          }}
                          width={18}
                        />
                      </Field>
                    )}
                    {isHeader && (
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
                            let newSettings = customSettings.concat();
                            newSettings[i] = { setting, value: '', enforced, source: effectiveSource, headerName: changeEvent.target.value, onMissing };
                            setCustomSettings(newSettings);
                          }}
                          onBlur={() => {
                            onCustomSettingsChange(customSettings);
                          }}
                        ></Input>
                      </Field>
                    )}
                    {isHeader && (
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
                            let newSettings = customSettings.concat();
                            newSettings[i] = { setting, value: '', enforced, source: effectiveSource, headerName, onMissing: selected.value as CHCustomSettingOnMissing };
                            setCustomSettings(newSettings);
                            onCustomSettingsChange(newSettings);
                          }}
                          width={18}
                        />
                      </Field>
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
                              let newSettings = customSettings.concat();
                              newSettings[i] = { setting, value: '', enforced, source: effectiveSource, onMissing, jwtHeaderName: changeEvent.target.value, jwtClaim, jwtClaimJoin, jwtVerify, jwtJwksUrl, jwtIssuer, jwtAudience };
                              setCustomSettings(newSettings);
                            }}
                            onBlur={() => { onCustomSettingsChange(customSettings); }}
                          ></Input>
                        </Field>
                        <Field
                          label={
                            <Tooltip content={csLabels.jwtClaimPathInput.tooltip}>
                              <span>{csLabels.jwtClaimPathInput.label}</span>
                            </Tooltip>
                          }
                          aria-label={csLabels.jwtClaimPathInput.label}
                        >
                          <Input
                            value={jwtClaim || ''}
                            placeholder={csLabels.jwtClaimPathInput.placeholder}
                            data-testid={selectors.components.Config.CustomSettingsConfig.jwtClaimPathInput}
                            onChange={(changeEvent: ChangeEvent<HTMLInputElement>) => {
                              let newSettings = customSettings.concat();
                              newSettings[i] = { setting, value: '', enforced, source: effectiveSource, onMissing, jwtHeaderName, jwtClaim: changeEvent.target.value, jwtClaimJoin, jwtVerify, jwtJwksUrl, jwtIssuer, jwtAudience };
                              setCustomSettings(newSettings);
                            }}
                            onBlur={() => { onCustomSettingsChange(customSettings); }}
                          ></Input>
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
                              let newSettings = customSettings.concat();
                              newSettings[i] = { setting, value: '', enforced, source: effectiveSource, onMissing, jwtHeaderName, jwtClaim, jwtClaimJoin: changeEvent.target.value, jwtVerify, jwtJwksUrl, jwtIssuer, jwtAudience };
                              setCustomSettings(newSettings);
                            }}
                            onBlur={() => { onCustomSettingsChange(customSettings); }}
                          ></Input>
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
                              let newSettings = customSettings.concat();
                              newSettings[i] = { setting, value: '', enforced, source: effectiveSource, onMissing, jwtHeaderName, jwtClaim, jwtClaimJoin, jwtVerify: selected.value as CHCustomSettingJWTVerify, jwtJwksUrl, jwtIssuer, jwtAudience };
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
                                  let newSettings = customSettings.concat();
                                  newSettings[i] = { setting, value: '', enforced, source: effectiveSource, onMissing, jwtHeaderName, jwtClaim, jwtClaimJoin, jwtVerify, jwtJwksUrl: changeEvent.target.value, jwtIssuer, jwtAudience };
                                  setCustomSettings(newSettings);
                                }}
                                onBlur={() => { onCustomSettingsChange(customSettings); }}
                              ></Input>
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
                                  let newSettings = customSettings.concat();
                                  newSettings[i] = { setting, value: '', enforced, source: effectiveSource, onMissing, jwtHeaderName, jwtClaim, jwtClaimJoin, jwtVerify, jwtJwksUrl, jwtIssuer: changeEvent.target.value, jwtAudience };
                                  setCustomSettings(newSettings);
                                }}
                                onBlur={() => { onCustomSettingsChange(customSettings); }}
                              ></Input>
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
                                  let newSettings = customSettings.concat();
                                  newSettings[i] = { setting, value: '', enforced, source: effectiveSource, onMissing, jwtHeaderName, jwtClaim, jwtClaimJoin, jwtVerify, jwtJwksUrl, jwtIssuer, jwtAudience: changeEvent.target.value };
                                  setCustomSettings(newSettings);
                                }}
                                onBlur={() => { onCustomSettingsChange(customSettings); }}
                              ></Input>
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
                              let newSettings = customSettings.concat();
                              newSettings[i] = { setting, value: '', enforced, source: effectiveSource, onMissing: selected.value as CHCustomSettingOnMissing, jwtHeaderName, jwtClaim, jwtClaimJoin, jwtVerify, jwtJwksUrl, jwtIssuer, jwtAudience };
                              setCustomSettings(newSettings);
                              onCustomSettingsChange(newSettings);
                            }}
                            width={18}
                          />
                        </Field>
                      </>
                    )}
                  </Stack>
                );
              })}
              <Button
                variant="secondary"
                icon="plus"
                type="button"
                onClick={() => {
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
                      onSwitchToggle('enforceReadOnly', e.currentTarget.checked);
                    }
                  }}
                />
              </Tooltip>
            </Field>

          </ConfigSection>
        </>
      )}
    </>
  );
};
