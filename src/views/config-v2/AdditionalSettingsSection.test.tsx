import React from 'react';
import { render, fireEvent, screen } from '@testing-library/react';

import { AdditionalSettingsSection } from './AdditionalSettingsSection';
import { createTestProps } from './helpers';
import { CHCustomSetting, Protocol } from 'types/config';
import { selectors } from '../../selectors';
import * as tracking from './tracking';

jest.mock('./tracking');

/**
 * These tests document a v1 → v2 round-trip contract for `customSettings`
 * plus v2's own interaction contract for enforced / header-sourced /
 * JWT-sourced enforced settings.
 *
 * v2 renders a compact base row (Setting / Value / Enforced / Source) plus
 * an on-demand Advanced panel for header- and JWT-source fields. Shared
 * label strings live in src/labels.ts and shared selectors under
 * selectors.components.Config.CustomSettingsConfig.*.
 *
 * A datasource provisioned or edited in v1 with enforcement fields set must
 * survive being edited in v2:
 *
 *   1. Editing the Setting or Value cell of an enforced row in v2 must NOT
 *      drop the enforcement metadata for that row.
 *   2. `onCustomSettingsChange`'s save filter must preserve header-sourced and
 *      JWT-sourced rows (whose `value` is intentionally empty), matching the
 *      v1 filter in CHConfigEditor.tsx.
 */
describe('AdditionalSettingsSection — custom settings round-trip', () => {
  const buildProps = (customSettings: CHCustomSetting[]) => {
    const onOptionsChange = jest.fn();
    const props = createTestProps({
      options: {
        jsonData: {
          host: 'localhost',
          port: 9000,
          protocol: Protocol.Native,
          username: '',
          customSettings,
        },
        secureJsonData: {},
        secureJsonFields: {},
      },
      mocks: { onOptionsChange },
    });
    return { props, onOptionsChange };
  };

  it('preserves enforced/source/headerName/onMissing when editing Setting in v2', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'max_rows_to_read',
        value: '1000',
        enforced: true,
        source: 'static',
        onMissing: 'reject',
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    const settingInput = screen.getByDisplayValue('max_rows_to_read');
    fireEvent.change(settingInput, { target: { value: 'max_rows_to_read_v2' } });
    fireEvent.blur(settingInput);

    expect(onOptionsChange).toHaveBeenCalled();
    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toHaveLength(1);
    expect(savedCustomSettings[0]).toEqual({
      setting: 'max_rows_to_read_v2',
      value: '1000',
      enforced: true,
      source: 'static',
      onMissing: 'reject',
    });
  });

  it('preserves a header-sourced enforced row (empty value) across v2 save filter', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    const settingInput = screen.getByDisplayValue('tenant_id');
    fireEvent.change(settingInput, { target: { value: 'tenant_id_v2' } });
    fireEvent.blur(settingInput);

    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toHaveLength(1);
    expect(savedCustomSettings[0]).toEqual({
      setting: 'tenant_id_v2',
      value: '',
      enforced: true,
      source: 'header',
      headerName: 'X-Tenant-Id',
      onMissing: 'reject',
    });
  });

  it('preserves a JWT-sourced enforced row (empty value) across v2 save filter', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'user_roles',
        value: '',
        enforced: true,
        source: 'jwt',
        onMissing: 'reject',
        jwtHeaderName: 'X-Id-Token',
        jwtClaimPath: ['realm_access', 'roles'],
        jwtClaimJoin: ',',
        jwtVerify: 'jwks',
        jwtJwksUrl: 'https://idp.example.com/jwks.json',
        jwtIssuer: 'https://idp.example.com/',
        jwtAudience: 'grafana',
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    const settingInput = screen.getByDisplayValue('user_roles');
    fireEvent.change(settingInput, { target: { value: 'user_roles_v2' } });
    fireEvent.blur(settingInput);

    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toHaveLength(1);
    expect(savedCustomSettings[0]).toEqual({
      setting: 'user_roles_v2',
      value: '',
      enforced: true,
      source: 'jwt',
      onMissing: 'reject',
      jwtHeaderName: 'X-Id-Token',
      jwtClaimPath: ['realm_access', 'roles'],
      jwtClaimJoin: ',',
      jwtVerify: 'jwks',
      jwtJwksUrl: 'https://idp.example.com/jwks.json',
      jwtIssuer: 'https://idp.example.com/',
      jwtAudience: 'grafana',
    });
  });

  it('disables the Value input for dynamic-source enforced rows to prevent silent overwrites', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    // Locate the disabled Value input in the header-sourced row via its placeholder.
    const valueInput = screen.getByPlaceholderText(/\(from header\)/i) as HTMLInputElement;
    expect(valueInput.disabled).toBe(true);
  });

  it('still drops rows that are empty and non-enforced', () => {
    const initial: CHCustomSetting[] = [
      { setting: 'kept', value: 'v' },
      { setting: '', value: '' },
      { setting: 'dropped_no_value', value: '' },
    ];
    const { props, onOptionsChange } = buildProps(initial);

    render(<AdditionalSettingsSection {...props} />);

    // Trigger a save by editing the first row's value and blurring.
    const valueInput = screen.getByDisplayValue('v');
    fireEvent.change(valueInput, { target: { value: 'v2' } });
    fireEvent.blur(valueInput);

    const savedCustomSettings: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;

    expect(savedCustomSettings).toEqual([{ setting: 'kept', value: 'v2' }]);
  });

  it('shows the header-warning banner when a row has source=header', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);
    expect(screen.getByText(/require a trusted upstream proxy/i)).toBeTruthy();
  });

  it('shows the JWT info banner when a row has source=jwt', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'user_roles',
        value: '',
        enforced: true,
        source: 'jwt',
        onMissing: 'reject',
        jwtClaimPath: ['roles'],
      },
    ];
    const { props } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);
    // The JWT info-banner title comes from shared labels: "JWT-claim source".
    expect(screen.getByText(/JWT-claim source/i)).toBeTruthy();
  });

  it('auto-expands the advanced panel for a row loaded with a non-static source', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);
    // Advanced-panel HeaderName input should be present without any interaction.
    const headerNameInput = screen.getByDisplayValue('X-Tenant-Id') as HTMLInputElement;
    expect(headerNameInput).toBeTruthy();
  });

  it('auto-locks the Enforce read-only master toggle when any row is enforced', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'max_rows_to_read',
        value: '1000',
        enforced: true,
        source: 'static',
      },
    ];
    const { props } = buildProps(initial);
    const { container } = render(<AdditionalSettingsSection {...props} />);
    const label = screen.getByText('Enforce read-only on all queries');
    // Locate the Switch by walking up from the label to the enclosing Field wrapper,
    // then finding the switch input within it.
    let node: HTMLElement | null = label;
    let toggle: HTMLInputElement | null = null;
    while (node && node !== container) {
      toggle = node.querySelector('input[type="checkbox"]') as HTMLInputElement | null;
      if (toggle) {
        break;
      }
      node = node.parentElement;
    }
    expect(toggle).toBeTruthy();
    expect(toggle!.checked).toBe(true);
    expect(toggle!.disabled).toBe(true);
  });

  it('leaves the Enforce read-only master toggle editable when no row is enforced', () => {
    const initial: CHCustomSetting[] = [{ setting: 'foo', value: 'bar' }];
    const { props } = buildProps(initial);
    const { container } = render(<AdditionalSettingsSection {...props} />);
    const label = screen.getByText('Enforce read-only on all queries');
    let node: HTMLElement | null = label;
    let toggle: HTMLInputElement | null = null;
    while (node && node !== container) {
      toggle = node.querySelector('input[type="checkbox"]') as HTMLInputElement | null;
      if (toggle) {
        break;
      }
      node = node.parentElement;
    }
    expect(toggle).toBeTruthy();
    expect(toggle!.disabled).toBe(false);
  });

  // Helpers scoped to the interaction-tests block below.
  const cs = selectors.components.Config.CustomSettingsConfig;

  const findMasterEnforceReadOnlyToggle = (container: HTMLElement): HTMLInputElement => {
    const label = screen.getByText('Enforce read-only on all queries');
    let node: HTMLElement | null = label;
    while (node && node !== container) {
      const t = node.querySelector('input[type="checkbox"]') as HTMLInputElement | null;
      if (t) {
        return t;
      }
      node = node.parentElement;
    }
    throw new Error('Master Enforce read-only toggle not found');
  };

  const findRowEnforcedCheckbox = (): HTMLInputElement => {
    // The row's Enforced Checkbox carries a stable test-id from the shared
    // selectors constant. Prefer this over DOM-walking anchors that break
    // when Grafana UI internals change.
    const wrapper = screen.getByTestId(cs.enforcedCheckbox);
    // Some Grafana UI Checkbox versions render the input as the test-id
    // target; others wrap it in a <label>. Handle both.
    if (wrapper instanceof HTMLInputElement) {
      return wrapper;
    }
    const input = wrapper.querySelector('input[type="checkbox"]') as HTMLInputElement | null;
    if (!input) {
      throw new Error('Row-level Enforced checkbox input not found');
    }
    return input;
  };

  // -- source-transition + edit-persistence tests -----------------------------

  it('renders JWT-source fields for a jwt row (no jwks fields when verify=none)', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'roles',
        value: '',
        enforced: true,
        source: 'jwt',
        jwtHeaderName: 'X-Id-Token',
        jwtClaimPath: ['roles'],
        jwtVerify: 'none',
      },
    ];
    const { props } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);
    expect(screen.getByTestId(cs.jwtTokenHeaderInput)).toBeInTheDocument();
    expect(screen.getByTestId(cs.jwtClaimPathInput)).toBeInTheDocument();
    expect(screen.getByTestId(cs.jwtArrayJoinInput)).toBeInTheDocument();
    expect(screen.getByTestId(cs.jwtVerifySelect)).toBeInTheDocument();
    expect(screen.queryByTestId(cs.jwtJwksUrlInput)).not.toBeInTheDocument();
    expect(screen.queryByTestId(cs.jwtIssuerInput)).not.toBeInTheDocument();
    expect(screen.queryByTestId(cs.jwtAudienceInput)).not.toBeInTheDocument();
    // Header-specific fields must NOT be present on a jwt row.
    expect(screen.queryByTestId(cs.headerNameInput)).not.toBeInTheDocument();
  });

  it('renders JWKS URL/Issuer/Audience when verify=jwks', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'roles',
        value: '',
        enforced: true,
        source: 'jwt',
        jwtClaimPath: ['roles'],
        jwtVerify: 'jwks',
        jwtJwksUrl: 'https://idp.example/jwks.json',
        jwtIssuer: 'https://idp.example/',
        jwtAudience: 'grafana',
      },
    ];
    const { props } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);
    expect(screen.getByTestId(cs.jwtJwksUrlInput)).toHaveValue('https://idp.example/jwks.json');
    expect(screen.getByTestId(cs.jwtIssuerInput)).toHaveValue('https://idp.example/');
    expect(screen.getByTestId(cs.jwtAudienceInput)).toHaveValue('grafana');
  });

  it('does not render header/JWT fields on a static-source enforced row', () => {
    const initial: CHCustomSetting[] = [
      { setting: 'max_rows_to_read', value: '1000', enforced: true, source: 'static' },
    ];
    const { props } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);
    expect(screen.queryByTestId(cs.headerNameInput)).not.toBeInTheDocument();
    expect(screen.queryByTestId(cs.jwtTokenHeaderInput)).not.toBeInTheDocument();
    expect(screen.queryByTestId(cs.jwtVerifySelect)).not.toBeInTheDocument();
    // Editable Value input must be present for a static row.
    expect(screen.getByDisplayValue('1000')).toBeInTheDocument();
  });

  it('persists HeaderName edits on blur', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);

    const headerNameInput = screen.getByTestId(cs.headerNameInput) as HTMLInputElement;
    fireEvent.change(headerNameInput, { target: { value: 'X-New-Tenant-Id' } });
    fireEvent.blur(headerNameInput);

    const saved: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;
    expect(saved).toHaveLength(1);
    expect(saved[0]).toMatchObject({
      setting: 'tenant_id',
      value: '',
      enforced: true,
      source: 'header',
      headerName: 'X-New-Tenant-Id',
      onMissing: 'reject',
    });
  });

  it('persists JWT claim path as a single-element array on blur', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'roles',
        value: '',
        enforced: true,
        source: 'jwt',
        jwtHeaderName: 'X-Id-Token',
        jwtClaimPath: ['roles'],
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);

    const claimInput = screen.getByTestId(cs.jwtClaimPathInput) as HTMLInputElement;
    fireEvent.change(claimInput, { target: { value: 'tenants' } });
    fireEvent.blur(claimInput);

    const saved: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;
    expect(saved[0].jwtClaimPath).toEqual(['tenants']);
  });

  it('persists JWKS URL edits on blur', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'roles',
        value: '',
        enforced: true,
        source: 'jwt',
        jwtClaimPath: ['roles'],
        jwtVerify: 'jwks',
        jwtJwksUrl: 'https://old.example/jwks.json',
      },
    ];
    const { props, onOptionsChange } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);

    const jwksInput = screen.getByTestId(cs.jwtJwksUrlInput) as HTMLInputElement;
    fireEvent.change(jwksInput, { target: { value: 'https://new.example/jwks.json' } });
    fireEvent.blur(jwksInput);

    const saved: CHCustomSetting[] =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData.customSettings;
    expect(saved[0].jwtJwksUrl).toBe('https://new.example/jwks.json');
    // jwtVerify must be preserved across the edit.
    expect(saved[0].jwtVerify).toBe('jwks');
  });

  // -- Enforced-checkbox reveal ------------------------------------------------

  it('toggling Enforced on an unenforced row reveals the Source select', () => {
    const initial: CHCustomSetting[] = [{ setting: 'foo', value: 'bar' }];
    const { props, onOptionsChange } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);

    expect(screen.queryByTestId(cs.sourceSelect)).not.toBeInTheDocument();

    onOptionsChange.mockClear();
    fireEvent.click(findRowEnforcedCheckbox());

    // Now the source select is visible, and the row was persisted as enforced.
    expect(screen.getByTestId(cs.sourceSelect)).toBeInTheDocument();
    // Find the call that mutated customSettings with an enforced row.
    const enforcedCall = onOptionsChange.mock.calls.find(
      (c: unknown[]) => (c[0] as { jsonData: { customSettings?: CHCustomSetting[] } }).jsonData.customSettings?.[0]?.enforced === true
    );
    expect(enforcedCall).toBeTruthy();
  });

  // -- Expand/collapse IconButton ---------------------------------------------

  it('advanced panel can be collapsed via the toggle IconButton', () => {
    const initial: CHCustomSetting[] = [
      {
        setting: 'tenant_id',
        value: '',
        enforced: true,
        source: 'header',
        headerName: 'X-Tenant-Id',
        onMissing: 'reject',
      },
    ];
    const { props } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);

    // Panel starts expanded (auto-expand rule).
    expect(screen.getByTestId(cs.headerNameInput)).toBeInTheDocument();

    const toggleBtn = screen.getByLabelText(/Hide advanced settings/i);
    fireEvent.click(toggleBtn);

    // After collapse, the HeaderName input should no longer be rendered.
    expect(screen.queryByTestId(cs.headerNameInput)).not.toBeInTheDocument();

    // And the button label flips.
    expect(screen.getByLabelText(/Show advanced settings/i)).toBeInTheDocument();
  });

  // -- Tracking events --------------------------------------------------------

  it('fires v2 tracking on Enforced toggle', () => {
    const initial: CHCustomSetting[] = [{ setting: 'foo', value: 'bar' }];
    const { props } = buildProps(initial);
    render(<AdditionalSettingsSection {...props} />);

    fireEvent.click(findRowEnforcedCheckbox());

    expect(tracking.trackClickhouseConfigV2CustomSettingEnforcedToggle as jest.Mock).toHaveBeenCalledWith({
      enforced: true,
    });
  });

  // -- Master Enforce read-only persistence -----------------------------------

  it('toggling the master Enforce read-only Switch persists enforceReadOnly', () => {
    const initial: CHCustomSetting[] = [{ setting: 'foo', value: 'bar' }];
    const { props, onOptionsChange } = buildProps(initial);
    const { container } = render(<AdditionalSettingsSection {...props} />);

    const toggle = findMasterEnforceReadOnlyToggle(container);
    expect(toggle.disabled).toBe(false);
    expect(toggle.checked).toBe(false);
    fireEvent.click(toggle);

    const savedJsonData =
      onOptionsChange.mock.calls[onOptionsChange.mock.calls.length - 1][0].jsonData;
    expect(savedJsonData.enforceReadOnly).toBe(true);
  });

  it('fires v2 tracking on master Enforce read-only toggle', () => {
    const initial: CHCustomSetting[] = [{ setting: 'foo', value: 'bar' }];
    const { props } = buildProps(initial);
    const { container } = render(<AdditionalSettingsSection {...props} />);

    const toggle = findMasterEnforceReadOnlyToggle(container);
    fireEvent.click(toggle);

    expect(tracking.trackClickhouseConfigV2EnforceReadOnlyToggle as jest.Mock).toHaveBeenCalledWith({
      enforceReadOnly: true,
    });
  });
});
